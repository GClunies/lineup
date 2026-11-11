# The user closes every Task tab

Only the user closes a Task's tab. A Task ends when its PR merges, the user closes it, the user submits a review, or the PR closes. It then goes to Done and keeps its tab and its worktree. We chose this because the user wants control of Rex tabs. Before this change, the Scheduler closed the tab and removed the worktree at Done. A tab with a running Worker and unread output then disappeared when a PR merged.

A Done Task's tab that the user closes removes the Task's worktree. It also removes the Repo session if that session holds no other tab. An explicit close does the same: `x` or `lineup close` on a Done Task, and `lineup archive` on a Done Task with a tab. A Done Task with an open tab stays on the Board past the 7-day age-off.

## Considered Options

- **Close the tab at Done, as before.** Replaced. The user cannot read the Worker's last output or ask it one more question after a merge.
- **Close the tab after a delay.** Rejected. The delay is a guess at when the user is done with the tab. The Scheduler would still close a tab that the user did not choose to close.
- **Keep the tab but remove the worktree at Done.** Rejected. The Worker in the tab runs in the worktree, so removing it breaks the Worker that the user keeps.
- **Ask before each close.** Rejected. The user already closes tabs in Rex. A prompt for each Task end adds a second place to do it.

## Consequences

- A Done Task can hold a tab, a worktree, and a Rex session for as long as the user wants. The row stays on the Board at least until the user closes the tab. After that it leaves when the user archives it or 7 days after it ends.
- The lifecycle has a new Signal, `tab-closed`. The Scheduler applies it when Rex reports that a Done Task's tab closed. `task-closed` on a Done Task and `tab-closed` both emit `Cleanup`. A Task that ends by itself emits no `Cleanup`.
- Recovery applies `tab-closed` to a Done Task whose tab closed while the Scheduler was down. Without it, the row would stay on the Board and the worktree would stay on disk.
- A new Task cannot take the ID of a Done Task that keeps its tab. A new review cannot take the PR of a Done review that keeps its tab. The new Record would replace the old one and leave the tab and the worktree with no owner. The Scheduler refuses until the user closes the tab. The review queue adds no review of that PR in the meantime.
- The tracker status still changes at Done. The Issue moves to its merged or closed status when the Task ends, not when the user closes the tab.
- The next changes in this program build on this rule. They are one Worker for each Task and a restore for a closed tab. A third is an Orchestrator that asks the user what to do at a Task's end.
