# Configuration Product Intent

This document states the current intended experience for the one config file, its layers, and how lineup treats a bad file.

## PROD-CONFIG-001: One file configures lineup

lineup reads all of its configuration from one file in the user's config directory. No file in a Repo configures lineup.

## PROD-CONFIG-002: Settings layer from the defaults to the Task

A setting comes from four levels, from lowest to highest: `[defaults]`, the `[repos."<owner>/*"]` table of the owner, the `[repos."<owner>/<repo>"]` table of the Repo, and a Task override. Each key layers on its own, so a higher level can set one key of `[budgets]` and keep the others. A table at each level can hold `[stage.<name>]`, `[budgets]`, `[review_bot]`, `[landing_bar]`, and `[merged]`. A `[stage.<name>]` section at any level beats `[stage.default]` at every level.

## PROD-CONFIG-003: Each key has one level where it belongs

The top level holds the keys that apply once: `roots`, `start_skills`, `linear_client_id`, `[linear.*]`, `[github.labels]`, `local_tracker`, `local_issues_dir`, `[review]`, `[board]`, and `[layout]`. `[defaults]` holds each key that a Repo can override: `wip_cap`, `poll_interval`, `[budgets]`, `[review_bot]`, `[landing_bar]`, `[merged]`, and `[stage.*]`. A Repo table holds those keys, and `teams`, `tracker`, and `prefix`. `teams` and `prefix` need an `<owner>/<repo>` key. A layer key at the top level stops the load, for example with `wip_cap belongs in [defaults]. Run lineup config migrate to move it`.

## PROD-CONFIG-004: A Task override changes one Task

`lineup add --set key=value` sets a Task override before the first prompt of the Worker, and the flag can repeat. `lineup set <task> key=value` changes an override later. An override changes only its own Task, and it beats every level of the config file. A bad override fails the add. For example, `skill=pr-explain` sets the skill of the Task, and `pr=false` gives a Task that ends without a pull request.

## PROD-CONFIG-005: A bad file names its key and changes nothing

lineup checks the whole config file at each load. A bad value, an unknown key, or a removed table stops the load with an error that names it, for example `landing_bar.threads: "any" is not one of ignored, resolved, replied`. `lineup restart` loads the file first, and on an error it prints the error and leaves the Scheduler on the old file. A bad Board layout does not stop the load. The Board then uses the default layout and shows the error on its own line. If the file does not load, the Board runs with defaults and shows `config: <error>`.

## PROD-CONFIG-006: One command moves an old file to the current form

`lineup config migrate` rewrites an old config file once to the current form and keeps its comments. The old forms are `[[repos]]` tables with a `match` glob, layer keys at the top level, `trackers` lists, and `ready` or `queued` in `[board]`. Each old form gives an error that names the command. The command writes a backup first, and it changes nothing if a backup already exists. A rewrite that is not clear fails and names its table. If the new file does not load, the command puts the old file back. A file in the current form stays as it is.

## PROD-CONFIG-007: Changes to the file apply only after a restart

The Scheduler and the Board read the config file only when they start. After the user edits the file or runs `lineup config migrate`, `lineup restart` applies the change to both. `lineup restart scheduler` applies it to the Scheduler alone. `lineup repo init` restarts the Scheduler by itself. A new clone below a root needs no restart.

## PROD-CONFIG-008: One panel shows every setting in groups with plain names

`lineup config` opens the config panel, with a Global tab and a Repo tab. Each tab is a tree of groups with plain names, such as Review bot › Min pass score. Each group starts closed and sums up its settings. Its SET IN column names the level that sets all of them, or `mixed`. The Global tab holds the top-level keys, and the `[defaults]` keys in a group of their own. The Repo tab holds the keys of one Repo from the checkouts below `roots`. Each setting shows the value that applies and the level that sets it.

## PROD-CONFIG-009: The panel writes only a change that loads

The panel changes one key at a time, at a level that the catalog allows for the key, and it refuses every other level. It loads the new file as the Scheduler does, and it writes the file only if that load passes. The write keeps every comment and blank line. On a load error, the panel shows the error and changes nothing. If the file does not load, the panel opens read-only and shows the error at the top. A set in a Repo with no table writes its table.

## PROD-CONFIG-010: The panel offers the restart that a change needs

After a save, the panel names the restart that the change needs: `lineup restart` for a key that the Board or the Hub reads, else `lineup restart scheduler`. Enter runs that restart, and Esc skips it. After a skip, a line in the panel says that the change waits for the restart.

## PROD-CONFIG-011: lineup config keys lists every key

`lineup config keys` lists every key of the config file with its levels, its type, its allowed values, its default, its doc, and the restart that a change needs. `--json` prints the list as JSON, with the doc of each group of the panel. `--repo <owner>/<repo>` adds the value that applies to the Repo and the level that sets it. The list works on a file that does not load, and `--json` then names the load error.

## PROD-CONFIG-012: The panel shows the value of a setting at each level

The selected setting in the config panel shows its doc and one line for each level, from the built-in value up, each with its value there. A mark shows the level that applies. A Stage key also shows each `[stage.default]` value that it falls back to. Then the panel names the key as the file writes it, and the restart that a change needs.

## PROD-CONFIG-013: The panel names each level in plain words

The config panel names each level by a plain name: `built-in` for the default, `your file` for the top level, `all Repos` for `[defaults]`, `all <owner> Repos` for the owner table, and `this Repo` for the Repo table. A Stage key that takes the value of `[stage.default]` adds `Every Stage`, as in `all Repos, Every Stage`. A line below the tabs names the levels of the tab in order, and the last level that sets a value wins. The level picker uses the same names.

## PROD-CONFIG-014: A selected group says what it is

The selected top group in the config panel shows a one-line doc above the rows that it holds. The doc names what the group is and how it relates to the other groups. For example, Merge requirements is the Landing bar, and the review bot's score is one more requirement.

## PROD-CONFIG-015: A row that does not apply says why

The config panel shows a setting that does not apply as a dim row that says why, and it does not edit that row. For a Repo whose Tracker is not linear, Linear teams says `only with the Linear Tracker`. Stage CI debug and Stage Responding each start with the row Model and effort, which says `those of Working (same Worker)`. A group leaves these rows out of its sum.
