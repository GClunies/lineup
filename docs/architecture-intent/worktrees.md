# Worktrees Architecture Intent

This file states how lineup creates and removes worktrees.

## ARCH-TREE-001: worktrunk creates and removes build worktrees

The Scheduler creates a build Task's worktree with `wt switch --create <branch>` in the setup tab, or with `wt switch` when the branch exists. The exit code of that command is the setup result. It removes the worktree with `wt remove`. worktrunk's `copy` steps and `pre-start` hooks, in the user's worktrunk config and in the Repo's, set up each worktree. lineup holds no setup config of its own, and runs the `wt` binary by its absolute path.

Because: the user already sets up worktrees with worktrunk, and two setup configs drift apart.
Not: a worktree setup config of lineup's own.

## ARCH-TREE-002: A review worktree is a detached git worktree that lineup makes

lineup makes a review Task's worktree with plain git, beside the Repo's main checkout. Under a lock for the Repo, it fetches the PR's head and its base branch into the main checkout. Then it runs `git worktree add --detach` at the head when the worktree is missing. A fetch that finds `cannot lock ref` tries again after 0.5 s, 1 s, and 2 s. lineup removes the worktree with `git worktree remove`, never with `wt`. A failed step gives the flag `setup failed`.

Because: worktrunk hooks run in the new worktree, so they would run the code of the PR.

## ARCH-TREE-003: lineup removes only a clean worktree

Before lineup removes a worktree, it runs `git status --porcelain` in it. If the output is not empty, lineup keeps the worktree and puts its path in the Task's note. For a build Task, lineup tries again at each poll. Before it removes a review worktree, lineup fetches the PR's head, and it also keeps a worktree that holds a commit on no remote. The rule holds for every removal, also after the user closes a tab.

Because: `wt remove` without `-f` fails on such a worktree with a message for people, so lineup asks git.

## ARCH-TREE-004: A kept worktree lasts at most as long as Claude keeps the conversation

`keep_worktree_days` goes from 0 to 30, and its default of 7 equals the Board's age-off for a Done Task. The days count from the tab's close, or from the merge when the Task had no tab. The Scheduler checks each kept worktree at each poll and once at its start, so a keep that ended while it was stopped ends at the next start. A Task whose tab is gone at the start keeps the full days.

Because: Claude deletes a conversation after `cleanupPeriodDays`, 30 days by default, so a longer keep leaves a worktree with no conversation to resume.

## ARCH-TREE-005: The folder of a Worker never holds the code of the PR

A review Worker runs in the Repo's main checkout, which the adapter trusts. It reads the PR's code in the review worktree through `--add-dir <review worktree>`, with no permission prompt. A restored review Worker gets the same folder and the same flag. No Worker runs with a review worktree as its folder.

Because: Claude runs the `.claude` settings and hooks of the folder that it starts in.
