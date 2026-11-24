package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

const prURL7 = "https://github.com/acme/data/pull/7"

func openPR(number int, head string) ReviewSnapshot {
	return ReviewSnapshot{Repo: "acme/data", Number: number, URL: "https://github.com/acme/data/pull/" + itoa(number),
		Title: "Fix the tooltip", State: "OPEN", Head: head, Base: "main"}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (h *harness) review(url string) ReviewResult {
	h.t.Helper()
	return h.do(Request{Op: "review", URL: url}).(ReviewResult)
}

func (h *harness) status(block, state string) {
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: block, Status: Status{State: state}}
}

// writeFindings leaves the file that pr-review writes at its Step 4.
func (h *harness) writeFindings(number int, head string) {
	h.t.Helper()
	dir := filepath.Join(h.stateHome, "pr-review", "acme-data-"+itoa(number))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings-"+head+".json"), []byte(`{"version":1}`), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// still checks that the Task stays in the Stage over several polls.
func (h *harness) still(id string, stage lifecycle.Stage) {
	h.t.Helper()
	time.Sleep(150 * time.Millisecond)
	if v := h.task(id); v.Stage != stage.String() {
		h.t.Fatalf("%s moved to %s, want it held in %s", id, v.Stage, stage)
	}
}

func (h *harness) reviewsRemoved() []string {
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	return slices.Clone(h.wt.reviewsRemoved)
}

func TestReviewLevels(t *testing.T) {
	added := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		pr      ReviewSnapshot
		working bool
		want    lifecycle.Signal
	}{
		{"open, no review", ReviewSnapshot{State: "OPEN"}, false, nil},
		{"review before the add", ReviewSnapshot{State: "OPEN", ReviewedAt: added.Add(-time.Minute)}, false, nil},
		{"review after the add", ReviewSnapshot{State: "OPEN", ReviewedAt: added.Add(time.Minute)}, false, lifecycle.PRReviewed{}},
		{"merged", ReviewSnapshot{State: "MERGED"}, false, lifecycle.PRMerged{}},
		{"closed", ReviewSnapshot{State: "CLOSED"}, false, lifecycle.PRClosed{}},
		{"reviewed, then merged", ReviewSnapshot{State: "MERGED", ReviewedAt: added.Add(time.Minute)}, false, lifecycle.PRReviewed{}},
		{"review while the Worker posts it", ReviewSnapshot{State: "OPEN", ReviewedAt: added.Add(time.Minute)}, true, nil},
		{"merged while the Worker works", ReviewSnapshot{State: "MERGED"}, true, lifecycle.PRMerged{}},
		{"closed while the Worker works", ReviewSnapshot{State: "CLOSED"}, true, lifecycle.PRClosed{}},
		{"reviewed and merged while the Worker works", ReviewSnapshot{State: "MERGED", ReviewedAt: added.Add(time.Minute)}, true, lifecycle.PRMerged{}},
	}
	for _, c := range cases {
		if got := c.pr.Level(added, c.working); got != c.want {
			t.Errorf("%s: level %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseReviewKey(t *testing.T) {
	want := ReviewKey{"umbrella/data", 4955}
	for _, s := range []string{
		"https://github.com/umbrella/data/pull/4955",
		"https://github.com/umbrella/data/pull/4955/files",
		"github.com/umbrella/data/pull/4955#discussion_r1",
		" umbrella/data#4955 ",
	} {
		if got, err := ParseReviewKey(s); err != nil || got != want {
			t.Errorf("ParseReviewKey(%q) = %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"https://github.com/umbrella/data/issues/4955", "data#4955", "umbrella/data#0", "https://gitlab.com/a/b/pull/1"} {
		if _, err := ParseReviewKey(s); err == nil {
			t.Errorf("ParseReviewKey(%q) took a string that names no PR", s)
		}
	}
}

func TestTheUsersReviewClosesTheReviewTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))

	res := h.review(prURL7)
	if res.ID != "PR-7" || res.Kind != lifecycle.KindReview || res.Action != ReviewAdded || res.Stage != "Reviewing" || res.Title != "data#7 Fix the tooltip" || res.PRURL != prURL7 || res.ReviewHead != "h1" {
		t.Fatalf("review = %+v", res)
	}
	worker := h.waitTab("claude reviewing")
	if worker.cwd != "/repos/acme/data" || worker.SessionID != "session:reviews" || worker.label != "PR-7 Fix the tooltip" {
		t.Fatalf("worker tab = %+v, want the main checkout in the reviews session", worker)
	}
	if worker.env["XDG_STATE_HOME"] != h.stateHome {
		t.Fatalf("worker env = %v, want XDG_STATE_HOME %s", worker.env, h.stateHome)
	}
	h.hn.mu.Lock()
	trusted, spec := slices.Clone(h.hn.trusted), h.hn.specs[len(h.hn.specs)-1]
	h.hn.mu.Unlock()
	if !slices.Equal(trusted, []string{"/repos/acme/data"}) {
		t.Fatalf("trusted %v, want the main checkout only", trusted)
	}
	if spec.Worktree != "/repos/acme/data.review-pr-7" || spec.PRURL != prURL7 || spec.Branch != "" {
		t.Fatalf("worker spec = %+v", spec)
	}
	if spec.Name != worker.label {
		t.Fatalf("session name %q, want the tab label %q", spec.Name, worker.label)
	}

	h.status(worker.BlockID, "done")
	eventually(t, "stopped without findings", func() bool { return h.task("PR-7").Flag == "stopped" })

	h.writeFindings(7, "h1")
	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)
	if v := h.task("PR-7"); v.Group != "Needs you" || v.Note != "#7 review ready" {
		t.Fatalf("review ready = %+v", v)
	}

	moved := openPR(7, "h2")
	h.gh.setReview(moved)
	eventually(t, "the head moved note", func() bool { return h.task("PR-7").Note == "#7 review ready · head moved" })

	// The user tells the Worker to post. While it works, the review waits.
	h.status(worker.BlockID, "working")
	moved.ReviewedAt = time.Now().Add(time.Hour)
	h.gh.setReview(moved)
	h.still("PR-7", lifecycle.ReviewReady)
	h.status(worker.BlockID, "blocked")
	h.still("PR-7", lifecycle.ReviewReady)

	h.status(worker.BlockID, "done")
	h.waitStage("PR-7", lifecycle.Done)
	if v := h.task("PR-7"); v.Outcome != "reviewed" {
		t.Fatalf("done = %+v", v)
	}
	eventually(t, "the review worktree removed", func() bool { return len(h.reviewsRemoved()) == 1 })
	if got := h.reviewsRemoved()[0]; got != "/repos/acme/data.review-pr-7" {
		t.Fatalf("removed %s", got)
	}
	if len(h.rex.open("claude reviewing")) != 0 {
		t.Fatal("the review tab stayed open after the user's review")
	}
	eventually(t, "the reviews session removed", func() bool { return slices.Contains(h.sessionsDestroyed(), "session:reviews") })
	var held github.ReviewTask
	h.s.call(func() { held, _ = h.s.recs["PR-7"].reviewTask() })
	if !held.Done || !held.Reviewed {
		t.Fatalf("queue hold = %+v, want a Done review that only a new request lets back", held)
	}
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if len(h.wt.removed) != 0 {
		t.Fatalf("a review Task ran wt remove for %v", h.wt.removed)
	}
	if v := h.task("PR-7"); v.Worktree != "" || v.Note != "#7 reviewed" || v.Tab {
		t.Fatalf("done task after cleanup = %+v", v)
	}
}

func TestAMergedPRClosesTheReviewTabWithItsSidePane(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	merged := openPR(7, "h1")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	h.waitStage("PR-7", lifecycle.Done)

	eventually(t, "the side pane's tab closed", func() bool {
		splits := h.rex.allSplits()
		return len(splits) == 1 && splits[0].closed
	})
	eventually(t, "the review worktree removed", func() bool {
		return slices.Equal(h.reviewsRemoved(), []string{"/repos/acme/data.review-pr-7"})
	})
}

func TestADoneReviewWithAnOpenTabBlocksANewReviewOfThePR(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTabRecorded("claude reviewing", "PR-7")
	// Cleanup runs in the Task's lane after the loop call that ends the Task,
	// so for a moment the loop sees a Done review that keeps its tab.
	h.s.call(func() { h.s.recs["PR-7"].Stage = lifecycle.Done })

	h.gh.setReview(openPR(7, "h2"))
	if _, err := h.s.Handle(context.Background(), Request{Op: "review", URL: prURL7}); err == nil || !strings.Contains(err.Error(), "close its tab first with lineup close PR-7") {
		t.Fatalf("review of a PR whose Done review keeps its tab: err = %v, want a refusal that names the tab", err)
	}
	var task github.ReviewTask
	h.s.call(func() { task, _ = h.s.recs["PR-7"].reviewTask() })
	if task.Done {
		t.Fatal("the queue sees a Done review that keeps its tab as ended, so it would add the PR again")
	}

	h.do(Request{Op: "close", Task: "PR-7"})
	eventually(t, "the tab closed", func() bool { return len(h.rex.open("claude reviewing")) == 0 })
	h.s.call(func() { task, _ = h.s.recs["PR-7"].reviewTask() })
	if !task.Done {
		t.Fatal("the queue still holds the PR after the tab closed")
	}
	if res := h.review(prURL7); res.Action != ReviewAdded {
		t.Fatalf("review after the tab closed = %+v, want a new review", res)
	}
}

func TestMergedAndClosedPRsEndTheReview(t *testing.T) {
	for state, outcome := range map[string]string{"MERGED": "merged", "CLOSED": "closed"} {
		h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
		h.gh.setReview(openPR(7, "h1"))
		h.review(prURL7)
		worker := h.waitTab("claude reviewing")
		h.status(worker.BlockID, "working")
		gone := openPR(7, "h1")
		gone.State = state
		h.gh.setReview(gone)
		h.waitStage("PR-7", lifecycle.Done)
		if v := h.task("PR-7"); v.Outcome != outcome {
			t.Fatalf("%s: outcome %q, want %q", state, v.Outcome, outcome)
		}
		eventually(t, "the review worktree removed", func() bool { return len(h.reviewsRemoved()) == 1 })
		if len(h.rex.open("claude reviewing")) != 0 {
			t.Fatalf("%s: the review tab stayed open while its Worker worked", state)
		}
	}
}

func TestTheLastReviewKeepsASessionThatHoldsOtherTabs(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	if _, err := h.rex.NewWindow(context.Background(), "session:reviews", "the user's own tab", "/", nil, []string{"zsh"}, false); err != nil {
		t.Fatal(err)
	}
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	merged := openPR(7, "h1")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	h.status(worker.BlockID, "idle")
	h.waitStage("PR-7", lifecycle.Done)
	eventually(t, "the review worktree removed", func() bool { return len(h.reviewsRemoved()) == 1 })
	time.Sleep(150 * time.Millisecond)
	h.rex.mu.Lock()
	defer h.rex.mu.Unlock()
	if len(h.rex.destroyed) != 0 {
		t.Fatalf("destroyed %v while it held the user's own tab", h.rex.destroyed)
	}
}

func TestAReviewThatReusesFindingsMeetsTheContract(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	old := filepath.Join(h.stateHome, "pr-review", "acme-data-7", "findings-h0.json")
	os.WriteFile(old, []byte("{}"), 0o644)
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	h.review(prURL7)
	h.status(h.waitTab("claude reviewing").BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)
}

func TestAnOldFindingsFileOfAnotherHeadDoesNotMeetTheContract(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h0")
	old := filepath.Join(h.stateHome, "pr-review", "acme-data-7", "findings-h0.json")
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	h.review(prURL7)
	h.status(h.waitTab("claude reviewing").BlockID, "done")
	eventually(t, "stopped", func() bool { return h.task("PR-7").Flag == "stopped" })
}

func TestReviewTwiceOnOnePRMakesOneTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	first := h.review(prURL7)
	h.waitTab("claude reviewing")
	for _, again := range []string{prURL7 + "/files", "Acme/Data#7"} {
		res := h.review(again)
		if res.Action != ReviewOpen || res.ID != first.ID {
			t.Fatalf("%s: %+v, want the open %s", again, res, first.ID)
		}
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"PR-7"}) {
		t.Fatalf("board = %v", ids)
	}
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if len(h.wt.added) != 1 {
		t.Fatalf("made the review worktree %d times", len(h.wt.added))
	}
}

