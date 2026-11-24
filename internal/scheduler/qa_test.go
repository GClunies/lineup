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
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// qaConfig turns QA on for every Repo, with maxWait or 48h.
type qaConfig struct {
	fakeConfig
	maxWait time.Duration
}

func (c qaConfig) QA(string) (QA, error) {
	return QA{Enabled: true, MaxWait: cmp.Or(c.maxWait, 48*time.Hour)}, nil
}

const mergeSHA = "93d24ce9ffefb6cc86bde89b50271457bb0ef9cb"

// mergedAt is half an hour before newClock's time.
var mergedAt = time.Date(2026, 11, 7, 8, 30, 0, 0, time.UTC)

// newQA is a harness with QA on and the clock c.
func newQA(t *testing.T, cfg qaConfig, opts ...func(*Deps)) (*harness, *clock) {
	c := newClock()
	set := func(d *Deps) { d.Config, d.Now = cfg, c.now }
	return newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, append([]func(*Deps){set}, opts...)...), c
}

// merge merges the PR of key outside lineup, as the branch poll reads it.
func (h *harness) merge(key PRKey, number int) {
	h.gh.mu.Lock()
	defer h.gh.mu.Unlock()
	h.gh.prs[key] = PRSnapshot{Number: number, URL: fmt.Sprintf("https://github.com/acme/data/pull/%d", number),
		Raw: json.RawMessage(`["pr-merged"]`), MergeCommit: mergeSHA, MergedAt: mergedAt}
}

// checking starts ENG-1, merges its PR #42, and returns the tab of the QA
// Worker that checks it.
func (h *harness) checking() tab {
	h.t.Helper()
	h.working()
	h.merge(buildKey, 42)
	h.waitStage("ENG-1", lifecycle.Done)
	qa := h.waitTab("claude verifying")
	eventually(h.t, "a live QA Worker", func() bool { return h.task("QA-ENG-1").Live })
	return qa
}

func (h *harness) qa(key, when, text string) error {
	_, err := h.s.Handle(context.Background(), Request{Op: "qa", Task: "QA-ENG-1", Key: key, Value: when, Text: text})
	return err
}

func (h *harness) statuses() []string {
	h.tr.mu.Lock()
	defer h.tr.mu.Unlock()
	return slices.Clone(h.tr.status)
}

func (h *harness) qaSpecs() []WorkerSpec {
	h.hn.mu.Lock()
	defer h.hn.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(h.hn.specs), func(w WorkerSpec) bool { return w.Kind != lifecycle.KindQA })
}

// A Repo with QA off ends the work at the merge (PROD-QA-001).
func TestWithQAOffAMergeAddsNoQATask(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.working()
	h.merge(buildKey, 42)
	h.waitStage("ENG-1", lifecycle.Done)
	time.Sleep(100 * time.Millisecond) // five ticks
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); !slices.Equal(ids, []string{"ENG-1"}) {
		t.Fatalf("board = %v, want no QA Task", ids)
	}
}

