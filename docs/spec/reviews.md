# Reviews: the review tab and review queues

Status: proposed. This spec adds the `review` Hub tab, PR views, and review queues. The decision behind it is in `docs/adr/0012-a-review-is-a-task-kind.md`.

## Goal

The user sees in one place which PRs need them: their own open PRs and the PRs that they must review. Agents start the reviews before the user arrives. A queued review stops before any write to GitHub, so the user decides every finding.

## Scope

| In this cut | Later |
|---|---|
| The `review` Hub tab with view tabs | Views that a `[[repos]]` entry or `.lineup.toml` scopes |
| The built-in views `My PRs` and `Requested` | GitLab and other forges |
| Views and queues in the global config | A queue that runs a skill other than `pr-review` |
| Review Tasks: a Task Kind with its own Stages | |
| Review slots for each queue, apart from the WIP cap | |
| `lineup review <url>` and the `r` key | |
| `pr-review` writes a summary and a findings JSON file. `pr-explain` reads that file. | |

## Shape

```
 ┌─────────────── Hub tab "review" ───────────────┐
 │ [My PRs]  Requested  My teammates  Q: platform │  tab / shift+tab, ranked with < >
 │ ▸ data#4945  Write the usage logs   CI running ENG-614 │  a row joins its Task by PR URL
 └──────────────┬─────────────────────────────────┘
                │ lineup board --review: one GraphQL search per refresh, cached on disk
                │
 config ─ [[review.views]]   filters ─┐
        └ [[review.queues]]  filters + slots
                                      │ the Scheduler searches each queue on every poll
                                      ▼
              Review Task (Kind review) ── Reviewing ──► Review ready ──► Done
                     Worker: /pr-review <url>      findings-<head>.json      reviewed, merged, closed
```

- The Board process runs every view search. The Scheduler runs only the queue searches. Neither waits on the other.
- A review Task is a Task. It shows on the Board, it alerts, and `enter` focuses its tab, as every other Task does.
- A review Task never writes to the PR that it reviews. lineup makes no GitHub write for it. The Worker writes to GitHub only after the user decides each finding, through `pr-review`.

## Filters

A view and a queue take the same five filters. Each filter is a list.

| Key | Matches a PR when | Search qualifier |
|---|---|---|
| `repos` | its repository is in the list | `repo:<owner/name>`, one per item |
| `authors` | its author is in the list | `author:<login>`, one per item |
| `author_teams` | its author is a member of a team in the list | `author:<login>` for each member |
| `requested_teams` | it requests review from a team in the list | `team-review-requested:<org/slug>`, one per item |
| `labels` | it has any label in the list | `label:<a>,<b>` |

- The items of one filter combine with OR. Two filters combine with AND.
- `authors` and `author_teams` are one filter. lineup puts their logins into one OR list.
- Every search adds `is:pr is:open archived:false`.
- A team is `org/slug`, for example `umbrella/platform`. lineup reads the members of each team in `author_teams` once an hour, in one GraphQL call for every team.

These rules come from probes of GitHub search with `search(type: ISSUE)`. A repeated `repo:`, `author:`, or `team-review-requested:` qualifier is OR. A repeated `label:` qualifier is AND, and `label:a,b` is OR. Boolean `OR` and parentheses return nothing in this search type.

## Views

```toml
# ~/.config/lineup/config.toml
[[review.views]]
name = "My teammates"
repos = ["umbrella/data"]
author_teams = ["umbrella/platform"]

[[review.views]]
name = "Data team"
requested_teams = ["umbrella/data"]
labels = ["needs-review"]
```

The review tab shows these view tabs, in this order before the user ranks them:

1. `My PRs`: the user's open PRs in every repository (`author:@me`). Drafts show.
2. `Requested`: the open PRs that request the user, by name or through a team (`review-requested:@me draft:false`).
3. Each `[[review.views]]` entry, in config order.
4. Each `[[review.queues]]` entry, named `Q: <name>`. It lists the queue's ranked PRs. See [Queues](#queues).

