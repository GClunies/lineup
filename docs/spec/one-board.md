# One board: the user's own Issues on the work view

Status: planned. This spec adds the user's own Issues to the `2 work` view, a quick capture, and a promote from the Local Tracker to Linear. The decisions behind it are in `docs/adr/0019-the-board-holds-my-issues.md` and `docs/adr/0020-capture-defaults-to-the-local-tracker.md`.

## Goal

The user sees all of their own work on one board, from a captured idea to a merged PR. The user plans the what. The Worker plans the how, after the user presses `s`. Paper cuts stay in the Local Tracker, and only work that the team needs goes to Linear.

## Scope

| In this cut | Later |
|---|---|
| Ready and Backlog groups on the `2 work` view | Columns in place of groups |
| Linear Issues assigned to the user, in every signed-in workspace | GitHub Issues in Ready and Backlog |
| Local Issues, and a new local status, `backlog` | Sync between the Local Tracker and Linear |
| `s` on an Issue row | A Destination picker in the Orchestrator |
| `n` captures one line at a Destination, `local` by default | A blocked Issue that starts on its blocker's branch as a stack |
| `P` promotes a local Issue to Linear | |
| The `unblocked` mark and the Ready order | |
| One Needs you count across the work and review views | |
| A toast that names the next Ready Issue when a WIP slot opens | |

The `1 plan` view does not change. It still browses the team's Linear views.

## Shape

```
 lineup  WIP 4/5   ●2 need you (+1 in reviews)   ◐2 working   ◷1 waiting   1 queued   3 ready   12 backlog
 ──────────────────────────────────────────────────────────────────────────────────────────────
 NEEDS YOU
 ▸ ENG-590 Working ⚑    data  Add billing model        stopped                    12m
 WORKING
   ENG-592 CI debug     data  Fix flaky test           attempt 2/3                 1m
 WAITING
   ENG-571 In review    data  Rate limit login         #4636 needs approval        1d
 QUEUED
   adhoc-9              data  Evaluate the ADR
 READY
   ENG-611              data  Move the retry job       unblocked        △2
   local-7                    Rename the log flag
   ENG-614              data  Drop old API keys        blocked by ENG-610  △3
 DONE
   ENG-560 merged       data  Rename config options    #4601                    today
 BACKLOG
   local-9                    Retry failed loads before alerting
   ENG-620              data  Split billing ingest     △4
```

An Issue row is an Issue of the user's own with no Task on the Board. The Board reads the Issues itself. The Scheduler and its store do not change.

```
 Linear MyIssues (each workspace) ─┐
                                   ├─► Issue rows ─► Ready, Backlog ─► s ─► add + start (Scheduler)
 Local Tracker folder ─────────────┘        ▲
                                            └─ minus each ID that a Task on the Board holds
```

## Issue rows

| Tracker | Ready | Backlog |
|---|---|---|
| Linear, assigned to the user | State type `unstarted` | State type `backlog` or `triage` |
| Local | `status: todo` | `status: backlog` |

- An Issue in a started, completed, or canceled state is in neither group.
- An Issue whose ID a Task on the Board holds has no Issue row. The Task row shows that Issue. A Linear ID is in upper case, for example `LOCAL-5`, and a local ID is in lower case, for example `local-5`, so an exact match names one Tracker.
- A row shows the ID, the Repo when it is known, the title, a note, and the priority. The note is `blocked by <IDs>`, `unblocked`, or empty.
- `unblocked` means that the Issue has one or more blockers and that every blocker is done or canceled. A Linear blocker is a "blocks" relation. A local blocker is an entry in `blocked_by`.

The group order is Needs you, Working, Waiting, Queued, Ready, Done, Backlog. Backlog is the longest list, so it comes last and never pushes Done off the screen.

Both groups sort in this order:

1. Issues with no open blocker, then the others.
2. Priority: urgent, high, medium, low, then no priority. A local Issue has no priority.
3. Dependency level, as in the tracker browser's My Issues.
4. Natural ID order.

### Load and refresh

- The Board paints the Issue rows from the tracker browser's cache for each workspace's My Issues, then fetches them live.
- It fetches again every 60 s, and right after a capture, a promote, or a start.
- A failed fetch keeps the rows from the last fetch, and the footer names the workspace that failed.
- The Board reads the Local Tracker folder whatever `local_tracker` says (ADR 0020).

## Keys on an Issue row

