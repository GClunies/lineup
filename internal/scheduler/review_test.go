package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
	if res.ID != "pr-7" || res.Kind != lifecycle.KindReview || res.Action != ReviewAdded || res.Stage != "Reviewing" || res.Title != "data#7 Fix the tooltip" || res.PRURL != prURL7 || res.ReviewHead != "h1" {
		t.Fatalf("review = %+v", res)
	}
	worker := h.waitTab("claude reviewing")
	if worker.cwd != "/repos/acme/data" || worker.SessionID != "session:reviews" || worker.label != "pr-7 data#7 Fix the tooltip" {
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
	eventually(t, "stopped without findings", func() bool { return h.task("pr-7").Flag == "stopped" })

	h.writeFindings(7, "h1")
	h.status(worker.BlockID, "working")
	h.status(worker.BlockID, "done")
	h.waitStage("pr-7", lifecycle.ReviewReady)
	if v := h.task("pr-7"); v.Group != "Needs you" || v.Note != "" {
		t.Fatalf("review ready = %+v", v)
	}

	moved := openPR(7, "h2")
	h.gh.setReview(moved)
	eventually(t, "the head moved note", func() bool { return h.task("pr-7").Note == "head moved" })

	// The user tells the Worker to post. While it works, the review waits.
	h.status(worker.BlockID, "working")
	moved.ReviewedAt = time.Now().Add(time.Hour)
	h.gh.setReview(moved)
	h.still("pr-7", lifecycle.ReviewReady)
	h.status(worker.BlockID, "blocked")
	h.still("pr-7", lifecycle.ReviewReady)

	h.status(worker.BlockID, "done")
	h.waitStage("pr-7", lifecycle.Done)
	if v := h.task("pr-7"); v.Outcome != "reviewed" {
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
	if v := h.task("pr-7"); v.Worktree != "" || v.Note != "#7" {
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
		h.still("pr-7", lifecycle.Reviewing)
		h.status(worker.BlockID, "idle")
		h.waitStage("pr-7", lifecycle.Done)
		if v := h.task("pr-7"); v.Outcome != outcome {
			t.Fatalf("%s: outcome %q, want %q", state, v.Outcome, outcome)
		}
	}
}

func TestTheLastReviewKeepsASessionThatHoldsOtherTabs(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	if _, err := h.rex.NewWindow(context.Background(), "session:reviews", "the user's own tab", "/", nil, []string{"zsh"}); err != nil {
		t.Fatal(err)
	}
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	merged := openPR(7, "h1")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	h.status(worker.BlockID, "idle")
	h.waitStage("pr-7", lifecycle.Done)
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
	h.waitStage("pr-7", lifecycle.ReviewReady)
}

func TestAnOldFindingsFileOfAnotherHeadDoesNotMeetTheContract(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h0")
	old := filepath.Join(h.stateHome, "pr-review", "acme-data-7", "findings-h0.json")
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	h.review(prURL7)
	h.status(h.waitTab("claude reviewing").BlockID, "done")
	eventually(t, "stopped", func() bool { return h.task("pr-7").Flag == "stopped" })
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
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"pr-7"}) {
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
	eventually(t, "setup failed", func() bool { return h.task("pr-7").Flag == "setup failed" })
	if n := len(h.rex.open("claude reviewing")); n != 0 {
		t.Fatalf("a Worker runs after the setup failed: %d tabs", n)
	}

	res := h.review(prURL7)
	if res.ID != "pr-7" || res.Action != ReviewRestarted {
		t.Fatalf("review of the stopped Task = %+v, want pr-7 restarted", res)
	}
	h.waitTab("claude reviewing")
	eventually(t, "the flag to clear", func() bool { return h.task("pr-7").Flag == "" })
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"pr-7"}) {
		t.Fatalf("board = %v", ids)
	}
}