The user ranks the tabs with `<` and `>`. lineup keeps the rank in `review_views.json` in the state directory, apart from the tracker browser's `views.json`, so two Board processes never overwrite each other's rank.

### Rows

```
 My PRs  20
 ▸ data#4945   Write the usage logs again       ◷ CI   ✓ 1   ENG-614 CI running   2d
   data#4919   Move the CSV coverage readers    ✗ CI         ENG-590 CI debug     5d
   backend#25144 Stop using file age as …       draft
 Requested  36
 ▸ data#4955   Pull the base images through …   goldberg        R-12 Review ready   1h
```

A row shows these fields, in this order:

1. The short repository name and the number.
2. The title.
3. The author, except in `My PRs`.
4. The CI state and the review decision, or `draft`.
5. The joined Task, if one exists.
6. The age of the PR.

On a narrow screen the fields drop from the right, so that the title keeps at least 24 cells.

**The Task join.** A row joins the Task whose `pr_url` is the row's PR URL. A Task that builds a PR gets its URL from the Scheduler's poll. A review Task gets the URL from the moment it is added. A joined row shows the Task ID and its Stage, or its Needs you reason. A Done Task that left the Board does not join.

### Keys

| Key | Action |
|---|---|
| `tab` `shift+tab` | Open the next or previous view |
| `<` `>` | Move this view's tab left or right, and save the rank |
| `j` `k` | Move the selection |
| `/` | Fuzzy search the rows |
| `enter` | Focus the joined Task's tab. With no joined Task, open the PR in the browser. |
| `o` | Open the PR in the browser |
| `r` | Start a review Task for this PR now, outside every queue's slots |
| `R` | Refresh the view now |
| `?` | Show all keys |

### Refresh and cache

- The tab paints first from the disk cache, `cache/github/<view key>.json` in the state directory.
- It searches every view in one GraphQL call when it starts, on `R`, and every 2 minutes. Each search reads the first 100 PRs, newest first.
- A cached list never replaces a live one. An empty live list is a result, not a load in progress.
- If a search fails, the tab keeps the last list and shows the error in the status line.

## Queues

```toml
[[review.queues]]
name = "platform"
repos = ["umbrella/data"]
authors = ["hopper", "noether"]
slots = 3
```

A queue is a view whose PRs lineup reviews without being asked. `slots` is how many of the queue's review Tasks can be started at one time. The default is 3. Review Tasks never count toward the WIP cap.

### Which PRs enter a queue

The queue's filters pick the candidates. A candidate enters the queue only when all of these are true:

- It is not a draft, and it does not come from a fork. `pr-review` refuses fork PRs, because a fork's code would run with the user's credentials.
- Its author is not the user and is not a bot.
- No review Task for it is open: a review Task that is not Done.
- It wants the user's review. A PR that the user reviewed before wants it only when it requests the user again. Any other PR wants it only when nobody approved it yet.

These rules are the rules of the Herdr `review-queue` plugin, which this queue replaces.

### Ranking

1. Re-reviews first: PRs that the user reviewed before and that request the user again.
2. Then PRs that request the user by name.
3. Then the oldest PR first.

### Fill

On every poll, after it reads Task PRs, the Scheduler fills each queue that is not paused:

1. Search the queue, and rank its PRs.
2. Count the queue's started review Tasks.
3. Add and start a review Task for each top-ranked PR, until the count reaches `slots`.

Each fill is idempotent. A PR that has an open review Task is never a candidate, so a second fill in the same state starts nothing.

`lineup reviews pause` stops every fill, and `lineup reviews resume` starts them again. A pause stops no review that already started. The header shows `reviews paused` while the pause holds.

## Review Tasks

A review Task is a Task with Kind `review`. Its ID has the form `R-<n>`, from the same counter as the ad hoc IDs. It keeps the PR's repository, number, and URL, and the queue that started it. A review that the user starts with `r` or `lineup review` has no queue.

### Stages

