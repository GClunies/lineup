# One board: the user's own Issues on the work view

Status: built. This spec adds the user's own Issues to the work view, a quick capture, and a promote from the Local Tracker to Linear. It also adds the description, the status change, and the filter that replace the plan view. The decisions behind it are in `docs/adr/0019-the-board-holds-my-issues.md`, `docs/adr/0020-capture-defaults-to-the-local-tracker.md`, `docs/adr/0021-the-board-has-no-plan-view.md`, and `docs/adr/0023-the-board-has-no-review-view.md`.

## Goal

The user sees all of their own work on one board, from a captured idea to a merged PR. The user plans the what. The Worker plans the how, after the user presses `s`. Paper cuts stay in the Local Tracker, and only work that the team needs goes to Linear.

## Scope

| In this cut | Later |
|---|---|
| Ready and Backlog groups on the work view | Columns in place of groups |
| Linear Issues assigned to the user, in every signed-in workspace | Sync between the Local Tracker and Linear |
| Local Issues, and a new local status, `backlog` | |
| The user's own GitHub Issues, while the GitHub Tracker is on | |
| `s` on an Issue row | A Destination picker in the Orchestrator |
| `n` captures one line at a Destination, `local` by default | A blocked Issue that starts on its blocker's branch as a stack |
| `P` promotes a local Issue to Linear | |
| The `unblocked` mark and the Ready order | |
| A count of the review Tasks that need the user | |
| A toast that names the next Ready Issue when a WIP slot opens | |
| `f` shows an Issue's description, and `c` changes its status | |
| `/` filters the rows of the work view | |

The Board shows the work view only. It has no plan view (ADR 0021) and no review view (ADR 0023).

## Shape

```
 lineup  WIP 4/5   ●1 need you (+1 in reviews)
 ──────────────────────────────────────────────────────────────────────────────────────────────
 NEEDS YOU  1
 ▸ ENG-590 data  Add billing model        stopped                    12m
 BACKLOG  2
   local-9                    Retry failed loads before alerting
   ENG-620              data  Split billing ingest     △4
 READY  3
   ENG-611              data  Move the retry job       unblocked        △2
   local-7                    Rename the log flag
   ENG-614              data  Drop old API keys        blocked by ENG-610  △3
 QUEUED  1
   ah-9                 data  Evaluate the ADR
 WORKING  1
   ENG-592 data  Fix flaky test           #4655 ○ no review yet · fixing CI     1m
 WAITING  1
   ENG-571 data  Rate limit login         #4636 ● commented · 1 open thread   1d
 DONE  1 …
```

Each heading shows its row count. Backlog is open in this picture, and Done is closed. The header has no per-group counts. See "Lay out the groups" below.

An Issue row is an Issue of the user's own with no Task on the Board. The Board reads the Issues itself. The Scheduler and its store do not change.

```
 Linear MyIssues (each workspace) ─┐
 Local Tracker folder ─────────────┼─► Issue rows ─► Ready, Backlog ─► s ─► add + start (Scheduler)
 gh search issues (GitHub Tracker) ┘        ▲
                                            └─ minus each ID that a Task on the Board holds
```

## Issue rows

| Tracker | Ready | Backlog |
|---|---|---|
| Linear, assigned to the user | State type `unstarted` | State type `backlog` or `triage` |
| Local | `status: todo` | `status: backlog` |
| GitHub, the user's own and open | No backlog label | The backlog label, from `[github.labels]` |

- An Issue in a started, completed, or canceled state is in neither group.
- A GitHub Issue is the user's own when the user is an assignee, or when it has no assignee and the user opened it. A pull request gets no row. `docs/spec/trackers.md` ("The Board") gives the details.
- An Issue whose ID a Task on the Board holds has no Issue row. The Task row shows that Issue. A Linear ID is in upper case, for example `LOCAL-5`, a local ID is in lower case, for example `local-5`, and a GitHub ID is `owner/repo#12`, so an exact match names one Tracker.
- A row shows the ID, the Repo when it is known, the title, a note, and the priority. The note is `blocked by <IDs>`, `unblocked`, or empty. A GitHub row's Repo is the repository name, and its note names a blocker in the same repository as `#12`.
- `unblocked` means that the Issue has one or more blockers and that every blocker is done or canceled. A Linear blocker is a "blocks" relation. A local blocker is an entry in `blocked_by`. A GitHub blocker is a `Blocked by #12` line in the body, and it is done when GitHub reports it closed.

