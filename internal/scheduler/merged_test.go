package scheduler

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// seedKept stores a build Task that merged age ago, with no tab, whose
// worktree lineup keeps until until.
func seedKept(id string, age time.Duration, until time.Time) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeMerged, Policy: testPolicy},
			Title: id, Session: "data", SessionID: "session:data", Repo: "/repos/acme/data", Branch: "b-" + id,
			Worktree: "/worktrees/b-" + id, Conversation: "conversation-" + id, KeepUntil: until, StageSince: time.Now().Add(-age)}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestAStoppedSchedulerRemovesAWorktreeWhoseDaysEndedAtItsStart(t *testing.T) {
	seed := func(st *store.Store) {
		seedKept("ENG-DUE", time.Hour, time.Now().Add(-time.Hour))(st)
		seedKept("ENG-KEPT", time.Hour, time.Now().Add(24*time.Hour))(st)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) { d.PollInterval = time.Hour })
	eventually(t, "the due worktree removed", func() bool { return h.task("ENG-DUE").Worktree == "" })
	time.Sleep(100 * time.Millisecond)
	if got := h.worktreesRemoved(); !slices.Equal(got, []string{"b-ENG-DUE"}) {
		t.Fatalf("removed %v, want only the worktree whose days ended", got)
	}
	if v := h.task("ENG-KEPT"); v.Worktree == "" || !strings.Contains(v.Note, "worktree until ") {
		t.Fatalf("kept task = %+v, want its worktree and the date it goes", v)
	}
}

func TestAKeptWorktreeKeepsItsTaskOnTheBoardPastTheAgeOff(t *testing.T) {
	seed := func(st *store.Store) {
		seedKept("ENG-KEPT", 8*24*time.Hour, time.Now().Add(24*time.Hour))(st)
		seedDone("ENG-BARE", 8*24*time.Hour, false)(st)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed)
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"ENG-KEPT"}) {
		t.Fatalf("board = %v, want only the Task that keeps its worktree", ids)
	}
}

// The user's close removes the worktree, but not while it holds uncommitted
// changes. The note says why it stays, and each tick tries again.
func TestAUsersCloseKeepsADirtyWorktreeUntilItIsClean(t *testing.T) {
	h, worker := doneWithATab(t)
	h.wt.setDirty(true)
	h.rex.userCloses(worker)
	path := filepath.Join(h.wt.root, "eng-1-billing")
	eventually(t, "the note to say why the worktree stays", func() bool {
		return h.task("ENG-1").Note == "#42 merged · worktree kept: uncommitted changes in "+path
	})
	tries := len(h.worktreesRemoved())
	time.Sleep(100 * time.Millisecond)
	if got := len(h.worktreesRemoved()); got <= tries {
		t.Fatalf("%d tries after 5 ticks, want more than %d", got, tries)
	}
	if v := h.task("ENG-1"); v.Worktree != path {
		t.Fatalf("task = %+v, want the dirty worktree kept", v)
	}
	h.wt.setDirty(false)
	eventually(t, "the clean worktree removed", func() bool { return h.task("ENG-1").Worktree == "" })
	if v := h.task("ENG-1"); v.Note != "#42 merged" {
		t.Fatalf("note = %q, want the merge alone", v.Note)
	}
}

// closeMerged turns [merged] close_tab on, with keep as the time that lineup
// keeps the worktree, on the clock c. The option asks lineup to close a
// merged Task's tab, so the fake Rex takes lineup's close of a live tab.
func closeMerged(c *clock, keep time.Duration) func(*Deps) {
	return func(d *Deps) {
		d.CloseMergedTab, d.KeepWorktree, d.Now = true, keep, c.now
		d.Rex.(*fakeRex).asked = true
	}
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)} }

