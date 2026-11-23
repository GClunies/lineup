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
───────────────────────────────────────────────────────────────────────────────────────────────
 NEEDS YOU  1
   ENG-590  Add billing model                                              stopped    12m  data
 BACKLOG  2
   ENG-620  Split billing ingest                                           backlog    ▂
   local-9  Retry failed loads before alerting                             backlog
 READY  3
   ENG-611  Move the retry job                                             unblocked  ▂▄▆
   local-7  Rename the log flag                                            ready           data
 ▸ ENG-614  Drop old API keys                                              blocked    ▂▄
 QUEUED  1
   ah-9     Evaluate the ADR                                               queued          data
 WORKING  1
   ENG-592  Fix flaky test                                                 CI debug   1m   data
 WAITING  1
   ENG-571  Rate limit login                                               in review  1d   data
 DONE  1 …
 ──────────────────────────────────────────────────────────────────────────────────────────────
 Drop old API keys
 ENG-614 · Ready · blocked by ENG-610
 s start   f details   c status   o open the Issue   j/k move   n new Issue   ? all keys
```

Each heading shows its row count. Backlog is open in this picture, and Done is closed. The header has no per-group counts. See "Lay out the groups" below. Each row shows a status word, and the strip under the rows shows the selected row, ENG-614. See "The status column and the strip" below.

An Issue row is an Issue of the user's own with no Task on the Board. The Board reads the Issues itself. The Scheduler and its store do not change.

```
 Linear MyIssues (each workspace) ─┐
 Local Tracker folder ─────────────┼─► Issue rows ─► Ready, Backlog ─► s ─► add + start (Scheduler)
 gh search issues (GitHub Tracker) ┘        ▲
                                            └─ minus each ID that a Task on the Board holds
```

## The status column and the strip

Each Task row and Issue row shows one status word in its own column, after the title. A status word has one or two words, except a Tracker state in Follow-up. The title takes the cells that the other columns leave.

| Row | Status word |
|---|---|
| A Task in Needs you with a flag | The reason of the flag: `question`, `permission`, `auth`, `error`, `setup failed`, `stopped`, `budget spent`, `tab closed`, `worker silent`, `draft`, `drafts`, `PR closed` for `pr closed`, or `bot late` for `no bot review` |
| A Task in Follow-up | The Tracker state, for example `Waiting for QA`. The Tracker names the state, so it can have more than two words. |
| Any other Task | The Stage: `queued`, `working`, `CI running`, `CI debug`, `in review`, `responding`, `ready` for Ready to land, or `done` |
| An Issue with an open blocker | `blocked` |
| An Issue whose blockers are all done or canceled | `unblocked` |
| Any other Issue | The group: `ready` or `backlog` |

The status column never drops. A Task row in Needs you and a blocked Issue row show the word in pink. The word carries the state without the color, so the Board reads the same with `NO_COLOR=1`.

The strip is the footer under the rows. It has these lines:

1. A rule.
2. The full title of the selected row.
3. The facts of the selected row, joined by ` · `: the ID, the Stage, the Repo, the PR number, and the full note. An empty fact does not show. The PR number shows only when the note does not name it. An Issue row shows its group in place of the Stage, and its note is `blocked by <IDs>`, `unblocked`, or empty.
4. The Board message, for example `open ENG-590 ok`, while it is set. The next key clears the message.
5. The key hints.

The title and the facts wrap at the width of the Board, so the strip never cuts them. On a group heading, the strip shows the group name and its row count, for example `Working` and `2 rows`. When no row is selected, the strip shows the rule and the key hints. A mode with its own keys shows its own footer in place of the strip, for example the capture box, a picker, or the detail pane.

## Issue rows

| Tracker | Ready | Backlog |
|---|---|---|
| Linear, assigned to the user | State type `unstarted` | State type `backlog` or `triage` |
| Local | `status: todo` | `status: backlog` |
| GitHub, the user's own and open | No backlog label | The backlog label, from `[github.labels]` |

- An Issue in a started, completed, or canceled state is in neither group.
- A GitHub Issue is the user's own when the user is an assignee, or when it has no assignee and the user opened it. A pull request gets no row. `docs/spec/trackers.md` ("The Board") gives the details.
- The Board shows a GitHub Issue only when a checkout below the roots has the Issue's owner/repo as its Repo key. The match ignores case. `lineup add --repo <repo> owner/repo#n` adds an Issue from any repository.
- An Issue whose ID a Task on the Board holds has no Issue row. The Task row shows that Issue. A Linear ID is in upper case, for example `LOCAL-5`, a local ID is in lower case, for example `local-5`, and a GitHub ID is `owner/repo#12`, so an exact match names one Tracker.
- A row shows the ID, the title, a status word, the priority, and the Repo when it is known. The status word is `blocked`, `unblocked`, `ready`, or `backlog`. The strip names the open blockers of the selected row, as `blocked by <IDs>`. A GitHub row's Repo is the repository name, and the strip names a blocker in the same repository as `#12`.
- `unblocked` means that the Issue has one or more blockers and that every blocker is done or canceled. A Linear blocker is a "blocks" relation. A local blocker is an entry in `blocked_by`. A GitHub blocker is a `Blocked by #12` line in the body, and it is done when GitHub reports it closed.