The default group order is Needs you, Backlog, Ready, Queued, Working, Waiting, Done. Needs you comes first because it holds the rows that wait for the user. The other groups follow the development cycle, from an idea to a merged PR. The `[board]` table in the global file sets another order. See "Lay out the groups" below.

Both groups sort in this order:

1. Issues with no open blocker, then the others.
2. Priority: urgent, high, medium, low, then no priority. A local or GitHub Issue has no priority.
3. Dependency level. An Issue with no open blocker has level 0. Any other Issue has a level one higher than its highest open blocker. Blockers in other Projects count too.
4. Natural ID order.

### Load and refresh

- The Board paints the Issue rows from the cache of each workspace's My Issues, then fetches them live.
- It fetches again every 60 s, and right after a capture, a promote, a start, a status change, or an edit in `$EDITOR`.
- A failed fetch keeps the rows from the last fetch, and the footer names the workspace that failed.
- The Board reads the Local Tracker folder whatever `local_tracker` says (ADR 0020).
- The Board reads GitHub Issues at each load while the GitHub Tracker is on, and it has no cache for them. A failed read keeps the GitHub rows from the last read, and the footer shows `GitHub Issues:` and the error. The other rows stay. When the Tracker turns off, the GitHub rows leave.

## Keys on an Issue row

