# Lifecycle Product Intent

This document states the current intended experience for how a build Task moves from its start to Done.

## PROD-LIFE-001: One add starts a Task

`lineup add <ISSUE>` adds a Task for an Issue and starts it in Working. `lineup add --repo <repo> "<text>"` does the same for an ad hoc Task, and `s` on an Issue row of the Board runs the same add. `--set key=value` sets a Task override before the Worker's first prompt. A Task exists only once its Worker starts, so lineup has no command that starts a Task later. An Issue that has not started is an Issue row, not a Task. The add runs in the background, and the Board shows the Task each time it changes Stage.

## PROD-LIFE-002: The build Stages and their owners

A build Task moves through seven Stages, and each Stage has one owner. The agent owns Working, CI debug, and Responding, and the Board shows them in the Working group. CI owns CI running, and other reviewers own In review. The Board shows these two Stages in the Waiting group. The user owns Ready to land, which shows in the Needs you group. Done has no owner and shows in the Done group. A Task has no Stage before Working.

## PROD-LIFE-003: The WIP cap holds unless the user says yes

The WIP cap counts the started build Tasks, from Working through Ready to land. Done Tasks and review Tasks do not count. If the cap is full, `lineup add` adds nothing and fails with `wip cap full`, and the Issue keeps its row. The Board then asks the user to start the Task once over the cap with `o`, or to raise the cap by one with `c`. `--over-cap` starts the Task once over the cap, and only the user's yes allows it. The Board header counts the Tasks over the cap, for example `+1 over`.

## PROD-LIFE-004: Each agent Stage leaves a fixed result

Each agent Stage prompt ends with a Stage contract: the result that the Worker must leave, never how to do the work. Working must push the branch and open a PR that is ready for review, not a draft. CI debug must push a commit that targets the gating checks that failed. Responding must push the changes that the threads ask for, and a merge of the base branch that resolves a conflict. With `threads = "replied"`, it also replies to and resolves each open thread. With `resolved`, it resolves each thread. With `ignored`, it replies to each thread.

## PROD-LIFE-005: Configuration can add a skill or a command to a Stage

A `[stage.<key>]` table in the config file can set a `skill` and a `command` for one Stage, and `[stage.default]` sets them for each Stage. A `[stage.<key>]` table at any level beats `[stage.default]` at every level. The skill becomes the first slash command of the Stage prompt. The prompt tells the Worker to run the command first. No Stage has a skill or a command by default. Each Stage prompt carries its own skill and command, so they apply to each Stage of the one Worker.

## PROD-LIFE-006: The Worker owns planning

lineup has no planning Stage and no plan approval. A started Task goes straight to Working. If the work needs a plan or the user's approval first, the chosen skill or the user's words ask for it. The Worker holds that dialogue with the user in its own tab. The Board has no plan pane and no keys that approve or return a plan.

## PROD-LIFE-007: A Task without a PR ends as stopped

The Task override `pr=false` is for work that ends without a pull request, for example a one-off run of `/pr-explain <url>`. Its Working contract tells the Worker to push no branch and open no PR, to say what it produced, and then to stop. The Task then gets the Needs you flag `stopped`, and the user closes it.

## PROD-LIFE-008: The user merges each PR

lineup merges a PR only when the user asks, with `m` on the Board or with `lineup merge <task>`. The merge is open only to a Task in Ready to land. To merge each PR when its checks pass, the user turns on auto-merge in the forge, for example with `gh pr merge --auto` on GitHub.

## PROD-LIFE-009: lineup never changes the draft state of a PR

lineup never marks a PR as a draft or as ready for review. The Worker opens each PR ready for review, so GitHub requests the CODEOWNERS reviewers when the PR opens. A draft PR means that a person holds it, for example the user. When a draft PR has no earlier gap of its Landing bar, the Task stays in CI running with the Needs you flag `draft`. When the user marks the PR ready, the next poll sees the change and moves the Task on.

## PROD-LIFE-010: A build Task ends at a merge or at the user's close

A build Task goes to Done in two ways. A merge marks it merged. The merge can be the user's `m` in Ready to land, or a merge outside lineup in any Stage, for example by GitHub auto-merge. The user's close marks it closed: `x` with a confirm, or `lineup close`, in any Stage. A close before Done also closes the tab of the Task and removes its worktree. No other event ends a build Task.

## PROD-LIFE-011: A closed PR flags the Task and keeps its work

If the PR of a build Task closes without a merge, the Task keeps its Stage, tab, and worktree, because the work is the user's. The Task gets the Needs you flag `pr closed`, with a detail such as `#5149 closed without a merge`. If the user reopens the PR, the next poll clears the flag and moves the Task where the facts of the PR lead. Else the user closes the Task with `x`. A Task with another flag gets `pr closed` when that flag clears. After a restart, the first poll also flags a Task whose PR is closed.

