// Package lifecycle holds the Task lifecycle as a pure reducer. The Scheduler
// feeds it Signals and executes the Effects it returns. Nothing here does I/O.
package lifecycle

import (
	"fmt"
	"slices"
)

type Stage int

const (
	Working Stage = iota
	CIRunning
	CIDebug
	InReview
	Responding
	ReadyToLand
	Done
	Reviewing
	ReviewReady
	QAWaiting
	Verifying
	QAFailed
)

// Kind is the sort of work that a Task does. A build Task makes the user's own
// PR. A review Task reviews someone else's PR. A QA Task checks a merged
// change after it runs (ARCH-QA-001). Each Kind has its own Stages, and Done
// belongs to each.
type Kind int

const (
	KindBuild Kind = iota
	KindReview
	KindQA
)

var kindNames = [...]string{KindBuild: "build", KindReview: "review", KindQA: "qa"}

func (k Kind) String() string { return kindNames[k] }

// Has reports whether a Task of this Kind can be in the Stage.
func (k Kind) Has(s Stage) bool { return stages[s].kinds&(1<<k) != 0 }

// A QA Task starts in QA waiting, and its Worker starts in Verifying.
var firstStages = [...]Stage{KindBuild: Working, KindReview: Reviewing, KindQA: Verifying}

// FirstStage is the Stage that a Task of this Kind starts its Worker in. The
// Worker keeps the model and effort of this Stage for the Task's whole life.
func (k Kind) FirstStage() Stage { return firstStages[k] }

func (s Stage) StartsWorker() bool { return slices.Contains(firstStages[:], s) }

func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

func (k *Kind) UnmarshalText(b []byte) error {
	for i, name := range kindNames {
		if name == string(b) {
			*k = Kind(i)
			return nil
		}
	}
	return fmt.Errorf("unknown kind %q", b)
}

// kindSet is a set of Kinds, one bit for each.
type kindSet uint8

const (
	build  = kindSet(1 << KindBuild)
	review = kindSet(1 << KindReview)
	qa     = kindSet(1 << KindQA)
)

type Owner int

const (
	OwnerUser Owner = iota
	OwnerAgent
	OwnerCI
	OwnerReviewers
	OwnerNone
	// OwnerClock is lineup's clock, which starts each due QA check.
	OwnerClock
)

type Group int

const (
	GroupNeedsYou Group = iota
	GroupWorking
	GroupWaiting
	GroupDone
)

var stages = [...]struct {
	name  string
	key   string
	owner Owner
	group Group
	kinds kindSet
}{
	Working:     {"Working", "working", OwnerAgent, GroupWorking, build},
	CIRunning:   {"CI running", "ci_running", OwnerCI, GroupWaiting, build},
	CIDebug:     {"CI debug", "ci_debug", OwnerAgent, GroupWorking, build},
	InReview:    {"In review", "in_review", OwnerReviewers, GroupWaiting, build},
	Responding:  {"Responding", "responding", OwnerAgent, GroupWorking, build},
	ReadyToLand: {"Ready to land", "ready_to_land", OwnerUser, GroupNeedsYou, build},
	Done:        {"Done", "done", OwnerNone, GroupDone, build | review | qa},
	Reviewing:   {"Reviewing", "reviewing", OwnerAgent, GroupWorking, review},
	ReviewReady: {"Review ready", "review_ready", OwnerUser, GroupNeedsYou, review},
	QAWaiting:   {"QA waiting", "qa_waiting", OwnerClock, GroupWaiting, qa},
	Verifying:   {"Verifying", "verifying", OwnerAgent, GroupWorking, qa},
	QAFailed:    {"QA failed", "qa_failed", OwnerUser, GroupNeedsYou, qa},
}

// Stages lists every Stage of every Kind.
func Stages() []Stage {
	out := make([]Stage, len(stages))
	for i := range stages {
		out[i] = Stage(i)
	}
	return out
}

func (s Stage) String() string { return stages[s].name }

// Key is the stage's name in configuration and on the CLI, for example ci_debug.
func (s Stage) Key() string { return stages[s].key }

func (s Stage) Owner() Owner { return stages[s].owner }

func (s Stage) Agent() bool { return stages[s].owner == OwnerAgent }

// Started reports whether a Task in the Stage has started: it counts toward
// the WIP cap for a build Task, and toward its queue's slots for a review Task.
func (s Stage) Started() bool { return s != Done }

// MarshalText saves the Stage by its key, so that the store does not depend on
// the order of the constants.
func (s Stage) MarshalText() ([]byte, error) { return []byte(s.Key()), nil }

func (s *Stage) UnmarshalText(b []byte) error {
	st, err := ParseStage(string(b))
	*s = st
	return err
}

func ParseStage(key string) (Stage, error) {
	for i, st := range stages {
		if st.key == key {
			return Stage(i), nil
		}
	}
	return 0, fmt.Errorf("unknown stage %q", key)
}

func (g Group) String() string {
	return [...]string{"Needs you", "Working", "Waiting", "Done"}[g]
}

// Reason says why a Task has the Needs you flag. The empty Reason is no flag.
type Reason string

const (
	ReasonQuestion    Reason = "question"
	ReasonPermission  Reason = "permission"
	ReasonAuth        Reason = "auth"
	ReasonError       Reason = "error"
	ReasonSetupFailed Reason = "setup failed"
	ReasonStopped     Reason = "stopped"
	ReasonBudgetSpent Reason = "budget spent"
	// ReasonTabClosed means the Worker's tab is gone in a Stage that needs the
	// Worker. The user closed the tab, or Rex lost it. lineup opens no tab.
	ReasonTabClosed Reason = "tab closed"
	// ReasonWorkerSilent is a Stage prompt that waits on a Worker that has sent
	// no status.
	ReasonWorkerSilent Reason = "worker silent"
	// ReasonDraft is a passing PR that is a draft. lineup never marks a PR
	// ready, so it waits for the user to.
	ReasonDraft       Reason = "draft"
	ReasonNoBotReview Reason = "no bot review"
	// ReasonDrafts is a Responding Worker that waits for the user to decide
	// its Draft replies. The detail counts them.
	ReasonDrafts Reason = "drafts"
	// ReasonPRClosed is a build Task whose PR closed without a merge. The work
	// is the user's: they reopen the PR or close the Task.
	ReasonPRClosed Reason = "pr closed"
)

// reasonWorkerLost is the flag that a Record from before tab closed can carry.
const reasonWorkerLost Reason = "worker lost"

// Transient reasons clear when the Worker's program status goes back to working.
// A Worker that stops before its contract output often waits for the user, so
// the user's reply clears it.
func (r Reason) Transient() bool {
	switch r {
	case ReasonQuestion, ReasonDrafts, ReasonPermission, ReasonAuth, ReasonError, ReasonStopped, ReasonWorkerSilent, reasonWorkerLost:
		return true
	}
	return false
}

type Outcome string

const (
	OutcomeNone     Outcome = ""
	OutcomeMerged   Outcome = "merged"
	OutcomeClosed   Outcome = "closed"
	OutcomeReviewed Outcome = "reviewed"
	OutcomeSkipped  Outcome = "skipped"
	// A QA Task ends passed by its Worker's pass, accepted by the user's
	// accept of a fail, or fix filed by the user's fix Issue.
	OutcomePassed   Outcome = "passed"
	OutcomeAccepted Outcome = "accepted"
	OutcomeFixFiled Outcome = "fix filed"
	// OutcomeRemoved is a Task that lineup stored before an add started each
	// Task, and that never started.
	OutcomeRemoved Outcome = "removed"
)
