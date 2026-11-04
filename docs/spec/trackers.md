# Trackers: Linear, GitHub Issues, and the Local Tracker

Status: built. This spec adds two Trackers to the MVP: GitHub Issues and the Local Tracker. The decisions behind it are in `docs/adr/0011-trackers-behind-one-port.md`.

## Goal

The user can plan and start work from Linear, from GitHub Issues, or with no hosted Tracker at all. A user with no Linear workspace and no GitHub Tracker still gets a working `plan` tab: the Local Tracker.

## Scope

| In this cut | Later |
|---|---|
| `lineup login` for Linear and GitHub | GitHub Projects, with its Status field |
| GitHub Issues: browse, start, create, lifecycle labels | Auto-intake from any Tracker |
| The Local Tracker: Markdown Issues in one folder | Sync between the Local Tracker and a hosted one |
| One Tracker browser with tabs from every Tracker | |
| Planner instructions for each Tracker | |

## Shape

```
                ┌──────────── Tracker browser (plan tab) ────────────┐
                │ Linear tabs │ GitHub tabs │ Local tab │ Browse      │
                └──────┬──────────────┬─────────────┬─────────────────┘
                       │ Source interface (list, start, status, create)
          ┌────────────┼──────────────┼─────────────┐
     Linear adapter   GitHub adapter (gh)   Local adapter (Markdown files)
          └────────────┼──────────────┼─────────────┘
                       │ scheduler.Tracker port (Lookup, Begin, SetStatus)
                  Tracker mux: routes each call by Issue.Kind
                       │
                   Scheduler
```

- `scheduler.Issue` gets a `Kind` field: `linear`, `github`, or `local`. A Record from before this field is `linear`.
- A Tracker mux in `cmd/lineup/wire.go` implements the existing `scheduler.Tracker` port. It routes each call to the adapter that `Kind` names. The Scheduler does not change.
- The Tracker browser reads Issues through a `Source` interface. Each adapter implements it. Linear-only features, for example cycles, favorite views, and Projects, stay inside the Linear source.

## Sign-in

| Command | Effect |
|---|---|
| `lineup login` | Asks which Tracker: Linear or GitHub |
| `lineup login linear [workspace]` | The Linear sign-in from the MVP. The workspace argument works as `lineup login <url key>` does now. |
| `lineup login github` | Runs `gh auth status`. If `gh` is signed in, lineup records GitHub as a Tracker in `~/.local/state/lineup/github.json`, with the account's login. If not, it tells the user to run `gh auth login`. |
| `lineup logout github` | Removes `github.json`. It does not sign `gh` out, because lineup needs `gh` for PRs. |
| `lineup logout linear [workspace] [--local]` | Revokes the sign-in at Linear, then forgets the tokens. With `--local`, it only forgets the tokens on this Mac. |
| `lineup trackers` | Names the Trackers that are on |

lineup always needs `gh` for PRs, so a signed-in `gh` does not make GitHub a Tracker. Only `lineup login github` does.

## When each Tracker is on

| Tracker | On when |
|---|---|
| Linear | At least one workspace is signed in |
| GitHub | `github.json` exists |
| Local | No Linear workspace and no GitHub Tracker is on, or the global config sets `local_tracker = true` |

"On" decides only which tabs the Tracker browser shows. A Task that links to a local Issue keeps working after the user signs in to Linear, because the Scheduler looks up each Issue by its `Kind`.

## Issue identifiers

| Tracker | Form | Example |
|---|---|---|
| Linear | `<TEAM>-<n>` | `ENG-614` |
| GitHub | `<owner>/<repo>#<n>` | `owner/repo#12` |
| Local | `local-<n>` | `local-7` |

`lineup add` takes any of these forms. An ID that starts with `local-` always names a local Issue, so a typed ID names exactly one Tracker. The Tracker browser sends each Issue's Kind with its add request, so it can still start an Issue from a Linear team keyed `LOCAL`.

## Repo for each Issue

| Tracker | How lineup picks the Repo | If nothing matches |
|---|---|---|
| Linear | The team or Project mapping, as in the MVP | The fuzzy picker, as in the MVP |
| GitHub | The `[[repos]]` entry whose `origin` remote is the Issue's repository, else a `[[repos]]` checkout with the repository's name when its `origin` matches | `lineup add` fails and names the repository. The user adds it to `[[repos]]`. |
| Local | The Issue's `repo:` field, resolved as `lineup add --repo` resolves a name | The fuzzy picker. lineup writes the choice back to `repo:`. |

## Lifecycle events

The Scheduler sends the same four events to every Tracker: `started`, `in_review`, `merged`, and `closed`.

| Event | Linear | GitHub | Local |
|---|---|---|---|
| Begin (the Task starts) | Assign to the user | Assign to the user (`gh issue edit --add-assignee @me`) | Nothing |
| `started` | The team's status map | The label map | `status: started` |
| `in_review` | The team's status map | The label map | `status: in review` |
| `merged` | The team's status map | The label map. The PR body has `Fixes <owner>/<repo>#<n>`, so GitHub closes the Issue at the merge. | `status: done` |
| `closed` | The team's status map | The label map | Nothing |

The GitHub label map is global, because GitHub has no team statuses:

```toml
[github.labels]
started = "in progress"
in_review = "in review"
```

lineup adds the label for an event and removes the labels of the other events, so an Issue carries at most one lifecycle label. If the map leaves out `started` or `in_review`, lineup writes nothing for that event. If it leaves out `merged` or `closed`, lineup removes the lifecycle labels, so a finished Issue does not keep `in progress`. If the label does not exist in the repository, lineup creates it.

The Working Stage contract for a GitHub Issue adds one rule: the PR body must contain `Fixes <owner>/<repo>#<n>`.

## The Local Tracker

The Local Tracker is one folder, `~/.local/share/lineup/issues/`. The global config key `local_issues_dir` moves it. The folder is outside every Repo, so planning files never enter a Repo's git history.

Each Issue is one file, `local-<n>.md`:

```markdown
---
title: Retry failed loads before alerting
repo: data
status: todo
blocked_by: [local-3]
---

The description, in Markdown.
```

| Field | Values | Required |
|---|---|---|
| `title` | Text | Yes |
| `repo` | A Repo name or path, as `lineup add --repo` takes | No. Without it, the start opens the fuzzy picker. |
| `status` | `todo`, `started`, `in review`, `done`, `canceled` | Yes |
| `blocked_by` | Local Issue IDs | No |

- The next ID is one more than the highest ID in the folder. The adapter creates the file with `O_EXCL`, so two writers can't take one ID.
- The branch name is `local-<n>-<title slug>`, for example `local-7-retry-failed-loads-before-alerting`. A GitHub Issue's branch is `gh-<n>-<title slug>`.
- The adapter reads files that the Planner or the user wrote. A file that it can't parse shows in the Local tab as an error row with the file name.

## Tracker browser

The tab row holds the tabs of every Tracker that is on, in this order:

1. Linear favorites, as in the MVP
2. `GitHub: mine`: the open Issues assigned to the user (`gh search issues --assignee @me --state open`)
3. One `GitHub: <repo>` tab for each `[[repos]]` entry that has a GitHub `origin`
4. `Local`: the local Issues, grouped by `status`, then by `repo`
5. Browse, which lists only Linear teams and Projects. It shows only when a Linear workspace is signed in.

The user ranks the tabs with `<` and `>`, as in the MVP.

| Key | Linear | GitHub | Local |
|---|---|---|---|
| `s` start | Yes | Yes | Yes |
| Status change | The team's statuses | Open or closed | The five `status` values |
| `n` new Issue | Yes | Yes, in a `GitHub: <repo>` tab. `GitHub: mine` has no single repository, so it tells the user to open one. | Yes |
| `o` open | In the browser | In the browser | In `$EDITOR` |
| Detail pane | Markdown | Markdown | Markdown |

## Planner

The Planner's instructions hold one section for each Tracker. Claude Code reads them only when the session starts, so a hook runs `lineup trackers` on each prompt. Its line names the Trackers that are on now, and the Planner works only in those. The Planner never starts Tasks, for every Tracker.

| Job | Linear | GitHub | Local |
|---|---|---|---|
| Read | `linear issue view` | `gh issue view` | Read `local-<n>.md` |
| Split | `linear issue create` | `gh issue create` | Write new `local-<n>.md` files. It takes the next free ID. |
| Criteria | `linear issue update` | `gh issue edit --body-file` | Edit the description |
| Order | `linear issue relation add` | A `Blocked by #<n>` line in the body | `blocked_by` |

## Code changes

| Area | Change |
|---|---|
| `internal/scheduler` | `Issue.Kind`, and a `kind` field on the add request. A Record without `Kind` is `linear`. |
| `cmd/lineup/wire.go` | The Tracker mux, and the GitHub and Local adapters for the port |
| `internal/github` | Issue reads and writes through `gh`: view, list, search, create, edit, labels |
| `internal/localissues` (new) | The Markdown folder: parse, list, create, set status |
| `internal/board` | The `Source` interface for the GitHub and Local tabs. The Linear tabs keep their own code in `tracker.go`. |
| `internal/config` | `local_tracker`, `local_issues_dir`, `[github.labels]` |
| `cmd/lineup` | `lineup login [linear [workspace] \| github]`, `lineup logout linear \| github`, `lineup trackers`, ID parsing for `lineup add` |
| Planner and Orchestrator instructions | The tables above |

## Success criteria and tests

1. **The mux:** A test adds one Issue of each Kind and checks that each port call reaches only its own adapter.
2. **Old Records:** A Record from before `Kind` loads as a Linear Issue.
3. **IDs:** Table tests for the three forms. A browser start of an Issue from a Linear team keyed `LOCAL` reaches the Linear adapter.
4. **GitHub labels:** Against recorded `gh` output, each event adds its label and removes the others. An unmapped event writes nothing.
5. **Local folder:** Property tests show that create never reuses an ID, and that parse after write gives back the same Issue. A broken file becomes an error row.
6. **When each Tracker is on:** Table tests over Linear signed in or not, `github.json` present or not, and `local_tracker`.
7. **Live:**
   - A local Issue goes from `todo` to `done` through a merged PR in a sandbox repository.
   - A GitHub Issue in that repository goes from open, through `in progress`, to closed by the PR.
