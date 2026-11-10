# A fresh Worker for each agent Stage

Each agent Stage (Working, CI debug, Responding) starts a new Worker in the Task's existing tab and worktree, with that Stage's skill, provider, and model. The Worker gets its context from the Issue, the PR, and a Task notes file in the worktree. We chose this because the user wants a different provider or model per Stage, and a fresh context per Stage keeps each Worker focused.

## Considered Options

- **One long-lived agent session per Task.** Rejected: it locks a Task to one provider and model, and its context fills with earlier Stages.
