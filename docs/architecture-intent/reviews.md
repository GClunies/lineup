# Reviews Architecture Intent

This file states how review queues and review Tasks are built.

## ARCH-REVIEW-001: A review is a Task Kind

A review of another person's PR is a Task of Kind `review` inside the Scheduler. It has its own Stages, Reviewing and Review ready, its own queue slots, and its own poll by PR number. It uses the tab, the Worker, the recovery, and the Needs you group of every Task. Its Stages carry none of the GitHub effects of a build Task, and lineup makes no GitHub write for it.

Because: a review needs what a Task already has, and the Kind keeps the build effects, which come from Stages, away from it.
Not: a review queue outside the Scheduler, which builds tabs, Workers, recovery, and alerts a second time.

## ARCH-REVIEW-002: One shared count caps the started reviews

`review.slots` caps the started review Tasks that the queues added, across all queues. Each queue's own `slots` caps its own started reviews. Its default is `review.slots`, else 3. Without `review.slots`, only the cap of each queue applies. The fill plan and the reducer both check the two counts, so no add can pass them. A review that the user starts with `lineup review` has no queue and never counts.

Because: the user reviews a limited number of PRs at once, and with a cap for each queue only, the user must add up the caps.
Not: a cap for each queue only; a start of a teammate's PR past the shared count.

## ARCH-REVIEW-003: A fill ranks, adds, and starts in one loop call

Each full poll fills the queues, unless the user paused reviews. Only one fill runs at a time, and a fill that is due while another runs is skipped. Off the loop, the fill searches each queue in its own GraphQL call, all at the same time. A failed search fails the fill, and the fill starts nothing. Then one loop call ranks each queue, drops skips and owned PRs, checks the counts, and adds and starts each review.

Because: one loop call makes each fill idempotent, so a second fill in the same state starts nothing.

## ARCH-REVIEW-004: A fill starts at most one review in each Repo

Each fill starts at most one review Task in each Repo. The start holds a lock for the Repo while it fetches the PR into the main checkout and adds the review worktree. If git reports `cannot lock ref`, lineup tries the fetch again after 0.5 s, 1 s, and 2 s. A step that still fails gives the flag `setup failed`.

Because: each start fetches into the one main checkout of the Repo, and another fetch, for example from the user's editor, can hold a ref lock there.

## ARCH-REVIEW-005: Queue searches use the ISSUE search type

lineup builds each queue search for GitHub's `search(type: ISSUE)`. A repeated `repo:` or `author:` qualifier gives OR, and `label:a,b` gives OR across labels. The members of each `author_team` join the `author:` list. GitHub search has no regex, so lineup drops the PRs whose titles fail `include_regex` or `exclude_regex` after the search. Each search sorts oldest first, so the oldest PRs fall inside the first 100.

Because: in probes, `ISSUE_ADVANCED` made a repeated `author:` an AND, and boolean `OR` and parentheses returned nothing in `ISSUE`.
Not: the `ISSUE_ADVANCED` search type.

## ARCH-REVIEW-006: The findings file is the result of the review contract

The contract of Reviewing is met when the `pr-review` record folder of the PR holds a findings file. That file is the one of the head that the Task pinned at its add, or any findings file written since Reviewing started. The Scheduler checks when the Worker reports `done` and when a Reviewing Worker asks a question. The file name gives the head that the review read. lineup sets `XDG_STATE_HOME` in the Worker's environment, so the Worker and the Scheduler use one folder.

Because: the first case covers a review that reuses the findings of the pinned head.