// A merge with QA on adds one QA Task, which checks the merge at once with a
// new Worker in a new conversation, in a tab of the qa session. Its Worker
// works in the main checkout and reads the QA worktree at the merge commit
// (PROD-QA-001 to PROD-QA-004, ARCH-QA-005).
func TestAMergeWithQAOnAddsOneQATask(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	qa := h.checking()
	if v := h.task("ENG-1"); v.Outcome != "merged" {
		t.Fatalf("build task = %+v, want merged", v)
	}
	if qa.SessionID != "session:qa" || qa.label != "QA-ENG-1 Add billing model" || qa.cwd != "/repos/acme/data" {
		t.Fatalf("QA tab = %+v, want the qa session and the main checkout", qa)
	}
	v := h.task("QA-ENG-1")
	if v.Kind != lifecycle.KindQA || v.Stage != "Verifying" || v.PRNumber != 42 || !v.Deadline.Equal(mergedAt.Add(48*time.Hour)) {
		t.Fatalf("QA task = %+v, want Verifying with a deadline 48h after the merge", v)
	}
	h.wt.mu.Lock()
	added := slices.Clone(h.wt.qaAdded)
	h.wt.mu.Unlock()
	if !slices.Equal(added, []string{"42 " + mergeSHA}) {
		t.Fatalf("QA worktrees added %v, want one at the merge commit", added)
	}
	specs := h.qaSpecs()
	if len(specs) != 1 {
		t.Fatalf("%d QA Worker specs, want 1", len(specs))
	}
	w := specs[0]
	h.hn.mu.Lock()
	build := h.hn.specs[0]
	trusted := slices.Clone(h.hn.trusted)
	h.hn.mu.Unlock()
	if w.Stage != lifecycle.Verifying || w.Conversation == "" || w.Conversation == build.Conversation || w.Recheck ||
		w.Issue == nil || w.Issue.Identifier != "ENG-1" || w.Worktree != "/repos/acme/data.qa-pr-42" ||
		w.MergeCommit != mergeSHA || !w.MergedAt.Equal(mergedAt) || w.PRURL != "https://github.com/acme/data/pull/42" {
		t.Fatalf("QA spec = %+v, want a new conversation on ENG-1's Issue and merge", w)
	}
	if !slices.Contains(trusted, "/repos/acme/data") {
		t.Fatalf("trusted %v, want the main checkout", trusted)
	}
	// The side pane shows the PR, as a review's does.
	var pane split
	eventually(t, "the QA side pane", func() bool {
		i := slices.IndexFunc(h.rex.allSplits(), func(s split) bool { return s.window == qa.WindowID })
		if i >= 0 {
			pane = h.rex.allSplits()[i]
		}
		return i >= 0
	})
	if pane.cwd != "/repos/acme/data.qa-pr-42" || !slices.Equal(pane.argv, reviewSide("https://github.com/acme/data/pull/42")) {
		t.Fatalf("side pane = %+v, want the PR in the QA worktree", pane)
	}

	// A restart that sees the merge again runs the Effect again.
	h.s.call(func() {
		h.s.enqueue("ENG-1", func(ctx context.Context) { h.s.exec(ctx, "ENG-1", lifecycle.StartQA{}) })
	})
	time.Sleep(100 * time.Millisecond) // five ticks
	if got := slices.DeleteFunc(h.events("QA-ENG-1"), func(e string) bool { return e != "add" }); len(got) != 1 {
		t.Fatalf("QA-ENG-1 added %d times", len(got))
	}
	if n := len(h.rex.open("claude verifying")); n != 1 {
		t.Fatalf("%d QA tabs, want 1", n)
	}
	// The PR merged, so no branch poll reads it for the QA Task.
	h.gh.mu.Lock()
	defer h.gh.mu.Unlock()
	if i := slices.IndexFunc(h.gh.polled, func(k PRKey) bool { return k.Branch == "" }); i >= 0 {
		t.Fatalf("the branch poll read %+v", h.gh.polled[i])
	}
}

// A restart keeps a QA Task in the qa session: only a build Task's session
// takes the Repo key (PROD-QA-002).
func TestARestartKeepsTheQASession(t *testing.T) {
	seed := func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: "QA-ENG-1", Kind: lifecycle.KindQA, Stage: lifecycle.QAWaiting, Policy: testPolicy,
			NextCheck: time.Now().Add(time.Hour), Deadline: time.Now().Add(2 * time.Hour)},
			Title: "Add billing model", Repo: "/repos/acme/data", Session: qaSession, SessionID: "session:qa"}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed)
	var session string
	h.s.call(func() { session = h.s.recs["QA-ENG-1"].Session })
	if session != qaSession {
		t.Fatalf("session = %q, want %q", session, qaSession)
	}
}

// A QA Task takes no WIP slot, so a full cap does not hold it (PROD-QA-007).
func TestAFullWIPCapDoesNotStopQA(t *testing.T) {
	h, _ := newQA(t, qaConfig{}, func(d *Deps) { d.DefaultCap = 1 })
	h.working()
	h.do(Request{Op: "add", Repo: "data", Text: "Fix the docs", OverCap: true})
	h.merge(buildKey, 42)
	h.waitTab("claude verifying")
	h.waitStage("QA-ENG-1", lifecycle.Verifying)
	if v := h.do(Request{Op: "ls"}).(BoardView); v.Started != 1 || v.Cap != 1 {
		t.Fatalf("started %d of %d, want the cap full with AH-1 alone", v.Started, v.Cap)
	}
}