func TestReviewOfAStoppedReviewRunsItAgain(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.wt.addErr = errors.New("fetch: no network")
	h.review(prURL7)
	eventually(t, "setup failed", func() bool { return h.task("PR-7").Flag == "setup failed" })
	if n := len(h.rex.open("claude reviewing")); n != 0 {
		t.Fatalf("a Worker runs after the setup failed: %d tabs", n)
	}

	res := h.review(prURL7)
	if res.ID != "PR-7" || res.Action != ReviewRestarted {
		t.Fatalf("review of the stopped Task = %+v, want PR-7 restarted", res)
	}
	h.waitTab("claude reviewing")
	eventually(t, "the flag to clear", func() bool { return h.task("PR-7").Flag == "" })
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"PR-7"}) {
		t.Fatalf("board = %v", ids)
	}
}

func TestReviewOfARunningReviewChangesNothing(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	if res := h.review(prURL7); res.Action != ReviewOpen {
		t.Fatalf("review of the running Task = %+v, want open", res)
	}
	h.still("PR-7", lifecycle.Reviewing)
	if n := len(h.rex.open("claude reviewing")); n != 1 {
		t.Fatalf("%d Worker tabs, want 1", n)
	}
}

func TestAReviewIDNamesItsPR(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	var data, web, later string
	h.s.call(func() {
		data = h.s.reviewID(ReviewPR{Repo: "acme/data", Number: 7})
		h.s.recs[data] = &Record{Task: lifecycle.Task{ID: data, Kind: lifecycle.KindReview, Stage: lifecycle.Reviewing}}
		web = h.s.reviewID(ReviewPR{Repo: "acme/web", Number: 7})
		h.s.recs[data].Stage = lifecycle.Done
		later = h.s.reviewID(ReviewPR{Repo: "acme/web", Number: 7})
	})
	if data != "PR-7" || web != "PR-7-WEB" || later != "PR-7" {
		t.Errorf("IDs = %q, %q, %q, want PR-7, then PR-7-WEB beside a live PR-7, then PR-7 once it is Done", data, web, later)
	}
}

