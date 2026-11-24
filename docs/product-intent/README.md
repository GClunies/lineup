# Product Intent

This directory is lineup's living authority for intended product behavior. It states what the user must experience, apart from the current code, the architecture, delivery plans, and history.

A statement can describe behavior that the code does not meet yet. Code and plans are judged against the statement, not the reverse.

## Authority boundaries

- Product intent states what experience lineup must give the user.
- Architecture intent (`docs/architecture-intent/`) states how lineup's internals are organized.
- Plans in `docs/plans/` state what change is approved for delivery.
- `GLOSSARY.md` defines the words that the statements use.
- Code and tests give implementation and evidence. They do not define product intent.

## Corpus contract

- Give each statement one stable ID of the form `PROD-<DOMAIN>-<NNN>` and one domain file. Keep the ID through wording changes and file moves. Never use an ID again, also after its statement is removed.
- Start each domain file with the title `# <Domain> Product Intent` and one intro sentence, `This document states the current intended experience for <what the domain covers>.`, in the words of the domain's entry under Domains. Only statements follow, with no other heading.
- Write each statement as a heading `## PROD-<DOMAIN>-<NNN>: <title>`, then one present-tense rule that the user can observe. The rule has at most 100 words, in sentences of at most 35 words. A statement states one rule, so split independent rules into separate statements.
- Leave out implementation, rationale, history, acceptance criteria, file paths, and delivery detail. A statement has no `Because:` line and no `Not:` line.
- A statement can name a command or a config key that the user types, in backticks. It names no file path and no internal name.
- When a statement breaks a cap, split it. The statement keeps its ID for the rule that its title names. Each other rule gets a new ID, and each citation of that rule moves to the new ID.
- Add a statement only when no current statement owns the behavior. When the intended meaning changes, rewrite the statement in place. Git history keeps the old wording.
- When a changed intent contradicts or narrows another statement, rewrite each of those statements so that it states the current intent on its own terms. Do not add an exception or a pointer to another statement.
- Do not edit the corpus for work that only restores or keeps the current intent.
- Code, tests, and documents cite a statement by its ID, in parentheses at the end of the sentence that it explains. The body of a commit that changes a statement names its ID. A fix names the statement that it restores.
- The test in `internal/intent` checks the shape of each file, the caps, the IDs, and every citation in the repo.

## Domains

Each entry is a bullet `- [<Domain name>](<domain file>.md): <what the domain covers>.`, in alphabetical order.

- [Board](board.md): what the Board shows and how the user moves on it.
- [Configuration](configuration.md): the one config file, its layers, and how lineup treats a bad file.
- [Issues](issues.md): which Issues the Board shows, in which order, and how the user captures and promotes them.
- [Landing bar](landing-bar.md): what a PR must meet before its build Task is Ready to land.
- [Lifecycle](lifecycle.md): how a build Task moves from its start to Done.
- [Orchestrator](orchestrator.md): the one agent that the user talks to in the Hub.
- [Palette](palette.md): the choices for each Task that needs the user, and the actions in the Rex command palette.
- [QA](qa.md): how QA Tasks start after a merge, check the merged change, and end.
- [Repos](repos.md): how lineup finds Repos, sets them up, and picks the Repo of each Task.
- [Reviews](reviews.md): how review queues fill, and how review Tasks run and end.
- [Service](service.md): how the Scheduler runs and recovers, and which tools it needs.
- [Tabs and worktrees](tabs.md): where Tasks live in Rex, and when their tabs and worktrees go away.
- [Trackers](trackers.md): the Trackers that lineup works with, their sign-in, their IDs, and what lineup writes to them.
- [Workers](workers.md): how the Worker of a Task runs, speaks in the user's name, and comes back.
