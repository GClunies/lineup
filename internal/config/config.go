// Package config loads lineup's layered TOML configuration and resolves it for
// one Repo and one Task. Parsing and validation happen here, once, at the boundary.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gclunies/lineup/internal/lifecycle"
)

const (
	defaultWIPCap       = 5
	defaultPollInterval = 30 * time.Second
	defaultModel        = "claude-opus-5-5"
	defaultEffort       = "high"
	defaultHarness      = "claude"
	repoFileName        = ".lineup.toml"
	defaultAdHocSession = "ad hoc"
)

// trackerEvents are the lifecycle events that a Tracker map can name.
var trackerEvents = map[string]bool{"started": true, "in_review": true, "merged": true, "closed": true}

type Resolved struct {
	WIPCap int
	// PR is false for a Task whose work ends without a pull request.
	PR           bool
	PollInterval time.Duration
	Budgets      lifecycle.Budgets
	Stages       map[lifecycle.Stage]StageConfig
	// Harnesses maps each Harness name in use to its absolute path.
	Harnesses map[string]string
}

type StageConfig struct{ Harness, Model, Effort, Skill, Command string }

func (r Resolved) Policy() lifecycle.Policy {
	return lifecycle.Policy{Budgets: r.Budgets}
}

type budgetsFile struct {
	CIDebug    *int `toml:"ci_debug"`
	Responding *int `toml:"responding"`
	Restarts   *int `toml:"restarts"`
}

type stageFile struct {
	Harness *string `toml:"harness"`
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
	Stage        map[string]stageFile `toml:"stage"`
}

type repoEntry struct {
	layer
	Match string   `toml:"match"`
	Teams []string `toml:"teams"`
}

type harnessFile struct {
	Path string `toml:"path"`
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
	Harness        map[string]harnessFile     `toml:"harness"`
	Linear         map[string]linearWorkspace `toml:"linear"`
	GitHub         struct {
		Labels map[string]string `toml:"labels"`
	} `toml:"github"`
	LocalTracker   bool   `toml:"local_tracker"`
	LocalIssuesDir string `toml:"local_issues_dir"`
	AssistDir      string `toml:"assist_dir"`
	Review         struct {
		Views  []ReviewView `toml:"views"`
		Queues []queueFile  `toml:"queues"`
	} `toml:"review"`
}

