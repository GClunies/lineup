package scheduler

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/harness/claude"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

type tab struct {
	Window
	label     string
	cwd       string
	env       map[string]string
	argv      []string
	closed    bool
	blockGone bool
	// shell marks a tab that Rex restored as an idle shell after a restart.
	shell bool
	// program is the state that Rex reads from the tab's program now.
	program string
	// keep marks a tab that stays open after its program exits, and exited
	// marks that exit with code.
	keep, exited bool
	code         int
}

// split is a block that Split opened beside the anchor block of a tab.
// beside lists the other open blocks of the tab when the split opened. label
// program, and args are what Panes reads: side and hunk for each split that
// Split opens. A test seeds other blocks of a tab with any label, program, and
// args.
type split struct {
	BlockID, anchor, cwd, window string
	argv, beside                 []string
	label, program               string
	args                         []string
	closed                       bool
}

// sideAt is a live side pane that an earlier Scheduler opened in window.
func sideAt(block, window string) *split {
	return &split{BlockID: block, window: window, label: "side", program: "hunk"}
}

type fakeRex struct {
	mu     sync.Mutex
	n      int
	tabs   []*tab
	splits []*split
	// splitErr fails every Split call.
	splitErr  error
	destroyed []string
	// renamed lists each RenameSession call as "<session> <label>".
	renamed []string
	events  chan RexEvent
	// exitFast makes a tab whose argv starts with this word exit with 0
	// before NewWindow returns.
	exitFast string
	// capture is what Capture returns.
	capture   string
	blocksErr error
	// idleErr fails every IdleShell call.
	idleErr error
	typed   []typed
	focused []string
	// calls lists each window that NewWindow opened and CloseWindow closed, in order.
	calls []string
	// hold makes NewWindow and NewBlock of a Worker wait for the channel to close.
	hold chan struct{}
	// t fails the test when lineup closes a live tab that the user did not
	// ask to close. asked is true after a close, archive, open, or review op.
	t     *testing.T
	asked bool
}

type typed struct{ block, text string }

func (f *fakeRex) EnsureSession(_ context.Context, label string) (string, error) {
	return "session:" + label, nil
}

func (f *fakeRex) RenameSession(_ context.Context, session, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renamed = append(f.renamed, session+" "+label)
	return nil
}

func (f *fakeRex) NewWindow(_ context.Context, session, label, cwd string, env map[string]string, argv []string, keep bool) (Window, error) {
	if utf8.RuneCountInString(label) > 128 {
		return Window{}, errors.New("label must be at most 128 characters")
	}
	f.waitHold(argv)
	f.mu.Lock()
	f.n++
	w := Window{session, fmt.Sprintf("window:%d", f.n), fmt.Sprintf("block:%d", f.n)}
	f.tabs = append(f.tabs, &tab{Window: w, label: label, cwd: cwd, env: env, argv: argv, keep: keep})
	f.calls = append(f.calls, "new "+w.WindowID)
	f.mu.Unlock()
	if f.exitFast != "" && argv[0] == f.exitFast {
		f.exits(tab{Window: w}, 0)
	}
	return w, nil
}

func (f *fakeRex) waitHold(argv []string) {
	f.mu.Lock()
	hold := f.hold
	f.mu.Unlock()
	if hold != nil && len(argv) > 0 && argv[0] == "claude" {
		<-hold
	}
}

func (f *fakeRex) NewBlock(_ context.Context, _, block, cwd string, env map[string]string, argv []string) (string, error) {
	f.waitHold(argv)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tabs {
		if t.BlockID == block && !t.closed {
			f.n++
			w := Window{t.SessionID, t.WindowID, fmt.Sprintf("block:%d", f.n)}
			f.tabs = append(f.tabs, &tab{Window: w, label: t.label, cwd: cwd, env: env, argv: argv})
			return w.BlockID, nil
		}
	}
	return "", fmt.Errorf("block target %q not found", block)
}

func (f *fakeRex) CloseBlock(_ context.Context, _, block string) error {
	f.mu.Lock()
	var closed bool
	for _, t := range f.tabs {
		if t.BlockID == block && !t.closed {
			if !t.exited && !f.asked && f.t != nil {
				f.t.Errorf("lineup closed the live block %s (%s) that the user did not ask to close", block, strings.Join(t.argv, " "))
			}
			t.closed, closed = true, true
		}
	}
	for _, s := range f.splits {
		if s.BlockID == block && !s.closed {
			s.closed, closed = true, true
		}
	}
	f.mu.Unlock()
	if closed {
		f.events <- RexEvent{Kind: EvClosed, BlockID: block}
	}
	return nil
}

func (f *fakeRex) Split(_ context.Context, _, block, cwd string, argv []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.splitErr != nil {
		return "", f.splitErr
	}
	window := ""
	for _, t := range f.tabs {
		if t.BlockID == block {
			window = t.WindowID
		}
	}
	for _, s := range f.splits {
		if s.BlockID == block && !s.closed {
			window = s.window
		}
	}
	if window == "" {
		return "", fmt.Errorf("block %s not found", block)
	}
	var beside []string
	for _, o := range f.tabs {
		if o.WindowID == window && o.BlockID != block && !o.closed {
			beside = append(beside, o.BlockID)
		}
	}
	f.n++
	f.splits = append(f.splits, &split{BlockID: fmt.Sprintf("split:%d", f.n), anchor: block, cwd: cwd, window: window, argv: argv, beside: beside,
		label: "side", program: "hunk"})
	return f.splits[len(f.splits)-1].BlockID, nil
}

// Panes gives each tab block the label of a plain tab's block and the first
// word of its command as its program.
func (f *fakeRex) Panes(_ context.Context, _, window string) ([]Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Pane
	for _, t := range f.tabs {
		if t.WindowID == window && !t.closed {
			p := Pane{BlockID: t.BlockID, Label: "shell"}
			if len(t.argv) > 0 {
				p.Program = t.argv[0]
			}
			out = append(out, p)
		}
	}
	for _, s := range f.splits {
		if s.window == window && !s.closed {
			out = append(out, Pane{BlockID: s.BlockID, Label: s.label, Program: s.program, Args: s.args})
		}
	}
	return out, nil
}

func (f *fakeRex) CloseWindow(_ context.Context, _, window string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "close "+window)
	for _, t := range f.tabs {
		if t.WindowID == window {
			if !t.closed && !t.blockGone && !t.shell && !t.exited && !f.asked && f.t != nil {
				f.t.Errorf("lineup closed the live tab %s (%s) that the user did not ask to close", window, strings.Join(t.argv, " "))
			}
			t.closed = true
		}
	}
	for _, s := range f.splits {
		if s.window == window {
			s.closed = true
		}
	}
	return nil
}

// allSplits returns every split that Split opened, closed or not.
func (f *fakeRex) allSplits() []split {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []split
	for _, s := range f.splits {
		out = append(out, *s)
	}
	return out
}

func (f *fakeRex) DestroySession(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed = append(f.destroyed, session)
	return nil
}

func (f *fakeRex) Blocks(_ context.Context, session string) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocksErr != nil {
		return nil, f.blocksErr
	}
	live, windows := map[string]bool{}, map[string]bool{}
	for _, t := range f.tabs {
		if !t.closed && t.SessionID == session {
			live[t.BlockID], windows[t.WindowID] = true, true
		}
	}
	for _, s := range f.splits {
		if !s.closed && windows[s.window] {
			live[s.BlockID] = true
		}
	}
	return live, nil
}

func (f *fakeRex) IdleShell(_ context.Context, _, block string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idleErr != nil {
		return false, f.idleErr
	}
	for _, t := range f.tabs {
		if t.BlockID == block {
			return t.shell, nil
		}
	}
	return false, nil
}

func (f *fakeRex) LastExit(_ context.Context, _, block string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tabs {
		if t.BlockID == block {
			return t.code, t.exited, nil
		}
	}
	return 0, false, nil
}

func (f *fakeRex) ProgramStatus(_ context.Context, _, block string) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tabs {
		if t.BlockID == block {
			return Status{State: t.program}, nil
		}
	}
	return Status{}, nil
}
func (f *fakeRex) Focus(_ context.Context, _, _, block string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focused = append(f.focused, block)
	return nil
}
func (f *fakeRex) Capture(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capture, nil
}

func (f *fakeRex) Type(_ context.Context, _, block, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, typed{block, text})
	return nil
}

func (f *fakeRex) typedInto(block string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, t := range f.typed {
		if t.block == block {
			out = append(out, t.text)
		}
	}
	return out
}

// exits ends the program in a tab, and Rex reports the exit. A tab opened
// with keep stays open. Rex closes any other tab after about 1 s.
func (f *fakeRex) exits(w tab, code int) {
	f.mu.Lock()
	for _, t := range f.tabs {
		if t.WindowID == w.WindowID && !t.closed {
			t.exited, t.closed, t.code = t.keep, !t.keep, code
		}
	}
	f.mu.Unlock()
	f.events <- RexEvent{Kind: EvExited, BlockID: w.BlockID, ExitCode: code}
}

func (f *fakeRex) blockCloses(w tab) {
	f.mu.Lock()
	for _, t := range f.tabs {
		if t.WindowID == w.WindowID {
			t.blockGone = true
		}
	}
	f.mu.Unlock()
	f.events <- RexEvent{Kind: EvClosed, BlockID: w.BlockID}
}

// userCloses closes a tab as the user does in Rex: the tab goes, then Rex
// reports its block closed.
func (f *fakeRex) userCloses(w tab) {
	f.mu.Lock()
	for _, t := range f.tabs {
		if t.WindowID == w.WindowID {
			t.closed = true
		}
	}
	for _, s := range f.splits {
		if s.window == w.WindowID {
			s.closed = true
		}
	}
	f.mu.Unlock()
	f.events <- RexEvent{Kind: EvClosed, BlockID: w.BlockID}
}

// open returns the open tabs whose command starts with word.
func (f *fakeRex) open(word string) []tab {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tab
	for _, t := range f.tabs {
		if !t.closed && strings.Join(t.argv, " ") == word {
			out = append(out, *t)
		}
	}
	return out
}

// fakeGitHub keeps one PR per key. A snapshot's Raw is a list of names: the
// edges that GitHub would report for it, and the facts of its pr-seen.
type fakeGitHub struct {
	mu     sync.Mutex
	prs    map[PRKey]PRSnapshot
	merged []int
	// polled is every key that a branch poll asked for.
	polled  []PRKey
	reviews map[ReviewKey]ReviewSnapshot
	// found is each queue search's rows, by queue name. searches logs each
	// SearchPRs call, and a search waits for hold while hold is set.
	found       map[string][]github.PullRequest
	searches    [][]github.Search
	hold        chan struct{}
	members     map[string][]string
	memberCalls int
}

func (g *fakeGitHub) SearchPRs(_ context.Context, ss []github.Search) (map[string][]github.PullRequest, error) {
	g.mu.Lock()
	g.searches = append(g.searches, ss)
	hold := g.hold
	g.mu.Unlock()
	if hold != nil {
		<-hold
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string][]github.PullRequest{}
	for _, s := range ss {
		out[s.Key] = slices.Clone(g.found[s.Key])
	}
	return out, nil
}

func (g *fakeGitHub) TeamMembers(_ context.Context, teams []string) (map[string][]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.memberCalls++
	out := map[string][]string{}
	for _, t := range teams {
		out[t] = g.members[t]
	}
	return out, nil
}

func (g *fakeGitHub) searchCalls() [][]github.Search {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.searches)
}

// edgesByName are the edge Signals, which come when the snapshot changes.
var edgesByName = map[string]lifecycle.Signal{
	"pr-open": lifecycle.PROpen{}, "pushed": lifecycle.Pushed{}, "pr-merged": lifecycle.PRMerged{},
}

// factsByName are the facts of the pr-seen that each poll ends with.
var factsByName = map[string]lifecycle.PRFacts{
	"ci-failed":       {Head: testHead, Checks: []lifecycle.Check{{Name: "test", State: lifecycle.CheckFailed}}},
	"ci-passed":       {Head: testHead},
	"review-required": {Head: testHead, Decision: lifecycle.DecisionRequired},
	"review-approved": {Head: testHead, Decision: lifecycle.DecisionApproved},
	"scored-low":      {Head: testHead, Decision: lifecycle.DecisionRequired, Score: &four},
}

const testHead = "c0ffee1234"

var four = 4

func (g *fakeGitHub) set(key PRKey, number int, signals ...string) {
	raw, _ := json.Marshal(signals)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prs[key] = PRSnapshot{Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", key.Repo, number), Raw: raw}
}

// endedAt makes the key's PR one that merged or closed at at.
func (g *fakeGitHub) endedAt(key PRKey, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pr := g.prs[key]
	pr.ClosedAt = at
	g.prs[key] = pr
}

func (g *fakeGitHub) RepoSlug(context.Context, string) (string, error) { return "acme/data", nil }

func (g *fakeGitHub) Poll(_ context.Context, keys []PRKey) (map[PRKey]PRSnapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.polled = append(g.polled, keys...)
	out := map[PRKey]PRSnapshot{}
	for _, k := range keys {
		if pr, ok := g.prs[k]; ok {
			out[k] = pr
		}
	}
	return out, nil
}

// setReview sets the PR that the review poll returns. GitHub names ignore case.
func (g *fakeGitHub) setReview(pr ReviewSnapshot) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reviews[ReviewKey{strings.ToLower(pr.Repo), pr.Number}] = pr
}

func (g *fakeGitHub) ReviewPRs(_ context.Context, keys []ReviewKey) (map[ReviewKey]ReviewSnapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[ReviewKey]ReviewSnapshot{}
	for _, k := range keys {
		if pr, ok := g.reviews[ReviewKey{strings.ToLower(k.Repo), k.Number}]; ok {
			out[k] = pr
		}
	}
	return out, nil
}

func (g *fakeGitHub) Signals(prev, cur *PRSnapshot) []lifecycle.Signal {
	changed := prev == nil || string(prev.Raw) != string(cur.Raw)
	var names []string
	json.Unmarshal(cur.Raw, &names)
	var out []lifecycle.Signal
	for _, n := range names {
		if e, ok := edgesByName[n]; ok && changed {
			out = append(out, e)
		}
	}
	for _, n := range names {
		if n == "pr-closed" {
			return []lifecycle.Signal{lifecycle.PRClosed{Number: cur.Number}}
		}
		if f, ok := factsByName[n]; ok {
			out = append(out, lifecycle.PRSeen{Facts: f})
		}
	}
	return out
}

