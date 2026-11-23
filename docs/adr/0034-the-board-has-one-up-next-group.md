# The Board has one Up next group

This ADR amends [ADR 0019](0019-the-board-holds-my-issues.md). The Up next group replaces the Ready group. The Backlog group does not change.

The Board shows the work that the user starts next in one group, Up next. It holds the user's own Issues that have no Task, in the order that Ready had. Its `[board]` key is `up_next`. We chose this because Ready and Queued both meant "start this next", and the user could not tell them apart. The name Ready also read like the Stage Ready to land. Issue #121 removed the Queued Stage, so a Task exists only once its Worker starts. Up next therefore holds only Issue rows.

## Considered Options

- **Keep Ready, and remove only Queued.** Rejected. Issue #121 did that, and the name still collides with Ready to land. An Issue row in Ready showed the status word `ready`, the same word as a Task in Ready to land.
- **Queued Tasks first in Up next, with a `queued` mark.** Rejected. Issue #121 removed Queued Tasks before this change: `lineup add` starts the Task. No row can carry the mark.
- **Accept `ready` and `queued` in `[board]` as other names for `up_next`.** Rejected. Two names for one group stay in the file for ever. An error that names `lineup config migrate` fixes the file once.

## Consequences

- The heading reads `Up next`. An Issue row in it shows the status word `up next`, so `ready` is now only the word of Ready to land.
- The free slot toast names the first Up next row.
- The Linear state type `unstarted`, the local status `todo`, and a GitHub Issue without the backlog label put an Issue in Up next, as they put it in Ready before.
- `[board]` takes `up_next` in `order`, `collapsed`, and `hidden`. A list that names `ready` or `queued` is an error that names `lineup config migrate`. The Board then uses the default layout, as for any bad `[board]` table, and the Scheduler still starts.
- `lineup config migrate` writes one `up_next` at the place of the first `ready` or `queued` in each list, and it removes the other. The user's order stays.
