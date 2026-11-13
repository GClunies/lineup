# Review Tasks give no alert

A review Task gives no alert. `scheduler.Alerts` returns build Tasks only, so `lineup watch` prints no review line and the Board makes no review toast. The Orchestrator then never asks the user about a review. The user sees each review in its tab in the Rex sidebar, and decides each finding there. We chose this because the Orchestrator asked the user again what the review tab already asked. The user said that the Hub agent must never prompt them about reviews.

## Considered Options

- **Tell the Orchestrator to skip review alerts.** Rejected as the only guard. An instruction can drift. With no alert, no prompt can follow. The instructions still tell the Orchestrator not to ask about a review that a status read shows.
- **Skip review Tasks in `lineup watch` only, and keep the Board's toasts.** Rejected. The Rex sidebar already shows each review tab. One rule in `Alerts` covers both consumers.

## Consequences

- This reverses one part of [ADR 0016](0016-one-front-agent.md). The Orchestrator still starts reviews, pauses and resumes the queues, skips PRs, edits the queue config, and answers when the user asks about a review.
- The Board's header still counts the review Tasks that need the user, as `(+N in reviews)`.