// A wait holds the Task in QA waiting until its next check. Then lineup
// types the check into the same Worker (PROD-QA-005, PROD-QA-006).
func TestAWaitTypesTheNextCheckIntoTheSameWorker(t *testing.T) {
	h, c := newQA(t, qaConfig{})
	qa := h.checking()
	if err := h.qa("wait", "2h", "the DAG has not run"); err != nil {
		t.Fatal(err)
	}
	h.status(qa.BlockID, "idle")
	next := c.now().Add(2 * time.Hour)
	v := h.task("QA-ENG-1")
	if v.Stage != "QA waiting" || !v.NextCheck.Equal(next) || v.Note != "next check "+next.Local().Format("Jan 2 15:04")+": the DAG has not run" {
		t.Fatalf("waiting task = %+v", v)
	}
	c.add(time.Hour)
	h.still("QA-ENG-1", lifecycle.QAWaiting)
	if typed := h.rex.typedInto(qa.BlockID); len(typed) != 0 {
		t.Fatalf("typed %q before the check was due", typed)
	}
	c.add(time.Hour)
	h.waitTyped(qa.BlockID, "prompt verifying")
	if v := h.task("QA-ENG-1"); v.Stage != "Verifying" || !v.NextCheck.IsZero() {
		t.Fatalf("checking task = %+v", v)
	}
	specs := h.qaSpecs()
	if last := specs[len(specs)-1]; !last.Recheck {
		t.Fatalf("the later check's spec = %+v, want a recheck", last)
	}
	if n := len(h.rex.open("claude verifying")); n != 1 {
		t.Fatalf("%d QA tabs, want the first Worker alone", n)
	}
}

// A pass tells the Tracker, then closes the tab once the Worker stops
// working, and removes the QA worktree and the empty qa session
// (PROD-QA-009).
func TestAPassWritesQAPassedAndClosesTheTabOnceTheWorkerIsIdle(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	qa := h.checking()
	// The QA end closes the tab itself.
	h.rex.mu.Lock()
	h.rex.asked = true
	h.rex.mu.Unlock()
	h.status(qa.BlockID, "working")
	h.waitWorkerState("QA-ENG-1", "working")
	if err := h.qa("pass", "", "the rows load"); err != nil {
		t.Fatal(err)
	}
	h.waitStage("QA-ENG-1", lifecycle.Done)
	eventually(t, "qa_passed", func() bool { return slices.Equal(h.statuses(), []string{"started", "merged", "qa_passed"}) })
	time.Sleep(100 * time.Millisecond) // five ticks
	if n := len(h.rex.open("claude verifying")); n != 1 {
		t.Fatal("the tab closed while the Worker worked")
	}
	h.status(qa.BlockID, "idle")
	eventually(t, "the QA tab closed", func() bool { return len(h.rex.open("claude verifying")) == 0 })
	eventually(t, "the QA worktree removed", func() bool { return h.task("QA-ENG-1").Worktree == "" })
	if v := h.task("QA-ENG-1"); v.Outcome != "passed" || v.Note != "the rows load" || v.Verdict != "the rows load" {
		t.Fatalf("passed task = %+v", v)
	}
	eventually(t, "the qa session removed", func() bool { return slices.Contains(h.sessionsDestroyed(), "session:qa") })
	if err := h.openFails("QA-ENG-1"); !strings.Contains(err.Error(), "is Done and its worktree is gone") {
		t.Fatalf("open of a passed QA Task: %v", err)
	}
}

// A fail waits for the user in Needs you, with the findings as its note
// (PROD-QA-010).
func TestAFailGoesToQAFailed(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	h.checking()
	if err := h.qa("fail", "", "the column is empty"); err != nil {
		t.Fatal(err)
	}
	h.waitStage("QA-ENG-1", lifecycle.QAFailed)
	if v := h.task("QA-ENG-1"); v.Group != "Needs you" || v.Note != "the column is empty" || !v.Tab {
		t.Fatalf("failed task = %+v", v)
	}
	if slices.Contains(h.statuses(), "qa_passed") {
		t.Fatal("a fail wrote qa_passed")
	}
}