| Key | Linear row | Local row | GitHub row |
|---|---|---|---|
| `s` | Start a Task from the Issue. The skill picker opens first, and the Repo picker opens when no mapping exists. Then the Board sends `add` and `start`. | The same | The same |
| `f` | Show the Issue's description from Linear, rendered from Markdown. `f` or `esc` goes back. | Show the body of the Issue's file, rendered from Markdown | Show the Issue's body, rendered from Markdown |
| `c` | Change the status. The list holds the workflow states of the Issue's team. | Change the status. The list holds `backlog`, `todo`, `started`, `in review`, `done`, and `canceled`. | Nothing. A GitHub Issue has no status. |
| `o` | Open the Issue in the browser | Open the Issue's file in `$EDITOR` | Open the Issue in the browser |
| `P` | Nothing | Promote the Issue to Linear. See [Promote](#promote). | Nothing |
| `n` | Capture a new Issue. It works on every row. See [Capture](#capture). | The same | The same |

One table, `issueKinds` in `internal/board/issuekind.go`, decides the keys of each Kind of row. The footer shows these keys in the order of the table. The keys of a Task row are unchanged. A key that does not apply to the selected row does nothing.

### Status change

`c` opens the status list with the cursor on the Issue's status. `j` and `k` move, `enter` sets the status, and `esc` cancels.

- After a change, the Board fetches the Issue rows again. The row moves to its new group, or it leaves the Board when its new status is in neither group.
- A failed read or write names the error in the footer, and the row does not change.

## Filter

`/` opens a filter line above the rows of the work view:

```
 / rename
 READY  1
 ▸ local-7                    Rename the log flag
 DONE  1
   ENG-560 Done         data  Rename config options
```

- The filter matches the ID and the title of each Task and Issue row, as a fuzzy match.
- While the filter holds text, only the matching rows show. Every group with a match is open and shown, whatever `[board]` says, and each heading counts its matches. `j` and `k` skip the headings.
- `enter` keeps the filter and gives the keys back, so the keys work on the matching rows. `esc` clears the filter and gives back the rows and the selection from before the filter.
- Digits and letters in the filter are text, so `q` does not quit.
- lineup does not save the filter.

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

The header counts the build Tasks that need the user. It counts the review Tasks that need the user in parentheses, when there are any: `●2 need you (+1 in reviews)`. While the queues are paused, the header also shows `reviews paused`.

The header has no count for the other groups. Each group heading shows its count.

The Board already holds the Tasks of both Kinds, so this needs no new data.

## Lay out the groups

A long Backlog or Done group can push Ready and Working off a 24-row pane. The board must show first what needs the user. The `[board]` table in the global file sets the order of the groups, which groups start closed, and which groups are hidden. Each group heading is a selectable row, so the user can also close a group for this run.

```toml
[board]
order = ["done", "needs_you", "working", "waiting", "queued", "ready", "backlog"]
collapsed = ["backlog"]
hidden = ["done"]
```

The keys of a group are `needs_you`, `backlog`, `ready`, `queued`, `working`, `waiting`, and `done`. A key that is absent takes its default, and an empty list means none. A group that `order` leaves out follows the listed groups, in the default order. `needs_you` cannot be in `collapsed` or `hidden`. An unknown group, a duplicate in one list, and `needs_you` in `collapsed` or `hidden` are errors that name the key, but they do not fail the load, so the Scheduler still starts. The board then uses the default layout and shows the error on its own line, for example `config: board.hidden: unknown section "dun" (default layout)`. No other status message replaces that line, and it stays until the board quits. If `config.Load` fails for any other reason, the board runs with defaults and shows `config: <error>` in the same line.

| Rule | Behavior |
|---|---|
| Heading | The group name in upper case and its row count, as `BACKLOG  7`. A closed group adds ` …` and hides its rows. A group with no row has no heading. |
| Needs you | It never closes, and its heading is not a selection stop. |
| Keys | `j` and `k` move through the headings and the rows of open groups, and skip a hidden group. `enter` on a heading opens or closes the group. The footer reads `enter open` or `enter close`, then `j/k move`. |
| Order | `order`. The default is the order above. |
| Closed | `collapsed` starts closed at each board start. The default is Backlog and Done. `enter` or a tap on a heading opens or closes a group for this run only. |
| Hidden | `hidden` groups show no heading and no rows, and `j` and `k` skip them. The header counts do not change. The default is none. The filter finds a row in a hidden group. |
| `d` | Shows Done if it is hidden, and opens it. Hides Done if it shows. It changes this run only, whatever `hidden` says. |
| Selection | A Task that moves into a closed group selects that group's heading. Any other lost selection goes to the first row, or to the first heading when every group is closed. |
| Capture | `n` into a closed or hidden Backlog shows and opens Backlog for this run, and selects the new row. |

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
| `internal/github` | `OwnIssues`: the user's own open Issues from two `gh search issues` calls, with each blocker's state from one GraphQL call |
| `internal/linear` | A `Levels` function that returns the dependency level of each Issue. An `Unblocked` fact on `Issue` from the closed "blocks" relations. |
| `internal/board` | Issue rows, their load and refresh, the Ready and Backlog groups, the keys table in `issuekind.go`, the description, the status list, the filter in `filter.go`, the capture box, the Destination list, promote, the group headings and the `[board]` layout, the header count, and the free slot toast |
| `cmd/lineup/wire.go` | The Local Tracker store for the work view, whatever `local_tracker` says |
| `cmd/lineup/hub.go` | The GitHub Tracker check and the backlog label for the work view |
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
| The header names no view, shows the review Tasks' Needs you count and `reviews paused`, has no per-group counts, and fits 80 columns | Board tests that read the header |
| Backlog and Done start closed with their counts, and `enter` on a heading opens or closes the group for this run only | Board tests that read the screen and the footer, and a restart |
| The groups show in the `[board]` order, `collapsed` groups start closed, and `hidden` groups show nothing | Board tests that read the screen at 81 columns |
| A bad `[board]` table loads with the default layout and an error, and the board shows the error until it quits | Config tests, and a board test that reads the screen after other status messages |
| `d` shows or hides Done, and opens it when it shows | Board tests that read the screen before and after `d` |
| `[board]` fails at load on an unknown group, a duplicate, and `needs_you` in `collapsed` or `hidden` | Config tests that read the error |
| `j` and `k` stop on each shown heading except Needs you and skip the rows of closed groups | A board test over the fixture, and a `rapid` property test over random rows, orders, closed sets, and hidden sets |
| The free slot toast names the first Ready row, and only when a slot is free | A board test that sends two board views |
| `1`, `2`, and `3` do nothing | A board test that reads the screen and the command |
| GitHub rows: assigned, unassigned and opened by the user, opened by someone else and unassigned (no row), a PR (no row), the backlog label, `Blocked by`, an Issue that a Task holds, the Tracker off, and a failed `gh` call | Board tests with recorded `gh` output in `internal/github/testdata` |
| `f` shows a Linear description and a local file body, and `f` goes back | Board tests with the fake Linear client and a temporary Local Tracker folder |
| `c` writes the new status, the row moves group or leaves the Board, and a failed write changes nothing | Board tests, and a `rapid` property test over every local status pair |
| The filter shows only the matches, opens a closed group with a match, and `esc` gives back the exact screen | Board tests, and a `rapid` property test over random filters |
| The first paint stays inside the speed budget | `TestSpeedBudget` with 200 Issue rows |
| Live | A dev build of `lineup board` in a Rex pane, read with `rex`, shows the groups with real Linear data, and captures a local Issue into a temporary Local Tracker folder |
