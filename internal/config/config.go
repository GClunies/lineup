// Package config loads lineup's layered TOML configuration and resolves it for
// one Repo and one Task. Parsing and validation happen here, once, at the boundary.
package config

import (
	"cmp"
	"encoding"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

const (
	defaultWIPCap       = 5
	defaultPollInterval = 30 * time.Second
	defaultModel        = "claude-opus-5-5"
	defaultEffort       = "high"
	repoFileName        = ".lineup.toml"
	defaultAdHocSession = "ad hoc"
	defaultReviewCycles = 3
)

// trackerEvents are the lifecycle events that a Tracker map can name.
var trackerEvents = map[string]bool{"started": true, "in_review": true, "merged": true, "closed": true}

type Resolved struct {
	WIPCap int
	// PR is false for a Task whose work ends without a pull request.
	PR           bool
	PollInterval time.Duration
	Budgets      lifecycle.Budgets
	// Bar is the Landing bar. Its Bot is nil when no [review_bot] table
	// applies to the Repo.
	Bar    lifecycle.LandingBar
	Stages map[lifecycle.Stage]StageConfig
	// Claude is the absolute path of the claude binary.
	Claude string
}

type StageConfig struct{ Model, Effort, Skill, Command string }

func (r Resolved) Policy() lifecycle.Policy {
	return lifecycle.Policy{Budgets: r.Budgets, Bar: r.Bar}
}

type budgetsFile struct {
	CIDebug    *int `toml:"ci_debug"`
	Responding *int `toml:"responding"`
}

// reviewBotFile is a [review_bot] table. A layer can leave out a key and
// take it from a lower layer, as in [budgets].
type reviewBotFile struct {
	Name            *string `toml:"name"`
	MinReviewScore  *int    `toml:"min_review_score"`
	MaxReviewCycles *int    `toml:"max_review_cycles"`
}

// landingBarFile is a [landing_bar] table. A layer can leave out a key and
// take it from a lower layer, as in [budgets].
type landingBarFile struct {
	Checks    *string `toml:"checks"`
	Threads   *string `toml:"threads"`
	Approvals *string `toml:"approvals"`
}

// defaultBar is the user's bar.
var defaultBar = lifecycle.LandingBar{
	Checks: lifecycle.RequiredChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals,
}

// apply sets each rule that the table names. The error names the key.
func (f *landingBarFile) apply(b *lifecycle.LandingBar) error {
	for _, k := range []struct {
		key   string
		value *string
		rule  encoding.TextUnmarshaler
	}{{"checks", f.Checks, &b.Checks}, {"threads", f.Threads, &b.Threads}, {"approvals", f.Approvals, &b.Approvals}} {
		if k.value == nil {
			continue
		}
		if err := k.rule.UnmarshalText([]byte(*k.value)); err != nil {
			return fmt.Errorf("landing_bar.%s: %w", k.key, err)
		}
	}
	return nil
}

type stageFile struct {
	Model   *string `toml:"model"`
	Effort  *string `toml:"effort"`
	Skill   *string `toml:"skill"`
	Command *string `toml:"command"`
}

// layer holds the keys that the global file, a [[repos]] entry, and .lineup.toml share.
type layer struct {
	WIPCap       *int                 `toml:"wip_cap"`
	PollInterval *string              `toml:"poll_interval"`
	Budgets      *budgetsFile         `toml:"budgets"`
	ReviewBot    *reviewBotFile       `toml:"review_bot"`
	LandingBar   *landingBarFile      `toml:"landing_bar"`
	Stage        map[string]stageFile `toml:"stage"`
}

type repoEntry struct {
	layer
	Match string   `toml:"match"`
	Teams []string `toml:"teams"`
}

type linearWorkspace struct {
	ClientID string                       `toml:"client_id"`
	Status   map[string]map[string]string `toml:"status"`
}

type globalFile struct {
	layer
	StartSkills    []string                   `toml:"start_skills"`
	LinearClientID string                     `toml:"linear_client_id"`
	AdHocSession   string                     `toml:"ad_hoc_session"`
	Repos          []repoEntry                `toml:"repos"`
	Linear         map[string]linearWorkspace `toml:"linear"`
	GitHub         struct {
		Labels map[string]string `toml:"labels"`
	} `toml:"github"`
	LocalTracker   bool   `toml:"local_tracker"`
	LocalIssuesDir string `toml:"local_issues_dir"`
	Review         struct {
		Views  []ReviewView `toml:"views"`
		Slots  *int         `toml:"slots"`
		Queues []queueFile  `toml:"queues"`
	} `toml:"review"`
}

// PRFilter picks pull requests. Its keys are named after GitHub's search
// qualifiers. The items of one list combine with OR, and two keys combine
// with AND. Author and AuthorTeam are one list. State is open, closed,
// merged, or all, and "" is open. Draft nil matches drafts and other PRs.
// IncludeRegex and ExcludeRegex are RE2 patterns on the title: a PR must
// match IncludeRegex when it is set, and must not match ExcludeRegex.
type PRFilter struct {
	State           string   `toml:"state"`
	Draft           *bool    `toml:"draft"`
	Repo            []string `toml:"repo"`
	Author          []string `toml:"author"`
	AuthorTeam      []string `toml:"author_team"`
	ReviewRequested string   `toml:"review_requested"`
	Label           []string `toml:"label"`
	IncludeRegex    string   `toml:"include_regex"`
	ExcludeRegex    string   `toml:"exclude_regex"`
}

// ReviewView is a tab of the review tab.
type ReviewView struct {
	Name string `toml:"name"`
	PRFilter
}

// ReviewQueue is a view whose PRs lineup reviews without being asked. Slots
// is how many of its review Tasks can be started at one time.
type ReviewQueue struct {
	Name string
	PRFilter
	Slots int
}

type queueFile struct {
	Name string `toml:"name"`
	PRFilter
	Slots *int `toml:"slots"`
}

const defaultSlots = 3

type Global struct {
	file     globalFile
	home     string
	lookPath func(string) (string, error)
}

// StartSkills lists the skills that the Linear browser offers when it starts a Task.
func (g *Global) StartSkills() []string { return g.file.StartSkills }

// AdHocSession is the Rex session for Tasks with no Issue.
func (g *Global) AdHocSession() string {
	if s := strings.TrimSpace(g.file.AdHocSession); s != "" {
		return s
	}
	return defaultAdHocSession
}

// LinearClientID is the Linear OAuth app to sign in to a workspace with: the
// workspace's own client_id, else linear_client_id, else "" for lineup's.
func (g *Global) LinearClientID(workspace string) string {
	if id := g.file.Linear[workspace].ClientID; id != "" {
		return id
	}
	return g.file.LinearClientID
}

// GitHubLabels maps a lifecycle event to the label it sets on a GitHub Issue.
func (g *Global) GitHubLabels() map[string]string { return g.file.GitHub.Labels }

// ReviewViews are the [[review.views]] entries, in config order.
func (g *Global) ReviewViews() []ReviewView { return g.file.Review.Views }

// ReviewSlots is how many review Tasks of all queues together can be started
// at one time, or 0 when review.slots is not set.
func (g *Global) ReviewSlots() int {
	if g.file.Review.Slots == nil {
		return 0
	}
	return *g.file.Review.Slots
}

// ReviewQueues are the [[review.queues]] entries, in config order. A queue
// with no slots of its own gets review.slots, else 3.
func (g *Global) ReviewQueues() []ReviewQueue {
	var out []ReviewQueue
	for _, q := range g.file.Review.Queues {
		slots := cmp.Or(g.ReviewSlots(), defaultSlots)
		if q.Slots != nil {
			slots = *q.Slots
		}
		out = append(out, ReviewQueue{Name: q.Name, PRFilter: q.PRFilter, Slots: slots})
	}
	return out
}

// LocalIssuesDir is the Local Tracker's folder.
func (g *Global) LocalIssuesDir() string {
	if d := strings.TrimSpace(g.file.LocalIssuesDir); d != "" {
		return g.expand(d)
	}
	return filepath.Join(g.home, ".local", "share", "lineup", "issues")
}

// LocalTrackerOn says whether the Local Tracker is on: when no
// other Tracker is on, or when local_tracker asks for it.
func (g *Global) LocalTrackerOn(linearOn, githubOn bool) bool {
	return g.file.LocalTracker || (!linearOn && !githubOn)
}

// Load reads the global file. A missing file means defaults.
func Load(globalPath string) (*Global, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home folder: %w", err)
	}
	g := &Global{home: home, lookPath: exec.LookPath}
	data, err := os.ReadFile(globalPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return g, nil
	case err != nil:
		return nil, err
	}
	if err := decodeStrict(string(data), &g.file); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	if err := g.validate(&g.file.layer, "global"); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	for i, sk := range g.file.StartSkills {
		if strings.TrimSpace(sk) == "" {
			return nil, fmt.Errorf("%s: start_skills[%d] is empty", globalPath, i)
		}
	}
	for i, r := range g.file.Repos {
		if r.Match == "" {
			return nil, fmt.Errorf("%s: repos[%d]: match is required", globalPath, i)
		}
		if _, err := filepath.Match(g.expand(r.Match), ""); err != nil {
			return nil, fmt.Errorf("%s: repos[%d]: bad match %q: %w", globalPath, i, r.Match, err)
		}
		if err := g.validate(&r.layer, fmt.Sprintf("repos[%d]", i)); err != nil {
			return nil, fmt.Errorf("%s: %w", globalPath, err)
		}
	}
	for ws, w := range g.file.Linear {
		for team, events := range w.Status {
			for ev := range events {
				if !trackerEvents[ev] {
					return nil, fmt.Errorf("%s: linear.%s.status.%s: unknown event %q", globalPath, ws, team, ev)
				}
			}
		}
	}
	for ev := range g.file.GitHub.Labels {
		if !trackerEvents[ev] {
			return nil, fmt.Errorf("%s: github.labels: unknown event %q", globalPath, ev)
		}
	}
	if err := validateReview(g.file.Review.Views, g.file.Review.Queues, g.file.Review.Slots); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	return g, nil
}

