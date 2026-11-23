# Configuration Product Intent

This document states the current intended experience for the one config file, its layers, and how lineup treats a bad file.

## PROD-CONFIG-001: One file configures lineup

lineup reads all of its configuration from one file in the user's config directory. No file in a Repo configures lineup.

## PROD-CONFIG-002: Settings layer from the defaults to the Task

A setting comes from four levels, from lowest to highest: `[defaults]`, the `[repos."<owner>/*"]` table of the owner, the `[repos."<owner>/<repo>"]` table of the Repo, and a Task override. Each key layers on its own, so a higher level can set one key of `[budgets]` and keep the others. A table at each level can hold `[stage.<name>]`, `[budgets]`, `[review_bot]`, and `[landing_bar]`. A `[stage.<name>]` section at any level beats `[stage.default]` at every level.

## PROD-CONFIG-003: Each key has one level where it belongs

The top level holds the keys that apply once: `roots`, `start_skills`, `linear_client_id`, `[linear.*]`, `[github.labels]`, `local_tracker`, `local_issues_dir`, `[review]`, `[board]`, `[merged]`, and `[layout]`. `[defaults]` holds each key that a Repo can override: `wip_cap`, `poll_interval`, `[budgets]`, `[review_bot]`, `[landing_bar]`, and `[stage.*]`. A Repo table holds those keys, and `teams`, `tracker`, and `prefix`. `teams` and `prefix` need an `<owner>/<repo>` key. A layer key at the top level stops the load, for example with `wip_cap belongs in [defaults]. Run lineup config migrate to move it`.

## PROD-CONFIG-004: A Task override changes one Task

`lineup add --set key=value` sets a Task override before the first prompt of the Worker, and the flag can repeat. `lineup set <task> key=value` changes an override later. An override changes only its own Task, and it beats every level of the config file. A bad override fails the add. For example, `skill=pr-explain` sets the skill of the Task, and `pr=false` gives a Task that ends without a pull request.

## PROD-CONFIG-005: A bad file names its key and changes nothing

lineup checks the whole config file at each load. A bad value, an unknown key, or a removed table stops the load with an error that names it, for example `landing_bar.threads: "any" is not one of ignored, resolved, replied`. `lineup restart` loads the file first, and on an error it prints the error and leaves the Scheduler on the old file. A bad Board layout does not stop the load. The Board then uses the default layout and shows the error on its own line. If the file does not load, the Board runs with defaults and shows `config: <error>`.

## PROD-CONFIG-006: One command moves an old file to the current form

`lineup config migrate` rewrites an old config file once to the current form and keeps its comments. The old forms are `[[repos]]` tables with a `match` glob, layer keys at the top level, `trackers` lists, and `ready` or `queued` in `[board]`. Each old form gives an error that names the command. The command writes a backup first, and it changes nothing if a backup already exists. A rewrite that is not clear fails and names its table. If the new file does not load, the command puts the old file back. A file in the current form stays as it is.

## PROD-CONFIG-007: Changes to the file apply only after a restart

The Scheduler and the Board read the config file only when they start. After the user edits the file or runs `lineup config migrate`, `lineup restart` applies the change to both. `lineup restart scheduler` applies it to the Scheduler alone. `lineup repo init` restarts the Scheduler by itself. A new clone below a root needs no restart.
