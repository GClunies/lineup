# Worktrunk creates and sets up worktrees

lineup calls worktrunk (`wt`) to create and remove each Task's worktree. Worktrunk's `copy` steps and `pre-start` hooks, in the user config and in each repo's `.config/wt.toml`, define the copied files and the startup commands. lineup keeps no setup config of its own. We chose this because the user already sets up worktrees this way, and two setup configs would drift apart.

## Consequences

- Worktrunk is a hard dependency. If `wt` is not on `PATH`, lineup must show an error with the install command (`brew install worktrunk`, see https://worktrunk.dev) and must not start a Task.
