package lifecycle

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

var (
	qaNow      = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	qaDeadline = qaNow.Add(48 * time.Hour)
)

func qaIn(stage Stage) (*Board, *Task) {
	b := NewBoard(1)
	t := b.AddQA("QA-T", policy, qaNow, qaDeadline)
	t.Stage = stage
	return b, t
}

func TestAQATaskStartsInQAWaitingDueNow(t *testing.T) {
	b, task := qaIn(QAWaiting)
	if task.Kind != KindQA || task.NextCheck != qaNow || task.Deadline != qaDeadline || b.Started() != 0 {
		t.Fatalf("task %+v, %d started", task, b.Started())
	}
}

// PROD-QA-005, PROD-QA-008, PROD-QA-009, PROD-QA-010, PROD-QA-012.
func TestQATransitions(t *testing.T) {
	ended := []Effect{TrackerStatus{"qa_passed"}, Cleanup{WhenIdle: true}}
	cases := []struct {
		name    string
		from    Stage
		signal  Signal
		to      Stage
		effects []Effect
		outcome Outcome
		verdict string
	}{
		{"a due check starts the Worker", QAWaiting, QADue{}, Verifying, []Effect{PromptWorker{Verifying}}, "", ""},
		{"a due check in Verifying changes nothing", Verifying, QADue{}, Verifying, nil, "", ""},
		{"a wait goes back to QA waiting", Verifying, QAWait{Until: qaNow.Add(time.Hour), Reason: "the DAG has not run"}, QAWaiting, nil, "", "the DAG has not run"},
		{"a pass ends the Task and tells the Tracker", Verifying, QAPass{Summary: "1 row each"}, Done, ended, OutcomePassed, "1 row each"},
		{"a fail waits for the user", Verifying, QAFail{Findings: "3 duplicates"}, QAFailed, nil, "", "3 duplicates"},
		{"a steered Worker can pass in QA waiting", QAWaiting, QAPass{Summary: "ok"}, Done, ended, OutcomePassed, "ok"},
		{"a steered Worker can wait in QA failed", QAFailed, QAWait{Until: qaNow}, QAWaiting, nil, "", ""},
		{"the deadline fails the wait", QAWaiting, QAExpired{Detail: "no verdict after 48h"}, QAFailed, nil, "", "no verdict after 48h"},
		{"the deadline does not stop a check", Verifying, QAExpired{Detail: "no verdict after 48h"}, Verifying, nil, "", ""},
		{"accept ends the Task as a pass", QAFailed, QAAccept{}, Done, ended, OutcomeAccepted, ""},
		{"a fix Issue ends the Task", QAFailed, QAFixFiled{Issue: "ENG-13"}, Done, []Effect{Cleanup{WhenIdle: true}}, OutcomeFixFiled, "fix filed: ENG-13"},
		{"check again starts the Worker", QAFailed, QARecheck{Deadline: qaDeadline.Add(time.Hour)}, Verifying, []Effect{PromptWorker{Verifying}}, "", ""},
		{"a rerun starts a check at once", QAWaiting, Rerun{Verifying}, Verifying, []Effect{PromptWorker{Verifying}}, "", ""},
		{"a close writes nothing to the Tracker", QAWaiting, TaskClosed{}, Done, []Effect{Cleanup{}}, OutcomeClosed, ""},
		{"a merge of the PR does not end a QA Task", QAWaiting, PRMerged{}, QAWaiting, nil, "", ""},
		{"a closed PR does not flag a QA Task", QAWaiting, PRClosed{Number: 5}, QAWaiting, nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := qaIn(c.from)
			effects, err := b.Apply(task.ID, c.signal)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if task.Stage != c.to || task.Outcome != c.outcome || task.Verdict != c.verdict || task.Flag != "" {
				t.Errorf("stage %s, outcome %q, verdict %q, flag %q; want %s, %q, %q, no flag", task.Stage, task.Outcome, task.Verdict, task.Flag, c.to, c.outcome, c.verdict)
			}
			if !reflect.DeepEqual(effects, c.effects) {
				t.Errorf("effects = %#v, want %#v", effects, c.effects)
			}
		})
	}
}

func TestAWaitNeverPassesTheDeadline(t *testing.T) {
	b, task := qaIn(Verifying)
	b.Apply(task.ID, QAWait{Until: qaDeadline.Add(time.Hour)})
	if task.NextCheck != qaDeadline {
		t.Fatalf("next check %v, want the deadline %v", task.NextCheck, qaDeadline)
	}
}

