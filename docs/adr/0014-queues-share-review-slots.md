# Queues share review slots, and the first queue owns a PR

`review.slots` caps the started review Tasks of all queues together. The queues fill in config order, and the first queue that matches a PR owns it. We chose this because the user reviews a limited number of PRs at once, and a teammate's PR must come before any other PR. With one `slots` for each queue, two queues gave twice the reviews, and a teammate PR past its queue's slots ranked by age in the second queue.

## Considered Options

- **A nested `[[review.queues.sources]]` shape**, one queue with ordered sources. Rejected: the user could not read the priority from the config. Two `[[review.queues]]` entries in order say it directly.
- **A per-queue `slots` only.** Rejected: the total is the number of queues times `slots`, and the user must do the sum.
- **Start a teammate PR past the shared count.** Rejected: it stops nothing and breaks the cap. The PR waits, and the next free slot goes to the first queue.

## Consequences

- A queue's `slots` is still a cap for that queue. Its default is `review.slots`. A later queue with a lower `slots` keeps the rest free for an earlier queue. This is the lever to reserve slots.
- A PR that two queues match is dropped from every later queue's ranked list, in the Scheduler and in the Board's queue tab. The Board uses the config order of the queues, not the order of the tabs, which the user can change.
- Rank inside a queue is unchanged: re-reviews, then PRs that request the user by name, then the oldest.
- The reducer checks the shared count in `Start`, so a Queued review Task that the user starts cannot pass it. A review with no queue never counts.
- A fill still starts at most one review in each Repo, and still searches every queue in one `SearchPRs` pass.
