package lifecycle

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var policy = Policy{Budgets: Budgets{CIDebug: 3, Responding: 3, Restarts: 2}}

func taskIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t := b.Add("T", policy)
	t.Stage = stage
	return b, t
}

func TestTransitions(t *testing.T) {
	cases := []struct {
		name    string
		from    Stage
		signal  Signal
		to      Stage
		effects []Effect
		outcome Outcome
	}{
		{"start", Queued, Start{}, Working, []Effect{TrackerStatus{"started"}, StartWorker{Working}}, ""},
		{"working done with draft PR", Working, WorkerDone{ContractMet: true}, CIRunning, nil, ""},
		{"ci failed", CIRunning, CIFailed{}, CIDebug, []Effect{StartWorker{CIDebug}}, ""},
		{"ci passed needs review", CIRunning, CIPassed{NeedsReview: true}, InReview, []Effect{TrackerStatus{"in_review"}}, ""},
		{"ci passed approved", CIRunning, CIPassed{}, ReadyToLand, nil, ""},
		{"ci debug pushed", CIDebug, Pushed{}, CIRunning, nil, ""},
		{"review approved", InReview, ReviewApproved{}, ReadyToLand, nil, ""},
		{"review commented", InReview, ReviewCommented{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"review changes requested", InReview, ReviewChangesRequested{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"review additional authors", InReview, ReviewAdditionalAuthors{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"in review conflict", InReview, PRConflict{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"responding pushed", Responding, Pushed{}, CIRunning, nil, ""},
		{"user merge", ReadyToLand, PRMerge{}, ReadyToLand, []Effect{Merge{}}, ""},
		{"ready to land commented", ReadyToLand, ReviewCommented{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"ready to land changes requested", ReadyToLand, ReviewChangesRequested{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"ready to land conflict", ReadyToLand, PRConflict{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"merged by another person", Working, PRMerged{}, Done, []Effect{Cleanup{}, TrackerStatus{"merged"}}, OutcomeMerged},
		{"merged from ready to land", ReadyToLand, PRMerged{}, Done, []Effect{Cleanup{}, TrackerStatus{"merged"}}, OutcomeMerged},
		{"closed", InReview, TaskClosed{}, Done, []Effect{Cleanup{}, TrackerStatus{"closed"}}, OutcomeClosed},
		{"closed while queued", Queued, TaskClosed{}, Done, []Effect{Cleanup{}, TrackerStatus{"closed"}}, OutcomeClosed},
		{"rerun ci debug", CIRunning, Rerun{CIDebug}, CIDebug, []Effect{StartWorker{CIDebug}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := taskIn(c.from)
			effects, err := b.Apply("T", c.signal)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if task.Stage != c.to {
				t.Errorf("stage = %s, want %s", task.Stage, c.to)
			}
			if !reflect.DeepEqual(effects, c.effects) {
				t.Errorf("effects = %#v, want %#v", effects, c.effects)
			}
			if task.Outcome != c.outcome {
				t.Errorf("outcome = %q, want %q", task.Outcome, c.outcome)
			}
		})
	}
}

func TestATaskSavesItsStageByKey(t *testing.T) {
	_, task := taskIn(CIDebug)
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"stage":"ci_debug"`) {
		t.Fatalf("saved %s, want the stage key ci_debug", raw)
	}
	var back Task
	if err := json.Unmarshal(raw, &back); err != nil || back.Stage != CIDebug {
		t.Fatalf("loaded stage %s, err %v, want CI debug", back.Stage, err)
	}
	if err := json.Unmarshal([]byte(`{"stage":"plan_review"}`), &back); err == nil {
		t.Fatal("loaded an unknown stage key without an error")
	}
}

func TestWIPCap(t *testing.T) {
	b := NewBoard(1)
	b.Add("A", policy)
	second := b.Add("B", policy)
	if _, err := b.Apply("A", Start{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Apply("B", Start{}); !errors.Is(err, ErrWIPCapFull) {
		t.Fatalf("start over cap: err = %v, want ErrWIPCapFull", err)
	}
	if second.Stage != Queued {
		t.Fatalf("refused start moved the Task to %s", second.Stage)
	}
	if _, err := b.Apply("B", Start{OverCap: true}); err != nil {
		t.Fatal(err)
	}
	if !second.OverCap || b.OverCap() != 1 {
		t.Fatalf("over-cap start not recorded: task %v, board %d", second.OverCap, b.OverCap())
	}
}

func TestStartUnderCapIsNotRecordedAsOverCap(t *testing.T) {
	b := NewBoard(2)
	task := b.Add("A", policy)
	if _, err := b.Apply("A", Start{OverCap: true}); err != nil {
		t.Fatal(err)
	}
	if task.OverCap {
		t.Fatal("a start with room under the cap was recorded as over the cap")
	}
}

func TestUserSignalsOutsideTheirStageFail(t *testing.T) {
	cases := []struct {
		stage  Stage
		signal Signal
	}{
		{InReview, PRMerge{}},
		{Working, Start{}},
		{Done, TaskClosed{}},
		{Queued, Rerun{Working}},
		{Working, Rerun{InReview}},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		_, err := b.Apply("T", c.signal)
		var na *NotAllowedError
		if !errors.As(err, &na) {
			t.Errorf("%s in %s: err = %v, want NotAllowedError", c.signal.Name(), c.stage, err)
		}
		if task.Stage != c.stage {
			t.Errorf("%s in %s moved the Task to %s", c.signal.Name(), c.stage, task.Stage)
		}
	}
}

func TestObservedSignalsOutsideTheirStageChangeNothing(t *testing.T) {
	cases := []struct {
		stage  Stage
		signal Signal
	}{
		{Queued, CIFailed{}},
		{Working, CIPassed{}},
		{Working, Pushed{}},
		{InReview, CIFailed{}},
		{CIRunning, ReviewApproved{}},
		{ReadyToLand, ReviewAdditionalAuthors{}},
		{Done, PRMerged{}},
		{ReadyToLand, WorkerDone{ContractMet: true}},
		{CIRunning, WorkerLost{}},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		effects, err := b.Apply("T", c.signal)
		if err != nil || effects != nil || task.Stage != c.stage || task.Flag != "" {
			t.Errorf("%s in %s: stage %s, flag %q, effects %v, err %v", c.signal.Name(), c.stage, task.Stage, task.Flag, effects, err)
		}
	}
}

func TestNeedsYouReasons(t *testing.T) {
	cases := []struct {
		name   string
		stage  Stage
		signal Signal
		reason Reason
	}{
		{"permission", Working, WorkerStatus{State: "blocked", Kind: "permission", Detail: "Bash(make deploy)"}, ReasonPermission},
		{"question", Working, WorkerStatus{State: "blocked", Kind: "question"}, ReasonQuestion},
		{"auth", Responding, WorkerStatus{State: "blocked", Kind: "auth"}, ReasonAuth},
		{"unknown kind", Working, WorkerStatus{State: "blocked", Kind: "telepathy"}, ReasonQuestion},
		{"status error", CIDebug, WorkerStatus{State: "error"}, ReasonError},
		{"exit code", Working, WorkerExited{Code: 1}, ReasonError},
		{"setup failed", Working, SetupFailed{"pre-start hook"}, ReasonSetupFailed},
		{"stopped", Working, WorkerDone{ContractMet: false}, ReasonStopped},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		if _, err := b.Apply("T", c.signal); err != nil {
			t.Fatal(err)
		}
		if task.Flag != c.reason || task.Group() != GroupNeedsYou {
			t.Errorf("%s: flag %q group %s, want %q in Needs you", c.name, task.Flag, task.Group(), c.reason)
		}
		if task.Stage != c.stage {
			t.Errorf("%s: stage moved to %s", c.name, task.Stage)
		}
	}
}

func TestWorkingStatusClearsOnlyWorkerFlags(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerStatus{State: "blocked", Kind: "permission"})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" {
		t.Fatalf("working did not clear %q", task.Flag)
	}
	b.Apply("T", WorkerDone{ContractMet: false})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" {
		t.Fatalf("the user's reply did not clear %q", task.Flag)
	}
	b.Apply("T", SetupFailed{"pre-start hook"})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != ReasonSetupFailed {
		t.Fatalf("working cleared %q, want setup failed kept", task.Flag)
	}
}

func TestExitZeroIsNotAnError(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerExited{Code: 0})
	if task.Flag != "" {
		t.Fatalf("exit 0 set flag %q", task.Flag)
	}
}

func TestPROpenAfterUnmetContractMovesOn(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", PROpen{})
	if task.Stage != Working {
		t.Fatalf("pr-open before done moved the Task to %s", task.Stage)
	}
	b.Apply("T", WorkerDone{ContractMet: false})
	b.Apply("T", PROpen{})
	if task.Stage != CIRunning || task.Flag != "" {
		t.Fatalf("stage %s flag %q, want CI running with no flag", task.Stage, task.Flag)
	}
}

// lineup never marks a PR ready, so a passing draft waits for the user to.
func TestADraftWaitsForTheUserToMarkItReady(t *testing.T) {
	b, task := taskIn(CIRunning)
	effects, _ := b.Apply("T", CIPassed{Draft: true, NeedsReview: true})
	if effects != nil || task.Stage != CIRunning || task.Flag != ReasonDraft || task.Group() != GroupNeedsYou {
		t.Fatalf("draft: stage %s flag %q effects %v, want CI running flagged draft and no effect", task.Stage, task.Flag, effects)
	}
	b.Apply("T", CIPassed{NeedsReview: true})
	if task.Stage != InReview || task.Flag != "" {
		t.Fatalf("marked ready: stage %s flag %q, want In review with no flag", task.Stage, task.Flag)
	}
}

func TestCIDebugBudget(t *testing.T) {
	b, task := taskIn(CIRunning)
	for i := range 3 {
		effects, _ := b.Apply("T", CIFailed{})
		if task.Stage != CIDebug || len(effects) != 1 {
			t.Fatalf("attempt %d: stage %s effects %v", i+1, task.Stage, effects)
		}
		b.Apply("T", Pushed{})
	}
	effects, _ := b.Apply("T", CIFailed{})
	if effects != nil || task.Flag != ReasonBudgetSpent || task.Stage != CIDebug {
		t.Fatalf("fourth failure: stage %s flag %q effects %v", task.Stage, task.Flag, effects)
	}
}

func TestRespondingBudgetIsPerReview(t *testing.T) {
	b, task := taskIn(InReview)
	for range 3 {
		b.Apply("T", ReviewCommented{})
		b.Apply("T", Pushed{})
		b.Apply("T", CIPassed{NeedsReview: true})
	}
	b.Apply("T", ReviewCommented{})
	if task.Flag != ReasonBudgetSpent {
		t.Fatalf("fourth round: flag %q, want budget spent", task.Flag)
	}

	b, task = taskIn(InReview)
	for range 3 {
		b.Apply("T", ReviewCommented{})
		b.Apply("T", Pushed{})
		b.Apply("T", CIPassed{NeedsReview: true})
	}
	b.Apply("T", ReviewApproved{})
	b.Apply("T", ReviewCommented{})
	if task.Stage != Responding || task.Flag != "" {
		t.Fatalf("a new review after approval: stage %s flag %q, want Responding with budget", task.Stage, task.Flag)
	}
}

func TestWorkerRestartBudgetIsPerStage(t *testing.T) {
	b, task := taskIn(Working)
	task.Left.Restarts = 2
	for i := range 2 {
		effects, _ := b.Apply("T", WorkerLost{})
		if !reflect.DeepEqual(effects, []Effect{StartWorker{Working}}) {
			t.Fatalf("restart %d: effects %v", i+1, effects)
		}
	}
	effects, _ := b.Apply("T", WorkerLost{})
	if effects != nil || task.Flag != ReasonWorkerLost {
		t.Fatalf("third loss: flag %q effects %v", task.Flag, effects)
	}
	b.Apply("T", PROpen{})
	b.Apply("T", Rerun{Working})
	if effects, _ := b.Apply("T", WorkerLost{}); len(effects) != 1 {
		t.Fatalf("a new Stage did not refill the restart budget: %v", effects)
	}
}
