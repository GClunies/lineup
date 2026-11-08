# Reviews: the review tab and review queues

Status: built. This spec adds the `review` Hub tab, PR views, and review queues. The decisions behind it are in `docs/adr/0012-a-review-is-a-task-kind.md` and `docs/adr/0014-queues-share-review-slots.md`.

## Goal

The user sees in one place which PRs need them: their own open PRs and the PRs that they must review. Agents start the reviews before the user arrives. A queued review writes nothing to GitHub until the user decides every finding.

## Scope

| In this cut | Later |
|---|---|
| The `review` Hub tab with view tabs | Views that a `[[repos]]` entry or `.lineup.toml` scopes |
| The built-in tabs `Reviews`, `My PRs`, and `Requested` | GitLab and other forges |
| Views and queues in the global config | A queue that runs a skill other than `pr-review` |
| Review Tasks: a Task Kind with its own Stages | |
| Review slots for each queue and for all queues together, apart from the WIP cap | |
| `lineup review <url>` and the `r` key | |
| `pr-review` writes a summary and a findings JSON file. `pr-explain` reads that file. | |

## Shape

```
 ┌──────────────────── Hub tab "review" ────────────────────┐
 │ [Reviews]  My PRs  Requested  My teammates  Q: platform  │  tab / shift+tab, ranked with < >
 │ ▸ data#4945  Write the usage logs   pr-4945 Reviewing       │  a PR row joins its review Task by PR URL
 └──────────────┬───────────────────────────────────────────┘
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
- A review Task is a Task. It shows on the Reviews tab, not on the Board in the `work` tab. It alerts as a toast from the review tab's board, and `enter` focuses its tab.
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
- A view search sorts newest first (`sort:created-desc`). A queue search sorts oldest first (`sort:created-asc`), so that its oldest PRs are inside the first 100.

These rules come from probes of GitHub search with `search(type: ISSUE)`. A repeated `repo:`, `author:`, or `team-review-requested:` qualifier is OR. A repeated `label:` qualifier is AND, and `label:a,b` is OR. Boolean `OR` and parentheses return nothing in this search type. lineup pins `type: ISSUE`, because `ISSUE_ADVANCED` makes a repeated `author:` an AND.

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

The review tab shows these tabs, in this order before the user ranks them:

1. `Reviews`: the review Tasks, in the Board's groups and with the Board's rows. A review whose PR is on no other tab, for example a Done review of a merged PR, shows here.
2. `My PRs`: the user's open PRs in every repository (`author:@me`). Drafts show.
3. `Requested`: the open PRs that request the user, by name or through a team (`review-requested:@me draft:false`).
4. Each `[[review.views]]` entry, in config order.
5. Each `[[review.queues]]` entry, named `Q: <name>`. It lists the queue's ranked PRs. See [Queues](#queues).

The user ranks the tabs with `<` and `>`. A tab that is not in the saved rank, such as a new view, comes after the ranked tabs. lineup keeps the rank in `review_views.json` in the state directory, apart from the tracker browser's `views.json`, so two Board processes never overwrite each other's rank.

### Rows

```
 My PRs  20
 ▸ data#4945   Write the usage logs again       ◷ CI   ✓ 1   pr-4945 Reviewing       2d
   data#4919   Move the CSV coverage readers    ✗ CI                              5d
   backend#25144 Stop using file age as …       draft
 Requested  36
 ▸ data#4955   Pull the base images through …   goldberg        pr-4955 Review ready   1h
