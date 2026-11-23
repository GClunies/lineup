# Orchestrator Architecture Intent

This file states how the Orchestrator connects to lineup.

## ARCH-ORCH-001: One front agent serves every Kind

The Hub runs one Claude agent, the Orchestrator, in the `LINEUP` pane under the Board, in the Hub's `my-lineup` tab. It takes build Tasks and review Tasks, and it plans in each Tracker that is on. It acts only through `lineup` commands and the Tracker CLIs, so every Task change goes through the Scheduler.

Because: the user wants every request in one conversation, and one agent for each tab made the user know which tab owns a request.
Not: four agents, one for each Hub tab; a router that sends each event to a handler and has no chat.

## ARCH-ORCH-002: The Orchestrator gets no alert wake-ups

Nothing in lineup wakes the Orchestrator. The Hub starts it with no first message, the Scheduler never types into its pane, and no background command or channel feeds it. The Orchestrator takes a turn only for a message from the user. The Board's toasts, the Board's status record, and Rex's palette carry each alert to the user.

Because: each alert cost an agent turn, and the palette asks the same choices from any tab with no agent turn.
Not: a background `lineup watch`; a Claude channel; the Scheduler types each alert into the pane; a prompt hook as the alert wake-up; `lineup watch` under Claude's Monitor tool.

## ARCH-ORCH-003: Prompt hooks give the Orchestrator its live facts

The `UserPromptSubmit` hook of the Orchestrator runs `lineup trackers` and `lineup ready` before each turn. `lineup trackers` names the Trackers that are on. `lineup ready` prints one line for each build Task in Ready to land with no flag, and nothing when no PR is ready. Each run prints the full current list, and the hook keeps no state.

Because: a hook costs no agent turn, and Claude reads its instructions only when a session starts, so a later sign-in or ready PR reaches it only through a hook.
Not: a background watch for Ready to land; a hook that prints only the lines that are new since the last turn.

## ARCH-ORCH-004: The settings of the Orchestrator deny writes to a PR

The Hub writes the Claude settings of the Orchestrator. They allow every `lineup` command, the Tracker reads and writes through `linear` and `gh issue`, `gh pr view`, `gh pr list`, and `gh search prs`. They allow reads and edits of the Local Tracker folder and of the config file, and of nothing else in its folder. They allow reads, and no edits, of the folder where Claude keeps the Orchestrator's sessions. They deny `gh pr review`, `gh pr comment`, `gh pr merge`, `gh pr close`, `gh pr edit`, and `gh api`. They also deny each Issue delete, `gh issue close`, `gh issue reopen`, `linear issue start`, and `linear issue pull-request`.

Because: only the user posts to a PR, and only lineup moves an Issue through its lifecycle.

## ARCH-ORCH-005: The Orchestrator works in its own folder

The Orchestrator runs in a `lineup` folder in lineup's state directory, never in a Repo. Each time lineup opens the Hub, it writes the instructions and the settings of the Orchestrator into that folder, and it trusts the folder for Claude. Claude reads them when a session starts, so a running Orchestrator takes a change at its next start. `lineup` continues the last conversation of the folder when one exists, and `lineup restart` always starts a new session.

Because: the folder holds only the settings and the instructions that lineup writes, so no Claude settings or hooks of a Repo apply.
