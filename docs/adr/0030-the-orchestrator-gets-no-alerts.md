# The Orchestrator gets no alerts

The Orchestrator no longer watches for alerts or asks the user about them. `lineup watch` is gone, and the Hub starts the Orchestrator with no first message. The user decides each Task in Needs you with What Needs Me… in Rex's command palette, as [ADR 0028](0028-rex-asks-the-needs-you-choices.md) and [ADR 0029](0029-the-palette-runs-everyday-lineup-commands.md) describe. We chose this because each alert question cost an agent turn, and the user had to go to the `lineup` tab to answer it. The palette asks the same choices from any tab, on any device, with no agent turn.

## Considered Options

- **Keep the watch and the questions beside the palette.** Rejected. Each alert would reach the user twice, and every Orchestrator turn still runs for it.
- **Keep the watch, and let the Orchestrator only say what changed.** Rejected. The Board's status record already shows the count on the `lineup` tab, and a toast reports each Task that reaches Done.

## Consequences

- The Orchestrator acts on a Task in Needs you only when the user asks. It runs `lineup decide` for the choices, and `lineup decide <task> <choice>` for the one that the user picks.
- The question for a Done Task with a tab is gone. The user closes the tab with Close a Task… or Archive Done Tasks…, or keeps it.
- The Orchestrator still asks with `AskUserQuestion` when `lineup add` or `lineup start` fails, for example with `wip cap full`.
- The Board keeps its Done toast, its free slot toast, and its status record. `scheduler.Alerts` serves only the Board.
- `alerted.json` and `watch.lock` in the state directory are no longer read. They can be deleted.