// mergeWith starts ENG-1, has its Worker report state, then merges its PR.
// It returns the Worker's tab.
func mergeWith(t *testing.T, state string, opts ...func(*Deps)) (*harness, tab) {
	t.Helper()
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, opts...)
	worker := h.working()
	eventually(t, "ENG-1 to record its tab", func() bool { return h.task("ENG-1").Tab })
	h.status(worker.BlockID, state)
	h.waitWorkerState("ENG-1", state)
	h.gh.set(PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}, 42, "pr-merged")
	h.waitStage("ENG-1", lifecycle.Done)
	return h, worker
}

// waitTabClosedByLineup waits until lineup closed the Worker's tab of a
// merged Task.
func (h *harness) waitTabClosedByLineup() {
	h.t.Helper()
	eventually(h.t, "the merged Task's tab closed", func() bool {
		return len(h.rex.open("claude working")) == 0 && !h.task("ENG-1").Tab
	})
}

func TestWithCloseTabOffAMergeKeepsTheTabAndTheWorktree(t *testing.T) {
	h, _ := mergeWith(t, "idle")
	time.Sleep(150 * time.Millisecond)
	if n := len(h.rex.open("claude working")); n != 1 {
		t.Fatalf("%d Worker tabs open, want the merged Task's tab kept", n)
	}
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v, want the worktree kept", got)
	}
	if v := h.task("ENG-1"); !v.Tab || v.Note != "#42 merged" {
		t.Fatalf("task = %+v, want its tab and the merge note", v)
	}
}

func TestAMergedTaskClosesItsTabWhenTheWorkerIsIdle(t *testing.T) {
	h, _ := mergeWith(t, "idle", closeMerged(newClock(), 7*24*time.Hour))
	h.waitTabClosedByLineup()
	eventually(t, "the Repo session removed", func() bool { return slices.Equal(h.sessionsDestroyed(), []string{"session:acme/data"}) })
	v := h.task("ENG-1")
	if v.Note != "#42 merged · worktree until Nov 14" || v.Worktree == "" {
		t.Fatalf("task = %+v, want its worktree kept until Nov 14", v)
	}
	time.Sleep(100 * time.Millisecond)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v, want the worktree kept", got)
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Contains(ids, "ENG-1") {
		t.Fatalf("board = %v, want the kept Task on it", ids)
	}
	if !slices.Contains(h.events("ENG-1"), "merged-tab-closed") {
		t.Fatalf("events %v, want the close recorded", h.events("ENG-1"))
	}
}

func TestAMergedTaskKeepsItsTabWhileTheWorkerWorks(t *testing.T) {
	h, worker := mergeWith(t, "working", closeMerged(newClock(), 7*24*time.Hour))
	for _, state := range []string{"working", "blocked"} {
		h.status(worker.BlockID, state)
		h.waitWorkerState("ENG-1", state)
		time.Sleep(100 * time.Millisecond)
		if !h.task("ENG-1").Tab || len(h.rex.open("claude working")) != 1 {
			t.Fatalf("the tab closed while the Worker was %s", state)
		}
	}
	h.status(worker.BlockID, "idle")
	h.waitTabClosedByLineup()
}

func TestAKeptWorktreeGoesWhenItsDaysEnd(t *testing.T) {
	c := newClock()
	h, _ := mergeWith(t, "idle", closeMerged(c, 7*24*time.Hour))
	h.waitTabClosedByLineup()
	c.add(7*24*time.Hour - time.Minute)
	time.Sleep(100 * time.Millisecond)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v a minute before the days end", got)
	}
	c.add(time.Minute)
	eventually(t, "the worktree removed", func() bool { return h.task("ENG-1").Worktree == "" })
	if got, v := h.worktreesRemoved(), h.task("ENG-1"); !slices.Equal(got, []string{"ada/eng-1-billing"}) || v.Note != "#42 merged" {
		t.Fatalf("removed %v, task %+v, want one removal and the merge note", got, v)
	}
}