func (g *fakeGitHub) Merge(_ context.Context, repo string, n int) error {
	g.mu.Lock()
	g.merged = append(g.merged, n)
	g.mu.Unlock()
	g.set(PRKey{Repo: repo, Branch: "ada/eng-1-billing"}, n, "pr-merged")
	return nil
}

type fakeWorktrees struct {
	mu      sync.Mutex
	root    string
	removed []string
	// added and reviewsRemoved log the review worktree calls. inFlight and
	// mostInFlight count the AddReview calls that run at one time.
	added          []int
	reviewsRemoved []string
	inFlight       int
	mostInFlight   int
	// keep makes RemoveReview keep the worktree, as for an unpushed commit.
	keep bool
	// dirty makes Remove keep the worktree, as for uncommitted changes.
	// removed still logs each try.
	dirty bool
	// removeErr fails each Remove call, and the worktree stays.
	removeErr error
	// addErr fails the next AddReview call.
	addErr error
	// base is the DiffBase of every worktree, or base1 when it is empty. gone
	// lists the worktrees whose folder is gone.
	base string
	gone []string
}

func (w *fakeWorktrees) DiffBase(_ context.Context, worktree string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if slices.Contains(w.gone, worktree) {
		return "", fmt.Errorf("%w: %s", ErrNoWorktree, worktree)
	}
	return cmp.Or(w.base, "base1"), nil
}

// rebase moves the DiffBase of every worktree to base.
func (w *fakeWorktrees) rebase(base string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.base = base
}

// lose removes the worktree's folder, as the user's wt remove does.
func (w *fakeWorktrees) lose(worktree string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gone = append(w.gone, worktree)
}

func (w *fakeWorktrees) AddReview(_ context.Context, repo string, pr ReviewPR) (string, error) {
	w.mu.Lock()
	w.inFlight++
	w.mostInFlight = max(w.mostInFlight, w.inFlight)
	w.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inFlight--
	if err := w.addErr; err != nil {
		w.addErr = nil
		return "", err
	}
	w.added = append(w.added, pr.Number)
	return fmt.Sprintf("%s.review-pr-%d", repo, pr.Number), nil
}

func (w *fakeWorktrees) RemoveReview(_ context.Context, _, path string, _ ReviewPR) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reviewsRemoved = append(w.reviewsRemoved, path)
	return w.keep, nil
}

func (w *fakeWorktrees) CreateArgv(_, branch string) []string { return []string{"wt", branch} }
func (w *fakeWorktrees) Path(_ context.Context, _, branch string) (string, error) {
	return filepath.Join(w.root, filepath.Base(branch)), nil
}

// Remove logs the path of each try, or the branch when the Task has no path.
func (w *fakeWorktrees) Remove(_ context.Context, _, branch, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removed = append(w.removed, cmp.Or(path, branch))
	if w.dirty {
		return fmt.Errorf("%w: %s", ErrUncommitted, branch)
	}
	return w.removeErr
}

func (w *fakeWorktrees) failRemove(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removeErr = err
}

func (w *fakeWorktrees) setDirty(dirty bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirty = dirty
}

type fakeHarness struct {
	mu sync.Mutex
	// gone lists the conversations that HasConversation does not find.
	gone []string
	// trusted lists each folder that Prepare trusted.
	trusted    []string
	specs      []WorkerSpec
	usage      map[string]*Usage
	usageCalls int
	usageHold  chan struct{}
}

func (h *fakeHarness) Prepare(_ context.Context, _, worktree string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trusted = append(h.trusted, worktree)
	return nil
}

func (h *fakeHarness) HasConversation(_ context.Context, w WorkerSpec) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !slices.Contains(h.gone, w.Conversation)
}

func (h *fakeHarness) Usage(_ context.Context, ws []WorkerSpec) []*Usage {
	h.mu.Lock()
	h.usageCalls++
	hold := h.usageHold
	h.mu.Unlock()
	if hold != nil {
		<-hold
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Usage, len(ws))
	for i, w := range ws {
		out[i] = h.usage[w.Conversation]
	}
	return out
}

func (h *fakeHarness) Prompt(_ context.Context, w WorkerSpec) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.specs = append(h.specs, w)
	if w.Stage == lifecycle.ReviewReady {
		return "", nil
	}
	return "prompt " + w.Stage.Key(), nil
}

func (h *fakeHarness) Command(_ context.Context, w WorkerSpec, prompt string) ([]string, map[string]string, error) {
	if prompt == "" {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.specs = append(h.specs, w)
		return []string{"claude", "--resume", w.Conversation}, nil, nil
	}
	return []string{"claude", w.Stage.Key()}, nil, nil
}

type fakeTracker struct {
	mu     sync.Mutex
	status []string
	kinds  []string // the Kind of each Lookup
	title  string   // the Issue's title, and "Add billing model" when empty
}

func (t *fakeTracker) Lookup(_ context.Context, kind, id string) (Issue, error) {
	t.mu.Lock()
	t.kinds = append(t.kinds, kind)
	title := cmp.Or(t.title, "Add billing model")
	t.mu.Unlock()
	return Issue{Kind: kind, Identifier: id, Title: title, BranchName: "ada/eng-1-billing", TeamKey: "ENG"}, nil
}
func (t *fakeTracker) Begin(context.Context, Issue) error { return nil }
func (t *fakeTracker) SetStatus(_ context.Context, _ Issue, event string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = append(t.status, event)
	return nil
}

type fakeConfig struct{}

var testPolicy = lifecycle.Policy{Budgets: lifecycle.Budgets{CIDebug: 3, Responding: 3}}

func (fakeConfig) Policy(string, map[string]string) (lifecycle.Policy, error) { return testPolicy, nil }
func (fakeConfig) RepoForIssue(context.Context, Issue) (string, error) {
	return "/repos/acme/data", nil
}
func (fakeConfig) PickedRepo(context.Context, Issue, string) error { return nil }
func (fakeConfig) ResolveRepo(name string) (string, error)         { return "/repos/acme/" + name, nil }
func (fakeConfig) RepoPrefix(string) string                        { return "" }
func (fakeConfig) SetUp(repo string) (string, bool)                { return "acme/" + filepath.Base(repo), true }

// Merged keeps the tab and a worktree for 7 days, as an empty [merged] does.
func (fakeConfig) Merged(string, map[string]string) (Merged, error) {
	return Merged{KeepWorktree: 7 * 24 * time.Hour}, nil
}

// RepoTracker gives /repos/acme/issues the GitHub Tracker, /repos/acme/notes
// the Local Tracker, and every other Repo Linear.
func (fakeConfig) RepoTracker(repo string) (string, string) {
	base := filepath.Base(repo)
	return "acme/" + base, cmp.Or(map[string]string{"issues": KindGitHub, "notes": KindLocal}[base], KindLinear)
}

func (fakeConfig) RepoForGitHub(slug string) (string, bool) {
	if strings.EqualFold(slug, "acme/data") {
		return "/repos/acme/data", true
	}
	return "", false
}

type harness struct {
	t   *testing.T
	s   *Scheduler
	rex *fakeRex
	gh  *fakeGitHub
	wt  *fakeWorktrees
	hn  *fakeHarness
	tr  *fakeTracker
	// stateHome is the XDG state folder of review Workers.
	stateHome string
	st        *store.Store
	cancel    context.CancelFunc
	done      chan error
}

func newHarness(t *testing.T, dbPath string, seed func(*store.Store), opts ...func(*Deps)) *harness {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if seed != nil {
		seed(st)
	}
	h := &harness{
		t: t, rex: &fakeRex{events: make(chan RexEvent, 16), t: t},
		gh: &fakeGitHub{prs: map[PRKey]PRSnapshot{}, reviews: map[ReviewKey]ReviewSnapshot{}}, wt: &fakeWorktrees{root: t.TempDir()},
		hn: &fakeHarness{}, tr: &fakeTracker{}, st: st, stateHome: t.TempDir(),
	}
	d := Deps{
		Rex: h.rex, GitHub: h.gh, Worktrees: h.wt, Harness: h.hn, Tracker: h.tr, Config: fakeConfig{},
		Store: st, DefaultCap: 5, PollInterval: 20 * time.Millisecond, StateHome: h.stateHome,
	}
	for _, o := range opts {
		o(&d)
	}
	h.s, err = New(d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan error, 1)
	go func() { h.done <- h.s.Run(ctx, h.rex.events) }()
	t.Cleanup(func() { cancel(); <-h.done; st.Close() })
	return h
}

func (h *harness) do(req Request) any {
	h.t.Helper()
	if req.Op == "close" || req.Op == "archive" || req.Op == "open" || req.Op == "review" || req.Op == "reviews-skip" {
		h.rex.mu.Lock()
		h.rex.asked = true
		h.rex.mu.Unlock()
	}
	v, err := h.s.Handle(context.Background(), req)
	if err != nil {
		h.t.Fatalf("%s: %v", req.Op, err)
	}
	return v
}

func (h *harness) task(id string) TaskView {
	h.t.Helper()
	d, err := h.s.show(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return d.TaskView
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) waitStage(id string, stage lifecycle.Stage) {
	h.t.Helper()
	eventually(h.t, id+" in "+stage.String(), func() bool { return h.task(id).Stage == stage.String() })
}

func (h *harness) waitTab(cmd string) tab {
	h.t.Helper()
	var tabs []tab
	eventually(h.t, "a tab running "+cmd, func() bool { tabs = h.rex.open(cmd); return len(tabs) == 1 })
	return tabs[0]
}

// waitTabRecorded waits until the Scheduler has recorded the Task's tab. Rex
// opens the tab before the Scheduler records it, so a test that ends the Task
// right after waitTab can run before the Record holds the window.
func (h *harness) waitTabRecorded(cmd, id string) tab {
	h.t.Helper()
	w := h.waitTab(cmd)
	eventually(h.t, id+" to record its tab", func() bool { return h.task(id).Tab })
	return w
}

func TestALongTitleCutsTheTabLabel(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.tr.title = strings.Repeat("é", 200)
	h.do(Request{Op: "add", Issue: "ENG-1"})

	setup := h.waitTab("wt ada/eng-1-billing")
	want := "ENG-1 " + strings.Repeat("é", 25) + "…"
	if setup.label != want {
		t.Fatalf("setup tab label = %q (%d characters), want %q", setup.label, utf8.RuneCountInString(setup.label), want)
	}
	h.rex.exits(setup, 0)
	if worker := h.waitTab("claude working"); worker.label != want {
		t.Fatalf("worker tab label = %q", worker.label)
	}
	h.hn.mu.Lock()
	defer h.hn.mu.Unlock()
	if n := h.hn.specs[len(h.hn.specs)-1].Name; n != want {
		t.Fatalf("worker name = %q", n)
	}
}

func TestTaskFromStartToMerged(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}

	h.do(Request{Op: "add", Issue: "ENG-1"})
	if v := h.task("ENG-1"); v.Stage != "Working" || v.Repo != "data" || v.Kind != lifecycle.KindBuild {
		t.Fatalf("added task = %+v", v)
	}

	setup := h.waitTab("wt ada/eng-1-billing")
	if setup.cwd != "/repos/acme/data" || setup.label != "ENG-1 Add billing model" {
		t.Fatalf("setup tab = %+v", setup)
	}
	h.rex.exits(setup, 0)
	worker := h.waitTab("claude working")
	if worktree := filepath.Join(h.wt.root, "eng-1-billing"); worker.cwd != worktree {
		t.Fatalf("worker cwd = %s, want %s", worker.cwd, worktree)
	}

	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}
	eventually(t, "stopped", func() bool { return h.task("ENG-1").Flag == "stopped" })

	h.gh.set(key, 42, "pr-open")
	h.waitStage("ENG-1", lifecycle.CIRunning)

	h.gh.set(key, 42, "ci-failed")
	h.waitStage("ENG-1", lifecycle.CIDebug)
	h.waitTyped(worker.BlockID, "prompt ci_debug")
	if v := h.task("ENG-1"); v.Note != "#42 ○ no review yet · fixing CI" {
		t.Fatalf("CI debug note = %q", v.Note)
	}
	h.gh.set(key, 42, "pushed")
	h.waitStage("ENG-1", lifecycle.CIRunning)
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}

	h.gh.set(key, 42, "review-required")
	h.waitStage("ENG-1", lifecycle.InReview)
	if v := h.task("ENG-1"); v.Note != "#42 ○ no review yet" {
		t.Fatalf("In review note = %q", v.Note)
	}
	h.gh.set(key, 42, "review-approved")
	h.waitStage("ENG-1", lifecycle.ReadyToLand)

	h.do(Request{Op: "merge", Task: "ENG-1"})
	h.waitStage("ENG-1", lifecycle.Done)
	eventually(t, "the tracker told", func() bool {
		h.tr.mu.Lock()
		defer h.tr.mu.Unlock()
		return slices.Equal(h.tr.status, []string{"started", "in_review", "merged"})
	})
	if v := h.task("ENG-1"); v.Outcome != "merged" || v.PRNumber != 42 || !v.Tab {
		t.Fatalf("done task = %+v, want merged with its tab", v)
	}
	if len(h.rex.open("claude working")) != 1 {
		t.Fatal("the Worker tab closed when the Task ended")
	}
	h.wt.mu.Lock()
	removed := slices.Clone(h.wt.removed)
	h.wt.mu.Unlock()
	h.rex.mu.Lock()
	destroyed := slices.Clone(h.rex.destroyed)
	h.rex.mu.Unlock()
	if len(removed) != 0 || len(destroyed) != 0 {
		t.Fatalf("the Task's end removed worktrees %v and sessions %v, want neither", removed, destroyed)
	}
}

func doneWithATab(t *testing.T) (*harness, tab) {
	t.Helper()
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	h.gh.set(PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}, 42, "pr-merged")
	h.waitStage("ENG-1", lifecycle.Done)
	eventually(t, "the tracker told", func() bool {
		h.tr.mu.Lock()
		defer h.tr.mu.Unlock()
		return slices.Contains(h.tr.status, "merged")
	})
	return h, worker
}

