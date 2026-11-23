# lineup closes a merged tab when the user turns it on

This ADR amends [ADR 0017](0017-the-user-closes-every-task-tab.md). [ADR 0024](0024-every-review-end-closes-the-review-tab.md) does not change: a review Task still closes its tab at each end.

With `close_tab = true` in the `[merged]` table of the config file, lineup closes the tab of a build Task whose PR merged. It waits until the Worker is idle. It keeps the Task's worktree for `keep_worktree_days` days, 7 by default and 30 at most, and then removes it. While the worktree exists, `lineup open` restores the Worker in it. After lineup removes the worktree, `lineup open` resumes the Worker's conversation in the Repo's main checkout, with no tool that writes files. The option is off by default. Then a merged Task keeps its tab and its worktree until the user closes the tab, as ADR 0017 says. We chose this because merged Tasks piled up tabs and worktrees until the user closed each one by hand. The user still wants to come back to a merged Task's Worker for some days, for example to ask it one more question.

## Considered Options

- **Close the tab at Done, as before ADR 0017.** Rejected again. A merge then closes a tab while its Worker runs, before the user reads its output. ADR 0017 replaced this rule for that reason.
- **Close the tab after a fixed delay.** Rejected again. A delay is still a guess at when the user is done with the tab. The chosen rule is different from the delay that ADR 0017 rejected in three ways. The user turns it on in the config. lineup waits for an idle Worker, not for a time. The worktree and the conversation stay, so `lineup open` brings back the same Worker.
- **Plan mode for the read-only Worker.** Rejected. A probe with `claude -p --permission-mode plan` wrote no file, but only because the model declined. Plan mode is not a hard block, and `ExitPlanMode` lifts it in an interactive session. The user's settings have `permissions.defaultMode = "auto"`, so a resumed Worker with no limit writes files with no prompt.
- **A deny list for the read-only Worker.** Chosen. The Worker starts with `--disallowedTools=Bash,Edit,Write,NotebookEdit,EnterWorktree`. A probe with `claude -p --disallowedTools=Edit,Write,NotebookEdit,Bash` had no Bash, Monitor, or Edit tool, and wrote no file. A subagent that it started had no write tool either.
- **Resume in a folder other than the main checkout.** Rejected. `claude --resume` resumes a conversation from any folder, also after its first folder is gone. An empty folder gives the Worker no code to read when it answers a question. A new worktree costs a worktrunk setup, and lineup must remove it again. The deny list already keeps the main checkout safe.

## Consequences

- The config has a new top-level table, `[merged]`, with `close_tab` and `keep_worktree_days`. `close_tab` is `false` by default, so nothing changes until the user turns it on.
- `keep_worktree_days` goes from 0 to 30. Claude deletes a conversation after `cleanupPeriodDays`, 30 days by default, so a longer keep leaves a worktree with no conversation to resume. The default of 7 equals the Board's age-off for a Done Task.
- The Worker is busy while it works, waits for the user, reports an error, or has not reported a status yet. The tab then stays until the Worker reports `idle` or `done`. A Task whose Worker exited closes its tab at the next check.
- The Scheduler checks at each poll and once at its start. A keep that ended while the Scheduler was stopped removes the worktree at the next start.
- lineup never removes a worktree that has uncommitted changes. It checks with `git status --porcelain` first, because `wt remove` without `-f` fails on such a worktree. The Board note reads `worktree kept: uncommitted changes in <path>`, and lineup tries again at each poll. The rule also covers a tab that the user closes. Before this ADR, that failed removal only wrote a log line.
- The user's own ends keep the rule of ADR 0017. The tab's close in Rex, `x`, `lineup close`, and `lineup archive` remove the worktree at once. `lineup archive` now also removes the worktree of a Done build Task that has no tab.
- If the tab of a Task that waits to close is gone at the Scheduler's start, lineup keeps the worktree for the full `keep_worktree_days`. The user did not close that tab, so lineup does not remove the worktree at once.
- A Done build Task stays on the Board while it keeps its worktree, with the note `worktree until <date>`, for example `worktree until Jan 2`. `lineup add` refuses its ID with `<task> is Done and keeps its worktree: remove it first with lineup close <task>`.
- `lineup open` never starts a fresh Worker at Done. If Claude no longer holds the conversation, it refuses with `<task> is Done and Claude no longer holds its conversation`.
- The read-only Worker has no side pane, because the side pane shows the worktree's changes. It cannot run `lineup ask`. Its system prompt says that lineup removed the worktree and that the Worker answers the user's questions. The Worker starts with `--system-prompt-snapshot off`, because a resume otherwise sends the system prompt that the conversation recorded first, without the added text.
- A tab that `lineup open` restores at Done stays until the user closes it. That close removes what is left at once, as ADR 0017 says.