| Key | Action |
|---|---|
| `s` | Start a Task from the Issue, as `s` in the tracker browser does: the skill picker when `start_skills` lists skills, the Repo picker when no mapping exists, then `add` and `start` |
| `o` | Open the Issue. A Linear Issue opens in the browser, and a local Issue opens in `$EDITOR`. |
| `P` | Promote a local Issue to Linear. See [Promote](#promote). |
| `n` | Capture a new Issue. It works on every row. See [Capture](#capture). |

The keys of a Task row are unchanged. A key that does not apply to the selected row does nothing, as today.

## Capture

`n` opens the capture box in the footer:

```
 new issue: Retry failed loads before alerting
 to: local                                          tab: change   enter: add   esc: cancel
```

- `enter` creates the Issue at the Destination that the box shows.
- `tab` opens the Destination list. Typing filters it. `enter` picks one, and `esc` goes back to the box.
- An empty title creates nothing.

A Destination is one of these:

```
Destination = Local
            | Linear { workspace, team, project? }
```

The list shows `local` first, then the recent Destinations, then every other Destination by workspace, team, and Project:

```
 > bill
   local                     just me
   acme / ENG / Billing      recent
   acme / ENG
   acme / APP / Web
   globex / OPS
```

- The default Destination is always `local`.
- The Board builds the Linear Destinations from each signed-in workspace's teams and each team's open Projects, on the first `tab` of the Board's life.
- `destinations.json` in the state folder keeps the last 5 Destinations that the user picked.
- A local capture writes a new `local-<n>.md` with `status: backlog`.
- A Linear capture creates the Issue in the team, and in the Project when the Destination names one. It assigns the Issue to the user. It sets no status, so the Issue gets the team's default status.
- The footer names the new Issue, for example `added local-9`, and the row shows after the next fetch.

## Promote

`P` on a local Issue row opens the Destination list without `local`. When the user picks a Destination:

1. The Board creates a Linear Issue there with the local Issue's title and body, assigned to the user.
2. The Board sets the local Issue's status to `canceled` and adds the line `Promoted to ENG-612.` to its body.
3. The footer names the new Issue.

If step 1 fails, the local file does not change. If step 2 fails, the footer names both Issues, so the user can cancel the local Issue by hand.

## One Needs you count

The header of each view counts the Needs you Tasks of the other view in parentheses, when there are any:

- The work view: `●2 need you (+1 in reviews)`
- The review view: `●1 need you (+2 in work)`

The Board already holds the Tasks of both Kinds, so this needs no new data.

## The free slot toast

When a build Task goes to Done and the started build Tasks are fewer than the WIP cap, the Board toasts the first Ready row, for example:

```
1 slot free, next ENG-611: Move the retry job
```

With no Ready row, the Board sends no extra toast. The Board does not start the Issue. The user presses `s`.

## Code changes

| Area | Change |
|---|---|
| `internal/localissues` | The `backlog` status. `Create` writes `backlog`. A `Promote` write that sets `canceled` and adds the line to the body. |
| `internal/linear` | A `Levels` function that returns the dependency level of each Issue. An `Unblocked` fact on `Issue` from the closed "blocks" relations. |
| `internal/board` | Issue rows, their load and refresh, the Ready and Backlog groups, the keys, the capture box, the Destination list, promote, the header count, and the free slot toast |
| `cmd/lineup/wire.go` | The Local Tracker store for the work view, whatever `local_tracker` says |
| Docs | `GLOSSARY.md` gets Ready, Backlog, Destination, and Promote. `docs/spec/mvp.md` and `docs/spec/trackers.md` name the new groups and the `backlog` status. |

## Delivery

Each PR lands on `main` before the next one starts.

1. This spec and ADRs 0019 and 0020.
2. Issue rows: the groups, the order, `unblocked`, `s`, `o`, and the `backlog` status.
3. Capture: `n` and the Destination list.
4. Promote: `P`.
5. The Needs you count and the free slot toast.

## Success criteria and tests

| Criterion | Test |
|---|---|
| Each Linear state type and local status lands in the right group, or in none | A table test over every state type and every status |
| An Issue that a Task holds has no Issue row, and `LOCAL-5` never hides `local-5` | A board test with a Task and both Issues |
| Ready sorts unblocked, then priority, then level, then ID | A table test, and a `rapid` property test that the order is total and stable |
| `unblocked` shows only when every blocker is done or canceled | Table tests for Linear relations and local `blocked_by` |
| `s` on an Issue row sends `add` with the right Kind, then `start` | A board test that reads the fake client's ops |
| A local capture writes `status: backlog`. A Linear capture sends the team, the Project, and `AssignToMe`. | Board tests with the fake Linear client and a temporary Local Tracker folder |
| The default Destination is `local`, even after the user picks a Linear one | A board test that captures twice |
| Promote changes the local file only after the Linear create succeeds | A board test with a failing fake create |
| The header shows the other view's Needs you count | A board test for each view |
| The free slot toast names the first Ready row, and only when a slot is free | A board test that sends two board views |
| The first paint stays inside the speed budget | `TestSpeedBudget` with 200 Issue rows |
| Live | A dev build of `lineup board` in a Rex pane, read with `rex`, shows the groups with real Linear data, and captures a local Issue into a temporary Local Tracker folder |
