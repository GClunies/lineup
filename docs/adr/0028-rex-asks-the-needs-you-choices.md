# Rex asks the Needs you choices

Each Needs you reason has a fixed list of choices in lineup. `lineup decide` lists the build Tasks that need the user, each with its choices, and it runs the choice that the user picks. Two actions in Rex's command palette, What Needs Me… and Tell a Worker…, ask with Rex prompts and run `lineup decide` and `lineup steer`. We chose this because the user can then decide from the tab that they are in, or from another device, and no agent turn runs. The Orchestrator's alert questions offer the same choices, but each one costs an agent turn, and the user must go to the lineup tab to answer.

## Considered Options

- **Keep the choices in the Lua file.** Rejected. CI cannot test the Lua file, and the file would repeat lineup's reason names. The choices belong to lineup, so they live in Go. The Lua file only asks.
- **Let the Scheduler push a prompt when a Task starts to need the user.** Rejected for now. A pushed prompt opens over the tab that the user works in. Also, Rex answers `busy` while another prompt is open. The user opens the palette when they choose.
- **Add actions that go to a Task or start a Task.** Rejected. Rex's tab switcher already goes to a Task's tab, and the Board's `s` starts an Issue.
- **Add the `require` line to `init.lua` for the user.** Rejected. `init.lua` is the user's file. lineup writes only its own `lineup.lua`.

## Consequences

- The palette runs a lone Open the tab with no second question. It asks before any other lone choice.
- `lineup.lua` runs lineup by its absolute path, because `rex.exec` runs a command with the Rex server's `PATH`. lineup writes the file again each time it opens the Hub.
- Rex prompts need API revision 17 or later. On an older server, the file defines no action, and the Orchestrator's alert questions stay the only way to decide in Rex.
