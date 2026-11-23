# Structure Architecture Intent

This file states how lineup divides its processes and who owns each decision.

## ARCH-CORE-001: Deterministic code owns each scheduling decision

The Scheduler is deterministic code. It owns the WIP cap, the review queue fills, Worker startup, and every Task transition, and it never judges the work. The Orchestrator is the agent that plans and triages. It changes Tasks only through the `lineup` CLI, which calls the Scheduler.

Because: Scheduling must be fast and predictable, and an agent on each transition costs time and tokens and turns the WIP cap into a preference.
Not: An LLM coordinator that makes each scheduling decision, as in Cursor Projects, Claude Projects, and herdr-projects.

## ARCH-CORE-002: One Scheduler owns the store, and clients use its socket

`lineup serve` is the Scheduler. It is the only process that opens the store, and it holds all Task state. A file lock in the state directory allows one Scheduler at a time, and the kernel drops the lock when the process dies. The Board and each `lineup` command reach the Scheduler through its Unix socket and never write the store. The Scheduler also writes its latest Board view to a cache file, so the Board can paint before it connects.

## ARCH-CORE-003: launchd runs the Scheduler

A launchd user agent with `RunAtLoad` and `KeepAlive` runs `lineup serve`, so the Scheduler starts at login and starts again when it stops. The Scheduler has no Rex tab. `lineup restart`, `lineup stop`, and `lineup logs` control the agent. The log is a file in the state directory. launchd does not rotate it, so each Scheduler start keeps only its last 1 MiB.

Because: A Scheduler in a Rex tab stops whenever the user closes or misuses that tab.
Not: The Scheduler in a visible block of the Hub session.

## ARCH-CORE-004: The Scheduler exits when the Rex server changes

The Scheduler reads the instance ID of the Rex server every 5 s. When the ID differs from the ID at its start, the Scheduler exits, and launchd starts it again. Startup recovery then flags each Task whose Worker is gone with `tab closed`, and only the user restores a Worker.

Because: Every Worker dies with the Rex server, and startup recovery is the one path that compares the store with Rex.

## ARCH-CORE-005: The launchd agent carries only the variables that the Scheduler reads

The launchd agent gets `PATH`, and it gets `LINEUP_STATE_DIR`, `LINEUP_CONFIG`, and `XDG_STATE_HOME` when they are set. It gets no other variable, so the `REX_*` variables of a Rex block never reach the Scheduler. `lineup restart` writes the current values into the agent and restarts it. `lineup` loads the agent only when launchd has not loaded it, and it leaves a loaded agent as it is.

Because: Each shell has its own `PATH`, and a reload restarts the Scheduler in the middle of its work.

## ARCH-CORE-006: lineup owns copies of the code that it took from herdr

lineup is one Go program. It keeps its own copies of the Linear client, the OAuth flow, the cache, and the Markdown view of the user's `herdr-linear` fork, and it changes them freely. No lineup code imports herdr code or calls the `herdr` binary.

Because: lineup runs on Rex only, and a copy lets the code change with no tie to herdr.
Not: An import of herdr code, or a call to the `herdr` binary.

## ARCH-CORE-007: The Board reads the Trackers for its own Issue rows

The Board reads its Issue rows from the Trackers itself. It reads Linear for the user's own Issues and the unassigned Issues of the user's teams in each workspace, the Local Tracker folder, and `gh` for the GitHub Tracker. It drops each Issue that a Task on the Board holds. The Scheduler and its store hold no Issue row. A start from an Issue row is an add request to the Scheduler.

## ARCH-CORE-008: Slow reads stay off the Scheduler loop

One loop goroutine owns every Record and applies every Signal. The Effects of each Task run in their own lane, on a copy of the Record, and they hand each change back to the loop. GitHub polls, usage reads, and other slow reads run outside the loop and give their results to it. Only `lineup show`, the detail pane, and `lineup ls --json` read usage, and the Scheduler stores no usage.

Because: A slow read inside the loop delays the Board and the hooks that run on each Orchestrator turn.