// The QA slots hold a due check until a slot frees (PROD-QA-007).
func TestFullQASlotsHoldADueCheck(t *testing.T) {
	h, _ := newQA(t, qaConfig{}, func(d *Deps) { d.QASlots = 1 })
	h.checking()
	h.do(Request{Op: "add", Repo: "data", Text: "Fix the docs"})
	h.rex.exits(h.waitTab("wt lineup/ah-1-fix-the-docs"), 0)
	eventually(t, "AH-1 live", func() bool { return h.task("AH-1").Live })
	h.merge(PRKey{Repo: "acme/data", Branch: "lineup/ah-1-fix-the-docs"}, 43)
	h.waitStage("AH-1", lifecycle.Done)
	h.still("QA-AH-1", lifecycle.QAWaiting)
	if err := h.qa("wait", "1h", "the DAG has not run"); err != nil {
		t.Fatal(err)
	}
	h.waitStage("QA-AH-1", lifecycle.Verifying)
	eventually(t, "a QA tab for each QA Task", func() bool { return len(h.rex.open("claude verifying")) == 2 })
	var adhoc WorkerSpec
	for _, w := range h.qaSpecs() {
		if w.Task == "QA-AH-1" {
			adhoc = w
		}
	}
	if adhoc.Issue != nil || adhoc.Text != "Fix the docs" {
		t.Fatalf("ad hoc QA spec = %+v, want the request in place of an Issue", adhoc)
	}
}

// A QA Task in QA waiting at its deadline goes to QA failed. A wait past the
// deadline ends at it (PROD-QA-008).
func TestTheDeadlineEndsTheWait(t *testing.T) {
	h, c := newQA(t, qaConfig{maxWait: 90 * time.Minute})
	h.checking()
	if err := h.qa("wait", "5h", "the DAG has not run"); err != nil {
		t.Fatal(err)
	}
	if v := h.task("QA-ENG-1"); !v.NextCheck.Equal(mergedAt.Add(90 * time.Minute)) {
		t.Fatalf("next check %v, want the deadline", v.NextCheck)
	}
	c.add(time.Hour)
	h.waitStage("QA-ENG-1", lifecycle.QAFailed)
	if v := h.task("QA-ENG-1"); v.Note != "no verdict after 1h30m" {
		t.Fatalf("expired task = %+v", v)
	}
}

// A done with no verdict means that the contract result is missing
// (PROD-QA-005, ARCH-QA-004).
func TestADoneWithNoVerdictStopsTheQATask(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	qa := h.checking()
	h.status(qa.BlockID, "done")
	eventually(t, "stopped", func() bool { return h.task("QA-ENG-1").Flag == "stopped" })
}

// A QA Worker whose tab is gone when its check comes due gets the tab closed
// flag. open resumes its conversation, and the check goes to it
// (PROD-QA-006, PROD-QA-012).
func TestALostQATabFlagsTheDueCheck(t *testing.T) {
	h, c := newQA(t, qaConfig{})
	qa := h.checking()
	if err := h.qa("wait", "1h", "the DAG has not run"); err != nil {
		t.Fatal(err)
	}
	h.rex.userCloses(qa)
	eventually(t, "the tab gone", func() bool { return !h.task("QA-ENG-1").Tab })
	if v := h.task("QA-ENG-1"); v.Flag != "" {
		t.Fatalf("a waiting Task got the flag %q", v.Flag)
	}
	c.add(time.Hour)
	eventually(t, "the tab closed flag", func() bool { return h.task("QA-ENG-1").Flag == "tab closed" })
	if res := h.open("QA-ENG-1"); res.Action != OpenRestored {
		t.Fatalf("open = %+v", res)
	}
	back := h.waitTab("claude --resume " + h.qaSpecs()[0].Conversation)
	h.status(back.BlockID, "idle")
	h.waitTyped(back.BlockID, "prompt verifying")
}

// failed takes the QA Task to QA failed, with its Worker idle.
func (h *harness) failed() tab {
	h.t.Helper()
	qa := h.checking()
	if err := h.qa("fail", "", "the column is empty"); err != nil {
		h.t.Fatal(err)
	}
	h.status(qa.BlockID, "idle")
	h.waitStage("QA-ENG-1", lifecycle.QAFailed)
	h.waitWorkerState("QA-ENG-1", "idle")
	return qa
}

// Accept the result ends the QA Task and tells the Tracker (PROD-QA-010).
func TestQAAcceptPassesAFailedCheck(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	h.failed()
	h.rex.mu.Lock()
	h.rex.asked = true
	h.rex.mu.Unlock()
	h.do(Request{Op: "qa-accept", Task: "QA-ENG-1"})
	h.waitStage("QA-ENG-1", lifecycle.Done)
	eventually(t, "qa_passed", func() bool { return slices.Contains(h.statuses(), "qa_passed") })
	eventually(t, "the QA tab closed", func() bool { return len(h.rex.open("claude verifying")) == 0 })
	if v := h.task("QA-ENG-1"); v.Outcome != "accepted" || v.Note != "accepted" {
		t.Fatalf("accepted task = %+v", v)
	}
}