func (h *harness) worktreesRemoved() []string {
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	return slices.Clone(h.wt.removed)
}

func (h *harness) sessionsDestroyed() []string {
	h.rex.mu.Lock()
	defer h.rex.mu.Unlock()
	return slices.Clone(h.rex.destroyed)
}

func TestADoneTabTheUserClosesRemovesTheWorktree(t *testing.T) {
	h, worker := doneWithATab(t)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v before the user closed the tab", got)
	}
	h.rex.userCloses(worker)
	eventually(t, "the worktree removed", func() bool {
		return slices.Equal(h.worktreesRemoved(), []string{filepath.Join(h.wt.root, "eng-1-billing")})
	})
	eventually(t, "the Repo session removed", func() bool { return slices.Equal(h.sessionsDestroyed(), []string{"session:acme/data"}) })
	if v := h.task("ENG-1"); v.Stage != "Done" || v.Outcome != "merged" || v.Tab || v.Live {
		t.Fatalf("task = %+v, want Done, merged, with no tab", v)
	}
}

func TestADoneTabTheUserClosesKeepsASessionWithAnotherTab(t *testing.T) {
	h, worker := doneWithATab(t)
	if _, err := h.rex.NewWindow(context.Background(), worker.SessionID, "another tab", "/", nil, []string{"zsh"}, false); err != nil {
		t.Fatal(err)
	}
	h.rex.userCloses(worker)
	eventually(t, "the worktree removed", func() bool { return len(h.worktreesRemoved()) == 1 })
	time.Sleep(150 * time.Millisecond)
	if got := h.sessionsDestroyed(); len(got) != 0 {
		t.Fatalf("destroyed %v while the session held another tab", got)
	}
}

func TestClosingADoneTaskClosesItsTabAndRemovesTheWorktree(t *testing.T) {
	h, worker := doneWithATab(t)
	h.do(Request{Op: "close", Task: "ENG-1"})
	eventually(t, "the tab closed", func() bool { return len(h.rex.open("claude working")) == 0 })
	eventually(t, "the worktree removed", func() bool {
		return slices.Equal(h.worktreesRemoved(), []string{filepath.Join(h.wt.root, "eng-1-billing")})
	})
	eventually(t, "the Repo session removed", func() bool { return slices.Equal(h.sessionsDestroyed(), []string{"session:acme/data"}) })
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: worker.BlockID}
	h.still("ENG-1", lifecycle.Done)
	if v := h.task("ENG-1"); v.Outcome != "merged" || v.Tab {
		t.Fatalf("task = %+v, want Done, merged, with no tab", v)
	}
	if got := h.worktreesRemoved(); len(got) != 1 {
		t.Fatalf("removed %v, want one removal", got)
	}
}

func TestACleanedUpTaskReportsNoWorktree(t *testing.T) {
	h, worker := doneWithATab(t)
	if v := h.task("ENG-1"); v.Worktree == "" {
		t.Fatalf("task = %+v, want its worktree before the close", v)
	}
	h.rex.userCloses(worker)
	eventually(t, "the worktree removed", func() bool { return len(h.worktreesRemoved()) == 1 })
	eventually(t, "the removed worktree to leave the Task", func() bool { return h.task("ENG-1").Worktree == "" })
}

func TestClosingADoneTaskTwiceRemovesItsWorktreeOnce(t *testing.T) {
	h, _ := doneWithATab(t)
	h.do(Request{Op: "close", Task: "ENG-1"})
	eventually(t, "the worktree removed", func() bool { return len(h.worktreesRemoved()) == 1 })
	eventually(t, "the removed worktree to leave the Task", func() bool { return h.task("ENG-1").Worktree == "" })
	h.do(Request{Op: "close", Task: "ENG-1"})
	h.still("ENG-1", lifecycle.Done)
	if got := h.worktreesRemoved(); len(got) != 1 {
		t.Fatalf("removed %v, want one removal for two closes", got)
	}
}

func seedDoneWithAWorktree(id string) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeMerged, Policy: testPolicy},
			Title: id, Session: "data", SessionID: "session:data", Repo: "/repos/acme/data", Branch: "b-" + id,
			Worktree: "/worktrees/b-" + id, StageSince: time.Now()}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestRecoveryRemovesTheWorktreeOfADoneTaskWhoseCleanupNeverRan(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedDoneWithAWorktree("ENG-crash"))
	eventually(t, "the worktree removed", func() bool { return slices.Equal(h.worktreesRemoved(), []string{"/worktrees/b-ENG-crash"}) })
	eventually(t, "the removed worktree to leave the Task", func() bool { return h.task("ENG-crash").Worktree == "" })
}

func TestRecoveryLeavesADoneTaskWithNoWorktreeAlone(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedDone("ENG-clean", time.Hour, false))
	h.still("ENG-clean", lifecycle.Done)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v for a Done Task with no tab and no worktree", got)
	}
}

func TestAddRefusesATaskWhoseDoneTaskKeepsItsTab(t *testing.T) {
	h, _ := doneWithATab(t)
	_, err := h.s.Handle(context.Background(), Request{Op: "add", Issue: "ENG-1"})
	if err == nil || !strings.Contains(err.Error(), "close its tab first with lineup close ENG-1") {
		t.Fatalf("add over a Done Task with a tab: err = %v, want a refusal that names the tab", err)
	}
	if v := h.task("ENG-1"); v.Stage != "Done" || !v.Tab {
		t.Fatalf("task = %+v, want the Done Task untouched", v)
	}
}

func TestArchiveClosesAnOpenTab(t *testing.T) {
	h, _ := doneWithATab(t)
	h.do(Request{Op: "archive", Task: "ENG-1"})
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); len(ids) != 0 {
		t.Fatalf("board after archive = %v", ids)
	}
	eventually(t, "the tab closed", func() bool { return len(h.rex.open("claude working")) == 0 })
	eventually(t, "the worktree removed", func() bool {
		return slices.Equal(h.worktreesRemoved(), []string{filepath.Join(h.wt.root, "eng-1-billing")})
	})
	if v := h.task("ENG-1"); v.Outcome != "merged" {
		t.Fatalf("task = %+v, want the merged outcome kept", v)
	}
}

func seedDone(id string, age time.Duration, withTab bool) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeMerged, Policy: testPolicy},
			Title: id, Session: "data", Repo: "/repos/acme/data", Branch: "b-" + id, StageSince: time.Now().Add(-age)}
		if withTab {
			r.SessionID, r.WindowID, r.BlockID = "session:data", "window:"+id, "block:"+id
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func withOpenTabs(ids ...string) func(*Deps) {
	return func(d *Deps) {
		rex := d.Rex.(*fakeRex)
		for _, id := range ids {
			rex.tabs = append(rex.tabs, &tab{Window: Window{"session:data", "window:" + id, "block:" + id}, argv: []string{"claude"}})
		}
	}
}

func TestADoneTaskWithAnOpenTabStaysOnTheBoard(t *testing.T) {
	seed := func(st *store.Store) {
		seedDone("ENG-TAB", 8*24*time.Hour, true)(st)
		seedDone("ENG-bare", 8*24*time.Hour, false)(st)
		seedDone("ENG-NEW", 24*time.Hour, false)(st)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, withOpenTabs("ENG-TAB"))
	ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView))
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"ENG-NEW", "ENG-TAB"}) {
		t.Fatalf("board = %v, want ENG-NEW and ENG-TAB", ids)
	}
	if v := h.task("ENG-TAB"); !v.Tab {
		t.Fatalf("task = %+v, want its tab shown", v)
	}
}

func TestRecoveryCleansUpADoneTaskWhoseTabClosedWhileTheSchedulerWasDown(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedDone("ENG-gone", 8*24*time.Hour, true))
	eventually(t, "the worktree removed", func() bool { return slices.Equal(h.worktreesRemoved(), []string{"b-ENG-gone"}) })
	if v := h.task("ENG-gone"); v.Tab || v.Stage != "Done" {
		t.Fatalf("task = %+v, want Done with no tab", v)
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); len(ids) != 0 {
		t.Fatalf("board = %v, want the aged-out Task gone", ids)
	}
}

func TestRecoveryKeepsEveryDoneTaskWhenRexCannotListBlocks(t *testing.T) {
	seed := func(st *store.Store) {
		seedDone("ENG-a", time.Hour, true)(st)
		seedDone("ENG-b", time.Hour, true)(st)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) {
		d.Rex.(*fakeRex).blocksErr = errors.New(`exec: "rex": executable file not found in $PATH`)
	})
	h.still("ENG-a", lifecycle.Done)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v while Rex could not list blocks", got)
	}
	for _, id := range []string{"ENG-a", "ENG-b"} {
		if v := h.task(id); !v.Tab {
			t.Fatalf("%s = %+v, want its tab record kept", id, v)
		}
	}
}

func TestRecoveryKeepsADoneTaskWhoseTabIsOpen(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedDone("ENG-open", time.Hour, true), withOpenTabs("ENG-open"))
	h.still("ENG-open", lifecycle.Done)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v for a Task whose tab is open", got)
	}
	if v := h.task("ENG-open"); !v.Tab {
		t.Fatalf("task = %+v, want its tab kept", v)
	}
}

func TestThePollOpPollsNow(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.PollInterval = time.Hour })
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	h.waitTab("claude working")
	polled := func() bool {
		h.gh.mu.Lock()
		defer h.gh.mu.Unlock()
		return slices.Contains(h.gh.polled, PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"})
	}
	if polled() {
		t.Fatal("GitHub was polled before the poll op")
	}
	h.do(Request{Op: "poll"})
	eventually(t, "a poll of ENG-1's branch", polled)
}

// A full WIP cap refuses an add, and the refused add leaves nothing: no Task,
// no event, and no number of the ad hoc counter.
func TestAnAddWithTheWIPCapFullFailsAndLeavesNothing(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "cap", N: 1})
	h.do(Request{Op: "add", Issue: "ENG-1"})
	adHoc := Request{Op: "add", Repo: "web", Text: "Fix map tooltip"}
	for _, req := range []Request{{Op: "add", Issue: "ENG-2"}, adHoc} {
		if _, err := h.s.Handle(context.Background(), req); err == nil || err.Error() != "wip cap full" {
			t.Fatalf("add %+v over the cap: err = %v, want wip cap full", req, err)
		}
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"ENG-1"}) {
		t.Fatalf("board = %v, want only ENG-1", ids)
	}
	stored, err := h.st.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if ids := slices.Sorted(maps.Keys(stored)); !slices.Equal(ids, []string{"ENG-1"}) {
		t.Fatalf("stored Tasks = %v, want only ENG-1", ids)
	}
	if events, err := h.st.Events("ENG-2"); err != nil || len(events) != 0 {
		t.Fatalf("events of the refused add = %v, %v, want none", events, err)
	}

	adHoc.OverCap = true
	v := h.do(adHoc).(TaskView)
	if v.ID != "AH-1" || v.Stage != "Working" || !v.OverCap {
		t.Fatalf("over-cap add = %+v, want ah-1 started in Working over the cap", v)
	}
	h.waitTab("wt lineup/ah-1-fix-map-tooltip")
	b := h.do(Request{Op: "ls"}).(BoardView)
	if b.Started != 2 || b.OverCap != 1 || b.Cap != 1 {
		t.Fatalf("board = %d started, %d over, cap %d", b.Started, b.OverCap, b.Cap)
	}
}

func TestFastExitBeforeNewWindowReturns(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.rex.exitFast = "wt"
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.waitTab("claude working")
}

func TestRecoveryFlagsATaskWhoseTabIsGone(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	seed := func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data",
			Branch: "ada/eng-7", Worktree: "/wt/eng-7", Conversation: "conv-7", SessionID: "session:data", WindowID: "window:gone", BlockID: "block:gone", Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, db, seed)
	h.do(Request{Op: "ls"})
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-7").Flag == "tab closed" })
	time.Sleep(100 * time.Millisecond)
	if n := h.tabs(); n != 0 {
		t.Fatalf("recovery opened %d tabs, want none until the user asks", n)
	}
	d, _ := h.s.show(context.Background(), "ENG-7")
	if d.Stage != "Working" || d.Group != "Needs you" || d.Tab || d.Live {
		t.Fatalf("recovered task = %+v", d.TaskView)
	}
	if last := d.Events[len(d.Events)-1]; last.Signal != "tab-closed" {
		t.Fatalf("last event = %+v, want tab-closed", last)
	}
}

func seedWorker(id string) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: id, Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: id, Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "b-" + id, Worktree: "/wt/" + id,
			Conversation: "conv-" + id, SessionID: "session:data", WindowID: "window:" + id, BlockID: "block:" + id, Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

// restoredTabs adds a tab for each Task that Rex brought back as an idle shell.
func restoredTabs(ids ...string) func(*Deps) {
	return func(d *Deps) {
		rex := d.Rex.(*fakeRex)
		for _, id := range ids {
			rex.tabs = append(rex.tabs, &tab{Window: Window{"session:data", "window:" + id, "block:" + id}, shell: true})
		}
	}
}

func TestRecoveryFlagsAndClosesAWorkerTabThatRexRestoredAsAShell(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedWorker("ENG-7"), restoredTabs("ENG-7"))
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-7").Flag == "tab closed" })
	eventually(t, "the restored tab closed", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		return h.rex.tabs[0].closed
	})
	if v := h.task("ENG-7"); v.Tab || v.Live || v.Group != "Needs you" {
		t.Fatalf("task = %+v, want Needs you with no tab", v)
	}
	if n := h.tabs(); n != 1 {
		t.Fatalf("recovery opened %d tabs, want none until the user asks", n-1)
	}
}

func TestRecoveryKeepsAWorkerTabWhenRexCannotReadItsProcess(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedWorker("ENG-7"), restoredTabs("ENG-7"), func(d *Deps) {
		d.Rex.(*fakeRex).idleErr = errors.New("rex block call: cannot connect to the rex server")
	})
	h.still("ENG-7", lifecycle.Working)
	if v := h.task("ENG-7"); !v.Tab || v.Flag != "" {
		t.Fatalf("task = %+v, want its tab kept", v)
	}
}

