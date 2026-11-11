# One Worker for each Task

Each Task keeps one Worker for its whole life, in one tab. The Worker is an interactive `claude` with a fixed conversation ID, passed as `--session-id`. When the next agent Stage starts, for example CI debug, Responding, or a rerun, the Scheduler types that Stage's prompt into the same Worker. It types only when the Worker's program status is `idle` or `done`. While the Worker is busy, the prompt waits. No tab closes at a Stage handoff. This supersedes [0004](0004-fresh-worker-per-stage.md). We chose this because the user wants to control his Rex tabs, and a fresh Worker for each Stage closed the tab of the Stage before it.

## Considered Options

- **A fresh Worker for each Stage, as in ADR 0004.** Replaced. It closed the tab at each handoff. No configuration used a model for each Stage, and the Working Worker holds the context that CI debug and Responding need.
- **Split the tab and keep the old Worker's pane.** Rejected. Panes pile up with each Stage, and two agents share one worktree.
- **Ask the user before each Stage.** Rejected. CI debug and Responding stop being automatic. The user wants control of tabs, not of each Stage.

## Consequences

- The Record keeps `Pending`, the Stage whose prompt the Worker has not received. The Stage contract is not checked while `Pending` is set, because a `done` then belongs to the Stage before it or to the user's own message.
- A Worker keeps the model and effort of the Stage that it starts in: Working for a build Task, Reviewing for a review Task. The configuration rejects `model` and `effort` under every other `[stage.<key>]`. `skill` and `command` stay for each Stage, because the typed prompt carries them.
- A build Task gets a conversation ID at its first Worker. A rerun of Reviewing keeps the review Task's conversation.
- A Worker whose tab is gone resumes its conversation with `claude --resume` in a new tab, then gets the Stage prompt at its first `idle`.
- A Worker's context grows over the Task's life. Claude Code compacts it by itself.
