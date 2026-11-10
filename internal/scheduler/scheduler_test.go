package scheduler

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

type tab struct {
	Window
	label  string
	cwd    string
	env    map[string]string
	argv   []string
	closed bool
}

// split is a block that Split opened beside the anchor block of a tab.
type split struct {
	BlockID, anchor, cwd, window string
	argv                         []string
	closed                       bool
}

type fakeRex struct {
	mu     sync.Mutex
	n      int
	tabs   []*tab
	splits []*split
	// splitErr fails every Split call.
	splitErr  error
	destroyed []string
	events    chan RexEvent
	// exitFast makes a tab whose argv starts with this word exit with 0
	// before NewWindow returns.
	exitFast string
}

func (f *fakeRex) EnsureSession(_ context.Context, label string) (string, error) {
	return "session:" + label, nil
}

func (f *fakeRex) NewWindow(_ context.Context, session, label, cwd string, env map[string]string, argv []string) (Window, error) {
	if utf8.RuneCountInString(label) > 128 {
		return Window{}, errors.New("label must be at most 128 characters")
	}
	f.mu.Lock()
	f.n++
	w := Window{session, fmt.Sprintf("window:%d", f.n), fmt.Sprintf("block:%d", f.n)}
	f.tabs = append(f.tabs, &tab{Window: w, label: label, cwd: cwd, env: env, argv: argv})
	f.mu.Unlock()
	if f.exitFast != "" && argv[0] == f.exitFast {
		f.events <- RexEvent{Kind: EvExited, BlockID: w.BlockID}
	}
	return w, nil
}

func (f *fakeRex) Split(_ context.Context, _, block, cwd string, argv []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.splitErr != nil {
		return "", f.splitErr
	}
	for _, t := range f.tabs {
		if t.BlockID == block {
			f.n++
			f.splits = append(f.splits, &split{BlockID: fmt.Sprintf("split:%d", f.n), anchor: block, cwd: cwd, window: t.WindowID, argv: argv})
			return f.splits[len(f.splits)-1].BlockID, nil
		}
	}
	return "", fmt.Errorf("block %s not found", block)
}

