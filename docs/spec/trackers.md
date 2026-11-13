# Trackers: Linear, GitHub Issues, and the Local Tracker

Status: built. This spec adds two Trackers to the MVP, GitHub Issues and the Local Tracker. The decisions behind it are in `docs/adr/0011-trackers-behind-one-port.md`. The Tracker browser that this spec first added is gone (`docs/adr/0021-the-board-has-no-plan-view.md`).

## Goal

The user can plan and start work from Linear, from GitHub Issues, or with no hosted Tracker at all. A user with no Linear workspace and no GitHub Tracker still gets Issue rows on the Board, from the Local Tracker.

## Scope

| In this cut | Later |
|---|---|
| `lineup login` for Linear and GitHub | GitHub Projects, with its Status field |
| GitHub Issues, with add, start, and lifecycle labels | Auto-intake from any Tracker |
| The Local Tracker: Markdown Issues in one folder | Sync between the Local Tracker and a hosted one |
| Orchestrator instructions for each Tracker | |

## Shape

```
     Linear adapter   GitHub adapter (gh)   Local adapter (Markdown files)
          └────────────┼──────────────┼─────────────┘
                       │ scheduler.Tracker port (Lookup, Begin, SetStatus)
                  Tracker mux: routes each call by Issue.Kind
                       │
                   Scheduler
```

- `scheduler.Issue` gets a `Kind` field: `linear`, `github`, or `local`. A Record from before this field is `linear`.
- A Tracker mux in `cmd/lineup/wire.go` implements the existing `scheduler.Tracker` port. It routes each call to the adapter that `Kind` names. The Scheduler does not change.
- The Board reads the user's Linear Issues and the Local Tracker folder for its Issue rows. It reads no GitHub Issue. See [The Board](#the-board).

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

"On" decides only what `lineup trackers` names, and so which Tracker sections the Orchestrator works in. The work view reads the Local Tracker folder whatever "on" says. A Task that links to a local Issue keeps working after the user signs in to Linear, because the Scheduler looks up each Issue by its `Kind`.

## Issue identifiers

| Tracker | Form | Example |
|---|---|---|
| Linear | `<TEAM>-<n>` | `ENG-614` |
| GitHub | `<owner>/<repo>#<n>` | `owner/repo#12` |
| Local | `local-<n>` | `local-7` |

`lineup add` takes any of these forms. An ID that starts with `local-` always names a local Issue, so a typed ID names exactly one Tracker. The Board sends each Issue row's Kind with its add request, so it can still start an Issue from a Linear team keyed `LOCAL`.

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
| `status` | `backlog`, `todo`, `started`, `in review`, `done`, `canceled` | Yes |
| `blocked_by` | Local Issue IDs | No |

- The next ID is one more than the highest ID in the folder. The adapter creates the file with `O_EXCL`, so two writers cannot take one ID.
- A new Issue gets `status: backlog`. The `1 work` view shows a `backlog` Issue in Backlog and a `todo` Issue in Ready.
- The branch name is `local-<n>-<title slug>`, for example `local-7-retry-failed-loads-before-alerting`. A GitHub Issue's branch is `gh-<n>-<title slug>`.
- The adapter reads files that the Orchestrator or the user wrote. The work view leaves out a file that the adapter cannot parse.

## The Board

The work view shows the user's own Linear Issues and the local Issues as Issue rows. It shows no GitHub Issue. A GitHub Issue starts with `lineup add owner/repo#12`, or through the Orchestrator. The GitHub Tracker then sets its labels, as [Lifecycle events](#lifecycle-events) says.

## Planning

The Orchestrator's instructions hold one section for each Tracker. Claude Code reads them only when the session starts, so a hook runs `lineup trackers` on each prompt. Its line names the Trackers that are on now, and the Orchestrator plans only in those. A plan starts no Task, in every Tracker.

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
| `internal/github` | Issue reads and writes through `gh`. It views an Issue, assigns it to the user, and sets the lifecycle labels. |
| `internal/localissues` (new) | The Markdown folder: parse, list, create, set status |
| `internal/board` | The local Issue rows of the work view. See `docs/spec/one-board.md`. |
| `internal/config` | `local_tracker`, `local_issues_dir`, `[github.labels]` |
| `cmd/lineup` | `lineup login [linear [workspace] \| github]`, `lineup logout linear \| github`, `lineup trackers`, ID parsing for `lineup add` |
| Orchestrator instructions | The tables above |

## Success criteria and tests

1. **The mux:** A test adds one Issue of each Kind and checks that each port call reaches only its own adapter.
2. **Old Records:** A Record from before `Kind` loads as a Linear Issue.
3. **IDs:** Table tests for the three forms. A Board start of a Linear row sends the Kind `linear`. An add of `LOCAL-1` with the Kind `linear` reaches the Linear adapter.
4. **GitHub labels:** Against recorded `gh` output, each event adds its label and removes the others. An unmapped event writes nothing.
5. **Local folder:** Property tests show that create never reuses an ID, and that parse after write gives back the same Issue. The list names a broken file and still lists the rest.
6. **When each Tracker is on:** Table tests over Linear signed in or not, `github.json` present or not, and `local_tracker`.
7. **Live:**
   - A local Issue goes from `todo` to `done` through a merged PR in a sandbox repository.
   - A GitHub Issue in that repository goes from open, through `in progress`, to closed by the PR.