func TestWithZeroDaysTheWorktreeGoesWithTheTab(t *testing.T) {
	h, _ := mergeWith(t, "idle", closeMerged(newClock(), 0))
	h.waitTabClosedByLineup()
	eventually(t, "the worktree removed", func() bool { return slices.Equal(h.worktreesRemoved(), []string{"ada/eng-1-billing"}) })
}

func TestAKeptWorktreeWithUncommittedChangesStaysUntilItIsClean(t *testing.T) {
	c := newClock()
	h, _ := mergeWith(t, "idle", closeMerged(c, 7*24*time.Hour))
	h.waitTabClosedByLineup()
	h.wt.setDirty(true)
	c.add(7 * 24 * time.Hour)
	eventually(t, "the note to say why the worktree stays", func() bool {
		return strings.Contains(h.task("ENG-1").Note, "worktree kept: uncommitted changes in ")
	})
	tries := len(h.worktreesRemoved())
	time.Sleep(100 * time.Millisecond)
	if got := len(h.worktreesRemoved()); got <= tries {
		t.Fatalf("%d tries after 5 ticks, want more than %d", got, tries)
	}
	h.wt.setDirty(false)
	eventually(t, "the clean worktree removed", func() bool { return h.task("ENG-1").Worktree == "" })
}

func TestAddRefusesATaskWhoseDoneTaskKeepsItsWorktree(t *testing.T) {
	h, _ := mergeWith(t, "idle", closeMerged(newClock(), 7*24*time.Hour))
	h.waitTabClosedByLineup()
	_, err := h.s.Handle(context.Background(), Request{Op: "add", Issue: "ENG-1"})
	if err == nil || err.Error() != "ENG-1 is Done and keeps its worktree: remove it first with lineup close ENG-1" {
		t.Fatalf("add over a kept worktree: err = %v", err)
	}
}

func TestTheUsersCloseRemovesAKeptWorktreeAtOnce(t *testing.T) {
	for _, op := range []string{"close", "archive"} {
		t.Run(op, func(t *testing.T) {
			h, _ := mergeWith(t, "idle", closeMerged(newClock(), 7*24*time.Hour))
			h.waitTabClosedByLineup()
			h.do(Request{Op: op, Task: "ENG-1"})
			eventually(t, "the worktree removed", func() bool { return slices.Equal(h.worktreesRemoved(), []string{"ada/eng-1-billing"}) })
			eventually(t, "the removed worktree to leave the Task", func() bool { return h.task("ENG-1").Worktree == "" })
		})
	}
}

func TestTheUsersCloseOfAClosingTabRemovesTheWorktreeAtOnce(t *testing.T) {
	h, worker := mergeWith(t, "working", closeMerged(newClock(), 7*24*time.Hour))
	h.rex.userCloses(worker)
	eventually(t, "the worktree removed", func() bool { return slices.Equal(h.worktreesRemoved(), []string{"ada/eng-1-billing"}) })
	eventually(t, "the removed worktree to leave the Task", func() bool { return h.task("ENG-1").Worktree == "" })
	if v := h.task("ENG-1"); v.Note != "#42 merged" {
		t.Fatalf("note = %q, want the merge note alone", v.Note)
	}
}

// seedMergedTab stores a merged build Task with a Worker tab, whose Worker
// last reported state. With closing, lineup waits to close the tab.
func seedMergedTab(id, state string, closing bool) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeMerged, Policy: testPolicy},
			Title: id, Session: "data", SessionID: "session:data", WindowID: "window:" + id, BlockID: "block:" + id,
			Job: JobWorker, WorkerState: state, CloseAtIdle: closing, Repo: "/repos/acme/data", Branch: "b-" + id,
			Worktree: "/worktrees/b-" + id, Conversation: "conversation-" + id, StageSince: time.Now()}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