// reservedViews are the built-in tabs of the review tab.
var reservedViews = []string{"My PRs", "Requested"}

func validateReview(views []ReviewView, queues []queueFile, slots *int) error {
	if slots != nil && *slots < 1 {
		return errors.New("review.slots: slots must be at least 1")
	}
	names := map[string]bool{}
	check := func(where, name string, f PRFilter, queue bool) error {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return fmt.Errorf("%s: name is required", where)
		case names[name] || slices.Contains(reservedViews, name):
			return fmt.Errorf("%s: name %q is used twice", where, name)
		}
		names[name] = true
		return f.validate(where, queue)
	}
	for i, v := range views {
		if err := check(fmt.Sprintf("review.views[%d]", i), v.Name, v.PRFilter, false); err != nil {
			return err
		}
	}
	for i, q := range queues {
		where := fmt.Sprintf("review.queues[%d]", i)
		if err := check(where, q.Name, q.PRFilter, true); err != nil {
			return err
		}
		if q.Slots != nil && *q.Slots < 1 {
			return fmt.Errorf("%s: slots must be at least 1", where)
		}
	}
	return nil
}

// prStates are the values of state, as GitHub's is: qualifier names them.
var prStates = []string{"open", "closed", "merged", "all"}

// A queue reviews PRs, so it lists only open PRs that are not drafts.
func (f PRFilter) validate(where string, queue bool) error {
	switch {
	case f.State != "" && !slices.Contains(prStates, f.State):
		return fmt.Errorf("%s: state %q must be one of %s", where, f.State, strings.Join(prStates, ", "))
	case queue && f.State != "" && f.State != "open":
		return fmt.Errorf("%s: a queue reviews open PRs, so state must be open", where)
	case queue && f.Draft != nil && *f.Draft:
		return fmt.Errorf("%s: a queue never reviews drafts, so draft must be false", where)
	case f.ReviewRequested != "" && !isWord(f.ReviewRequested):
		return fmt.Errorf("%s: review_requested %q must be @me or a login", where, f.ReviewRequested)
	}
	lists := []struct {
		key   string
		items []string
		want  string
		good  func(string) bool
	}{
		{"repo", f.Repo, "owner/name", isPair},
		{"author", f.Author, "a login", isWord},
		{"author_team", f.AuthorTeam, "org/slug", isPair},
		// A label travels in double quotes, so it can hold spaces but no quote.
		{"label", f.Label, "a label with no double quote", func(s string) bool {
			return strings.TrimSpace(s) != "" && !strings.Contains(s, `"`)
		}},
	}
	empty := f.ReviewRequested == ""
	for _, l := range lists {
		for j, it := range l.items {
			empty = false
			if !l.good(it) {
				return fmt.Errorf("%s: %s[%d] %q must be %s", where, l.key, j, it, l.want)
			}
		}
	}
	if empty {
		return fmt.Errorf("%s: needs at least one of repo, author, author_team, review_requested, label", where)
	}
	for key, re := range map[string]string{"include_regex": f.IncludeRegex, "exclude_regex": f.ExcludeRegex} {
		if _, err := regexp.Compile(re); err != nil {
			return fmt.Errorf("%s: %s: %w", where, key, err)
		}
	}
	return nil
}

