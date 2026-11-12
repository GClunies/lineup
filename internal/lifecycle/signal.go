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

// TaskClosed is the user's close. It ends a Task that has not ended, and it
// cleans up a Done Task that kept its tab.
type TaskClosed struct{}

// Rerun is the Orchestrator's override that types the prompt for Stage into the
// Task's Worker again.
type Rerun struct{ Stage Stage }

// Worker signals.

// WorkerDone means the Worker reported done. The Scheduler checks the Stage
// contract first and sets ContractMet. Detail is the Worker's last line, which
// a stop shows as its flag detail.
type WorkerDone struct {
	ContractMet bool
	Detail      string
}

// WorkerStatus is a program status other than done: working, blocked, or error.
type WorkerStatus struct {
	State  string // working, blocked, error, or idle
	Kind   string // for blocked: permission, question, or auth
	Detail string
}

// TabClosed means the Task's tab is gone: the user closed it, or Rex lost it. A
// Done Task then cleans up. A Task in an agent Stage gets the tab closed flag,
// and a Task in a Stage that waits for others changes nothing. Waits is true
// when the Stage's prompt waits for a Worker that has no tab.
type TabClosed struct{ Waits bool }

// TabOpened means the user restored the Task's Worker in a tab. The old
// Worker's flags go, such as tab closed.
type TabOpened struct{}

type WorkerExited struct{ Code int }
type SetupFailed struct{ Detail string }

// GitHub signals.

type PROpen struct{}
type Pushed struct{}

// PRSeen is a level, not an edge. Each poll of an open PR sends it with the
// PR's current facts, after the poll's edge Signals. A repeat changes nothing.
type PRSeen struct{ Facts PRFacts }

// ReviewBotLate is the Scheduler's timer: the Task has been in CI running
// long enough that a review bot that reviews the head would have scored it.
type ReviewBotLate struct{}

// PromptStalled is the Scheduler's timer: the prompt for Stage has waited too
// long on a Worker that has never reported a program status.
type PromptStalled struct{ Stage Stage }

type ReviewCommented struct{}
type ReviewChangesRequested struct{}
type ReviewAdditionalAuthors struct{}
type PRMerged struct{}

// Review signals. Each is a level, not an edge: the Scheduler computes it from
// the reviewed PR's current state on each full poll.

// PRReviewed means the user submitted a review after the Task was added. A
// pending review is not submitted.
type PRReviewed struct{}

// PRClosed means the PR closed without a merge.
type PRClosed struct{}

func (Start) Name() string                   { return "start" }
func (PRMerge) Name() string                 { return "pr-merge" }
func (TaskClosed) Name() string              { return "task-closed" }
func (TabClosed) Name() string               { return "tab-closed" }
func (TabOpened) Name() string               { return "tab-opened" }
func (Rerun) Name() string                   { return "rerun" }
func (WorkerDone) Name() string              { return "done" }
func (WorkerStatus) Name() string            { return "status" }
func (WorkerExited) Name() string            { return "exited" }
func (SetupFailed) Name() string             { return "setup-failed" }
func (PROpen) Name() string                  { return "pr-open" }
func (Pushed) Name() string                  { return "pushed" }
func (PRSeen) Name() string                  { return "pr-seen" }
func (ReviewCommented) Name() string         { return "review-commented" }
func (ReviewChangesRequested) Name() string  { return "review-changes-requested" }
func (ReviewAdditionalAuthors) Name() string { return "review-additional-authors" }
func (PRMerged) Name() string                { return "pr-merged" }
func (ReviewBotLate) Name() string           { return "review-bot-late" }
func (PromptStalled) Name() string           { return "prompt-stalled" }
func (PRReviewed) Name() string              { return "pr-reviewed" }
func (PRClosed) Name() string                { return "pr-closed" }

func (Start) signal()                   {}
func (PRMerge) signal()                 {}
func (TaskClosed) signal()              {}
func (TabClosed) signal()               {}
func (TabOpened) signal()               {}
func (Rerun) signal()                   {}
func (WorkerDone) signal()              {}
func (WorkerStatus) signal()            {}
func (WorkerExited) signal()            {}
func (SetupFailed) signal()             {}
func (PROpen) signal()                  {}
func (Pushed) signal()                  {}
func (PRSeen) signal()                  {}
func (ReviewCommented) signal()         {}
func (ReviewChangesRequested) signal()  {}
func (ReviewAdditionalAuthors) signal() {}
func (PRMerged) signal()                {}
func (ReviewBotLate) signal()           {}
func (PromptStalled) signal()           {}
func (PRReviewed) signal()              {}
func (PRClosed) signal()                {}

// Effect is work the Scheduler must do after a transition, in order.
type Effect interface{ effect() }

// PromptWorker gives the Task's one Worker the prompt of Stage. It opens the
// Worker first when the Task has none.
type PromptWorker struct{ Stage Stage }

type Merge struct{}

// Cleanup closes the Task's tab if it has one, removes its worktree, and
// removes the Repo session when it is empty. Only the user starts it.
type Cleanup struct{}

// TrackerStatus writes the mapped Tracker status for a lifecycle event:
// started, in_review, merged, or closed.
type TrackerStatus struct{ Event string }

func (PromptWorker) effect()  {}
func (Merge) effect()         {}
func (Cleanup) effect()       {}
func (TrackerStatus) effect() {}