func TestCheckAgainSetsTheNewDeadline(t *testing.T) {
	b, task := qaIn(QAFailed)
	later := qaDeadline.Add(24 * time.Hour)
	b.Apply(task.ID, QARecheck{Deadline: later})
	if task.Deadline != later {
		t.Fatalf("deadline %v, want %v", task.Deadline, later)
	}
}

// PROD-QA-005: a done with no verdict is a missing contract result.
func TestADoneInVerifyingWithNoVerdictStops(t *testing.T) {
	b, task := qaIn(Verifying)
	b.Apply(task.ID, WorkerDone{Detail: "I looked"})
	if task.Stage != Verifying || task.Flag != ReasonStopped {
		t.Fatalf("stage %s flag %q, want Verifying and stopped", task.Stage, task.Flag)
	}
}

// PROD-QA-006: a lost tab shows only when a check needs the Worker.
func TestALostTabInQAWaitingSetsNoFlag(t *testing.T) {
	b, task := qaIn(QAWaiting)
	b.Apply(task.ID, TabClosed{})
	if task.Flag != "" {
		t.Fatalf("flag %q, want none", task.Flag)
	}
	b.Apply(task.ID, QADue{})
	b.Apply(task.ID, TabClosed{Waits: true})
	if task.Stage != Verifying || task.Flag != ReasonTabClosed {
		t.Fatalf("stage %s flag %q, want Verifying and tab closed", task.Stage, task.Flag)
	}
}

func TestAFlaggedQATaskStartsNoCheck(t *testing.T) {
	b, task := qaIn(QAWaiting)
	b.Apply(task.ID, WorkerStatus{State: "blocked", Kind: "question", Detail: "which table?"})
	if effects, err := b.Apply(task.ID, QADue{}); err != nil || effects != nil || task.Stage != QAWaiting {
		t.Fatalf("effects %v err %v stage %s, want nothing", effects, err, task.Stage)
	}
}

// PROD-QA-007: full slots hold a due check, and a user's rerun passes them.
func TestQASlotsHoldADueCheck(t *testing.T) {
	b := NewBoard(1)
	b.QASlots = 1
	first := b.AddQA("QA-A", policy, qaNow, qaDeadline)
	second := b.AddQA("QA-B", policy, qaNow, qaDeadline)
	if _, err := b.Apply(first.ID, QADue{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Apply(second.ID, QADue{}); !errors.Is(err, ErrQASlotsFull) || second.Stage != QAWaiting {
		t.Fatalf("err %v stage %s, want ErrQASlotsFull and QA waiting", err, second.Stage)
	}
	if _, err := b.Apply(second.ID, Rerun{Verifying}); err != nil || second.Stage != Verifying {
		t.Fatalf("err %v stage %s, want a rerun past the slots", err, second.Stage)
	}
}

func TestNoQASlotsMeansNoCap(t *testing.T) {
	b := NewBoard(1)
	for _, id := range []string{"QA-A", "QA-B", "QA-C"} {
		b.AddQA(id, policy, qaNow, qaDeadline)
		if _, err := b.Apply(id, QADue{}); err != nil {
			t.Fatal(err)
		}
	}
	if b.QAChecking() != 3 {
		t.Fatalf("%d checking, want 3", b.QAChecking())
	}
}

func TestQAVerdictsAndChoicesRefuseOtherStages(t *testing.T) {
	cases := []struct {
		name   string
		task   func() (*Board, *Task)
		signal Signal
	}{
		{"a pass on a build Task", func() (*Board, *Task) { return taskIn(Working) }, QAPass{}},
		{"a wait on a Done QA Task", func() (*Board, *Task) { return qaIn(Done) }, QAWait{}},
		{"an accept in Verifying", func() (*Board, *Task) { return qaIn(Verifying) }, QAAccept{}},
		{"a fix in QA waiting", func() (*Board, *Task) { return qaIn(QAWaiting) }, QAFixFiled{Issue: "ENG-13"}},
		{"check again in Verifying", func() (*Board, *Task) { return qaIn(Verifying) }, QARecheck{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := c.task()
			var na *NotAllowedError
			if _, err := b.Apply(task.ID, c.signal); !errors.As(err, &na) {
				t.Fatalf("err %v, want NotAllowedError", err)
			}
		})
	}
}