| Stage | Owner | Board group | Counts toward |
|---|---|---|---|
| Queued | user | Queued | nothing |
| Reviewing | agent | Working | its queue's slots |
| Review ready | user | Needs you | its queue's slots |
| Done | none | Done | nothing |

A review Task never enters a Stage of a build Task, and a build Task never enters Reviewing or Review ready.

### Transitions

| From | Signal | To |
|---|---|---|
| Queued | `start`, and the queue has a free slot, or the Task has no queue | Reviewing |
| Reviewing | `done`, and the findings file exists | Review ready |
| Reviewing | `done`, and no findings file exists | Reviewing, with the Needs you flag `stopped` |
| Reviewing or Review ready | `pr-reviewed`: the user submitted a review, and the PR does not request the user again | Done, marked reviewed |
| Reviewing or Review ready | `pr-merged` | Done, marked merged |
| Reviewing or Review ready | `pr-closed` | Done, marked closed |
| Review ready | the user closes the Task's tab | Done, marked closed |
| Any Stage | `task-closed` (user key `x`, confirmed) | Done, marked closed |

- The Scheduler applies `pr-reviewed`, `pr-merged`, and `pr-closed` only while the Worker does not work. `pr-review` posts the review in its Step 9 and removes its worktree in its Step 10. A Done Task closes its tab, so a Done that came in between would stop that cleanup.
- Review ready is a Stage, not a flag. Recovery restarts a lost Worker only in an agent Stage, so a finished review never runs again after a restart.
- When a PR that the user reviewed requests the user again, its review Task is already Done. The queue then starts a new review Task. `pr-review` reads its review record and reviews only what changed.

### The review poll

A review Task polls its PR by number, never by branch, in the same GraphQL call as the Task PRs. The poll reads `state`, `headRefOid`, `isDraft`, the requested reviewers by login and team, and the user's latest review. A review Task's poll never makes a lifecycle Signal of a build Task, so lineup never marks ready, merges, or debugs CI on a PR that it reviews.

### Worker

| Item | Value |
|---|---|
| Rex session | `reviews` |
| Tab label | `R-12 data#4955 <title>` |
| Folder | The Repo's main checkout: the `[[repos]]` checkout whose `origin` is the PR's repository. `pr-review` makes and removes its own review worktree. lineup makes no worktree. |
| Prompt | `/pr-review <PR URL>`, then the review contract |
| Harness, model, effort | `[stage.reviewing]`, else `[stage.default]` |

If no `[[repos]]` checkout has the PR's repository as its `origin`, the add fails and names the repository.

The review contract:

> Do `pr-review` Steps 1 to 5 for this PR. Stop at Step 5 and wait for the user's decisions. Write nothing to GitHub before the user decides. Leave the findings file of `pr-review` Step 3 behind.

The contract output is a file `findings-<head>.json` in the PR's `pr-review` record folder, `${XDG_STATE_HOME:-~/.local/state}/pr-review/<owner>-<repo>-<number>/`, written after the Task entered Reviewing.

## The pr-review and pr-explain change

The skills live in the dotfiles repository, `~/.agents/skills`.

| Skill | Change |
|---|---|
| `pr-review` | Step 2 writes a summary of the PR: What and why, How, and Validation. Step 5 shows the summary before the findings. |
| `pr-review` | Step 3 saves the findings as `findings-<head>.json` by `templates/findings.json`, in place of `findings-<head>.md`. The file holds the summary, the findings with their labels and actions, the recap lists, and the proposed state. |
| `pr-explain` | If the record holds `findings-<head>.json` for the PR's current head, Step 2 starts no lanes, and Step 5 takes the findings from the file. Only the page's parts come from new work. |
| Herdr `review-queue` plugin and the `review-queue` skill | Deleted after the live test of this spec passes, so that one queue owns each review. |

`templates/findings.json` uses the field names of the `draw` walkthrough, so `pr-explain` copies each finding with no mapping:

```json
{
  "version": 1,
  "repo": "umbrella/data", "pr": 4955, "head": "<sha>", "mode": "reviewer",
  "summary": {"what_why": "...", "how": "...", "validation": "..."},
  "findings": [
    {"id": 1, "kind": "Blocking", "title": "...", "narrative": "...",
     "ref": {"path": "a.py", "line": 42}, "proof": "...", "action": "Fix via commit",
     "unavailable": {"suggestion": "the fix spans two hunks"}}
  ],
  "recap": {"risks": [], "readers": [], "checks": [], "outside": []},
  "state": "REQUEST_CHANGES"
}
```

`pr-explain` adds each finding's `part` itself, because the parts come from its own model of the system.

## CLI and Orchestrator

| Command | Effect |
|---|---|
| `lineup review <PR URL>` | Adds and starts a review Task with no queue |
| `lineup reviews` | Lists each queue with its slots, started reviews, and ranked PRs |
| `lineup reviews pause` / `resume` | Stops or restarts every queue fill |

The Orchestrator maps "review <url>" and `/pr-review <url>` to `lineup review <url>`. It keeps the `/pr-explain` row of its instructions. In a review Task's tab, the user asks the Worker for `/pr-explain` directly.

## Code changes

| Area | Change |
|---|---|
| `internal/lifecycle` | `Kind` on Task. The Stages Reviewing and Review ready. Slots for each queue in the reducer. `Started()` counts only build Tasks. The Signals `pr-reviewed` and `pr-closed`. |
| `internal/scheduler` | `ReviewPR` and `Queue` on Record. The review add path. The review poll. The contract check by findings file. The queue fill. The `reviews` settings and ops. |
| `internal/github` | `SearchPRs`: one GraphQL call with one aliased search per view. `TeamMembers`. The review poll fields. |
| `internal/config` | `[[review.views]]` and `[[review.queues]]` in the global file only, with validation at load. `[stage.reviewing]`. |
| `internal/harness/claude` | The review prompt and contract |
| `internal/board` | The review view: a PR list model, its cache, its keys, and the Task join |
| `cmd/lineup` | The `review` Hub tab, `lineup board --review`, `lineup review`, `lineup reviews` |
| Orchestrator instructions | The review rows |

The `review` Hub tab holds one pane, `REVIEWS`, which runs `lineup board --review` at full height. It has no agent pane. The reviews run in their own tabs, and the Orchestrator in the `orchestrate` tab takes every request.

## Success criteria and tests

1. **Query builder.** Table tests: each filter alone, two filters together, `author_teams` merged with `authors`, labels as one comma list, and the queue's added qualifiers.
2. **Search.** Against a recorded GraphQL response with three aliased searches, each view gets its own PRs, with author, CI, review decision, and requested reviewers.
3. **Lifecycle.** Table tests for every transition above. Property tests with `rapid`:
   - No queue ever has more started review Tasks than its slots.
   - Review Tasks never change `Started()`, so the WIP cap property holds unchanged.
   - Every started review Task ends in Done or in the Needs you group.
   - A review Task never receives an Effect that writes to GitHub.
4. **Queue rules.** Table tests over draft, fork, bot, own PR, open review Task, approved, reviewed, and re-requested PRs, and the ranking order.
5. **Fill.** Two fills in the same state start the same Tasks once. A paused queue starts nothing.
6. **Review poll.** Against recorded responses, `pr-reviewed`, `pr-merged`, and `pr-closed` fire as the table says, and wait while the Worker works.
7. **Config.** Load and validation: an unknown key, an empty filter list, a bad team or repository form, a duplicate name, and `slots` below 1 each fail at load.
8. **Board.** A render test of the review view with a joined build Task and a joined review Task. `enter` on a joined row sends `focus` for that Task. Typing `q` in the search does not quit.
9. **Speed.** The review view's first paint from the cache meets the Board's 100 ms budget.
10. **Live.**
    - The review tab lists the user's open PRs, and a PR of a running Task shows that Task's Stage.
    - A queue on one repository starts `/pr-review` on a real PR. The Task reaches Review ready with a findings file, and nothing is written to GitHub.
    - Closing that Task's tab ends the review. Its Rex session goes away with its last Task.
