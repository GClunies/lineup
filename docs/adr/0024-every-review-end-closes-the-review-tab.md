# Every review end closes the review tab

A review Task closes its tab and removes its review worktree at each of its ends: the user's review, a merge, a close of the PR, `x`, and a skip. A merge or a close ends the Task at once, also while the Worker works, and so stops the Worker. We chose this because a review of a merged or closed PR has no use. Its Worker can post nothing that the author will act on, and the tab only waits for the user to close it. [ADR 0022](0022-the-users-review-closes-the-review-tab.md) closed the tab only at the user's review, and kept it at a merge or a close.

## Considered Options

- **Keep the tab at a merge or a close, as ADR 0022 says.** Replaced. The user read such tabs only to close them.
- **End the Task at a merge or a close only after the Worker stops.** Rejected. The Worker then goes on with a review that nobody can use, and the tab stays open until it stops.

## Consequences

- `finish` gives a review Task the `Cleanup` effect, so each end of a review Task closes its tab. A build Task's ends do not change.
- The review poll applies `pr-merged` and `pr-closed` while the Worker works. It still applies `pr-reviewed` only after the Worker stops, because `pr-review` posts the review in its Step 9.
- If the user reviewed and the PR merged while the Worker worked, the Task's outcome is `merged`.
- A Done review keeps its tab only until its Cleanup runs. Its guards stay: the queue and `lineup review` still treat such a Task as holding its PR.
