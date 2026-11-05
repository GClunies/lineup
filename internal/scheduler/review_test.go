package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
		name string
		pr   ReviewSnapshot
		want lifecycle.Signal
	}{
		{"open, no review", ReviewSnapshot{State: "OPEN"}, nil},
		{"review before the add", ReviewSnapshot{State: "OPEN", ReviewedAt: added.Add(-time.Minute)}, nil},
		{"review after the add", ReviewSnapshot{State: "OPEN", ReviewedAt: added.Add(time.Minute)}, lifecycle.PRReviewed{}},
		{"merged", ReviewSnapshot{State: "MERGED"}, lifecycle.PRMerged{}},
		{"closed", ReviewSnapshot{State: "CLOSED"}, lifecycle.PRClosed{}},
		{"reviewed, then merged", ReviewSnapshot{State: "MERGED", ReviewedAt: added.Add(time.Minute)}, lifecycle.PRReviewed{}},
	}
	for _, c := range cases {
		if got := c.pr.Level(added); got != c.want {
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

func TestReviewFromStartToReviewedAndCleanedUp(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))

	res := h.review(prURL7)
	if res.ID != "R-1" || res.Existing || res.Stage != "Reviewing" || res.Title != "data#7 Fix the tooltip" || res.PRURL != prURL7 {
		t.Fatalf("review = %+v", res)
	}
	worker := h.waitTab("claude reviewing")
	if worker.cwd != "/repos/acme/data" || worker.SessionID != "session:reviews" || worker.label != "R-1 data#7 Fix the tooltip" {
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

	h.status(worker.BlockID, "done")
	eventually(t, "stopped without findings", func() bool { return h.task("R-1").Flag == "stopped" })

	h.writeFindings(7, "h1")
	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "done")
	h.waitStage("R-1", lifecycle.ReviewReady)
	if v := h.task("R-1"); v.Group != "Needs you" || v.Note != "" {
		t.Fatalf("review ready = %+v", v)
	}

	moved := openPR(7, "h2")
	h.gh.setReview(moved)
	eventually(t, "the head moved note", func() bool { return h.task("R-1").Note == "head moved" })

	// The user tells the Worker to post. While it works, the review waits.
	h.status(worker.BlockID, "working")
	moved.ReviewedAt = time.Now().Add(time.Hour)
	h.gh.setReview(moved)
	h.still("R-1", lifecycle.ReviewReady)
	h.status(worker.BlockID, "blocked")
	h.still("R-1", lifecycle.ReviewReady)

	h.status(worker.BlockID, "done")
	h.waitStage("R-1", lifecycle.Done)
	if v := h.task("R-1"); v.Outcome != "reviewed" {
		t.Fatalf("done = %+v", v)
	}
	eventually(t, "the review worktree removed", func() bool { return len(h.reviewsRemoved()) == 1 })
	if got := h.reviewsRemoved()[0]; got != "/repos/acme/data.review-pr-7" {
		t.Fatalf("removed %s", got)
	}
	eventually(t, "the reviews session removed", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		return slices.Contains(h.rex.destroyed, "session:reviews")
	})
	if len(h.rex.open("claude reviewing")) != 0 {
		t.Fatal("the review tab is still open after Done")
	}
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if len(h.wt.removed) != 0 {
		t.Fatalf("a review Task ran wt remove for %v", h.wt.removed)
	}
	if v := h.task("R-1"); v.Worktree != "" || v.Note != "#7" {
		t.Fatalf("done task after cleanup = %+v", v)
	}
}

func TestMergedAndClosedPRsEndTheReview(t *testing.T) {
	for state, outcome := range map[string]string{"MERGED": "merged", "CLOSED": "closed"} {
		h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
		h.gh.setReview(openPR(7, "h1"))
		h.review(prURL7)
		worker := h.waitTab("claude reviewing")
		gone := openPR(7, "h1")
		gone.State = state
		h.gh.setReview(gone)
		h.still("R-1", lifecycle.Reviewing)
		h.status(worker.BlockID, "idle")
		h.waitStage("R-1", lifecycle.Done)
		if v := h.task("R-1"); v.Outcome != outcome {
			t.Fatalf("%s: outcome %q, want %q", state, v.Outcome, outcome)
		}
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
	h.waitStage("R-1", lifecycle.ReviewReady)
}

func TestAnOldFindingsFileOfAnotherHeadDoesNotMeetTheContract(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h0")
	old := filepath.Join(h.stateHome, "pr-review", "acme-data-7", "findings-h0.json")
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	h.review(prURL7)
	h.status(h.waitTab("claude reviewing").BlockID, "done")
	eventually(t, "stopped", func() bool { return h.task("R-1").Flag == "stopped" })
}

func TestReviewTwiceOnOnePRMakesOneTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	first := h.review(prURL7)
	h.waitTab("claude reviewing")
	for _, again := range []string{prURL7 + "/files", "Acme/Data#7"} {
		res := h.review(again)
		if !res.Existing || res.ID != first.ID {
			t.Fatalf("%s: %+v, want the open %s", again, res, first.ID)
		}
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"R-1"}) {
		t.Fatalf("board = %v", ids)
	}
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if len(h.wt.added) != 1 {
		t.Fatalf("made the review worktree %d times", len(h.wt.added))
	}
}