// liveWorker adds the live Worker tab of the Task id, whose program Rex reads
// as state.
func liveWorker(id, state string) func(*Deps) {
	return func(d *Deps) {
		rex := d.Rex.(*fakeRex)
		rex.tabs = append(rex.tabs, &tab{Window: Window{"session:data", "window:" + id, "block:" + id}, argv: []string{"claude"}, program: state})
	}
}

func (f *fakeRex) closedWindow(window string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, "close "+window)
}

// Rex lost the tab while the Scheduler was down, so the user did not close it,
// and the worktree stays for its days.
func TestATabThatRexLostWhileTheTaskWaitedKeepsTheWorktree(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedMergedTab("ENG-LOST", "working", true), closeMerged(newClock(), 7*24*time.Hour))
	eventually(t, "the lost tab forgotten", func() bool { return !h.task("ENG-LOST").Tab })
	time.Sleep(100 * time.Millisecond)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v, want the worktree kept", got)
	}
	if v := h.task("ENG-LOST"); v.Note != "merged · worktree until Nov 14" {
		t.Fatalf("note = %q, want the worktree kept until Nov 14", v.Note)
	}
}

// Rex restored the tab as an idle shell, so lineup's Worker is gone. The
// shell closes, and the worktree stays for its days.
func TestAClosingTabThatRexRestoredAsAShellClosesAndKeepsTheWorktree(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedMergedTab("ENG-SHELL", "working", true),
		restoredTabs("ENG-SHELL"), closeMerged(newClock(), 7*24*time.Hour))
	eventually(t, "the restored shell closed", func() bool { return h.rex.closedWindow("window:ENG-SHELL") })
	time.Sleep(100 * time.Millisecond)
	if got := h.worktreesRemoved(); len(got) != 0 {
		t.Fatalf("removed %v, want the worktree kept", got)
	}
	if v := h.task("ENG-SHELL"); v.Tab || v.Worktree == "" || v.Note != "merged · worktree until Nov 14" {
		t.Fatalf("task = %+v, want no tab and the worktree kept until Nov 14", v)
	}
}

// The Worker went idle while the Scheduler was down, so its tab closes at start.
func TestAClosingTabWhoseWorkerWentIdleWhileTheSchedulerWasDownCloses(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedMergedTab("ENG-IDLE", "working", true),
		liveWorker("ENG-IDLE", "idle"), closeMerged(newClock(), 7*24*time.Hour))
	eventually(t, "the idle Worker's tab closed", func() bool { return h.rex.closedWindow("window:ENG-IDLE") })
	eventually(t, "the closed tab to leave the Task", func() bool { return !h.task("ENG-IDLE").Tab })
	if v := h.task("ENG-IDLE"); v.Worktree == "" || v.Note != "merged · worktree until Nov 14" {
		t.Fatalf("task = %+v, want the worktree kept until Nov 14", v)
	}
}

// A Done Task that lineup does not wait to close keeps a restored shell for
// the user, with close_tab on too.
func TestADoneTaskThatIsNotClosingKeepsItsRestoredShell(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedMergedTab("ENG-KEPT", "idle", false),
		restoredTabs("ENG-KEPT"), closeMerged(newClock(), 7*24*time.Hour))
	h.still("ENG-KEPT", lifecycle.Done)
	if h.rex.closedWindow("window:ENG-KEPT") {
		t.Fatal("lineup closed the tab of a Done Task that it does not wait to close")
	}
	if v := h.task("ENG-KEPT"); !v.Tab || v.Worktree == "" {
		t.Fatalf("task = %+v, want its tab and worktree kept", v)
	}
}

// Each end of a review Task closes its tab, so close_tab changes nothing for it (PROD-REVIEW-012).
func TestAMergedReviewIgnoresCloseTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, closeMerged(newClock(), 7*24*time.Hour))
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTabRecorded("claude reviewing", "PR-7")
	merged := openPR(7, "h1")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	h.waitStage("PR-7", lifecycle.Done)
	eventually(t, "the review worktree removed", func() bool {
		return slices.Equal(h.reviewsRemoved(), []string{"/repos/acme/data.review-pr-7"})
	})
	eventually(t, "the review tab closed", func() bool { return !h.task("PR-7").Tab })
	if v := h.task("PR-7"); v.Note != "#7 merged" || v.Worktree != "" {
		t.Fatalf("task = %+v, want the review ended as before", v)
	}
}