func TestRecoveryLeavesTheRestoredShellOfADoneTaskToTheUser(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedDone("ENG-done", time.Hour, true), restoredTabs("ENG-done"))
	h.still("ENG-done", lifecycle.Done)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v for a Done Task whose tab is open", got)
	}
	if v := h.task("ENG-done"); !v.Tab {
		t.Fatalf("task = %+v, want its tab kept", v)
	}
}

func TestRecoveryLeavesAWaitingTaskWithoutATabUnflagged(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	seed := func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.ReadyToLand, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data",
			Branch: "ada/eng-7", Worktree: "/wt/eng-7", Conversation: "conv-7", SessionID: "session:data", WindowID: "window:gone", BlockID: "block:gone", Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, db, seed)
	h.do(Request{Op: "ls"})
	eventually(t, "the tab forgotten", func() bool { return !h.task("ENG-7").Tab })
	if v := h.task("ENG-7"); v.Flag != "" || v.Stage != "Ready to land" {
		t.Fatalf("task = %+v, want Ready to land with no flag", v)
	}
	if n := h.tabs(); n != 0 {
		t.Fatalf("recovery opened %d tabs", n)
	}
}

// ENG-8's setup failed before its tab opened, so it has no worktree and no
// session, and its close removes nothing. ENG-2's setup failed in its tab.
func TestCloseRemovesTheWorktreeOfAFailedSetupOnly(t *testing.T) {
	seed := func(st *store.Store) {
		r := Record{
			Task: lifecycle.Task{ID: "ENG-8", Stage: lifecycle.Working, Flag: lifecycle.ReasonSetupFailed,
				Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "No tab", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "ada/eng-8",
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed)
	h.do(Request{Op: "close", Task: "ENG-8"})
	h.waitStage("ENG-8", lifecycle.Done)

	h.do(Request{Op: "add", Issue: "ENG-2"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 1)
	eventually(t, "setup failed", func() bool { return h.task("ENG-2").Flag == "setup failed" })
	h.do(Request{Op: "close", Task: "ENG-2"})
	eventually(t, "the failed setup's worktree removed", func() bool {
		h.wt.mu.Lock()
		defer h.wt.mu.Unlock()
		return len(h.wt.removed) > 0
	})
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if !slices.Equal(h.wt.removed, []string{"ada/eng-1-billing"}) {
		t.Fatalf("removed %v, want only the worktree of ENG-2's setup", h.wt.removed)
	}
}

func TestChecksThatPassedBeforeCIRunningStillCount(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")

	h.gh.set(key, 42, "pr-open", "ci-passed")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}
	h.waitStage("ENG-1", lifecycle.ReadyToLand)
}

func TestAStopShowsTheWorkerLastLine(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")

	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done", Msg: "  PR #5042: \"1.0\" or \"1.0.0\"?\nsecond line"}}
	eventually(h.t, "ENG-1 stopped with the last line", func() bool {
		v := h.task("ENG-1")
		return v.Flag == "stopped" && v.FlagDetail == `PR #5042: "1.0" or "1.0.0"?`
	})
}

func TestRecoveryLeavesAFlaggedSetupForTheUser(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	seed := func(st *store.Store) {
		r := Record{
			Task: lifecycle.Task{ID: "ENG-8", Stage: lifecycle.Working, Flag: lifecycle.ReasonSetupFailed,
				Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Flagged", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "ada/eng-8",
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, db, seed)
	h.do(Request{Op: "ls"})
	time.Sleep(100 * time.Millisecond)
	if n := len(h.rex.open("wt ada/eng-8")); n != 0 {
		t.Fatalf("recovery reran setup for a Task flagged setup failed (%d tabs)", n)
	}
	if v := h.task("ENG-8"); v.Flag != "setup failed" {
		t.Fatalf("flag = %q", v.Flag)
	}
}

func TestViewCarriesTheSkillOverride(t *testing.T) {
	r := &Record{ID: "ENG-1", Overrides: map[string]string{"skill": "kit:tdd"}}
	if got := r.view().Skill; got != "kit:tdd" {
		t.Errorf("Skill = %q", got)
	}
	if got := (&Record{ID: "ENG-2"}).view().Skill; got != "" {
		t.Errorf("Skill without an override = %q", got)
	}
}

func TestTheViewShowsTheLandingBar(t *testing.T) {
	five := 5
	bar := lifecycle.LandingBar{Checks: lifecycle.RequiredChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals,
		Bot: &lifecycle.ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: 4, TopScore: 5}}
	r := &Record{Task: lifecycle.Task{ID: "ENG-590", Stage: lifecycle.ReadyToLand, Policy: lifecycle.Policy{Bar: bar}}, PR: &PRSnapshot{Number: 42}}
	r.Standing = bar.Measure(lifecycle.PRFacts{Head: testHead, Score: &five, Decision: lifecycle.DecisionApproved,
		Checks: []lifecycle.Check{{Name: "test", State: lifecycle.CheckPassed, Required: true}, {Name: "lint", State: lifecycle.CheckFailed}}})
	v := r.view()
	if v.Note != "#42 ✓ approved · ready to land · optional failed: lint" || !slices.Equal(v.Optional, []string{"lint"}) {
		t.Errorf("note %q optional %q", v.Note, v.Optional)
	}
	want := []string{"✓ required checks passed", "✓ Greptile 5/5", "✓ review threads done", "✓ comments answered", "✓ required reviews approved", "! optional checks failed: lint"}
	if got := r.checklist(); !slices.Equal(got, want) {
		t.Errorf("checklist %q, want %q", got, want)
	}

	r.Stage = lifecycle.InReview
	r.Standing = bar.Measure(lifecycle.PRFacts{Head: testHead, Score: &five, Decision: lifecycle.DecisionRequired})
	if v := r.view(); v.Note != "#42 ○ no review yet" || r.checklist()[4] != "✗ needs approval" {
		t.Errorf("In review: note %q checklist %q", v.Note, r.checklist())
	}
	r.Stage, r.Standing.Comments = lifecycle.CIRunning, 2
	if v := r.view(); v.Note != "#42 ○ no review yet · 2 new comments" || r.checklist()[3] != "✗ 2 new comments" {
		t.Errorf("new comments: note %q checklist %q", v.Note, r.checklist())
	}
	if got := (&Record{Task: lifecycle.Task{ID: "ENG-591", Stage: lifecycle.Working}}).checklist(); got != nil {
		t.Errorf("a Task with no measured PR: checklist %q", got)
	}
}

func boardIDs(v BoardView) []string {
	var ids []string
	for _, t := range v.Tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func TestArchiveHidesADoneTaskAndKeepsItsHistory(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	_, err := h.s.Handle(context.Background(), Request{Op: "archive", Task: "ENG-1"})
	if err == nil || err.Error() != "archive is not allowed in Working" {
		t.Fatalf("archive in Working: err = %v", err)
	}
	h.do(Request{Op: "close", Task: "ENG-1"})
	h.waitStage("ENG-1", lifecycle.Done)
	h.do(Request{Op: "archive", Task: "ENG-1"})
	h.do(Request{Op: "archive", Task: "ENG-1"})

	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); len(ids) != 0 {
		t.Fatalf("board after archive = %v", ids)
	}
	d, _ := h.s.show(context.Background(), "ENG-1")
	var archives int
	for _, e := range d.Events {
		if e.Signal == "archive" {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("archive events = %d, want 1: %+v", archives, d.Events)
	}

	h.do(Request{Op: "add", Issue: "ENG-1"})
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"ENG-1"}) {
		t.Fatalf("board after re-add = %v", ids)
	}
}

func TestDoneTasksAgeOffTheBoard(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	seed := func(st *store.Store) {
		for id, age := range map[string]time.Duration{"ENG-OLD": 8 * 24 * time.Hour, "ENG-NEW": 24 * time.Hour} {
			r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeClosed, Policy: testPolicy},
				Title: id, StageSince: time.Now().Add(-age)}
			data, _ := json.Marshal(r)
			st.Save(r.ID, data, nil)
		}
	}
	h := newHarness(t, db, seed)
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"ENG-NEW"}) {
		t.Fatalf("board = %v, want only ENG-NEW", ids)
	}
	if v := h.task("ENG-OLD"); v.Stage != "Done" {
		t.Fatalf("aged-out task = %+v", v)
	}
}

// The view holds the time that each Task reached Done in the last 48 hours,
// an archived Task included, so that the Board can count the Tasks done since
// 00:00 yesterday.
func TestTheViewHoldsTheTimeOfEachTaskDoneInTheLast48Hours(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	seed := func(st *store.Store) {
		for _, x := range []struct {
			id       string
			stage    lifecycle.Stage
			age      time.Duration
			archived bool
		}{
			{"ENG-1", lifecycle.Done, time.Hour, false},
			{"ENG-2", lifecycle.Done, 20 * time.Hour, true},
			{"ENG-3", lifecycle.Done, 30 * time.Hour, false},
			{"ENG-4", lifecycle.Done, 47 * time.Hour, false},
			{"ENG-5", lifecycle.Done, 72 * time.Hour, false},
			{"ENG-6", lifecycle.Working, time.Hour, false},
		} {
			r := Record{Task: lifecycle.Task{ID: x.id, Stage: x.stage, Policy: testPolicy}, Title: x.id,
				StageSince: c.now().Add(-x.age), Archived: x.archived}
			data, _ := json.Marshal(r)
			st.Save(r.ID, data, nil)
		}
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) { d.Now = c.now })
	v := h.do(Request{Op: "ls"}).(BoardView)
	var ages []time.Duration
	for _, at := range v.Done {
		ages = append(ages, c.now().Sub(at))
	}
	slices.Sort(ages)
	if want := []time.Duration{time.Hour, 20 * time.Hour, 30 * time.Hour, 47 * time.Hour}; !slices.Equal(ages, want) {
		t.Errorf("the view holds Done times %v ago, want %v", ages, want)
	}
	if ids := boardIDs(v); slices.Contains(ids, "ENG-2") {
		t.Errorf("board = %v, want no archived ENG-2", ids)
	}
}

func TestTaskWithNoIssueGetsAnAhID(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Repo: "web", Text: "Fix map tooltip"})
	h.do(Request{Op: "add", Repo: "web", Text: "Second"})
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"AH-1", "AH-2"}) {
		t.Fatalf("board = %v, want AH-1 and AH-2", ids)
	}
}

func TestATaskWithNoIssueTakesAPlainTitleCutAtAWord(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	for _, c := range []struct{ text, want string }{
		{"Start a sessions to explore the ideas and UI here:\n- https://x.com/abc\n- more", "Start a sessions to explore the ideas and UI here"},
		{"Move the nightly export job off the legacy pricing tables and onto the new views", "Move the nightly export job off the legacy pricing tables"},
		{"Rename the config options, the flags, and the env variables: then the docs", "Rename the config options, the flags, and the env variables"},
		{strings.Repeat("é", 70), strings.Repeat("é", 60)},
	} {
		v := h.do(Request{Op: "add", Repo: "web", Text: c.text}).(TaskView)
		if got := h.task(v.ID).Title; got != c.want {
			t.Errorf("text %q: title %q, want %q", c.text, got, c.want)
		}
	}
}

func TestTaskWithNoIssueOpensInItsRepoSession(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Repo: "web", Text: "Fix map tooltip"})
	h.do(Request{Op: "add", Issue: "ENG-1"})

	sessions := map[string]string{}
	eventually(t, "both setup tabs", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		for _, tb := range h.rex.tabs {
			sessions[tb.label] = tb.SessionID
		}
		return len(sessions) == 2
	})
	if s := sessions["AH-1 Fix map tooltip"]; s != "session:acme/web" {
		t.Errorf("text Task session = %q, want session:acme/web (all: %v)", s, sessions)
	}
	if s := sessions["ENG-1 Add billing model"]; s != "session:acme/data" {
		t.Errorf("Issue Task session = %q, want session:acme/data", s)
	}
	if v := h.task("AH-1"); v.Repo != "web" {
		t.Errorf("text Task Repo column = %q, want web", v.Repo)
	}
}

// ownersConfig resolves a Repo name owner/folder to /repos/owner/folder, so
// two Repos can share a folder name.
type ownersConfig struct{ fakeConfig }

func (ownersConfig) ResolveRepo(name string) (string, error) { return "/repos/" + name, nil }
func (ownersConfig) SetUp(repo string) (string, bool) {
	return filepath.Base(filepath.Dir(repo)) + "/" + filepath.Base(repo), true
}

func TestTwoReposWithOneFolderNameOpenInTwoSessions(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config = ownersConfig{} })
	h.do(Request{Op: "add", Repo: "acme/data", Text: "Fix the export"})
	h.do(Request{Op: "add", Repo: "other/data", Text: "Fix the import"})

	sessions := map[string]string{}
	eventually(t, "both setup tabs", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		for _, tb := range h.rex.tabs {
			sessions[tb.label] = tb.SessionID
		}
		return len(sessions) == 2
	})
	if s := sessions["AH-1 Fix the export"]; s != "session:acme/data" {
		t.Errorf("acme/data session = %q, want session:acme/data (all: %v)", s, sessions)
	}
	if s := sessions["AH-2 Fix the import"]; s != "session:other/data" {
		t.Errorf("other/data session = %q, want session:other/data (all: %v)", s, sessions)
	}
	if v := h.task("AH-2"); v.Repo != "other-data" {
		t.Errorf("other/data Repo column = %q, want other-data", v.Repo)
	}
}