func TestReviewOfAQueuedReviewStartsIt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.s.call(func() {
		r := &Record{
			Title: "data#7 Fix the tooltip", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: reviewSession,
			Review: &ReviewPR{Repo: "acme/data", Number: 7, URL: prURL7, Head: "h1", Base: "main"}, CreatedAt: h.s.now(), StageSince: h.s.now(),
		}
		r.ID = h.s.reviewID(*r.Review)
		r.Task = *h.s.board.AddReview(r.ID, "", lifecycle.Policy{})
		h.s.board.Tasks[r.ID] = &r.Task
		h.s.recs[r.ID] = r
	})
	if v := h.task("pr-7"); v.Stage != "Queued" {
		t.Fatalf("Task = %+v, want Queued", v)
	}

	res := h.review(prURL7)
	if res.ID != "pr-7" || res.Action != ReviewStarted {
		t.Fatalf("review of the Queued Task = %+v, want pr-7 started", res)
	}
	h.waitTab("claude reviewing")
}

func TestReviewOfARunningReviewChangesNothing(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	if res := h.review(prURL7); res.Action != ReviewOpen {
		t.Fatalf("review of the running Task = %+v, want open", res)
	}
	h.still("pr-7", lifecycle.Reviewing)
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
	if data != "pr-7" || web != "pr-7-web" || later != "pr-7" {
		t.Errorf("IDs = %q, %q, %q, want pr-7, then pr-7-web beside a live pr-7, then pr-7 once it is Done", data, web, later)
	}
}

