# Tabs and worktrees Product Intent

This document states the current intended experience for where Tasks live in Rex, and when their tabs and worktrees go away.

## PROD-TABS-001: The Hub is one tab with the Board and the Orchestrator

The Hub is the Rex session `my-lineup`, with one tab, `lineup`. The tab holds the Board and the Orchestrator's `LINEUP` pane, which has its own background color. By default, the `LINEUP` pane takes the left half of the tab, and the Board takes the right half. `lineup` opens the Hub and creates the session when it is missing. `lineup` also brings a Hub of an older form to this layout and keeps the Orchestrator's conversation. The Scheduler has no tab.

## PROD-TABS-002: Each Repo has its own Rex session

Each Repo with an open build Task has its own Rex session, named by the Repo key, for example `crexi/data`. A key always holds a `/`, so no Repo session takes the Hub's name `my-lineup`. An ad hoc Task opens in its Repo's session, as an Issue's Task does. Review Tasks open in the `reviews` session. lineup creates a Repo session with its first Task. It removes the session after the last Task tab in it closes, unless the session holds a tab that the user or another Scheduler opened.

## PROD-TABS-003: A tab label names the Task

The label of a Task tab is the Task ID and the title, for example `ENG-590 Add billing model`. A GitHub Issue Task's label drops the owner from the ID, for example `lineup#121 Remove the ADRs and…`, because the Repo session names the owner. A label longer than 32 characters ends at a word, with `…`. A review Task's ID, for example `PR-4955`, already names the PR. So its label drops the repository and number, the Issue key, and the commit type from the start of the PR title.

## PROD-TABS-004: A Task tab opens without the focus

lineup opens each Task tab, Worker, and side pane without the focus, so the user stays where the user works, for example on the Hub. The tab of a Task appears once. Setup runs in it, and the Task's first Worker then opens in the same tab. If the user closed the setup tab, the first Worker opens in a new tab.

## PROD-TABS-005: By default, only the user closes the tab of a build Task

By default, lineup never closes the tab of a build Task by itself. A Stage handoff keeps the tab. A Task that ends at a merge keeps its tab and its worktree at Done, so the user can read the Worker's last output and ask it one more question. The user closes the tab in Rex, with `x` on the row after a confirmation, or with `lineup close <task>`. The user's close of a Task before Done also closes its tab and removes its worktree.

## PROD-TABS-006: The user's close of a Done Task removes its worktree

When the user closes a Done Task, lineup removes the Task's clean worktree at once. A close is the tab's own close in Rex, a close of the Worker's pane, `x` on the row after a confirmation, `lineup close <task>`, or `lineup archive <task>`. `lineup archive` also removes the kept worktree of a Done build Task with no tab. lineup also removes the Repo session when the close leaves it with no other tab. The Tracker status of the Issue changes when the Task ends, not when the user closes the tab.

## PROD-TABS-007: lineup can close the tab of a merged Task when its Worker is idle

With `close_tab = true` in the `[merged]` table of the config file, lineup closes the tab of each build Task that merges, once its Worker reports `idle` or `done`, or exits. It also removes the Repo session when the close leaves it empty. It keeps the worktree for `keep_worktree_days`, 0 to 30 and 7 by default, from the tab's close, or from the merge with no tab. Then it removes the worktree, at a poll or at its next start. A tab that is gone at the Scheduler's start keeps its worktree for the full days. `close_tab` is off by default.

## PROD-TABS-008: lineup never removes a worktree with uncommitted changes

lineup never removes a worktree that has uncommitted changes, also when the user closes the tab. It keeps the worktree, and the Task's note reads `worktree kept: uncommitted changes in <path>`. lineup tries again at each poll, and removes the worktree after the user commits or discards the changes.

## PROD-TABS-009: A Done Task stays on the Board while it keeps a tab or a worktree

A Done Task leaves the Board when the user archives it, or 7 days after it ends if it keeps no tab and no worktree. A Done Task with an open tab or a kept worktree stays on the Board. While lineup keeps the worktree of a Task with no tab, the note reads `worktree until <date>`, for example `worktree until Jan 2`. A Follow-up row shows this note in the strip after the Tracker state. `lineup show <task>` still finds a Task that left the Board, and `lineup add` puts an archived Issue back.

## PROD-TABS-010: A Done Task that keeps a tab or a worktree holds its ID

`lineup add` refuses the ID of a Done Task that keeps its tab, with `<task> is Done and keeps its tab: close its tab first with lineup close <task>`. While the Task keeps only its worktree, the refusal reads `<task> is Done and keeps its worktree: remove it first with lineup close <task>`. A new review cannot take the PR of a Done review Task that keeps its tab, and the review queues add no review of that PR until the user closes the tab.

## PROD-TABS-011: A failed start keeps its tab for the error

A failed setup keeps its tab, so the user can read worktrunk's error. `lineup close` closes that tab, and a rerun closes it before it opens a new setup tab. If a Worker exits within about 1 s, Rex keeps its tab open for the error, and lineup never closes that tab by itself. `lineup open <task>` closes the kept tab and opens a new one, because the user asked for the Worker.

## PROD-TABS-012: lineup opens no tab for a lost Worker

lineup opens a tab with no ask from the user only for a Task's first Worker. When the tab of a Task in an agent Stage is gone, the Task gets the flag `tab closed`. A Stage prompt that is due waits, and the flag names its Stage. Recovery at the Scheduler's start opens no tab either. A Task whose setup tab is gone, or whose Worker never started, starts again, because it has no Worker to restore. A lost Worker spends no Loop budget.

## PROD-TABS-013: The Hub repairs itself after a Rex restart

After a Rex server restart, Rex brings back each tab as an idle shell. When `lineup` runs and the `BOARD` or `LINEUP` pane of the Hub holds an idle shell, `lineup` closes the Hub tab and opens it again. The Orchestrator then continues its conversation. A pane that the user added does not count.

## PROD-TABS-014: A Repo session with a folder name takes the Repo key

An older lineup named each Repo session after the Repo's folder, for example `data`. At its start, lineup renames each such session of a Task to the Repo key, for example `crexi/data`, and the session's tabs stay open. If a session already has the key, lineup leaves the old session as it is.

## PROD-TABS-015: `lineup restart` reopens the Hub on the installed lineup

`lineup restart` restarts the Scheduler, then closes each Hub tab and opens it again. Each Board then runs the installed `lineup`, and the Orchestrator starts a new session. Worker tabs and review tabs stay as they are. If the config file does not load, `lineup restart` restarts nothing. If no Hub is open, it opens none. `lineup restart scheduler` restarts only the Scheduler.

## PROD-TABS-016: `lineup` brings back a missing Hub pane

When the Hub tab has no `BOARD` pane or no `LINEUP` pane, `lineup` closes the tab and opens it again with both panes. The Orchestrator then continues its conversation.

## PROD-TABS-017: lineup reopens the tab that it runs in

When `lineup` or `lineup restart` must reopen the Hub tab that it runs in, a background process reopens the tab, and the command returns at once. The command prints the path of that process's log. The Orchestrator can run `lineup restart` in its own pane, and its tab then comes back with a new Orchestrator session.

## PROD-TABS-018: The config file sets the split of the Hub tab

`agent_side` in the `[layout]` table puts the `LINEUP` pane on the `left`, `right`, `top`, or `bottom` of the `lineup` tab, and the Board takes the other side. `agent_size` gives the pane 10 to 90 percent of the width for `left` or `right`, or of the height for `top` or `bottom`. The defaults are `left` and 50. A new Hub tab and a reopened Hub tab both use the table. A bad value stops the config load.
