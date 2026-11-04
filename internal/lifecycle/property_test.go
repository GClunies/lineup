package lifecycle

import (
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

var anySignal = rapid.SampledFrom([]Signal{
	Start{}, Start{OverCap: true}, PRMerge{}, TaskClosed{},
	Rerun{Working}, Rerun{CIDebug}, Rerun{Responding},
	WorkerDone{ContractMet: true}, WorkerDone{ContractMet: false},
	WorkerStatus{State: "working"}, WorkerStatus{State: "blocked", Kind: "permission"}, WorkerStatus{State: "error"},
	WorkerExited{Code: 0}, WorkerExited{Code: 1}, WorkerLost{}, SetupFailed{},
	PROpen{}, Pushed{}, CIFailed{}, CIPassed{}, CIPassed{Draft: true}, CIPassed{NeedsReview: true},
	ReviewApproved{}, ReviewCommented{}, ReviewChangesRequested{}, ReviewAdditionalAuthors{}, PRConflict{}, PRMerged{},
})

// failure is what a Worker that never succeeds, and a world that keeps
// rejecting its work, sends for a Task in each Stage. It leaves out the
// failures that flag the Task at once, so only the Loop budgets can stop it.
func failure(t *rapid.T, s Stage, pushes bool) Signal {
	switch s {
	case CIRunning:
		return CIFailed{}
	case InReview:
		return rapid.SampledFrom([]Signal{ReviewCommented{}, ReviewChangesRequested{}, ReviewAdditionalAuthors{}, PRConflict{}}).Draw(t, "review")
	case CIDebug, Responding:
		if pushes {
			return Pushed{}
		}
	}
	return WorkerLost{}
}

func randomBoard(t *rapid.T) (*Board, []string) {
	b := NewBoard(rapid.IntRange(1, 4).Draw(t, "cap"))
	var ids []string
	for i := range rapid.IntRange(1, 8).Draw(t, "tasks") {
		id := fmt.Sprintf("T%d", i)
		b.Add(id, Policy{
			Budgets: Budgets{
				CIDebug:    rapid.IntRange(0, 3).Draw(t, "ci"),
				Responding: rapid.IntRange(0, 3).Draw(t, "resp"),
				Restarts:   rapid.IntRange(0, 2).Draw(t, "restarts"),
			},
		})
		ids = append(ids, id)
	}
	return b, ids
}

func TestWIPCapNeverPassedWithoutOverride(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, ids := randomBoard(t)
		for range rapid.IntRange(0, 200).Draw(t, "steps") {
			id := rapid.SampledFrom(ids).Draw(t, "task")
			s := anySignal.Draw(t, "signal")
			before := b.Started()
			_, err := b.Apply(id, s)
			if start, ok := s.(Start); ok && err == nil && !start.OverCap && before >= b.Cap {
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
			_, err := b.Apply(rapid.SampledFrom(ids).Draw(t, "task"), anySignal.Draw(t, "signal"))
			var na *NotAllowedError
			if err != nil && !errors.As(err, &na) && !errors.Is(err, ErrWIPCapFull) {
				t.Fatal(err)
			}
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
