# The Board holds the user's own Issues

The `2 work` view shows the user's own Issues that have no Task, in two new groups, Ready and Backlog. An Issue is the user's own when Linear assigns it to the user or when it is a local Issue. The user plans the what, which is the ideas and the problems that only the user knows. The Worker plans the how, as part of the work ([ADR 0010](0010-planning-belongs-to-the-worker.md)). We chose this because the user wants one board from an idea to a merged PR, and lineup orchestrates the user's own work, not the team's.

## Considered Options

- **The tracker is the database** (lerp). Still rejected, as in [ADR 0002](0002-tasks-live-in-a-local-store.md). Linear holds no Stage and no Needs you flag. A team has about five statuses and lineup has eight Stages. Copies of each Stage would flood the team's views and fight Linear's GitHub automation.
- **A planning tab or a `plan` Task Kind**, where a Worker writes an Issue's criteria in its own tab. Rejected. It plans the how of an Issue that already exists, and that planning belongs to the Worker that does the work.
- **The team's views, as the tracker browser shows them.** Rejected for the work view. Team Issues would bury the user's Tasks. The `1 plan` view still browses the team's views.
- **Columns, one for each group.** Rejected for now. Eight columns do not fit a terminal pane, and the grouped list keeps Needs you at the top.

## Consequences

- An Issue row leaves Ready or Backlog when its Task comes on the Board, because a Task row then shows the Issue.
- `s` on a Ready or Backlog row starts a Task from the Issue, as `s` in the tracker browser does.
- Ready lists unblocked Issues first, then by priority, then by dependency level. A row whose blockers are all done shows `unblocked`.
- Lineup still writes the Linear status only through the status map.