func TestAReviewIDKeepsADoneReviewThatHoldsItsTab(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	var web string
	h.s.call(func() {
		h.s.recs["PR-7"] = &Record{
			Task:   lifecycle.Task{ID: "PR-7", Kind: lifecycle.KindReview, Stage: lifecycle.Done},
			Review: &ReviewPR{Repo: "acme/data", Number: 7}, WindowID: "window:7", BlockID: "block:7",
		}
		web = h.s.reviewID(ReviewPR{Repo: "acme/web", Number: 7})
	})
	if web != "PR-7-WEB" {
		t.Errorf("ID of acme/web#7 = %q, want PR-7-WEB beside a Done acme/data#7 that keeps its tab", web)
	}
}

func TestADoneReviewDoesNotBlockANewOne(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.do(Request{Op: "close", Task: "PR-7"})
	h.waitStage("PR-7", lifecycle.Done)
	if res := h.review(prURL7); res.Action != ReviewAdded || res.ID != "PR-7" {
		t.Fatalf("review after close = %+v, want a new PR-7", res)
	}
}

func TestReviewRefusals(t *testing.T) {
	fork := openPR(8, "f1")
	fork.Fork = true
	merged := openPR(9, "m1")
	merged.State = "MERGED"
	elsewhere := openPR(10, "e1")
	elsewhere.Repo = "acme/web"
	cases := []struct {
		pr   ReviewSnapshot
		url  string
		want string
	}{
		{fork, "acme/data#8", "fork"},
		{merged, "acme/data#9", "merged"},
		{elsewhere, "acme/web#10", "acme/web"},
		{ReviewSnapshot{}, "acme/data#11", "no PR"},
		{ReviewSnapshot{}, "https://github.com/acme/data/issues/3", "not a GitHub PR URL"},
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	for _, c := range cases {
		if c.pr.Number != 0 {
			h.gh.setReview(c.pr)
		}
		_, err := h.s.Handle(context.Background(), Request{Op: "review", URL: c.url})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want one that says %q", c.url, err, c.want)
		}
	}
	if _, err := h.s.Handle(context.Background(), Request{Op: "review", URL: "acme/web#10"}); !errors.Is(err, ErrNoRepo) {
		t.Errorf("no checkout: err %v, want ErrNoRepo", err)
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); len(ids) != 0 {
		t.Fatalf("a refused review added %v", ids)
	}
}

func TestTwoReviewStartsInOneRepoFetchOneAfterTheOther(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.gh.setReview(openPR(8, "h2"))
	h.review(prURL7)
	h.review("acme/data#8")
	eventually(t, "both review worktrees", func() bool {
		h.wt.mu.Lock()
		defer h.wt.mu.Unlock()
		return len(h.wt.added) == 2
	})
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if h.wt.mostInFlight != 1 {
		t.Fatalf("%d review fetches ran at once in one Repo", h.wt.mostInFlight)
	}
}

func TestCleanupKeepsAReviewWorktreeWithWorkOnNoRemote(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.wt.keep = true
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	h.do(Request{Op: "close", Task: "PR-7"})
	eventually(t, "the kept note", func() bool {
		return h.task("PR-7").Note == "#7 closed · worktree kept: /repos/acme/data.review-pr-7"
	})
	if v := h.task("PR-7"); v.Worktree != "/repos/acme/data.review-pr-7" || v.Outcome != "closed" {
		t.Fatalf("closed task = %+v", v)
	}
}

func TestTheBranchPollSkipsReviewTasks(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.waitTab("claude reviewing")
	eventually(t, "a branch poll", func() bool {
		h.gh.mu.Lock()
		defer h.gh.mu.Unlock()
		return len(h.gh.polled) > 2
	})
	h.gh.mu.Lock()
	defer h.gh.mu.Unlock()
	for _, k := range h.gh.polled {
		if k.Branch != "ada/eng-1-billing" {
			t.Fatalf("the branch poll asked for %+v", k)
		}
	}
}

// The user can reopen the Worker, so a review that lost its tab in Review
// ready waits for the user and keeps its queue's slot.
func TestALostTabInReviewReadyKeepsTheReview(t *testing.T) {
	for _, ev := range []RexEvent{{Kind: EvClosed}, {Kind: EvExited, ExitCode: 0}, {Kind: EvExited, ExitCode: 130}} {
		h := queueHarness(t, authorQueue("q", 1, "hopper"))
		h.gh.found = map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30), queuePR("acme/data", 8, 20)}}
		h.writeFindings(7, "h7")
		h.s.fill(context.Background())
		worker := h.waitTab("claude reviewing")
		h.status(worker.BlockID, "done")
		h.waitStage("PR-7", lifecycle.ReviewReady)
		ev.BlockID = worker.BlockID
		if ev.Kind == EvClosed {
			h.rex.userCloses(worker)
		} else {
			h.rex.events <- ev
		}
		eventually(t, "the Worker gone", func() bool { return !h.task("PR-7").Live })
		h.s.fill(context.Background())
		h.still("PR-7", lifecycle.ReviewReady)
		if v := h.task("PR-7"); v.Group != "Needs you" || v.Flag != "" {
			t.Fatalf("event %+v: task = %+v, want Needs you with no flag", ev, v)
		}
		if got := h.reviews(); !slices.Equal(got, []string{"PR-7 #7 Review ready"}) {
			t.Fatalf("event %+v: reviews %v, want the slot held by PR-7", ev, got)
		}
		if n := len(h.reviewsRemoved()); n != 0 {
			t.Fatalf("event %+v: removed %d review worktrees", ev, n)
		}
	}
}

