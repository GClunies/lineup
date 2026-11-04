// Package lifecycle holds the Task lifecycle as a pure reducer. The Scheduler
// feeds it Signals and executes the Effects it returns. Nothing here does I/O.
package lifecycle

import "fmt"

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
}{
	Queued:      {"Queued", "queued", OwnerUser, GroupQueued},
	Working:     {"Working", "working", OwnerAgent, GroupWorking},
	CIRunning:   {"CI running", "ci_running", OwnerCI, GroupWaiting},
	CIDebug:     {"CI debug", "ci_debug", OwnerAgent, GroupWorking},
	InReview:    {"In review", "in_review", OwnerReviewers, GroupWaiting},
	Responding:  {"Responding", "responding", OwnerAgent, GroupWorking},
	ReadyToLand: {"Ready to land", "ready_to_land", OwnerUser, GroupNeedsYou},
	Done:        {"Done", "done", OwnerNone, GroupDone},
}

func (s Stage) String() string { return stages[s].name }

// Key is the stage's name in configuration and on the CLI, for example ci_debug.
func (s Stage) Key() string { return stages[s].key }

func (s Stage) Owner() Owner { return stages[s].owner }

func (s Stage) Agent() bool { return stages[s].owner == OwnerAgent }

// Started reports whether the Stage counts toward the WIP cap.
func (s Stage) Started() bool { return s != Queued && s != Done }

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
)

// Transient reasons clear when the Worker's program status goes back to working.
// A Worker that stops before its contract output often waits for the user, so
// the user's reply clears it.
func (r Reason) Transient() bool {
	switch r {
	case ReasonQuestion, ReasonPermission, ReasonAuth, ReasonError, ReasonStopped:
		return true
	}
	return false
}

type Outcome string

const (
	OutcomeNone   Outcome = ""
	OutcomeMerged Outcome = "merged"
	OutcomeClosed Outcome = "closed"
)
