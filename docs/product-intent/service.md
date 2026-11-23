# Service Product Intent

This document states the current intended experience for how the Scheduler runs and recovers, and which tools it needs.

## PROD-SERVICE-001: The Scheduler runs from login and starts again when it stops

launchd runs the Scheduler as a user agent. It starts the Scheduler at login and starts it again when it stops. `lineup` installs the agent, and it loads the agent only when the agent is not loaded. `lineup restart`, `lineup stop`, and `lineup logs` control the Scheduler. `lineup restart` gives the agent the current `PATH` and lineup's environment variables. Only one Scheduler runs at a time. Each start of the Scheduler keeps only the last 1 MiB of its log.

## PROD-SERVICE-002: lineup names each missing dependency

lineup needs Rex at server API revision 7 or later, worktrunk, the GitHub CLI, and Claude Code. If `wt` is not on `PATH`, lineup shows `worktrunk is required: brew install worktrunk (https://worktrunk.dev)` and starts no Task. If `rex`, `gh`, or `claude` is not on `PATH`, lineup stops with an error that names the tool. Without a Linear sign-in, the Board shows no Linear Issue rows, and `lineup login` signs in. lineup needs no herdr.

## PROD-SERVICE-003: The Scheduler recovers its Tasks at start

When the Scheduler starts, it compares its Tasks with the tabs in Rex. A Task in an agent Stage whose Worker is gone gets the flag `tab closed`, and lineup opens no tab for it. A Task whose setup tab is gone, or whose Worker never started, starts again. If worktrunk exited while the Scheduler was down, the Task goes on from that exit. A Done Task keeps its tab. Then recovery runs the cleanup check of each poll, so a worktree whose `keep_worktree_days` passed goes before the first poll.

## PROD-SERVICE-004: A Rex server restart flags each lost Worker

A restart of the Rex server ends every Worker. Within 5 s, the Scheduler sees the new Rex server and exits, and launchd starts it again. Its recovery flags each Task whose Worker is gone with `tab closed`. Rex brings back each tab as a login shell, and lineup closes each tab that holds only an idle shell. A tab where the user started a program stays. The user restores each Worker with `lineup open <task>`, or with `enter` on its row.