func TestARepoSessionUnderItsFolderNameTakesTheRepoKey(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	seed := func(st *store.Store) {
		for _, id := range []string{"ENG-7", "ENG-8"} {
			r := Record{
				Task:  lifecycle.Task{ID: id, Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
				Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data",
				Branch: "ada/" + id, Worktree: "/wt/" + id, Conversation: "conv-" + id, SessionID: "session:data",
				WindowID: "window:" + id, BlockID: "block:" + id, Job: JobWorker, WorkerStage: new(lifecycle.Working),
			}
			data, _ := json.Marshal(r)
			st.Save(r.ID, data, nil)
		}
	}
	h := newHarness(t, db, seed, func(d *Deps) {
		d.Rex.(*fakeRex).tabs = []*tab{
			{Window: Window{"session:data", "window:ENG-7", "block:ENG-7"}, argv: []string{"claude"}, program: "working"},
			{Window: Window{"session:data", "window:ENG-8", "block:ENG-8"}, argv: []string{"claude"}, program: "working"},
		}
	})
	h.do(Request{Op: "ls"})
	h.rex.mu.Lock()
	renamed := slices.Clone(h.rex.renamed)
	h.rex.mu.Unlock()
	if want := []string{"session:data acme/data"}; !slices.Equal(renamed, want) {
		t.Fatalf("renamed = %q, want %q", renamed, want)
	}
	for _, id := range []string{"ENG-7", "ENG-8"} {
		r, _ := h.s.snapshot(id)
		if r.Session != "acme/data" || r.WindowID != "window:"+id || r.BlockID != "block:"+id {
			t.Errorf("%s session %q, tab %s %s, want acme/data and its tab", id, r.Session, r.WindowID, r.BlockID)
		}
	}
}

func TestARecordFromBeforeRepoLabelShowsItsSession(t *testing.T) {
	r := &Record{Task: lifecycle.Task{ID: "B-1"}, Session: "backend"}
	if got := r.view().Repo; got != "backend" {
		t.Errorf("Repo = %q, want backend", got)
	}
}

func TestKindOfReadsTheTrackerFromTheID(t *testing.T) {
	for id, want := range map[string]string{
		"ENG-614":       KindLinear,
		"LOCAL-7":       KindLocal, // a typed local- always means the Local Tracker
		"local-7":       KindLocal,
		"hod-3":         KindLocal, // a Repo prefix, though it has the form of a Linear ID
		"HOD-3":         KindLocal,
		"web-app-2":     KindLocal,
		"dex-3":         KindLinear, // no Repo has the prefix dex
		"hod-0":         KindLinear,
		"owner/repo#12": KindGitHub,
		"lineup#12":     KindLinear, // no owner: not a GitHub ID
	} {
		if got := KindOf(id, []string{"hod", "web-app"}); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestAnIssueFromBeforeKindIsLinear(t *testing.T) {
	var is Issue
	if err := json.Unmarshal([]byte(`{"workspace":"w","identifier":"ENG-1","team_key":"ENG"}`), &is); err != nil {
		t.Fatal(err)
	}
	if is.Tracker() != KindLinear {
		t.Fatalf("Tracker() = %q, want linear", is.Tracker())
	}
}

func TestAddPassesTheBrowsersKindToTheTracker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "LOCAL-1", Kind: KindLinear})
	h.do(Request{Op: "add", Issue: "ENG-2"})
	if want := []string{KindLinear, ""}; !slices.Equal(h.tr.kinds, want) {
		t.Fatalf("Lookup kinds = %q, want %q", h.tr.kinds, want)
	}
}

// trackerConfig gives /repos/acme/data, the Repo of every mapping, the Local
// Tracker, and /repos/acme/web no Tracker.
type trackerConfig struct{ fakeConfig }

func (trackerConfig) RepoTracker(repo string) (string, string) {
	key := "acme/" + filepath.Base(repo)
	if repo == "/repos/acme/data" {
		return key, KindLocal
	}
	return key, ""
}

func TestAddRefusesAnIssueOfAnotherTrackerThanItsRepos(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config = trackerConfig{} })
	for _, req := range []Request{
		{Op: "add", Issue: "ENG-1"},
		{Op: "add", Issue: "acme/data#3", Kind: KindGitHub},
		{Op: "add", Issue: "ENG-2", Repo: "data"},
	} {
		_, err := h.s.Handle(context.Background(), req)
		if err == nil {
			t.Fatalf("add %s: no error, want a refusal", req.Issue)
		}
		for _, want := range []string{"acme/data", "its Tracker local", req.Issue, "is a " + cmp.Or(req.Kind, KindLinear) + " Issue"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("add %s: error %q does not name %q", req.Issue, err, want)
			}
		}
	}
	h.do(Request{Op: "add", Issue: "LOCAL-1", Kind: KindLocal})
	h.do(Request{Op: "add", Repo: "data", Text: "tidy the README"})
	ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView))
	slices.Sort(ids)
	if want := []string{"AH-1", "LOCAL-1"}; !slices.Equal(ids, want) {
		t.Fatalf("Tasks = %v, want %v", ids, want)
	}
}

func TestARepoWithNoTrackerTakesOnlyAdHocTasks(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config = trackerConfig{} })
	for _, req := range []Request{
		{Op: "add", Issue: "ENG-3", Repo: "web"},
		{Op: "add", Issue: "LOCAL-2", Kind: KindLocal, Repo: "web"},
	} {
		if _, err := h.s.Handle(context.Background(), req); err == nil || err.Error() != "repo has no tracker: acme/web" {
			t.Fatalf("add %s --repo web: err = %v, want repo has no tracker: acme/web", req.Issue, err)
		}
	}
	if v := h.do(Request{Op: "add", Repo: "web", Text: "tidy the README"}).(TaskView); v.Repo != "web" {
		t.Fatalf("the ad hoc Task %+v, want it in web", v)
	}
}

// A setup tab can exit before the Scheduler queues the Worker, as when the
// worktree already exists. The Task must still open one setup tab only.
func TestAFastSetupOpensOneSetupTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.rex.exitFast = "wt"
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.waitTab("claude working")
	h.rex.mu.Lock()
	defer h.rex.mu.Unlock()
	var setups int
	for _, tb := range h.rex.tabs {
		if tb.argv[0] == "wt" {
			setups++
		}
	}
	if setups != 1 {
		t.Fatalf("opened %d setup tabs, want 1", setups)
	}
}

// gatedConfig gates every Task on Greptile.
type gatedConfig struct{ fakeConfig }

func (gatedConfig) Policy(string, map[string]string) (lifecycle.Policy, error) {
	p := testPolicy
	p.Budgets.ReviewCycles = 2
	p.Bar.Bot = &lifecycle.ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: 5, TopScore: 5}
	return p, nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (h *harness) polls() int {
	h.gh.mu.Lock()
	defer h.gh.mu.Unlock()
	return len(h.gh.polled)
}

// waitPolls waits for n more polls, so the ticks before them have run.
func (h *harness) waitPolls(n int) {
	for start := h.polls(); h.polls() < start+n; {
		time.Sleep(5 * time.Millisecond)
	}
}

// A gated Task waits in CI running for the bot's score, flags the user when
// no score comes, and starts a review cycle for a low score.
func TestAGatedTaskWaitsForTheBotThenFlagsIt(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config, d.Now = gatedConfig{}, c.now })
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing", Bot: "greptile"}

	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}
	eventually(t, "stopped", func() bool { return h.task("ENG-1").Flag == "stopped" })
	h.gh.set(key, 42, "pr-open")
	h.waitStage("ENG-1", lifecycle.CIRunning)

	h.gh.set(key, 42, "review-required")
	eventually(t, "the wait note", func() bool { return h.task("ENG-1").Note == "#42 ○ no review yet · waiting for Greptile" })
	c.add(14 * time.Minute)
	h.waitPolls(3)
	if v := h.task("ENG-1"); v.Stage != "CI running" || v.Flag != "" {
		t.Fatalf("flagged before the wait ended: %+v", v)
	}
	c.add(2 * time.Minute)
	eventually(t, "the no bot review flag", func() bool {
		v := h.task("ENG-1")
		return v.Stage == "CI running" && v.Note == "no bot review: Greptile did not score c0ffee1"
	})

	h.gh.set(key, 42, "scored-low")
	h.waitStage("ENG-1", lifecycle.Responding)
	h.waitTyped(worker.BlockID, "prompt responding")
	if v := h.task("ENG-1"); v.Note != "#42 ○ no review yet · answering review" {
		t.Fatalf("review cycle note = %q", v.Note)
	}
	h.hn.mu.Lock()
	spec := h.hn.specs[len(h.hn.specs)-1]
	h.hn.mu.Unlock()
	if spec.Stage != lifecycle.Responding || spec.ReviewCycle == nil || spec.ReviewCycle.Score != 4 || spec.ReviewCycle.MinScore != 5 ||
		spec.Bar.Bot == nil || !spec.Standing.ScoreLow {
		t.Fatalf("worker spec %+v, want a Responding round for Greptile 4 of 5", spec)
	}
}

// A pr-seen can change the Standing and nothing else. The poll saves it
// anyway, so the Board shows it and a restart keeps it.
func TestAPollSavesTheStanding(t *testing.T) {
	gated, _ := gatedConfig{}.Policy("", nil)
	seed := func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: "ENG-7", Stage: lifecycle.CIRunning, Policy: gated, Left: gated.Budgets},
			Title: "Backfill events", RepoSlug: "acme/data", Branch: "ada/eng-7", StageSince: time.Now()}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) { d.PollInterval = time.Hour })
	h.gh.set(PRKey{Repo: "acme/data", Branch: "ada/eng-7", Bot: "greptile"}, 42, "review-required")
	h.do(Request{Op: "poll"})
	eventually(t, "the Standing saved by one poll", func() bool {
		raw, err := h.st.Tasks()
		var saved Record
		return err == nil && json.Unmarshal(raw["ENG-7"], &saved) == nil &&
			saved.Stage == lifecycle.CIRunning && saved.Standing.ScoreDue && saved.Standing.Head == testHead
	})
}

// A Task that waited on a PR which closed before pr-closed existed has the
// closed PR as its stored snapshot. The first poll after the upgrade flags it,
// and later polls change nothing.
func TestAStuckTaskWithAClosedPRGetsTheFlagOnceAtTheFirstPoll(t *testing.T) {
	closed := PRSnapshot{Number: 5149, URL: "https://github.com/acme/data/pull/5149", Raw: json.RawMessage(`["pr-closed"]`)}
	seed := func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: "ENG-7", Stage: lifecycle.InReview, Policy: lifecycle.Policy{}, Left: lifecycle.Budgets{}},
			Title: "Backfill events", RepoSlug: "acme/data", Branch: "ada/eng-7", StageSince: time.Now(), PR: &closed}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) { d.PollInterval = time.Hour })
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-7"}
	h.gh.set(key, 5149, "pr-closed")
	h.do(Request{Op: "poll"})
	eventually(t, "the pr closed flag", func() bool { return h.task("ENG-7").Flag == "pr closed" })
	v := h.task("ENG-7")
	if v.StageKey != "in_review" || v.Group != lifecycle.GroupNeedsYou.String() ||
		v.Note != "pr closed: #5149 closed without a merge" {
		t.Fatalf("stage %s, group %s, note %q", v.StageKey, v.Group, v.Note)
	}
	h.do(Request{Op: "poll"})
	h.do(Request{Op: "poll"})
	events, err := h.st.Events("ENG-7")
	if err != nil || len(events) != 1 || events[0].Signal != "pr-closed" {
		t.Fatalf("events %+v, err %v: want one pr-closed transition", events, err)
	}

	h.gh.set(key, 5149, "review-required")
	h.do(Request{Op: "poll"})
	eventually(t, "the flag cleared by a reopened PR", func() bool { return h.task("ENG-7").Flag == "" })
	if got := h.task("ENG-7").StageKey; got != "in_review" {
		t.Fatalf("stage %s after the reopen", got)
	}
}

func (h *harness) waitSpecs(n int) WorkerSpec {
	h.t.Helper()
	var got WorkerSpec
	eventually(h.t, fmt.Sprintf("%d Worker commands", n), func() bool {
		h.hn.mu.Lock()
		defer h.hn.mu.Unlock()
		if len(h.hn.specs) < n {
			return false
		}
		got = h.hn.specs[n-1]
		return true
	})
	return got
}

func (h *harness) closeAndFlag(worker tab) {
	h.t.Helper()
	h.rex.userCloses(worker)
	eventually(h.t, "the tab closed flag", func() bool { return h.task("ENG-1").Flag == "tab closed" })
}

func (h *harness) open(id string) OpenResult {
	h.t.Helper()
	return h.do(Request{Op: "open", Task: id}).(OpenResult)
}

func (h *harness) openFails(id string) error {
	h.t.Helper()
	_, err := h.s.Handle(context.Background(), Request{Op: "open", Task: id})
	if err == nil {
		h.t.Fatalf("open %s: no error", id)
	}
	return err
}

func TestAClosedTabOpensNoTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.closeAndFlag(worker)
	time.Sleep(150 * time.Millisecond)
	if n := h.tabs(); n != 2 {
		t.Fatalf("%d tabs were opened, want the setup tab and the Worker's tab only", n)
	}
	v := h.task("ENG-1")
	if v.Stage != "Working" || v.Group != "Needs you" || v.Note != "tab closed" || v.Tab || v.Live {
		t.Fatalf("task = %+v, want Working, Needs you, tab closed, with no tab", v)
	}
}

func TestOpenResumesTheConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	h.closeAndFlag(worker)

	res := h.open("ENG-1")
	if res.Action != OpenRestored {
		t.Fatalf("open = %+v, want restored", res)
	}
	resumed := h.waitTab("claude --resume " + first.Conversation)
	if resumed.cwd != filepath.Join(h.wt.root, "eng-1-billing") || resumed.BlockID == worker.BlockID {
		t.Fatalf("resumed tab = %+v, want a new tab in the Task's worktree", resumed)
	}
	h.status(resumed.BlockID, "idle")
	time.Sleep(100 * time.Millisecond)
	if got := h.rex.typedInto(resumed.BlockID); len(got) != 0 {
		t.Fatalf("typed %q into the restored Worker, want nothing: its Stage prompt arrived long ago", got)
	}
	eventually(t, "the restored Worker live", func() bool { return h.task("ENG-1").Live })
	if v := h.task("ENG-1"); v.Stage != "Working" || v.Flag != "" || !v.Tab {
		t.Fatalf("task = %+v, want Working with no flag and a tab", v)
	}
	h.rex.mu.Lock()
	focused := slices.Clone(h.rex.focused)
	h.rex.mu.Unlock()
	if !slices.Contains(focused, resumed.BlockID) {
		t.Fatalf("focused %v, want the restored tab %s", focused, resumed.BlockID)
	}
}