```

A header row names the columns. A row shows these columns, in this order, with 3 cells between them:

| Column | Shows |
|---|---|
| `PR` | The short repository name and the number |
| `TITLE` | The title |
| `AUTHOR` | The author, in every view |
| `CI` | The CI state, or `draft` |
| `REVIEW` | `✗ changes` when changes are requested, or the count of approvals |
| `TASK` | The joined review Task, if one exists |
| `AGE` | The age of the PR |

On a narrow screen the columns drop from the right, with their headings, so that the title keeps at least 24 cells.

**The Task join.** A row joins the review Task whose `pr_url` is the row's PR URL. A review Task gets the URL from the moment it is added. A build Task never joins: it belongs to the `work` tab. A joined row shows the Task ID and its Stage, or its Needs you reason. A Done Task that left the Board does not join.

### Keys

On the Reviews tab, the keys are the Board's keys (see `docs/spec/mvp.md`), with `tab`, `shift+tab`, `<`, `>`, and `R` as below. `m` does nothing, because no review Task is Ready to land. `enter` on a review Task in Review ready whose Worker closed reopens the Worker.

On a PR tab:

| Key | Action |
|---|---|
| `tab` `shift+tab` | Open the next or previous view |
| `<` `>` | Move this view's tab left or right, and save the rank |
| `j` `k` | Move the selection |
| `/` | Fuzzy search the rows |
| `enter` | Focus the Worker's tab of the joined Task. If the joined Task is in Review ready and its Worker closed, reopen the Worker, as `r` does. If no Worker runs for the row, open the PR in the browser. |
| `o` | Open the PR in the browser |
| `r` | Start a review Task for this PR now, outside every queue's slots. If the PR has an open review Task, start it if it is Queued, reopen its Worker if the Worker closed in Review ready, or run it again if it stopped for good. Else focus its tab. |
| `R` | Refresh now: search every PR tab again, and ask the Scheduler to poll every started Task |
| `z` | Zoom the Reviewer pane to full height, and back |
| `?` | Show all keys |

### Refresh and cache

- The tab paints first from the disk cache, `cache/github/<view key>.json` in the state directory.
- It searches every PR tab in one GraphQL call when it starts, on `R`, and every 2 minutes. The Reviews tab has no search. Each search reads the first 100 PRs, newest first.
- A cached list never replaces a live one. An empty live list is a result, not a load in progress.
- If a search fails, the tab keeps the last list and shows the error in the status line.

## Queues

```toml
[review]
slots = 5             # at most 5 queue reviews run at once, across all queues

[[review.queues]]     # queues fill in config order, so this one picks first
name = "platform"
repos = ["umbrella/data"]
authors = ["hopper", "noether"]

