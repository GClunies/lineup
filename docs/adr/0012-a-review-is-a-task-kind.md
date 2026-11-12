# A review is a Task Kind with its own Stages

A review of someone else's PR is a Task with Kind `review`. It has its own Stages, Reviewing and Review ready, its own slots for each queue, and its own poll by PR number. We chose this because a review needs what a Task already has: a tab, a Worker, recovery, the Needs you group, alerts, and `enter` to focus it. A review also must never get the GitHub effects of a build Task, and those effects come from Stages, so the Kind keeps the two sets of Stages apart.

## Considered Options

- **An ad hoc Task with `skill=pr-review` and `pr=false`.** Rejected: the Task never learns the PR it reviews, so it never ends when the PR merges or the user posts the review. It ends with the transient flag `stopped`, so recovery runs a finished review again.
- **A build Task whose branch is the PR's head branch.** Rejected: the branch poll treats the other author's PR as the Task's own. lineup would mark it ready for review, start CI debug Workers on it, and offer to merge it.
- **A separate review queue outside the Scheduler**, as the Herdr `review-queue` plugin is. Rejected: it builds tabs, Workers, recovery, and alerts a second time, and its reviews do not show on the Board or reach the Orchestrator.

## Consequences

- `Started()` counts only build Tasks, so reviews never fill the WIP cap. Each queue's `slots` caps its own started reviews in the reducer.
- Review ready is a user-owned Stage. Recovery flags only agent Stages, so a finished review does not run again.
- lineup makes no worktree for a review. `pr-review` makes and removes its own review worktree from the Repo's main checkout.