func TestADoneReviewDoesNotBlockANewOne(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.do(Request{Op: "close", Task: "pr-7"})
	h.waitStage("pr-7", lifecycle.Done)
	if res := h.review(prURL7); res.Action != ReviewAdded || res.ID != "pr-7" {
		t.Fatalf("review after close = %+v, want a new pr-7", res)
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
	h.do(Request{Op: "close", Task: "pr-7"})
	eventually(t, "the kept note", func() bool {
		return h.task("pr-7").Note == "worktree kept: /repos/acme/data.review-pr-7"
	})
	if v := h.task("pr-7"); v.Worktree != "/repos/acme/data.review-pr-7" || v.Outcome != "closed" {
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
		h.waitStage("pr-7", lifecycle.ReviewReady)
		ev.BlockID = worker.BlockID
		if ev.Kind == EvClosed {
			h.rex.CloseWindow(context.Background(), worker.SessionID, worker.WindowID)
		}
		h.rex.events <- ev
		eventually(t, "the Worker gone", func() bool { return !h.task("pr-7").Live })
		h.s.fill(context.Background())
		h.still("pr-7", lifecycle.ReviewReady)
		if v := h.task("pr-7"); v.Group != "Needs you" || v.Flag != "" {
			t.Fatalf("event %+v: task = %+v, want Needs you with no flag", ev, v)
		}
		if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Review ready"}) {
			t.Fatalf("event %+v: reviews %v, want the slot held by pr-7", ev, got)
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
	h.waitStage("pr-7", lifecycle.ReviewReady)
	h.rex.CloseWindow(context.Background(), worker.SessionID, worker.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: worker.BlockID}
	eventually(h.t, "the Worker gone", func() bool { return !h.task("pr-7").Live })
	return spec
}

func TestReviewOfAClosedReviewReadyWorkerResumesItsConversation(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	reviewing := h.readyReview()
	if u, err := uuid.Parse(reviewing.Conversation); err != nil || u.String() != reviewing.Conversation {
		t.Fatalf("the Reviewing Worker's conversation %q is not a UUID: %v", reviewing.Conversation, err)
	}

	res := h.review(prURL7)
	if res.ID != "pr-7" || res.Action != ReviewReopened {
		t.Fatalf("review of the closed Worker = %+v, want pr-7 reopened", res)
	}
	worker := h.waitTab("claude review_ready")
	if worker.cwd != "/repos/acme/data" || worker.env["XDG_STATE_HOME"] != h.stateHome {
		t.Fatalf("reopened tab = %+v, want the main checkout and the review state folder", worker)
	}
	spec := h.lastSpec()
	if spec.Stage != lifecycle.ReviewReady || spec.Conversation != reviewing.Conversation || spec.Worktree != "/repos/acme/data.review-pr-7" || spec.PRURL != prURL7 {
		t.Fatalf("reopened spec = %+v, want Review ready in conversation %s", spec, reviewing.Conversation)
	}
	eventually(t, "the reopened Worker live", func() bool { return h.task("pr-7").Live })
	if v := h.task("pr-7"); v.Stage != "Review ready" || v.Flag != "" {
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

func TestALostTabWhileReviewingStartsAFreshWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	first := h.waitTab("claude reviewing")
	firstSpec := h.lastSpec()
	h.rex.CloseWindow(context.Background(), first.SessionID, first.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: first.BlockID}
	second := h.waitTab("claude reviewing")
	if second.BlockID == first.BlockID {
		t.Fatal("no fresh Worker")
	}
	if spec := h.lastSpec(); spec.Conversation == "" || spec.Conversation == firstSpec.Conversation {
		t.Fatalf("restarted conversation %q, want a new one after %q", spec.Conversation, firstSpec.Conversation)
	}
	var left int
	h.s.call(func() { left = h.s.recs["pr-7"].Left.Restarts })
	if v := h.task("pr-7"); v.Stage != "Reviewing" || left != testPolicy.Budgets.Restarts-1 {
		t.Fatalf("stage %s, restarts left %d", v.Stage, left)
	}
}

func seedReview(stage lifecycle.Stage) func(*store.Store) {
	return seedReviewTab(stage, "window:gone", "block:gone", "")
}

func seedReviewTab(stage lifecycle.Stage, window, block, hunk string) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{
			Task:  lifecycle.Task{ID: "R-3", Kind: lifecycle.KindReview, Stage: stage, Policy: testPolicy, Left: testPolicy.Budgets},
			Title: "data#7 Fix the tooltip", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: reviewSession,
			Review:   &ReviewPR{Repo: "acme/data", Number: 7, URL: prURL7, Head: "h1", Base: "main"},
			Worktree: "/repos/acme/data.review-pr-7", SessionID: "session:reviews", WindowID: window, BlockID: block, HunkBlockID: hunk, Job: JobWorker,
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
	h.waitStage("pr-7", lifecycle.ReviewReady)
	h.still("pr-7", lifecycle.ReviewReady)
	if v := h.task("pr-7"); v.Note != "" {
		t.Fatalf("note %q, want none: the review read the current head", v.Note)
	}
}

var hunkPR7 = []string{"sh", "-c", `GH_TOKEN=$(gh auth token) exec hunk gh pr "$1"`, "hunk", prURL7}

// waitSplit waits until Split has opened n blocks in all, and returns the last.
func (h *harness) waitSplit(n int) split {
	h.t.Helper()
	var ss []split
	eventually(h.t, itoa(n)+" hunk panes", func() bool { ss = h.rex.allSplits(); return len(ss) == n })
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

func TestEachReviewWorkerStartOpensThePRInHunkBesideIt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	started := h.waitTab("claude reviewing")
	pane := h.waitSplit(1)
	if pane.anchor != started.BlockID || pane.window != started.WindowID || pane.cwd != "/repos/acme/data.review-pr-7" || !slices.Equal(pane.argv, hunkPR7) {
		t.Fatalf("hunk pane = %+v, want hunk on %s beside block %s in the review worktree", pane, prURL7, started.BlockID)
	}

	h.rex.CloseWindow(context.Background(), started.SessionID, started.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: started.BlockID}
	restarted := h.waitNewTab("claude reviewing", started.BlockID)
	if pane := h.waitSplit(2); pane.anchor != restarted.BlockID {
		t.Fatalf("restart: hunk pane beside %s, want %s", pane.anchor, restarted.BlockID)
	}

	h.do(Request{Op: "rerun", Task: "pr-7", Stage: "reviewing"})
	rerun := h.waitNewTab("claude reviewing", restarted.BlockID)
	if pane := h.waitSplit(3); pane.anchor != rerun.BlockID {
		t.Fatalf("rerun: hunk pane beside %s, want %s", pane.anchor, rerun.BlockID)
	}

	h.writeFindings(7, "h1")
	h.status(rerun.BlockID, "done")
	h.waitStage("pr-7", lifecycle.ReviewReady)
	h.rex.CloseWindow(context.Background(), rerun.SessionID, rerun.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: rerun.BlockID}
	eventually(t, "the Worker gone", func() bool { return !h.task("pr-7").Live })
	h.review(prURL7)
	reopened := h.waitTab("claude review_ready")
	if pane := h.waitSplit(4); pane.anchor != reopened.BlockID || !slices.Equal(pane.argv, hunkPR7) {
		t.Fatalf("reopen: hunk pane = %+v, want hunk beside %s", pane, reopened.BlockID)
	}
	h.still("pr-7", lifecycle.ReviewReady)
	if n := len(h.rex.allSplits()); n != 4 {
		t.Fatalf("%d hunk panes for 4 Worker starts", n)
	}
}

func TestABuildWorkerOpensNoHunkPane(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID}
	h.waitTab("claude working")
	eventually(t, "the Worker live", func() bool { return h.task("ENG-1").Live })
	if ss := h.rex.allSplits(); len(ss) != 0 {
		t.Fatalf("a build Worker opened panes %+v", ss)
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

func TestAFailedHunkPaneLeavesTheReviewWorkerRunning(t *testing.T) {
	var log syncBuffer
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		d.Log = slog.New(slog.NewTextHandler(&log, nil))
	})
	h.rex.splitErr = errors.New("rex split: no such block")
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	eventually(t, "the warning", func() bool {
		return strings.Contains(log.String(), `level=WARN msg="hunk pane" task=pr-7 err="rex split: no such block"`)
	})
	if v := h.task("pr-7"); !v.Live || v.Stage != "Reviewing" || v.Flag != "" {
		t.Fatalf("task = %+v, want a live Worker in Reviewing with no flag", v)
	}
}

func TestHunkPaneEventsLeaveTheReviewAsItWas(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	pane := h.waitSplit(1)
	eventually(t, "the Worker live", func() bool { return h.task("pr-7").Live })
	before := h.task("pr-7")

	for _, ev := range []RexEvent{
		{Kind: EvStatus, Status: Status{State: "done"}},
		{Kind: EvStatus, Status: Status{State: "blocked", Kind: "question"}},
		{Kind: EvExited, ExitCode: 1},
		{Kind: EvClosed},
	} {
		ev.BlockID = pane.BlockID
		h.rex.events <- ev
	}
	h.still("pr-7", lifecycle.Reviewing)
	if v := h.task("pr-7"); v != before {
		t.Fatalf("task = %+v after hunk pane events, want it as it was: %+v", v, before)
	}
	if tabs := h.rex.open("claude reviewing"); len(tabs) != 1 || tabs[0].BlockID != worker.BlockID {
		t.Fatalf("Worker tabs %+v, want the first Worker only", tabs)
	}

	// Closing the whole tab closes both blocks and restarts the Worker once.
	h.rex.CloseWindow(context.Background(), worker.SessionID, worker.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: worker.BlockID}
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: pane.BlockID}
	h.waitNewTab("claude reviewing", worker.BlockID)
	h.still("pr-7", lifecycle.Reviewing)
	var left int
	h.s.call(func() { left = h.s.recs["pr-7"].Left.Restarts })
	if left != testPolicy.Budgets.Restarts-1 {
		t.Fatalf("restarts left %d, want one restart", left)
	}
}

// The hunk pane holds a tab open after its Worker's block closes.
func TestAClosedReviewWorkerClosesItsHunkPane(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.writeFindings(7, "h1")
	h.review(prURL7)
	worker := h.waitTab("claude reviewing")
	h.waitSplit(1)
	h.status(worker.BlockID, "done")
	h.waitStage("pr-7", lifecycle.ReviewReady)

	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: worker.BlockID}
	eventually(t, "the hunk pane closed", func() bool { return h.rex.allSplits()[0].closed })
	if v := h.task("pr-7"); v.Live || v.Stage != "Review ready" || v.Flag != "" {
		t.Fatalf("task = %+v, want Review ready with no Worker", v)
	}
}

