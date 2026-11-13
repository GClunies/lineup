# Reviews: review queues and review Tasks

Status: built. This spec adds review queues and review Tasks. The decisions behind it are in `docs/adr/0012-a-review-is-a-task-kind.md`, `docs/adr/0014-queues-share-review-slots.md`, and `docs/adr/0023-the-board-has-no-review-view.md`.

## Goal

A review queue fills itself. Agents start the reviews of the PRs that need the user before the user arrives. A queued review writes nothing to GitHub until the user decides every finding.

## Scope

| In this cut | Later |
|---|---|
| Queues in the global config | Queues that a `[[repos]]` entry or `.lineup.toml` scopes |
| Review Tasks: a Task Kind with its own Stages | GitLab and other forges |
| Review slots for each queue and for all queues together, apart from the WIP cap | A queue that runs a skill other than `pr-review` |
| `lineup review <url>` | |
| `pr-review` writes a summary and a findings JSON file. `pr-explain` reads that file. | |

## Shape

```
 config ─ [[review.queues]]  filters + slots
                                      │ the Scheduler searches each queue on every poll
                                      ▼
              Review Task (Kind review) ── Reviewing ──► Review ready ──► Done
                     Worker: /pr-review <url>      findings-<head>.json      reviewed, merged, closed
                     in its own tab of the Rex session "reviews"
```

- A review Task is a Task. It runs in its own tab in the `reviews` Rex session. The Board lists no review Task. Its header counts the review Tasks that need the user, as `(+1 in reviews)`, and the Board toasts their alerts.
- A review Task never writes to the PR that it reviews. lineup makes no GitHub write for it. The Worker writes to GitHub only after the user decides each finding, through `pr-review`.

## Filters

A queue takes these filter keys. Each key takes the name of a GitHub search qualifier.

| Key | Matches a PR when | Search qualifier |
|---|---|---|
| `state` | its state is `open`, the default and the only state that a queue takes | `is:open` |
| `draft` | `false`: it is not a draft. The queue search adds `draft:false` itself. | `draft:false` |
| `repo` | its repository is in the list | `repo:<owner/name>`, one per item |
| `author` | its author is in the list | `author:<login>`, one per item |
| `author_team` | its author is a member of a team in the list | `author:<login>` for each member |
| `review_requested` | it requests review from the user, by name or through any of the user's teams | `review-requested:<@me or login>` |
| `label` | it has any label in the list | `label:<a>,<b>` |
| `include_regex` | its title matches the pattern | none: lineup drops the PR after the search |
| `exclude_regex` | its title does not match the pattern | none: lineup drops the PR after the search |

- The items of one list combine with OR. Two keys combine with AND.
- `author` and `author_team` are one filter. lineup puts their logins into one OR list.
- `review_requested = "@me"` matches a request to the user by name and a request to any team of the user. GitHub never requests the author, so the user's own PRs never match.
- `include_regex` and `exclude_regex` are RE2 patterns, case-sensitive. `(?i)` makes a pattern ignore case. GitHub search has no regex, so lineup matches the titles itself. A PR that matches `exclude_regex` drops, also when it matches `include_regex`.
- A queue needs at least one of `repo`, `author`, `author_team`, `review_requested`, or `label`. A bad pattern, an unknown `state`, and a key with its old name fail at load. The old names are `repos`, `authors`, `author_teams`, `labels`, `requested_teams`, and `exclude_titles`. The error names the new key.
- A `[[review.views]]` table fails at load. The error says that the Board has no review view and to delete the `[[review.views]]` tables.
- Every search adds `is:pr archived:false`.
- A team is `org/slug`, for example `umbrella/platform`. lineup reads the members of each team in `author_team` once an hour, in one GraphQL call for every team.
- A queue search sorts oldest first (`sort:created-asc`), so that its oldest PRs are inside the first 100.
- A queue search also adds `draft:false -author:@me`. A queue reviews open PRs only, so a queue with a `state` other than `open`, or with `draft = true`, fails at load.

These rules come from probes of GitHub search with `search(type: ISSUE)`. A repeated `repo:` or `author:` qualifier is OR. A repeated `label:` qualifier is AND, and `label:a,b` is OR. Boolean `OR` and parentheses return nothing in this search type. lineup pins `type: ISSUE`, because `ISSUE_ADVANCED` makes a repeated `author:` an AND. In a probe, `review-requested:@me` returned every PR of `user-review-requested:@me` and every PR of the user's team requests, except the user's own PRs.

