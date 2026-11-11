package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func seen(f PRFacts) Signal { return PRSeen{f} }

var anySignal = rapid.OneOf(rapid.SampledFrom([]Signal{
	Start{}, Start{OverCap: true}, PRMerge{}, TaskClosed{},
	Rerun{Working}, Rerun{CIDebug}, Rerun{Responding}, Rerun{Reviewing}, Reopen{},
	WorkerDone{ContractMet: true}, WorkerDone{ContractMet: false},
	WorkerStatus{State: "working"}, WorkerStatus{State: "blocked", Kind: "permission"}, WorkerStatus{State: "error"},
	WorkerExited{Code: 0}, WorkerExited{Code: 1}, WorkerLost{}, SetupFailed{},
	PROpen{}, Pushed{}, ReviewCommented{}, ReviewChangesRequested{}, ReviewAdditionalAuthors{}, PRMerged{},
	PRReviewed{}, PRClosed{}, ReviewBotLate{}, TabClosed{},
}), rapid.Map(anyFacts, seen))

// spending are the facts of a PR whose first gap spends a Loop budget under
// every bar: a conflict, and a required check that failed.
var spending = []Signal{
	PRSeen{PRFacts{Head: "h", Conflict: true}},
	PRSeen{PRFacts{Head: "h", Checks: []Check{{Name: "test", State: CheckFailed, Required: true}}}},
}

// failure is what a Worker that never succeeds, and a world that keeps
// rejecting its work, sends for a Task in each Stage. It leaves out the
// failures that flag the Task at once, so only the Loop budgets can stop it.
func failure(t *rapid.T, s Stage, pushes bool) Signal {
	switch s {
	case CIRunning:
		return rapid.SampledFrom(spending).Draw(t, "ci")
	case InReview:
		return rapid.SampledFrom(append([]Signal{ReviewCommented{}, ReviewChangesRequested{}, ReviewAdditionalAuthors{}}, spending...)).Draw(t, "review")
	case CIDebug, Responding:
		if pushes {
			return Pushed{}
		}
	}
	return WorkerLost{}
}

// queues are the review queues of a random Board. "" is a review with no queue.
var queues = []string{"", "q1", "q2"}

// randomBoard mixes build Tasks with review Tasks. q1 and q2 get 0 to 2
// slots, so some review starts must fail.
func randomBoard(t *rapid.T) (*Board, []string) {
	b := NewBoard(rapid.IntRange(1, 4).Draw(t, "cap"))
	for _, q := range queues[1:] {
		b.Slots[q] = rapid.IntRange(0, 2).Draw(t, "slots "+q)
	}
	b.SharedSlots = rapid.IntRange(0, 3).Draw(t, "shared slots")
	var ids []string
	for i := range rapid.IntRange(1, 8).Draw(t, "tasks") {
		id := fmt.Sprintf("T%d", i)
		p := Policy{
			Budgets: Budgets{
				CIDebug:    rapid.IntRange(0, 3).Draw(t, "ci"),
				Responding: rapid.IntRange(0, 3).Draw(t, "resp"),
				Restarts:   rapid.IntRange(0, 2).Draw(t, "restarts"),
			},
			Bar: anyBar.Draw(t, "bar"),
		}
		if p.Bar.Bot != nil {
			p.Budgets.ReviewCycles = rapid.IntRange(0, 3).Draw(t, "cycles")
		}
		if rapid.Bool().Draw(t, "review") {
			b.AddReview(id, rapid.SampledFrom(queues).Draw(t, "queue"), p)
		} else {
			b.Add(id, p)
		}
		ids = append(ids, id)
	}
	return b, ids
}

// gapsOn each add one gap to a PR that meets every bar. A bar with no bot
// ignores the score gaps, and a bar that ignores threads ignores the thread gaps.
var gapsOn = []func(*PRFacts){
	func(f *PRFacts) { f.Conflict = true },
	func(f *PRFacts) { f.Checks = append(f.Checks, Check{Name: "enable", State: CheckFailed}) },
	func(f *PRFacts) {
		f.Checks = append(f.Checks, Check{Name: "enable", State: CheckPending, Required: true})
	},
	func(f *PRFacts) { f.Draft = true },
	func(f *PRFacts) { f.Score = nil },
	func(f *PRFacts) { low := 0; f.Score = &low },
	func(f *PRFacts) { f.Threads = append(f.Threads, Thread{Bot: true}) },
	func(f *PRFacts) { f.Threads = append(f.Threads, Thread{}) },
	func(f *PRFacts) { f.Decision = DecisionRequired },
}

func withGap(f PRFacts, add func(*PRFacts)) PRFacts {
	f.Checks, f.Threads = slices.Clone(f.Checks), slices.Clone(f.Threads)
	add(&f)
	return f
}

