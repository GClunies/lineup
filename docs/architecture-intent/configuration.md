# Configuration Architecture Intent

This file states how lineup loads, layers, and checks its configuration.

## ARCH-CONFIG-001: One global file holds every setting

lineup reads every setting from one global config file. No file in a Repo configures lineup, and the Repo setup never writes one. The Scheduler, the Board, the Hub, and the CLI load that file through one loader. The loader parses and checks the file once, at the boundary, and the rest of lineup trusts the result.

## ARCH-CONFIG-002: Repos are the only level above the Task

The config file has four layers, lowest first: `[defaults]`, the `[repos."<owner>/*"]` table, the `[repos."<owner>/<repo>"]` table, and the Task override. The Repo key of the Task's checkout picks the tables. The Repo also decides the Tracker and the Rex session of the Task. Each key layers on its own. A `[stage.<name>]` table at any level beats `[stage.default]` at every level.

Because: every Task has exactly one Repo, so a level above Repos adds a concept with no new information.
Not: an Area level above Repos that owns Tracker accounts, Repos, and defaults.

## ARCH-CONFIG-003: The load checks every table, and a restart keeps the old file

The loader decodes the file strictly. An unknown key, a key at the wrong level, and a bad value in any table stop the whole load with an error that names the key. A failed load applies no part of the file. The Scheduler loads the file once, at its start, and keeps that copy. `lineup restart`, `lineup repo init`, and `lineup config migrate` load the new text first. If that load fails, the running Scheduler keeps the old file.

Because: a bad edit must not stop or change a running Scheduler.

## ARCH-CONFIG-004: A migration command replaces old names

The loader accepts only the current form of the file. A `[[repos]]` table, a layer key at the top level, or a `trackers` list stops the load with an error that names `lineup config migrate`. That command rewrites the file once and keeps its comments. It writes a backup first, and it stops with no change if a backup exists. It then loads the new file, and it puts the old file back if the load fails. A file in the current form stays as it is.

Because: an alias keeps two names for one thing in the file forever, and one migration fixes the file once.
Not: old names, such as `ready` and `queued` in `[board]`, kept as aliases of the new names.

## ARCH-CONFIG-005: A bad Board layout never stops the Scheduler

The loader checks the `[board]` table apart from the rest of the file. An unknown group, a duplicate in one list, `needs_you` in `collapsed` or `hidden`, and the old keys `ready` and `queued` are Board errors, not load errors. The load succeeds, and the Scheduler starts. The Board then uses the default layout and shows the error on its own line. If the whole load fails, the Board runs with defaults and shows the load error on that line.

## ARCH-CONFIG-006: The review bot table sits in the table of the Repo

`[review_bot]` is a layer key. It can sit in `[defaults]` and in each `[repos."<key>"]` table, and each of its keys layers on its own. The documented form puts it in the table of the Repos where the bot is installed. A `[review_bot]` table at the top level stops the load. The loader checks each `[review_bot]` table, and a gate that applies to a Repo must resolve to a `name`.

Because: Greptile is installed on some Repos only.
Not: a global `[review_bot]` table as the documented form.

## ARCH-CONFIG-007: The config order of the queues states their priority

The order of the `[[review.queues]]` entries is their priority. Each fill and `lineup reviews` take the queues in that order. The first queue that matches a PR owns it, so no later queue lists, ranks, or starts it. A later queue with a `slots` lower than `review.slots` keeps slots free for the queues before it. The Scheduler reads the queues at its start.

Because: the user can read the priority of the queues directly from the file.
Not: nested queue sources, as one queue with ordered sources.

## ARCH-CONFIG-008: One key catalog describes every key

The config package holds one key catalog: the path, the levels, the type, the allowed values, the default, the doc, and the restart of each key. The panel and `lineup config keys` read only the catalog. The catalog takes each list of allowed values from the code that checks it. A test ties the catalog to the decoder of the loader. A key with no entry, an entry with no key, and an entry at a level where the loader does not read it fail the test.

Because: a second list of keys drifts from the loader.

## ARCH-CONFIG-009: An edit changes the text, and the loader checks it first

An edit of the config file changes the lines of one key and keeps every other line. Before it writes, it checks three things. The new text decodes to the old values with only that key changed. The loader passes the new text. The file on disk still holds the text that the edit read. One rename then writes the new text, so a reader never sees half a file. A key in a form that a line edit cannot change safely, such as an inline table, is refused.

Because: the TOML library cannot write comments back.
Not: a decode of the whole file and an encode of it again.
