# Capture defaults to the Local Tracker

`n` on the `2 work` view captures one line as a new Issue at one Destination. A Destination is the Local Tracker or one Linear workspace, team, and optional Project. The capture box shows the default Destination, `local`. `tab` opens one fuzzy list of every Destination, with recent picks first. A Linear capture is always assigned to the user. We chose this because the audience decides where an Issue goes. Most quick captures are paper cuts that only the user needs to see, and a wrong default must not put a paper cut in the team's Project.

A local Issue can turn out to matter to the team. "Promote" creates a Linear Issue from it at a Destination from the same list, then marks the local Issue `canceled` and names the new Issue in the file.

## Considered Options

- **Ask for the workspace, then the team, then the Project.** Rejected. Three prompts for each capture slow the capture, and the user files to a few places again and again.
- **Default to the last Destination.** Rejected. One team capture would send the next paper cut to the team.
- **A ticket in Linear for every Task.** Rejected. Paper cuts and small to-dos would flood the team's Project. The PR is the team's record of such a change.
- **Sync between the Local Tracker and Linear.** Rejected for now. Promote moves one Issue once, which is all the user needs.

## Consequences

- The work view reads the Local Tracker whatever `local_tracker` says. That key, as in [ADR 0011](0011-trackers-behind-one-port.md), still decides only the tracker browser's tabs.
- The Local Tracker gets a `backlog` status. A local capture lands in Backlog, as a Linear capture lands in its team's default status.
- The Orchestrator names the Destination before it writes an Issue from the user's words.
