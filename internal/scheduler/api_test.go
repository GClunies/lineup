package scheduler

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// working starts ENG-1 and returns its Worker's tab.
func (h *harness) working() tab {
	h.t.Helper()
	h.do(Request{Op: "add", Issue: "ENG-1"})
	h.do(Request{Op: "start", Task: "ENG-1"})
	h.rex.events <- RexEvent{Kind: EvExited, BlockID: h.waitTab("wt ada/eng-1-billing").BlockID}
	w := h.waitTab("claude working")
	eventually(h.t, "a live Worker", func() bool { return h.task("ENG-1").Live })
	return w
}

func (h *harness) waitWorkerState(id, state string) {
	h.t.Helper()
	eventually(h.t, id+" "+state, func() bool {
		var got string
		h.s.call(func() { got = h.s.recs[id].WorkerState })
		return got == state
	})
}

func (h *harness) ask(id, question string) error {
	_, err := h.s.Handle(context.Background(), Request{Op: "ask", Task: id, Text: question})
	return err
}

func TestAskSetsQuestionFlagAndDetail(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.working()
	if err := h.ask("ENG-1", "Migrate with a dual write first?"); err != nil {
		t.Fatal(err)
	}
	v := h.task("ENG-1")
	if v.Stage != "Working" || v.Flag != "question" || v.Note != "question: Migrate with a dual write first?" {
		t.Fatalf("task after ask = %+v", v)
	}

	long := strings.Repeat("é", 1000)
	if err := h.ask("ENG-1", long); err != nil {
		t.Fatal(err)
	}
	if d := h.task("ENG-1").FlagDetail; d != strings.Repeat("é", 300) {
		t.Fatalf("detail of a 1000-character question has %d characters, want 300", len([]rune(d)))
	}
}

func TestAskReplacesTheOldQuestion(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.working()
	h.ask("ENG-1", "first?")
	if err := h.ask("ENG-1", "second?"); err != nil {
		t.Fatal(err)
	}
	d, err := h.s.show("ENG-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Note != "question: second?" {
		t.Fatalf("note = %q", d.Note)
	}
	if last := d.Events[len(d.Events)-1]; last.Detail != "question: second?" {
		t.Fatalf("last stored event = %+v, want the second question", last)
	}
}

func TestAskRefusedWhenContractMet(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.set(PRKey{Repo: "acme/data", Branch: "ada/eng-1-billing"}, 42)
	h.working()
	eventually(t, "the PR seen", func() bool { return h.task("ENG-1").PRNumber == 42 })
	if err := h.ask("ENG-1", "ok?"); err == nil || err.Error() != "ENG-1: contract output exists" {
		t.Fatalf("ask with an open PR = %v", err)
	}
	if v := h.task("ENG-1"); v.Flag != "" {
		t.Fatalf("a refused ask flagged the task: %+v", v)
	}
}

func TestAskInAReviewIsRefusedOnlyAfterTheFindings(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "abc"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	eventually(t, "a live Worker", func() bool { return h.task("pr-7").Live })
	if err := h.ask("pr-7", "post the nit?"); err != nil {
		t.Fatalf("ask before the findings: %v", err)
	}
	h.writeFindings(7, "abc")
	if err := h.ask("pr-7", "post the nit?"); err == nil || err.Error() != "pr-7: contract output exists" {
		t.Fatalf("ask after the findings = %v", err)
	}
}

func TestAskRefusedWithoutWorker(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.do(Request{Op: "add", Issue: "ENG-1"})
	if err := h.ask("ENG-1", "ok?"); err == nil || err.Error() != "ENG-1 has no live Worker" {
		t.Fatalf("ask on a Queued task = %v", err)
	}
}

// A Worker asks in the middle of its turn, reports more steps, and stops. The
// question stays until the user's answer starts the Worker again.
func TestAQuestionClearsWhenTheWorkerWorksAgain(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	worker := h.working()
	h.status(worker.BlockID, "working")
	h.waitWorkerState("ENG-1", "working")
	h.ask("ENG-1", "ok?")
	h.rex.events <- RexEvent{Kind: EvStatus, BlockID: worker.BlockID, Status: Status{State: "working", Msg: "Asking the user"}}
	h.status(worker.BlockID, "done")
	h.waitWorkerState("ENG-1", "done")
	h.still("ENG-1", lifecycle.Working)
	if v := h.task("ENG-1"); v.Note != "question: ok?" {
		t.Fatalf("note after the Worker stopped = %q, want the question", v.Note)
	}

	h.status(worker.BlockID, "working")
	eventually(t, "the question cleared", func() bool {
		v := h.task("ENG-1")
		return v.Flag == "" && v.FlagDetail == "" && v.Note == ""
	})
	h.still("ENG-1", lifecycle.Working)
}

func TestSteerIsRecordedAsAnEvent(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.working()
	h.do(Request{Op: "steer", Task: "ENG-1", Text: "use a dual write"})
	d, err := h.s.show("ENG-1")
	if err != nil {
		t.Fatal(err)
	}
	last := d.Events[len(d.Events)-1]
	if last.Signal != "steer" || last.Detail != "use a dual write" || last.From != "Working" || last.To != "Working" {
		t.Fatalf("last event = %+v, want a steer in Working", last)
	}
}
