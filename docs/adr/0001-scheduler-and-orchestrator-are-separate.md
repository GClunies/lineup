# Scheduler and Orchestrator are separate

Deterministic code (the Scheduler) owns the queue, the WIP cap, Worker startup, and every Task transition. The long-lived Orchestrator agent plans and triages, and it changes the Board only through the Scheduler's CLI. We chose this split because scheduling must be fast, predictable, and free of judgment about the work, while planning and triage need an agent.

## Considered Options

- **An LLM coordinator makes every scheduling decision** (Cursor Projects, Claude Projects, herdr-projects). Rejected: it is slow, it costs tokens on each transition, and a concurrency limit becomes a preference instead of a rule.