// nearFacts draws a PR that meets every bar but for a few gaps, so that each
// gap is often the first one.
var nearFacts = rapid.Custom(func(t *rapid.T) PRFacts {
	f := passingFacts.Draw(t, "passing")
	for range rapid.IntRange(0, 2).Draw(t, "gaps") {
		f = withGap(f, rapid.SampledFrom(gapsOn).Draw(t, "gap"))
	}
	return f
})

// barSignal favors the Signals that carry a Task to CI running and past its
// Landing bar. With anySignal alone, a run reaches Ready to land rarely.
var barSignal = rapid.OneOf(anySignal, rapid.SampledFrom([]Signal{
	Start{OverCap: true}, WorkerDone{ContractMet: true}, Pushed{},
}), rapid.Map(passingFacts, seen), rapid.Map(nearFacts, seen))

// step applies one random Signal to the Task and fails on an error that no
// caller expects.
func step(t *rapid.T, b *Board, task *Task) (Signal, []Effect, error) {
	return stepFrom(t, b, task, anySignal)
}

func stepFrom(t *rapid.T, b *Board, task *Task, signals *rapid.Generator[Signal]) (Signal, []Effect, error) {
	s := signals.Draw(t, "signal")
	effects, err := b.Apply(task.ID, s)
	var na *NotAllowedError
	if err != nil && !errors.As(err, &na) && !errors.Is(err, ErrWIPCapFull) && !errors.Is(err, ErrSlotsFull) {
		t.Fatal(err)
	}
	return s, effects, err
}

func anyTask(t *rapid.T, b *Board, ids []string) *Task {
	return b.Tasks[rapid.SampledFrom(ids).Draw(t, "task")]
}

func TestWIPCapNeverPassedWithoutOverride(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			id := rapid.SampledFrom(ids).Draw(t, "task")
			s := anySignal.Draw(t, "signal")
			before := b.Started()
			_, err := b.Apply(id, s)
			if start, ok := s.(Start); ok && err == nil && !start.OverCap && before >= b.Cap && b.Tasks[id].Kind == KindBuild {
				t.Fatalf("start without override with %d started and cap %d", before, b.Cap)
			}
			if b.Started()-b.OverCap() > b.Cap {
				t.Fatalf("%d started, %d recorded over cap, cap %d", b.Started(), b.OverCap(), b.Cap)
			}
		}
	})
}

func TestEveryStartedTaskEndsInDoneOrNeedsYou(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 100).Draw(t, "steps") {
			step(t, b, anyTask(t, b, ids))
		}
		// Budgets total at most 3+3+2 per Stage visit, so a bounded number of
		// failures must reach the user.
		const bound = 60
		for _, id := range ids {
			task := b.Tasks[id]
			if !task.Stage.Started() {
				continue
			}
			pushes := rapid.Bool().Draw(t, "pushes bad fixes")
			for step := 0; task.Group() != GroupNeedsYou && task.Group() != GroupDone; step++ {
				if step == bound {
					t.Fatalf("%s still in %s after %d failures", id, task.Stage, bound)
				}
				if _, err := b.Apply(id, failure(t, task.Stage, pushes)); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func TestReviewStartNeverPassesItsQueueSlots(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			before := b.QueueStarted(task.Queue)
			s, _, err := step(t, b, task)
			if _, ok := s.(Start); ok && err == nil && task.Kind == KindReview && task.Queue != "" && before >= b.Slots[task.Queue] {
				t.Fatalf("%s started in %s with %d started and %d slots", task.ID, task.Queue, before, b.Slots[task.Queue])
			}
			if b.SharedSlots > 0 && b.QueuesStarted() > b.SharedSlots {
				t.Fatalf("%d started queue reviews and %d shared slots", b.QueuesStarted(), b.SharedSlots)
			}
			for _, q := range queues[1:] {
				if b.QueueStarted(q) > b.Slots[q] {
					t.Fatalf("%s has %d started reviews and %d slots", q, b.QueueStarted(q), b.Slots[q])
				}
			}
		}
	})
}

func TestReviewTasksNeverChangeTheWIPCount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			before, over := b.Started(), b.OverCap()
			if _, err := b.Apply(task.ID, anySignal.Draw(t, "signal")); err == nil && task.Kind == KindReview && (b.Started() != before || b.OverCap() != over) {
				t.Fatalf("a review signal moved started %d->%d, over cap %d->%d", before, b.Started(), over, b.OverCap())
			}
		}
	})
}

func TestEachKindStaysInItsOwnStages(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			s, _, _ := step(t, b, task)
			if !task.Kind.Has(task.Stage) {
				t.Fatalf("%s Task %s entered %s after %s", task.Kind, task.ID, task.Stage, s.Name())
			}
		}
	})
}

