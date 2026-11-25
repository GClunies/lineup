# Harness Architecture Intent

This file states how lineup starts and talks to agent programs.

## ARCH-HARNESS-001: One Harness adapter serves Claude

lineup has one Harness, Claude Code. One adapter owns each part that is Claude's: the configuration folder, the trust entry, the Worker command, the Stage prompts, and the usage read. The Scheduler finds `claude` on its own `PATH` when it resolves the configuration, and starts each Worker by that absolute path. A `harness` key in the config file is an error.

Because: Rex starts commands through a login shell, which can resolve a name to another binary than the Scheduler sees.

## ARCH-HARNESS-002: One Worker for each Task keeps a fixed conversation

Each Task has one Worker for its whole life: an interactive `claude` in one tab, started with `--session-id <conversation>`. The Record keeps the conversation ID from the first Worker. Each later agent Stage, and each rerun, types its prompt into the same Worker, so no tab closes at a Stage handoff. A restore runs `claude --resume <conversation>` in a new tab. Only a Task's first Worker opens a tab without the user's ask. Claude Code compacts the growing context by itself.

Because: the Working Worker holds the context that CI debug and Responding need, and the user controls the tabs.
Not: a fresh Worker for each Stage, which closes the tab at each handoff; a split tab that keeps the old Worker's pane, where panes pile up and two agents share one worktree.

## ARCH-HARNESS-003: The Scheduler types into a Worker only when the Worker is idle

The Scheduler types a Stage prompt only when the Worker's last program status is `idle` or `done`. While the Worker is busy, the prompt waits in the Record as pending. A Worker that has sent no status is of unknown state, and the Scheduler never types into it. From a typed prompt until the Worker's next `working`, `blocked`, or `error`, the Worker is `prompted`. The Scheduler drops a `done` or `idle` in that state, because that status ended the turn before the prompt.

## ARCH-HARNESS-004: A skill prompt starts as typed keys

The adapter types a prompt that has no newline as keys. It sends a prompt with a newline as one bracketed paste, and types a leading `/skill` command as keys before the paste. It drops control bytes from the text, so Issue text cannot end the paste or press a key.

Because: Claude runs a slash command only from typed keys, a menu reads a pasted answer as no keypress, and Claude drops the start of a long unbracketed write after a turn ends.

## ARCH-HARNESS-005: The adapter trusts each folder under Claude's own lock

Before a Worker starts in a folder, the adapter marks the folder's real path as trusted in Claude's state file, in the configuration folder of the Repo. It takes Claude's own lock folder, waits while another process holds it, and treats a lock older than 10 s as abandoned. It writes a temporary file and replaces the state file with one rename, with every other key as it was. It trusts a build Task's worktree, and the main checkout for a review Worker or a read-only Worker.

Because: an interactive Claude has no flag that skips the trust dialog, and a running Claude keeps an entry that another process added under the lock.

## ARCH-HARNESS-006: The direnv of the Repo decides the Claude configuration folder

The adapter finds a Repo's Claude configuration folder with `direnv exec <main checkout>`, which prints `CLAUDE_CONFIG_DIR`. An empty value, or no `direnv` on the machine, means the default profile. The worktree path never decides the folder, because worktrees live outside the Repo. The Worker command sets `CLAUDE_CONFIG_DIR` itself. The trust entry and the usage read use the same folder.

Because: Rex starts each Worker through a login shell that runs no `direnv`.

## ARCH-HARNESS-007: A read-only Worker runs with a deny list

A read-only Worker starts with `--disallowedTools=Bash,Edit,Write,NotebookEdit,EnterWorktree` and without the allow rule for `lineup ask`, so it has no tool that writes files. `--append-system-prompt` tells it that lineup removed the worktree and that it answers the user's questions. `--system-prompt-snapshot off` makes the resume send that text, because a resume otherwise sends the system prompt that the conversation recorded first.

Because: a probe showed that this deny list leaves Claude and each subagent that it starts with no write tool, while plan mode blocks a write only when the model declines it.
Not: plan mode for the read-only Worker, which is no hard block and which `ExitPlanMode` lifts.

## ARCH-HARNESS-008: A read-only resume runs in the main checkout

After lineup removes a Done build Task's worktree, `lineup open` resumes the Worker's conversation read-only in the Repo's main checkout. The adapter trusts the main checkout first. The tab gets no side pane, because the side pane shows the worktree's changes. While the worktree exists, the Worker resumes in it with every tool.

Because: `claude --resume` resumes a conversation from any folder, also after its first folder is gone, and the deny list keeps the main checkout safe.
Not: a resume in an empty folder, which gives the Worker no code to read; a resume in a new worktree, which costs a worktrunk setup and a later removal.

## ARCH-HARNESS-009: Usage comes from the conversation files at each read

The adapter computes a Task's usage from its conversation files at each request, and the Scheduler stores no usage. It reads the conversation file in every project folder, because a resume from another folder writes a file with the same name there. It also reads each subagent file. It counts the tokens of each `message.id` once, because Claude repeats a message's usage in each entry. Agent time is the sum of `durationMs` of the `turn_duration` entries in the conversation's own files, each turn counted once by its `uuid`.

## ARCH-HARNESS-010: Only the start Stage sets the model and the effort

A Worker keeps the model and the effort of the Stage that it starts in: Working for a build Task, Reviewing for a review Task, and Verifying for a QA Task. The config load rejects `model` and `effort` under every other `[stage.<key>]` table. `skill` and `command` stay for each Stage, because the typed prompt carries them. A changed `model` override applies when the Task's next Worker starts.

Because: Claude caches a conversation for one model only, so a switch at a handoff makes the next request write the whole context to the cache again.
Not: `/model` or `/effort` typed at a handoff, which also saves the value as the user's default for new sessions; a resume with a new `--model` at each handoff, which pays the same cache write.
