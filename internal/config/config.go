// Package config loads lineup's TOML configuration file and resolves it for
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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

const (
	defaultWIPCap       = 5
	defaultPollInterval = 30 * time.Second
	defaultModel        = "claude-opus-5-5"
	defaultEffort       = "high"
	// DefaultReviewCycles is the review bot's cycle budget when no
	// max_review_cycles applies.
	DefaultReviewCycles = 3
	// defaultKeepWorktreeDays equals the days that a Done Task stays on the Board.
	defaultKeepWorktreeDays = 7
	// maxKeepWorktreeDays is how long Claude keeps a conversation, so that a
	// Worker resumed in a kept worktree still finds its conversation.
	maxKeepWorktreeDays = 30
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

// DefaultBar is the user's bar, before the [landing_bar] tables.
func DefaultBar() lifecycle.LandingBar {
	return lifecycle.LandingBar{Checks: lifecycle.RequiredChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals}
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

// layer holds the keys that [defaults] and each [repos."<key>"] table share.
type layer struct {
	WIPCap       *int                 `toml:"wip_cap"`
	PollInterval *string              `toml:"poll_interval"`
	Budgets      *budgetsFile         `toml:"budgets"`
	ReviewBot    *reviewBotFile       `toml:"review_bot"`
	LandingBar   *landingBarFile      `toml:"landing_bar"`
	Merged       *mergedFile          `toml:"merged"`
	Stage        map[string]stageFile `toml:"stage"`
}

// layerKeys are the keys of a layer.
var layerKeys = []string{"wip_cap", "poll_interval", "budgets", "review_bot", "landing_bar", "merged", "stage"}

// repoTable is a [repos."<key>"] table. Its key is owner/repo for one Repo,
// or owner/* for every Repo of one owner.
type repoTable struct {
	layer
	Teams []string `toml:"teams"`
	// Tracker is the one Kind of Tracker whose Issues the Repo takes. ""
	// takes the owner/* table's, and a Repo with none takes no Issues.
	Tracker string `toml:"tracker"`
	// Prefix starts the ID of each Local Issue and ad hoc Task of the Repo,
	// as in HOD-3. The file can write it in any case, and Load keeps it in
	// lower case. "" keeps LOCAL-<n> and AH-<n>.
	Prefix string `toml:"prefix"`
}

// ReservedPrefixes start the IDs that lineup gives without a Repo prefix:
// LOCAL-<n> Local Issues, AH-<n> ad hoc Tasks, and PR-<n> review Tasks.
var ReservedPrefixes = []string{"local", "ah", "pr"}

var prefixForm = regexp.MustCompile(`^[A-Za-z0-9]+(-[A-Za-z0-9]+)*$`)

// PrefixedRepo is a [repos."owner/repo"] table with a prefix, in lower case.
type PrefixedRepo struct {
	Key    string
	Prefix string
}

// TrackerKinds are the values of tracker, one for each Kind of Tracker.
var TrackerKinds = []string{"linear", "github", "local"}

// TrackedRepo is a [repos."<key>"] table that sets tracker.
type TrackedRepo struct {
	Key     string
	Tracker string
}

type linearWorkspace struct {
	ClientID string                       `toml:"client_id"`
	Status   map[string]map[string]string `toml:"status"`
}

type globalFile struct {
	// Roots are the folders that hold the checkouts.
	Roots          []string                   `toml:"roots"`
	Defaults       layer                      `toml:"defaults"`
	Repos          map[string]repoTable       `toml:"repos"`
	StartSkills    []string                   `toml:"start_skills"`
	LinearClientID string                     `toml:"linear_client_id"`
	Linear         map[string]linearWorkspace `toml:"linear"`
	GitHub         struct {
		Labels map[string]string `toml:"labels"`
	} `toml:"github"`
	LocalTracker   bool   `toml:"local_tracker"`
	LocalIssuesDir string `toml:"local_issues_dir"`
	Review         struct {
		Slots  *int        `toml:"slots"`
		Queues []queueFile `toml:"queues"`
	} `toml:"review"`
	Board  boardFile  `toml:"board"`
	Layout layoutFile `toml:"layout"`
}

// mergedFile is a [merged] table: what lineup does with a build Task's tab
// and worktree after its PR merges. A layer can leave out a key and take it
// from a lower layer, as in [budgets].
type mergedFile struct {
	CloseTab         *bool `toml:"close_tab"`
	KeepWorktreeDays *int  `toml:"keep_worktree_days"`
}

// Merged is what lineup does with a build Task's tab and worktree after its
// PR merges.
type Merged struct {
	// CloseTab closes the tab once the Worker is not busy.
	CloseTab bool
	// KeepWorktree is how long lineup keeps the worktree after the tab's close.
	KeepWorktree time.Duration
}

// over sets each key that the table f of a higher layer sets.
func (m *Merged) over(f *mergedFile) {
	if f == nil {
		return
	}
	if f.CloseTab != nil {
		m.CloseTab = *f.CloseTab
	}
	if f.KeepWorktreeDays != nil {
		m.KeepWorktree = time.Duration(*f.KeepWorktreeDays) * 24 * time.Hour
	}
}

// checkKeepDays refuses a keep_worktree_days below 0, or above the days that
// Claude keeps a conversation.
func checkKeepDays(d int) error {
	switch {
	case d < 0:
		return fmt.Errorf("%d must be 0 or more", d)
	case d > maxKeepWorktreeDays:
		return fmt.Errorf("%d is more than %d, the days that Claude keeps a conversation", d, maxKeepWorktreeDays)
	}
	return nil
}

// resolveMerged applies the [merged] tables of the layers, lowest first, then
// the close_tab and keep_worktree_days overrides. A bad override gives the
// value of the tables with the error.
func resolveMerged(layers []layer, overrides map[string]string) (Merged, error) {
	m := Merged{KeepWorktree: defaultKeepWorktreeDays * 24 * time.Hour}
	for _, l := range layers {
		m.over(l.Merged)
	}
	var o mergedFile
	if v, ok := overrides["close_tab"]; ok {
		if v != "true" && v != "false" {
			return m, fmt.Errorf("override close_tab must be true or false, got %q", v)
		}
		closeTab := v == "true"
		o.CloseTab = &closeTab
	}
	if v, ok := overrides["keep_worktree_days"]; ok {
		d, err := strconv.Atoi(v)
		if err != nil {
			return m, fmt.Errorf("override keep_worktree_days must be a number of days, got %q", v)
		}
		if err := checkKeepDays(d); err != nil {
			return m, fmt.Errorf("override keep_worktree_days: %w", err)
		}
		o.KeepWorktreeDays = &d
	}
	m.over(&o)
	return m, nil
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

// ReviewQueue is a PR filter whose PRs lineup reviews without being asked. Slots
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
	board    Board
	boardErr error
	home     string
	lookPath func(string) (string, error)

	// keys caches the Repo key of each checkout path. The Scheduler asks for
	// keys from several goroutines.
	mu   sync.Mutex
	keys map[string]string
}

// StartSkills lists the skills that the Linear browser offers when it starts a Task.
func (g *Global) StartSkills() []string { return g.file.StartSkills }

// LinearClientID is the Linear OAuth app to sign in to a workspace with: the
// workspace's own client_id, else linear_client_id, else "" for lineup's.
func (g *Global) LinearClientID(workspace string) string {
	if id := g.file.Linear[workspace].ClientID; id != "" {
		return id
	}
	return g.file.LinearClientID
}

// backlogKey is the key of [github.labels] that names the label of a
// Backlog Issue. It is not a lifecycle event, so lineup never writes it.
const backlogKey = "backlog"

// GitHubLabels maps a lifecycle event to the label it sets on a GitHub Issue.
func (g *Global) GitHubLabels() map[string]string {
	labels := maps.Clone(g.file.GitHub.Labels)
	delete(labels, backlogKey)
	return labels
}

// GitHubBacklogLabel is the label that puts a GitHub Issue in Backlog on the
// Board: "backlog" unless [github.labels] sets backlog. An empty value puts
// every GitHub Issue in Up next.
func (g *Global) GitHubBacklogLabel() string {
	if l, ok := g.file.GitHub.Labels[backlogKey]; ok {
		return l
	}
	return backlogKey
}

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
		return expand(g.home, d)
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
	data, err := os.ReadFile(globalPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return newGlobal()
	case err != nil:
		return nil, err
	}
	return parseGlobal(globalPath, string(data))
}

func newGlobal() (*Global, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home folder: %w", err)
	}
	return &Global{home: home, lookPath: exec.LookPath, board: DefaultBoard(), keys: map[string]string{}}, nil
}

