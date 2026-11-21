# The Orchestrator learns each PR that is ready to land

This ADR changes one part of [ADR 0030](0030-the-orchestrator-gets-no-alerts.md) and one part of [ADR 0027](0027-the-board-reports-needs-you-to-rex.md).

A build Task that reaches Ready to land alerts the user and the Orchestrator. The Board toasts the Task and its PR, for example `ENG-571 #4636 ✓ approved · ready to land | Rate limit login`. The Orchestrator's `UserPromptSubmit` hook runs `lineup ready` before each turn. That command prints one line for each build Task whose PR is ready to land. The user asked for this alert: a Worker carries the PR to ready to merge, and the user wants to learn when the work is done without a check by hand. We chose a prompt hook for the Orchestrator because it costs no agent turn. ADR 0030 removed the alerts for that cost.

## Considered Options

- **Wake the Orchestrator with a background `lineup watch` for Ready to land.** Rejected. Each ready PR costs an agent turn. The Orchestrator would only repeat the toast, which ADR 0030 rejected.
- **Name the ready Task in the Board's status record, and send no toast.** Rejected. The Rex docs do not say which text a status notification shows. The Board's Done toast already reaches the user as a desktop toast.
- **Print only the Tasks that are new since the last turn.** Rejected. The hook would need its own state. A full list is short, and the Orchestrator can always name each ready Task.

## Consequences

- The Orchestrator learns of a ready PR at the user's next message, not at once. The toast is the alert at once.
- When Rex is in the background, the user can get two notifications for one ready PR: the toast and the status record. ADR 0027 rejected that for the other Needs you reasons, and it still holds for them.
- A Task in Ready to land with a flag, for example `pr closed`, gives no toast and no line, because the flag needs the user for another reason.
- If a Repo arms GitHub auto-merge, the PR can merge just after the toast. Then the Done toast follows.
- The Orchestrator still asks the user nothing about a Task on its own. It uses the lines to answer "what is ready?" and to find the Task for "merge it".
