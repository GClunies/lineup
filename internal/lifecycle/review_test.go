package lifecycle

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func reviewIn(stage Stage, queue string) (*Board, *Task) {
	b := NewBoard(1)
	t := b.AddReview("R", queue, policy)
	t.Stage = stage
	return b, t
}

func TestReviewTransitions(t *testing.T) {
	ended := []Effect{Cleanup{}}
	cases := []struct {
		name    string
		from    Stage
		signal  Signal
		to      Stage
		flag    Reason
		effects []Effect
		outcome Outcome
	}{
		{"start", Queued, Start{}, Reviewing, "", []Effect{PromptWorker{Reviewing}}, ""},
		{"done with findings", Reviewing, WorkerDone{ContractMet: true}, ReviewReady, "", nil, ""},
		{"done without findings", Reviewing, WorkerDone{ContractMet: false}, Reviewing, ReasonStopped, nil, ""},
		{"reviewed while reviewing", Reviewing, PRReviewed{}, Done, "", ended, OutcomeReviewed},
		{"reviewed when ready", ReviewReady, PRReviewed{}, Done, "", ended, OutcomeReviewed},
		{"merged while reviewing", Reviewing, PRMerged{}, Done, "", ended, OutcomeMerged},
		{"merged when ready", ReviewReady, PRMerged{}, Done, "", ended, OutcomeMerged},
		{"closed while reviewing", Reviewing, PRClosed{}, Done, "", ended, OutcomeClosed},
		{"closed when ready", ReviewReady, PRClosed{}, Done, "", ended, OutcomeClosed},
		{"tab closed while reviewing", Reviewing, TabClosed{}, Reviewing, ReasonTabClosed, nil, ""},
		{"tab closed when ready", ReviewReady, TabClosed{}, ReviewReady, "", nil, ""},
		{"worker exits when ready", ReviewReady, WorkerExited{Code: 0}, ReviewReady, "", nil, ""},
		{"worker fails when ready", ReviewReady, WorkerExited{Code: 1}, ReviewReady, "", nil, ""},
		{"user closes while queued", Queued, TaskClosed{}, Done, "", ended, OutcomeClosed},
		{"user closes while reviewing", Reviewing, TaskClosed{}, Done, "", ended, OutcomeClosed},
		{"user closes when ready", ReviewReady, TaskClosed{}, Done, "", ended, OutcomeClosed},
		{"user skips while queued", Queued, TaskClosed{Skipped: true}, Done, "", ended, OutcomeSkipped},
		{"user skips while reviewing", Reviewing, TaskClosed{Skipped: true}, Done, "", ended, OutcomeSkipped},
		{"user skips when ready", ReviewReady, TaskClosed{Skipped: true}, Done, "", ended, OutcomeSkipped},
		{"rerun the review", ReviewReady, Rerun{Reviewing}, Reviewing, "", []Effect{PromptWorker{Reviewing}}, ""},
		{"blocked when ready", ReviewReady, WorkerStatus{State: "blocked", Kind: "permission"}, ReviewReady, ReasonPermission, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := reviewIn(c.from, "")
			effects, err := b.Apply("R", c.signal)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if task.Stage != c.to || task.Flag != c.flag || task.Outcome != c.outcome {
				t.Errorf("stage %s flag %q outcome %q, want %s %q %q", task.Stage, task.Flag, task.Outcome, c.to, c.flag, c.outcome)
			}
			if !reflect.DeepEqual(effects, c.effects) {
				t.Errorf("effects = %#v, want %#v", effects, c.effects)
			}
		})
	}
}

func TestAReviewStartsOnlyWithAFreeSlot(t *testing.T) {
	b := NewBoard(5)
	b.Slots["platform"] = 1
	first := b.AddReview("R-1", "platform", policy)
	second := b.AddReview("R-2", "platform", policy)
	asked := b.AddReview("R-3", "", policy)
	gone := b.AddReview("R-4", "a queue that left the config", policy)
	if _, err := b.Apply("R-1", Start{}); err != nil || first.Stage != Reviewing {
		t.Fatalf("first start: stage %s err %v", first.Stage, err)
	}
	if _, err := b.Apply("R-2", Start{OverCap: true}); !errors.Is(err, ErrSlotsFull) || second.Stage != Queued {
		t.Fatalf("second start: stage %s err %v, want ErrSlotsFull in Queued", second.Stage, err)
	}
	if _, err := b.Apply("R-4", Start{}); !errors.Is(err, ErrSlotsFull) || gone.Stage != Queued {
		t.Fatalf("start in a queue with no slots: stage %s err %v", gone.Stage, err)
	}
	if _, err := b.Apply("R-3", Start{}); err != nil || asked.Stage != Reviewing {
		t.Fatalf("a review with no queue: stage %s err %v", asked.Stage, err)
	}
	b.Apply("R-1", PRReviewed{})
	if _, err := b.Apply("R-2", Start{}); err != nil || second.Stage != Reviewing {
		t.Fatalf("start after a slot freed: stage %s err %v", second.Stage, err)
	}
}