// parseGlobal reads the text of the global file at globalPath, which each
// error names.
func parseGlobal(globalPath, data string) (*Global, error) {
	g, err := newGlobal()
	if err != nil {
		return nil, err
	}
	why, err := oldForm(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	if why != "" {
		return nil, fmt.Errorf("%s: %s", globalPath, why)
	}
	if err := decodeStrict(data, &g.file); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	for key, t := range g.file.Repos {
		t.Prefix = strings.ToLower(t.Prefix)
		g.file.Repos[key] = t
	}
	if err := g.validate(&g.file.Defaults, "defaults"); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	for i, sk := range g.file.StartSkills {
		if strings.TrimSpace(sk) == "" {
			return nil, fmt.Errorf("%s: start_skills[%d] is empty", globalPath, i)
		}
	}
	for i, r := range g.file.Roots {
		if strings.TrimSpace(r) == "" {
			return nil, fmt.Errorf("%s: roots[%d] is empty", globalPath, i)
		}
	}
	if err := g.checkRepos(); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	if err := g.checkPrefixes(nil); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
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
		if !trackerEvents[ev] && ev != backlogKey {
			return nil, fmt.Errorf("%s: github.labels: unknown event %q", globalPath, ev)
		}
	}
	if err := validateReview(g.file.Review.Queues, g.file.Review.Slots); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	if _, err := g.file.Layout.resolve(); err != nil {
		return nil, fmt.Errorf("%s: %w", globalPath, err)
	}
	if g.board, g.boardErr = resolveBoard(g.file.Board); g.boardErr != nil {
		g.board = DefaultBoard()
	}
	return g, nil
}

