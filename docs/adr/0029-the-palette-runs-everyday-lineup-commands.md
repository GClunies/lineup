# The palette runs everyday lineup commands

Rex's command palette gets an action for each lineup command that the user runs often: go to a Task, start a Task, start an ad hoc Task, review a PR, close a Task, archive Done Tasks, run a Stage again, set the WIP cap, pause and resume reviews, and set a Task's model. `lineup decide` also lists the review Tasks in Needs you, so What Needs Me… shows every Task that waits for the user. We chose this because the user wants to type what they want to do in the palette, from any tab, instead of in the Orchestrator's chat or on the Board.

## Considered Options

- **Keep the palette to the Needs you choices, as [ADR 0028](0028-rex-asks-the-needs-you-choices.md) chose.** Rejected. The user asked for every action. The Rex tab switcher finds a tab by its label, but it cannot restore a closed Worker or start a Queued Task.
- **Let lineup own the key bindings.** Rejected. Rex binds a key with `rex.bind` in `init.lua`, and `init.lua` is the user's file. Each action has a name, so the user binds it there.
- **Add a Go command that lists the user's Issues for Start a Task….** Rejected for now. Only the Board reads the Trackers. Start a Task… lists the Queued Tasks and takes a typed Issue.

## Consequences

- The actions run only commands that exist. The Lua file holds no lineup rule except the agent Stages of each Kind, and a Go test checks that table against the lifecycle package.
- A review Task in Review ready offers Open the tab and Close the Task. Each reason that a build Task has keeps its choices for a review Task.
- Answer here needs a live Worker, so a Task whose Worker is gone offers the other choices only.
- [ADR 0025](0025-review-tasks-give-no-alert.md) still holds. A review Task gives no alert, and the palette lists it only when the user opens What Needs Me….
