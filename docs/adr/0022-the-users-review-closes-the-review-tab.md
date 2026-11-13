# The user's review closes the review tab

[ADR 0024](0024-every-review-end-closes-the-review-tab.md) extends this rule: a merge or a close of the PR also closes the review tab.

A review Task that ends with the user's review closes its tab and removes its review worktree. The review then holds its PR only until the PR requests the user again. A submitted review counts in each of its states: approve, comment, and request changes. A pending review does not count. We chose this because the user's review is the user's own end of the review, as `x` is. Before this change, each Done review kept its tab under [ADR 0017](0017-the-user-closes-every-task-tab.md). The user then closed one tab for each review that they had just finished, and the queue held the PR until they did.

## Considered Options

- **Keep the tab, as ADR 0017 says for every Task.** Replaced for this one end. ADR 0017 keeps a tab that the user can still read after a merge. After the user's review, the user has read the tab and acted on it.
- **Also close the tab when the PR merges or closes.** Rejected. Those ends come from other people. The user may not have read the findings yet.
- **Hold the PR at its head after the user's review.** Rejected. A request again with no new push, for example after the author answers a comment, then brings no new review.

## Consequences

- `pr-reviewed` on a review Task emits `Cleanup`, as `task-closed` does. `pr-merged` and `pr-closed` keep the tab, as ADR 0017 says.
- The Scheduler applies `pr-reviewed` only while the Worker does not work. So the tab closes after `pr-review` posts the review and stops.
- The queue lets the PR back only when the PR requests the user again. GitHub clears the user's own review request when the user submits a review. A probe of 15 PRs that the user reviewed found no request for the user on any of them, also where a request to the user's team stayed open.
- A Task that `x` closed still holds its PR until the author pushes a new head.