// oldForm says why the text needs lineup config migrate: its [[repos]]
// tables, the first layer key at the top level, or the first trackers list.
// "" means none of them.
func oldForm(text string) (string, error) {
	var raw map[string]any
	md, err := toml.Decode(text, &raw)
	if err != nil {
		return "", err
	}
	if _, ok := raw["repos"].([]map[string]any); ok {
		return `[[repos]] tables are gone. Run lineup config migrate to rewrite the file with roots and [repos."owner/repo"] tables`, nil
	}
	for _, k := range md.Keys() {
		if slices.Contains(layerKeys, k[0]) {
			return k[0] + " belongs in [defaults]. Run lineup config migrate to move it", nil
		}
	}
	repos, _ := raw["repos"].(map[string]any)
	for _, key := range slices.Sorted(maps.Keys(repos)) {
		if t, _ := repos[key].(map[string]any); t["trackers"] != nil {
			return fmt.Sprintf("repos.%q: the trackers list is gone, as a Repo takes Issues from one Tracker. Run lineup config migrate to rewrite it as tracker", key), nil
		}
	}
	return "", nil
}

// splitKey splits a Repo key into its owner and its repo, which is * for
// every Repo of the owner. ok is false for any other form.
func splitKey(key string) (owner, repo string, ok bool) {
	owner, repo, found := strings.Cut(key, "/")
	part := func(s string) bool {
		return s != "" && !strings.ContainsAny(s, "/*?[") && !strings.ContainsFunc(s, unicode.IsSpace)
	}
	return owner, repo, found && part(owner) && (repo == "*" || part(repo))
}

// checkRepos checks the key of each [repos."<key>"] table, and its layer,
// teams, and tracker keys.
func (g *Global) checkRepos() error {
	seen := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(g.file.Repos)) {
		t := g.file.Repos[key]
		where := fmt.Sprintf("repos.%q", key)
		_, repo, ok := splitKey(key)
		if !ok {
			return fmt.Errorf("%s: a Repo key is owner/repo or owner/*", where)
		}
		if other, ok := seen[strings.ToLower(key)]; ok {
			return fmt.Errorf("%s: repos.%q names the same Repo, as GitHub names ignore case", where, other)
		}
		seen[strings.ToLower(key)] = key
		if err := g.validate(&t.layer, where); err != nil {
			return err
		}
		if t.Tracker != "" && !slices.Contains(TrackerKinds, t.Tracker) {
			return fmt.Errorf("%s: tracker %q is not a Tracker: use %s", where, t.Tracker, strings.Join(TrackerKinds, ", "))
		}
		if len(t.Teams) == 0 {
			continue
		}
		if repo == "*" {
			return fmt.Errorf("%s: teams maps Linear teams to one Repo, so it needs an owner/repo key", where)
		}
		switch tr := g.tracker(key); tr {
		case "linear":
		case "":
			return fmt.Errorf(`%s: teams maps Linear teams to the Repo, so the Repo needs tracker = "linear". Run lineup config migrate to add it`, where)
		default:
			return fmt.Errorf("%s: teams maps Linear teams to the Repo, so its tracker must be linear, not %s", where, tr)
		}
	}
	return nil
}