func TestADoneReviewDoesNotBlockANewOne(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.do(Request{Op: "close", Task: "R-1"})
	h.waitStage("R-1", lifecycle.Done)
	if res := h.review(prURL7); res.Existing || res.ID != "R-2" {
		t.Fatalf("review after close = %+v, want a new R-2", res)
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
	h.do(Request{Op: "close", Task: "R-1"})
	eventually(t, "the kept note", func() bool {
		return h.task("R-1").Note == "worktree kept: /repos/acme/data.review-pr-7"
	})
	if v := h.task("R-1"); v.Worktree != "/repos/acme/data.review-pr-7" || v.Outcome != "closed" {
		t.Fatalf("closed task = %+v", v)
	}
}

func TestTheBranchPollSkipsReviewTasks(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
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

func TestALostTabInReviewReadyEndsTheReview(t *testing.T) {
	for _, ev := range []RexEvent{{Kind: EvClosed}, {Kind: EvExited, ExitCode: 0}, {Kind: EvExited, ExitCode: 130}} {
		h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
		h.gh.setReview(openPR(7, "h1"))
		h.writeFindings(7, "h1")
		h.review(prURL7)
		worker := h.waitTab("claude reviewing")
		h.status(worker.BlockID, "done")
		h.waitStage("R-1", lifecycle.ReviewReady)
		ev.BlockID = worker.BlockID
		h.rex.events <- ev
		h.waitStage("R-1", lifecycle.Done)
		if v := h.task("R-1"); v.Outcome != "closed" {
			t.Fatalf("event %+v: outcome %q, want closed", ev, v.Outcome)
		}
		eventually(t, "cleanup", func() bool { return len(h.reviewsRemoved()) == 1 })
	}
}

func TestALostTabWhileReviewingStartsAFreshWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	first := h.waitTab("claude reviewing")
	h.rex.CloseWindow(context.Background(), first.SessionID, first.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: first.BlockID}
	second := h.waitTab("claude reviewing")
	if second.BlockID == first.BlockID {
		t.Fatal("no fresh Worker")
	}
	var left int
	h.s.call(func() { left = h.s.recs["R-1"].Left.Restarts })
	if v := h.task("R-1"); v.Stage != "Reviewing" || left != testPolicy.Budgets.Restarts-1 {
		t.Fatalf("stage %s, restarts left %d", v.Stage, left)
	}
}

func seedReview(stage lifecycle.Stage) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "R-3", Kind: lifecycle.KindReview, Stage: stage, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "data#7 Fix the tooltip", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: reviewSession,
			Review:   &ReviewPR{Repo: "acme/data", Number: 7, URL: prURL7, Head: "h1", Base: "main"},
			Worktree: "/repos/acme/data.review-pr-7", SessionID: "session:reviews", WindowID: "window:gone", BlockID: "block:gone", Job: JobWorker,
		}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestRecoveryWithNoTabInReviewReadyEndsTheReview(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady))
	h.waitStage("R-3", lifecycle.Done)
	if v := h.task("R-3"); v.Outcome != "closed" {
		t.Fatalf("recovered review = %+v", v)
	}
	if len(h.rex.open("claude reviewing")) != 0 {
		t.Fatal("recovery ran a finished review again")
	}
	eventually(t, "cleanup", func() bool { return len(h.reviewsRemoved()) == 1 })
}

func TestRecoveryWithNoTabWhileReviewingStartsAFreshWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.Reviewing))
	if w := h.waitTab("claude reviewing"); w.cwd != "/repos/acme/data" {
		t.Fatalf("recovered worker cwd = %s", w.cwd)
	}
}

func TestReviewTasksStayOutsideTheWIPCap(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "cap", N: 1})
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
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
	h.waitStage("R-1", lifecycle.ReviewReady)
	h.still("R-1", lifecycle.ReviewReady)
	if v := h.task("R-1"); v.Note != "" {
		t.Fatalf("note %q, want none: the review read the current head", v.Note)
	}
}