[[review.queues]]
name = "umbrella/data"
repos = ["umbrella/data"]
slots = 2             # at most 2 of this queue's reviews, so 3 stay free for "platform"
```

A queue is a view whose PRs lineup reviews without being asked. Review Tasks never count toward the WIP cap.

- `review.slots` is optional, an integer of at least 1. It is how many review Tasks that queues added can be started at one time, across all queues. A review that the user starts with `r` or `lineup review` has no queue and does not count.
- A queue's own `slots` is how many of that queue's review Tasks can be started at one time. Its default is `review.slots` when that is set, else 3. Without `review.slots`, each queue has its own count and no shared count applies.
- The queues fill in config order. An earlier queue picks first, so its PRs rank ahead of every later queue's PRs.
- A PR that several queues match belongs to the first of them in config order. No later queue lists, ranks, or starts it.
- To keep slots free for an earlier queue, give each later queue a `slots` that is lower than `review.slots`.

### Which PRs enter a queue

The queue's filters pick the candidates. A candidate enters the queue only when all of these are true:

- It is not a draft, and it does not come from a fork. `pr-review` refuses fork PRs, because a fork's code would run with the user's credentials.
- Its author is not the user and is not a bot.
- No review Task exists for its URL at its current head commit, Done or not. A Task that the user closed at one head stays closed until the author pushes. The user can always start a review again with `r`.
- It wants the user's review. A PR that the user reviewed before wants it only when it requests the user again. Any other PR wants it only when nobody approved it yet. A pending review, a draft that the user has not submitted, does not count as a review.

These rules are the rules of the Herdr `review-queue` plugin, which this queue replaces.

### Ranking

Ranking is inside one queue. A queue's PRs always rank behind the PRs of the queues before it, because those queues fill first.

1. Re-reviews first: PRs that the user reviewed before and that request the user again.
2. Then PRs that request the user by name.
3. Then the oldest PR first.

### Fill

On every full poll, after it reads Task PRs, the Scheduler fills each queue that is not paused. A poll for a few Tasks, for example after a Worker reports `done`, fills nothing.

1. Search every queue in one GraphQL call. Only one fill runs at a time. A fill that is due while another runs is skipped.
2. In one call on the loop goroutine, rank each queue's candidates in config order. Drop from a queue each PR that an earlier queue ranked, so each PR has one owner.
3. In the same call, for each queue in config order: start its Queued review Tasks first, then add and start a review Task for each top-ranked candidate. Stop when the queue's started count reaches its `slots`, or when the started count of all queues reaches `review.slots`.
4. Start at most one review Task in each Repo per fill, because each start fetches into the Repo's main checkout.

Each fill is idempotent. The candidate check, the count, the add, and the start happen in one loop call, so a second fill in the same state starts nothing.

A full queue makes its PRs wait. No fill stops a started review to make room. When a slot frees, the next fill gives it to the first queue that has a candidate, so a PR of an earlier queue starts before a PR of a later queue.

If a queue leaves the config, its started review Tasks go on, and no fill starts another one for it. If `slots` drops, no review stops. Fills start nothing new until the count is below `slots`.

`lineup reviews pause` stops every fill, and `lineup reviews resume` starts them again. A pause stops no review that already started. The pause holds across a restart of the Scheduler. The review tab's header shows `reviews paused` while the pause holds.

When a review Task of a queue ends, its slot is free, and the next fill starts the next PR in queue order and rank. To stop the queue, pause it.

The Scheduler reads the queues and their slots when it starts. After a change to `[[review.queues]]`, run `lineup restart`.

## Review Tasks

A review Task is a Task with Kind `review`. Its ID has the form `pr-<number>`, for example `pr-4955`. If a live Task already has that ID for another repository's PR, the ID adds the repository's name, for example `pr-4955-web`. A new review of a PR whose review Task is Done takes the same ID. It keeps the PR's repository, number, URL, and head commit, and the queue that started it. Its title is `<repo>#<number> <PR title>`. It has no branch. A review that the user starts with `r` or `lineup review` has no queue.

`lineup review` and `r` read the URL as `owner/repo#number`. If a review Task for that PR is open, they add nothing. They start that Task if it is Queued. They reopen its Worker if the Worker closed in Review ready. They run it again if it stopped for good, for example after a failed setup. If it runs, they focus its tab. They refuse a PR from a fork, and a PR that is closed or merged.

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
| Reviewing | `done` or a question, and the findings file exists | Review ready |
| Reviewing | `done`, and no findings file exists | Reviewing, with the Needs you flag `stopped` |
| Reviewing | a question, and no findings file exists | Reviewing, with the Needs you flag `question` |
| Reviewing or Review ready | `pr-reviewed`: the user submitted a review after the Task was added. A pending review does not count. | Done, marked reviewed |
| Reviewing or Review ready | `pr-merged` | Done, marked merged |
| Reviewing or Review ready | `pr-closed`: closed without a merge | Done, marked closed |
| Reviewing | the Worker's tab closes or the Worker is lost | Reviewing, with a fresh Worker from the restart budget, as for a build Task |
| Review ready | the Worker's tab closes, the Worker exits, or recovery finds no tab | Review ready, with no Worker. The Task keeps its slot and still needs the user. |
| Review ready | `reopen`: `lineup review`, `r`, or `enter` on a Task with no live Worker | Review ready, with a Worker that resumes the review's conversation |
| Any Stage | `task-closed` (user key `x`, confirmed) | Done, marked closed |

