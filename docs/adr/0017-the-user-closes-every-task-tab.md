# The user closes every Task tab

Only the user closes a Task's tab. When a Task ends, because its PR merged, the user closed it, the user submitted a review, or the PR closed, it goes to Done and keeps its tab and its worktree. We chose this because the user wants to control his Rex tabs. Before this change, the Scheduler closed the tab and removed the worktree at Done, so a tab with a running Worker and unread output disappeared when a PR merged.

A Done Task's tab that the user closes removes the Task's worktree. It also removes the Repo session if that session holds no other tab. An explicit close does the same: `x` or `lineup close` on a Done Task, and `lineup archive` on a Done Task with a tab. A Done Task with an open tab stays on the Board past the 7-day age-off.

## Considered Options

- **Close the tab at Done, as before.** Replaced. The user cannot read the Worker's last output or ask it one more question after a merge, and he cannot tell which tab the Scheduler closed.
- **Close the tab after a delay.** Rejected. The delay is a guess at when the user is done with the tab, and the Scheduler still closes a tab that the user did not choose to close.
- **Keep the tab but remove the worktree at Done.** Rejected. The Worker in the tab runs in the worktree, so removing it breaks the Worker the user keeps.
- **Ask before each close.** Rejected. The user already closes tabs in Rex, and a prompt for each Task end adds a second place to do it.

## Consequences

- A Done Task can hold a tab, a worktree, and a Rex session for as long as the user wants. The Board shows it until the user closes the tab or archives it, so open tabs do not hide.
- The lifecycle has a new Signal, `tab-closed`. The Scheduler applies it when Rex reports that a Done Task's tab closed. `task-closed` on a Done Task and `tab-closed` both emit `Cleanup`. A Task that ends by itself emits no `Cleanup`.
- Recovery applies `tab-closed` to a Done Task whose tab closed while the Scheduler was down. Without it, the row would stay on the Board and the worktree would stay on disk.
- A new Task cannot take the ID of a Done Task that keeps its tab, and a new review cannot take the PR of a Done review that keeps its tab. The new Record would replace the old one and leave the tab and the worktree with no owner. The Scheduler refuses until the user closes the tab, and the review queue adds no review of that PR in the meantime.
- The tracker status still changes at Done. The Issue moves to its merged or closed status when the Task ends, not when the user closes the tab.
- The next changes in this program build on this rule: one Worker for each Task, a restore for a closed tab, and an Orchestrator that asks the user what to do at a Task's end.