func TestReviewTasksGetNoEffectThatWritesOutside(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			s, effects, _ := step(t, b, task)
			if task.Kind != KindReview {
				continue
			}
			for _, e := range effects {
				switch e.(type) {
				case Merge, TrackerStatus:
					t.Fatalf("review Task %s got %#v from %s", task.ID, e, s.Name())
				}
			}
		}
	})
}

// A Review ready Task, and so its slot, stays until the PR or the user ends
// the review. No Worker signal moves it.
func TestAReviewReadyTaskLeavesOnlyByThePROrTheUser(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, task := reviewIn(ReviewReady, rapid.SampledFrom(queues).Draw(t, "queue"))
		for task.Stage == ReviewReady {
			s, _, _ := step(t, b, task)
			if task.Stage == ReviewReady {
				continue
			}
			switch s.(type) {
			case PRReviewed, PRMerged, PRClosed, TaskClosed, Rerun:
			default:
				t.Fatalf("%s left Review ready for %s", s.Name(), task.Stage)
			}
		}
	})
}

// A build Task is in Ready to land only while its Standing has no gap. Only a
// pr-seen whose facts meet the Task's bar moves it there, so no override and
// no Worker skips the bar.
func TestOnlyAPRThatMeetsTheBarIsReadyToLand(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			from := task.Stage
			s, _, _ := stepFrom(t, b, task, barSignal)
			if task.Stage != ReadyToLand {
				continue
			}
			if !task.Standing.Met() {
				t.Fatalf("%s is in Ready to land with %q", task.ID, task.Policy.Bar.Explain(task.Standing))
			}
			seen, ok := s.(PRSeen)
			if !ok && from != ReadyToLand {
				t.Fatalf("%s entered Ready to land from %s on %s", task.ID, from, s.Name())
			}
			if ok && !meets(task.Policy.Bar, seen.Facts) {
				t.Fatalf("%s is in Ready to land after facts %+v that miss bar %+v", task.ID, seen.Facts, task.Policy.Bar)
			}
		}
	})
}

func TestAnOptionalCheckNeverStartsCIDebug(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			s, effects, _ := stepFrom(t, b, task, barSignal)
			seen, ok := s.(PRSeen)
			if ok && slices.Contains(effects, Effect(PromptWorker{CIDebug})) && !plainGaps(task.Policy.Bar, seen.Facts)[GapFailed] {
				t.Fatalf("%s started CI debug on facts %+v with no gating check failed under %+v", task.ID, seen.Facts, task.Policy.Bar)
			}
		}
	})
}

// Each poll repeats pr-seen, so a repeat must change nothing. Each reachable
// Task gets a PR with each single gap, and one random PR.
func TestASecondPRSeenChangesNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 100).Draw(t, "steps") {
			stepFrom(t, b, anyTask(t, b, ids), barSignal)
		}
		task := anyTask(t, b, ids)
		if rapid.Bool().Draw(t, "to CI running") {
			b.Apply(task.ID, Start{OverCap: true})
			b.Apply(task.ID, WorkerDone{ContractMet: true})
		}
		passing := passingFacts.Draw(t, "passing")
		facts := []PRFacts{passing, anyFacts.Draw(t, "facts")}
		for _, add := range gapsOn {
			facts = append(facts, withGap(passing, add))
		}
		saved, _ := json.Marshal(task)
		for _, f := range facts {
			var c Task
			if err := json.Unmarshal(saved, &c); err != nil {
				t.Fatal(err)
			}
			one := &Board{Cap: 1, Slots: map[string]int{}, Tasks: map[string]*Task{c.ID: &c}}
			if _, err := one.Apply(c.ID, PRSeen{f}); err != nil {
				t.Fatal(err)
			}
			before := c
			effects, err := one.Apply(c.ID, PRSeen{f})
			if err != nil || effects != nil || !reflect.DeepEqual(c, before) {
				t.Fatalf("second pr-seen of %+v: effects %v err %v\nbefore %+v\nafter  %+v", f, effects, err, before, c)
			}
		}
	})
}

func TestReviewCyclesNeverPassTheirBudget(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		rounds := map[string]int{}
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			task := anyTask(t, b, ids)
			s, effects, _ := stepFrom(t, b, task, barSignal)
			if _, ok := s.(PRSeen); ok && slices.Contains(effects, Effect(PromptWorker{Responding})) && task.BotScore != nil {
				rounds[task.ID]++
			}
			if rounds[task.ID] > task.Policy.Budgets.ReviewCycles || task.Left.ReviewCycles < 0 {
				t.Fatalf("%s started %d review cycles with a budget of %d, %d left", task.ID, rounds[task.ID], task.Policy.Budgets.ReviewCycles, task.Left.ReviewCycles)
			}
		}
	})
}
