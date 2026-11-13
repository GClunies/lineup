# The Board has no review view

The Board had two views. `1 work` held the user's own Issues and build Tasks. `2 review` held the review Tasks on its Reviews tab, and PR lists on its other tabs: `My PRs`, `Requested`, one tab for each `[[review.views]]` table, and one `Q:` tab for each queue. Each review Task runs in its own Rex tab in the `reviews` session, and the user's review closes that tab ([ADR 0022](0022-the-users-review-closes-the-review-tab.md)). The user wants a review queue that fills itself and nothing more. So the review view repeated what Rex and GitHub already show. We removed the review view. The Board now shows the work view only. Its header keeps the count of the review Tasks that need the user, `(+N in reviews)`, and `reviews paused` while the queues are paused.

## Considered Options

- **Keep only the Reviews tab.** Rejected. The `reviews` session in Rex already holds one tab for each started review Task.
- **Keep the PR tabs as a PR browser.** Rejected. GitHub already lists the user's PRs and review requests. The PR tabs searched GitHub every 2 minutes while the Board was open. The review view and its tests held 2,034 of the 7,816 lines in `internal/board`.

## Consequences

- The keys `1` and `2` do nothing, and the header names no view.
- `groups.json` keeps the closed groups of the build Tasks only. The Board ignores a `review` entry in the file.
- `[[review.views]]` is gone. A config that holds a `[[review.views]]` table fails at load and names the table to delete. `[[review.queues]]` does not change.
- The Scheduler's queue fill, `lineup review`, and `lineup reviews` do not change. The Orchestrator skips and unskips a PR with `lineup reviews skip` and `lineup reviews unskip`.
- lineup no longer reads `review_views.json`, the rank of the review view's tabs, or the PR lists in `cache/github`. The files stay on disk.