func (h *harness) lastSpec() WorkerSpec {
	h.hn.mu.Lock()
	defer h.hn.mu.Unlock()
	return h.hn.specs[len(h.hn.specs)-1]
}

// readyReview drives a review of #7 to Review ready, then closes its tab as
// the user does. It returns the Reviewing Worker's spec.
func (h *harness) readyReview() WorkerSpec {
	h.t.Helper()
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	spec := h.lastSpec()
	h.status(worker.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)
	h.rex.userCloses(worker)
	eventually(h.t, "the Worker gone", func() bool { return !h.task("PR-7").Live })
	return spec
}

func TestReviewOfAClosedReviewReadyWorkerResumesItsConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	reviewing := h.readyReview()
	if u, err := uuid.Parse(reviewing.Conversation); err != nil || u.String() != reviewing.Conversation {
		t.Fatalf("the Reviewing Worker's conversation %q is not a UUID: %v", reviewing.Conversation, err)
	}

	res := h.review(prURL7)
	if res.ID != "PR-7" || res.Action != ReviewReopened {
		t.Fatalf("review of the closed Worker = %+v, want PR-7 reopened", res)
	}
	worker := h.waitTab("claude --resume " + reviewing.Conversation)
	if worker.cwd != "/repos/acme/data" || worker.env["XDG_STATE_HOME"] != h.stateHome {
		t.Fatalf("reopened tab = %+v, want the main checkout and the review state folder", worker)
	}
	spec := h.lastSpec()
	if spec.Stage != lifecycle.ReviewReady || spec.Conversation != reviewing.Conversation || spec.Worktree != "/repos/acme/data.review-pr-7" || spec.PRURL != prURL7 {
		t.Fatalf("reopened spec = %+v, want Review ready in conversation %s", spec, reviewing.Conversation)
	}
	eventually(t, "the reopened Worker live", func() bool { return h.task("PR-7").Live })
	if v := h.task("PR-7"); v.Stage != "Review ready" || v.Flag != "" {
		t.Fatalf("reopened task = %+v", v)
	}
	if res := h.review(prURL7); res.Action != ReviewOpen {
		t.Fatalf("review of the reopened Worker = %+v, want open", res)
	}
}

// A Task from before conversation IDs has none to resume, so a reopen
// reviews the PR again.
func TestReviewOfAReviewReadyTaskWithNoConversationReviewsAgain(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady))
	res := h.review(prURL7)
	if res.ID != "R-3" || res.Action != ReviewRestarted || res.Stage != "Reviewing" {
		t.Fatalf("review = %+v, want R-3 restarted in Reviewing", res)
	}
	h.waitTab("claude reviewing")
	if spec := h.lastSpec(); spec.Stage != lifecycle.Reviewing || spec.Conversation == "" {
		t.Fatalf("spec = %+v, want a fresh review in a new conversation", spec)
	}
}

func TestALostTabWhileReviewingFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	first := h.waitTab("claude reviewing")
	firstSpec := h.lastSpec()
	h.rex.userCloses(first)
	eventually(t, "the tab closed flag", func() bool { return h.task("PR-7").Flag == "tab closed" })
	time.Sleep(150 * time.Millisecond)
	if n := len(h.rex.open("claude --resume " + firstSpec.Conversation)); n != 0 {
		t.Fatalf("%d tabs opened by themselves, want none until the user asks", n)
	}
	if v := h.task("PR-7"); v.Stage != "Reviewing" || v.Group != "Needs you" || v.Live {
		t.Fatalf("task = %+v, want Reviewing, Needs you, with no Worker", v)
	}

	res := h.open("PR-7")
	if res.Action != OpenRestored {
		t.Fatalf("open = %+v, want restored", res)
	}
	second := h.waitTab("claude --resume " + firstSpec.Conversation)
	if second.cwd != "/repos/acme/data" || second.env["XDG_STATE_HOME"] != h.stateHome {
		t.Fatalf("restored tab = %+v, want the main checkout and the review state folder", second)
	}
	h.status(second.BlockID, "idle")
	time.Sleep(100 * time.Millisecond)
	if got := h.rex.typedInto(second.BlockID); len(got) != 0 {
		t.Fatalf("typed %q into the restored review Worker", got)
	}
	eventually(t, "the restored Worker live", func() bool { return h.task("PR-7").Live })
	if v := h.task("PR-7"); v.Stage != "Reviewing" || v.Flag != "" {
		t.Fatalf("restored task = %+v", v)
	}
}

func TestReviewOfAReviewingTaskWhoseTabClosedReopensItsWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	first := h.waitTab("claude reviewing")
	conversation := h.lastSpec().Conversation
	h.rex.userCloses(first)
	eventually(t, "the tab closed flag", func() bool { return h.task("PR-7").Flag == "tab closed" })

	if res := h.review(prURL7); res.ID != "PR-7" || res.Action != ReviewReopened {
		t.Fatalf("review = %+v, want PR-7 reopened", res)
	}
	h.waitTab("claude --resume " + conversation)
	eventually(t, "the reopened Worker live", func() bool { v := h.task("PR-7"); return v.Live && v.Flag == "" })
}

func seedReview(stage lifecycle.Stage) func(*store.Store) {
	return seedReviewTab(stage, "window:gone", "block:gone", "")
}

func seedReviewTab(stage lifecycle.Stage, window, block, side string) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "R-3", Kind: lifecycle.KindReview, Stage: stage, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "data#7 Fix the tooltip", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: reviewSession,
			Review:   &ReviewPR{Repo: "acme/data", Number: 7, URL: prURL7, Head: "h1", Base: "main"},
			Worktree: "/repos/acme/data.review-pr-7", SessionID: "session:reviews", WindowID: window, BlockID: block, SideBlockID: side, Job: JobWorker,
		}
		if side != "" {
			r.Side = &sidePane{Dir: r.Worktree, Argv: sidePR7}
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestRecoveryWithNoTabInReviewReadyKeepsTheReview(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady))
	h.still("R-3", lifecycle.ReviewReady)
	if v := h.task("R-3"); v.Live || v.Flag != "" || v.Group != "Needs you" {
		t.Fatalf("recovered review = %+v, want it waiting with no Worker", v)
	}
	h.rex.mu.Lock()
	defer h.rex.mu.Unlock()
	if len(h.rex.tabs) != 0 {
		t.Fatalf("recovery opened %d tabs for a finished review", len(h.rex.tabs))
	}
	if n := len(h.reviewsRemoved()); n != 0 {
		t.Fatalf("recovery removed %d review worktrees", n)
	}
}

func TestRecoveryRemovesTheWorktreeOfADoneReviewWhoseCleanupNeverRan(t *testing.T) {
	seed := func(st *store.Store) {
		seedReviewTab(lifecycle.Done, "", "", "")(st)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed)
	eventually(t, "the review worktree removed", func() bool {
		return slices.Equal(h.reviewsRemoved(), []string{"/repos/acme/data.review-pr-7"})
	})
	eventually(t, "the removed worktree to leave the review", func() bool { return h.task("R-3").Worktree == "" })
}