func isWord(s string) bool { return s != "" && !strings.ContainsAny(s, " \t\"") }

// isPair is an "owner/name" or "org/slug" form.
func isPair(s string) bool {
	a, b, ok := strings.Cut(s, "/")
	return ok && isWord(a) && isWord(b) && !strings.Contains(b, "/")
}

var errHarnessGone = errors.New("harness is no longer a setting, lineup runs claude")

// renamedFilterKeys are the review filter keys before they took GitHub's
// qualifier names, and what replaces each.
var renamedFilterKeys = map[string]string{
	"repos":           "repo",
	"authors":         "author",
	"author_teams":    "author_team",
	"labels":          "label",
	"exclude_titles":  `exclude_regex, one RE2 pattern, for example 'HLESS-|WIP'`,
	"requested_teams": `review_requested = "@me", which matches a request to you or to any of your teams`,
}

const restartsHint = ". A lost Worker no longer restarts by itself, so delete the restarts line from the [budgets] table"

func decodeStrict(text string, v any) error {
	md, err := toml.Decode(text, v)
	if err != nil {
		return err
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		for _, k := range keys {
			if k[0] == "harness" || k[len(k)-1] == "harness" {
				return errHarnessGone
			}
		}
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = k.String()
		}
		msg := "unknown keys: " + strings.Join(names, ", ")
		if slices.Contains(names, "budgets.restarts") {
			msg += restartsHint
		}
		for _, k := range keys {
			if len(k) == 3 && k[0] == "review" {
				if hint, ok := renamedFilterKeys[k[2]]; ok {
					msg += fmt.Sprintf(". Rename %s to %s", k[2], hint)
				}
			}
		}
		return errors.New(msg)
	}
	return nil
}

