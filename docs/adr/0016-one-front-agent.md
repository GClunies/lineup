# One front agent over a board for each Kind

[ADR 0025](0025-review-tasks-give-no-alert.md) changes one part: a review Task gives no alert, so none reaches the chat.

The Hub has one agent that the user talks to, the Orchestrator, in the `LINEUP` pane of the `hub` tab. It plans Issues, takes build Tasks and review Tasks, and reports the alerts of every Task. We chose this because the user wants every alert to reach one chat and every answer to leave from it. With one agent for each tab, a review alert reached no chat, and "what needs me?" left out the other Kind.

## Considered Options

- **Four agents, one for each Hub tab.** Replaced. The commits 018be9a, 8f25938, and bd42fdb gave the review tab its own Reviewer and fenced the Orchestrator to build Tasks. No ADR recorded the choice. The Reviewer's commit, 018be9a, gave only the reason "as the orchestrate and plan tabs have". The user had to know which tab owns a request, and each agent sent the other requests to another tab.
- **A router with no chat**, as the dsh-superagent prototype is. Its panel sends each event to a handler and has no agent for the user to talk to. Rejected. The user wants to ask questions and give orders in one conversation, not only to read a feed.

## Consequences

- The agent's settings allow every `lineup` command, the Tracker reads and writes, and `gh pr view`. They deny each GitHub write to a PR, `gh api`, and each Issue delete. Only the user posts a review.
- The agent works in a new folder, `lineup` in the state folder, so the Hub starts it with no conversation from the old Orchestrator.
- `openHub` closes a tab that still runs `ORCHESTRATOR`, `PLANNER`, or `REVIEWER`, and opens it again in the new form.
- The `hub` tab replaces the `plan`, `work`, and `review` tabs, so the Hub has two tabs, `assist` and `hub`. One Board process shows the three views over the one agent pane. The keys `1`, `2`, and `3` pick the plan, work, and review view, and the header names them with the active one bright. A digit stays text in a filter or a capture. The Board loads the Linear browser and the PR search on the first visit to their views, and it toasts the alerts of both Kinds in every view. `openHub` renames a running `work` tab to `hub` and closes the `plan` and `review` tabs. `z` zooms the agent pane from every view.
- The Tracker sections and the `lineup trackers` hook move from the Planner to this agent.
