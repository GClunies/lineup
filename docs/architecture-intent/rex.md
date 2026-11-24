# Rex Architecture Intent

This file states how lineup uses Rex sessions, tabs, status records, and the palette.

## ARCH-REX-001: Every block call names its session

Each Rex call from lineup that names a block or a window passes `-s <session>`. This holds for the Scheduler, the Hub setup, and recovery.

Because: without `-s`, Rex resolves a block only inside the caller's own session and returns `block target ... not found`.

## ARCH-REX-002: The tab of a Task appears once

The Scheduler opens a build Task's setup tab with `rex window new --focus=false --keep-open`, and worktrunk runs in it. After setup, the first Worker opens beside the setup block with `rex split`. The Record takes the Worker's block before the Scheduler closes the setup block, so that close never reads as the user's. If the user closed the setup tab, the Worker opens in a new tab. Every Task tab opens with `--focus=false`, so the user stays on the tab where they work.

## ARCH-REX-003: Each tab names its command

Each Task tab and each split runs a named command after `--`, never a plain shell. The command sets its environment with an `env` prefix, because Rex starts it through a login shell that runs no `direnv`. Rex keeps a tab open when its program exits within about 1 s. The Scheduler never closes that tab by itself, and treats it as a tab with no live Worker.

Because: a tab without a command reports exit code 0 when its shell exits, whatever the real code is.

## ARCH-REX-004: The Board reports Needs you as one record on its pane

The Board reports the build Tasks in Needs you as one OSC 7501 program status record on its own pane. The record has `state=blocked` and `kind=question`, and its message names the count. The Board sends it at start, and each time the set of those Tasks, or the Stage or flag of one, changes. At a count of 0 it sends a clear with no id. The Board is the only program in its pane. It sends no clear at quit, because Rex drops a `blocked` record when its process exits.

Because: Rex keeps a record on the tab until the Board replaces or clears it, while a toast shows once.
Not: a record for each Task, whose ID breaks the OSC 7501 id grammar; a record from the Scheduler, which has no terminal under launchd; the toast kept beside the record, which notifies twice.

## ARCH-REX-005: The Board sends the toasts

The Board sends each toast as an OSC 777 notification from its own pane, so a click on the toast opens that pane. It computes the alerts from the views that it paints. The first view counts as known, so it makes no toast. A Task alerts again only after it leaves the alerting state and enters another state that needs the user. A build Task in Needs you for a flag gets no toast, because the status record reports it.

Because: the Scheduler runs under launchd with no terminal, so it has no pane to notify from.

## ARCH-REX-006: lineup owns the choices, and the Lua file only asks

lineup holds the choices of each Needs you reason. `lineup decide --json` lists the Tasks in Needs you with their choices, and `lineup decide <task> <choice>` runs one. The palette actions in lineup's Lua file only ask with Rex prompts and run lineup commands that exist. The Lua file holds one lineup rule, the agent Stages of each Kind, and a test checks that table against the lifecycle.

Because: CI cannot test the Lua file, and choices there repeat lineup's reason names.
Not: the choices in the Lua file.

## ARCH-REX-007: lineup writes only its own Lua file

Each time lineup opens the Hub, it brings its own Lua file up to date, beside Rex's init file. `REX_CONFIG` names the init file when it is set. The Lua file runs lineup by the absolute path of its binary, because `rex.exec` runs a command with the Rex server's `PATH`. The user adds `require("lineup")` to the init file and binds keys to the named actions there. On a Rex server below API revision 17, the Lua file defines no action.

Not: lineup edits the user's Rex init file; lineup owns the key bindings.

## ARCH-REX-008: The user opens each prompt

A Rex prompt opens only when the user runs a palette action. No part of lineup pushes a prompt when a Task starts to need the user.

Because: a pushed prompt opens over the tab where the user works, and Rex answers `busy` while another prompt is open.
Not: the Scheduler pushes a prompt.

## ARCH-REX-009: Recovery reads the process of each tab

At start, recovery compares the store with `rex ls` and `rex session inspect`, then reads each tab's process with `rex block call process`. A tab that holds only an idle login shell counts as gone, and recovery closes it. A tab where the user started a program counts as live. A setup tab that Rex kept gives worktrunk's exit code, so the Task goes on as if the Scheduler saw the exit. Recovery reads no process for a Done Task, except a merged Task whose tab waits for an idle Worker.

Because: Rex brings back each tab after its restart with the same IDs, as a login shell with no program.

## ARCH-REX-010: The side pane never changes the Task

The side pane is a block labeled `side` to the right of the Worker, in the Task's worktree. The Record keeps the pane's block ID, folder, and command apart, and the Worker's block stays the block of the Task, so no event of the pane changes the Task. When the Worker's block closes and the pane holds the tab open, lineup closes the tab.

## ARCH-REX-011: A Hub reopen that would end its caller runs in a detached copy

When lineup must close the Hub tab whose pane runs it, it starts a copy of itself in a new process session and returns. The copy gets no `REX_BLOCK`, appends its output to a log in the state directory, and runs the whole Hub setup again. Each step of that setup is idempotent, so the second run reaches the same Hub.

Because: closing a Rex window ends every process of its panes, and a process in its own session outlives that close.
Not: refusing to reopen the caller's tab; a Board that re-execs itself when its binary changes.

## ARCH-REX-012: One step on the Task's lane converges its side pane

One step on the Task's lane converges the tab on one side pane. It runs after a Worker opens, at start for each live Worker, and at each poll where the Record names a pane. A side pane is the Record's pane, or a block but the Worker's that is labeled `side` or runs `hunk` or `rexweb-pane`. The step keeps the Record's pane while it matches the Task's worktree and merge-base, and closes the others. A new pane splits the old pane, which then closes, so it keeps its place and size. A failed split logs a warning.

Because: a typed `hunk` has the label `shell`, and a rexweb pane has the page's title and runs `bash` with the `rexweb-pane` script as its first argument. One check alone misses a pane.
Not: the merge-base computed once in the pane's shell, which freezes the base.