func TestRecoveryWithNoTabWhileReviewingFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.Reviewing))
	eventually(t, "the tab closed flag", func() bool { return h.task("R-3").Flag == "tab closed" })
	h.still("R-3", lifecycle.Reviewing)
	if n := h.tabs(); n != 0 {
		t.Fatalf("recovery opened %d tabs, want none until the user asks", n)
	}

	h.open("R-3")
	if w := h.waitTab("claude reviewing"); w.cwd != "/repos/acme/data" {
		t.Fatalf("opened worker cwd = %s", w.cwd)
	}
}

func TestReviewTasksStayOutsideTheWIPCap(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "cap", N: 1})
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.gh.setReview(openPR(7, "h1"))
	if res := h.review(prURL7); res.Stage != "Reviewing" {
		t.Fatalf("review with the cap full = %+v", res)
	}
	if v := h.do(Request{Op: "ls"}).(BoardView); v.Started != 1 || v.OverCap != 0 {
		t.Fatalf("board started %d over %d, want only the build Task", v.Started, v.OverCap)
	}
}

// The author can push while the review runs. pr-review then reviews the new
// head, and its findings file names that head.
func TestFindingsForANewerHeadMeetTheContract(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	h.gh.setReview(openPR(7, "h2"))
	h.writeFindings(7, "h2")
	h.status(worker.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)
	h.still("PR-7", lifecycle.ReviewReady)
	if v := h.task("PR-7"); v.Note != "#7 review ready" {
		t.Fatalf("note %q, want no head moved: the review read the current head", v.Note)
	}
}

var sidePR7 = []string{"sh", "-c", `for a in "$HOME/Applications/RexWeb.app" /Applications/RexWeb.app; do p=$a/Contents/Resources/rexweb-pane; [ -x "$p" ] && exec "$p" "$1"; done; GH_TOKEN=$(gh auth token) exec hunk gh pr "$1"`, "side", prURL7}

// waitSplit waits until Split has opened n blocks in all, and returns the last.
func (h *harness) waitSplit(n int) split {
	h.t.Helper()
	var ss []split
	eventually(h.t, itoa(n)+" side panes", func() bool { ss = h.rex.allSplits(); return len(ss) == n })
	return ss[n-1]
}

// waitNewTab waits for the one open tab that runs cmd and is not the tab of block old.
func (h *harness) waitNewTab(cmd, old string) tab {
	h.t.Helper()
	var tabs []tab
	eventually(h.t, "a new tab running "+cmd, func() bool {
		tabs = h.rex.open(cmd)
		return len(tabs) == 1 && tabs[0].BlockID != old
	})
	return tabs[0]
}

func TestEachReviewWorkerTabOpensThePRBesideIt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	started := h.waitTab("claude reviewing")
	conversation := h.lastSpec().Conversation
	pane := h.waitSplit(1)
	if pane.anchor != started.BlockID || pane.window != started.WindowID || pane.cwd != "/repos/acme/data.review-pr-7" || !slices.Equal(pane.argv, sidePR7) {
		t.Fatalf("side pane = %+v, want the PR %s beside block %s in the review worktree", pane, prURL7, started.BlockID)
	}

	h.rex.userCloses(started)
	eventually(t, "the tab closed flag", func() bool { return h.task("PR-7").Flag == "tab closed" })
	h.open("PR-7")
	resumed := h.waitTab("claude --resume " + conversation)
	if pane := h.waitSplit(2); pane.anchor != resumed.BlockID {
		t.Fatalf("resume: side pane beside %s, want %s", pane.anchor, resumed.BlockID)
	}
	h.status(resumed.BlockID, "idle")
	h.status(resumed.BlockID, "working")
	h.status(resumed.BlockID, "idle")

	h.do(Request{Op: "rerun", Task: "PR-7", Stage: "reviewing"})
	h.waitTyped(resumed.BlockID, "prompt reviewing")
	time.Sleep(50 * time.Millisecond)
	if n := len(h.rex.allSplits()); n != 2 {
		t.Fatalf("%d side panes after a rerun in the same tab, want 2", n)
	}

	h.writeFindings(7, "h1")
	h.status(resumed.BlockID, "working")
	h.status(resumed.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)
	h.rex.userCloses(resumed)
	eventually(t, "the Worker gone", func() bool { return !h.task("PR-7").Live })
	h.review(prURL7)
	reopened := h.waitNewTab("claude --resume "+conversation, resumed.BlockID)
	if pane := h.waitSplit(3); pane.anchor != reopened.BlockID || !slices.Equal(pane.argv, sidePR7) {
		t.Fatalf("reopen: side pane = %+v, want the PR beside %s", pane, reopened.BlockID)
	}
	h.still("PR-7", lifecycle.ReviewReady)
	if n := len(h.rex.allSplits()); n != 3 {
		t.Fatalf("%d side panes for 3 Worker tabs", n)
	}
}

// hunkDiff is the side pane of a build Task whose worktree has the DiffBase base1.
var hunkDiff = []string{"hunk", "diff", "--watch", "base1"}

func TestEachBuildWorkerTabOpensItsDiffInHunkBesideIt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	conversation := h.lastSpec().Conversation
	pane := h.waitSplit(1)
	if pane.anchor != worker.BlockID || pane.window != worker.WindowID || pane.cwd != filepath.Join(h.wt.root, "eng-1-billing") || !slices.Equal(pane.argv, hunkDiff) {
		t.Fatalf("side pane = %+v, want the worktree's diff beside block %s", pane, worker.BlockID)
	}

	h.rex.userCloses(worker)
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Flag == "tab closed" })
	h.open("ENG-1")
	resumed := h.waitTab("claude --resume " + conversation)
	if pane := h.waitSplit(2); pane.anchor != resumed.BlockID || !slices.Equal(pane.argv, hunkDiff) {
		t.Fatalf("restore: side pane = %+v, want the diff beside %s", pane, resumed.BlockID)
	}
}

// The side pane holds a tab open after its Worker's block closes.
func TestAClosedBuildWorkerClosesItsSidePane(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	h.waitSplit(1)
	eventually(t, "the Worker live", func() bool { return h.task("ENG-1").Live })

	h.rex.blockCloses(worker)
	eventually(t, "the side pane closed", func() bool { return h.rex.allSplits()[0].closed })
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Flag == "tab closed" })
}

