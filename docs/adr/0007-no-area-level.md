# No Area level above Repos

lineup has no "Area" (a work, consulting, or personal domain) above Repos. Each Repo maps to zero or one Tracker workspace and its teams, and the configuration shares defaults across Repos by path glob, for example `~/repos/acme/*`. We chose this because every Task has exactly one Repo, so the Repo already answers which Tracker, which defaults, and which Rex session apply.

## Considered Options

- **An Area that owns Tracker accounts, Repos, and defaults.** Rejected: each of its jobs is a Repo mapping or a path glob, so it adds a concept and a configuration level with no new information.