func (g *Global) validate(l *layer, where string) error {
	if l.WIPCap != nil && *l.WIPCap < 1 {
		return fmt.Errorf("%s: wip_cap must be at least 1", where)
	}
	if l.PollInterval != nil {
		if _, err := parseInterval(*l.PollInterval); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}
	if b := l.Budgets; b != nil {
		for name, v := range map[string]*int{"ci_debug": b.CIDebug, "responding": b.Responding} {
			if v != nil && *v < 0 {
				return fmt.Errorf("%s: budgets.%s must not be negative", where, name)
			}
		}
	}
	if l.ReviewBot != nil {
		if err := l.ReviewBot.check(where); err != nil {
			return err
		}
	}
	if l.LandingBar != nil {
		if err := l.LandingBar.apply(&lifecycle.LandingBar{}); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}
	for key := range l.Stage {
		if key == "default" {
			continue
		}
		st, err := lifecycle.ParseStage(key)
		if err != nil || !st.Agent() {
			return fmt.Errorf("%s: stage.%s is not an agent Stage or default", where, key)
		}
		if st.StartsWorker() {
			continue
		}
		field := ""
		switch s := l.Stage[key]; {
		case s.Model != nil:
			field = "model"
		case s.Effort != nil:
			field = "effort"
		}
		if field != "" {
			return fmt.Errorf("%s: stage.%s.%s: a Task keeps the %s of the Stage its Worker starts in", where, key, field, field)
		}
	}
	return nil
}