func (f *fakeRex) CloseWindow(_ context.Context, _, window string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tabs {
		if t.WindowID == window {
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

func (f *fakeRex) ProgramStatus(context.Context, string, string) (Status, error) {
	return Status{}, nil
}
func (f *fakeRex) Focus(context.Context, string, string, string) error     { return nil }
func (f *fakeRex) Capture(context.Context, string, string) (string, error) { return "", nil }

func (f *fakeRex) Type(context.Context, string, string, string) error { return nil }

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
	// addErr fails the next AddReview call.
	addErr error
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
func (w *fakeWorktrees) Remove(_ context.Context, _, branch string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removed = append(w.removed, branch)
	return nil
}

type fakeHarness struct {
	mu sync.Mutex
	// trusted lists each folder that Prepare trusted.
	trusted []string
	specs   []WorkerSpec
}

func (h *fakeHarness) Prepare(_ context.Context, _, worktree string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trusted = append(h.trusted, worktree)
	return nil
}

func (h *fakeHarness) Command(_ context.Context, w WorkerSpec) ([]string, map[string]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.specs = append(h.specs, w)
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
	return Issue{Identifier: id, Title: title, BranchName: "ada/eng-1-billing", TeamKey: "ENG"}, nil
}
func (t *fakeTracker) Begin(context.Context, Issue) error { return nil }
func (t *fakeTracker) SetStatus(_ context.Context, _ Issue, event string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = append(t.status, event)
	return nil
}

type fakeConfig struct{}

var testPolicy = lifecycle.Policy{Budgets: lifecycle.Budgets{CIDebug: 3, Responding: 3, Restarts: 2}}

func (fakeConfig) Policy(string, map[string]string) (lifecycle.Policy, error) { return testPolicy, nil }
func (fakeConfig) RepoForIssue(Issue) (string, bool)                          { return "/repos/acme/data", true }
func (fakeConfig) ResolveRepo(name string) (string, error)                    { return "/repos/acme/" + name, nil }
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
		t: t, rex: &fakeRex{events: make(chan RexEvent, 16)},
		gh: &fakeGitHub{prs: map[PRKey]PRSnapshot{}, reviews: map[ReviewKey]ReviewSnapshot{}}, wt: &fakeWorktrees{root: t.TempDir()},
		hn: &fakeHarness{}, tr: &fakeTracker{}, st: st, stateHome: t.TempDir(),
	}
	d := Deps{
		Rex: h.rex, GitHub: h.gh, Worktrees: h.wt, Harness: h.hn, Tracker: h.tr, Config: fakeConfig{},
		Store: st, DefaultCap: 5, PollInterval: 20 * time.Millisecond, AdHocSession: "ad hoc", StateHome: h.stateHome,
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
	v, err := h.s.Handle(context.Background(), req)
	if err != nil {
		h.t.Fatalf("%s: %v", req.Op, err)
	}
	return v
}

func (h *harness) task(id string) TaskView {
	h.t.Helper()
	d, err := h.s.show(id)
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

func TestALongTitleCutsTheTabLabel(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.tr.title = strings.Repeat("é", 200)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})

	setup := h.waitTab("wt ada/eng-1-billing")
	want := "ENG-1 " + strings.Repeat("é", 121) + "…"
	if setup.label != want {
		t.Fatalf("setup tab label = %q (%d characters), want %q", setup.label, utf8.RuneCountInString(setup.label), want)
	}
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: setup.BlockID}
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
	if v := h.task("ENG-1"); v.Stage != "Queued" || v.Repo != "data" || v.Kind != lifecycle.KindBuild {
		t.Fatalf("added task = %+v", v)
	}
	h.do(Request{Op: "start", Task: "ENG-1"})

	setup := h.waitTab("wt ada/eng-1-billing")
	if setup.cwd != "/repos/acme/data" || setup.label != "ENG-1 Add billing model" {
		t.Fatalf("setup tab = %+v", setup)
	}
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: setup.BlockID}
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
	debugger := h.waitTab("claude ci_debug")
	if v := h.task("ENG-1"); v.Note != "attempt 1/3" {
		t.Fatalf("CI debug note = %q", v.Note)
	}
	h.gh.set(key, 42, "pushed")
	h.waitStage("ENG-1", lifecycle.CIRunning)
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: debugger.BlockID, Status: Status{State: "done"}}

	h.gh.set(key, 42, "review-required")
	h.waitStage("ENG-1", lifecycle.InReview)
	if v := h.task("ENG-1"); v.Note != "#42 needs approval" {
		t.Fatalf("In review note = %q", v.Note)
	}
	h.gh.set(key, 42, "review-approved")
	h.waitStage("ENG-1", lifecycle.ReadyToLand)

	h.do(Request{Op: "merge", Task: "ENG-1"})
	h.waitStage("ENG-1", lifecycle.Done)
	eventually(t, "cleanup", func() bool {
		h.wt.mu.Lock()
		defer h.wt.mu.Unlock()
		return slices.Contains(h.wt.removed, "ada/eng-1-billing")
	})
	if v := h.task("ENG-1"); v.Outcome != "merged" || v.PRNumber != 42 {
		t.Fatalf("done task = %+v", v)
	}
	eventually(t, "the Repo session removed", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		return slices.Contains(h.rex.destroyed, "session:data")
	})
	if len(h.rex.open("claude ci_debug")) != 0 {
		t.Fatal("a Worker tab is still open after Done")
	}
	h.tr.mu.Lock()
	defer h.tr.mu.Unlock()
	if !slices.Equal(h.tr.status, []string{"started", "in_review", "merged"}) {
		t.Fatalf("tracker events = %v", h.tr.status)
	}
}

func TestThePollOpPollsNow(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.PollInterval = time.Hour })
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID}
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

func TestWIPCapFullFailsStart(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "cap", N: 1})
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "add", Repo: "web", Text: "Fix map tooltip"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	if _, err := h.s.Handle(context.Background(), Request{Op: "start", Task: "adhoc-1"}); err == nil || err.Error() != "wip cap full" {
		t.Fatalf("start over cap: err = %v, want wip cap full", err)
	}
	h.do(Request{Op: "start", Task: "adhoc-1", OverCap: true})
	v := h.do(Request{Op: "ls"}).(BoardView)
	if v.Started != 2 || v.OverCap != 1 || v.Cap != 1 {
		t.Fatalf("board = %d started, %d over, cap %d", v.Started, v.OverCap, v.Cap)
	}
}