## PROD-LIFE-012: Needs you holds every Task that waits for the user

A started build Task with a Needs you flag shows in the Needs you group, whatever its Stage is. A Task in Ready to land shows there too, because it waits for the user's merge. Each flag names one reason, and it can carry a detail, for example `2 replies to approve`. A Done Task shows only in the Done group.

## PROD-LIFE-013: Each Needs you reason has one source

Each Needs you reason has one source. `question`, `permission`, and `auth`: a Worker that is blocked on that kind of request, or `lineup ask` for a `question`. `error`: a Worker error, or a Worker exit with a code other than 0. `setup failed`: a failed worktree setup. `stopped`: a `done` without the contract result. `budget spent`: a spent Loop budget. `draft`: a draft PR. `no bot review`: a review bot that did not score a passing PR. `pr closed`: a PR that closed without a merge. `drafts`: `lineup ask --drafts`. `tab closed`: a lost Worker tab. `worker silent`: a Stage prompt that waits too long.

## PROD-LIFE-014: A flag clears with its cause

lineup removes a flag when its cause clears. `question`, `drafts`, `permission`, `auth`, `error`, `stopped`, and `worker silent` clear when the Worker reports `working` again, for example after the user replies in its tab. `draft` clears when the user marks the PR ready. `no bot review` clears when the review bot scores the head commit. `pr closed` clears when the PR reopens. `tab closed` clears only when the user restores the Worker, for example with `lineup open <task>`.

## PROD-LIFE-015: A closed tab replaces the Worker's own flags

If the tab of a Worker goes away in an agent Stage, the Task gets the flag `tab closed`, and lineup opens no tab. This flag replaces each flag that the state of the Worker set: `question`, `drafts`, `permission`, `auth`, `error`, `stopped`, and `worker silent`. Every other flag stays, because it holds with no Worker. A tab that closes in a Stage that waits for others, for example CI running, sets no flag. The next agent Stage sets it when its prompt waits, with a detail such as `CI debug waits for the Worker`. A Task with no tab never shows `worker silent`.

## PROD-LIFE-016: A missing contract result stops the Task

If a Worker reports `done` and its Stage contract result is missing, the Task gets the Needs you flag `stopped`. A Worker often stops to show a plan or to ask a question. lineup does not run the Stage again. The flag clears when the Worker starts working again, for example after the user replies. If a stopped Working Worker opens its PR later, the Task goes on to CI running. A `done` keeps a `question`, `drafts`, or `tab closed` flag that the Task already has.

## PROD-LIFE-017: A Responding round with no push can meet its contract

A Responding round that pushes nothing can still meet its contract. It meets it when the latest poll saw every review thread done, no new Feedback, no conflict, and no review bot score below the minimum. The Task then goes to CI running, and the poll settles it on its Landing bar. For example, the user decides each Draft reply, and the Worker posts the replies and resolves the threads with no push. With `threads = "ignored"`, a round with no push gets the flag `stopped`.

## PROD-LIFE-018: Loop budgets cap the automatic rounds

Three Loop budgets cap the automatic rounds of a build Task. A Task gets 3 CI debug attempts by default. It gets 3 Responding rounds by default, and this budget fills again each time the Task reaches Ready to land. It gets 3 review cycles by default, only with a review bot gate. The `ci_debug` and `responding` keys of `[budgets]` set the first two budgets. The `max_review_cycles` key of `[review_bot]` sets the review cycles. A Task keeps the budgets that it had at its start.

## PROD-LIFE-019: A spent budget stops the loop and names the gap

If a gap of the Landing bar finds its Loop budget spent, the Task gets the flag `budget spent`, and lineup starts no Worker. The detail names the gap and the budget, for example `2 open threads (1 bot) after 3 rounds` or `test failed after 3 attempts`. The flag clears at the next poll after the gap closes, and the Task goes where its new first gap leads. A `budget spent` flag that a new review or a commit by another author set stays until the user acts, for example with `lineup rerun`.

## PROD-LIFE-020: A silent Worker gets a flag after one minute

If a Stage prompt waits 1 minute for a Worker that has sent no program status, the Task gets the flag `worker silent`. The Task gets the same flag when a Worker that received a typed prompt does not report `working` within 1 minute. lineup then types nothing more into that Worker. The flag clears when the Worker reports `working`. A Task that already has a flag keeps that flag.

## PROD-LIFE-021: A new Task ignores the PR of an earlier Task

A build Task reads no PR that merged or closed before the Task started. So a new add of an Issue whose earlier PR merged starts new work in Working. The Task moves on when the Worker opens a new PR from the branch.
