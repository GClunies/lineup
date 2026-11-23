# Reviews Product Intent

This document states the current intended experience for how review queues fill, and how review Tasks run and end.

## PROD-REVIEW-001: A review queue fills itself and writes nothing before the user decides

A review queue starts the reviews of the PRs that need the user, before the user arrives. Each review Task runs `/pr-review` in its own tab of the `reviews` Rex session. lineup writes nothing to GitHub for a review Task. The Worker writes to the PR only after the user decides each finding, and then it posts what the user decided.

## PROD-REVIEW-002: Queue filters pick the candidate PRs

Each `[[review.queues]]` table filters open PRs with `repo`, `author`, `author_team`, `review_requested`, `label`, `include_regex`, and `exclude_regex`. It needs at least one of the first five. The items of one list match with OR, and two keys match with AND. `author` and `author_team` form one list. `review_requested = "@me"` matches a request to the user or to a team of the user. The title patterns are case-sensitive RE2, and `exclude_regex` wins. A queue never takes a draft, an archived repository, or the user's own PR. If the author renames a PR to a title that its queue drops, its open review Task ends.

## PROD-REVIEW-003: A PR enters a queue only when it wants the user's review

A candidate PR enters a queue only when all of these hold. It is not a draft and not from a fork. Its author is neither the user nor a bot. The user did not skip it, and no review Task holds it. It requests the user again after the user's earlier review, or nobody approved it yet. No other person's review is open on it: a request for changes, or a comment on the current head with no approval. A pending review counts as no review. An approval that a comment follows still counts as an approval.

## PROD-REVIEW-004: Review slots cap the started reviews

`review.slots` caps the started review Tasks of all queues together. A queue's own `slots` caps its own started reviews, and its default is `review.slots`, else 3. Without `review.slots`, only the cap of each queue applies. A later queue with a lower `slots` keeps slots free for earlier queues. A review in Reviewing or Review ready holds its slot. A review that the user starts with `lineup review` has no queue and takes no slot. Review Tasks never count toward the WIP cap.

## PROD-REVIEW-005: The first queue in the file owns a PR

The queues fill in the order of the config file, so an earlier queue picks first. A PR that several queues match belongs to the first of them, and no later queue lists, ranks, or starts it. So each PR of an earlier queue ranks ahead of every PR of a later queue.

## PROD-REVIEW-006: Re-reviews rank first

Inside one queue, the PRs rank in this order. First come the re-reviews: the PRs that the user reviewed before and that request the user again. Then come the PRs that request the user by name. Then the oldest PR comes first.

## PROD-REVIEW-007: A full queue makes its PRs wait

A full queue makes its PRs wait, and no fill stops a started review to make room. When a slot frees, the next fill gives it to the first queue that has a candidate. If a queue leaves the config file, its started reviews go on, and it starts no more. If `slots` drops, no review stops, and the fills start nothing until the count is below it. The Scheduler reads the queues when it starts, so a queue change applies after `lineup restart`.

## PROD-REVIEW-008: The user can pause every queue

`lineup reviews pause` stops every queue fill, and `lineup reviews resume` starts the fills again. A pause stops no review that already started. The pause holds across a restart of the Scheduler. The Board's header shows `reviews paused` while the pause holds.

## PROD-REVIEW-009: A skip holds a PR out of every queue

`lineup reviews skip <PR>` holds a PR out of every queue, at every head, until `lineup reviews unskip <PR>`. The PR is a URL or `owner/repo#number`, in any case. A skip writes nothing to GitHub, refuses a PR that is not open, and never expires. It ends the PR's open review Task with the outcome skipped. A second skip and an unskip of a PR with no skip change nothing. After an unskip, the next fill can add the PR at the same head. `lineup review` still reviews a skipped PR.

## PROD-REVIEW-010: A review Task names its PR

A review Task has the ID `PR-<number>`, for example `PR-4955`. Another repository's PR can already hold that ID in a live Task, or in a Task that keeps its tab. Then the ID adds the repository name in upper case, for example `PR-4955-WEB`. A new review of a PR with a Done review Task takes the same ID. The title and the tab label are `<repo>#<number> <PR title>`. If no checkout below the roots has the PR's repository as its origin, the add fails and names the repository. A failed setup gives the flag `setup failed`.

## PROD-REVIEW-011: A review is ready when its findings exist

A review Task in Reviewing moves to Review ready when the findings file of `pr-review` exists for the PR's head. lineup checks for the file when the Worker reports done or asks a question. Without the file, a done gives the flag `stopped`, and a question gives the flag `question`. Review ready waits for the user in Needs you. A later status of the Worker never moves the Task back to Reviewing.

## PROD-REVIEW-012: Each end of a review closes its tab

A review Task ends at the user's submitted review, a merge, a close of the PR, `lineup close`, or a skip. Each end moves it to Done, closes its tab and its side pane, and removes its review worktree. A merge or a close ends the Task at once, also while the Worker works. The user's review ends it only after the Worker stops working. Each submitted review counts, and a pending review does not. lineup keeps a review worktree with an uncommitted change or an unpushed commit, and names its path in the note.

## PROD-REVIEW-013: A finished review holds its PR until the PR asks again

A review Task that the user's review ended holds its PR out of the queues until the PR requests the user again. The next fill then starts a new review Task, and `pr-review` reviews only what changed. A review Task that `lineup close` ended holds the PR until the author pushes a new head. A review Task that a skip ended holds nothing, because the skip holds the PR. The user can always start a review again with `lineup review`.

## PROD-REVIEW-014: Review Tasks give no alert and stay off the Board

A review Task gives no alert. The Board lists no review Task, makes no toast for one, and leaves it out of the Needs you count on the `my-lineup` tab. The Board's header counts the review Tasks that need the user, as `+N in reviews`. The user sees each review in its tab in the Rex sidebar, and decides each finding there.

## PROD-REVIEW-015: A lost review tab keeps the review

If the tab of a review Task closes in Reviewing, the Task gets the flag `tab closed`, and lineup opens no tab. If the tab closes or the Worker exits in Review ready, the Task stays in Review ready with no Worker. It keeps its slot and still needs the user. `lineup open <task>` or `lineup review <url>` restores the Worker in a new tab, in the same conversation, with no new slot. A restore of a Task with no conversation runs the review again. A restart of the Scheduler never runs a finished review again.

## PROD-REVIEW-016: A moved head marks the review

If the head of the PR moves while its review Task is in Review ready, the Task gets the note `head moved` and stays in Review ready.

## PROD-REVIEW-017: The user starts a review from its URL

`lineup review <PR URL>` adds and starts a review Task with no queue, and it also takes `owner/repo#number`. If the PR has an open review Task, the command adds nothing. It restores a closed Worker, runs a Task that stopped for good again, or else focuses the Task's tab. It refuses a PR from a fork, and a PR that is closed or merged.

## PROD-REVIEW-018: The user can list the queues

`lineup reviews` lists each queue with its slots, its started reviews, and its ranked PRs. Then it lists the skips, then the repositories that have no checkout, under `Unmapped:`. When `review.slots` is set, the list starts with `Reviews: <started>/<slots> started`. `lineup reviews --json` prints the same facts as JSON.

## PROD-REVIEW-019: The review summary and findings carry over to pr-explain

Each review leaves a summary of the PR and its findings for the head that it read. The summary gives the what and why, the how, and the validation. `pr-review` shows the summary before the findings. When the user asks the review Worker for `/pr-explain` at that head, the page takes the summary and the findings from the review, with no new review.