func TestOpenResumesAKeptWorkerInItsWorktree(t *testing.T) {
	h, _ := mergeWith(t, "idle", closeMerged(newClock(), 7*24*time.Hour))
	first := h.waitSpecs(1)
	h.waitTabClosedByLineup()
	if res := h.open("ENG-1"); res.Action != OpenRestored {
		t.Fatalf("open = %+v, want restored", res)
	}
	resumed := h.waitTab("claude --resume " + first.Conversation)
	if worktree := filepath.Join(h.wt.root, "eng-1-billing"); resumed.cwd != worktree {
		t.Fatalf("resumed tab cwd = %s, want the worktree %s", resumed.cwd, worktree)
	}
	if spec := h.lastSpec(); spec.ReadOnly {
		t.Fatalf("spec = %+v, want a Worker that writes", spec)
	}
	eventually(t, "the side pane beside the Worker", func() bool {
		return slices.ContainsFunc(h.rex.allSplits(), func(s split) bool { return s.anchor == resumed.BlockID })
	})
	eventually(t, "the restored tab recorded", func() bool { return h.task("ENG-1").Tab })
}

func TestAfterItsDaysOpenResumesTheWorkerReadOnlyInTheMainCheckout(t *testing.T) {
	c := newClock()
	h, _ := mergeWith(t, "idle", closeMerged(c, 7*24*time.Hour))
	first := h.waitSpecs(1)
	h.waitTabClosedByLineup()
	c.add(7 * 24 * time.Hour)
	eventually(t, "the worktree removed", func() bool { return h.task("ENG-1").Worktree == "" })
	if res := h.open("ENG-1"); res.Action != OpenRestored {
		t.Fatalf("open = %+v, want restored", res)
	}
	resumed := h.waitTab("claude --resume " + first.Conversation)
	if resumed.cwd != "/repos/acme/data" {
		t.Fatalf("resumed tab cwd = %s, want the main checkout", resumed.cwd)
	}
	if spec := h.lastSpec(); !spec.ReadOnly {
		t.Fatalf("spec = %+v, want a read-only Worker", spec)
	}
	h.hn.mu.Lock()
	trusted := slices.Clone(h.hn.trusted)
	h.hn.mu.Unlock()
	if !slices.Contains(trusted, "/repos/acme/data") {
		t.Fatalf("trusted %v, want the main checkout", trusted)
	}
	eventually(t, "the restored tab recorded", func() bool { return h.task("ENG-1").Tab })
	time.Sleep(50 * time.Millisecond)
	if slices.ContainsFunc(h.rex.allSplits(), func(s split) bool { return s.anchor == resumed.BlockID }) {
		t.Fatal("a side pane opened beside the read-only Worker, but it has no worktree to show")
	}
}

// A Done Task never gets a fresh Worker: a new conversation would not know the Task.
func TestOpenRefusesADoneTaskWhoseConversationIsGone(t *testing.T) {
	h, _ := mergeWith(t, "idle", closeMerged(newClock(), 7*24*time.Hour))
	first := h.waitSpecs(1)
	h.waitTabClosedByLineup()
	h.hn.mu.Lock()
	h.hn.gone = []string{first.Conversation}
	h.hn.mu.Unlock()
	tabs := h.tabs()
	if err := h.openFails("ENG-1"); err.Error() != "ENG-1 is Done and Claude no longer holds its conversation" {
		t.Fatalf("open: %v", err)
	}
	if h.tabs() != tabs {
		t.Fatalf("%d tabs, want %d: a refused open opens no tab", h.tabs(), tabs)
	}
}
