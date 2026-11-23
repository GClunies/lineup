# lineup MVP spec

lineup is a terminal board and an orchestrator agent on top of Rex. It moves agent work from a Linear Issue to a merged pull request. The user sees each Task move through its Stages. The user never has to watch a PR.

The words in this spec have the meanings in [GLOSSARY.md](../../GLOSSARY.md). The decisions behind this spec are in [docs/adr](../adr/).

## Finish line

The MVP is complete when this sequence works with no manual step other than the four that the user owns:

1. The user starts a Linear Issue from its row on the Board.
2. A Worker does the Task in the background. If the skill or the user asks for a plan first, the Worker shows it to the user in its own tab.
3. Workers implement the change, fix CI, and answer review comments.
4. The user merges the PR from the Board.
5. The Task goes to Done and keeps its tab and worktree. When the user closes the tab, the Scheduler removes the worktree. With [`close_tab`](#merged-tasks) on, the Scheduler closes a merged Task's tab when its Worker is idle, and removes the worktree some days later.

## Scope

| In the MVP | In phase 2 |
|---|---|
| Hub with the Board and the Orchestrator | pi and Codex Harness adapters |
| Scheduler, store, and recovery | GitHub Issues and GitHub Projects Trackers |
| All Stages, Signals, and Loop budgets | Local Markdown Tracker |
| WIP cap with confirm or raise | Auto-intake |
| Linear Issue rows and Issue actions | Alerts through a Claude channel |
| `lineup` CLI for the Orchestrator | Per-workspace WIP caps |
| Claude Harness adapter | Edits to labels, priority, cycles, and milestones |
| Worktree setup through worktrunk | |
| Alerts in the Orchestrator, and a status record and toasts from the Board | |

The design for the phase 2 Trackers is in `docs/spec/trackers.md`.

A one-off skill run, for example `/pr-explain <url>`, is a Task with the override `pr=false`. Debug sessions are outside lineup. The user does them in a separate agent session. If one of them finds work, the Orchestrator can make that work a Task. The Orchestrator also plans Projects in the Trackers. A plan starts no Task.

## Dependencies

| Dependency | Use | If it is missing |
|---|---|---|
| Rex (server API revision 7 or later) | Sessions, tabs, blocks, events, program status | `lineup` stops with an error |
| worktrunk (`wt`) | Creates, sets up, and removes worktrees | `lineup` shows `worktrunk is required: brew install worktrunk (https://worktrunk.dev)` and starts no Task |
| GitHub CLI (`gh`) | PR state and merge | `lineup` stops with an error |
| Claude Code | The Orchestrator and the Workers | `lineup` stops with an error |
| Linear OAuth | Tracker access | The Board shows no Linear Issue rows. `lineup login` signs in. |

lineup has no dependency on herdr. It copies the Linear client, OAuth, cache, and Markdown view from the `herdr-linear` fork into this repo.

## Lifecycle

### Stages

| Stage | Owner | Board group | Counts toward WIP cap |
|---|---|---|---|
| Queued | user | Queued | no |
| Working | agent | Working | yes |
| CI running | CI | Waiting | yes |
| CI debug | agent | Working | yes |
| In review | other reviewers | Waiting | yes |
| Responding | agent | Working | yes |
| Ready to land | user | Needs you | yes |
| Done | none | Done | no |

A Task with the Needs you flag shows in the Needs you group, whatever its Stage is.

### Stage contracts

The Scheduler appends the contract to the prompt of each Worker. A contract says what the Worker must leave behind. It never says how to do the work. Configuration can add a skill or a command to a Stage. The skill becomes the first slash command in the Worker prompt. No Stage has one by default.

lineup has no planning Stage. If the work needs a plan or the user's approval first, the skill or the user's words ask for it, and that dialogue happens in the Worker's tab. See [ADR 0010](../adr/0010-planning-belongs-to-the-worker.md).

| Stage | Contract |
|---|---|
| Working | Push the branch and open a PR that is ready for review, not a draft. |
| CI debug | Push a commit that targets the failed checks that keep the PR from landing. |
| Responding, `threads = "replied"` | Reply to each open review thread, then resolve it. A thread that the user opened needs no reply. Push the changes that your replies promise. If the PR has a conflict, push a merge of the base branch that resolves it. |
| Responding, `threads = "resolved"` | Resolve each open review thread after you address it. Push the changes that the threads ask for. If the PR has a conflict, push a merge of the base branch that resolves it. |
| Responding, `threads = "ignored"` | Push the changes and reply to each open review thread. If the PR has a conflict, push a merge of the base branch that resolves it. |

The `threads` key of the [Landing bar](#landing-bar) picks the Responding contract. The CI debug prompt names the gating checks that failed and the optional checks that failed. It tells the Worker to leave the optional checks. The Responding prompt counts the open review threads and the new [Feedback](#feedback).

A Worker pushes and posts in the user's name, so the CI debug and Responding prompts hold two rules:

- **The reply rule** ([ADR 0026](../adr/0026-the-user-approves-every-reply-that-is-not-a-fix.md)). A Worker posts a reply to a person only when the reply reports a fix. It replies in the person's thread, or in the PR conversation for Feedback. Feedback that asks for nothing, for example thanks, needs no reply. Every other reply to a person is a Draft reply. A review cycle has no reply rule, because a reply to a bot speaks to no colleague.
- **The scope rule.** Before a Worker makes a change, it checks the change against the intent of the PR and the scope of the Task. The rule covers each change that a CI fix needs, and each suggestion from a person or a review bot that the Worker would apply or decline. If a change goes against that intent or outside that scope, or the Worker is not sure, the user decides. The Worker pushes nothing yet, not even its other changes, and posts nothing about that change. It runs `lineup ask <task> "<question>"`, which names the change, who asked for it, and why it is outside the scope. The Task then shows `question` in Needs you. When the user answers in the Worker's tab, the Worker does what the user says, then pushes.

If a Worker reports `done` and the contract output is missing, the Task gets the Needs you flag with the reason `stopped`. A Worker often stops to show the user a plan or to ask a question. The flag clears when the Worker starts working again, for example after the user replies. The Scheduler does not retry the Stage.

A Responding round that pushes nothing meets its contract when the latest poll saw every review thread done, no new Feedback, no conflict, and no review bot score below the minimum. Then the Task goes to CI running, and the poll settles it on its Landing bar. For example, a Worker can reply "won't fix" and resolve each thread. This rule needs a bar that holds threads. With `threads = "ignored"`, a round with no push gets `stopped`.

### Transitions

| From | Signal | To |
|---|---|---|
| Queued | `start`, and the WIP cap allows it or the user confirms | Working |
| Working | `done`, and a PR exists (`pr-open`) | CI running |
| CI running, In review, Ready to land | `pr-seen` | The Stage that the first gap of the Landing bar leads to. See [Landing bar](#landing-bar). |
| CI debug, Responding | `pushed` (a new head commit) | CI running |
| Responding | `done` with no push, and every review thread done | CI running |
| CI running | `review-bot-late`, and the review bot has not scored the head commit | CI running, with the Needs you flag `no bot review` |
| In review | `review-commented`, `review-changes-requested`, or `review-additional-authors` | Responding |
| Ready to land | `review-commented` or `review-changes-requested` | Responding |
| CI debug, Responding, with a `budget spent` flag that `pr-seen` set | `pr-seen` | The Stage that the first gap leads to |
| Any started build Stage | `pr-closed`, and the Task has no flag | The same Stage, with the Needs you flag `pr closed`. `pr-seen` on a reopened PR clears the flag. |
| Ready to land | `pr-merge` (user key `m`) | Done, marked merged |
| Any Stage | `pr-merged` outside lineup, for example by a person or by GitHub auto-merge | Done, marked merged |
| Any Stage except Done | `task-closed` (user key `x`, confirmed) | Done, marked closed. The Scheduler closes the tab and removes the worktree. |
| Done | `task-closed`: `x` (confirmed), `lineup close`, or `lineup archive` on a Done Task that keeps its tab or its worktree | Done, with the same outcome. The Scheduler closes the tab and removes the worktree. |
| Done | `tab-closed`: the user closed the Task's tab in Rex | Done, with the same outcome. The Scheduler removes the worktree, and the Repo session if it holds no other tab. |
| Done, merged, with [`close_tab`](#merged-tasks) on and a tab | The check at each poll finds the Worker idle | Done, with the same outcome. The Scheduler closes the tab, and the Repo session if it holds no other tab. It keeps the worktree for `keep_worktree_days`. |
| Done, with no tab and a kept worktree | The check at each poll or at start finds that `keep_worktree_days` passed | Done, with the same outcome. The Scheduler removes the worktree. |

`pr-seen` is a level Signal. The poll sends it for each open build PR at each poll, after the other GitHub Signals. It carries the PR's current facts. A second `pr-seen` with the same facts changes nothing. A Task that is already where its first gap leads stays there.

The Worker opens the PR ready for review, so GitHub requests the CODEOWNERS reviewers when the PR opens. lineup never changes the draft state of a PR. A draft PR means that a person wants to hold it, for example the user who made it a draft. When the user marks the PR ready, the next `pr-seen` sees the change.

### Landing bar

The Landing bar is what a PR must meet for its build Task to be in Ready to land. At each poll, the reducer measures the PR against the Task's bar. The result is the Task's Standing. A Task is in Ready to land only while its Standing has no gap. A PR that stops meeting the bar takes its Task out of Ready to land. See [ADR 0015](../adr/0015-a-landing-bar-decides-ready-to-land.md).

The reducer takes the first gap in this order. The order is one table in `internal/lifecycle/bar.go`.

| Order | Gap | Board text | The Task goes to |
|---|---|---|---|
| 1 | The PR has a merge conflict | `conflict` | Responding, for one Responding round |
| 2 | A gating check failed | `test failed` | CI debug, for one CI debug attempt |
| 3 | A gating check runs, or GitHub has not made it yet | `2 checks pending` | CI running |
| 4 | The PR is a draft | `draft` | CI running, with the Needs you flag `draft` |
| 5 | The review bot has not scored the head commit | `waiting for Greptile` | CI running |
| 6 | The review bot's score is below the minimum | `Greptile 4/5` | Responding, for one review cycle |
| 7 | A review thread is not done | `2 open threads (1 bot)` | Responding. If only bots opened the open threads and the review bot's score passes, the round is a review cycle. Otherwise it is a Responding round. |
| 8 | A person's [Feedback](#feedback) is new | `2 new comments` | Responding, for one Responding round |
| 9 | The reviews that the bar asks for have not approved | `needs approval`, `changes requested` | In review |
| | No gap | | Ready to land |

The rules for each item:

- A gating check with `checks = "required"` is a check that the base branch's rules require. If the base branch requires no check, every check gates. With `checks = "all"`, every check gates.
- The required checks are the union of two lists on the base branch: the classic branch protection's `requiredStatusCheckContexts`, and the checks of each active ruleset rule of type `REQUIRED_STATUS_CHECKS`. The poll reads both by branch name, with no PR number, so the first poll of a PR has them.
- A required check that GitHub has not made on the head commit yet counts as pending.
- An optional check that failed is no gap. It never starts CI debug. The Board note, the detail pane, and `lineup ls --json` (the `optional` field) name it.
- A review thread with `threads = "replied"` is done when it is resolved and the user wrote one of its last 5 comments. A thread that the user opened needs only the resolve. Workers post as the user. With `threads = "resolved"`, a thread is done when it is resolved. With `threads = "ignored"`, threads never gate.
- The poll reads the first 50 review threads. A PR with more threads does not meet the bar while the bar holds threads. The Board shows "more threads than lineup reads", and the unread threads start a Responding round, because people can have opened them.
- With `approvals = "required"`, GitHub's `reviewDecision` must not be `REVIEW_REQUIRED` or `CHANGES_REQUESTED`. With `approvals = "requested"`, the PR also must have no open review request, unless the decision is `APPROVED`.
- The review bot items apply only to a Task with a [review bot gate](#review-bot-gate).
- The Feedback item follows the `threads` key. With `threads = "ignored"`, Feedback never gates.

The detail pane and `lineup show` print the bar as a checklist, for example:

```
Landing bar
  ✓ required checks passed
  ✗ 1 open thread (1 bot)
  ✓ comments answered
  ✓ required reviews approved
  ! optional checks failed: govulncheck
```

A Task keeps the Landing bar that it had at Intake. A Task stored before the Landing bar keeps the rules that lineup had then: every check gates, threads never gate, and every requested reviewer must approve.

### Feedback

Feedback is a comment that a person other than the user wrote on a PR outside a review thread. See [ADR 0032](../adr/0032-a-responding-round-takes-the-feedback-before-it.md).

- Feedback is a comment in the PR conversation, or a review with a body or with a request for changes.
- A comment that a bot account wrote is not Feedback. A hidden comment is not Feedback. A review with no body and no request for changes holds only thread comments, which the threads item counts. A pending or a dismissed review is not Feedback.
- The poll reads the last 30 PR comments and the last 30 reviews.
- Each Responding round for people takes the Feedback that the first poll after the round's start sees. Feedback after that is new. A review edge starts a round before the poll's `pr-seen`, so that `pr-seen` is the poll that the round takes.
- A review cycle takes no Feedback, because its prompt names only the bot's review. Then the new Feedback starts its own round.
- A Task stored before lineup read Feedback takes the Feedback of its first poll, so the old comments on its PR are not new.
- With the Responding budget spent, new Feedback keeps the `budget spent` flag. A comment has no resolve, so the user runs the Stage again, hides the comment, or closes the Task.

### Review bot gate

A Repo can have a review bot gate, which adds the review bot's score to the Landing bar. The gate applies only to the Tasks whose Repo configuration has a `[review_bot]` table. lineup knows one bot, Greptile. Greptile reviews each push by itself and keeps one summary comment on the PR. lineup reads the confidence score and the reviewed commit from that comment. See [ADR 0013](../adr/0013-a-review-bot-gates-ci-passed.md).

- A score counts only when its reviewed commit is the head commit of the PR.
- Only a comment by Greptile's GitHub App counts. The PR body and comments by other authors never give a score.
- Greptile's check run passes at a low score too, so the score is an item of the Landing bar and not a check.
- A PR whose gating checks passed stays in CI running until the bot scores its head commit.
- A score below the minimum starts a Responding round, which spends one review cycle. The Worker prompt names the score and the minimum. It tells the Worker to address Greptile's unresolved review comments and the suggestions in its summary, and to resolve each thread that it addressed. The scope rule applies to each suggestion: the Worker asks the user before it applies or declines one that goes outside the Task's scope.
- If only bots opened the open threads and the score passes, the Responding round is a review cycle too. Then the bot's threads do not spend the Responding rounds that people's reviews need.
- With no review cycle left, the Task gets the flag `budget spent`, with a detail such as `Greptile 4/5 after 5 cycles` or `1 open thread (1 bot) after 3 cycles`.
- Greptile's own PR reviews do not send `review-commented`. The Landing bar decides each round.

A Repo where the bot does not review must not hold a Task forever. If a gated Task is in CI running for 15 minutes, and its checks passed with no score for the head commit, the Scheduler sends `review-bot-late`. The Task then gets the flag `no bot review`, with a detail such as `Greptile did not score 3591eba`. Greptile's check run keeps CI pending while it reviews, and in the probes Greptile scored a push within 4 minutes. The flag clears when Greptile scores the head commit.

### Needs you reasons

| Reason | Source |
|---|---|
| `question`, `permission`, `auth` | Program status `blocked`, with its `kind` |
| `error` | Program status `error`, or the Worker process exits with a code other than 0 |
| `setup failed` | A worktrunk hook exits with a code other than 0 |
| `stopped` | `done` without the contract output |
| `budget spent` | A Loop budget reaches 0 |
| `draft` | `pr-seen` on a draft PR with no earlier gap |
| `no bot review` | `review-bot-late`: the review bot did not score the head commit of a passing PR |
| `pr closed` | `pr-closed`: the PR of a build Task closed without a merge. The detail names the PR, for example `#5149 closed without a merge`. The Task keeps its Stage, tab, and worktree, because the work is the user's. The user reopens the PR, and the next poll clears the flag and moves the Task where the PR's facts lead. Or the user closes the Task with `x`. `pr-closed` is a level, so the first poll after a restart also flags a Task whose stored PR already reads closed. A Task with another flag gets `pr closed` when that flag clears. |
| `drafts` | `lineup ask --drafts <n> <task>` from a Responding Worker: n Draft replies wait for the user in the Worker's tab, by [ADR 0026](../adr/0026-the-user-approves-every-reply-that-is-not-a-fix.md). The detail counts them, for example `2 replies to approve`. The flag clears when the Worker reports `working`. |
| `tab closed` | The Task is in an agent Stage, and its Worker's tab or block is gone. The user closed the tab, Rex lost it, or the Rex server restarted. The detail names a Stage prompt that waits, for example `CI debug waits for the Worker`. lineup opens no tab. `lineup open <task>` restores the Worker and clears the flag. |
| `worker silent` | `prompt-stalled`: a Stage prompt has waited 1 minute for a Worker that has sent no program status, or for a prompted Worker to report `working`. The Scheduler types nothing more into it. The flag clears when the Worker reports `working`. |

When the cause clears, for example when program status goes back to `working`, the Scheduler removes the flag. The flag `tab closed` clears only when the user restores the Worker. A Task with no tab shows `tab closed` and never `worker silent`, because the silent wait counts only while a Worker runs. A restore starts that wait again.

A closed tab sets `tab closed` over the flags that a Worker's own state set, which are `question`, `drafts`, `permission`, `auth`, `error`, `stopped`, and `worker silent`. It leaves the other flags as they are, because they hold with no Worker. A tab closed in a Stage that waits for others, for example CI running, sets no flag. The Worker is not needed there, and the next agent Stage finds the closed tab when its prompt waits.

A gap of the Landing bar that finds its Loop budget spent sets `budget spent`, starts no Worker, and names the gap in the detail. Examples are `2 open threads (1 bot) after 3 rounds` and `test failed after 3 attempts`. The flag clears at the next poll after the gap closes, and the Task goes where its new first gap leads. A `budget spent` flag that a review Signal set stays until the user acts, for example with `lineup rerun`.

### Loop budgets

| Budget | Default | Scope |
|---|---|---|
| CI debug attempts | 3 | per Task |
| Responding rounds | 3 | per review |
| Review cycles | 3 | per Task, only with a review bot gate |

These spend the Responding rounds:

- people's reviews
- open threads, when a person opened one of them
- new Feedback
- a merge conflict

A low review bot score spends a review cycle. Open threads that only bots opened, on a score that passes, spend a review cycle too. A PR with no review bot gate has no score, so its bot threads spend Responding rounds.

## Rex layout

```
Rex server
├── session "my-lineup"         (the Hub session)
│   └── tab "lineup"
│       ├── block "BOARD"             lineup board           (top, most of the height)
│       └── block "LINEUP"            claude --continue     (bottom, 25% of the height, own theme)
├── session "data"                    (one session for each Repo with an open Task)
│   ├── tab "ENG-590 Add billing model"
│   └── tab "ENG-592 Fix flaky test"
└── session "web"
    ├── tab "WEB-12 …"
    └── tab "ah-9 Evaluate the ADR …"  (an ad hoc Task opens in its Repo's session too)
```

The Scheduler has no tab. launchd runs `lineup serve` as a user agent, and the log is `~/.local/state/lineup/scheduler.log`.

The rules for this layout:

- `lineup` opens the Hub session. If the session does not exist, `lineup` creates it.
- The Hub has one tab, `hub`. Its Board shows the build Tasks and the user's own Issues.
- A Hub from before the `lineup` tab has a session named `lineup hub` and a tab labeled `hub`. `lineup` renames them to `my-lineup` and `lineup`, so the agent keeps its conversation. It closes the old `orchestrate`, `plan`, and `review` tabs.
- A Hub from before the one agent has tabs with an `ORCHESTRATOR`, `PLANNER`, or `REVIEWER` block. `lineup` closes each such tab and opens it again.
- After a Rex server restart, each pane of the `lineup` tab holds an idle shell. If the `BOARD` or `LINEUP` pane is an idle shell, `lineup` closes the tab and opens it again, and the agent continues its conversation. A pane that the user added does not count. If `lineup` runs in that tab, it stops with an error, because the close would end it. Run `lineup` in another tab.
- The Hub session is named `my-lineup`. A Repo session is named after its folder, and the Scheduler removes it after its last Task. A Repo whose folder is named `my-lineup` gets the session `<parent>-my-lineup`, so no Repo session takes the Hub's name.
- The Scheduler creates each Task tab with `rex window new -s <session> <label> --focus=false -- <command>`. The user stays on the Hub.
- The setup tab also gets `--keep-open`, so it stays after worktrunk exits. The Task's first Worker opens in it with `rex split -b <setup block> --focus=false`. Then the Scheduler closes the setup block with `rex block close`. So the Task's tab appears once. If the user closed the setup tab, the Worker opens in a new tab.
- Each Worker tab holds a side pane to the right of the Worker, with half of the tab. The Scheduler opens it with `rex split -b <Worker block> --focus=false` after it closes the setup block. A build Task's pane shows the worktree's changes since it left the default branch. A review Task's pane shows the PR's GitHub page. The commands and rules are in **The side pane** in `docs/spec/reviews.md`.
- A failed setup keeps its tab, so the user can read worktrunk's error. `lineup close` closes that tab. A rerun closes it before it opens a new setup tab.
- The label is the Task ID, or the Issue identifier, and the title. Rex refuses a label longer than 128 characters, so a longer label stops at 127 characters and ends in `…`.
- Each Task tab names its command. A tab without a command reports exit code 0 when its shell exits, whatever the real code is.
- If a Worker exits within about 1 s, Rex keeps its tab open so that the user can read the error. The Scheduler never closes that tab by itself. A later prompt for the Task sets the flag `tab closed`. When the user runs `lineup open <task>`, the Scheduler closes the kept tab and opens a new one, because the user asked for the Worker.
- The Scheduler creates a Repo session with the first Task in that Repo. It removes the session when the last Task tab in it closes and the session holds no other tab. A tab that the user or another Scheduler opened keeps the session.
- If two Repos have the same folder name, the session label adds the parent folder, for example `acme-data`.
- An ad hoc Task opens in its Repo's session, as an Issue's Task does.
- The Scheduler passes `-s <session>` on every block call. Without it, Rex resolves the block only inside the caller's own session and returns `block target ... not found`.
- Each agent block uses a terminal `theme` with its own background color. The Orchestrator's block is `LINEUP`, and its Claude status line reads `Lineup`.

Rex has no key/value metadata on its objects. The store keeps the session, window, and block IDs of each Task.

## Components

```
            ┌──────────────── Unix socket ────────────────┐
 lineup board ┤                                              ├ lineup serve (Scheduler) ── SQLite store
 lineup CLI  ─┘                                              │        │
 (Orchestrator calls it)                                   │        ├── rex events --all --json
                                                           │        ├── gh GraphQL poll (30 s)
                                                           │        ├── wt (worktrees)
                                                           │        └── Linear API
```

| Component | Job |
|---|---|
| `lineup serve` | The Scheduler. It owns all Task state, applies the transitions, starts Workers, and reads Signals. launchd starts it at login and starts it again when it stops. A lock in `serve.lock` allows one Scheduler at a time. `lineup restart`, `lineup stop`, and `lineup logs` control it. |
| SQLite store | `~/.local/state/lineup/state.db`. One table holds Tasks. An append-only table holds the events of each Task. |
| `lineup board` | The Board. It reads state from the Scheduler and never writes the store. |
| `lineup` CLI | The commands that the Orchestrator uses. Each command calls the Scheduler. |
| Orchestrator folder | `~/.local/state/lineup/lineup/`, with a `CLAUDE.md` that defines the Orchestrator's role and the `lineup` commands. |
| Harness adapter | Launches a Harness, trusts the worktree for it, and supplies its status. The MVP has the Claude adapter only. |

### Signal sources

| Source | Signals |
|---|---|
| Rex events: `terminal.program_status_changed`, `terminal.child_exited`, `block_closed` | `done`, `blocked`, `error`, `tab-closed`. `child_exited` comes for every exit. `block_closed` does not come when Rex keeps the tab open. A tab that the user closes sends `block_closed` and no `child_exited`. So a `block_closed` of a Worker's block means that a person or Rex closed the tab. It does not mean that the Worker exited. |
| GitHub, one batched GraphQL query for all open Task PRs every 30 s, and one query right after a Worker reports `done` | `pr-open`, `pushed`, `review-*`, `pr-merged`, and `pr-seen` with the PR's facts at each poll |
| The user, through Board keys or the CLI | `start`, `pr-merge`, `task-closed`, and `tab-opened`, which `lineup open` sends when it restores a Worker |
| The Scheduler's clock, at each poll | `review-bot-late` |

The GraphQL query reads these fields of each PR:

- the first 100 contexts of the head commit's `statusCheckRollup`
- `reviewDecision`, `latestReviews`, `reviewRequests`, `commits`, `mergeStateStatus`, and `isDraft`
- the base branch's `refUpdateRule.requiredStatusCheckContexts` and its `rules`
- the first 50 `reviewThreads`, with the author of each thread's first comment and of its last 5 comments
- the last 30 PR comments, with each author, time, and `isMinimized`, and the last 30 `reviews`, with each author, state, body, and time. lineup reads Feedback from them.

For a Task with a review bot gate, it also reads the author and body of the first 20 PR comments.

A PR whose head commit has no checks meets the check items of the Landing bar, so a Repo with no CI goes on to review. The exception is a Repo whose base branch has checks. GitHub creates a push's checks a few seconds after the push, so there a PR with no checks yet stays in CI running. Without this rule, the Task could reach Ready to land, and the user could merge the PR, before its CI starts.

### Recovery

When `lineup serve` starts, it compares the store with `rex ls` and `rex session inspect`. Each Task in an agent Stage whose tab or block is gone gets the flag `tab closed`. Recovery opens no tab, because only the user restores a closed tab. A Task whose setup tab is gone, or whose Worker never started, starts again, because it never had a Worker to restore. A setup tab that Rex kept after worktrunk exited counts as gone, and recovery closes it. A failed setup's tab stays, because it shows worktrunk's error. If worktrunk exited while the Scheduler was down, recovery reads its exit code with `rex block call process`, and the Task goes on as if the Scheduler had seen the exit.

Recovery also runs after a Rex server restart. The Scheduler reads the Rex server's instance id every 5 s. When the id changes, every Worker is gone, so the Scheduler exits. launchd starts it again. Startup recovery flags each Task whose Worker is gone. The user restores each one with `lineup open <task>` or with `enter` on its row.

Rex brings back each tab after its restart, with the same IDs, but as a login shell with no program. Recovery reads each tab's process with `rex block call process`. A tab that holds only an idle login shell counts as gone, and recovery closes it. A tab where the user started a program in that shell counts as live. Recovery reads no process for a Done Task, so its tab stays until the user closes it. A merged Task whose tab waits for an idle Worker is the exception. If its tab holds an idle shell, recovery closes the tab and keeps the worktree. Otherwise recovery reads the Worker's program status, so a Worker that went idle while the Scheduler was down lets the tab close.

At its end, recovery runs the check that the Scheduler runs at each poll. So a worktree whose `keep_worktree_days` passed while the Scheduler was down goes before the first poll. Recovery can find a merged Task whose tab is gone while the Task waits for an idle Worker. The user did not close that tab, so recovery keeps the worktree for `keep_worktree_days` from the start.

## Start a Task

This sequence runs in the background. The Board shows only the Task as it changes Stage.

1. The Scheduler checks the WIP cap. If the cap is full, the Board asks the user to start this Task once over the cap or to raise the cap. A start over the cap shows `+1 over` in the header.
2. The Scheduler assigns the Issue to the user and sets the `started` status from the team's status map. See [Linear status map](#linear-status-map).
3. The Scheduler picks the Repo. The Repo comes from the team or Project mapping in the configuration. If no mapping exists, a fuzzy picker opens and remembers the choice for that Project.
4. The Scheduler creates the Repo session if it does not exist, and then a setup tab for the Task with `--focus=false --keep-open`.
5. In that tab, the Scheduler runs `wt switch --create <branch>`. The branch is Linear's `branchName` for the Issue. The worktrunk `copy` steps and `pre-start` hooks set up the worktree.
6. The Claude adapter marks the worktree as trusted. Claude stores this in `projects[<real path>].hasTrustDialogAccepted` in the `.claude.json` of the Claude configuration folder that the Repo uses. Without this step, the Worker stops at a trust prompt that reports no program status. The rules for this step:
   - The configuration folder comes from `direnv exec <Repo main checkout> printenv CLAUDE_CONFIG_DIR`. An empty value is the default profile, `~/.claude.json`. The worktree path does not decide it, because worktrees live outside the Repo.
   - The adapter takes Claude's own lock, the folder `.claude.json.lock`, and replaces the file with one rename. A lock folder older than 10 s is abandoned.
   - The Worker command sets `CLAUDE_CONFIG_DIR` itself, because Rex starts it through a login shell that runs no direnv.
7. The Scheduler starts the Worker in the setup tab, from the absolute path of the Harness, with the Stage's model, effort, prompt, and contract.
   - The Worker command passes `--name <tab label>`. The tab label starts with the Task ID, so the prompt box and the resume picker show each Worker's Task ID. Each Hub agent gets the same flag, with the name `lineup-<tab>`: `lineup-work`, `lineup-plan`, and `lineup-review`.
8. The Scheduler closes the setup block. Then it opens a side pane to the right of the Worker, in the worktree, with this command:

   ```
   sh -c 'b=$(git symbolic-ref -q --short refs/remotes/origin/HEAD) || b=origin/main; exec hunk diff --watch "$(git merge-base HEAD "$b")"' hunk
   ```

   The pane shows the worktree's changes since it left the default branch, and updates as they change.

Each Task keeps one Worker for its whole life. The Worker starts with `--session-id <conversation>`, and the Record keeps that conversation ID. Each agent Stage after Working types its prompt into the same Worker, in the same tab and worktree:

- The Scheduler types the prompt only when the Worker's program status is `idle` or `done`. While the Worker is busy, for example with a message that the user typed, the prompt waits for its next `idle` or `done`.
- The Scheduler does not check the Stage contract while a prompt waits, because a `done` then belongs to the Stage before it.
- A Worker whose tab is gone leaves the Task flagged `tab closed`. A prompt that is due then stays pending, and the flag names its Stage. Nothing opens a tab.
- Only a Task's first Worker opens a tab with no ask from the user. A Task that has had a Worker never gets a tab from a prompt, whether or not it holds a conversation.
- `lineup open <task>` restores the Worker. A live tab comes to the front. A Task with no tab gets a new tab in the Worker's folder that runs `claude --resume <conversation>`, with a new side pane, and the flag clears. A prompt that waited arrives at the restored Worker's first `idle`. The restore types nothing else, because the Stage prompt reached the Worker before the tab closed.
- A Task with no conversation, or whose conversation file is gone, gets a fresh Worker with the Stage prompt. The prompt says that this is a restart, and it quotes every steer that the user sent to the Task, in order. A Done Task never gets a fresh Worker.
- `open` refuses a Queued Task, with `<task> has not started`. It refuses a Done review Task that has no tab, with `<task> is Done and its worktree is gone`. It refuses a Task in a Stage that waits for others when no conversation is left to resume.
- A Done build Task with no tab resumes its conversation. While its worktree exists, `open` restores the Worker in the worktree, with a new side pane, as for any Task.
- After the worktree is gone, `open` resumes the Worker read-only in the Repo's main checkout. The Claude adapter trusts the main checkout first. The Worker starts with `--disallowedTools=Bash,Edit,Write,NotebookEdit,EnterWorktree` and without `--allowedTools=Bash(lineup ask:*)`, so it has no tool that writes files. `--append-system-prompt` tells it that lineup removed the worktree, and that it answers the user's questions. `--system-prompt-snapshot off` makes the resume send that text, because a resume otherwise sends the system prompt that the conversation recorded first. The tab has no side pane, because the side pane shows the worktree's changes.
- If Claude no longer holds the conversation of a Done build Task, `open` refuses with `<task> is Done and Claude no longer holds its conversation`.
- A Done Task that `open` restored keeps its new tab until the user closes it. That close removes the worktree at once, if the worktree still exists.
- A Worker that exited and left its tab open, as for a side pane, has no live Worker. `open` closes that tab and opens the new one in its place, so the Task's window is the live tab.
- The Worker keeps the model and effort of Working. `skill` and `command` apply to each Stage, because the prompt carries them.

## Configuration

lineup reads all of its configuration from one file, `~/.config/lineup/config.toml`. No file in a Repo configures lineup.

The precedence is, from lowest to highest:

1. `[defaults]`
2. `[repos."<owner>/*"]`, the table for each Repo of one owner
3. `[repos."<owner>/<repo>"]`, the table for one Repo
4. A Task override, for example `lineup set <task> model=claude-opus-5-5`

Each key layers on its own, as in `[budgets]`. A table at each level can hold `[stage.<name>]`, `[budgets]`, `[review_bot]`, and `[landing_bar]`. A `[stage.<name>]` section at any level beats `[stage.default]` at every level.

```toml
# ~/.config/lineup/config.toml
roots = ["~/repos"]
start_skills = ["pstack:poteto-mode"]

[defaults]
wip_cap = 5
stage.default = { model = "claude-opus-5-5", effort = "high", skill = "pstack:poteto-mode" }

[repos."crexi/*"]
review_bot = { name = "greptile", min_review_score = 5, max_review_cycles = 5 }

[repos."crexi/data"]
teams = ["PD"]

[repos."GClunies/hod"]
trackers = ["local"]
prefix = "hod"
```

The top level holds the keys that apply once: `roots`, `start_skills`, `linear_client_id`, `[linear.*]`, `[github.labels]`, `local_tracker`, `local_issues_dir`, `[review]`, `[board]`, and `[merged]`.

`[defaults]` holds each key that a Repo can override: `wip_cap`, `poll_interval`, `[budgets]`, `[review_bot]`, `[landing_bar]`, and `[stage.*]`. A layer key at the top level stops the load, for example with `wip_cap belongs in [defaults]. Run lineup config migrate to move it`.

`[repos."<key>"]` holds the keys of `[defaults]`, and `teams`, `trackers`, and `prefix`. `teams` and `prefix` need an `<owner>/<repo>` key. `trackers` can go in both forms.

### Repo keys and checkouts

`roots` lists the folders that hold the checkouts. It has no default. lineup looks at each root, at each folder in it, and at each folder in those. A folder with a `.git` folder is a checkout. A folder with a `.git` file is a linked worktree or a submodule. It is not a checkout. lineup does not look inside either folder. lineup skips each folder whose name starts with a dot. lineup walks the roots each time that it needs the checkouts, so a new clone shows up without a restart.

lineup names each checkout by its `origin` remote. This name is the Repo key:

- A GitHub origin gives `<owner>/<repo>`, for example `crexi/data`.
- A checkout with no origin, or with an origin outside GitHub, gives `local/<folder name>`, for example `local/brain`.

A key in the file is `<owner>/<repo>` for one Repo, or `<owner>/*` for each Repo of one owner. No other glob form exists. Keys ignore case, as GitHub names do. Two keys that differ only in case stop the load.

Each key has one checkout. A linked worktree of a Repo is never a checkout, so it never shares the Repo's key. If two clones have one key, `lineup restart` writes a warning that names both paths. lineup then picks no checkout for that key until you remove one clone from the roots.

- `lineup add --repo <name>` and a Local Issue's `repo:` take a folder name, an `<owner>/<repo>` key, or a path. A path starts with `/`, `~/`, or `.`.
- If two checkouts have the folder name, the error lists their keys, for example `2 checkouts named de-interview-exercise: pass crexi-dev/de-interview-exercise or crexi/de-interview-exercise`.
- A key names its one checkout. If two checkouts have the key, the error names both, for example `2 checkouts have the key crexi/data: /home/u/repos/crexi/data and /home/u/old/data: pass a path`.
- A path must be a checkout. A path to a linked worktree fails with `<path> is not a main git checkout`.
- A GitHub Issue goes to the checkout of its repository's key.
- The Repo picker lists the checkouts below the roots.

### Migration

A file from before this model holds `[[repos]]` tables with a `match` glob, and layer keys at the top level. Such a file stops the load with an error that names `lineup config migrate`. The command rewrites the file once:

- A `[[repos]]` table whose `match` is a path becomes `[repos."<owner>/<repo>"]`, with the key of that checkout.
- A `[[repos]]` table whose `match` is a `<folder>/*` glob becomes `[repos."<owner>/*"]`, if each checkout that the glob matches has that owner. Otherwise the command fails and names the glob.
- A `[[repos]]` table with only `match` goes away. Its folder becomes a `roots` entry, for example `~/repos` for `~/repos/*/*`.
- The roots find each checkout that the old `match` globs found. If a checkout is more than two folders below the roots, the command fails and names it.
- The layer keys at the top level move into `[defaults]`.

The command keeps the comments. It writes `config.toml.bak` first. If `config.toml.bak` already exists, the command stops and changes nothing, so an older backup stays. Then it loads the new file, and it puts the old file back if the load fails. A file in the new form stays as it is. Run `lineup restart` after the migration.

### Repo settings

`trackers` locks the table's Repos to those Trackers, for example `trackers = ["local"]`. The [Trackers spec](trackers.md#locked-repos) has the rules.

`prefix` gives the table's Repo its own IDs for Local Issues and ad hoc Tasks, for example `prefix = "hod"` gives `hod-1`. The [Trackers spec](trackers.md#repo-prefixes) has the rules.

`[review_bot]` turns on the review bot gate. Put it in the table of the Repos where the bot is installed, as in `[repos."crexi/*"]` above. The example gates each Repo of `crexi`, and no other Repo.

| Key | Value | Default |
|---|---|---|
| `name` | The bot. The only bot is `greptile`. | none, required |
| `min_review_score` | The lowest score that passes, from 1 to 5 | 5 |
| `max_review_cycles` | The review cycle budget | 3 |

A higher level can set one key and keep the others from a lower level, as in `[budgets]`. lineup checks each `[review_bot]` table when it loads the configuration. A Task keeps the gate that it had at Intake.

`[landing_bar]` sets the [Landing bar](#landing-bar) of build Tasks. It can go in `[defaults]` and in each `[repos."<key>"]` table. With no `[landing_bar]` table, each key has its default.

| Key | Values | Default |
|---|---|---|
| `checks` | `required`: the base branch's required checks gate. If the base branch requires no check, every check gates. `all`: every check gates. | `required` |
| `threads` | `replied`: each review thread is resolved, and the user replied in each thread that someone else opened. `resolved`: each thread is resolved. `ignored`: threads never gate. | `replied` |
| `approvals` | `required`: the reviews that the base branch requires approve. `requested`: also every open review request, unless the decision is `APPROVED`. | `required` |

```toml
# A Repo where every requested reviewer must approve
[repos."crexi/data".landing_bar]
approvals = "requested"
checks = "all"
```

Each key layers on its own, as in `[budgets]`. A value that is not in the table stops the load with an error that names the key, for example `landing_bar.threads: "any" is not one of ignored, resolved, replied`. A Task keeps the Landing bar that it had at Intake.

`start_skills` is a top-level key. It lists the skills that the skill picker offers when the user starts a Task from an Issue row. A chosen skill is stored as the Task override `skill`.

`[board]` is a top-level table. It sets the layout of the Board's groups:

```toml
[board]
order = ["done", "needs_you", "working", "waiting", "queued", "ready", "backlog"]
collapsed = ["backlog"]
hidden = ["done"]
```

`order` lists the groups, first to last. A group that it leaves out follows the listed ones, in the default order: `needs_you`, `backlog`, `ready`, `queued`, `working`, `waiting`, `done`. `collapsed` lists the groups that start closed. Its default is `backlog` and `done`. `hidden` lists the groups that the Board does not show. Its default is none. A key that is absent takes its default, and an empty list means none. `needs_you` cannot be in `collapsed` or `hidden`. An unknown group, a duplicate in one list, and `needs_you` in `collapsed` or `hidden` are errors, but they do not fail the load. The Scheduler starts, and the Board uses the default layout. The Board shows the error on its own line until it quits, for example `config: board.hidden: unknown section "dun" (default layout)`. If the load fails for any other reason, the Board runs with defaults and shows `config: <error>` the same way.

`linear_client_id` is a top-level key. Put it above the first `[table]`, or TOML reads it as part of that table. Leave it out to sign in through lineup's own Linear OAuth app, which any workspace can use. To use your own app, register an OAuth app in Linear with the redirect URI `http://localhost:47821/callback` and no client credentials. Then set its client ID here.

Some workspaces let members use only apps that an admin approved. For such a workspace, register an app in that workspace and set its client ID as `client_id` under `[linear.<url key>]`. Then run `lineup login <url key>`. lineup refuses the sign-in if you pick another workspace on Linear's consent screen. Each workspace's tokens keep the app that issued them, so a new client ID takes effect at the next `lineup login`.

lineup merges a PR only when the user asks, with `m` on the Board or with `lineup merge <task>`. To merge each PR when its checks pass, turn on auto-merge in the forge, for example `gh pr merge --auto` on GitHub.

The Task override `pr=false` is for work that ends without a pull request. Its Working contract is "Do not push a branch or open a pull request. Say what you produced, then stop." The Task then gets the flag `stopped`, and the user closes it.

```toml
# The settings of one Repo
[repos."crexi/data".stage.ci_debug]
skill = "diagnosing-bugs"

[repos."crexi/data".review_bot]
min_review_score = 4
```

The Scheduler finds the `claude` path on PATH when it resolves the configuration. A `harness` key is an error until a second Harness exists. Rex starts commands through the login shell, which can resolve a name to a different binary than the Scheduler sees.

### Merged Tasks

`[merged]` is a top-level table. It sets what lineup does with a build Task after its PR merges. A review Task ignores it, because each end of a review Task closes its tab ([ADR 0024](../adr/0024-every-review-end-closes-the-review-tab.md)). See [ADR 0033](../adr/0033-lineup-closes-a-merged-tab-when-the-user-turns-it-on.md).

```toml
[merged]
close_tab = true          # close a merged Task's tab when its Worker is idle
keep_worktree_days = 3    # remove the worktree 3 days after the tab closes
```

| Key | Value | Default |
|---|---|---|
| `close_tab` | `true`: lineup closes a merged build Task's tab when its Worker is idle, and keeps the worktree. `false`: the Task keeps its tab and worktree until the user closes the tab. | `false` |
| `keep_worktree_days` | The days that lineup keeps the worktree after it closes the tab, from 0 to 30. With 0, lineup removes the worktree when it closes the tab. | 7 |

The rules for `close_tab`:

- The Worker is idle when it reports `idle` or `done`, or when it has exited. While the Worker works, waits for the user, reports an error, or has not reported a status yet, the tab stays.
- The days count from the tab's close. If the Task has no tab at the merge, they count from the merge.
- lineup marks a Task at its merge. A Task that merged before the Scheduler started with `close_tab` on keeps its tab.
- The Scheduler checks at each poll and once at its start. A worktree whose days ended while the Scheduler was stopped goes at the next start.
- When lineup closes the tab, it also removes the Repo session if that session holds no other tab.
- While lineup keeps the worktree, the Task stays on the Board with the note `worktree until <date>`, for example `worktree until Jan 2`. `lineup open` and `enter` restore the Worker, as [Start a Task](#start-a-task) describes.

lineup never removes a worktree that has uncommitted changes. Before it runs `wt remove`, it runs `git status --porcelain` in the worktree. If the output is not empty, lineup keeps the worktree, and the Task's note reads `worktree kept: uncommitted changes in <path>`. lineup tries again at each poll, and removes the worktree after the user commits or discards the changes. This rule also holds when the user closes a tab, with `close_tab` on or off.

A negative `keep_worktree_days` is an error. A value over 30 is an error that names the limit, for example `merged.keep_worktree_days: 31 is more than 30, the days that Claude keeps a conversation`. Claude deletes a conversation after `cleanupPeriodDays`, 30 days by default. After that, `lineup open` has no conversation to resume. The default of 7 equals the Board's age-off for a Done Task. The Scheduler reads `[merged]` when it starts, so run `lineup restart` after a change.

### Linear status map

Each Linear team has its own statuses, and Linear's GitHub automation already moves some of them. Each team gets a map from a lifecycle event to a status. lineup writes a status only where the map names one. It writes nothing for an event that the map leaves out.

For example, take two teams. ENG tests each change after the merge, and APP does not:

| Event | ENG | APP |
|---|---|---|
| `started` (the Task starts) | In Progress | In Progress |
| `in_review` | none (Linear sets In Review) | none (Linear sets In Review) |
| `merged` | none (Linear sets In QA) | Done |
| `closed` (the user closes the Task) | none | none |

The Needs you flag writes nothing to Linear. In Linear, "Blocked" and "Waiting" mean that other people block the Issue. ENG puts QA after the merge, so lineup must not move an ENG Issue to Done.

```toml
[linear.acme.status.ENG]
started = "In Progress"

[linear.acme.status.APP]
started = "In Progress"
merged = "Done"
```

To find which statuses Linear's GitHub automation sets, query `teams { gitAutomationStates }`. If a team changes its automation, its map must change too.

## Board

The Board shows the build Tasks and the user's own Issues that have no Task. Review Tasks run in their own tabs in the `reviews` Rex session, and the Board lists none of them (see `docs/spec/reviews.md`). The Issue rows are in `docs/spec/one-board.md`.

```
 lineup  WIP 4/5   ●3 need you (+1 in reviews)
 ─────────────────────────────────────────────────────────────────────────
 NEEDS YOU  3
 ▸ ENG-590 data   Add billing model          stopped      12m
   ENG-588 data   Backfill events           permission: Bash(make deploy)   3m
   ENG-580 data   Retry failed loads        drafts: 2 replies to approve    1m
 BACKLOG  7 …
 READY  2
   ENG-611        Move the retry job        unblocked    ▂▄▆
   local-7 data   Rename the log flag
 QUEUED  1
   ENG-601 data   Drop old API keys
 WORKING  2
   WEB-12  web    Fix map tooltip           #4650 ○ no review yet · answering review    5m
   ENG-592 data   Fix flaky test            #4655 ○ no review yet · fixing CI           1m
 WAITING  1
   ENG-571 data   Rate limit login          #4636 ● commented · 1 open thread · optional failed: lint  1d
 DONE  1 …
```

The header shows the WIP cap and the count of the build Tasks that need the user. It counts the review Tasks that need the user in parentheses, when there are any: `(+1 in reviews)`. It has no count for the other groups, because each group heading shows its own count. The header also shows `reviews paused` while the review queues are paused, and a warning when the Scheduler is offline. At 80 columns, the header with `+1 over` and the review count fits.

A row has no Stage column. The group heading says who must act, and the note says what happens. The note of a build Task with a PR starts with the PR number and where the PR stands with its reviewers. Then it says what happens now, or the next gap that the Landing bar has. The approval gap never shows, because the review state already says it.

| Review state | When |
|---|---|
| `✗ changes requested` | GitHub's review decision, or a person's latest review, requests changes. |
| `✓ approved` | GitHub's review decision is approved. |
| `● commented` | A person reviewed the PR, and the PR is not approved. |
| `○ no review yet` | No person other than the user reviewed the PR. |

The first row that holds is the state. The text after the review state, by Stage:

| Stage | Text |
|---|---|
| CI debug | `fixing CI` |
| Responding | `answering review` |
| CI running, In review | The next gap, for example `2 checks pending` or `1 open thread`. |
| Ready to land | `ready to land` |

A review Task shows `#N reviewing`, `#N review ready` with `· head moved` when the head changed, and `#N` and its outcome when it is done. A flag replaces the note with `<flag>: <detail>`, for example `drafts: 2 replies to approve`.

Reviews by bots do not count as a person's review.

The groups are Needs you, Backlog, Ready, Queued, Working, Waiting, and Done. By default they show in that order: Needs you comes first, and the other groups follow the development cycle. The `[board]` table in the global file changes the order. Backlog and Ready hold Issue rows. Each lists the Issues with no open blocker first, then sorts by priority, dependency level, and ID. An Issue with no open blocker has level 0. Any other Issue has a level one higher than its highest open blocker.

Each group with at least one row has a heading that shows the group name and its row count, for example `BACKLOG  7`. A heading is a row that the user can select, except the Needs you heading. `j` and `k` stop on each heading and skip the rows of a closed group. `enter` on a heading opens or closes the group. A closed group shows its heading with a `…` mark and hides its rows, as `BACKLOG  7 …` does. Needs you never closes. This keeps a long Backlog or Done group from pushing Ready and Working off the screen.

Backlog and Done start closed, and `[board] collapsed` changes that. `enter` or a tap on a heading opens or closes a group for this run only. `[board] hidden` hides groups: a hidden group shows no heading and no rows, and `j` and `k` skip it. `d` shows or hides Done for this run, whatever `hidden` says, and opens Done when it shows. While the filter holds text, a hidden group shows its matches. When a Task moves into a closed group, the selection goes to that group's heading. When `n` adds a local Issue, lineup shows and opens Backlog for this run and selects the new row.

| Key | Action |
|---|---|
| `j` `k` | Move the selection through the group headings and the rows of open groups |
| `d` | Show or hide Done |
| `enter` | On a Task row, focus the Task's tab in Rex. If the Task has started and has no live Worker, restore the Worker with `claude --resume`, as `lineup open` does. On a Done Task with no tab, restore the Worker in its kept worktree, or read-only in the main checkout after lineup removed the worktree. The hint reads `restore the Worker`. On a group heading, open or close the group. The hint reads `open` or `close`. |
| `p` | Show a live preview of the Worker's screen beside the Board |
| `f` | Show the detail pane at full screen, and back. On an Issue row, the pane shows the Issue's description. |
| `m` | Merge the PR (Ready to land) |
| `x` | Close the Task, after a confirmation. On a Done Task with a tab, close the tab and remove the worktree, after a confirmation. On a Done Task with no tab, archive it and remove its kept worktree, after a confirmation. |
| `s` | Start a Queued Task. On an Issue row, open the skill picker, then add the Issue as a Task and start it. The first row of the picker is `just go`, which starts with no skill. The other rows are the skills in `start_skills`. Typed text filters these rows, and a typed name that is not in the list becomes its own row. The picker drops a leading `/`. When no Repo mapping exists, the Repo picker opens next. |
| `c` | On an Issue row, change the status. A Linear row lists the workflow states of its team, and a local row lists the local statuses. |
| `o` | Open the PR or the Issue in the browser. A local Issue opens in `$EDITOR`. |
| `n` | Capture a new Issue at a Destination, `local` by default. `tab` picks a Linear team or Project. A local Issue shows and opens Backlog and selects the new row. See `docs/spec/one-board.md`. |
| `P` | Promote a local Issue to Linear. Pick a Linear team or Project. lineup creates the Issue there and cancels the local Issue. See `docs/spec/one-board.md`. |
| `/` | Filter the rows by ID and title. `enter` keeps the filter, and `esc` clears it. |
| `R` | Refresh now: the Scheduler polls GitHub for every started Task |
| `z` | Zoom this tab's agent pane to full height, and back |
| `?` | Show all keys |

The Board also works by mouse or touch, for example from Rex on a phone. A tap on a group heading opens or closes the group. A tap on a Task or Issue row selects the row and opens a menu of its actions, the same actions that the footer hints name. A tap on an action runs it as its key does, and `back` closes the menu. When a tapped action asks yes or no, as a close does, or hits the WIP cap, the question is a menu too. A tap on the detail pane goes back. The scroll wheel moves the selection, as `j` and `k` do.

A Task that ends keeps its tab and worktree. With [`close_tab`](#merged-tasks) on, lineup closes a merged build Task's tab when its Worker is idle, and keeps the worktree for `keep_worktree_days`. The user's own close removes the worktree at once: `x` or `lineup close <task>` on the Done Task, the tab's own close in Rex, or `lineup archive <task>`. A Done Task leaves the Board when the user archives it. It also leaves 7 days after it ends, but only if it keeps no tab and no worktree. A Done Task with an open tab or a kept worktree stays on the Board. The Scheduler refuses `lineup add` for a Done Task's ID while that Task keeps its tab, with `<task> is Done and keeps its tab: close its tab first with lineup close <task>`. While the Task keeps only its worktree, the refusal reads `<task> is Done and keeps its worktree: remove it first with lineup close <task>`. The Scheduler keeps the Task and its events, so `lineup show <task>` still finds it. To put an archived Issue back on the Board, run `lineup add` again.

## Orchestrator

The Orchestrator has four jobs:

| Job | Example | `lineup` command |
|---|---|---|
| Intake | "start ENG-123", "fix the flaky test in data" | `lineup add ENG-123`, `lineup add --repo data "<text>"` |
| Status | "what needs me?" | `lineup decide` |
| Status | "what is ready?" | The ready lines that its hook adds, from `lineup ready` |
| Steer | "tell ENG-592 to use approach B" | `lineup steer <task> "<text>"` |
| Override | "raise the cap to 6", "re-run CI debug" | `lineup cap 6`, `lineup rerun <task> ci_debug` |
| Restore | "bring back ENG-592's tab" | `lineup open <task>` |

The Orchestrator handles build Tasks and review Tasks. It maps each review request, `/pr-review` included, to `lineup review`. Its settings deny every GitHub write to a PR.

If `lineup add` or `lineup start` meets a full WIP cap, the command fails with `wip cap full`. The Orchestrator must then ask the user with `AskUserQuestion`. It must not use `--over-cap` without the user's pick.

### Alerts

A Task alerts when it enters the Needs you group or Done. It alerts again only after it leaves that state and enters a state that needs the user. These parts show the alerts:

| Part | What it does |
|---|---|
| The Board | It prints an OSC 777 notification for each alert of a build Task that reaches Ready to land or Done. Rex shows it as a desktop toast for the Board's pane. A click on the toast opens that pane. The toast names the Task and the PR, for example `ENG-571 #4636 ✓ approved · ready to land \| Rate limit login`. A build Task that enters Needs you for a flag gets no toast, because the status record reports it. A Task in Ready to land with a flag gets no toast either. The view that the Board opens on makes no toast. |
| The status record | While a build Task is in Needs you, the Board keeps one Rex program status record (OSC 7501) on its pane. The record has `state=blocked` and `kind=question`. Its message names the count, for example `2 Tasks need you`. The count is the one that the header shows. Rex shows a dot and the message on the `lineup` tab. The Board sends the record at start, and again each time the set of those Tasks changes. A Task changes the set when it joins or leaves Needs you, or when its Stage or its flag changes. A review Task does not count. When no build Task is in Needs you, the Board clears the record. Rex drops the record when the Board quits. |
| The free slot toast | When a build Task alerts for Done and the started build Tasks are fewer than the WIP cap, the Board sends one more toast that names the first Ready row, for example `1 slot free, next ENG-611: Move the retry job`. A Board view sends at most one such toast. With no Ready row, it sends none. The Board does not start the Issue. |

The Orchestrator's one alert is each PR that is ready to land. Its `UserPromptSubmit` hook runs `lineup ready` before each turn. The command prints one line for each build Task in Ready to land with no flag, for example `Ready to land: ENG-571, PR #4636 https://github.com/acme/data/pull/4636, Rate limit login`. It prints nothing when no PR is ready. The hook costs no agent turn, so the Orchestrator learns a ready PR at the user's next message. See [ADR 0031](../adr/0031-the-orchestrator-learns-each-pr-that-is-ready-to-land.md).

The Orchestrator asks the user nothing about a Task on its own. It uses the ready lines to answer "what is ready?" and to find the Task for "merge it". The user decides each Task in Needs you with What Needs Me… in Rex's palette. For a Done Task with a tab, the user picks Close a Task… or Archive Done Tasks…. When the user asks the Orchestrator about a Task that needs them, it runs `lineup decide`, and then the choice that the user picks.

### Choices in Rex's palette

A choice is one thing that the user can do about a Task that needs them. `lineup decide` lists the build Tasks and the review Tasks in the Needs you group. Each Task comes with its choices, the default first. `lineup decide <task> <choice>` runs one choice. It refuses a Task that does not need the user, and a choice that the Task does not have. Answer here needs a Worker to read the answer, so a Task with no live Worker does not get it.

| The Task needs the user because | Choices, the default first | Commands |
|---|---|---|
| Its PR is ready to land | Merge the PR, Open the tab | `lineup merge`, `lineup open` |
| `tab closed` | Restore the Worker, Close the Task | `lineup open`, `lineup close` |
| `question` | Answer here, Open the tab | `lineup steer <task> "<text>"`, `lineup open` |
| `stopped` or `setup failed` | Open the tab, Close the Task | `lineup open`, `lineup close` |
| `budget spent` | Run the Stage again, Open the tab, Close the Task | `lineup rerun <task> <stage>`, `lineup open`, `lineup close` |
| `pr closed` | Close the Task, Open the tab | `lineup close`, `lineup open` |
| A review is ready | Open the tab, Close the Task | `lineup open`, `lineup close` |
| Any other reason | Open the tab | `lineup open` |

Each time that `lineup` opens the Hub, it writes `lineup.lua` beside Rex's `init.lua`, which is `REX_CONFIG` when that variable is set. The file runs lineup by the absolute path of the binary, because `rex.exec` runs a command with the Rex server's `PATH`. The line `require("lineup")` in `init.lua` adds these actions to Rex's command palette, in the category Lineup:

| Action | What it does |
|---|---|
| What Needs Me… | Lists the Tasks of `lineup decide`. Then it lists the choices of the Task that the user picks. A lone Open the tab runs with no second question. For Answer here, it asks for the user's words. Then it runs `lineup decide <task> <choice>`. |
| Tell a Worker… | Lists the Tasks with a live Worker, asks for the user's words, and runs `lineup steer`. |
| Go to a Task… | Lists the Tasks on the Board, except a Done review Task with no tab, and runs `lineup open`. A Done build Task with no tab resumes its Worker, as `enter` on the Board does. |
| Start a Task… | Lists the Queued Tasks. The user can type an Issue instead, which `lineup add` adds first. Then it runs `lineup start`. |
| Start an Ad Hoc Task… | Lists the Repos of the build Tasks on the Board. The user can type another Repo. It asks for the job, then runs `lineup add --repo` and `lineup start`. |
| Review a PR… | Asks for a PR URL and runs `lineup review`. |
| Close a Task… | Lists the Tasks on the Board, except a Done Task with no tab, asks again, and runs `lineup close`. |
| Archive Done Tasks… | Lists the Done Tasks with no item ticked, and runs `lineup archive` for each ticked one. |
| Run a Stage Again… | Lists the started Tasks, then the agent Stages of the Task's Kind, the current Stage first. Then it runs `lineup rerun`. |
| Set the WIP Cap… | Asks for the cap, with the current cap in the field, and runs `lineup cap`. |
| Pause Reviews, Resume Reviews | Run `lineup reviews pause` or `lineup reviews resume`. |
| Set a Task's Model… | Lists the Tasks that are not Done, then the Claude models. The user can type another model. It runs `lineup set <task> model=<model>`. The model applies when the Task's next Worker starts. |

When a Task start finds the WIP cap full, the action asks to start the Task once over the cap or to leave it in Queued.

The actions ask with Rex prompts, which need Rex API revision 17 or later. On an older server, `lineup.lua` defines no action. Rex asks every connected Rex app, so the user can answer on another device. The first answer wins.

## Planning

The Orchestrator shapes the backlog in Linear with the `linear` CLI. Planning has four jobs:

| Job | Example | `linear` command |
|---|---|---|
| Read | "show ENG-614", "what is in this Project?" | `linear issue view`, `linear project view`, `linear issue query` |
| Split | "split this Project into Issues" | `linear issue create` |
| Criteria | "write acceptance criteria for ENG-614" | `linear issue update` |
| Order | "ENG-615 needs ENG-614 first" | `linear issue relation add ENG-614 blocks ENG-615` |

A plan starts no Task. The Orchestrator must not run `linear issue start` or `linear issue pull-request`, because lineup moves each Issue through its lifecycle. Its settings deny these commands. To start work, the user presses `s` on an Issue row of the Board, or asks the Orchestrator.

## Speed budget

| Measure | Target |
|---|---|
| First paint of the Board, from the cache | under 100 ms |
| Keypress to render | under 16 ms |
| Network calls on the render path | none |
| A new Task tab, without hook time | under 2 s |
| Signal to Board update, for a Rex event | under 100 ms |
| Signal to Board update, for a GitHub change | under 35 s |

The Board shows cached data and refreshes it in the background.

## Success criteria and tests

| Criterion | Test |
|---|---|
| Each transition in the table above is correct | Table-driven unit tests of a pure transition function |
| The started-Task count never passes the WIP cap without a recorded override | Property-based tests with `pgregory.net/rapid` on random sequences of Signals |
| Each started Task ends in Done or carries the Needs you flag | The same property-based tests, with every Worker made to stall or fail |
| Each Needs you reason has a choice, Ready to land is the only build Stage in Needs you with no flag, and each choice sends exactly one request | Tests of `lineup decide` through a fake Scheduler socket |
| The Scheduler reads program status correctly | An integration test that runs `claude` in a throwaway Rex session and reads `working`, `done`, and `blocked` (`kind: permission`) |
| Recovery flags a lost Worker and opens no tab, and `lineup open` restores it | An integration test that kills a Task tab, restarts `lineup serve`, and runs `lineup open` |
| The GitHub Signals are correct | Tests against recorded GraphQL responses for each PR state |
| The poll reads the required checks, the optional checks, and the review threads correctly | Tests against recorded GraphQL responses of public repos: classic protection, a ruleset, no protection, a required check that is not reported yet, and open, replied, and resolved threads |
| No build Task is in Ready to land while its Standing has a gap. An optional check never starts CI debug. A second `pr-seen` with the same facts changes nothing. | Property-based tests with `pgregory.net/rapid` |
| A gated Task never reaches Ready to land with a head score below the minimum, and its review cycles never pass the budget | Property-based tests with `pgregory.net/rapid` |
| The speed budget holds | A benchmark of the Board's first paint and of its keypress render, run in CI |
| End to end | One real Linear Issue in a sandbox repo goes from start to Done |

## Probe results that shaped this spec

These facts come from probes on this machine: throwaway Rex sessions, and `gh api` calls for GitHub.

| Fact | Evidence |
|---|---|
| Claude Code reports `working`, `done` with a summary, and `blocked` with `kind: permission` and the command | `rex block call -s <session> -b <block> program_status` |
| Claude Code and Codex stop at a trust prompt in a new folder, and report no program status while they wait | `rex capture` of the probe blocks |
| pi reports no program status | 40 samples, all empty |
| Codex shows a "Review hooks" prompt at each start until the user trusts the Rex hook | `rex capture` of the probe blocks |
| A block call without `-s` cannot find a block in another session | `Error: block target "block:…" not found` |
| `rex window new --focus=false` keeps the active window | `TestRexServer` in `internal/rex` |
| A tab without a command reports `exit_code 0` when its shell runs `exit 3`. A tab that runs `sh` or `zsh` reports 3. | `child_exited` events of three probe tabs |
| Rex keeps a tab open when its program exits within about 1 s. `child_exited` comes, `block_closed` does not. | Probe tabs that ran `sleep N; exit 3` for N = 0, 1, 2, 3, 5. Only N = 0 stayed open. |
| Each `rex` CLI call takes 20 to 95 ms from Go | `go test -v` log of `TestRexServer`, three runs |
| Claude locks `.claude.json` with a lock folder, `.claude.json.lock`, and writes through `.claude.json.tmp.<pid>.<random>` and a rename | A file watcher on the profile folder during `claude -p` |
| A running Claude keeps a project entry that another process added under the lock | A live `claude` with a throwaway profile exited and rewrote the file. The added entry stayed. |
| `direnv exec <dir>` unloads the caller's own `.envrc` values and keeps exported values | `printenv CLAUDE_CONFIG_DIR` through `direnv exec` for two Repos |
| Claude has no flag that skips the trust dialog in an interactive session | `claude --help`: only `-p` skips it |
| `--name` sets the session's name in the prompt box, the `/resume` picker, and the terminal title | `claude --help` |
| Greptile keeps one summary comment on a PR. The comment holds `<!-- greptile_confidence_score:N -->` and `Last reviewed commit: …/commit/<sha>`. | `gh api repos/better-auth/better-auth/issues/12303/comments`: the `greptile-apps[bot]` comment holds score 4 and the head commit. `internal/github/testdata/review_bot.json` is a trimmed copy. |
| GraphQL names Greptile's author `greptile-apps`, with no `[bot]` suffix | `Client.Snapshot` against better-auth/better-auth#12303 |
| The `Greptile Review` check run passes at a score below 5 | `gh api repos/better-auth/better-auth/commits/eb6b436/check-runs`: conclusion `success` at score 4/5 |
| Greptile edits its summary about 2 s after its check run completes | The same PR: the check run completed at 18:00:46Z, and the comment changed at 18:00:48Z |
| Some Greptile installs write the summary into the PR body, which the PR author can edit | `gh api repos/synnaxlabs/synnax/pulls/4881`: the body holds the score, and Greptile wrote no comment |
| A viewer with read access can read the required checks of classic branch protection in `refUpdateRule` | The poll's GraphQL query against cli/cli#13013: `requiredStatusCheckContexts` names the three `build (…)` checks. `internal/github/testdata/classic_protection.json` is a trimmed copy. |
| `refUpdateRule` does not show a ruleset's required checks. `rules` does, by branch name, with no PR number. | The poll's GraphQL query against GClunies/lineup#9: `refUpdateRule` is `null`, and an `ACTIVE` `REQUIRED_STATUS_CHECKS` rule names `test`. `internal/github/testdata/ruleset.json`. |
| A review thread's first comment tells a bot from a person: its author's `__typename` is `Bot` or `User` | The poll's GraphQL query against cli/cli#12942: 7 threads that bots opened and 4 that users opened. `internal/github/testdata/threads.json`. |
| A reply in a review thread makes a `COMMENTED` review with an empty body. A review bot's review has a body and the author `__typename` `Bot`. | The poll's GraphQL query against cli/cli#12942: of 8 reviews, 5 had an empty body, and Copilot's 2 had `Bot` authors. `internal/github/testdata/feedback.json` is a trimmed copy. |
| With `--disallowedTools=Edit,Write,NotebookEdit,Bash`, Claude has no Bash, Monitor, or Edit tool and writes no file. A subagent that it starts has no write tool either. | `claude -p` 2.1.323 in a throwaway git repo, asked to create `probe.txt` by any means |
| `--permission-mode plan` is not a hard block. Claude wrote no file only because the model declined. | The same probe |
| `claude --resume <id>` resumes a conversation from another folder, also after the first folder is deleted | A session started in folder A answered `ALPHA`. A resume from folder B, then a resume from folder C after A was deleted, both answered `ALPHA`. |
| `wt remove` without `-f` fails when the worktree has uncommitted changes | `wt remove --help` |
| A read-only resume from the main checkout keeps the conversation and writes no file | A session started in folder A was told the word `ZEBRA42`. After A was deleted, a resume from a main checkout with `--disallowedTools=Bash,Edit,Write,NotebookEdit,EnterWorktree` answered `ZEBRA42`. `git status` then showed no new file. |
| A resume sends `--append-system-prompt` only with `--system-prompt-snapshot off` | `claude -p --resume` with an added code word: the default answered that the system prompt holds no such sentence. With `off`, it quoted the sentence. |
