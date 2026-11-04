# Orchestrator alerts come from a Monitor

The Orchestrator learns about each alert from `lineup watch`, which it runs under Claude's Monitor tool. The Board in the `orchestrate` tab shows each alert as a Rex toast. We chose a Monitor because an organization policy can block channels. Under such a policy, `claude --dangerously-load-development-channels server:lineup` shows "blocked by org policy", and the channel event does not reach Claude.

## Considered Options

- **A Claude channel (an MCP server that pushes events).** Rejected for now. The org policy `channelsEnabled` must be true first. If an admin enables it, `lineup watch` can feed a channel with no other change.
- **The Scheduler types each alert into the Orchestrator's pane.** Rejected. The text joins any message that the user is typing, and Enter sends it.
- **A prompt hook that adds the alerts to each user message.** Rejected. It does not wake the Orchestrator.

## Consequences

- A Monitor stops after 30 minutes. The Orchestrator must start it again, and each restart costs one short turn.
- The Hub must start the Orchestrator with a first message, because a Monitor does not survive a restart of Claude.
- The toast does not depend on the Orchestrator. The Board prints it.