func TestRecoveryClosesTheHunkPaneOfALostReviewWorker(t *testing.T) {
	rex := &fakeRex{events: make(chan RexEvent, 16), splits: []*split{{BlockID: "split:9", window: "window:gone"}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReview(lifecycle.ReviewReady), func(d *Deps) { d.Rex = rex })
	eventually(t, "the hunk pane closed", func() bool { return rex.allSplits()[0].closed })
	h.still("R-3", lifecycle.ReviewReady)
}

// A Worker that an older lineup started, or whose hunk pane closed, gets the
// pane back when the Scheduler starts.
func TestRecoveryOpensAMissingHunkPaneBesideALiveReviewWorker(t *testing.T) {
	live := &tab{Window: Window{"session:reviews", "window:live", "block:live"}, argv: []string{"claude", "reviewing"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{{BlockID: "split:9", window: "window:live", closed: true}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReviewTab(lifecycle.ReviewReady, "window:live", "block:live", "split:9"), func(d *Deps) { d.Rex = rex })
	eventually(t, "a new hunk pane", func() bool { return len(rex.allSplits()) == 2 })
	got := rex.allSplits()[1]
	if got.anchor != "block:live" || got.cwd != "/repos/acme/data.review-pr-7" || !slices.Equal(got.argv, hunkArgv(prURL7)) {
		t.Fatalf("split = %+v", got)
	}
	h.still("R-3", lifecycle.ReviewReady)
	if v := h.task("R-3"); !v.Live {
		t.Fatalf("task = %+v, want its Worker kept", v)
	}
}

func TestRecoveryKeepsALiveHunkPane(t *testing.T) {
	live := &tab{Window: Window{"session:reviews", "window:live", "block:live"}, argv: []string{"claude", "reviewing"}}
	rex := &fakeRex{events: make(chan RexEvent, 16), tabs: []*tab{live}, splits: []*split{{BlockID: "split:9", window: "window:live"}}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seedReviewTab(lifecycle.ReviewReady, "window:live", "block:live", "split:9"), func(d *Deps) { d.Rex = rex })
	h.still("R-3", lifecycle.ReviewReady)
	if n := len(rex.allSplits()); n != 1 {
		t.Fatalf("%d hunk panes, want the live one only", n)
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
		h.waitStage("pr-7", c.stage)
		h.still("pr-7", c.stage)
		if v := h.task("pr-7"); v.Flag != c.flag {
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
	h.waitStage("pr-7", lifecycle.ReviewReady)
	for _, state := range []string{"working", "done", "idle"} {
		h.status(worker.BlockID, state)
		h.still("pr-7", lifecycle.ReviewReady)
		if v := h.task("pr-7"); v.Flag != "" {
			t.Fatalf("after %s: flag %q, want none", state, v.Flag)
		}
	}
	h.rex.CloseWindow(context.Background(), worker.SessionID, worker.WindowID)
	h.rex.events <- RexEvent{Kind: EvClosed, BlockID: worker.BlockID}
	eventually(t, "the Worker gone", func() bool { return !h.task("pr-7").Live })
	if res := h.review(prURL7); res.Action != ReviewReopened {
		t.Fatalf("review after the tab closed = %+v, want reopened", res)
	}
}

func TestABuildWorkerQuestionOnlyFlagsTheTask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.set(PRKey{"acme/data", "ada/eng-1-billing"}, 42)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID}
	worker := h.waitTab("claude working")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "blocked", Kind: "question"}}
	eventually(t, "the question flag", func() bool { return h.task("ENG-1").Flag == "question" })
	h.still("ENG-1", lifecycle.Working)
}