// Check again checks at once, whatever the slots say, with a new deadline
// of max_wait from now (PROD-QA-010).
func TestQARecheckChecksAtOnceWithANewDeadline(t *testing.T) {
	h, c := newQA(t, qaConfig{maxWait: 6 * time.Hour}, func(d *Deps) { d.QASlots = 1 })
	qa := h.failed()
	c.add(time.Hour)
	h.do(Request{Op: "qa-recheck", Task: "QA-ENG-1"})
	h.waitTyped(qa.BlockID, "prompt verifying")
	if v := h.task("QA-ENG-1"); v.Stage != "Verifying" || !v.Deadline.Equal(c.now().Add(6*time.Hour)) {
		t.Fatalf("rechecked task = %+v, want Verifying with a deadline 6h from now", v)
	}
	if _, err := h.s.Handle(context.Background(), Request{Op: "qa-recheck", Task: "QA-ENG-1"}); err == nil {
		t.Fatal("a recheck in Verifying was allowed")
	}
}

// Start a fix Task files a fix Issue beside the build Task's Issue and ends
// the QA Task (PROD-QA-011).
func TestQAFixFilesTheFixIssue(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	h.failed()
	h.rex.mu.Lock()
	h.rex.asked = true
	h.rex.mu.Unlock()
	if id := h.do(Request{Op: "qa-fix", Task: "QA-ENG-1"}); id != "ENG-99" {
		t.Fatalf("qa-fix gave %v, want ENG-99", id)
	}
	h.waitStage("QA-ENG-1", lifecycle.Done)
	if v := h.task("QA-ENG-1"); v.Outcome != "fix filed" || v.Note != "fix filed: ENG-99" {
		t.Fatalf("fixed task = %+v", v)
	}
	h.tr.mu.Lock()
	fixes := slices.Clone(h.tr.fixes)
	h.tr.mu.Unlock()
	if len(fixes) != 1 {
		t.Fatalf("%d fix Issues, want 1", len(fixes))
	}
	f := fixes[0]
	if f.build == nil || f.build.Identifier != "ENG-1" || f.repo != "acme/data" || f.title != "Fix QA of ENG-1: Add billing model" {
		t.Fatalf("fix = %+v", f)
	}
	for _, want := range []string{"the column is empty", "[#42](https://github.com/acme/data/pull/42)",
		"[93d24ce](https://github.com/acme/data/commit/" + mergeSHA + ")"} {
		if !strings.Contains(f.about, want) {
			t.Errorf("description lacks %q:\n%s", want, f.about)
		}
	}
	if slices.Contains(h.statuses(), "qa_passed") {
		t.Fatal("a fix wrote qa_passed")
	}
}

// A second Start a fix Task while the first files its Issue files no second
// Issue (PROD-QA-011).
func TestASecondQAFixDuringTheFirstIsRefused(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	h.failed()
	h.rex.mu.Lock()
	h.rex.asked = true
	h.rex.mu.Unlock()
	gate := make(chan struct{})
	h.tr.mu.Lock()
	h.tr.gate = gate
	h.tr.mu.Unlock()
	fix := func() chan error {
		out := make(chan error, 1)
		go func() {
			_, err := h.s.Handle(context.Background(), Request{Op: "qa-fix", Task: "QA-ENG-1"})
			out <- err
		}()
		return out
	}
	first := fix()
	eventually(t, "the first fix Issue", func() bool {
		h.tr.mu.Lock()
		defer h.tr.mu.Unlock()
		return len(h.tr.fixes) == 1
	})
	select {
	case err := <-fix():
		if err == nil {
			t.Fatal("a second qa-fix during the first was accepted")
		}
	case <-time.After(time.Second):
		// The second call waits on the Tracker too, and files a second Issue.
	}
	close(gate)
	err := <-first
	h.tr.mu.Lock()
	n := len(h.tr.fixes)
	h.tr.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d fix Issues, want 1", n)
	}
	if err != nil {
		t.Fatal(err)
	}
	h.waitStage("QA-ENG-1", lifecycle.Done)
}