The default group order is Needs you, Follow-up, Backlog, Ready, Queued, Working, Waiting, Done. Needs you comes first because it holds the rows that wait for the user. Follow-up comes next because its Issues still need the user after their PRs merge. The other groups follow the development cycle, from an idea to a merged PR. The `[board]` table in the global file sets another order. See "Lay out the groups" below.

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

## Follow-up

When a build Task's PR merges, the Task goes to Done. Its Issue can still need the user, for example in the Linear state "Waiting for QA". A Done Task whose Issue its Tracker still holds open is in the Follow-up group. The row's status word is the Tracker state. The note in the strip is the Tracker state, then the worktree note when lineup keeps the Task's worktree, for example `Waiting for QA · worktree kept: uncommitted changes in <path>`. See [Merged Tasks](mvp.md#merged-tasks).

| Tracker | The Issue is open when | Tracker state |
|---|---|---|
| Linear | The state type is `unstarted` or `started` | The state name, for example `Waiting for QA` |
| GitHub | The Issue is open | `open` |
| Local | `status` is not `done` or `canceled` | The status, for example `in review` |

- The Task's ID must match the Issue's ID exactly, as for an Issue row.
- Each load of the Issues places the row again. When the Tracker closes the Issue, the row moves to Done.
- The Board reads only the user's own Issues. When an Issue leaves them, for example when someone assigns the Linear Issue to another person, its Task moves to Done.
- Follow-up starts open, and its rows do not fade. On a Follow-up row, `x`, its hint, and the `today` age work as on a Done row.
- Follow-up rows are not in Needs you. They add nothing to the Needs you count in the header or on the `lineup` tab.

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
 ▸ local-7  Rename the log flag                                              ready         data
 DONE  1
   ENG-560  Rename config options                                            done   today  data
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

The keys of a group are `needs_you`, `follow_up`, `backlog`, `ready`, `queued`, `working`, `waiting`, and `done`. A key that is absent takes its default, and an empty list means none. A group that `order` leaves out follows the listed groups, in the default order. `needs_you` cannot be in `collapsed` or `hidden`. An unknown group, a duplicate in one list, and `needs_you` in `collapsed` or `hidden` are errors that name the key, but they do not fail the load, so the Scheduler still starts. The board then uses the default layout and shows the error on its own line, for example `config: board.hidden: unknown section "dun" (default layout)`. No other status message replaces that line, and it stays until the board quits. If `config.Load` fails for any other reason, the board runs with defaults and shows `config: <error>` in the same line.

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
| `[board]` takes `follow_up` in `order`, `collapsed`, and `hidden`. Follow-up is second in the default order, and it starts open. | Config tests that read the layout |
| A Done Task whose Issue is open in Linear, GitHub, or the Local Tracker shows in Follow-up with the Tracker state. It moves to Done when the Tracker closes the Issue. | Board tests that read the screen before and after the Tracker closes the Issue, and a table test over every Linear state type and every local status |
| A Follow-up row adds nothing to the Needs you count | A board test that reads the Status reports and the header |
| A Follow-up row keeps the `x` key, the archive hint, and the `today` age of a Done row | A board test that reads the row and the footer, and presses `x` and `y` |
| `j` and `k` stop on each shown heading except Needs you and skip the rows of closed groups | A board test over the fixture, and a `rapid` property test over random rows, orders, closed sets, and hidden sets |
| The free slot toast names the first Ready row, and only when a slot is free | A board test that sends two board views |
| `1`, `2`, and `3` do nothing | A board test that reads the screen and the command |
| GitHub rows: assigned, unassigned and opened by the user, opened by someone else and unassigned (no row), a PR (no row), the backlog label, `Blocked by`, an Issue that a Task holds, the Tracker off, and a failed `gh` call | Board tests with recorded `gh` output in `internal/github/testdata` |
| `f` shows a Linear description and a local file body, and `f` goes back | Board tests with the fake Linear client and a temporary Local Tracker folder |
| `c` writes the new status, the row moves group or leaves the Board, and a failed write changes nothing | Board tests, and a `rapid` property test over every local status pair |
| The filter shows only the matches, opens a closed group with a match, and `esc` gives back the exact screen | Board tests, and a `rapid` property test over random filters |
| The first paint stays inside the speed budget | `TestSpeedBudget` with 200 Issue rows |
| Each row shows the status word of its Stage or its Needs you reason, and no status word has more than two words | A table test over every Stage and every reason that reads the row on the screen |
| At 95 columns, a row in Needs you and a Working row show their status words in one column, and the title gets at least 45 cells | A board test that measures the cells of the rows |
| A blocked Issue row shows `blocked`, and the strip names its blockers | Board tests with a Linear blocker and a GitHub blocker |
| The strip shows the full title and the full note of the selected row, also at 60 columns, and the facts of a heading | Board tests that read the strip |
| A Board message and the key hints show at the same time, and the next key clears the message | A board test that presses `enter` on a live Task |
| With color off, each status is a word. With color on, a row in Needs you shows its word in pink. | A board test with the color profile of `NO_COLOR` and with a color profile |
| Live | A dev build of `lineup board` in a Rex pane, read with `rex`, shows the groups with real Linear data, and captures a local Issue into a temporary Local Tracker folder |
