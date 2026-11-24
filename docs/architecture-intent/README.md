# Architecture Intent

This directory is lineup's living authority for intended architecture. It states how lineup's internals must be organized, apart from the current code, delivery plans, and history.

A statement can describe an organization that the code does not meet yet. Code and plans are judged against the statement, not the reverse. A statement that the code does not meet is recorded structural debt.

## Authority boundaries

- Product intent (`docs/product-intent/`) states what experience lineup must give the user.
- Architecture intent states how lineup's internals must be organized to give it.
- Plans in `docs/plans/` state what change is approved for delivery.
- Code and tests give implementation and evidence. They do not define architecture intent.

A statement belongs here only when the user cannot observe it. A rule that the user can observe on the Board, in Rex, in the CLI, or in a file that lineup writes for the user belongs in product intent.

## Corpus contract

- Give each statement one stable ID of the form `ARCH-<DOMAIN>-<NNN>` and one domain file. Keep the ID through wording changes and file moves. Never use an ID again, also after its statement is removed.
- Start each domain file with the title `# <Domain> Architecture Intent` and one intro sentence, `This file states <what the domain covers>.`, in the words of the domain's entry under Domains. Only statements follow, with no other heading.
- Write each statement as a heading `## ARCH-<DOMAIN>-<NNN>: <title>`, then one present-tense rule of at most 100 words, in sentences of at most 35 words. A statement states one rule, so split independent rules into separate statements.
- After the rule, a statement can have one line `Because: <one sentence>` and one line `Not: <rejected designs>`. Add them only when a reader without them is likely to propose the rejected design again.
- Name boundaries, owners, invariants, layers, and modules. A statement can name a command, a config key, a Signal, a flag, or an external tool in backticks. It names no function, no type, and no file path, and it records no history, delivery detail, or acceptance criteria.
- When a statement breaks a cap, split it. The statement keeps its ID for the rule that its title names. Each other rule gets a new ID, and each citation of that rule moves to the new ID. Move the rationale to the `Because:` line and the rejected design to the `Not:` line, and drop the rest.
- Add a statement only when no current statement owns the rule. When the intended meaning changes, rewrite the statement in place. Git history keeps the old wording.
- When a changed intent contradicts or narrows another statement, rewrite each of those statements so that it states the current intent on its own terms. Do not add an exception or a pointer to another statement.
- Do not edit the corpus for work that only restores or keeps the current intent.
- Code, tests, and documents cite a statement by its ID, in parentheses at the end of the sentence that it explains. The body of a commit that changes a statement names its ID. A fix names the statement that it restores.
- The test in `internal/intent` checks the shape of each file, the caps, the IDs, and every citation in the repo.

## Domains

Each entry is a bullet `- [<Domain name>](<domain file>.md): <what the domain covers>.`, in alphabetical order.

- [Configuration](configuration.md): how lineup loads, layers, and checks its configuration.
- [Harness](harness.md): how lineup starts and talks to agent programs.
- [Lifecycle](lifecycle.md): how the lifecycle reducer turns Signals into Stages and effects.
- [Orchestrator](orchestrator.md): how the Orchestrator connects to lineup.
- [QA](qa.md): how QA Tasks are built.
- [Reviews](reviews.md): how review queues and review Tasks are built.
- [Rex](rex.md): how lineup uses Rex sessions, tabs, status records, and the palette.
- [Signals](signals.md): how lineup reads Rex and GitHub, and turns what it reads into Signals.
- [Store](store.md): how lineup records Tasks and their history.
- [Structure](structure.md): how lineup divides its processes and who owns each decision.
- [Trackers](trackers.md): how lineup reaches its Trackers.
- [Worktrees](worktrees.md): how lineup creates and removes worktrees.