// checkPrefixes refuses a prefix that is not a word of letters, digits, and
// dashes, a prefix of an owner/* table, a prefix that two tables share, a
// reserved prefix, and a prefix that equals a Linear team key. Each check
// ignores case. The team keys are those of every teams list, and teamKeys.
func (g *Global) checkPrefixes(teamKeys []string) error {
	teams := map[string]bool{}
	for _, r := range g.file.Repos {
		for _, t := range r.Teams {
			teams[strings.ToLower(t)] = true
		}
	}
	for _, t := range teamKeys {
		teams[strings.ToLower(t)] = true
	}
	seen := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(g.file.Repos)) {
		p := g.file.Repos[key].Prefix
		if p == "" {
			continue
		}
		where := fmt.Sprintf("repos.%q: prefix %q", key, strings.ToUpper(p))
		switch {
		case !prefixForm.MatchString(p):
			return fmt.Errorf("%s must hold only letters, digits, and dashes, and must start and end with a letter or a digit", where)
		case strings.HasSuffix(key, "/*"):
			return fmt.Errorf("%s needs an owner/repo key, the key of one Repo", where)
		case slices.Contains(ReservedPrefixes, p):
			return fmt.Errorf("%s is reserved: lineup uses %s for IDs without a Repo prefix", where, strings.ToUpper(strings.Join(ReservedPrefixes, ", ")))
		case teams[p]:
			return fmt.Errorf("%s equals a Linear team key, so its IDs would read as Linear IDs", where)
		}
		if other, ok := seen[p]; ok {
			return fmt.Errorf("%s is also the prefix of repos.%q: each Repo needs its own prefix", where, other)
		}
		seen[p] = key
	}
	return nil
}

// CheckPrefixTeams refuses a prefix that equals one of the team keys, in any
// case. lineup restart passes the team keys of each Linear workspace.
func (g *Global) CheckPrefixTeams(teamKeys []string) error { return g.checkPrefixes(teamKeys) }