func TestOpenStartsAFreshWorkerWithNoConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "skip the backfill"})
	h.s.call(func() { h.s.recs["ENG-1"].Conversation = "" })
	h.closeAndFlag(worker)
	if n := h.tabs(); n != 2 {
		t.Fatalf("%d tabs after the close, want 2", n)
	}

	res := h.open("ENG-1")
	if res.Action != OpenStarted {
		t.Fatalf("open = %+v, want started", res)
	}
	fresh := h.waitTab("claude working")
	if fresh.BlockID == worker.BlockID {
		t.Fatal("no new tab")
	}
	spec := h.waitSpecs(2)
	if !spec.Restart || !slices.Equal(spec.Steers, []string{"use a dual write", "skip the backfill"}) || spec.Conversation == "" {
		t.Fatalf("fresh spec = restart %v, steers %q, conversation %q", spec.Restart, spec.Steers, spec.Conversation)
	}
	eventually(t, "the fresh Worker live", func() bool { return h.task("ENG-1").Live })
	if v := h.task("ENG-1"); v.Flag != "" {
		t.Fatalf("flag = %q, want none", v.Flag)
	}
}

func TestOpenStartsAFreshWorkerWhenTheConversationFileIsGone(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	h.closeAndFlag(worker)
	h.hn.mu.Lock()
	h.hn.gone = []string{first.Conversation}
	h.hn.mu.Unlock()

	res := h.open("ENG-1")
	if res.Action != OpenStarted {
		t.Fatalf("open = %+v, want started", res)
	}
	h.waitTab("claude working")
	if n := len(h.rex.open("claude --resume " + first.Conversation)); n != 0 {
		t.Fatalf("%d resume tabs opened for a conversation that is gone", n)
	}
	spec := h.lastSpec()
	if !spec.Restart || !slices.Equal(spec.Steers, []string{"use a dual write"}) || spec.Conversation == first.Conversation || spec.Conversation == "" {
		t.Fatalf("fresh spec = restart %v, steers %q, conversation %q, want a new conversation that replays the steer", spec.Restart, spec.Steers, spec.Conversation)
	}
}

func TestAFreshWorkerReplaysTheSteersOfEveryStage(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	h.prOpens(worker)
	h.ciFails()
	h.waitTyped(worker.BlockID, "use a dual write", "prompt ci_debug")
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "leave the lint alone"})
	h.s.call(func() { h.s.recs["ENG-1"].Conversation = "" })
	h.rex.userCloses(worker)
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Flag == "tab closed" })

	h.open("ENG-1")
	h.waitTab("claude ci_debug")
	spec := h.lastSpec()
	if spec.Stage != lifecycle.CIDebug || !spec.Restart || !slices.Equal(spec.Steers, []string{"use a dual write", "leave the lint alone"}) {
		t.Fatalf("fresh CI debug spec = stage %s restart %v steers %q, want both steers from the Task's life", spec.Stage, spec.Restart, spec.Steers)
	}
}

func TestOpenFocusesALiveTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	tabs := h.tabs()
	res := h.open("ENG-1")
	if res.Action != OpenFocused {
		t.Fatalf("open = %+v, want focused", res)
	}
	h.rex.mu.Lock()
	focused := slices.Clone(h.rex.focused)
	h.rex.mu.Unlock()
	if !slices.Equal(focused, []string{worker.BlockID}) || h.tabs() != tabs {
		t.Fatalf("focused %v, %d tabs, want the Worker's tab and no new tab", focused, h.tabs())
	}
}

// A GitHub Task's tab from before its label lost the owner still belongs to
// the Task, and a restore resumes the same conversation under the new label.
func TestOpenFindsTheTabAndConversationOfAGitHubTaskFromBeforeTheShortLabel(t *testing.T) {
	const id = "acme/data#7"
	seed := func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: id, Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Issue: &Issue{Kind: KindGitHub, Identifier: id, Title: "Backfill events"},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "acme/data", Branch: "ada/data-7",
			Worktree: "/wt/data-7", Conversation: "conv-7", SessionID: "session:data", WindowID: "window:" + id, BlockID: "block:" + id, Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	old := &tab{Window: Window{"session:data", "window:" + id, "block:" + id}, label: id + " Backfill events", argv: []string{"claude"}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) { d.Rex.(*fakeRex).tabs = []*tab{old} })

	if res := h.open(id); res.Action != OpenFocused {
		t.Fatalf("open = %+v, want the old tab focused", res)
	}
	h.rex.mu.Lock()
	focused := slices.Clone(h.rex.focused)
	h.rex.mu.Unlock()
	if !slices.Equal(focused, []string{old.BlockID}) || h.tabs() != 1 {
		t.Fatalf("focused %v, %d tabs, want the old tab and no new tab", focused, h.tabs())
	}

	h.rex.userCloses(*old)
	eventually(t, "the tab closed flag", func() bool { return h.task(id).Flag == "tab closed" })
	if res := h.open(id); res.Action != OpenRestored {
		t.Fatalf("open = %+v, want restored", res)
	}
	resumed := h.waitTab("claude --resume conv-7")
	if resumed.label != "data#7 Backfill events" {
		t.Fatalf("resumed tab label %q, want data#7 Backfill events", resumed.label)
	}
}

func TestOpenRefusesATaskWithNothingToOpen(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	if err := h.openFails("ENG-9"); !errors.Is(err, lifecycle.ErrUnknownTask) {
		t.Fatalf("unknown: %v", err)
	}
	h = newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTabRecorded("claude reviewing", "PR-7")
	merged := openPR(7, "h1")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	eventually(t, "the review tab closed", func() bool { v := h.task("PR-7"); return v.Stage == "Done" && !v.Tab })
	if err := h.openFails("PR-7"); err.Error() != "PR-7 is Done and its worktree is gone" {
		t.Fatalf("done review: %v", err)
	}
	if n := len(h.rex.open("claude reviewing")); n != 0 {
		t.Fatalf("open left %d Worker tabs", n)
	}
}

func TestOpenFocusesADoneTaskThatKeepsItsTab(t *testing.T) {
	h, worker := doneWithATab(t)
	if res := h.open("ENG-1"); res.Action != OpenFocused {
		t.Fatalf("open = %+v, want focused", res)
	}
	h.rex.mu.Lock()
	defer h.rex.mu.Unlock()
	if !slices.Equal(h.rex.focused, []string{worker.BlockID}) {
		t.Fatalf("focused %v", h.rex.focused)
	}
}

func TestOpenRefusesAWaitingTaskWithNoConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.s.call(func() { h.s.recs["ENG-1"].Conversation = "" })
	h.rex.userCloses(worker)
	eventually(t, "the tab gone", func() bool { return !h.task("ENG-1").Tab })
	if err := h.openFails("ENG-1"); err.Error() != "ENG-1 has no conversation to restore in CI running" {
		t.Fatalf("open: %v", err)
	}
}

func TestAPendingStageWaitsForTheRestore(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Now, d.PromptWait = c.now, time.Minute })
	worker := h.working()
	first := h.waitSpecs(1)
	h.prOpens(worker)
	h.rex.userCloses(worker)
	eventually(t, "the tab gone", func() bool { return !h.task("ENG-1").Tab })
	if v := h.task("ENG-1"); v.Flag != "" {
		t.Fatalf("flag = %q in CI running, want none: no Worker is needed there", v.Flag)
	}

	h.ciFails()
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Note == "tab closed: CI debug waits for the Worker" })
	c.add(2 * time.Minute)
	h.waitPolls(3)
	if v := h.task("ENG-1"); v.Flag != "tab closed" || v.Stage != "CI debug" {
		t.Fatalf("task = %+v, want CI debug flagged tab closed, not worker silent", v)
	}
	if n := h.tabs(); n != 2 {
		t.Fatalf("%d tabs, want no new tab until the user asks", n)
	}
	var waiting bool
	h.s.call(func() { waiting = h.s.recs["ENG-1"].Pending != nil })
	if !waiting {
		t.Fatal("the CI debug prompt is not pending")
	}

	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + first.Conversation)
	h.waitPolls(3)
	if v := h.task("ENG-1"); v.Flag != "" {
		t.Fatalf("flag = %q after the restore, want none: the wait for the Worker's first status starts at the restore", v.Flag)
	}
	if got := h.rex.typedInto(resumed.BlockID); len(got) != 0 {
		t.Fatalf("typed %q before the restored Worker was idle", got)
	}
	h.status(resumed.BlockID, "idle")
	h.waitTyped(resumed.BlockID, "prompt ci_debug")
	if spec := h.lastSpec(); spec.Stage != lifecycle.CIDebug || spec.Restart || len(spec.Steers) != 0 {
		t.Fatalf("typed spec = %+v, want a CI debug prompt with no restart note", spec)
	}
}

func TestARerunWithNoTabWaitsForTheRestore(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.closeAndFlag(worker)
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})
	eventually(t, "the waiting Stage named", func() bool { return h.task("ENG-1").Note == "tab closed: Working waits for the Worker" })
	if n := h.tabs(); n != 2 {
		t.Fatalf("%d tabs, want no new tab", n)
	}
}

func TestASlowOpenKeepsAPromptThatARerunSetAgain(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	hold := make(chan struct{})
	h.rex.mu.Lock()
	h.rex.hold = hold
	h.rex.mu.Unlock()
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	eventually(t, "the Worker command built", func() bool {
		h.hn.mu.Lock()
		defer h.hn.mu.Unlock()
		return len(h.hn.specs) > 0
	})
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})
	close(hold)

	worker := h.waitTab("claude working")
	h.status(worker.BlockID, "idle")
	h.waitTyped(worker.BlockID, "prompt working")
}

// reopenAndExit reopens a closed Review ready Worker and exits it with code 1,
// with the given text on its screen. It returns the exited Worker's tab.
func (h *harness) reopenAndExit(capture string) tab {
	h.t.Helper()
	reviewing := h.readyReview()
	h.rex.mu.Lock()
	h.rex.capture = capture
	h.rex.mu.Unlock()
	h.review(prURL7)
	worker := h.waitTab("claude --resume " + reviewing.Conversation)
	h.rex.events <- RexEvent{Kind: EvExited, ExitCode: 1, BlockID: worker.BlockID}
	return worker
}

func TestResumeFallsBackToSessionID(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.reopenAndExit("Error: " + claude.NoConversation + " with session ID: x")

	h.waitNewTab("claude reviewing", worker.BlockID)
	spec := h.lastSpec()
	if spec.Stage != lifecycle.Reviewing || spec.Conversation == "" {
		t.Fatalf("fallback spec = %+v, want a Reviewing start with a conversation, so Command passes --session-id and a prompt", spec)
	}
	d, _ := h.s.show(context.Background(), "PR-7")
	if d.Stage != lifecycle.Reviewing.String() || d.Note != "#7 reviewing" {
		t.Fatalf("task = %+v, want Reviewing with no flag", d.TaskView)
	}
	var fallbacks int
	for _, e := range d.Events {
		if e.Signal == replacedSignal {
			fallbacks++
		}
	}
	if fallbacks != 1 {
		t.Fatalf("%s events = %d, want 1: %+v", replacedSignal, fallbacks, d.Events)
	}
}

// Review ready is not an agent Stage, so a failed reopen raises no flag. The
// fallback must still not fire for another failure.
func TestOtherExitDoesNotFallBack(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.reopenAndExit("Error: session ID is already in use")
	h.still("PR-7", lifecycle.ReviewReady)
	d, _ := h.s.show(context.Background(), "PR-7")
	for _, e := range d.Events {
		if e.Signal == replacedSignal {
			t.Fatalf("%s event for another failure: %+v", replacedSignal, d.Events)
		}
	}
	if tabs := h.rex.open("claude reviewing"); len(tabs) != 0 {
		t.Fatalf("open Reviewing tabs = %d, want none", len(tabs))
	}
}

func TestABuildResumeWithNoConversationStartsAFreshWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.rex.mu.Lock()
	h.rex.capture = "Error: " + claude.NoConversation + " with session ID: x"
	h.rex.mu.Unlock()
	h.closeAndFlag(worker)
	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + first.Conversation)
	h.rex.events <- RexEvent{Kind: EvExited, ExitCode: 1, BlockID: resumed.BlockID}

	fresh := h.waitTab("claude working")
	var conversation string
	h.s.call(func() { conversation = h.s.recs["ENG-1"].Conversation })
	if fresh.BlockID == worker.BlockID || conversation == "" || conversation == first.Conversation {
		t.Fatalf("fresh tab %s, conversation %q, want a new Worker in a new conversation after %q", fresh.BlockID, conversation, first.Conversation)
	}
	if v := h.task("ENG-1"); v.Stage != "Working" || v.Flag != "" {
		t.Fatalf("task = %+v, want Working with no flag", v)
	}
}

var buildKey = PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}

func (h *harness) prOpens(worker tab) {
	h.t.Helper()
	h.status(worker.BlockID, "done")
	eventually(h.t, "stopped", func() bool { return h.task("ENG-1").Flag == "stopped" })
	h.gh.set(buildKey, 42, "pr-open")
	h.waitStage("ENG-1", lifecycle.CIRunning)
}

func (h *harness) ciFails() {
	h.t.Helper()
	h.gh.set(buildKey, 42, "ci-failed")
	h.waitStage("ENG-1", lifecycle.CIDebug)
}

func (h *harness) waitTyped(block string, want ...string) {
	h.t.Helper()
	eventually(h.t, fmt.Sprintf("%q typed into %s", want, block), func() bool { return slices.Equal(h.rex.typedInto(block), want) })
}

func (h *harness) tabs() int {
	h.rex.mu.Lock()
	defer h.rex.mu.Unlock()
	return len(h.rex.tabs)
}

func (h *harness) events(id string) []string {
	h.t.Helper()
	d, err := h.s.show(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, e := range d.Events {
		out = append(out, e.Signal)
	}
	return out
}

func TestTheNextStageTypesIntoTheSameWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	opened := h.tabs()
	h.ciFails()

	h.waitTyped(worker.BlockID, "prompt ci_debug")
	if n := h.tabs(); n != opened {
		t.Fatalf("CI debug opened %d new tabs, want none", n-opened)
	}
	if v := h.task("ENG-1"); !v.Live || !v.Tab {
		t.Fatalf("task = %+v, want the Working Worker live in its tab", v)
	}
	var stage *lifecycle.Stage
	var conversation string
	h.s.call(func() { stage, conversation = h.s.recs["ENG-1"].WorkerStage, h.s.recs["ENG-1"].Conversation })
	if stage == nil || *stage != lifecycle.CIDebug || conversation == "" {
		t.Fatalf("worker stage %v, conversation %q, want CI debug in the Working conversation", stage, conversation)
	}
}

func TestABuildWorkerStartsItsConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.working()
	spec := h.waitSpecs(1)
	var conversation string
	h.s.call(func() { conversation = h.s.recs["ENG-1"].Conversation })
	if spec.Conversation == "" || spec.Conversation != conversation {
		t.Fatalf("Working spec conversation %q, record %q, want one new ID in both", spec.Conversation, conversation)
	}
}

func TestThePromptWaitsForABusyWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.status(worker.BlockID, "working")
	h.waitWorkerState("ENG-1", "working")
	h.ciFails()

	time.Sleep(100 * time.Millisecond)
	if got := h.rex.typedInto(worker.BlockID); len(got) != 0 {
		t.Fatalf("typed %q into a working Worker", got)
	}
	h.status(worker.BlockID, "blocked")
	time.Sleep(50 * time.Millisecond)
	if got := h.rex.typedInto(worker.BlockID); len(got) != 0 {
		t.Fatalf("typed %q into a blocked Worker", got)
	}
	h.status(worker.BlockID, "done")
	h.waitTyped(worker.BlockID, "prompt ci_debug")
}

func TestARerunTypesThePromptAgain(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.ciFails()
	h.waitTyped(worker.BlockID, "prompt ci_debug")

	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "idle")
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "ci_debug"})
	h.waitTyped(worker.BlockID, "prompt ci_debug", "prompt ci_debug")
}

func TestARerunWaitsWhileTheWorkerReadsATypedPrompt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.ciFails()
	h.waitTyped(worker.BlockID, "prompt ci_debug")

	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "ci_debug"})
	time.Sleep(300 * time.Millisecond)
	if got := h.rex.typedInto(worker.BlockID); len(got) != 1 {
		t.Fatalf("typed %q: the second prompt went in before the Worker reported working", got)
	}
	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "idle")
	h.waitTyped(worker.BlockID, "prompt ci_debug", "prompt ci_debug")
}

func TestAPromptThatWaitsOnASilentWorkerFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.PromptWait = 100 * time.Millisecond })
	worker := h.working()
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})

	eventually(h.t, "the silent Worker flag", func() bool { return h.task("ENG-1").Flag == string(lifecycle.ReasonWorkerSilent) })
	if got := h.rex.typedInto(worker.BlockID); len(got) != 0 {
		t.Fatalf("typed %q into a Worker of unknown state", got)
	}
	h.status(worker.BlockID, "idle")
	h.waitTyped(worker.BlockID, "prompt working")
	h.status(worker.BlockID, "working")
	eventually(h.t, "the flag clears", func() bool { return h.task("ENG-1").Flag == "" })
}

func TestAPromptThatWaitsOnAReportingWorkerRaisesNoFlag(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.PromptWait = 50 * time.Millisecond })
	worker := h.working()
	h.status(worker.BlockID, "working")
	h.waitWorkerState("ENG-1", "working")
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})

	time.Sleep(300 * time.Millisecond)
	if f := h.task("ENG-1").Flag; f != "" {
		t.Fatalf("flag = %q, want none: the Worker reports its turn", f)
	}
}

type failTypes struct {
	*fakeRex
	left atomic.Int32
}

func (f *failTypes) Type(ctx context.Context, session, block, text string) error {
	if f.left.Add(-1) >= 0 {
		return errors.New("rex write failed")
	}
	return f.fakeRex.Type(ctx, session, block, text)
}

type noPrompts struct {
	Harness
	empty atomic.Bool
}

func (n *noPrompts) Prompt(ctx context.Context, w WorkerSpec) (string, error) {
	if n.empty.Load() {
		return "", nil
	}
	return n.Harness.Prompt(ctx, w)
}

// gateTypes holds the first Type until release closes, then fails it.
type gateTypes struct {
	*fakeRex
	started, release chan struct{}
	once             sync.Once
}

func (g *gateTypes) Type(ctx context.Context, session, block, text string) error {
	first := false
	g.once.Do(func() { first = true })
	if !first {
		return g.fakeRex.Type(ctx, session, block, text)
	}
	g.started <- struct{}{}
	<-g.release
	return errors.New("block target not found")
}

func TestAFailedTypeOnATabThatClosedFlagsTheTask(t *testing.T) {
	var rex *gateTypes
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		rex = &gateTypes{fakeRex: d.Rex.(*fakeRex), started: make(chan struct{}, 1), release: make(chan struct{})}
		d.Rex = rex
	})
	worker := h.working()
	first := h.waitSpecs(1)
	h.prOpens(worker)
	h.ciFails()
	<-rex.started
	h.rex.exits(worker, 0)
	eventually(t, "the exit read", func() bool { return !h.task("ENG-1").Live })
	close(rex.release)

	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Note == "tab closed: CI debug waits for the Worker" })
	var pending bool
	h.s.call(func() { pending = h.s.recs["ENG-1"].Pending != nil })
	if !pending {
		t.Fatal("the CI debug prompt is not pending")
	}
	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + first.Conversation)
	h.status(resumed.BlockID, "idle")
	h.waitTyped(resumed.BlockID, "prompt ci_debug")
}

func TestARerunAfterAFailedTypeTypesThePrompt(t *testing.T) {
	var rex *failTypes
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		rex = &failTypes{fakeRex: d.Rex.(*fakeRex)}
		rex.left.Store(1)
		d.Rex = rex
	})
	worker := h.working()
	h.prOpens(worker)
	h.ciFails()
	eventually(t, "the error flag", func() bool { return h.task("ENG-1").Flag == "error" })

	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "ci_debug"})
	h.waitTyped(worker.BlockID, "prompt ci_debug")
}

func TestAnEmptyPromptLeavesTheWorkerReadyForTheNextPrompt(t *testing.T) {
	var prompts *noPrompts
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		prompts = &noPrompts{Harness: d.Harness}
		d.Harness = prompts
	})
	worker := h.working()
	h.status(worker.BlockID, "idle")
	h.waitWorkerState("ENG-1", "idle")
	prompts.empty.Store(true)
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})
	time.Sleep(100 * time.Millisecond)
	if got := h.rex.typedInto(worker.BlockID); len(got) != 0 {
		t.Fatalf("typed %q for an empty prompt", got)
	}

	prompts.empty.Store(false)
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})
	h.waitTyped(worker.BlockID, "prompt working")
}

func TestAWorkerThatExitsBeforeItsPromptFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.status(worker.BlockID, "working")
	h.waitWorkerState("ENG-1", "working")
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})
	h.rex.exits(worker, 0)
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Note == "tab closed: Working waits for the Worker" })
	time.Sleep(100 * time.Millisecond)
	if n := h.tabs(); n != 2 {
		t.Fatalf("%d tabs, want no tab until the user asks", n)
	}

	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + first.Conversation)
	h.status(resumed.BlockID, "idle")
	h.waitTyped(resumed.BlockID, "prompt working")
}

func seedPendingWorker(since time.Time) func(*store.Store) {
	return func(st *store.Store) {
		pending := lifecycle.Working
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data",
			Branch: "ada/eng-7", Worktree: "/wt/eng-7", SessionID: "session:data", WindowID: "window:live", BlockID: "block:live",
			Job: JobWorker, Pending: &pending, PendingSince: since,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestARestartedSchedulerGivesAPendingWorkerAFullPromptWait(t *testing.T) {
	cases := []struct {
		name      string
		since     time.Time
		blocksErr error
	}{
		{"saved before the restart", time.Now().Add(-time.Hour), nil},
		{"saved before the field existed", time.Time{}, nil},
		{"with Rex unable to list blocks", time.Time{}, errors.New("rex is down")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
			rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, blocksErr: c.blocksErr}
			h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedPendingWorker(c.since), func(d *Deps) {
				d.Rex = rex
				d.PromptWait = 400 * time.Millisecond
			})
			time.Sleep(100 * time.Millisecond)
			if f := h.task("ENG-7").Flag; f != "" {
				t.Fatalf("flag = %q within 100 ms of the restart, want none", f)
			}
			eventually(t, "the silent Worker flag", func() bool { return h.task("ENG-7").Flag == string(lifecycle.ReasonWorkerSilent) })
		})
	}
}

func TestAWorkerBetweenStagesMeetsNoContract(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.status(worker.BlockID, "working")
	h.waitWorkerState("ENG-1", "working")
	h.ciFails()
	before := len(h.events("ENG-1"))

	h.status(worker.BlockID, "done")
	h.waitTyped(worker.BlockID, "prompt ci_debug")
	time.Sleep(100 * time.Millisecond)
	if v := h.task("ENG-1"); v.Stage != "CI debug" || v.Flag != "" {
		t.Fatalf("task = %+v, want CI debug with no flag: the done was the user's chat", v)
	}
	if got := h.events("ENG-1")[before:]; slices.Contains(got, "done") {
		t.Fatalf("events after the prompt waited = %q, want no done", got)
	}

	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "done")
	eventually(t, "the CI debug contract checked", func() bool { return h.task("ENG-1").Flag == "stopped" })
}

func TestAStatusFromBeforeATypedPromptDoesNotCompleteTheStage(t *testing.T) {
	for _, late := range []string{"done", "idle"} {
		t.Run(late, func(t *testing.T) {
			h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
			worker := h.working()
			h.prOpens(worker)
			h.ciFails()
			h.waitTyped(worker.BlockID, "prompt ci_debug")
			before := len(h.events("ENG-1"))

			h.status(worker.BlockID, late)
			time.Sleep(300 * time.Millisecond)
			if v := h.task("ENG-1"); v.Stage != "CI debug" || v.Flag != "" {
				t.Fatalf("task = %+v, want CI debug with no flag: the %s ended the previous turn", v, late)
			}
			h.waitWorkerState("ENG-1", "prompted")
			if got := h.events("ENG-1")[before:]; slices.Contains(got, "done") {
				t.Fatalf("events after the prompt = %q, want no done", got)
			}

			h.status(worker.BlockID, "working")
			h.waitWorkerState("ENG-1", "working")
			h.status(worker.BlockID, "done")
			eventually(t, "the CI debug contract checked", func() bool { return h.task("ENG-1").Flag == "stopped" })
		})
	}
}

func TestAStatusFromBeforeATypedPromptDoesNotTypeTheNextPrompt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.ciFails()
	h.waitTyped(worker.BlockID, "prompt ci_debug")
	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "ci_debug"})

	h.status(worker.BlockID, "done")
	h.status(worker.BlockID, "idle")
	time.Sleep(300 * time.Millisecond)
	if got := h.rex.typedInto(worker.BlockID); len(got) != 1 {
		t.Fatalf("typed %q: a status from before the first prompt released the second", got)
	}
	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "idle")
	h.waitTyped(worker.BlockID, "prompt ci_debug", "prompt ci_debug")
}

func TestAPromptTheWorkerNeverAnswersFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.PromptWait = 200 * time.Millisecond })
	worker := h.working()
	h.prOpens(worker)
	h.ciFails()
	h.waitTyped(worker.BlockID, "prompt ci_debug")

	eventually(t, "the silent Worker flag", func() bool { return h.task("ENG-1").Flag == string(lifecycle.ReasonWorkerSilent) })
	h.status(worker.BlockID, "working")
	eventually(t, "the flag clears", func() bool { return h.task("ENG-1").Flag == "" })
}

func seedPromptedWorker(since time.Time) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data",
			Branch: "ada/eng-7", Worktree: "/wt/eng-7", SessionID: "session:data", WindowID: "window:live", BlockID: "block:live",
			Job: JobWorker, WorkerState: promptedState, WorkerStage: new(lifecycle.Working), PendingSince: since,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestARestartedSchedulerKeepsAPromptedWorkerPrompted(t *testing.T) {
	live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedPromptedWorker(time.Now().Add(-time.Hour)), func(d *Deps) {
		d.Rex = rex
		d.PromptWait = 400 * time.Millisecond
	})
	h.status("block:live", "done")
	time.Sleep(100 * time.Millisecond)
	if f := h.task("ENG-7").Flag; f != "" {
		t.Fatalf("flag = %q within 100 ms of the restart, want none: the done may end the turn before the prompt", f)
	}
	eventually(t, "the silent Worker flag", func() bool { return h.task("ENG-7").Flag == string(lifecycle.ReasonWorkerSilent) })
	h.status("block:live", "working")
	eventually(t, "the flag clears", func() bool { return h.task("ENG-7").Flag == "" })
}

func TestARefusedOpenLeavesTheConversationSoAPromptOpensNoTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.prOpens(worker)
	h.rex.userCloses(worker)
	eventually(t, "the tab gone", func() bool { return !h.task("ENG-1").Tab })
	h.hn.mu.Lock()
	h.hn.gone = []string{first.Conversation}
	h.hn.mu.Unlock()
	h.openFails("ENG-1")
	var kept string
	h.s.call(func() { kept = h.s.recs["ENG-1"].Conversation })
	if kept != first.Conversation {
		t.Fatalf("a refused open changed the conversation to %q", kept)
	}

	tabs := h.tabs()
	h.ciFails()
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Note == "tab closed: CI debug waits for the Worker" })
	time.Sleep(300 * time.Millisecond)
	if n := h.tabs(); n != tabs {
		t.Fatalf("%d tabs, want %d: a prompt opened a tab with no ask from the user", n, tabs)
	}
}

// A Task from before conversation IDs has had a Worker but holds no conversation.
func TestAPromptOpensNoTabForATaskThatHadAWorkerButHoldsNoConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.prOpens(worker)
	h.s.call(func() { h.s.recs["ENG-1"].Conversation = "" })
	h.rex.userCloses(worker)
	eventually(t, "the tab gone", func() bool { return !h.task("ENG-1").Tab })

	tabs := h.tabs()
	h.ciFails()
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Note == "tab closed: CI debug waits for the Worker" })
	time.Sleep(300 * time.Millisecond)
	if n := h.tabs(); n != tabs {
		t.Fatalf("%d tabs, want %d: a prompt opened a tab with no ask from the user", n, tabs)
	}
	h.open("ENG-1")
	h.waitTab("claude ci_debug")
}