- `pr-reviewed`, `pr-merged`, and `pr-closed` are levels, not edges. Each full poll computes them from the PR's current state, so a Signal that waits is not lost.
- The Scheduler applies them only while the Worker does not work. The Record keeps the Worker's last program status. The Worker does not work when that status is `done` or `idle`, when the Worker exited, or when the Task has no tab. `blocked` counts as working, because `pr-review` can ask for a permission in its Step 10. `pr-review` posts the review in its Step 9, and a Done in between would close the tab before that step ends.
- A question is the program status `blocked` with the kind `question`. `pr-review` asks for the decision on each finding with `AskUserQuestion`, after it saves the findings file. A question after the findings file waits for the user, as `done` does. A `blocked` permission never meets the contract.
- Review ready is a Stage, not a flag. The Worker's later statuses, for example `working` after the user's answer, never move the Task back to Reviewing. Recovery restarts a lost Worker only in an agent Stage, so a finished review never runs again after a restart.
- `reopen` fits only Review ready. It clears the Task's flag and opens a new tab, and it takes no new slot.
- `rerun` takes only the Stages of the Task's Kind, so a review Task never enters Working, CI running, or any other build Stage.
- When a PR that the user reviewed requests the user again, its review Task is already Done. The queue then starts a new review Task. `pr-review` reads its review record and reviews only what changed.

### The review poll

A review Task polls its PR by number, never by branch, in one GraphQL call for every review Task. The poll reads `state`, `headRefOid`, and the user's reviews with their state and time. The branch poll skips review Tasks, and a review snapshot never goes through the build Signals or their replay. So lineup never marks ready, merges, or debugs CI on a PR that it reviews.

If the PR's head moves after Review ready, the row shows the note `head moved`. The Task stays in Review ready.

### Worker

| Item | Value |
|---|---|
| Rex session | `reviews` |
| Tab label | `pr-4955 data#4955 <title>` |
| Main checkout | The `[[repos]]` checkout whose `origin` is the PR's repository. It is the Worker's folder and the trusted checkout of `pr-review` Step 1. The PR's code never becomes the Worker's folder, because a folder's `.claude` settings and hooks would run. |
| Review worktree | `<main checkout's parent>/<repo>.review-pr-<number>`, detached at the PR's head. lineup makes it and removes it. |
| Prompt | `/pr-review <PR URL>`, the review worktree, then the review contract |
| Harness, model, effort | `[stage.reviewing]`, else `[stage.default]`. The Worker gets `--add-dir <review worktree>`, so it reads the PR's code with no permission prompt. |
| Conversation | Each Reviewing Worker starts a Claude conversation with `--session-id <uuid>`, and the Task's Record keeps the ID. A restart or a rerun starts a new conversation, because it reviews afresh. |
| Reopened Worker | `claude --resume <uuid>` with no prompt, the Reviewing model and effort, and `--add-dir <review worktree>`. Setup runs again first. If the Task has no conversation ID, for example a Task from before conversation IDs, the reopen runs the review again in Reviewing. |
| Hunk pane | `hunk gh pr <PR URL>` in the review worktree, to the right of the Worker in its tab, with half of the tab. See **The hunk pane** below. |

If no `[[repos]]` checkout has the PR's repository as its `origin`, the add fails and names the repository.

**Setup.** Before the first Worker starts, the Task's lane does this, holding a lock for the Repo:

1. `git -C <main checkout> fetch origin pull/<number>/head <base>`
2. If the review worktree is missing, `git worktree add --detach <review worktree> <head>`.
3. Trust the main checkout for Claude, as a build Task trusts its worktree.

The lock keeps two starts from fetching into one checkout at once. A fetch from outside lineup, for example the user's editor, can still hold a ref lock in that checkout. If git reports `cannot lock ref`, lineup tries the fetch again after 0.5 s, 1 s, and 2 s. A failed step gives the flag `setup failed`.

**Cleanup.** At Done, after the tab closes, lineup fetches and then removes the review worktree if it holds no uncommitted change and no commit that exists on no remote. Otherwise it keeps the worktree and puts its path in the Task's note. lineup never runs `wt remove` for a review Task.

**The hunk pane.** Each review Worker gets the PR's diff beside it, so the user reads the diff next to the review. lineup splits the Worker's tab when the Worker starts: on the first start, a restart, a rerun, and a reopen. The Worker keeps the focus. The pane runs this command:

```
sh -c 'GH_TOKEN=$(gh auth token) exec hunk gh pr "$1"' hunk <PR URL>
```

