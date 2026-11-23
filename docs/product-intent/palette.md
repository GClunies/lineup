# Palette Product Intent

This document states the current intended experience for the choices for each Task that needs the user, and the actions in the Rex command palette.

## PROD-PALETTE-001: Each Needs you reason has a fixed list of choices

Each Needs you reason has a fixed list of choices, the default first. Ready to land offers Merge the PR, then Open the tab. `tab closed` offers Restore the Worker, then Close the Task. `question` offers Answer here, then Open the tab. `stopped` and `setup failed` offer Open the tab, then Close the Task. `budget spent` offers Run the Stage again, Open the tab, and Close the Task. `pr closed` offers Close the Task, then Open the tab. A ready review offers Open the tab, then Close the Task. Each other reason offers Open the tab. Answer here needs a live Worker.

## PROD-PALETTE-002: lineup decide lists and runs the choices

`lineup decide` lists each build Task and each review Task in Needs you, each with its choices. `lineup decide <task> <choice>` runs one choice, and Answer here takes the user's words as a third argument. The command refuses a Task that does not need the user, and a choice that the Task does not have.

## PROD-PALETTE-003: Rex asks the choices from any tab or device

In Rex's command palette, What Needs Me… lists the Tasks of `lineup decide`, then the choices of the Task that the user picks. It runs a lone Open the tab with no second question. For Answer here, it asks for the user's words. Tell a Worker… lists the Tasks with a live Worker, asks for words, and runs `lineup steer`. Rex asks on every connected Rex app, and the first answer wins. lineup pushes no prompt, so the user opens the palette when they choose.

## PROD-PALETTE-004: The palette runs the everyday lineup commands

Rex's palette holds a Lineup action for each everyday command. Go to a Task… runs `lineup open`. Start a Task… takes a typed Issue ID. Start an Ad Hoc Task… offers the Repos of the Board's build Tasks or a typed Repo, then asks for the job. Review a PR… takes a PR URL. Close a Task… asks again before `lineup close`. Archive Done Tasks… archives each ticked Done Task. Run a Stage Again… offers the agent Stages of the Task's Kind. Set the WIP Cap…, Pause Reviews, Resume Reviews, and Set a Task's Model… run `lineup cap`, `lineup reviews`, or `lineup set`.

## PROD-PALETTE-005: A start from the palette asks at a full WIP cap

When Start a Task… or Start an Ad Hoc Task… finds the WIP cap full, the action asks to start the Task once over the cap. If the user says no, it adds nothing, and the Issue stays in Up next.

## PROD-PALETTE-006: The palette needs Rex API revision 17

The lineup actions in Rex's palette need a Rex server at API revision 17 or later. On an older server, the palette shows no lineup action.

## PROD-PALETTE-007: lineup leaves the user's own Rex files alone

lineup never edits Rex's init file and owns no key binding. Each time lineup opens the Hub, it writes its own file of palette actions beside Rex's init file, so an edit to that file is lost. The user adds `require("lineup")` to Rex's init file once to load the actions. The user binds a key to an action by its name there.