func TestAStoredWorkerLostFlagClearsWhenTheUserOpensTheTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.closeAndFlag(worker)
	h.s.call(func() { h.s.recs["ENG-1"].Flag = "worker lost" })
	h.open("ENG-1")
	eventually(t, "the restored Worker live", func() bool { return h.task("ENG-1").Live })
	if f := h.task("ENG-1").Flag; f != "" {
		t.Fatalf("flag = %q after the open, want none", f)
	}
}

func TestRecoveryReplacesAStoredWorkerLostFlag(t *testing.T) {
	seed := func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Flag: "worker lost", Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", WorkerStage: new(lifecycle.Working),
			Branch: "ada/eng-7", Worktree: "/wt/eng-7", Conversation: "conv-7", SessionID: "session:data", WindowID: "window:gone", BlockID: "block:gone", Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed)
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-7").Flag == "tab closed" })
}

func TestTwoOpensAtOnceOpenOneTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.closeAndFlag(worker)
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.s.Handle(context.Background(), Request{Op: "open", Task: "ENG-1"})
		}()
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)
	if n := h.tabs(); n != 3 {
		t.Fatalf("%d tabs, want the setup tab, the first Worker, and one restored Worker", n)
	}
}

func TestOpenOfAReviewReadyTaskWithNoConversationFocusesTheNewTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady))
	h.open("R-3")
	tab := h.waitTab("claude reviewing")
	eventually(t, "the new tab focused", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		return slices.Contains(h.rex.focused, tab.BlockID)
	})
}

// A failed resume in a Stage that waits for others has no prompt to start over
// with, so the Task keeps its conversation.
func TestAFailedResumeInAWaitingStageKeepsTheConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.prOpens(worker)
	h.rex.userCloses(worker)
	eventually(t, "the tab gone", func() bool { return !h.task("ENG-1").Tab })
	h.rex.mu.Lock()
	h.rex.capture = "Error: " + claude.NoConversation + " with session ID: x"
	h.rex.mu.Unlock()
	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + first.Conversation)
	tabs := h.tabs()
	h.rex.events <- RexEvent{Kind: EvExited, ExitCode: 1, BlockID: resumed.BlockID}
	time.Sleep(300 * time.Millisecond)
	var kept string
	h.s.call(func() { kept = h.s.recs["ENG-1"].Conversation })
	if kept != first.Conversation || h.tabs() != tabs {
		t.Fatalf("conversation %q, %d tabs, want %q and %d: the failed resume must change nothing", kept, h.tabs(), first.Conversation, tabs)
	}
}

// keptTabRestore exits the Worker with code 0 and leaves its tab open, as Rex
// does for a program that ends within a second, then restores the Worker. It
// returns the new tab.
func (h *harness) keptTabRestore(id string, kept tab, conversation string) tab {
	h.t.Helper()
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: kept.BlockID, ExitCode: 0}
	eventually(h.t, "the Worker gone", func() bool { v := h.task(id); return !v.Live && v.Tab })
	h.open(id)
	reopened := h.waitNewTab("claude --resume "+conversation, kept.BlockID)
	h.rex.mu.Lock()
	calls := slices.Clone(h.rex.calls)
	h.rex.mu.Unlock()
	closeAt, newAt := slices.Index(calls, "close "+kept.WindowID), slices.Index(calls, "new "+reopened.WindowID)
	if closeAt < 0 || newAt < 0 || closeAt > newAt {
		h.t.Fatalf("calls %v, want the kept window %s closed before the new window %s opens", calls, kept.WindowID, reopened.WindowID)
	}
	var window string
	h.s.call(func() { window = h.s.recs[id].WindowID })
	if window != reopened.WindowID {
		h.t.Fatalf("the Task's window is %s, want only the new window %s", window, reopened.WindowID)
	}
	return reopened
}

func TestOpenClosesTheKeptTabOfABuildWorkerBeforeTheNewTabOpens(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.keptTabRestore("ENG-1", worker, first.Conversation)
	eventually(t, "the restored Worker live", func() bool { return h.task("ENG-1").Live })
	if tabs := h.rex.open("claude working"); len(tabs) != 0 {
		t.Fatalf("the kept tab stayed open: %+v", tabs)
	}
	h.do(Request{Op: "close", Task: "ENG-1"})
	eventually(t, "the restored tab closed", func() bool { return len(h.rex.open("claude --resume "+first.Conversation)) == 0 })
}

// The user types /exit, and a rerun types the CI debug prompt into the exiting
// Worker. The exiting Worker sends a late done, then its program exits, then
// Rex closes its block. The prompt was lost, so the Task waits for the restore.
func TestAPromptTypedIntoAnExitingWorkerWaitsForTheRestore(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	first := h.waitSpecs(1)
	h.prOpens(worker)
	h.ciFails()
	h.waitTyped(worker.BlockID, "prompt ci_debug")

	h.status(worker.BlockID, "done")
	h.waitWorkerState("ENG-1", "prompted")
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: worker.BlockID, ExitCode: 0}
	h.rex.blockCloses(worker)

	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Note == "tab closed: CI debug waits for the Worker" })
	var pending bool
	h.s.call(func() { pending = h.s.recs["ENG-1"].Pending != nil })
	if !pending {
		t.Fatal("the CI debug prompt is not pending")
	}

	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + first.Conversation)
	h.status(resumed.BlockID, "idle")
	h.waitTyped(resumed.BlockID, "prompt ci_debug")
}

// An exit and then a close of the same block still flags the Task.
func TestABlockThatClosesAfterItsProgramExitedFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: worker.BlockID, ExitCode: 0}
	eventually(t, "the Worker gone", func() bool { return !h.task("ENG-1").Live })
	h.rex.blockCloses(worker)
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Flag == "tab closed" })
}

// The user can close the setup tab after worktrunk exits and before the Worker
// opens in it. That close is not a lost Worker, so the Worker opens in a new tab.
func TestASetupTabThatTheUserClosesGivesTheWorkerANewTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	hold := make(chan struct{})
	h.rex.mu.Lock()
	h.rex.hold = hold
	h.rex.mu.Unlock()
	h.do(Request{Op: "add", Issue: "ENG-1"})
	setup := h.waitTabRecorded("wt ada/eng-1-billing", "ENG-1")
	h.rex.exits(setup, 0)
	eventually(t, "the Worker command built", func() bool {
		h.hn.mu.Lock()
		defer h.hn.mu.Unlock()
		return len(h.hn.specs) > 0
	})
	h.rex.userCloses(setup)
	eventually(t, "the setup tab forgotten", func() bool { return !h.task("ENG-1").Tab })
	close(hold)

	worker := h.waitTab("claude working")
	if worker.WindowID == setup.WindowID {
		t.Fatalf("the Worker runs in the closed setup tab %s", setup.WindowID)
	}
	eventually(t, "a live Worker", func() bool { return h.task("ENG-1").Live })
	if got := h.events("ENG-1"); slices.Contains(got, "tab-closed") {
		t.Fatalf("events = %q, want no tab-closed: the setup tab had no Worker", got)
	}
	if f := h.task("ENG-1").Flag; f != "" {
		t.Fatalf("flag = %q, want none", f)
	}
}

// A failed setup keeps its tab, so the user can read worktrunk's error.
func TestAFailedSetupKeepsItsTabUntilTheUserClosesTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	setup := h.waitTabRecorded("wt ada/eng-1-billing", "ENG-1")
	h.rex.exits(setup, 1)
	eventually(t, "the setup failed flag", func() bool { return h.task("ENG-1").Flag == "setup failed" })
	time.Sleep(100 * time.Millisecond)
	h.rex.mu.Lock()
	calls := slices.Clone(h.rex.calls)
	h.rex.mu.Unlock()
	if v := h.task("ENG-1"); !v.Tab || !slices.Equal(calls, []string{"new " + setup.WindowID}) {
		t.Fatalf("task %+v and tab calls %q, want the setup tab kept", v, calls)
	}

	h.do(Request{Op: "close", Task: "ENG-1"})
	eventually(t, "the setup tab closed", func() bool { return len(h.rex.open("wt ada/eng-1-billing")) == 0 })
}

func TestARerunAfterAFailedSetupReplacesItsTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	failed := h.waitTabRecorded("wt ada/eng-1-billing", "ENG-1")
	h.rex.exits(failed, 1)
	eventually(t, "the setup failed flag", func() bool { return h.task("ENG-1").Flag == "setup failed" })

	h.do(Request{Op: "rerun", Task: "ENG-1", Stage: "working"})
	again := h.waitNewTab("wt ada/eng-1-billing", failed.BlockID)
	h.rex.mu.Lock()
	calls := slices.Clone(h.rex.calls)
	h.rex.mu.Unlock()
	if want := []string{"new " + failed.WindowID, "close " + failed.WindowID, "new " + again.WindowID}; !slices.Equal(calls, want) {
		t.Fatalf("tab calls = %q, want %q", calls, want)
	}
}

// keptSetupTab seeds a Task whose setup tab Rex kept, with job as the Record's
// Job, and adds the tab to Rex with exited marking worktrunk's exit with code.
func keptSetupTab(job Job, exited bool, code int) (func(*store.Store), func(*Deps)) {
	seed := func(st *store.Store) {
		pending := lifecycle.Working
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "ada/eng-7",
			SessionID: "session:data", WindowID: "window:kept", BlockID: "block:kept", Job: job, Pending: &pending,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	tabs := func(d *Deps) {
		rex := d.Rex.(*fakeRex)
		rex.tabs = append(rex.tabs, &tab{Window: Window{"session:data", "window:kept", "block:kept"}, argv: []string{"wt", "ada/eng-7"}, keep: true, exited: exited, code: code})
	}
	return seed, tabs
}

func TestRecoveryClosesAKeptSetupTabAndSetsUpAgain(t *testing.T) {
	seed, tabs := keptSetupTab(JobExited, true, 0)
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, tabs)
	setup := h.waitNewTab("wt ada/eng-7", "block:kept")
	h.rex.mu.Lock()
	calls := slices.Clone(h.rex.calls)
	h.rex.mu.Unlock()
	if want := []string{"close window:kept", "new " + setup.WindowID}; !slices.Equal(calls, want) {
		t.Fatalf("tab calls = %q, want %q", calls, want)
	}
}

func TestRecoveryKeepsTheTabOfAFailedSetup(t *testing.T) {
	_, tabs := keptSetupTab(JobExited, true, 1)
	seed := func(st *store.Store) {
		r := Record{
			Task: lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Flag: lifecycle.ReasonSetupFailed,
				Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "ada/eng-7",
			SessionID: "session:data", WindowID: "window:kept", BlockID: "block:kept", Job: JobExited,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, tabs)
	h.do(Request{Op: "ls"})
	time.Sleep(100 * time.Millisecond)
	h.rex.mu.Lock()
	calls := slices.Clone(h.rex.calls)
	h.rex.mu.Unlock()
	if len(calls) != 0 || !h.task("ENG-7").Tab {
		t.Fatalf("tab calls = %q, Tab = %v, want the failed setup's tab kept", calls, h.task("ENG-7").Tab)
	}
}

func TestRecoveryGoesOnFromASetupThatExitedWhileTheSchedulerWasDown(t *testing.T) {
	seed, tabs := keptSetupTab(JobSetup, true, 0)
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, tabs)
	worker := h.waitTab("claude working")
	if worker.WindowID != "window:kept" {
		t.Fatalf("the Worker runs in %s, want the setup tab window:kept", worker.WindowID)
	}
	eventually(t, "a live Worker", func() bool { return h.task("ENG-7").Live })
}

func TestRecoveryFlagsASetupThatFailedWhileTheSchedulerWasDown(t *testing.T) {
	seed, tabs := keptSetupTab(JobSetup, true, 1)
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, tabs)
	eventually(t, "the setup failed flag", func() bool { return h.task("ENG-7").Flag == "setup failed" })
	if !h.task("ENG-7").Tab || len(h.rex.open("wt ada/eng-7")) != 1 {
		t.Fatal("recovery closed the failed setup's tab")
	}
}

func TestRecoveryWaitsForASetupThatStillRuns(t *testing.T) {
	seed, tabs := keptSetupTab(JobSetup, false, 0)
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, tabs)
	h.still("ENG-7", lifecycle.Working)
	if n := h.tabs(); n != 1 || !h.task("ENG-7").Tab {
		t.Fatalf("%d tabs, want the running setup tab only", n)
	}
	h.rex.exits(tab{Window: Window{"session:data", "window:kept", "block:kept"}}, 0)
	if worker := h.waitTab("claude working"); worker.WindowID != "window:kept" {
		t.Fatalf("the Worker runs in %s, want the setup tab window:kept", worker.WindowID)
	}
}

func TestTheWorkerStartsInTheSetupTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	setup := h.waitTab("wt ada/eng-1-billing")
	h.rex.exits(setup, 0)
	worker := h.waitTab("claude working")
	if worker.WindowID != setup.WindowID {
		t.Fatalf("the Worker runs in %s, want the setup tab %s", worker.WindowID, setup.WindowID)
	}
	eventually(t, "a live Worker", func() bool { return h.task("ENG-1").Live })
	h.rex.mu.Lock()
	calls := slices.Clone(h.rex.calls)
	h.rex.mu.Unlock()
	if !slices.Equal(calls, []string{"new " + setup.WindowID}) {
		t.Fatalf("tab calls = %q, want one new tab and no close", calls)
	}
	if got := h.events("ENG-1"); slices.Contains(got, "tab-closed") {
		t.Fatalf("events = %q, want no tab-closed", got)
	}
}

// The side pane splits the Worker's block after the setup block closes, so
// the Worker and its pane share the tab.
func TestTheSidePaneOpensAfterTheSetupBlockCloses(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	if pane := h.waitSplit(1); pane.anchor != worker.BlockID || len(pane.beside) != 0 {
		t.Fatalf("side pane = %+v, want it beside the Worker's block %s alone", pane, worker.BlockID)
	}
}