func TestFastExitBeforeNewWindowReturns(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.rex.exitFast = "wt"
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.waitTab("claude working")
}

func TestRecoveryRestartsALostWorker(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	seed := func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data",
			Branch: "ada/eng-7", Worktree: "/wt/eng-7", SessionID: "session:data", WindowID: "window:gone", BlockID: "block:gone", Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, db, seed)
	w := h.waitTab("claude working")
	if w.cwd != "/wt/eng-7" {
		t.Fatalf("restarted worker cwd = %s", w.cwd)
	}
	d, _ := h.s.show("ENG-7")
	if d.Stage != "Working" || d.Flag != "" {
		t.Fatalf("recovered task = %+v", d.TaskView)
	}
	if last := d.Events[len(d.Events)-1]; last.Signal != "worker-lost" {
		t.Fatalf("last event = %+v, want worker-lost", last)
	}
	var left int
	h.s.call(func() { left = h.s.recs["ENG-7"].Left.Restarts })
	if left != 1 {
		t.Fatalf("restarts left = %d, want 1", left)
	}
}

func TestCloseRemovesTheWorktreeOfAFailedSetupOnly(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "close", Task: "ENG-1"})
	h.waitStage("ENG-1", lifecycle.Done)

	h.do(Request{Op: "add", Issue: "ENG-2"})
	h.do(Request{Op: "start", Task: "ENG-2"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID, ExitCode: 1}
	eventually(t, "setup failed", func() bool { return h.task("ENG-2").Flag == "setup failed" })
	h.do(Request{Op: "close", Task: "ENG-2"})
	eventually(t, "the failed setup's worktree removed", func() bool {
		h.wt.mu.Lock()
		defer h.wt.mu.Unlock()
		return len(h.wt.removed) > 0
	})
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if len(h.wt.removed) != 1 {
		t.Fatalf("removed %v, want only the started Task's worktree", h.wt.removed)
	}
}

func TestChecksThatPassedBeforeCIRunningStillCount(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID}
	worker := h.waitTab("claude working")

	h.gh.set(key, 42, "pr-open", "ci-passed")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}
	h.waitStage("ENG-1", lifecycle.ReadyToLand)
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
	if v.Note != "#42 · optional failed: lint" || !slices.Equal(v.Optional, []string{"lint"}) {
		t.Errorf("note %q optional %q", v.Note, v.Optional)
	}
	want := []string{"✓ required checks passed", "✓ Greptile 5/5", "✓ review threads done", "✓ required reviews approved", "! optional checks failed: lint"}
	if got := r.checklist(); !slices.Equal(got, want) {
		t.Errorf("checklist %q, want %q", got, want)
	}

	r.Stage = lifecycle.InReview
	r.Standing = bar.Measure(lifecycle.PRFacts{Head: testHead, Score: &five, Decision: lifecycle.DecisionRequired})
	if v := r.view(); v.Note != "#42 needs approval" || r.checklist()[3] != "✗ needs approval" {
		t.Errorf("In review: note %q checklist %q", v.Note, r.checklist())
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
	if err == nil || err.Error() != "archive is not allowed in Queued" {
		t.Fatalf("archive in Queued: err = %v", err)
	}
	h.do(Request{Op: "close", Task: "ENG-1"})
	h.waitStage("ENG-1", lifecycle.Done)
	h.do(Request{Op: "archive", Task: "ENG-1"})
	h.do(Request{Op: "archive", Task: "ENG-1"})

	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); len(ids) != 0 {
		t.Fatalf("board after archive = %v", ids)
	}
	d, _ := h.s.show("ENG-1")
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
		for id, age := range map[string]time.Duration{"ENG-old": 8 * 24 * time.Hour, "ENG-new": 24 * time.Hour} {
			r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeClosed, Policy: testPolicy},
				Title: id, StageSince: time.Now().Add(-age)}
			data, _ := json.Marshal(r)
			st.Save(r.ID, data, nil)
		}
	}
	h := newHarness(t, db, seed)
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"ENG-new"}) {
		t.Fatalf("board = %v, want only ENG-new", ids)
	}
	if v := h.task("ENG-old"); v.Stage != "Done" {
		t.Fatalf("aged-out task = %+v", v)
	}
}