## Queues

```toml
[review]
slots = 5             # at most 5 queue reviews run at once, across all queues

[[review.queues]]     # queues fill in config order, so this one picks first
name = "platform"
repo = ["umbrella/data"]
author = ["hopper", "noether"]

[[review.queues]]
name = "umbrella/data"
repo = ["umbrella/data"]
slots = 2             # at most 2 of this queue's reviews, so 3 stay free for "platform"
```

A queue is a PR filter whose PRs lineup reviews without being asked. Review Tasks never count toward the WIP cap.

- `review.slots` is optional, an integer of at least 1. It is how many review Tasks that queues added can be started at one time, across all queues. A review that the user starts with `lineup review` has no queue and does not count.
- A queue's own `slots` is how many of that queue's review Tasks can be started at one time. Its default is `review.slots` when that is set, else 3. Without `review.slots`, each queue has its own count and no shared count applies.
- The queues fill in config order. An earlier queue picks first, so its PRs rank ahead of every later queue's PRs.
- A PR that several queues match belongs to the first of them in config order. No later queue lists, ranks, or starts it.
- To keep slots free for an earlier queue, give each later queue a `slots` that is lower than `review.slots`.

### Which PRs enter a queue

The queue's filters pick the candidates. A candidate enters the queue only when all of these are true:

- It is not a draft, and it does not come from a fork. `pr-review` refuses fork PRs, because a fork's code would run with the user's credentials.
- Its author is not the user and is not a bot.
- The user did not skip it. A skip holds the PR out of every queue, at every head, until the user unskips it. See [Skips](#skips).
- No review Task exists for its URL at its current head commit, Done or not. A Task that the user closed with `lineup close` at one head stays closed until the author pushes. A Task that a skip ended holds nothing, because the skip alone holds the PR. A Task that the user's review ended holds nothing at its head. The next rule lets the PR back only when it requests the user again. The user can always start a review again with `lineup review`.
- It wants the user's review. A PR that the user reviewed before wants it only when it requests the user again. Any other PR wants it only when nobody approved it yet. A pending review, a draft that the user has not submitted, does not count as a review.
- No other person's review is open on it. A review is open when a person other than the user and the author requested changes, or commented on the current head and did not approve. Bots do not count. A second review can contradict an open review, so the PR waits until the author pushes a new head or the reviewer approves. `pr-review` stops on the same rule.
- A person who approved and then commented still counts as an approval. GitHub's `latestReviews` shows only the comment, so lineup reads `latestOpinionatedReviews` too.

These rules are the rules of the Herdr `review-queue` plugin, which this queue replaces.

### Skips

The queue can pick a PR in code that the user does not know, or has no time to learn. The user skips that PR. A skip holds the PR out of every queue until the user unskips it.

- A skip keys on the PR alone, `owner/repo#number`. It holds at every head, in every queue, and across a restart of the Scheduler. The URL of a skip ignores case.
- A skip writes nothing to GitHub. lineup reads the PR once, to take its URL and title and to check that it is open.
- If the PR has an open review Task, the skip ends that Task in the same loop call, as `lineup close` does: the Task goes to Done, the Scheduler closes its tab, and it removes the review worktree. The Task's outcome is `skipped`. A skip leaves a Done review as it is.
- A Task that a skip ended holds nothing. So an unskip lets the next fill add the PR again at the same head.
- A skip and an unskip are idempotent. A second skip changes nothing, except that it ends a review Task that is still open. An unskip of a PR that is not skipped is not an error.
- `lineup review` reviews a skipped PR, and keeps the skip. While that review Task is open it holds the PR. When it ends, the skip holds the PR again.
- A skip never expires. The user unskips a PR that merged or closed with `lineup reviews unskip <PR>`.
- The state is the `skips` table of the Scheduler's database: the PR URL, its title, and the time of the skip.

### Ranking

Ranking is inside one queue. A queue's PRs always rank behind the PRs of the queues before it, because those queues fill first.

1. Re-reviews first: PRs that the user reviewed before and that request the user again.
2. Then PRs that request the user by name.
3. Then the oldest PR first.

### Fill

On every full poll, after it reads Task PRs, the Scheduler fills each queue that is not paused. A poll for a few Tasks, for example after a Worker reports `done`, fills nothing.

1. Search every queue, each in its own GraphQL call, at the same time. Only one fill runs at a time. A fill that is due while another runs is skipped. If a search fails, the fill logs the error and starts nothing. Without the PRs of an earlier queue, a later queue can take a PR that the earlier queue owns.
2. In one call on the loop goroutine, rank each queue's candidates in config order. Drop each skipped PR. Drop from a queue each PR that an earlier queue ranked, so each PR has one owner.
3. In the same call, for each queue in config order: start its Queued review Tasks first, then add and start a review Task for each top-ranked candidate. Stop when the queue's started count reaches its `slots`, or when the started count of all queues reaches `review.slots`.
4. Start at most one review Task in each Repo per fill, because each start fetches into the Repo's main checkout.

Each fill is idempotent. The candidate check, the count, the add, and the start happen in one loop call, so a second fill in the same state starts nothing.

A full queue makes its PRs wait. No fill stops a started review to make room. When a slot frees, the next fill gives it to the first queue that has a candidate, so a PR of an earlier queue starts before a PR of a later queue.

If a queue leaves the config, its started review Tasks go on, and no fill starts another one for it. If `slots` drops, no review stops. Fills start nothing new until the count is below `slots`.

`lineup reviews pause` stops every fill, and `lineup reviews resume` starts them again. A pause stops no review that already started. The pause holds across a restart of the Scheduler. The Board's header shows `reviews paused` while the pause holds.

When a review Task of a queue ends, its slot is free, and the next fill starts the next PR in queue order and rank. To stop the queue, pause it.

The Scheduler reads the queues and their slots when it starts. After a change to `[[review.queues]]`, run `lineup restart`. `lineup restart` loads the config first. If the file is bad, it prints the error and leaves the Scheduler running on the old file. The Orchestrator can edit `[review]` and `[[review.queues]]` for the user: its settings allow it to read and edit the config file, and nothing else in that folder.

## Review Tasks

A review Task is a Task with Kind `review`. Its ID has the form `pr-<number>`, for example `pr-4955`. If a Task with that ID for another repository's PR is live or keeps its tab, the ID adds the repository's name, for example `pr-4955-web`. A new review of a PR whose review Task is Done takes the same ID. The Task keeps the PR's repository, number, URL, and head commit, and the queue that started it. Its title is `<repo>#<number> <PR title>`. It has no branch. A review that the user starts with `lineup review` has no queue.

`lineup review` reads the URL as `owner/repo#number`. If a review Task for that PR is open, it adds nothing. It starts that Task if it is Queued. It restores the Task's Worker if the Worker closed in Review ready, or in Reviewing with the flag `tab closed`. It runs the Task again if the Task stopped for good, for example after a failed setup. If the Task runs, it focuses the Task's tab. It refuses a PR from a fork, and a PR that is closed or merged.

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
| Reviewing or Review ready | `pr-reviewed`: the user submitted a review after the Task was added: an approval, a comment, or a request for changes. A pending review does not count. | Done, marked reviewed. The Scheduler closes the tab and removes the review worktree. |
| Reviewing or Review ready | `pr-merged` | Done, marked merged. The Scheduler closes the tab, which stops the Worker, and removes the review worktree. |
| Reviewing or Review ready | `pr-closed`: closed without a merge | Done, marked closed. The Scheduler closes the tab, which stops the Worker, and removes the review worktree. |
| Reviewing | the Worker's tab closes, or recovery finds no tab | Reviewing, with the Needs you flag `tab closed` and no Worker. lineup opens no tab. |
| Review ready | the Worker's tab closes, the Worker exits, or recovery finds no tab | Review ready, with no Worker. The Task keeps its slot and still needs the user. |
| Reviewing or Review ready | `open`: `lineup open` or `lineup review` on a Task with no live Worker | The same Stage, with a Worker that resumes the review's conversation. The flag `tab closed` clears. |
| Any Stage except Done | `task-closed` (`lineup close`) | Done, marked closed. The Scheduler closes the tab and removes the review worktree. |
| Any Stage except Done | `task-closed` from a skip (`lineup reviews skip`) | Done, marked skipped. The Scheduler closes the tab and removes the review worktree. |
| Done | `task-closed` (`lineup close`) or `tab-closed`: the user closed the Task's tab in Rex | Done, with the same outcome. The Scheduler closes the tab, removes the review worktree, and removes the Repo session if it holds no other tab. |

- `pr-reviewed`, `pr-merged`, and `pr-closed` are levels, not edges. Each full poll computes them from the PR's current state, so a Signal that waits is not lost.
- The Scheduler applies `pr-merged` and `pr-closed` at once, also while the Worker works. It applies `pr-reviewed` only while the Worker does not work. The Record keeps the Worker's last program status. The Worker does not work when that status is `done` or `idle`, when the Worker exited, or when the Task has no tab. `blocked` counts as working, because `pr-review` can ask for a permission in its Step 10. `pr-review` posts the review in its Step 9, and a Done in between would close the tab before that step ends. See [ADR 0024](../adr/0024-every-review-end-closes-the-review-tab.md).
- A question is the program status `blocked` with the kind `question`. `pr-review` asks for the decision on each finding with `AskUserQuestion`, after it saves the findings file. A question after the findings file waits for the user, as `done` does. A `blocked` permission never meets the contract.
- Review ready is a Stage, not a flag. The Worker's later statuses, for example `working` after the user's answer, never move the Task back to Reviewing. Recovery flags a lost Worker only in an agent Stage, and opens no tab, so a finished review never runs again after a restart.
- `open` fits every started review Task. It opens a new tab, clears the Worker's flags, and takes no new slot. A Task in Review ready with no conversation ID reviews again in Reviewing.
- `rerun` takes only the Stages of the Task's Kind, so a review Task never enters Working, CI running, or any other build Stage.
- When a PR that the user reviewed requests the user again, its review Task is already Done, and its tab is closed. The queue starts a new review Task at the next fill, at the same head or a new one. `pr-review` reads its review record and reviews only what changed. See [ADR 0022](../adr/0022-the-users-review-closes-the-review-tab.md).

### The review poll

A review Task polls its PR by number, never by branch, in one GraphQL call for every review Task. The poll reads `state`, `headRefOid`, and the user's reviews with their state and time. The branch poll skips review Tasks, and a review snapshot never goes through the build Signals or their replay. So lineup never marks ready, merges, or debugs CI on a PR that it reviews.

If the PR's head moves after Review ready, the Task gets the note `head moved`. The Task stays in Review ready.

### Worker

| Item | Value |
|---|---|
| Rex session | `reviews` |
| Tab label | `pr-4955 data#4955 <title>` |
| Main checkout | The `[[repos]]` checkout whose `origin` is the PR's repository. It is the Worker's folder and the trusted checkout of `pr-review` Step 1. The PR's code never becomes the Worker's folder, because a folder's `.claude` settings and hooks would run. |
| Review worktree | `<main checkout's parent>/<repo>.review-pr-<number>`, detached at the PR's head. lineup makes it and removes it. |
| Prompt | `/pr-review <PR URL>`, the review worktree, then the review contract |
| Harness, model, effort | `[stage.reviewing]`, else `[stage.default]`. The Worker gets `--add-dir <review worktree>`, so it reads the PR's code with no permission prompt. |
| Conversation | The first Reviewing Worker starts a Claude conversation with `--session-id <uuid>`, and the Task's Record keeps the ID. A rerun of Reviewing types the review prompt into the same Worker and keeps the conversation. `lineup open` resumes it. If the conversation file is gone, the Worker starts a new conversation. |
| Restored Worker | `claude --resume <uuid>` with no prompt, the Reviewing model and effort, and `--add-dir <review worktree>`. Setup runs again first. If the Task has no conversation ID, the restore runs the review again in Reviewing. A Task from before conversation IDs is one example. A Worker that exited and left its tab open for the hunk pane has that tab closed first. So the Task's window is the new tab. |
| Hunk pane | `hunk gh pr <PR URL>` in the review worktree, to the right of the Worker in its tab, with half of the tab. See **The hunk pane** below. |

If no `[[repos]]` checkout has the PR's repository as its `origin`, the add fails and names the repository.

**Setup.** Before the first Worker starts, the Task's lane does this, holding a lock for the Repo:

1. `git -C <main checkout> fetch origin pull/<number>/head <base>`
2. If the review worktree is missing, `git worktree add --detach <review worktree> <head>`.
3. Trust the main checkout for Claude, as a build Task trusts its worktree.

The lock keeps two starts from fetching into one checkout at once. A fetch from outside lineup, for example the user's editor, can still hold a ref lock in that checkout. If git reports `cannot lock ref`, lineup tries the fetch again after 0.5 s, 1 s, and 2 s. A failed step gives the flag `setup failed`.

**Cleanup.** A review Task that ends loses its tab, its hunk pane, and its review worktree at once, at each end in the Transitions table. lineup closes the tab and fetches. It removes the review worktree if the worktree holds no uncommitted change and no commit that exists on no remote. Otherwise it keeps the worktree and puts its path in the Task's note. lineup never runs `wt remove` for a review Task.

**The hunk pane.** Each review Worker gets the PR's diff beside it, so the user reads the diff next to the review. lineup splits the Worker's tab when it opens the tab. That happens on the first start and on each restore. A rerun types into the same tab, so it keeps its pane. The Worker keeps the focus. The pane runs this command:

```
sh -c 'GH_TOKEN=$(gh auth token) exec hunk gh pr "$1"' hunk <PR URL>
```

- `hunk` needs a token for a private repository. It reads `GH_TOKEN`.
- The shell in the pane gets the token from `gh`. The token is in no command line that `ps` shows.
- The pane's block has the label `hunk`. lineup keeps its ID, and the Worker's block stays the block of the Task.
- When the Scheduler starts, it opens the pane again beside each live review Worker whose pane is gone.
- The events of the pane never change the Task. Closing the pane alone leaves the review as it is.
- The pane holds the tab open after the Worker's block closes. lineup then closes the tab. Recovery does the same.
- When the user closes the Worker's pane of a Done review, lineup closes the hunk pane's tab and removes the review worktree.
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
| `lineup review <PR URL>` | Adds and starts a review Task with no queue, or restores the closed Worker of an open review Task |
| `lineup open <task>` | Focuses the review Task's tab, or restores its closed Worker |
| `lineup reviews` | Lists each queue with its slots, started reviews, and ranked PRs, then the skips, then the repositories that have no checkout (`Unmapped:`). When `review.slots` is set, it prints `Reviews: <started>/<slots> started` first. `--json` has `slots` when it is set, and always has `started`, the started review Tasks of all queues, and `skips`. `unmapped` lists the repositories that have no checkout |
| `lineup reviews pause` / `resume` | Stops or restarts every queue fill |
| `lineup reviews skip <PR>` | Skips the PR, and ends its open review Task. `<PR>` is a PR URL or `owner/repo#number`. It refuses a PR that GitHub does not have, and a PR that is not open. |
| `lineup reviews unskip <PR>` | Lets the queues pick the PR again, at the next fill |

The Orchestrator maps "review <url>" and `/pr-review <url>` to `lineup review <url>`. It maps "skip <PR>" and "unskip <PR>" to `lineup reviews skip <PR>` and `lineup reviews unskip <PR>`. It asks the user before a skip that closes a review Task. It keeps the `/pr-explain` row of its instructions. In a review Task's tab, the user asks the Worker for `/pr-explain` directly.

## Code changes

| Area | Change |
|---|---|
| `internal/lifecycle` | `Kind` and `Queue` on Task, and `Slots` and `SharedSlots` on Board. The Stages Reviewing and Review ready. `Board.Started()` and `Board.OverCap()` count only build Tasks. `Stage.Started()` stays as it is. `Start` and `Rerun` check the Task's Kind. The Signals `pr-reviewed` and `pr-closed`. |
| `internal/scheduler` | The review PR, head, and review worktree on Record, and the Worker's last program status. The review add path with its dedupe. Review setup and cleanup. The review poll, apart from the branch poll. The contract check by findings file. The single-flight queue fill. The `reviews` settings and ops. |
| `internal/github` | `SearchPRs`: one GraphQL call for each queue, run at the same time. `TeamMembers`. The review poll by number. `Rank(prs, Holds)` applies the queue rules. `Holds` is the review Tasks and the skips. |
| `internal/config` | `[[review.queues]]` in the global file only, with validation at load. A `[[review.views]]` table fails at load. `[stage.reviewing]`, and the Stage loop of `Resolve` covers the new Stages. |
| `internal/harness/claude` | The review intro, prompt, and contract. The `pr` override does not apply to a review Task. `--add-dir` for the review worktree. |
| `internal/board` | The header count of the review Tasks that need the user, and `reviews paused` |
| `cmd/lineup` | `lineup review`, `lineup reviews` |
| Orchestrator instructions | The review jobs in `cmd/lineup/agent/CLAUDE.md` |

The Orchestrator in the `hub` tab adds, steers, reruns, and closes review Tasks, and it pauses and resumes the queues, all through the `lineup` CLI. Its settings deny every GitHub write to a PR. The reviews run in their own tabs.

## Success criteria and tests

1. **Query builder.** Table tests: each filter alone, two filters together, `author_team` merged with `author`, labels as one comma list, each `state` and `draft` value, `review_requested`, and the queue's added qualifiers. Title regexes: table tests, and a `rapid` property test that a kept PR matches `include_regex` and does not match `exclude_regex`.
2. **Search.** Against a recorded GraphQL response with three aliased searches, each search gets its own PRs, with author and requested reviewers.
3. **Lifecycle.** Table tests for every transition above. Property tests with `rapid`:
   - `start` never succeeds for a queue whose started count is at or above its slots.
   - `start` never succeeds when the started count of all queues is at or above `review.slots`.
   - No review Task ever enters a build Stage, and no build Task ever enters a review Stage.
   - Review Tasks never change `Started()`, so the WIP cap property holds unchanged.
   - Every started review Task ends in Done or in the Needs you group.
   - A review Task never receives an Effect that writes to GitHub.
4. **Queue rules.** Table tests over draft, fork, bot, own PR, open review Task, approved, reviewed, and re-requested PRs, and the ranking order. A skipped PR never ranks, at any head. A Task that a skip ended holds nothing, a Task that the user's review ended lets the PR back only on a new request, and a Task that `lineup close` closed holds its head. Property tests with `rapid` check both.
5. **Fill.** A skipped PR is never added, at any head, also after a restart of the Scheduler. A skip ends the open review Task in one loop call, and an unskip lets the next fill add the PR at the same head. A skip writes nothing to GitHub. Two fills in the same state start the same Tasks once. A paused queue starts nothing. Table tests for the shared slots: an earlier queue's PR is added above an older later-queue PR, a PR that two queues match is added once in the first queue, the shared count caps the started reviews of all queues, and a full queue leaves a new PR of the first queue waiting until a slot frees.
6. **Review poll.** Against recorded responses, `pr-reviewed`, `pr-merged`, and `pr-closed` fire as the table says. `pr-reviewed` waits while the Worker works and fires on the next poll after the Worker stops. `pr-merged` and `pr-closed` fire while the Worker works. A pending review does not fire `pr-reviewed`.
7. **Setup and cleanup.** Two review starts in one Repo fetch one after the other. Cleanup removes a clean review worktree and keeps one with an unpushed commit. A review Task never calls `wt remove`.
8. **Lost tabs.** A closed tab in Review ready leaves the Task in Review ready, and a fill starts no review in its slot. `lineup review` then restores the Worker in the same conversation. A closed tab in Reviewing flags the Task `tab closed` and opens no tab, and `lineup open` or `lineup review` then resumes the Worker. Recovery with no tab in Review ready leaves the Task as it is, and in Reviewing it sets the flag. The events of the hunk pane leave the Task as it is. A closed Worker block closes the hunk pane with its tab.
9. **Config.** Load and validation: an unknown key, an empty filter list, a bad team or repository form, a duplicate name, a bad regex, an unknown `state`, a queue on closed PRs or drafts, each old key name, a `[[review.views]]` table, and `slots` below 1 each fail at load.
10. **Board.** The Board lists no review Task and toasts the alerts of both Kinds. Its header counts the review Tasks that need the user, and shows `reviews paused` while the queues are paused.
11. **Live.**
    - A queue on one repository starts `/pr-review` on a real PR. The Task reaches Review ready with a findings file, and nothing is written to GitHub.
    - Closing that Task's tab keeps the review in Review ready. `lineup open <task>` restores the Worker in the same conversation.
