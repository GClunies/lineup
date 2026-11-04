package lifecycle

// Signal is an observed event or a user action. Each concrete type is one kind
// from the spec's transition table, and Name returns the spec's name for it.
type Signal interface {
	Name() string
	signal()
}

// User signals. Apply returns an error when one does not fit the Task's Stage.

type Start struct{ OverCap bool }
type PRMerge struct{}
type TaskClosed struct{}

// Rerun is the Orchestrator's override that starts a fresh Worker for Stage.
type Rerun struct{ Stage Stage }

// Worker signals.

// WorkerDone means the Worker reported done. The Scheduler checks the Stage
// contract first and sets ContractMet.
type WorkerDone struct{ ContractMet bool }

// WorkerStatus is a program status other than done: working, blocked, or error.
type WorkerStatus struct {
	State  string // working, blocked, error, or idle
	Kind   string // for blocked: permission, question, or auth
	Detail string
}

type WorkerExited struct{ Code int }
type WorkerLost struct{}
type SetupFailed struct{ Detail string }

// GitHub signals.

type PROpen struct{}
type Pushed struct{}
type CIFailed struct{}

// CIPassed carries the review facts that decide between In review and Ready to land.
type CIPassed struct {
	Draft       bool
	NeedsReview bool // reviewers exist and have not approved
}

type ReviewApproved struct{}
type ReviewCommented struct{}
type ReviewChangesRequested struct{}
type ReviewAdditionalAuthors struct{}
type PRConflict struct{}
type PRMerged struct{}

func (Start) Name() string                   { return "start" }
func (PRMerge) Name() string                 { return "pr-merge" }
func (TaskClosed) Name() string              { return "task-closed" }
func (Rerun) Name() string                   { return "rerun" }
func (WorkerDone) Name() string              { return "done" }
func (WorkerStatus) Name() string            { return "status" }
func (WorkerExited) Name() string            { return "exited" }
func (WorkerLost) Name() string              { return "worker-lost" }
func (SetupFailed) Name() string             { return "setup-failed" }
func (PROpen) Name() string                  { return "pr-open" }
func (Pushed) Name() string                  { return "pushed" }
func (CIFailed) Name() string                { return "ci-failed" }
func (CIPassed) Name() string                { return "ci-passed" }
func (ReviewApproved) Name() string          { return "review-approved" }
func (ReviewCommented) Name() string         { return "review-commented" }
func (ReviewChangesRequested) Name() string  { return "review-changes-requested" }
func (ReviewAdditionalAuthors) Name() string { return "review-additional-authors" }
func (PRConflict) Name() string              { return "pr-conflict" }
func (PRMerged) Name() string                { return "pr-merged" }

func (Start) signal()                   {}
func (PRMerge) signal()                 {}
func (TaskClosed) signal()              {}
func (Rerun) signal()                   {}
func (WorkerDone) signal()              {}
func (WorkerStatus) signal()            {}
func (WorkerExited) signal()            {}
func (WorkerLost) signal()              {}
func (SetupFailed) signal()             {}
func (PROpen) signal()                  {}
func (Pushed) signal()                  {}
func (CIFailed) signal()                {}
func (CIPassed) signal()                {}
func (ReviewApproved) signal()          {}
func (ReviewCommented) signal()         {}
func (ReviewChangesRequested) signal()  {}
func (ReviewAdditionalAuthors) signal() {}
func (PRConflict) signal()              {}
func (PRMerged) signal()                {}

// Effect is work the Scheduler must do after a transition, in order.
type Effect interface{ effect() }

// StartWorker replaces any Worker in the Task's tab with a fresh one for Stage.
type StartWorker struct{ Stage Stage }

// MarkReady marks the draft PR ready for review and requests CODEOWNERS reviewers.
type MarkReady struct{}

type Merge struct{}

// Cleanup closes the Task's tab and removes its worktree.
type Cleanup struct{}

// TrackerStatus writes the mapped Tracker status for a lifecycle event:
// started, in_review, merged, or closed.
type TrackerStatus struct{ Event string }

func (StartWorker) effect()   {}
func (MarkReady) effect()     {}
func (Merge) effect()         {}
func (Cleanup) effect()       {}
func (TrackerStatus) effect() {}