func TestSidePaneEventsLeaveTheBuildTaskAsItWas(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	pane := h.waitSplit(1)
	eventually(t, "the Worker live", func() bool { return h.task("ENG-1").Live })
	before := h.task("ENG-1")

	for _, ev := range []RexEvent{
		{Kind: EvStatus, Status: Status{State: "done"}},
		{Kind: EvStatus, Status: Status{State: "blocked", Kind: "question"}},
		{Kind: EvExited, ExitCode: 1},
		{Kind: EvClosed},
	} {
		ev.BlockID = pane.BlockID
		h.rex.events <- ev
	}
	h.still("ENG-1", lifecycle.Working)
	if v := h.task("ENG-1"); !reflect.DeepEqual(v, before) {
		t.Fatalf("task = %+v after side pane events, want it as it was: %+v", v, before)
	}
	if tabs := h.rex.open("claude working"); len(tabs) != 1 || tabs[0].BlockID != worker.BlockID {
		t.Fatalf("Worker tabs %+v, want the first Worker only", tabs)
	}

	conversation := h.lastSpec().Conversation
	h.rex.userCloses(worker)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: pane.BlockID}
	eventually(t, "the tab closed flag", func() bool { return h.task("ENG-1").Flag == "tab closed" })
	h.still("ENG-1", lifecycle.Working)
	h.open("ENG-1")
	h.waitTab("claude --resume " + conversation)
}

// seedBuildTab seeds a build Task with a live Worker whose Record names the
// side pane side, which runs the Task's diff. Each edit then changes the Record.
func seedBuildTab(side string, edits ...func(*Record)) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "ENG-7", Stage: lifecycle.Working, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "Backfill events", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "ada/eng-7", Worktree: "/wt/eng-7",
			Conversation: "conv-7", SessionID: "session:data", WindowID: "window:live", BlockID: "block:live", SideBlockID: side, Job: JobWorker,
			Side: &sidePane{Dir: "/wt/eng-7", Argv: hunkDiff},
		}
		for _, e := range edits {
			e(&r)
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestRecoveryOpensAMissingSidePaneBesideALiveBuildWorker(t *testing.T) {
	live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{{BlockID: "split:9", window: "window:live", closed: true}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedBuildTab("split:9"), func(d *Deps) { d.Rex = rex })
	eventually(t, "a new side pane", func() bool { return len(rex.allSplits()) == 2 })
	if got := rex.allSplits()[1]; got.anchor != "block:live" || got.cwd != "/wt/eng-7" || !slices.Equal(got.argv, hunkDiff) {
		t.Fatalf("split = %+v", got)
	}
	h.still("ENG-7", lifecycle.Working)
	if v := h.task("ENG-7"); !v.Live || v.Flag != "" {
		t.Fatalf("task = %+v, want its Worker kept", v)
	}
}

// The kept pane is the Task's, so it closes with its Worker.
func TestRecoveryKeepsALiveSidePaneBesideABuildWorker(t *testing.T) {
	live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
	rex := &fakeRex{tabs: []*tab{live}, splits: []*split{sideAt("split:9", "window:live")}}
	// The Scheduler reads the events of the harness's Rex.
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedBuildTab("split:9"), func(d *Deps) { rex.events = d.Rex.(*fakeRex).events; d.Rex = rex })
	h.still("ENG-7", lifecycle.Working)
	if got := rex.allSplits(); len(got) != 1 || got[0].closed {
		t.Fatalf("side panes %+v, want the live one only, open", got)
	}

	rex.blockCloses(*live)
	eventually(t, "the side pane closed", func() bool { return rex.allSplits()[0].closed })
}

// The user can split a plain shell in the tab and type hunk in it. Recovery
// closes that pane, and the user's other shell stays.
func TestRecoveryClosesEachSidePaneThatTheRecordDoesNotName(t *testing.T) {
	live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
	typed := &split{BlockID: "split:8", window: "window:live", label: "shell", program: "hunk"}
	shell := &split{BlockID: "split:7", window: "window:live", label: "shell", program: "zsh"}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{sideAt("split:9", "window:live"), typed, shell}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedBuildTab("split:9"), func(d *Deps) { d.Rex = rex })
	eventually(t, "the typed hunk closed", func() bool { return rex.allSplits()[1].closed })
	h.still("ENG-7", lifecycle.Working)
	if got := rex.allSplits(); len(got) != 3 || got[0].closed || got[2].closed {
		t.Fatalf("panes %+v, want the named pane and the user's shell open, and no new pane", got)
	}
	if v := h.task("ENG-7"); !v.Live || v.Flag != "" {
		t.Fatalf("task = %+v, want its Worker kept", v)
	}
}

// A Record from before the side pane's command cannot tell what its pane
// shows, so the tab ends with one new pane that shows the current base.
func TestRecoveryReplacesTheSidePanesOfARecordThatKeepsNoCommand(t *testing.T) {
	live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
	typed := &split{BlockID: "split:8", window: "window:live", label: "shell", program: "hunk"}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{sideAt("split:9", "window:live"), typed}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedBuildTab("split:9", func(r *Record) { r.Side = nil }), func(d *Deps) { d.Rex = rex })
	eventually(t, "a new side pane", func() bool { return len(rex.allSplits()) == 3 })
	eventually(t, "the old panes closed", func() bool { got := rex.allSplits(); return got[0].closed && got[1].closed })
	if got := rex.allSplits()[2]; got.anchor != "split:9" || got.cwd != "/wt/eng-7" || !slices.Equal(got.argv, hunkDiff) || got.closed {
		t.Fatalf("new pane = %+v, want the diff in the place of split:9", got)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(rex.allSplits()); n != 3 {
		t.Fatalf("%d panes, want no pane after the new one", n)
	}
	h.still("ENG-7", lifecycle.Working)
}

func TestRecoveryReplacesASidePaneThatShowsAnotherWorktree(t *testing.T) {
	live := &tab{Window: Window{"session:data", "window:live", "block:live"}, argv: []string{"claude", "working"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{sideAt("split:9", "window:live")}}
	old := func(r *Record) { r.Side = &sidePane{Dir: "/wt/eng-7-old", Argv: hunkDiff} }
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedBuildTab("split:9", old), func(d *Deps) { d.Rex = rex })
	eventually(t, "a new side pane", func() bool { return len(rex.allSplits()) == 2 })
	eventually(t, "the old pane closed", func() bool { return rex.allSplits()[0].closed })
	if got := rex.allSplits()[1]; got.anchor != "split:9" || got.cwd != "/wt/eng-7" || !slices.Equal(got.argv, hunkDiff) {
		t.Fatalf("new pane = %+v, want the diff of /wt/eng-7 in the place of split:9", got)
	}
	h.still("ENG-7", lifecycle.Working)
}