// PRFilter picks pull requests. The items of one list combine with OR, and
// two lists combine with AND. Authors and AuthorTeams are one list.
type PRFilter struct {
	Repos          []string `toml:"repos"`
	Authors        []string `toml:"authors"`
	AuthorTeams    []string `toml:"author_teams"`
	RequestedTeams []string `toml:"requested_teams"`
	Labels         []string `toml:"labels"`
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
	harness  map[string]string
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

// ReviewQueues are the [[review.queues]] entries, in config order.
func (g *Global) ReviewQueues() []ReviewQueue {
	var out []ReviewQueue
	for _, q := range g.file.Review.Queues {
		slots := defaultSlots
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

// AssistDir is the folder where the Hub's assist agent starts. The default is the home folder.
func (g *Global) AssistDir() string {
	if d := strings.TrimSpace(g.file.AssistDir); d != "" {
		return g.expand(d)
	}
	return g.home
}

// LocalTrackerOn says whether the browser shows the Local Tracker: when no
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
	g := &Global{home: home, harness: map[string]string{}, lookPath: exec.LookPath}
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
	for name, h := range g.file.Harness {
		p := g.expand(h.Path)
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("%s: harness.%s.path must be absolute, got %q", globalPath, name, h.Path)
		}
		g.harness[name] = p
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
	if err := validateReview(g.file.Review.Views, g.file.Review.Queues); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	return g, nil
}

// reservedViews are the built-in tabs of the review tab.
var reservedViews = []string{"My PRs", "Requested"}

func validateReview(views []ReviewView, queues []queueFile) error {
	names := map[string]bool{}
	check := func(where, name string, f PRFilter) error {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return fmt.Errorf("%s: name is required", where)
		case names[name] || slices.Contains(reservedViews, name):
			return fmt.Errorf("%s: name %q is used twice", where, name)
		}
		names[name] = true
		return f.validate(where)
	}
	for i, v := range views {
		if err := check(fmt.Sprintf("review.views[%d]", i), v.Name, v.PRFilter); err != nil {
			return err
		}
	}
	for i, q := range queues {
		where := fmt.Sprintf("review.queues[%d]", i)
		if err := check(where, q.Name, q.PRFilter); err != nil {
			return err
		}
		if q.Slots != nil && *q.Slots < 1 {
			return fmt.Errorf("%s: slots must be at least 1", where)
		}
	}
	return nil
}

func (f PRFilter) validate(where string) error {
	lists := []struct {
		key   string
		items []string
		want  string
		good  func(string) bool
	}{
		{"repos", f.Repos, "owner/name", isPair},
		{"authors", f.Authors, "a login", isWord},
		{"author_teams", f.AuthorTeams, "org/slug", isPair},
		{"requested_teams", f.RequestedTeams, "org/slug", isPair},
		// A label travels in double quotes, so it can hold spaces but no quote.
		{"labels", f.Labels, "a label with no double quote", func(s string) bool {
			return strings.TrimSpace(s) != "" && !strings.Contains(s, `"`)
		}},
	}
	empty := true
	for _, l := range lists {
		for j, it := range l.items {
			empty = false
			if !l.good(it) {
				return fmt.Errorf("%s: %s[%d] %q must be %s", where, l.key, j, it, l.want)
			}
		}
	}
	if empty {
		return fmt.Errorf("%s: needs at least one of repos, authors, author_teams, requested_teams, labels", where)
	}
	return nil
}

func isWord(s string) bool { return s != "" && !strings.ContainsAny(s, " \t\"") }

// isPair is an "owner/name" or "org/slug" form.
func isPair(s string) bool {
	a, b, ok := strings.Cut(s, "/")
	return ok && isWord(a) && isWord(b) && !strings.Contains(b, "/")
}

func decodeStrict(text string, v any) error {
	md, err := toml.Decode(text, v)
	if err != nil {
		return err
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = k.String()
		}
		return fmt.Errorf("unknown keys: %s", strings.Join(names, ", "))
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
		for name, v := range map[string]*int{"ci_debug": b.CIDebug, "responding": b.Responding, "restarts": b.Restarts} {
			if v != nil && *v < 0 {
				return fmt.Errorf("%s: budgets.%s must not be negative", where, name)
			}
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
	}
	return nil
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
		Budgets:      lifecycle.Budgets{CIDebug: 3, Responding: 3, Restarts: 2},
	}
	for _, r := range matched {
		layers = append(layers, r.layer)
	}
	layers = append(layers, repoFile)

	for _, l := range layers {
		if l.WIPCap != nil {
			res.WIPCap = *l.WIPCap
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
			if b.Restarts != nil {
				res.Budgets.Restarts = *b.Restarts
			}
		}
	}

	base := StageConfig{Harness: defaultHarness, Model: defaultModel, Effort: defaultEffort}
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

	res.Harnesses = map[string]string{}
	for _, sc := range res.Stages {
		if _, done := res.Harnesses[sc.Harness]; done {
			continue
		}
		p, err := g.harnessPath(sc.Harness)
		if err != nil {
			return Resolved{}, err
		}
		res.Harnesses[sc.Harness] = p
	}
	return res, nil
}

func apply(sc *StageConfig, s stageFile) {
	for dst, src := range map[*string]*string{&sc.Harness: s.Harness, &sc.Model: s.Model, &sc.Effort: s.Effort, &sc.Skill: s.Skill, &sc.Command: s.Command} {
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
		case "harness", "model", "effort", "skill", "command":
			for st, sc := range res.Stages {
				switch k {
				case "harness":
					sc.Harness = v
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

// harnessPath never asks a login shell: on this machine the login shell finds a broken codex.
func (g *Global) harnessPath(name string) (string, error) {
	p, ok := g.harness[name]
	if !ok {
		found, err := g.lookPath(name)
		if err != nil {
			return "", fmt.Errorf("harness %q: no [harness.%s] path and not on PATH: %w", name, name, err)
		}
		p, err = filepath.Abs(found)
		if err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("harness %q: %w", name, err)
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
