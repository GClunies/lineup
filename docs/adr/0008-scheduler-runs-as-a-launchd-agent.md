# The Scheduler runs as a launchd agent

The Scheduler (`lineup serve`) runs as a launchd user agent with `RunAtLoad` and `KeepAlive`. `lineup` installs the agent, and `lineup restart`, `lineup stop`, and `lineup logs` control it. This supersedes [0006](0006-scheduler-runs-inside-rex.md). A Rex tab stopped the Scheduler whenever the user closed or misused it. launchd starts the Scheduler at login and starts it again when it stops.

## Consequences

- A lock on `serve.lock` in the state directory allows one Scheduler at a time. The kernel drops the lock when the process dies.
- The Workers still die with the Rex server. The Scheduler reads the Rex server's instance id every 5 s. It exits when the id changes. launchd starts it again. Startup recovery flags each lost Worker with `tab closed`. The user restores it with `lineup open`.
- The agent gets only the variables that the Scheduler reads: `PATH`, and `LINEUP_STATE_DIR`, `LINEUP_CONFIG`, and `XDG_STATE_HOME` when set. `lineup restart` writes the current values into the plist. `lineup` loads the agent only when it is not loaded. Each shell has its own `PATH`, and a reload restarts the Scheduler in the middle of its work.
- The log is `scheduler.log` in the state directory. launchd does not rotate it, so each Scheduler start keeps only its last 1 MiB.
