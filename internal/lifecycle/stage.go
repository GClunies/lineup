// Package lifecycle holds the Task lifecycle as a pure reducer. The Scheduler
// feeds it Signals and executes the Effects it returns. Nothing here does I/O.
package lifecycle

import (
	"fmt"
	"slices"
)

type Stage int

const (
	Queued Stage = iota
	Working
	CIRunning
	CIDebug
	InReview
	Responding
	ReadyToLand
	Done
	Reviewing
	ReviewReady
)

// Kind is the sort of work that a Task does. A build Task makes the user's own
// PR. A review Task reviews someone else's PR. Each Kind has its own Stages,
// and Queued and Done belong to both.
type Kind int

const (
	KindBuild Kind = iota
	KindReview
)

var kindNames = [...]string{KindBuild: "build", KindReview: "review"}

func (k Kind) String() string { return kindNames[k] }

// Has reports whether a Task of this Kind can be in the Stage.
func (k Kind) Has(s Stage) bool { return stages[s].kinds&(1<<k) != 0 }

var firstStages = [...]Stage{KindBuild: Working, KindReview: Reviewing}

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
)

type Owner int

const (
	OwnerUser Owner = iota
	OwnerAgent
	OwnerCI
	OwnerReviewers
	OwnerNone
)

type Group int

const (
	GroupNeedsYou Group = iota
	GroupWorking
	GroupWaiting
	GroupQueued
	GroupDone
)

var stages = [...]struct {
	name  string
	key   string
	owner Owner
	group Group
	kinds kindSet
	// resumesWorker marks a Stage whose Worker the user can reopen by
	// resuming its conversation. Review ready waits for the user's decisions
	// in that conversation, so a closed tab loses nothing.
	resumesWorker bool
}{
	Queued:      {"Queued", "queued", OwnerUser, GroupQueued, build | review, false},
	Working:     {"Working", "working", OwnerAgent, GroupWorking, build, false},
	CIRunning:   {"CI running", "ci_running", OwnerCI, GroupWaiting, build, false},
	CIDebug:     {"CI debug", "ci_debug", OwnerAgent, GroupWorking, build, false},
	InReview:    {"In review", "in_review", OwnerReviewers, GroupWaiting, build, false},
	Responding:  {"Responding", "responding", OwnerAgent, GroupWorking, build, false},
	ReadyToLand: {"Ready to land", "ready_to_land", OwnerUser, GroupNeedsYou, build, false},
	Done:        {"Done", "done", OwnerNone, GroupDone, build | review, false},
	Reviewing:   {"Reviewing", "reviewing", OwnerAgent, GroupWorking, review, false},
	ReviewReady: {"Review ready", "review_ready", OwnerUser, GroupNeedsYou, review, true},
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
func (s Stage) Started() bool { return s != Queued && s != Done }

// Resumable reports whether the user can reopen the Stage's Worker by
// resuming its conversation.
func (s Stage) Resumable() bool { return stages[s].resumesWorker }

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
	return [...]string{"Needs you", "Working", "Waiting", "Queued", "Done"}[g]
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
	ReasonWorkerLost  Reason = "worker lost"
	// ReasonWorkerSilent is a Stage prompt that waits on a Worker that has sent
	// no status.
	ReasonWorkerSilent Reason = "worker silent"
	// ReasonDraft is a passing PR that is a draft. lineup never marks a PR
	// ready, so it waits for the user to.
	ReasonDraft       Reason = "draft"
	ReasonNoBotReview Reason = "no bot review"
)

// Transient reasons clear when the Worker's program status goes back to working.
// A Worker that stops before its contract output often waits for the user, so
// the user's reply clears it.
func (r Reason) Transient() bool {
	switch r {
	case ReasonQuestion, ReasonPermission, ReasonAuth, ReasonError, ReasonStopped, ReasonWorkerSilent:
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
)
