Superseded by [0008](0008-scheduler-runs-as-a-launchd-agent.md).

# The Scheduler runs inside Rex

The Scheduler (`lineup serve`) runs in a visible block of the Hub session, not as a launchd service. It owns a SQLite store, and the Board and the CLI connect to it over a Unix socket. We chose this because the Workers live and die with Rex, so a Scheduler that outlives Rex has nothing to manage, and a Rex block shows its logs with no extra tooling.

## Consequences

- When Rex starts again, the Scheduler must compare its store with Rex and restart the Stage of each Task that lost its Worker.
