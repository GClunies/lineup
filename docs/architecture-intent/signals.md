# Signals Architecture Intent

This file states how lineup reads Rex and GitHub, and turns what it reads into Signals.

## ARCH-SIGNAL-001: Rex events map to Worker Signals

The Scheduler reads `rex events --all --json` and finds the Task of each event by its block ID. `program_status_changed` gives the status of the Worker: `working`, `blocked`, `idle`, `done`, or `error`. `child_exited` gives the exit code, and it comes for every exit. A `block_closed` of a Worker block gives `tab-closed`: a person or Rex closed the tab. The Scheduler keeps each event for a block that it does not know yet, because a fast program can exit before its block ID returns.

Because: Rex keeps a tab open when its program exits within about 1 s, and then sends `child_exited` and no `block_closed`.

## ARCH-SIGNAL-002: One batched GitHub query serves each poll

Every 30 s, the Scheduler reads the PRs of all started build Tasks in one batched GraphQL query through `gh`, by Repo and branch. It also polls a Task right after its Worker reports `done`, and after a merge. The GitHub adapter compares each PR with its last snapshot, turns each change into an edge Signal, and then sends one `pr-seen`. Only a full poll fills the review queues. A poll for a few Tasks fills none.

## ARCH-SIGNAL-003: The poll reads fixed windows of each PR

For each PR, the poll reads the first 100 check contexts of the head commit and the first 50 review threads. For each thread it reads the author of the first comment and of the last 5 comments. The author type of the first comment tells a bot's thread from a person's. The poll also reads the last 30 PR comments and the last 30 reviews, for Feedback. A PR with more threads than the window does not meet a bar that holds threads, because a person can have opened the unread threads.

## ARCH-SIGNAL-004: Required checks come from the rules of the base branch

The poll reads the required checks of the base branch by branch name, with no PR number. They are the union of the `requiredStatusCheckContexts` of classic branch protection and the checks of each active ruleset rule of type `REQUIRED_STATUS_CHECKS`. A required check that GitHub has not made on the head commit yet counts as pending. A head with no checks counts as due when the head commit of the base branch has checks.

Because: `refUpdateRule` does not show the checks of a ruleset, and `rules` shows them by branch name, so the first poll of a PR has them.
Not: `isRequired` on each check, which needs a PR number, fails the whole batched query on a wrong number, and cannot see a check that GitHub has not made yet.

## ARCH-SIGNAL-005: The score of the review bot comes only from its own summary comment

For a gated Task, the poll reads the first 20 PR comments. The GitHub adapter takes the first comment that a login of the bot wrote with a score and a reviewed commit. The Greptile logins are `greptile-apps` and `greptile-apps-staging`, as GraphQL spells them, with no `[bot]` suffix. The score counts only when its reviewed commit is the head of the PR. The PR reviews of the bot send no review Signal for a gated Task.

Because: Greptile keeps one summary comment that holds `<!-- greptile_confidence_score:N -->` and the last reviewed commit, and no other author can write a score there.
Not: The score from the `Greptile Review` check run, which passes at 4/5 and names no score; the score from the PR body, which the PR author can edit.

## ARCH-SIGNAL-006: Reviews poll by PR number, apart from the branch poll

The review poll reads the PR of each review Task by repository and number, in one GraphQL call for all review Tasks. It reads the state, the head, the title, and the user's own reviews with their state and time. The branch poll skips review Tasks, and a review snapshot never goes through the build Signals. A head that moves after Review ready gives the Task the note `head moved` and no Signal.

Because: lineup must never mark ready, merge, or debug CI on a PR that it reviews.

## ARCH-SIGNAL-007: The clock of the Scheduler sends review-bot-late

At each poll, after it applies the Signals of a build PR, the Scheduler sends `review-bot-late` to the Task when the Task has been in CI running for 15 minutes or more. The reducer flags the Task `no bot review` only when its first gap is a head that the review bot has not scored.

Because: Greptile's check run keeps CI pending while it reviews, and it scored each probed push within 4 minutes, so passed checks with no score after 15 minutes mean that the bot does not review.
