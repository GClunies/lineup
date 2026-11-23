# Landing bar Product Intent

This document states the current intended experience for what a PR must meet before its build Task is Ready to land.

## PROD-LAND-001: Ready to land means that the PR can merge now

A build Task is in Ready to land only while its PR meets the Landing bar of the Task. The PR has no merge conflict and is not a draft. Every gating check passed, and the review bot's score meets the minimum when a gate applies. Every review thread is done, no Feedback is new, and the required reviews approve. lineup measures the PR against the bar at each poll. If the PR stops meeting the bar, for example when a new thread opens, the Task leaves Ready to land.

## PROD-LAND-002: The first gap decides where the Task goes

At each poll, the first gap in a fixed order decides the Stage. A merge conflict starts a Responding round. A failed gating check starts a CI debug attempt. A pending gating check, then a draft PR, then a head commit that the review bot has not scored hold the Task in CI running. A draft PR also gets the flag `draft`. A low score starts a review cycle. An open thread, then new Feedback, starts a Responding round. A missing approval puts the Task in In review. With no gap, the Task goes to Ready to land.

## PROD-LAND-003: Gating checks come from the rules of the base branch

With `checks = "required"`, the default, the gating checks are the checks that the rules of the base branch require. These rules are the classic branch protection and each active ruleset that requires status checks. If the base branch requires no check, every check gates. With `checks = "all"`, every check gates. A required check that GitHub has not made on the head commit yet counts as pending, also at the first poll of a PR.

## PROD-LAND-004: A failed optional check never starts CI debug

A check that does not gate is optional. An optional check that failed is no gap, and it never starts CI debug. The Board note, the detail pane, and the `optional` field of `lineup ls --json` name it. The CI debug prompt names the gating checks that failed and the optional checks that failed. It tells the Worker to leave the optional checks.

## PROD-LAND-005: A PR with no checks waits only when its base branch has checks

A PR whose head commit has no checks meets the check items of the bar, so a Repo with no CI goes on to review. If the latest commit of the base branch has checks, a PR with no checks yet stays in CI running. GitHub makes the checks of a push a few seconds after the push. So a Task cannot reach Ready to land, and the user cannot merge its PR, before its CI starts.

## PROD-LAND-006: The threads setting decides when a review thread is done

The `threads` key of `[landing_bar]` decides when a review thread is done. With `replied`, the default, a thread is done when it is resolved and the user wrote one of its last 5 comments. A thread that the user opened needs only the resolve. Workers post as the user, so the reply of a Worker counts. With `resolved`, a thread is done when it is resolved. With `ignored`, threads never gate.

## PROD-LAND-007: Threads past the read window keep the bar open

The poll reads the first 50 review threads of a PR. While the bar holds threads, a PR with more threads does not meet the bar, and the Board shows `more threads than lineup reads`. The unread threads start a Responding round, not a review cycle, because people can have opened them.

## PROD-LAND-008: The approvals setting decides which reviews must approve

The `approvals` key of `[landing_bar]` decides which reviews must approve. With `required`, the default, the reviews that the base branch requires must approve. GitHub's review decision then must not be review required or changes requested. With `requested`, the PR also must have no open review request, unless GitHub's decision is approved. Until the reviews approve, the Task waits in In review, and the Board shows `needs approval` or `changes requested`.

## PROD-LAND-009: Feedback is a comment by a person outside a thread

Feedback is a comment that a person other than the user wrote on a PR outside a review thread. It is a comment in the PR conversation, or a review with a body or a request for changes. A comment by a bot account, a hidden comment, and a pending or dismissed review are not Feedback. The user can hide a comment on GitHub to drop it before a round takes it. The poll reads the last 30 PR comments and the last 30 reviews. With `threads = "ignored"`, Feedback never gates.

## PROD-LAND-010: Each Responding round takes the Feedback before it

Each Responding round for people takes the Feedback that the first poll after its start sees. Only Feedback after that poll is new, so a comment that comes while the Worker works starts the next round. The Responding prompt counts the open threads and the new Feedback. A new review by a person in In review or Ready to land starts a round at once. A commit by another author in In review does too. A review cycle takes no Feedback, so people's Feedback starts its own round after the cycle.

## PROD-LAND-011: A spent budget holds new Feedback until the user acts

If the Responding budget is spent, new Feedback keeps the flag `budget spent`. A comment has no resolve, so this gap stays until the user acts. The user runs the Stage again with `lineup rerun`, hides the comment on GitHub, or closes the Task.

## PROD-LAND-012: A Repo can gate on the score of the review bot

A `[review_bot]` table in the table of a Repo, or of an owner, adds the review bot's score to the Landing bar. Its keys are `name`, whose only value is `greptile`, `min_review_score`, from 1 to 5 with the default 5, and `max_review_cycles`, with the default 3. A higher level can set one key and keep the others. Only the summary comment of Greptile's GitHub App gives a score, and only for the head commit. A PR whose gating checks passed stays in CI running, as `waiting for Greptile`, until the bot scores its head commit.

## PROD-LAND-013: Bot rounds spend review cycles, not Responding rounds

A score below the minimum starts a review cycle. Its prompt names the score and the minimum, and it tells the Worker to address Greptile's unresolved comments and summary suggestions. Open threads that only bots opened, on a score that passes, also start a review cycle. People's reviews, threads that a person opened, new Feedback, and a merge conflict spend Responding rounds. With no gate, bot threads spend Responding rounds. With no review cycle left, the Task gets `budget spent`, for example `Greptile 4/5 after 5 cycles`. Greptile's own PR reviews start no round.

## PROD-LAND-014: A review bot that gives no score flags the Task after 15 minutes

A gated Task can wait in CI running with passed checks and no score for its head commit. After 15 minutes, the Task gets the flag `no bot review`. The detail names the commit, for example `Greptile did not score 3591eba`. So a Repo where the bot does not review never holds a Task forever. The flag clears when the bot scores the head commit.

## PROD-LAND-015: A Task keeps the bar and the gate that it had at Intake

A Task keeps the Landing bar and the review bot gate that it had at Intake. A change to the config file reaches only the Tasks that come on the Board after the change.

## PROD-LAND-016: The bar of a Task prints as a checklist

After a poll measures the PR, the detail pane and `lineup show` print the Landing bar of the Task as a checklist. Each line is one item: `✓` for an item that the PR meets, and `✗` for a gap, for example `✗ 1 open thread (1 bot)`. A last line with `!` names the optional checks that failed. The list shows only what the bar holds, so it has no score line without a gate. A conflict and a draft show only when the PR has them.