// rerun verifying checks at once, whatever the next check says, and close
// ends the QA Task with no Tracker write (PROD-QA-012).
func TestRerunAndCloseWorkOnAQATask(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	qa := h.checking()
	if err := h.qa("wait", "6h", "the DAG has not run"); err != nil {
		t.Fatal(err)
	}
	h.status(qa.BlockID, "idle")
	h.waitWorkerState("QA-ENG-1", "idle")
	h.do(Request{Op: "rerun", Task: "QA-ENG-1", Stage: "verifying"})
	h.waitTyped(qa.BlockID, "prompt verifying")
	h.do(Request{Op: "close", Task: "QA-ENG-1"})
	h.waitStage("QA-ENG-1", lifecycle.Done)
	eventually(t, "the QA worktree removed", func() bool { return h.task("QA-ENG-1").Worktree == "" })
	if v := h.task("QA-ENG-1"); v.Outcome != "closed" || v.Tab || v.Note != "closed" {
		t.Fatalf("closed task = %+v", v)
	}
	if slices.Contains(h.statuses(), "qa_passed") {
		t.Fatal("a close wrote qa_passed")
	}
}

// lineup qa refuses a verdict that it cannot apply.
func TestTheQAOpRefusesABadVerdict(t *testing.T) {
	h, _ := newQA(t, qaConfig{})
	h.checking()
	for _, c := range []struct{ key, when, text, want string }{
		{"wait", "soon", "x", `"soon" is not a duration`},
		{"wait", "-1h", "x", "is not in the future"},
		{"wait", "2h", " ", "qa wait needs a reason"},
		{"pass", "", "", "qa pass needs a summary"},
		{"fail", "", "", "qa fail needs the findings"},
		{"skip", "", "x", `qa takes wait, pass, or fail, not "skip"`},
	} {
		if err := h.qa(c.key, c.when, c.text); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("qa %s %q %q: err %v, want %q", c.key, c.when, c.text, err, c.want)
		}
	}
	_, err := h.s.Handle(context.Background(), Request{Op: "qa", Task: "ENG-1", Key: "pass", Text: "x"})
	var na *lifecycle.NotAllowedError
	if !errors.As(err, &na) {
		t.Fatalf("a verdict on a build Task: err %v", err)
	}
	if _, err := h.s.Handle(context.Background(), Request{Op: "qa-fix", Task: "QA-ENG-1"}); !errors.As(err, &na) {
		t.Fatalf("qa-fix in Verifying: err %v", err)
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"2h":                   now.Add(2 * time.Hour),
		"90m":                  now.Add(90 * time.Minute),
		"16:30":                time.Date(2026, 11, 24, 16, 30, 0, 0, time.Local),
		"03:00":                time.Date(2026, 11, 25, 3, 0, 0, 0, time.Local),
		"15:00":                time.Date(2026, 11, 25, 15, 0, 0, 0, time.Local),
		"2026-11-26T03:00:00Z": time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC),
	} {
		if got, err := ParseWhen(in, now); err != nil || !got.Equal(want) {
			t.Errorf("ParseWhen(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"0s", "-2h", "2026-11-01T03:00:00Z", "tomorrow", "25:00", ""} {
		if got, err := ParseWhen(in, now); err == nil {
			t.Errorf("ParseWhen(%q) = %v, want an error", in, got)
		}
	}
}

// A QA tab drops the owner of a GitHub ID and keeps QA- (PROD-QA-002).
func TestAQATabLabelDropsTheGitHubOwner(t *testing.T) {
	r := Record{Task: lifecycle.Task{ID: "QA-GClunies/lineup#123", Kind: lifecycle.KindQA}, Title: "Add billing model"}
	if got := r.tabLabel(); got != "QA-lineup#123 Add billing model" {
		t.Fatalf("label = %q", got)
	}
}

// ls --json gives a QA Task its next_check and its deadline, and a build Task
// neither (PROD-QA-012).
func TestOnlyAQATaskHasANextCheckAndADeadline(t *testing.T) {
	at := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	qa := Record{Task: lifecycle.Task{ID: "QA-ENG-1", Kind: lifecycle.KindQA, Stage: lifecycle.QAWaiting, NextCheck: at, Deadline: at.Add(time.Hour), Verdict: "the DAG has not run"}}
	build := Record{Task: lifecycle.Task{ID: "ENG-1", Stage: lifecycle.Working}}
	for r, want := range map[*Record]bool{&qa: true, &build: false} {
		data, _ := json.Marshal(r.view())
		for _, key := range []string{`"next_check"`, `"deadline"`, `"verdict"`} {
			if strings.Contains(string(data), key) != want {
				t.Errorf("%s: has %s = %v, want %v: %s", r.ID, key, !want, want, data)
			}
		}
	}
}
