# Orchestrator alerts come from a background watch

The Orchestrator learns about each alert from `lineup watch --once`, which it runs as a background Bash command. The watch prints the alerts of every Task, build or review. The command exits at the next alerts, and the exit wakes the Orchestrator. Each board shows the alerts of its own Tasks as a Rex toast. We chose a background command because an organization policy can block channels. Under such a policy, `claude --dangerously-load-development-channels server:lineup` shows "blocked by org policy", and the channel event does not reach Claude.

## Considered Options

- **A Claude channel (an MCP server that pushes events).** Rejected for now. The org policy `channelsEnabled` must be true first. If an admin enables it, `lineup watch` can feed a channel with no other change.
- **The Scheduler types each alert into the Orchestrator's pane.** Rejected. The text joins any message that the user is typing, and Enter sends it.
- **A prompt hook that adds the alerts to each user message.** Rejected. It does not wake the Orchestrator.
- **`lineup watch` under Claude's Monitor tool.** Replaced. A Monitor expires after 30 minutes. Each expiry woke the Orchestrator, which wrote "Watch re-armed." to its chat every 30 minutes.

## Consequences

- The Orchestrator starts the watch again after each alert. An idle Orchestrator takes no turns.
- The Hub must start the Orchestrator with a first message, because a background command does not survive a restart of Claude.
- The toast does not depend on the Orchestrator. The Board prints it.