func TestAReviewStartsOnlyWithAFreeSharedSlot(t *testing.T) {
	b := NewBoard(5)
	b.Slots["first"], b.Slots["second"], b.SharedSlots = 3, 3, 2
	a := b.AddReview("R-1", "first", policy)
	c := b.AddReview("R-2", "second", policy)
	full := b.AddReview("R-3", "first", policy)
	asked := b.AddReview("R-4", "", policy)
	for _, id := range []string{"R-1", "R-2"} {
		if _, err := b.Apply(id, Start{}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := b.Apply("R-3", Start{}); !errors.Is(err, ErrSlotsFull) || full.Stage != Queued {
		t.Fatalf("start past the shared slots: stage %s err %v, want ErrSlotsFull", full.Stage, err)
	}
	if _, err := b.Apply("R-4", Start{}); err != nil || asked.Stage != Reviewing {
		t.Fatalf("a review with no queue: stage %s err %v", asked.Stage, err)
	}
	b.Apply("R-1", PRReviewed{})
	if a.Stage != Done {
		t.Fatalf("R-1 stage %s, want Done", a.Stage)
	}
	if _, err := b.Apply("R-3", Start{}); err != nil || full.Stage != Reviewing || c.Stage != Reviewing {
		t.Fatalf("start after a shared slot freed: stage %s err %v", full.Stage, err)
	}
}

// The user can reopen a closed Review ready Worker, so the Task keeps its
// slot while it has no Worker.
func TestAReviewReadyTaskKeepsItsSlotWithoutItsWorker(t *testing.T) {
	b := NewBoard(5)
	b.Slots["platform"] = 1
	ready := b.AddReview("R-1", "platform", policy)
	next := b.AddReview("R-2", "platform", policy)
	b.Apply("R-1", Start{})
	b.Apply("R-1", WorkerDone{ContractMet: true})
	for _, s := range []Signal{TabClosed{}, WorkerExited{Code: 0}, WorkerExited{Code: 130}} {
		if effects, err := b.Apply("R-1", s); err != nil || effects != nil {
			t.Fatalf("%s: effects %v err %v", s.Name(), effects, err)
		}
		if ready.Stage != ReviewReady || ready.Group() != GroupNeedsYou || b.QueueStarted("platform") != 1 {
			t.Fatalf("%s: stage %s group %s, %d started", s.Name(), ready.Stage, ready.Group(), b.QueueStarted("platform"))
		}
	}
	if _, err := b.Apply("R-2", Start{}); !errors.Is(err, ErrSlotsFull) || next.Stage != Queued {
		t.Fatalf("start with the slot held: stage %s err %v, want ErrSlotsFull", next.Stage, err)
	}
}

func TestReviewsStayOutsideTheWIPCap(t *testing.T) {
	b := NewBoard(1)
	b.Add("B-1", policy)
	review := b.AddReview("R-1", "", policy)
	b.Apply("B-1", Start{})
	if _, err := b.Apply("R-1", Start{}); err != nil || review.Stage != Reviewing {
		t.Fatalf("review start with the cap full: stage %s err %v", review.Stage, err)
	}
	if b.Started() != 1 || b.OverCap() != 0 || review.OverCap {
		t.Fatalf("started %d over cap %d, want only the build Task counted", b.Started(), b.OverCap())
	}
}

func TestEachKindRerunsOnlyItsOwnStages(t *testing.T) {
	cases := []struct {
		name  string
		kind  Kind
		stage Stage
		rerun Stage
	}{
		{"review into Working", KindReview, Reviewing, Working},
		{"review into CI debug", KindReview, ReviewReady, CIDebug},
		{"review into Responding", KindReview, Reviewing, Responding},
		{"build into Reviewing", KindBuild, CIRunning, Reviewing},
	}
	for _, c := range cases {
		b := NewBoard(5)
		task := b.Add("T", policy)
		task.Kind, task.Stage = c.kind, c.stage
		_, err := b.Apply("T", Rerun{c.rerun})
		var na *NotAllowedError
		if !errors.As(err, &na) || task.Stage != c.stage {
			t.Errorf("%s: stage %s err %v, want NotAllowedError", c.name, task.Stage, err)
		}
	}
}

func TestReviewTasksIgnoreBuildSignals(t *testing.T) {
	for _, stage := range []Stage{Reviewing, ReviewReady} {
		for _, s := range []Signal{PROpen{}, Pushed{}, failedCI, approved, needsReview, conflict, PRSeen{PRFacts{Draft: true}},
			ReviewCommented{}, ReviewChangesRequested{}, ReviewAdditionalAuthors{}} {
			b, task := reviewIn(stage, "")
			effects, err := b.Apply("R", s)
			if err != nil || effects != nil || task.Stage != stage {
				t.Errorf("%s in %s: stage %s effects %v err %v", s.Name(), stage, task.Stage, effects, err)
			}
		}
		b, task := reviewIn(stage, "")
		if _, err := b.Apply("R", PRMerge{}); err == nil || task.Stage != stage {
			t.Errorf("merge of a reviewed PR in %s: stage %s err %v", stage, task.Stage, err)
		}
	}
}

func TestBuildTasksIgnoreReviewSignals(t *testing.T) {
	for _, s := range []Signal{PRReviewed{}, PRClosed{}} {
		b, task := taskIn(InReview)
		if effects, err := b.Apply("T", s); err != nil || effects != nil || task.Stage != InReview {
			t.Errorf("%s: stage %s effects %v err %v", s.Name(), task.Stage, effects, err)
		}
	}
}

// A Record from before review Tasks has no kind, and it must load as a build Task.
func TestATaskSavesItsKindAndOldTasksAreBuilds(t *testing.T) {
	var old Task
	if err := json.Unmarshal([]byte(`{"id":"ENG-1","stage":"working"}`), &old); err != nil || old.Kind != KindBuild {
		t.Fatalf("old task kind %s err %v", old.Kind, err)
	}
	_, review := reviewIn(ReviewReady, "q")
	raw, _ := json.Marshal(review)
	var back Task
	if err := json.Unmarshal(raw, &back); err != nil || back.Kind != KindReview || back.Queue != "q" || back.Stage != ReviewReady {
		t.Fatalf("saved %s, loaded %+v, err %v", raw, back, err)
	}
}