// A rebase or a merge of the default branch moves the merge-base. The next
// poll replaces the pane in its place, with the new base.
func TestARebaseReplacesTheSidePaneWithinOnePoll(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	h.waitTab("claude working")
	first := h.waitSplit(1)
	eventually(t, "the Worker live", func() bool { return h.task("ENG-1").Live })

	h.wt.rebase("base2")
	pane := h.waitSplit(2)
	if pane.anchor != first.BlockID || pane.cwd != filepath.Join(h.wt.root, "eng-1-billing") || !slices.Equal(pane.argv, []string{"hunk", "diff", "--watch", "base2"}) {
		t.Fatalf("new pane = %+v, want the diff since base2 in the place of %s", pane, first.BlockID)
	}
	eventually(t, "the old pane closed", func() bool { return h.rex.allSplits()[0].closed })
	time.Sleep(100 * time.Millisecond)
	if n := len(h.rex.allSplits()); n != 2 {
		t.Fatalf("%d panes after more polls, want 2", n)
	}
	h.still("ENG-1", lifecycle.Working)
}

func TestARemovedWorktreeClosesItsSidePane(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	h.waitSplit(1)
	eventually(t, "the Worker live", func() bool { return h.task("ENG-1").Live })
	before := h.task("ENG-1")

	h.wt.lose(filepath.Join(h.wt.root, "eng-1-billing"))
	eventually(t, "the side pane closed", func() bool { return h.rex.allSplits()[0].closed })
	time.Sleep(100 * time.Millisecond)
	if n := len(h.rex.allSplits()); n != 1 {
		t.Fatalf("%d panes, want no new pane for a worktree that is gone", n)
	}
	h.still("ENG-1", lifecycle.Working)
	if v := h.task("ENG-1"); !reflect.DeepEqual(v, before) {
		t.Fatalf("task = %+v, want it as it was: %+v", v, before)
	}
	if tabs := h.rex.open("claude working"); len(tabs) != 1 || tabs[0].BlockID != worker.BlockID {
		t.Fatalf("Worker tabs %+v, want the Worker kept", tabs)
	}
}

// syncBuffer is a log that a test reads while lanes write to it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestAFailedSidePaneLeavesTheReviewWorkerRunning(t *testing.T) {
	var log syncBuffer
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		d.Log = slog.New(slog.NewTextHandler(&log, nil))
	})
	h.rex.splitErr = errors.New("rex split: no such block")
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	eventually(t, "the warning", func() bool {
		return strings.Contains(log.String(), `level=WARN msg="side pane" task=PR-7 err="rex split: no such block"`)
	})
	if v := h.task("PR-7"); !v.Live || v.Stage != "Reviewing" || v.Flag != "" {
		t.Fatalf("task = %+v, want a live Worker in Reviewing with no flag", v)
	}
}

func TestSidePaneEventsLeaveTheReviewAsItWas(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	pane := h.waitSplit(1)
	eventually(t, "the Worker live", func() bool { return h.task("PR-7").Live })
	before := h.task("PR-7")

	for _, ev := range []RexEvent{
		{Kind: EvStatus, Status: Status{State: "done"}},
		{Kind: EvStatus, Status: Status{State: "blocked", Kind: "question"}},
		{Kind: EvExited, ExitCode: 1},
		{Kind: EvClosed},
	} {
		ev.BlockID = pane.BlockID
		h.rex.events <- ev
	}
	h.still("PR-7", lifecycle.Reviewing)
	if v := h.task("PR-7"); !reflect.DeepEqual(v, before) {
		t.Fatalf("task = %+v after side pane events, want it as it was: %+v", v, before)
	}
	if tabs := h.rex.open("claude reviewing"); len(tabs) != 1 || tabs[0].BlockID != worker.BlockID {
		t.Fatalf("Worker tabs %+v, want the first Worker only", tabs)
	}

	conversation := h.lastSpec().Conversation
	h.rex.userCloses(worker)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: pane.BlockID}
	eventually(t, "the tab closed flag", func() bool { return h.task("PR-7").Flag == "tab closed" })
	h.still("PR-7", lifecycle.Reviewing)
	h.open("PR-7")
	h.waitTab("claude --resume " + conversation)
}

// The side pane holds a tab open after its Worker's block closes.
func TestAClosedReviewWorkerClosesItsSidePane(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	h.waitSplit(1)
	h.status(worker.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)

	h.rex.blockCloses(worker)
	eventually(t, "the side pane closed", func() bool { return h.rex.allSplits()[0].closed })
	if v := h.task("PR-7"); v.Live || v.Stage != "Review ready" || v.Flag != "" {
		t.Fatalf("task = %+v, want Review ready with no Worker", v)
	}
}

func TestRecoveryClosesTheSidePaneOfALostReviewWorker(t *testing.T) {
	rex := &fakeRex{events: make(chan RexEvent, 16), splits: []*split{{BlockID: "split:9", window: "window:gone"}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady), func(d *Deps) { d.Rex = rex })
	eventually(t, "the side pane closed", func() bool { return rex.allSplits()[0].closed })
	h.still("R-3", lifecycle.ReviewReady)
}

