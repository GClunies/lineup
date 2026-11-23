# Repos Product Intent

This document states the current intended experience for how lineup finds Repos, sets them up, and picks the Repo of each Task.

## PROD-REPO-001: lineup finds the checkouts below the roots

The `roots` key lists the folders that hold the user's checkouts, and it has no default. lineup looks for checkouts at each root, in each folder in it, and in each folder in those. A folder with a `.git` folder is a checkout. A folder with a `.git` file is a linked worktree or a submodule, not a checkout. lineup looks inside neither, and it skips each folder whose name starts with a dot. lineup walks the roots each time that it needs the checkouts, so a new clone shows up without a restart. The Repo picker lists these checkouts.

## PROD-REPO-002: The origin remote names each Repo

lineup names each checkout by its `origin` remote, and this name is the Repo key. A GitHub origin gives `<owner>/<repo>`, for example `crexi/data`. A checkout with no origin, or with an origin outside GitHub, gives `local/<folder name>`. A key in the config file is `<owner>/<repo>` for one Repo, or `<owner>/*` for each Repo of one owner. No other glob form exists. Keys ignore case, as GitHub names do, and two keys in the file that differ only in case stop the load.

## PROD-REPO-003: A Repo key has one checkout

Each Repo key names one checkout. A linked worktree is never a checkout, so it never shares the key of its Repo. If two clones have one key, `lineup restart` prints a warning that names both paths. lineup then picks no checkout for that key until the user removes one clone from the roots. A Repo argument that names such a key fails, and the error names both paths and asks for a path.

## PROD-REPO-004: A Repo argument takes a folder name, a key, or a path

`lineup add --repo <name>` and the `repo` field of a local Issue take a folder name, an `<owner>/<repo>` key, or a path. A path starts with `/`, `~/`, or `.`, and it must be a main checkout, so a linked worktree fails with `<path> is not a main git checkout`. If two checkouts have the folder name, the error lists their keys, for example `pass crexi-dev/de-interview-exercise or crexi/de-interview-exercise`. If no checkout has the folder name, the error asks the user to add its parent folder to `roots` or to pass a path.

## PROD-REPO-005: Each Task works in one Repo

Each Task works in exactly one Repo. Work across Repos splits into one Issue for each Repo, and `blocks` relations join these Issues. An ad hoc Task, from `lineup add --repo <repo> "<text>"`, works in the Repo that the user names.

## PROD-REPO-006: The first build Task in a Repo asks the user to set it up

A build Task needs a Repo that is set up. A Repo is set up when the config file has its own `[repos."<owner>/<repo>"]` table, also an empty one. An owner table and a past Task do not count. For a Repo that is not set up, `lineup add` adds nothing and fails with `repo not set up: <owner>/<repo>`. `s` on a Board Issue row then shows `<owner>/<repo> is not set up. Ask the Orchestrator to set it up` and starts nothing. A review Task needs no setup.

## PROD-REPO-007: The setup questions start from detected values

`lineup repo detect <owner>/<repo> --json` prints the detected setup, and each setup question offers the detected value first. The Tracker comes from the owner table, else `github` for a GitHub origin with Issues on. The review bot gate comes from `[defaults]` or the owner table, else `greptile` if Greptile commented on one of the last 20 PRs. The Landing bar checks come from those tables too, else `all` if the default branch requires no check, else `required`. Without a worktrunk setup hook, the Orchestrator offers an ad hoc Task that writes one. The Linear teams question comes only with the Linear Tracker.

## PROD-REPO-008: The Repo setup writes only the user's config file

The Repo setup writes only the config file, never a file in the Repo. `lineup repo init <owner>/<repo> [key=value ...]` adds the table of the Repo after the last Repo table, and keeps every other byte of the file, comments too. With `--defaults`, it writes the values that `lineup repo detect` lists in `init`. It refuses a Repo that already has a table, and an `<owner>/*` key. If the new file does not load, the file stays as it was, and the command prints the error. Otherwise it prints the new table and restarts the Scheduler.

## PROD-REPO-009: lineup picks the Repo of a Linear Issue in fixed steps

The candidates of a Linear Issue are the Repos whose `teams` list holds its team. lineup stops at the first step that names exactly one candidate. The steps, in order, are the only candidate, a `Repo: <owner>/<repo>` line or a `repo:<name>` label on the Issue, and the Repo that the user picked for the Issue's Project. Then come the GitHub links of the Issue, and a model that reads the Issue and each candidate. If no step names one, `lineup add` fails and names the candidates, the Orchestrator asks the user, and the Board opens its Repo picker with only the candidates.

## PROD-REPO-010: lineup records an inferred Repo on the Issue

When the links or the model pick the Repo, lineup writes the line `Repo: <owner>/<repo>` at the end of the Issue's description, and removes each other `Repo:` line. lineup also writes the line when the user passes a candidate with `--repo` that the Issue does not name. The next start then stops at that line. lineup writes nothing when the only candidate, the Issue's own line or label, or the Project's Repo decides.

## PROD-REPO-011: A GitHub or Local Issue gets its Repo from its ID or its file

A GitHub Issue works in the one checkout below the roots whose Repo key is the Issue's repository. If no checkout has the key, `lineup add` fails with `no Repo mapping`, and the user clones the repository below a root or passes `--repo`. If two checkouts have the key, the error names both. A local Issue works in the Repo whose prefix starts its ID, else in the Repo of its `repo` field. If neither fits, the Repo picker opens, and lineup writes the choice to the `repo` field.

## PROD-REPO-012: An Issue whose team is on no Repo takes the Repo of its Project

When no `teams` list holds the team of a Linear Issue, the Issue works in the Repo that the user picked for its Project. Without that Repo, `lineup add` fails with `no Repo mapping`, and the Board's Repo picker lists each Repo whose Tracker is Linear. The Board keeps each pick in its Repo picker as the Repo of the Issue's Project.
