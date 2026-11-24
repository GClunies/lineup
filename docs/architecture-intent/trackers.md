# Trackers Architecture Intent

This file states how lineup reaches its Trackers.

## ARCH-TRACK-001: Every Tracker sits behind one port

Linear, GitHub Issues, and the Local Tracker are each one adapter behind the Scheduler's one Tracker port. The port looks up an Issue, begins its Task, writes the state for a lifecycle event, reads the description, and creates a fix Issue. A mux implements the port and routes each call to the adapter that the Issue's Kind names. The Scheduler speaks only the port and holds no rule of one Tracker.

Because: the start, status, and create actions are the same for every Tracker, so a path for each Tracker builds them three times.
Not: a separate browser or code path for each Tracker.

## ARCH-TRACK-002: The Local Tracker folder lives outside every Repo

The Local Tracker keeps each local Issue as one Markdown file in one folder of lineup's data directory, outside every Repo. The global key `local_issues_dir` moves the folder. An Issue file gets its Repo from its prefix, else from its `repo` field. The adapter reads files that the Orchestrator or the user wrote, and a reader sets aside a file that it cannot parse.

Because: planning files in a Repo enter the git history of work repositories that other people share.
Not: local Issues in each Repo.

## ARCH-TRACK-003: Each Issue carries its Kind

Each Issue that a Task holds carries its Kind: `linear`, `github`, or `local`. A stored Issue with no Kind is `linear`. When a lookup names no Kind, the mux reads it from the ID, with case ignored. `local-<n>` and `<prefix>-<n>` for each Repo prefix are `local`, `<owner>/<repo>#<n>` is `github`, and every other ID is `linear`. A caller that knows the Kind, for example the Board, sends it, so a Linear team keyed `LOCAL` still starts its Issues.

## ARCH-TRACK-004: lineup writes Tracker state only through maps

The Scheduler sends the same five lifecycle events to every Tracker: `started`, `in_review`, `merged`, `closed`, and `qa_passed`. Each adapter turns an event into state only through its map. A Linear team's status map names a status, and the global GitHub label map names a label. The Local adapter maps `started`, `in_review`, and `merged` to its `status` field, and `closed` and `qa_passed` to nothing. A Linear event that the map leaves out writes nothing. A Stage change that is not an event, and every Needs you flag, writes nothing.

Because: Linear's GitHub automation already moves some statuses, and a status for each Stage floods the team's views and fights that automation.
Not: a Linear status for each Stage.

## ARCH-TRACK-005: A Local Issue file is created exclusively

A new local Issue takes one more than the highest number of its prefix in the folder. The adapter creates its file with `O_EXCL`, so two writers never take one ID. A writer that finds the file taken reads the highest number again and tries the next one. The file name is the ID in lower case, and a new Issue starts with `status: backlog`.

## ARCH-TRACK-006: The Repo pick runs fixed steps before a constrained model

For a Linear Issue, lineup runs fixed steps in order and stops at the first that names exactly one candidate Repo. One candidate needs no read from Linear. Then come the Issue's `Repo:` line or `repo:` label, the Project's Repo, and the GitHub links. Only then a model reads the Issue and the start of each candidate's README. It runs as `claude -p --safe-mode --model haiku --tools ""`, with a JSON schema that allows only the candidates and `unsure`. A failure or a run over 1 minute counts as `unsure`.

Because: a model with no tools and a closed answer set can at worst pick a wrong candidate.