func TestTaskWithNoIssueOpensInTheAdHocSession(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Repo: "web", Text: "Fix map tooltip"})
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "adhoc-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})

	sessions := map[string]string{}
	eventually(t, "both setup tabs", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		for _, tb := range h.rex.tabs {
			sessions[tb.label] = tb.SessionID
		}
		return len(sessions) == 2
	})
	if s := sessions["adhoc-1 Fix map tooltip"]; s != "session:ad hoc" {
		t.Errorf("text Task session = %q, want session:ad hoc (all: %v)", s, sessions)
	}
	if s := sessions["ENG-1 Add billing model"]; s != "session:data" {
		t.Errorf("Issue Task session = %q, want session:data", s)
	}
	if v := h.task("adhoc-1"); v.Repo != "web" {
		t.Errorf("text Task Repo column = %q, want web", v.Repo)
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
		"owner/repo#12": KindGitHub,
		"lineup#12":     KindLinear, // no owner: not a GitHub ID
	} {
		if got := KindOf(id); got != want {
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

// A setup tab can exit before the Scheduler queues the Worker, as when the
// worktree already exists. The Task must still open one setup tab only.
func TestAFastSetupOpensOneSetupTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.rex.exitFast = "wt"
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
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

// A gated Task waits in CI running for the bot's score, flags the user when
// no score comes, and starts a review cycle for a low score.
func TestAGatedTaskWaitsForTheBotThenFlagsIt(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config, d.Now = gatedConfig{}, c.now })
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing", Bot: "greptile"}

	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID}
	worker := h.waitTab("claude working")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}
	eventually(t, "stopped", func() bool { return h.task("ENG-1").Flag == "stopped" })
	h.gh.set(key, 42, "pr-open")
	h.waitStage("ENG-1", lifecycle.CIRunning)

	h.gh.set(key, 42, "review-required")
	eventually(t, "the wait note", func() bool { return h.task("ENG-1").Note == "#42 waiting for Greptile" })
	c.add(14 * time.Minute)
	for n := h.polls(); h.polls() < n+3; {
		time.Sleep(5 * time.Millisecond)
	}
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
	h.waitTab("claude responding")
	if v := h.task("ENG-1"); v.Note != "Greptile 4/5, cycle 1/2" {
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

func TestRecoverReplaysSteers(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	if first := h.waitSpecs(1); first.Restart || len(first.Steers) != 0 {
		t.Fatalf("first Worker spec = %+v, want no restart", first)
	}
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "skip the backfill"})

	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: worker.BlockID}
	spec := h.waitSpecs(2)
	if !spec.Restart || !slices.Equal(spec.Steers, []string{"use a dual write", "skip the backfill"}) {
		t.Fatalf("restart spec = restart %v, steers %q", spec.Restart, spec.Steers)
	}
}

func TestFreshStageHasNoRestartNote(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	key := PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}
	worker := h.working()
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "done"}}
	eventually(t, "stopped", func() bool { return h.task("ENG-1").Flag == "stopped" })
	h.gh.set(key, 42, "pr-open")
	h.waitStage("ENG-1", lifecycle.CIRunning)
	h.gh.set(key, 42, "ci-failed")
	h.waitStage("ENG-1", lifecycle.CIDebug)

	debugger := h.waitTab("claude ci_debug")
	if spec := h.waitSpecs(2); spec.Stage != lifecycle.CIDebug || spec.Restart || len(spec.Steers) != 0 {
		t.Fatalf("CI debug spec = %+v, want a fresh start with no steers", spec)
	}
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: debugger.BlockID}
	if spec := h.waitSpecs(3); !spec.Restart || len(spec.Steers) != 0 {
		t.Fatalf("CI debug restart spec = restart %v, steers %q, want a restart with no Working steers", spec.Restart, spec.Steers)
	}
}