// check validates the keys that one table sets. The bot's top score bounds
// min_review_score only when the table names the bot. gate checks the merge.
func (f *reviewBotFile) check(where string) error {
	top := 0
	if f.Name != nil {
		bot, ok := github.ReviewBots[*f.Name]
		if !ok {
			return fmt.Errorf("%s: review_bot.name %q is not a known review bot: %s", where, *f.Name, strings.Join(slices.Sorted(maps.Keys(github.ReviewBots)), ", "))
		}
		top = bot.TopScore
	}
	if m := f.MinReviewScore; m != nil && (*m < 1 || top > 0 && *m > top) {
		if top > 0 {
			return fmt.Errorf("%s: review_bot.min_review_score must be from 1 to %d", where, top)
		}
		return fmt.Errorf("%s: review_bot.min_review_score must be at least 1", where)
	}
	if c := f.MaxReviewCycles; c != nil && *c < 0 {
		return fmt.Errorf("%s: review_bot.max_review_cycles must not be negative", where)
	}
	return nil
}

// gate resolves the merged [review_bot] tables into the gate and its cycle
// budget. No table means no gate.
func (f reviewBotFile) gate() (*lifecycle.ReviewGate, int, error) {
	if f == (reviewBotFile{}) {
		return nil, 0, nil
	}
	if f.Name == nil {
		return nil, 0, errors.New("review_bot: name is required")
	}
	if err := f.check("review_bot"); err != nil {
		return nil, 0, err
	}
	bot := github.ReviewBots[*f.Name]
	g := &lifecycle.ReviewGate{Bot: *f.Name, Title: bot.Title, MinScore: bot.TopScore, TopScore: bot.TopScore}
	if f.MinReviewScore != nil {
		g.MinScore = *f.MinReviewScore
	}
	cycles := defaultReviewCycles
	if f.MaxReviewCycles != nil {
		cycles = *f.MaxReviewCycles
	}
	return g, cycles, nil
}

func parseInterval(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("poll_interval %q must be a positive duration such as 30s", s)
	}
	return d, nil
}

func (g *Global) expand(p string) string {
	if p == "~" {
		return g.home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(g.home, rest)
	}
	return p
}

func (g *Global) matches(r repoEntry, repoPath string) bool {
	ok, _ := filepath.Match(g.expand(r.Match), repoPath)
	return ok
}

// specificity counts the characters in a glob that are not wildcards.
func specificity(pattern string) int {
	return len(pattern) - strings.Count(pattern, "*") - strings.Count(pattern, "?") - strings.Count(pattern, "[")
}