- `hunk` needs a token for a private repository. It reads `GH_TOKEN`.
- The shell in the pane gets the token from `gh`. The token is in no command line that `ps` shows.
- The pane's block has the label `hunk`. lineup keeps its ID, and the Worker's block stays the block of the Task.
- When the Scheduler starts, it opens the pane again beside each live review Worker whose pane is gone.
- The events of the pane never change the Task. Closing the pane alone leaves the review as it is.
- The pane holds the tab open after the Worker's block closes. lineup then closes the tab. Recovery does the same.
- If the split fails, lineup logs a warning. The Worker runs without the pane.

The review contract:

> Use the review worktree <path> as the worktree of `pr-review` Step 1. Check out each new head there, and keep it at Step 10. If the record holds findings at the current head, reuse them. If it holds an older review, review again at the current head. Do `pr-review` Steps 1 to 10. At Step 5, wait for the user's decisions, and write nothing to GitHub before them. The decisions approve the pushes and the post of Steps 6 to 9, so go on to them without asking again. Leave the findings file of `pr-review` behind, also when there are no findings.

The contract output is a file in the PR's `pr-review` record folder: `findings-<head>.json` for the head that the Task pinned when it was added, or any `findings-*.json` whose time is the same as or later than the start of Reviewing. The first case covers a review that reuses findings. lineup checks for the file when the Worker reports `done`, and when a Reviewing Worker asks a question. The folder is `${XDG_STATE_HOME:-~/.local/state}/pr-review/<owner>-<repo>-<number>/`. lineup resolves `XDG_STATE_HOME` from the Worker's environment, which it builds itself, so the two always agree. The file's `head` field names the head that the review read.

## The pr-review and pr-explain change

The skills live in the dotfiles repository, `~/.agents/skills`.

| Skill | Change |
|---|---|
| `pr-review` | Step 2 writes a summary of the PR: What and why, How, and Validation. Step 5 shows the summary before the findings. |
| `pr-review` | Step 1 uses a review worktree that the prompt names, in place of a new one, and Step 10 keeps it. |
| `pr-review` | Step 4 saves the findings as `findings-<head>.json` by `templates/findings.json`, in place of the Markdown file that Step 3 saved. The file holds the summary, the findings with their labels and actions, the recap lists, and the proposed state. |
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
     "ref": {"path": "a.py", "line": 42}, "proof": "...", "action": "commit",
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
| `lineup review <PR URL>` | Adds and starts a review Task with no queue, or reopens the closed Worker of an open review Task |
| `lineup reviews` | Lists each queue with its slots, started reviews, and ranked PRs. When `review.slots` is set, it prints `Reviews: <started>/<slots> started` first, and `--json` adds `slots` and `started` |
| `lineup reviews pause` / `resume` | Stops or restarts every queue fill |

The Reviewer maps "review <url>" and `/pr-review <url>` to `lineup review <url>`. The Orchestrator sends these requests to the Reviewer and does not touch review Tasks. It keeps the `/pr-explain` row of its instructions. In a review Task's tab, the user asks the Worker for `/pr-explain` directly.

## Code changes

| Area | Change |
|---|---|
| `internal/lifecycle` | `Kind` and `Queue` on Task, and `Slots` and `SharedSlots` on Board. The Stages Reviewing and Review ready. `Board.Started()` and `Board.OverCap()` count only build Tasks. `Stage.Started()` stays as it is. `Start` and `Rerun` check the Task's Kind. The Signals `pr-reviewed` and `pr-closed`. |
| `internal/scheduler` | The review PR, head, and review worktree on Record, and the Worker's last program status. The review add path with its dedupe. Review setup and cleanup. The review poll, apart from the branch poll. The contract check by findings file. The single-flight queue fill. The `reviews` settings and ops. |
| `internal/github` | `SearchPRs`: one GraphQL call with one aliased search per view. `TeamMembers`. The review poll by number. |
| `internal/config` | `[[review.views]]` and `[[review.queues]]` in the global file only, with validation at load. `[stage.reviewing]`, and the Stage loop of `Resolve` covers the new Stages. |
| `internal/harness/claude` | The review intro, prompt, and contract. The `pr` override does not apply to a review Task. `--add-dir` for the review worktree. |
| `internal/board` | The review view: a PR list model, its cache, its keys, and the Task join |
| `cmd/lineup` | The `review` Hub tab, `lineup board --review`, `lineup review`, `lineup reviews` |
| Orchestrator instructions | A rule that sends each review request to the Reviewer |
| Reviewer instructions | `cmd/lineup/reviewer/CLAUDE.md` |

