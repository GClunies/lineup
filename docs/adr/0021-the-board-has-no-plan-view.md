# The Board has no plan view

[ADR 0023](0023-the-board-has-no-review-view.md) also removes the review view, so the Board shows the work view only.

The Board had three views. `1 plan` was the tracker browser, with the user's favorite Linear views, Browse, the Local tab, and one tab for each GitHub Issue list. `2 work` held the user's own Issues and build Tasks. `3 review` held the review Tasks. The plan view showed the work of the whole team, but lineup orchestrates the user's own work. Two of its actions serve the user's own Issues. One shows the description, and one changes the status. We removed the plan view and moved these two actions to the work view. The Board now has two views, `1 work` and `2 review`. An Issue row on the work view takes `f` to show the Issue's description and `c` to change its status. `/` filters the rows of the work view.

## Considered Options

- **Keep a team browser as `1 plan`.** Rejected. Linear and GitHub already show the team's work. The browser files held 2,243 of the 5,293 lines of code in `internal/board`. The browser also kept a second way to start an Issue.
- **Keep only the GitHub Issue tabs.** Rejected. These tabs listed other people's Issues too. The user starts a GitHub Issue with `lineup add owner/repo#12` or through the Orchestrator.

## Consequences

- `1` shows the work view, `2` shows the review view, and `3` does nothing.
- One table, `issueKinds` in `internal/board/issuekind.go`, decides the keys of each Kind of Issue row.
- The GitHub Tracker stays. `lineup login github`, `lineup add owner/repo#12`, the Tracker mux, and the lifecycle labels do not change. The work view shows no GitHub Issue.
- `A`, which assigned an Issue to the user, is gone. The work view shows only the Issues that are already the user's.
- lineup no longer reads `views.json`, the rank of the plan view's tabs. The file stays on disk.
- `local_tracker`, as in [ADR 0011](0011-trackers-behind-one-port.md), now decides only what `lineup trackers` names.
