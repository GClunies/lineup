# The Board reports Needs you to Rex

[ADR 0030](0030-the-orchestrator-gets-no-alerts.md) removes `lineup watch`. The Board's alerts do not change.

The Board reports the build Tasks in Needs you as one Rex program status record. The record uses OSC 7501 and sits on the Board's pane. While a build Task is in Needs you, the record is `blocked`. Its message names the count, for example `2 Tasks need you`. When no build Task is in Needs you, the Board clears the record. The record replaces the Board's toast for a Task in Needs you. A Task that reaches Done and the free slot hint still toast. The Board reports at start. It reports again each time the set of those Tasks changes. A Task that changes its Stage or its flag counts as a change, so it sends a new report at the same count. We chose this because a toast shows once and then disappears. Rex keeps a record on the tab until the Board replaces or clears it. The protocol is at https://www.superlogical.com/rex/docs/build/program-status.

## Considered Options

- **A record for each Task, with the Task ID as the record id.** Rejected. A Task ID such as `owner/repo#12` is outside the OSC 7501 id grammar, which allows only letters, digits, and `_.+-` in each segment. A Rex server at API revision 18 dropped such ids in a probe. Also, Rex shows one mark for each tab, so a record for each Task still gives one mark for the whole tab.
- **Keep the toast beside the record.** Rejected. Rex for Mac also notifies for a status while Rex is in the background. The user gets two notifications for one event.
- **Report from the Scheduler.** Rejected. A record belongs to a terminal, and the Scheduler runs under launchd with no terminal ([ADR 0008](0008-scheduler-runs-as-a-launchd-agent.md)).

## Consequences

- A Board that quits drops the record, because Rex drops `blocked` records when the process exits. The Board does not clear the record when it quits.
- The Dock badge counts the `lineup` tab once, whatever the number of Tasks.
- `lineup watch` and `scheduler.Alerts` do not change. The Orchestrator still reads the alerts of the build Tasks in Needs you.
- A review Task gives no alert ([ADR 0025](0025-review-tasks-give-no-alert.md)), and the record does not count it. The header still shows it as `(+N in reviews)`.
- The clear has no id, so it removes every record on the Board's pane. The Board is the only program that runs in that pane.
- At start the Board reports the view that it paints first, which can be the cached view. A restart sends that count again. A live view that differs sends a second report.
