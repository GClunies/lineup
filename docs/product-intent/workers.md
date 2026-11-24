# Workers Product Intent

This document states the current intended experience for how the Worker of a Task runs, speaks in the user's name, and comes back.

## PROD-WORKER-001: One Worker serves a Task for its whole life

Each Task has one Worker, an interactive `claude` in one tab, from its first Stage to Done. When the next agent Stage starts, for example CI debug, Responding, or a rerun, lineup types that Stage's prompt into the same Worker, in the same tab and worktree. No tab closes and no new Worker starts at a Stage handoff. So the Worker keeps the context of the earlier Stages, and the user keeps the tab and its output.

## PROD-WORKER-002: A Worker keeps the model of its first Stage

A Worker keeps the model and the effort of the Stage that it starts in: Working for a build Task, Reviewing for a review Task. The config file sets them in `[stage.working]` or `[stage.reviewing]`, else in `[stage.default]`. lineup rejects `model` and `effort` under every other `[stage.<key>]` table, with an error that names the key. `skill` and `command` apply to each Stage, because each typed prompt carries them. `lineup set <task> model=<model>` picks the model of the Task's next Worker, for example the Worker that a restore opens.

## PROD-WORKER-003: A Worker posts to a person only to report a fix

A Worker posts a reply to a person only when the reply reports a fix: the requested change, in a commit that the Worker pushed. It replies in the person's review thread, or in the PR conversation for Feedback. Feedback that asks for nothing, for example thanks, needs no reply. Every other reply to a person is a Draft reply, for example a deferral, a rebuttal, a disagreement, an answer to a question, or a reply that the Worker cannot sort. The rule holds for an approval, a comment, and a request for changes alike.

## PROD-WORKER-004: The user decides each Draft reply in the tab of the Worker

A Worker with Draft replies pushes and posts nothing, and the Task shows `drafts` in Needs you with their count. In its tab, the Worker shows each draft with the reviewer, the comment and its place, the review state, and the reply. Drafts for a request for changes come first. The user posts each draft as written, posts it with an edit, or asks for the fix instead. The flag stays, with the count that is left, until the user decides each draft. Then the Worker pushes, posts each reply, and resolves each thread that it answered.

## PROD-WORKER-005: A reply to a review bot needs no approval

A Worker answers a review thread that a review bot opened with no Draft reply. It posts its reply and resolves the thread by itself, because a reply to a bot speaks to no colleague. A Responding round that the review bot starts, a review cycle, holds no Draft reply and does not wait for the user.

## PROD-WORKER-006: A deferral becomes a draft ticket

When a Worker wants to defer a person's comment to a follow-up, it drafts the follow-up ticket in the Task's Tracker with no Project, beside the Draft reply that names the deferral. The user picks the Project when the user approves the draft.

## PROD-WORKER-007: A change outside the scope of the Task goes to the user first

In CI debug and Responding, a Worker checks each change against the intent of the PR and the scope of the Task. The check covers each change that a CI fix needs, and each suggestion from a person or a review bot. If a change goes against that intent or outside that scope, or the Worker is not sure, the Worker pushes and posts nothing yet. It runs `lineup ask <task> "<question>"` to name the change, who asked for it, and why it is outside the scope. The Task shows `question` in Needs you until the user answers in the Worker's tab.

## PROD-WORKER-008: The user can steer a running Worker

The user can talk to a Worker at any time. The user types in the Worker's tab, or runs `lineup steer <task> "<text>"`, which types the text into the Worker and records it. The Orchestrator passes the user's words to a Worker with `lineup steer`, unchanged, and the palette action `Tell a Worker…` does the same. A steer needs a live Worker. lineup types a Stage prompt only when the Worker is idle or done, so a prompt never interrupts a message that the user typed. The prompt waits for the Worker's next idle.

## PROD-WORKER-009: A restore brings back the same Worker

`lineup open <task>` and `enter` on the Task's row bring back the Task's own Worker. A live tab comes to the front. Otherwise lineup opens a new tab that resumes the Worker's conversation in its folder, with a new side pane, and the `tab closed` flag clears. A Stage prompt that waited reaches the restored Worker at its first idle, and the restore types nothing else. A tab where the Worker exited closes, and the new tab takes its place. A Done build Task with a kept worktree restores in that worktree. A Done review Task with no tab refuses with `<task> is Done and its worktree is gone`.

## PROD-WORKER-010: A Task with no conversation gets a fresh Worker that knows its steers

If a Task has no conversation, or Claude deleted it, `lineup open` starts a fresh Worker with the prompt of the current Stage. The prompt says that this is a restart and quotes each steer that the user sent to the Task, in order. A review Task in Review ready reviews the PR again. `lineup open` refuses a Task in a Stage that waits for others, for example CI running, when no conversation is left. A Done Task never gets a fresh Worker: `lineup open` refuses with `<task> is Done and Claude no longer holds its conversation`.

## PROD-WORKER-011: The Worker of a Done Task comes back read-only after its worktree is gone

After lineup removes the worktree of a Done build Task, `lineup open` and `enter` resume the Task's Worker in the main checkout of the Repo. The Worker has no tool that writes files, so it cannot change the main checkout or run `lineup ask`. It is told that lineup removed the worktree and that it answers the user's questions. Its tab has no side pane. The user can come back this way for as long as Claude keeps the conversation.

## PROD-WORKER-012: Each Worker tab shows the change beside the Worker

Each Worker tab holds a side pane to the right of the Worker, in the Task's own worktree. The pane opens half of the tab wide, and the Worker keeps the focus. A build Task's pane shows the worktree's changes since the branch left the default branch, and updates live. After a rebase or a merge of the default branch, the pane shows the changes since the new base within one poll. A review Task's pane shows the PR's GitHub page in RexWeb, else the PR's diff in `hunk`. A close of the pane alone changes nothing on the Task.

## PROD-WORKER-013: lineup shows what each Worker spent

lineup shows what the Worker of each Task spent: input, output, cache write, and cache read tokens, with its subagents, and agent time. Agent time counts only the turns in which the Worker worked, so a wait for the user, CI, or a reviewer adds nothing. The detail pane (`f`) and `lineup show` print a `tokens` line and an `agent time` line, with large counts in three digits and `k` or `M`. `lineup ls --json` and `lineup show --json` give each Task a `usage` object. lineup shows usage that it cannot read as unknown, never as zero.

## PROD-WORKER-014: The name of each Worker names its Task

lineup names each Worker after its tab label, which starts with the Task ID, or with `<repo>#<n>` for a GitHub Issue. So Claude's prompt box, its resume picker, and the terminal title show which Task each Worker works on.

## PROD-WORKER-015: A Worker tab never holds a second side pane

lineup opens a Worker's side pane at each start and restore of the Worker. At its own start, lineup finds the side panes in each live Worker's tab, keeps one, and closes the others. A plain split where the user started `hunk` counts as a side pane too, and so does each rexweb pane, whatever page it shows. lineup replaces the kept pane when it shows another worktree or base, and opens one when none is left. When the Task's worktree is gone, its pane closes. A failed split leaves the Worker without a pane.
