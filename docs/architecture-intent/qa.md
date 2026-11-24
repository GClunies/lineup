# QA Architecture Intent

This file states how QA Tasks are built.

## ARCH-QA-001: A QA check is a Task Kind

A check of a merged change is a Task of Kind `qa` inside the Scheduler. It has its own Stages, QA waiting, Verifying, and QA failed, and its own slots. It uses the tab, the Worker, the recovery, and the Needs you group of every Task. Its only writes outside lineup are the Tracker event `qa_passed` and the fix Issue.

Because: a check needs what a Task already has, and a Kind of its own keeps the build effects and the WIP cap away from it.
Not: a Stage of the build Task after the merge, which keeps the build Task from Done, holds a WIP slot, and reuses the build Worker.

## ARCH-QA-002: A merge adds the QA Task through an Effect

When a build Task ends with the outcome merged, the reducer returns the QA start Effect. The Scheduler reads the `[qa]` table of the Repo when it runs the Effect. The Effect adds nothing when a Task already holds the QA ID, so a merge that a restart sees again adds no second QA Task.

Because: the Task's Policy is fixed at its start, and a QA setting must apply to each merge after the restart that loads it.

## ARCH-QA-003: The Scheduler's clock starts each due check

At each poll, the Scheduler sends `qa-expired` to each QA Task in QA waiting whose deadline has passed. Then it sends `qa-due` to each one whose next check has come, the earliest due first, while the QA Tasks in Verifying are fewer than `slots`. The reducer refuses `qa-due` when the slots are full. The next check and the deadline live in the Task, so a restart keeps them.

Because: a wait of hours outlives a Scheduler restart, and a level check at each poll loses no due check.

## ARCH-QA-004: A verdict is a Signal from the Worker's command

`lineup qa` sends `qa-wait`, `qa-pass`, or `qa-fail` through the socket. The reducer takes each one in every QA Stage before Done, and returns an error for one in Done. A verdict moves the Task out of Verifying, so a `done` in Verifying always means that the contract result is missing.

## ARCH-QA-005: The QA worktree is detached at the merge commit

The branch poll reads the merge commit of a merged PR, and the QA Task keeps it. The QA setup fetches the default branch into the Repo's main checkout, under the lock of the Repo, and runs `git worktree add --detach` at the merge commit. Each end of a QA Task removes that worktree with `git worktree remove`, unless it holds an uncommitted change.