The `review` Hub tab has two panes, as the `work` and `plan` tabs do:

- `REVIEWS` on top runs `lineup board --review`.
- `REVIEWER` below runs Claude with the Reviewer role. It adds, steers, reruns, and closes review Tasks, and it pauses and resumes the queues, all through the `lineup` CLI.

The Reviewer's settings deny `lineup add`, `lineup start`, `lineup merge`, and every GitHub write. The reviews run in their own tabs. Only the Reviewer takes a review request. The Orchestrator's settings deny `lineup review` and `lineup reviews`.

## Success criteria and tests

1. **Query builder.** Table tests: each filter alone, two filters together, `author_teams` merged with `authors`, labels as one comma list, and the queue's added qualifiers.
2. **Search.** Against a recorded GraphQL response with three aliased searches, each view gets its own PRs, with author, CI, review decision, and requested reviewers.
3. **Lifecycle.** Table tests for every transition above. Property tests with `rapid`:
   - `start` never succeeds for a queue whose started count is at or above its slots.
   - `start` never succeeds when the started count of all queues is at or above `review.slots`.
   - No review Task ever enters a build Stage, and no build Task ever enters a review Stage.
   - Review Tasks never change `Started()`, so the WIP cap property holds unchanged.
   - Every started review Task ends in Done or in the Needs you group.
   - A review Task never receives an Effect that writes to GitHub.
4. **Queue rules.** Table tests over draft, fork, bot, own PR, open review Task, approved, reviewed, and re-requested PRs, and the ranking order.
5. **Fill.** Two fills in the same state start the same Tasks once. A paused queue starts nothing. Table tests for the shared slots: an earlier queue's PR is added above an older later-queue PR, a PR that two queues match is added once in the first queue, the shared count caps the started reviews of all queues, and a full queue leaves a new PR of the first queue waiting until a slot frees.
6. **Review poll.** Against recorded responses, `pr-reviewed`, `pr-merged`, and `pr-closed` fire as the table says, wait while the Worker works, and fire on the next poll after the Worker stops. A pending review does not fire `pr-reviewed`.
7. **Setup and cleanup.** Two review starts in one Repo fetch one after the other. Cleanup removes a clean review worktree and keeps one with an unpushed commit. A review Task never calls `wt remove`.
8. **Lost tabs.** A closed tab in Review ready leaves the Task in Review ready, and a fill starts no review in its slot. `lineup review` then reopens the Worker in the same conversation. A closed tab in Reviewing starts a fresh Worker. Recovery with no tab in Review ready leaves the Task as it is. The events of the hunk pane leave the Task as it is. A closed Worker block closes the hunk pane with its tab.
9. **Config.** Load and validation: an unknown key, an empty filter list, a bad team or repository form, a duplicate name, and `slots` below 1 each fail at load.
10. **Board.** A render test of the review view with a build Task and a review Task on PRs. Only the review Task joins. `enter` on a joined row sends `focus` for that Task. The Reviews tab lists only the review Task. Typing `q` in the search does not quit.
11. **Speed.** The review view's first paint from the cache meets the Board's 100 ms budget.
12. **Live.**
    - The review tab lists the user's open PRs, and a PR of a running Task shows that Task's Stage.
    - A queue on one repository starts `/pr-review` on a real PR. The Task reaches Review ready with a findings file, and nothing is written to GitHub.
    - Closing that Task's tab keeps the review in Review ready. `enter` on its row reopens the Worker in the same conversation.
