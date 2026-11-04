# Planning belongs to the Worker

lineup has no Planning or Plan review Stage. A started Task goes straight to Working. If the work needs a plan or the user's approval first, the user's words or the chosen skill ask for it, and the Worker holds that dialogue with the user in its own tab. We chose this because planning is part of doing the work. A fixed "write the plan, then stop" contract overrode skills that run their own planning, and it blocked work that has no plan, for example a skill that explains a PR.

## Considered Options

- **Keep the plan gate and let the user turn it off per Task.** Rejected. lineup would still decide that planning comes first, and every skill with its own planning would plan twice.
- **Keep the Planning contract but drop "then stop".** Rejected. The Worker codes past the plan, and the Scheduler has nothing to gate on.

## Consequences

- A Worker that stops to show a plan or ask a question ends its turn without a PR. The Task gets the Needs you flag `stopped`, and the flag clears when the Worker starts working again.
- The Board has no plan pane and no approve or return keys. The CLI has no `lineup approve` or `lineup return`.
- The store saves each Stage by its key, not its number. A one-time migration moved the Tasks that were in Planning or Plan review to Working.
- The Linear status event for a start is `started`, not `planning`.
- ADR 0004 still holds for Working, CI debug, and Responding.