// A Worker that an older lineup started, or whose side pane closed, gets the
// pane back when the Scheduler starts.
func TestRecoveryOpensAMissingSidePaneBesideALiveReviewWorker(t *testing.T) {
	live := &tab{Window: Window{"session:reviews", "window:live", "block:live"}, argv: []string{"claude", "reviewing"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{{BlockID: "split:9", window: "window:live", closed: true}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReviewTab(lifecycle.ReviewReady, "window:live", "block:live", "split:9"), func(d *Deps) { d.Rex = rex })
	eventually(t, "a new side pane", func() bool { return len(rex.allSplits()) == 2 })
	got := rex.allSplits()[1]
	if got.anchor != "block:live" || got.cwd != "/repos/acme/data.review-pr-7" || !slices.Equal(got.argv, sidePR7) {
		t.Fatalf("split = %+v", got)
	}
	h.still("R-3", lifecycle.ReviewReady)
	if v := h.task("R-3"); !v.Live {
		t.Fatalf("task = %+v, want its Worker kept", v)
	}
}

func TestRecoveryKeepsALiveSidePane(t *testing.T) {
	live := &tab{Window: Window{"session:reviews", "window:live", "block:live"}, argv: []string{"claude", "reviewing"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{sideAt("split:9", "window:live")}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReviewTab(lifecycle.ReviewReady, "window:live", "block:live", "split:9"), func(d *Deps) { d.Rex = rex })
	h.still("R-3", lifecycle.ReviewReady)
	if got := rex.allSplits(); len(got) != 1 || got[0].closed {
		t.Fatalf("side panes %+v, want the live one only, open", got)
	}
}

// A review Worker's tab keeps one rexweb pane across restarts. rexweb names
// its block after the page, and bash runs the rexweb-pane script. Recovery
// keeps the Record's pane, closes the panes that earlier restarts left, and
// opens none. The user's bash shell stays.
func TestRecoveryKeepsOneRexwebPaneOfAReviewWorker(t *testing.T) {
	live := &tab{Window: Window{"session:reviews", "window:live", "block:live"}, argv: []string{"claude", "reviewing"}}
	rexweb := func(block string) *split {
		return &split{BlockID: block, window: "window:live", label: "PD-668: Say that the backend stacks released the overlay tiles buckets by cbsmit…",
			program: "bash", args: []string{"/bin/bash", "/Users/me/Applications/RexWeb.app/Contents/Resources/rexweb-pane", prURL7}}
	}
	shell := &split{BlockID: "split:6", window: "window:live", label: "shell", program: "bash", args: []string{"-bash"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{rexweb("split:7"), rexweb("split:8"), rexweb("split:9"), shell}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReviewTab(lifecycle.ReviewReady, "window:live", "block:live", "split:9"), func(d *Deps) { d.Rex = rex })
	eventually(t, "the older rexweb panes closed", func() bool { got := rex.allSplits(); return got[0].closed && got[1].closed })
	h.still("R-3", lifecycle.ReviewReady)
	if got := rex.allSplits(); len(got) != 4 || got[2].closed || got[3].closed {
		t.Fatalf("panes %+v, want the named pane and the user's shell open, and no new pane", got)
	}
	if v := h.task("R-3"); !v.Live || v.Flag != "" {
		t.Fatalf("task = %+v, want its Worker kept", v)
	}
}

func TestRecoveryClosesAReviewTabThatRexRestoredAsAShell(t *testing.T) {
	restored := &tab{Window: Window{"session:reviews", "window:live", "block:live"}, shell: true}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{restored}, splits: []*split{{BlockID: "split:9", window: "window:live"}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReviewTab(lifecycle.ReviewReady, "window:live", "block:live", "split:9"), func(d *Deps) { d.Rex = rex })
	eventually(t, "the restored tab closed", func() bool { return rex.allSplits()[0].closed })
	h.still("R-3", lifecycle.ReviewReady)
	if v := h.task("R-3"); v.Live || v.Tab {
		t.Fatalf("task = %+v, want no Worker and no tab", v)
	}
	rex.mu.Lock()
	defer rex.mu.Unlock()
	if !slices.Equal(rex.calls, []string{"close window:live"}) || len(rex.splits) != 1 {
		t.Fatalf("calls = %v with %d side panes, want one close and no new pane", rex.calls, len(rex.splits))
	}
}

// pr-review asks for the decision on each finding with AskUserQuestion after
// it saves the findings.
func TestAReviewQuestionMeetsTheContractOnlyAfterTheFindings(t *testing.T) {
	cases := []struct {
		name     string
		findings bool
		kind     string
		stage    lifecycle.Stage
		flag     string
	}{
		{"a question after the findings", true, "question", lifecycle.ReviewReady, ""},
		{"a question before the findings", false, "question", lifecycle.Reviewing, "question"},
		{"a permission after the findings", true, "permission", lifecycle.Reviewing, "permission"},
	}
	for _, c := range cases {
		h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
		h.gh.setReview(openPR(7, "h1"))
		h.review(prURL7)
		worker := h.waitTab("claude reviewing")
		if c.findings {
			h.writeFindings(7, "h1")
		}
		h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "blocked", Kind: c.kind, Msg: "answer: finding 3"}}
		h.waitStage("PR-7", c.stage)
		h.still("PR-7", c.stage)
		if v := h.task("PR-7"); v.Flag != c.flag {
			t.Errorf("%s: %s with flag %q, want %s with flag %q", c.name, v.Stage, v.Flag, c.stage, c.flag)
		}
	}
}

func TestLaterStatusesKeepAReviewReadyFromAQuestion(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	h.writeFindings(7, "h1")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "blocked", Kind: "question"}}
	h.waitStage("PR-7", lifecycle.ReviewReady)
	for _, state := range []string{"working", "done", "idle"} {
		h.status(worker.BlockID, state)
		h.still("PR-7", lifecycle.ReviewReady)
		if v := h.task("PR-7"); v.Flag != "" {
			t.Fatalf("after %s: flag %q, want none", state, v.Flag)
		}
	}
	h.rex.userCloses(worker)
	eventually(t, "the Worker gone", func() bool { return !h.task("PR-7").Live })
	if res := h.review(prURL7); res.Action != ReviewReopened {
		t.Fatalf("review after the tab closed = %+v, want reopened", res)
	}
}

func TestABuildWorkerQuestionOnlyFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.set(PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}, 42)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.rex.exits(h.waitTab("wt ada/eng-1-billing"), 0)
	worker := h.waitTab("claude working")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "blocked", Kind: "question"}}
	eventually(t, "the question flag", func() bool { return h.task("ENG-1").Flag == "question" })
	h.still("ENG-1", lifecycle.Working)
}

func TestARerunReviewKeepsTheConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	conversation := h.lastSpec().Conversation
	h.status(worker.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)

	h.do(Request{Op: "rerun", Task: "PR-7", Stage: "reviewing"})
	h.waitTyped(worker.BlockID, "prompt reviewing")
	if spec := h.lastSpec(); spec.Stage != lifecycle.Reviewing || spec.Conversation != conversation || spec.PRURL != prURL7 {
		t.Fatalf("rerun spec = %+v, want Reviewing in conversation %s", spec, conversation)
	}
	var got string
	h.s.call(func() { got = h.s.recs["PR-7"].Conversation })
	if got != conversation || len(h.rex.open("claude reviewing")) != 1 {
		t.Fatalf("conversation %q after the rerun, want %q in the one tab", got, conversation)
	}
}

// The side pane keeps a review tab open after its Worker exits. The restore
// closes that tab first, so the Task's window is the new tab and a close
// reaches it.
func TestOpenClosesATabThatRexKeptAfterTheWorkerExited(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	old := h.waitTab("claude reviewing")
	conversation := h.lastSpec().Conversation
	h.waitSplit(1)
	h.status(old.BlockID, "done")
	h.waitStage("PR-7", lifecycle.ReviewReady)
	h.keptTabRestore("PR-7", old, conversation)
	if tabs := h.rex.open("claude reviewing"); len(tabs) != 0 {
		t.Fatalf("the kept tab stayed open: %+v", tabs)
	}

	h.do(Request{Op: "close", Task: "PR-7"})
	eventually(t, "the reopened tab closed", func() bool { return len(h.rex.open("claude --resume "+conversation)) == 0 })
}

func TestOpenOfAReviewReadyTaskWithNoConversationReviewsAgain(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady))
	res := h.open("R-3")
	if res.Action != OpenStarted || res.Stage != "Reviewing" {
		t.Fatalf("open = %+v, want a started review in Reviewing", res)
	}
	h.waitTab("claude reviewing")
}