func validateReview(queues []queueFile, slots *int) error {
	if slots != nil && *slots < 1 {
		return errors.New("review.slots: slots must be at least 1")
	}
	names := map[string]bool{}
	for i, q := range queues {
		where := fmt.Sprintf("review.queues[%d]", i)
		name := strings.TrimSpace(q.Name)
		switch {
		case name == "":
			return fmt.Errorf("%s: name is required", where)
		case names[name]:
			return fmt.Errorf("%s: name %q is used twice", where, name)
		}
		names[name] = true
		if err := q.PRFilter.validate(where); err != nil {
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
func (f PRFilter) validate(where string) error {
	switch {
	case f.State != "" && !slices.Contains(prStates, f.State):
		return fmt.Errorf("%s: state %q must be one of %s", where, f.State, strings.Join(prStates, ", "))
	case f.State != "" && f.State != "open":
		return fmt.Errorf("%s: a queue reviews open PRs, so state must be open", where)
	case f.Draft != nil && *f.Draft:
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

var (
	errHarnessGone    = errors.New("harness is no longer a setting, lineup runs claude")
	errViewTablesGone = errors.New("the Board has no review view, so delete the [[review.views]] tables")
)

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

const adHocSessionHint = ". An ad hoc Task opens in its Repo's session, so delete the ad_hoc_session line"

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
			if len(k) >= 2 && k[0] == "review" && k[1] == "views" {
				return errViewTablesGone
			}
		}
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = k.String()
		}
		msg := "unknown keys: " + strings.Join(names, ", ")
		if slices.ContainsFunc(names, func(n string) bool { return strings.HasSuffix(n, "budgets.restarts") }) {
			msg += restartsHint
		}
		if slices.Contains(names, "ad_hoc_session") {
			msg += adHocSessionHint
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
	if m := l.Merged; m != nil && m.KeepWorktreeDays != nil {
		if err := checkKeepDays(*m.KeepWorktreeDays); err != nil {
			return fmt.Errorf("%s: merged.keep_worktree_days: %w", where, err)
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

// over sets each key that the table h of a higher layer sets.
func (f *reviewBotFile) over(h *reviewBotFile) {
	f.Name = cmp.Or(h.Name, f.Name)
	f.MinReviewScore = cmp.Or(h.MinReviewScore, f.MinReviewScore)
	f.MaxReviewCycles = cmp.Or(h.MaxReviewCycles, f.MaxReviewCycles)
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
	cycles := DefaultReviewCycles
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

// expand replaces a leading ~ with the home folder.
func expand(home, p string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// layers are the layers of the Repo at repoPath, lowest first: [defaults],
// the Repo's owner/* table, the Repo's owner/repo table.
func (g *Global) layers(repoPath string) []layer {
	out := []layer{g.file.Defaults}
	for _, t := range g.tables(g.keyOf(repoPath)) {
		out = append(out, t.layer)
	}
	return out
}

// Merged applies the [merged] tables of the Repo's layers, then the Task
// overrides. A bad override gives the value of the file with the error, so a
// caller can still act on it.
func (g *Global) Merged(repoPath string, overrides map[string]string) (Merged, error) {
	return resolveMerged(g.layers(repoPath), overrides)
}

// Resolve applies the layers for one Repo and the Task overrides, lowest
// first: [defaults], the Repo's owner/* table, the Repo's owner/repo table.
func (g *Global) Resolve(repoPath string, overrides map[string]string) (Resolved, error) {
	layers := g.layers(repoPath)
	res := Resolved{
		WIPCap:       defaultWIPCap,
		PR:           true,
		PollInterval: defaultPollInterval,
		Budgets:      lifecycle.Budgets{CIDebug: 3, Responding: 3},
	}

	var bot reviewBotFile
	res.Bar = DefaultBar()
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
			bot.over(f)
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

	var err error
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
	// The Scheduler reads [merged] at the merge, so Resolve only checks its overrides.
	if _, err := resolveMerged(layers, overrides); err != nil {
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
		case "close_tab", "keep_worktree_days":
			// resolveMerged reads them.
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

// ClaudePath is the absolute path of the claude binary.
func (g *Global) ClaudePath() (string, error) { return g.claudePath() }

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

// table is the [repos."<key>"] table whose key equals key ignoring case.
func (g *Global) table(key string) (repoTable, bool) {
	for k, t := range g.file.Repos {
		if strings.EqualFold(k, key) {
			return t, true
		}
	}
	return repoTable{}, false
}

// tables are the owner/* table and the owner/repo table of the Repo key,
// lowest first. A table that the file does not have is absent.
func (g *Global) tables(key string) []repoTable {
	owner, _, _ := strings.Cut(key, "/")
	var out []repoTable
	for _, k := range []string{owner + "/*", key} {
		if t, ok := g.table(k); ok {
			out = append(out, t)
		}
	}
	return out
}

// tracker is the tracker of the Repo key's own table, else of its owner/*
// table. "" means that the Repo takes no Issues.
func (g *Global) tracker(key string) string {
	out := ""
	for _, t := range g.tables(key) {
		out = cmp.Or(t.Tracker, out)
	}
	return out
}

// ReposForTeam lists the keys of the [repos."owner/repo"] tables whose teams
// list holds the team, in any case, sorted.
func (g *Global) ReposForTeam(teamKey string) []string {
	var out []string
	for _, key := range slices.Sorted(maps.Keys(g.file.Repos)) {
		if slices.ContainsFunc(g.file.Repos[key].Teams, func(t string) bool { return strings.EqualFold(t, teamKey) }) {
			out = append(out, key)
		}
	}
	return out
}

// TeamKeys lists the Linear team keys of every teams list, sorted, each once.
func (g *Global) TeamKeys() []string {
	var out []string
	for _, r := range g.file.Repos {
		out = append(out, r.Teams...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// RepoTracker is the Kind of Tracker whose Issues the Repo takes, from its
// own table, else from its owner/* table, and the Repo's key. tracker "" means
// that the Repo takes no Issues.
func (g *Global) RepoTracker(repoPath string) (key, tracker string) {
	key = g.keyOf(repoPath)
	return key, g.tracker(key)
}

// SetUp reports whether the file has the Repo's own [repos."owner/repo"]
// table, in any case, even an empty one. An owner/* table sets up no Repo.
// key is the Repo's key.
func (g *Global) SetUp(repoPath string) (key string, ok bool) {
	key = g.keyOf(repoPath)
	_, ok = g.table(key)
	return key, ok
}

// Inherited is what [defaults] and a Repo's owner/* table set for the keys
// that lineup repo init writes. "" and nil mean that neither sets the key.
type Inherited struct {
	// Tracker comes only from the owner/* table, as [defaults] has none.
	Tracker    string
	ReviewBot  InheritedBot
	LandingBar InheritedBar
}

type InheritedBot struct {
	Name            string
	MinReviewScore  *int
	MaxReviewCycles *int
}

// InheritedBar holds the names of the Landing bar rules, such as all.
type InheritedBar struct{ Checks, Threads, Approvals string }

// Inherited merges [defaults] and the owner/* table of the Repo key, as
// Resolve does. The Repo's own table does not count.
func (g *Global) Inherited(key string) Inherited {
	owner, _, _ := strings.Cut(key, "/")
	layers := []layer{g.file.Defaults}
	var in Inherited
	if t, ok := g.table(owner + "/*"); ok {
		layers, in.Tracker = append(layers, t.layer), t.Tracker
	}
	set := func(dst, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	var bot reviewBotFile
	for _, l := range layers {
		if f := l.ReviewBot; f != nil {
			bot.over(f)
		}
		if f := l.LandingBar; f != nil {
			set(&in.LandingBar.Checks, f.Checks)
			set(&in.LandingBar.Threads, f.Threads)
			set(&in.LandingBar.Approvals, f.Approvals)
		}
	}
	set(&in.ReviewBot.Name, bot.Name)
	in.ReviewBot.MinReviewScore, in.ReviewBot.MaxReviewCycles = bot.MinReviewScore, bot.MaxReviewCycles
	return in
}

// PrefixedRepos lists the tables with a prefix, sorted by key.
func (g *Global) PrefixedRepos() []PrefixedRepo {
	var out []PrefixedRepo
	for _, key := range slices.Sorted(maps.Keys(g.file.Repos)) {
		if p := g.file.Repos[key].Prefix; p != "" {
			out = append(out, PrefixedRepo{Key: key, Prefix: p})
		}
	}
	return out
}

// Prefixes lists the prefix of each table that has one, in lower case.
func (g *Global) Prefixes() []string {
	var out []string
	for _, r := range g.PrefixedRepos() {
		out = append(out, r.Prefix)
	}
	return out
}

// RepoPrefix is the prefix of the Repo's own table, in lower case, or "".
func (g *Global) RepoPrefix(repoPath string) string {
	t, _ := g.table(g.keyOf(repoPath))
	return t.Prefix
}

// RepoForPrefix is the checkout of the table with the prefix, which is in
// lower case.
func (g *Global) RepoForPrefix(prefix string) (string, bool) {
	for _, r := range g.PrefixedRepos() {
		if r.Prefix == prefix {
			return g.CheckoutFor(r.Key)
		}
	}
	return "", false
}

// TrackedRepos lists the tables that set tracker, sorted by key.
func (g *Global) TrackedRepos() []TrackedRepo {
	var out []TrackedRepo
	for _, key := range slices.Sorted(maps.Keys(g.file.Repos)) {
		if tr := g.file.Repos[key].Tracker; tr != "" {
			out = append(out, TrackedRepo{Key: key, Tracker: tr})
		}
	}
	return out
}

// CheckoutsFor lists the checkouts whose Tracker is the kind.
func (g *Global) CheckoutsFor(kind string) []string {
	var out []string
	for _, c := range g.Checkouts() {
		if g.tracker(c.Key) == kind {
			out = append(out, c.Path)
		}
	}
	return out
}