// Resolve applies the layers for one Repo and the Task overrides, lowest first.
func (g *Global) Resolve(repoPath string, overrides map[string]string) (Resolved, error) {
	repoPath = filepath.Clean(g.expand(repoPath))

	var matched []repoEntry
	for _, r := range g.file.Repos {
		if g.matches(r, repoPath) {
			matched = append(matched, r)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return specificity(g.expand(matched[i].Match)) < specificity(g.expand(matched[j].Match))
	})

	var repoFile layer
	data, err := os.ReadFile(filepath.Join(repoPath, repoFileName))
	switch {
	case err == nil:
		if err := decodeStrict(string(data), &repoFile); err != nil {
			return Resolved{}, fmt.Errorf("%s: %w", filepath.Join(repoPath, repoFileName), err)
		}
		if err := g.validate(&repoFile, repoFileName); err != nil {
			return Resolved{}, fmt.Errorf("%s: %w", filepath.Join(repoPath, repoFileName), err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return Resolved{}, err
	}

	layers := []layer{g.file.layer}
	res := Resolved{
		WIPCap:       defaultWIPCap,
		PR:           true,
		PollInterval: defaultPollInterval,
		Budgets:      lifecycle.Budgets{CIDebug: 3, Responding: 3},
	}
	for _, r := range matched {
		layers = append(layers, r.layer)
	}
	layers = append(layers, repoFile)

	var bot reviewBotFile
	res.Bar = defaultBar
	for _, l := range layers {
		if l.WIPCap != nil {
			res.WIPCap = *l.WIPCap
		}
		if f := l.LandingBar; f != nil {
			if err := f.apply(&res.Bar); err != nil {
				return Resolved{}, err
			}
		}
		if f := l.ReviewBot; f != nil {
			bot.Name = cmp.Or(f.Name, bot.Name)
			bot.MinReviewScore = cmp.Or(f.MinReviewScore, bot.MinReviewScore)
			bot.MaxReviewCycles = cmp.Or(f.MaxReviewCycles, bot.MaxReviewCycles)
		}
		if l.PollInterval != nil {
			res.PollInterval, _ = parseInterval(*l.PollInterval)
		}
		if b := l.Budgets; b != nil {
			if b.CIDebug != nil {
				res.Budgets.CIDebug = *b.CIDebug
			}
			if b.Responding != nil {
				res.Budgets.Responding = *b.Responding
			}
		}
	}

	if res.Bar.Bot, res.Budgets.ReviewCycles, err = bot.gate(); err != nil {
		return Resolved{}, err
	}

	base := StageConfig{Model: defaultModel, Effort: defaultEffort}
	for _, l := range layers {
		if s, ok := l.Stage["default"]; ok {
			apply(&base, s)
		}
	}
	res.Stages = map[lifecycle.Stage]StageConfig{}
	for _, st := range lifecycle.Stages() {
		if !st.Agent() {
			continue
		}
		sc := base
		for _, l := range layers {
			if s, ok := l.Stage[st.Key()]; ok {
				apply(&sc, s)
			}
		}
		res.Stages[st] = sc
	}

	if err := applyOverrides(&res, overrides); err != nil {
		return Resolved{}, err
	}

	if res.Claude, err = g.claudePath(); err != nil {
		return Resolved{}, err
	}
	return res, nil
}

func apply(sc *StageConfig, s stageFile) {
	for dst, src := range map[*string]*string{&sc.Model: s.Model, &sc.Effort: s.Effort, &sc.Skill: s.Skill, &sc.Command: s.Command} {
		if src != nil {
			*dst = *src
		}
	}
}

func applyOverrides(res *Resolved, overrides map[string]string) error {
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := overrides[k]
		switch k {
		case "pr":
			if v != "true" && v != "false" {
				return fmt.Errorf("override pr must be true or false, got %q", v)
			}
			res.PR = v == "true"
		case "harness":
			return errHarnessGone
		case "model", "effort", "skill", "command":
			for st, sc := range res.Stages {
				switch k {
				case "model":
					sc.Model = v
				case "effort":
					sc.Effort = v
				case "skill":
					sc.Skill = v
				case "command":
					sc.Command = v
				}
				res.Stages[st] = sc
			}
		default:
			return fmt.Errorf("unknown override %q", k)
		}
	}
	return nil
}

// claudePath never asks a login shell: on this machine the login shell finds a broken codex.
func (g *Global) claudePath() (string, error) {
	found, err := g.lookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude is not on PATH: %w", err)
	}
	p, err := filepath.Abs(found)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	return p, nil
}

// LinearStatus returns the status that the team's map names for an event.
func (g *Global) LinearStatus(workspace, team, event string) (string, bool) {
	s, ok := g.file.Linear[workspace].Status[team][event]
	return s, ok
}

// RepoForTeam returns the Repo that a [[repos]] entry maps the team to. Only an
// entry whose match is a plain path can name a Repo.
func (g *Global) RepoForTeam(teamKey string) (string, bool) {
	for _, r := range g.file.Repos {
		if strings.ContainsAny(r.Match, "*?[") {
			continue
		}
		for _, t := range r.Teams {
			if t == teamKey {
				return filepath.Clean(g.expand(r.Match)), true
			}
		}
	}
	return "", false
}

// RepoPaths lists the [[repos]] entries whose match is a plain path.
func (g *Global) RepoPaths() []string {
	var out []string
	for _, r := range g.file.Repos {
		if !strings.ContainsAny(r.Match, "*?[") {
			out = append(out, filepath.Clean(g.expand(r.Match)))
		}
	}
	return out
}

// Checkouts lists the git checkouts that a [[repos]] match names, as a plain
// path or as a glob such as ~/code/*/*. These are the Repos that lineup can
// find by folder name.
func (g *Global) Checkouts() []string {
	var out []string
	for _, r := range g.file.Repos {
		dirs, _ := filepath.Glob(filepath.Clean(g.expand(r.Match)))
		for _, d := range dirs {
			if _, err := os.Stat(filepath.Join(d, ".git")); err == nil && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}
