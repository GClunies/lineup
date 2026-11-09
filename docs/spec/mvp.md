# lineup MVP spec

lineup is a terminal board and an orchestrator agent on top of Rex. It moves agent work from a Linear Issue to a merged pull request. The user sees each Task move through its Stages. The user never has to watch a PR.

The words in this spec have the meanings in [GLOSSARY.md](../../GLOSSARY.md). The decisions behind this spec are in [docs/adr](../adr/).

## Finish line

The MVP is complete when this sequence works with no manual step other than the four that the user owns:

1. The user starts a Linear Issue from the tracker browser.
2. A Worker does the Task in the background. If the skill or the user asks for a plan first, the Worker shows it to the user in its own tab.
3. Workers implement the change, fix CI, and answer review comments.
4. The user merges the PR from the Board.
5. The Scheduler closes the tab and removes the worktree.

## Scope

| In the MVP | In phase 2 |
|---|---|
| Hub with the Board and the Orchestrator | pi and Codex Harness adapters |
| Scheduler, store, and recovery | GitHub Issues and GitHub Projects Trackers |
| All Stages, Signals, and Loop budgets | Local Markdown Tracker |
| WIP cap with confirm or raise | Auto-intake |
| Linear browser and Issue actions | Alerts through a Claude channel |
| `lineup` CLI for the Orchestrator | Per-workspace WIP caps |
| Claude Harness adapter | Edits to labels, priority, cycles, and milestones |
| Worktree setup through worktrunk | |
| Alerts in the Orchestrator and toasts from the Board | |

The design for the phase 2 Trackers is in `docs/spec/trackers.md`.

A one-off skill run, for example `/pr-explain <url>`, is a Task with the override `pr=false`. Debug sessions are outside lineup. The user does them in a separate agent session. If one of them finds work, the Orchestrator can make that work a Task. Project planning happens in the Planner tab. The Planner edits Linear and never starts Tasks.

## Dependencies

| Dependency | Use | If it is missing |
|---|---|---|
| Rex (server API revision 7 or later) | Sessions, tabs, blocks, events, program status | `lineup` stops with an error |
| worktrunk (`wt`) | Creates, sets up, and removes worktrees | `lineup` shows `worktrunk is required: brew install worktrunk (https://worktrunk.dev)` and starts no Task |
| GitHub CLI (`gh`) | PR state and merge | `lineup` stops with an error |
| Claude Code | The Orchestrator and the Workers | `lineup` stops with an error |
| Linear OAuth | Tracker access | The tracker browser asks the user to sign in |

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

The `threads` key of the [Landing bar](#landing-bar) picks the Responding contract. The CI debug prompt names the gating checks that failed and the optional checks that failed. It tells the Worker to leave the optional checks.

If a Worker reports `done` and the contract output is missing, the Task gets the Needs you flag with the reason `stopped`. A Worker often stops to show the user a plan or to ask a question. The flag clears when the Worker starts working again, for example after the user replies. The Scheduler does not retry the Stage.

A Responding round that pushes nothing meets its contract when the latest poll saw every review thread done, no conflict, and no review bot score below the minimum. Then the Task goes to CI running, and the poll settles it on its Landing bar. For example, a Worker can reply "won't fix" and resolve each thread. This rule needs a bar that holds threads. With `threads = "ignored"`, a round with no push gets `stopped`.

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
| Ready to land | `pr-merge` (user key `m`) | Done, marked merged |
| Any Stage | `pr-merged` outside lineup, for example by a person or by GitHub auto-merge | Done, marked merged |
| Any Stage | `task-closed` (user key `x`, confirmed) | Done, marked closed |

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
| 8 | The reviews that the bar asks for have not approved | `needs approval`, `changes requested` | In review |
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

The detail pane and `lineup show` print the bar as a checklist, for example:

```
Landing bar
  ✓ required checks passed
  ✗ 1 open thread (1 bot)
  ✓ required reviews approved
  ! optional checks failed: govulncheck
```

A Task keeps the Landing bar that it had at Intake. A Task stored before the Landing bar keeps the rules that lineup had then: every check gates, threads never gate, and every requested reviewer must approve.

### Review bot gate

A Repo can have a review bot gate, which adds the review bot's score to the Landing bar. The gate applies only to the Tasks whose Repo configuration has a `[review_bot]` table. lineup knows one bot, Greptile. Greptile reviews each push by itself and keeps one summary comment on the PR. lineup reads the confidence score and the reviewed commit from that comment. See [ADR 0013](../adr/0013-a-review-bot-gates-ci-passed.md).

- A score counts only when its reviewed commit is the head commit of the PR.
- Only a comment by Greptile's GitHub App counts. The PR body and comments by other authors never give a score.
- Greptile's check run passes at a low score too, so the score is an item of the Landing bar and not a check.
- A PR whose gating checks passed stays in CI running until the bot scores its head commit.
- A score below the minimum starts a Responding round, which spends one review cycle. The Worker prompt names the score and the minimum. It tells the Worker to address Greptile's unresolved review comments and the suggestions in its summary, and to resolve each thread that it addressed.
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
| `worker lost` | The tab or block is gone, and the restart budget is spent |

When the cause clears, for example when program status goes back to `working`, the Scheduler removes the flag.

A gap of the Landing bar that finds its Loop budget spent sets `budget spent`, starts no Worker, and names the gap in the detail. Examples are `2 open threads (1 bot) after 3 rounds` and `test failed after 3 attempts`. The flag clears at the next poll after the gap closes, and the Task goes where its new first gap leads. A `budget spent` flag that a review Signal set stays until the user acts, for example with `lineup rerun`.

### Loop budgets

| Budget | Default | Scope |
|---|---|---|
| CI debug attempts | 3 | per Task |
| Responding rounds | 3 | per review |
| Worker restarts | 2 | per Stage |
| Review cycles | 3 | per Task, only with a review bot gate |

These spend the Responding rounds:

- people's reviews
- open threads, when a person opened one of them
- a merge conflict

A low review bot score spends a review cycle. Open threads that only bots opened, on a score that passes, spend a review cycle too. A PR with no review bot gate has no score, so its bot threads spend Responding rounds.

## Rex layout

```
Rex server
├── session "lineup hub"        (the Hub session)
│   ├── tab "assist"                 (first tab)
│   │   └── block "ASSIST"            claude                 (the whole tab, in assist_dir, own theme)
│   ├── tab "plan"
│   │   ├── block "LINEAR"            lineup board --linear  (top, most of the height)
│   │   └── block "PLANNER"           claude --continue     (bottom, 25% of the height, own theme)
│   ├── tab "work"                   (active when a new Hub opens)
│   │   ├── block "BOARD"             lineup board           (top, most of the height)
│   │   └── block "ORCHESTRATOR"      claude --continue     (bottom, 25% of the height, own theme)
│   └── tab "review"
│       ├── block "REVIEWS"           lineup board --review  (top, most of the height)
│       └── block "REVIEWER"          claude --continue     (bottom, 25% of the height, own theme)
├── session "data"                    (one session for each Repo with an open Task)
│   ├── tab "ENG-590 Add billing model"
│   └── tab "ENG-592 Fix flaky test"
├── session "web"
│   └── tab "WEB-12 …"
└── session "ad hoc"                  (every ad hoc Task, whatever its Repo)
    └── tab "adhoc-9 Evaluate the ADR …"
```

The Scheduler has no tab. launchd runs `lineup serve` as a user agent, and the log is `~/.local/state/lineup/scheduler.log`.

The rules for this layout:

- `lineup` opens the Hub session. If the session does not exist, `lineup` creates it.
- The Hub tabs are in the order assist, plan, work, review. Each run of `lineup` moves every Hub tab back to its place.
- Each Hub tab owns one kind of work. The `plan` tab plans Issues, the `work` tab holds build Tasks, and the `review` tab holds review Tasks.
- A Hub from before the `work` tab has a tab labeled `orchestrate`. `lineup` renames it to `work`, so the Orchestrator in it keeps running.
- The Hub session is named `lineup hub`. A Repo session is named after its folder, and the Scheduler removes it after its last Task. The space keeps a Repo session from taking the Hub's name.
- The Scheduler creates each Task tab with `rex window new -s <session> <label> --focus=false -- <command>`. The user stays on the Hub.
- Each Task tab names its command. A tab without a command reports exit code 0 when its shell exits, whatever the real code is.
- If a Worker exits within about 1 s, Rex keeps its tab open so that the user can read the error. Before the Scheduler restarts that Worker, it closes the tab.
- The Scheduler creates a Repo session with the first Task in that Repo. It removes the session when the last Task tab in it closes and the session holds no other tab. A tab that the user or another Scheduler opened keeps the session.
- If two Repos have the same folder name, the session label adds the parent folder, for example `acme-data`.
- An ad hoc Task opens in the session `ad hoc`, or in the session that `ad_hoc_session` names. The Board still shows its Repo. The Scheduler removes this session after its last Task, as with a Repo session, so `ad_hoc_session` can't be `lineup hub`.
- The Scheduler passes `-s <session>` on every block call. Without it, Rex resolves the block only inside the caller's own session and returns `block target ... not found`.
- Each agent block uses a terminal `theme` with its own background color. The labels are `ORCHESTRATOR`, `PLANNER`, and `REVIEWER`, and each Claude status line starts with the agent's name.

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
| `lineup board` | The Board and the tracker browser. It reads state from the Scheduler and never writes the store. |
| `lineup` CLI | The commands that the Orchestrator uses. Each command calls the Scheduler. |
| Orchestrator folder | `~/.local/state/lineup/orchestrator/`, with a `CLAUDE.md` that defines the Orchestrator's role and the `lineup` commands. |
| Harness adapter | Launches a Harness, trusts the worktree for it, and supplies its status. The MVP has the Claude adapter only. |

### Signal sources

| Source | Signals |
|---|---|
| Rex events: `terminal.program_status_changed`, `terminal.child_exited`, `block_closed` | `done`, `blocked`, `error`, worker lost. `child_exited` comes for every exit. `block_closed` does not come when Rex keeps the tab open. |
| GitHub, one batched GraphQL query for all open Task PRs every 30 s, and one query right after a Worker reports `done` | `pr-open`, `pushed`, `review-*`, `pr-merged`, and `pr-seen` with the PR's facts at each poll |
| The user, through Board keys or the CLI | `start`, `pr-merge`, `task-closed` |
| The Scheduler's clock, at each poll | `review-bot-late` |

The GraphQL query reads these fields of each PR:

- the first 100 contexts of the head commit's `statusCheckRollup`
- `reviewDecision`, `latestReviews`, `reviewRequests`, `commits`, `mergeStateStatus`, and `isDraft`
- the base branch's `refUpdateRule.requiredStatusCheckContexts` and its `rules`
- the first 50 `reviewThreads`, with the author of each thread's first comment and of its last 5 comments

For a Task with a review bot gate, it also reads the author and body of the first 20 PR comments.

A PR whose head commit has no checks meets the check items of the Landing bar, so a Repo with no CI goes on to review. The exception is a Repo whose base branch has checks. GitHub creates a push's checks a few seconds after the push, so there a PR with no checks yet stays in CI running. Without this rule, the Task could reach Ready to land, and the user could merge the PR, before its CI starts.

### Recovery

When `lineup serve` starts, it compares the store with `rex ls` and `rex session inspect`. For each Task in an agent Stage whose tab or block is gone, the Scheduler opens a new tab in the existing worktree and starts a fresh Worker for the same Stage. Each restart uses one Worker restart from the budget.

Recovery also runs after a Rex server restart. The Scheduler reads the Rex server's instance id every 5 s. When the id changes, every Worker is gone, so the Scheduler exits. launchd starts it again and startup recovery runs.

## Start a Task

This sequence runs in the background. The Board shows only the Task as it changes Stage.

1. The Scheduler checks the WIP cap. If the cap is full, the Board asks the user to start this Task once over the cap or to raise the cap. A start over the cap shows `+1 over` in the header.
2. The Scheduler assigns the Issue to the user and sets the `started` status from the team's status map. See [Linear status map](#linear-status-map).
3. The Scheduler picks the Repo. The Repo comes from the team or Project mapping in the configuration. If no mapping exists, a fuzzy picker opens and remembers the choice for that Project.
4. The Scheduler creates the Repo session if it does not exist, and then a tab for the Task with `--focus=false`.
5. In that tab, the Scheduler runs `wt switch --create <branch>`. The branch is Linear's `branchName` for the Issue. The worktrunk `copy` steps and `pre-start` hooks set up the worktree.
6. The Claude adapter marks the worktree as trusted. Claude stores this in `projects[<real path>].hasTrustDialogAccepted` in the `.claude.json` of the Claude configuration folder that the Repo uses. Without this step, the Worker stops at a trust prompt that reports no program status. The rules for this step:
   - The configuration folder comes from `direnv exec <Repo main checkout> printenv CLAUDE_CONFIG_DIR`. An empty value is the default profile, `~/.claude.json`. The worktree path does not decide it, because worktrees live outside the Repo.
   - The adapter takes Claude's own lock, the folder `.claude.json.lock`, and replaces the file with one rename. A lock folder older than 10 s is abandoned.
   - The Worker command sets `CLAUDE_CONFIG_DIR` itself, because Rex starts it through a login shell that runs no direnv.
7. The Scheduler starts the Worker from the absolute path of the Harness, with the Stage's model, effort, prompt, and contract.
   - The Worker command passes `--name <tab label> --remote-control <tab label>`. The tab label starts with the Task ID, so the user finds each Worker by its Task ID on claude.ai and on their phone. Each Hub agent gets the same flags, with the name `lineup-<tab>`: `lineup-work`, `lineup-plan`, `lineup-review`, and `lineup-assist`.
   - `--remote-control` takes an optional value, so the command always gives it the name. Without a value, it takes the prompt as the name.

Each agent Stage after Working starts a fresh Worker in the same tab and worktree. The Worker gets its context from the Issue and the PR.

## Configuration

The precedence is, from lowest to highest:

1. The global file, `~/.config/lineup/config.toml`
2. A `[[repos]]` entry whose `match` glob matches the Repo path. The most specific glob wins.
3. The Repo file, `.lineup.toml`, checked in to the Repo
4. A `[stage.<name>]` section
5. A Task override, for example "do this with pi"

```toml
# ~/.config/lineup/config.toml
wip_cap = 5
poll_interval = "30s"
start_skills = ["kit:tdd"]

[budgets]
ci_debug = 3
responding = 3
restarts = 2

[harness.claude]
path = "~/.local/bin/claude"

[stage.default]
harness = "claude"
model = "claude-opus-5-5"
effort = "high"

[[repos]]
match = "~/code/*/*"

[[repos]]
match = "~/code/acme/*"

[repos.review_bot]
name = "greptile"
min_review_score = 5
max_review_cycles = 5

[[repos]]
match = "~/code/acme/data"
teams = ["APP", "ENG"]
```

A `[[repos]]` `match` is a path or a glob. It sets keys for the Repos that it matches, and it tells lineup where the checkouts are. lineup uses each git checkout that a `match` names:

- `lineup add --repo <name>` and a Local Issue's `repo:` take the folder name of one such checkout. If two checkouts have the name, pass a path.
- A GitHub Issue goes to the checkout with the repository's name whose `origin` is that repository.
- The Repo picker lists these checkouts.

An entry with only a glob, such as `match = "~/code/*/*"` above, lists every checkout two folders below `~/code`.

`[review_bot]` turns on the review bot gate. Put it in the `[[repos]]` entry for the Repos where the bot is installed, as `[repos.review_bot]` above, or in a Repo's `.lineup.toml`. The example gates each Repo below `~/code/acme`, and no other Repo.

| Key | Value | Default |
|---|---|---|
| `name` | The bot. The only bot is `greptile`. | none, required |
| `min_review_score` | The lowest score that passes, from 1 to 5 | 5 |
| `max_review_cycles` | The review cycle budget | 3 |

A higher layer can set one key and keep the others from a lower layer, as in `[budgets]`. lineup checks each `[review_bot]` table when it loads the configuration. A Task keeps the gate that it had at Intake.

`[landing_bar]` sets the [Landing bar](#landing-bar) of build Tasks. It can go in each layer: the global file, a `[[repos]]` entry as `[repos.landing_bar]`, and a Repo's `.lineup.toml`. With no `[landing_bar]` table, each key has its default.

| Key | Values | Default |
|---|---|---|
| `checks` | `required`: the base branch's required checks gate. If the base branch requires no check, every check gates. `all`: every check gates. | `required` |
| `threads` | `replied`: each review thread is resolved, and the user replied in each thread that someone else opened. `resolved`: each thread is resolved. `ignored`: threads never gate. | `replied` |
| `approvals` | `required`: the reviews that the base branch requires approve. `requested`: also every open review request, unless the decision is `APPROVED`. | `required` |

```toml
# .lineup.toml in a Repo where every requested reviewer must approve
[landing_bar]
approvals = "requested"
checks = "all"
```

Each key layers on its own, as in `[budgets]`. A value that is not in the table stops the load with an error that names the key, for example `landing_bar.threads: "any" is not one of ignored, resolved, replied`. A Task keeps the Landing bar that it had at Intake.

`start_skills` is a top-level key in the global file. It lists the skills that the tracker browser offers when the user starts a Task. A chosen skill is stored as the Task override `skill`.

`ad_hoc_session` is a top-level key in the global file. It names the Rex session for ad hoc Tasks. The default is `ad hoc`.

`assist_dir` is a top-level key in the global file. It names the folder where the `assist` tab's agent starts, for example `~/repos`. The default is the home folder. The assist agent has no lineup role, so it is a general agent for one-off questions. It starts a new conversation each time the Hub opens its tab, because the folder can hold the user's own conversations.

`linear_client_id` is a top-level key in the global file. Put it above the first `[table]`, or TOML reads it as part of that table. Leave it out to sign in through lineup's own Linear OAuth app, which any workspace can use. To use your own app, register an OAuth app in Linear with the redirect URI `http://localhost:47821/callback` and no client credentials. Then set its client ID here.

Some workspaces let members use only apps that an admin approved. For such a workspace, register an app in that workspace and set its client ID as `client_id` under `[linear.<url key>]`. Then run `lineup login <url key>`. lineup refuses the sign-in if you pick another workspace on Linear's consent screen. Each workspace's tokens keep the app that issued them, so a new client ID takes effect at the next `lineup login`.

lineup merges a PR only when the user asks, with `m` on the Board or with `lineup merge <task>`. To merge each PR when its checks pass, turn on auto-merge in the forge, for example `gh pr merge --auto` on GitHub.

The Task override `pr=false` is for work that ends without a pull request. Its Working contract is "Do not push a branch or open a pull request. Say what you produced, then stop." The Task then gets the flag `stopped`, and the user closes it.

```toml
# .lineup.toml in a Repo
[stage.ci_debug]
skill = "diagnosing-bugs"

[review_bot]
min_review_score = 4
```

The Scheduler finds each Harness path when it loads the configuration. Rex starts commands through the login shell, which can resolve a name to a different binary than the Scheduler sees.

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

The Board shows the build Tasks. Review Tasks show on the Reviews tab of the `review` tab (see `docs/spec/reviews.md`).

```
 lineup  WIP 4/5   ●2 need you   ◐2 working   ◷1 waiting   3 queued
 ─────────────────────────────────────────────────────────────────────────
 NEEDS YOU
 ▸ ENG-590 Working ⚑       data   Add billing model          stopped      12m
   ENG-588 Working ⚑       data   Backfill events           permission: Bash(make deploy)   3m
 WORKING
   WEB-12  Responding      web    Fix map tooltip           round 1/3    5m
   ENG-592 CI debug        data   Fix flaky test            attempt 2/3  1m
 WAITING
   ENG-571 In review       data   Rate limit login          #4636 needs approval · optional failed: lint  1d
 QUEUED
   ENG-601                 data   Drop old API keys
 DONE
   ENG-560 merged          data   Rename config options     #4601  today
```

| Key | Action |
|---|---|
| `j` `k` | Move the selection |
| `enter` | Focus the Task's tab in Rex |
| `p` | Show a live preview of the Worker's screen beside the Board |
| `f` | Show the detail pane at full screen, and back |
| `m` | Merge the PR (Ready to land) |
| `x` | Close the Task, after a confirmation. On a Done Task, archive it after a confirmation. |
| `s` | Start a Queued Task |
| `o` | Open the PR or the Issue in the browser |
| `R` | Refresh now: the Scheduler polls GitHub for every started Task |
| `z` | Zoom this tab's agent pane to full height, and back |
| `?` | Show all keys |

A Done Task leaves the Board 7 days after it ends, or when the user archives it (`x` or `lineup archive <task>`). The Scheduler keeps the Task and its events, so `lineup show <task>` still finds it. To put an archived Issue back on the Board, run `lineup add` again.

## Tracker browser

The tracker browser runs in the top pane of the `plan` tab, and the Board runs in the top pane of the `work` tab. Neither pane can switch to the other view.

The tracker browser has a row of tabs at the top. Each tab is one of the user's Linear favorites that is a view. The last tab is Browse.

```
 [Current+ (Ada)]  Current+ (Team)   ENG Issues   ENG Projects   Browse
 Cycle 10  Oct 28 → Nov 11  7 Issues
  In QA  1
 ▸ ··· ENG-537  Fix login bounce             #4810  ● Bug  ◇ Billing  △3  AL  Oct 17
```

### View tabs

To add a tab, the user stars the view in Linear. lineup shows these favorites:

| Favorite | Tab | Content |
|---|---|---|
| Custom Issue view | The view's name | The view's Issues, with Linear's filter |
| Custom Project view | The view's name | The view's Projects, with Linear's filter |
| A team's Active Issues | Team key and "Issues", for example `ENG Issues` | The team's unstarted and started Issues |
| A team's Projects | Team key and "Projects", for example `ENG Projects` | The team's open Projects |

lineup leaves out every other favorite.

The user ranks the tabs with `<` and `>`. lineup keeps the rank in `~/.local/state/lineup/views.json`. The tab order is:

1. The saved views that are still favorites, in the saved order.
2. The other favorites, in the order of Linear's sidebar.
3. Browse.

The browser opens on the first tab.

An Issue view groups its Issues as Linear does:

1. Cycle, by start date. Issues with no cycle come last.
2. State type: started, unstarted, backlog, triage, completed, canceled. Inside a type, Linear's state position, high first.
3. Priority: urgent, high, medium, low, then no priority.
4. Linear's manual order.

A header names each cycle, its dates, and its Issue count. A sub-header names each state and its count. A search hides the headers.

A Project list groups by status as Linear does: started, planned, backlog, paused, completed, canceled. Then it sorts by status position, priority, and manual order. Browse uses the same Project order.

`esc` never goes above a view tab. In a Project view, `enter` opens the Project's Issues, and `esc` goes back to the view.

### Browse

Browse starts at a switcher for the Linear workspaces (`acme`, `globex`). The tree is workspace, then team, then Project, then Issues. A "My Issues" view shows the user's Issues in all workspaces. If the user has no favorite views, Browse is the only tab.

My Issues and Project Issue lists use the same order:

1. Dependency level. An Issue with no open blocker has level 0. Any other Issue has a level one higher than its highest open blocker. Only open "blocks" relations count, and blockers in other Projects count too. A cycle is broken as in `herdr-linear` `graph.go`.
2. Natural ID order, so that ENG-9 comes before ENG-10.

### Rows

An Issue row shows the priority, the identifier, the state, the title, and these chips: PRs, labels, Project, estimate, assignee initials, and created date. A grouped view leaves out the state, because the header shows it. On a narrow screen, chips drop from the left so that the title keeps at least 24 cells. A blocked row names its open blockers, for example `blocked by ENG-590`.

A Project row shows the priority, the name, the health, the lead, the target date, and the progress.

### Keys

| Key | Action |
|---|---|
| `tab` `shift+tab` | Open the next or previous tab |
| `<` `>` | Move this view's tab left or right, and save the rank |
| `/` | Fuzzy search |
| `enter` | Open the row |
| `esc` | Go back up one level |
| `s` | Open the skill picker, then start the Issue as a Task. The first row is `just go`, which starts with no skill. The other rows are the skills in `start_skills`. Typed text filters these rows, and a typed name that is not in the list becomes its own row. A leading `/` is dropped. |
| `c` | Change the status |
| `A` | Assign the Issue to the user |
| `n` | Add an Issue quickly, in a Project or a team's view |
| `o` | Open the Issue in the browser |
| `R` | Read again what is on screen: the Issue list, the open Issue, or the teams or Projects |

A typed `R` in a search is search text.

## Orchestrator

The Orchestrator has four jobs:

| Job | Example | `lineup` command |
|---|---|---|
| Intake | "start ENG-123", "fix the flaky test in data" | `lineup add ENG-123`, `lineup add --repo data "<text>"` |
| Status | "what needs me?" | `lineup ls --json`, `lineup show <task>` |
| Steer | "tell ENG-592 to use approach B" | `lineup steer <task> "<text>"` |
| Override | "do this with pi", "raise the cap to 6", "re-run CI debug" | `lineup set <task> harness=pi`, `lineup cap 6`, `lineup rerun <task> ci_debug` |

The Orchestrator handles only build Tasks. It sends each review request, `/pr-review` included, to the Reviewer in the `review` tab. Its settings deny `lineup review` and `lineup reviews`.

If `lineup add` or `lineup start` meets a full WIP cap, the command fails with `wip cap full`. The Orchestrator must then ask the user in chat. It must not use `--over-cap` without the user's yes.

### Alerts

A Task alerts when it enters the Needs you group or Done. It alerts again only after it leaves that state and enters a state that needs the user. Two parts show each alert:

| Part | What it does |
|---|---|
| `lineup watch` | Prints one line for each alert of a build Task, for example `ENG-34 Working, stopped: <title>`. It keeps the alerts it printed in `alerted.json` in the state directory, so a restart repeats none of them. On its first run, Tasks that are already Done print nothing. A lock on `watch.lock` allows one watch at a time. With `--once`, it exits after the first Board that has new alerts. |
| The boards in the `work` and `review` tabs | Each prints an OSC 777 notification for each alert of its own Tasks: build Tasks in `work`, review Tasks in `review`. Rex shows it as a desktop toast for that board's pane. A click on the toast opens that pane. The view that a board opens on makes no toast. |

The Hub starts the Orchestrator with a first message that tells it to run `lineup watch --once` as a background Bash command. The command exits at the next alerts, and the exit wakes the Orchestrator. The Orchestrator tells the user what changed and what the user can say next, then starts the command again. An idle Orchestrator writes nothing to its chat.

## Planner

The Planner is a Claude agent in the bottom bar of the `plan` tab, below the Linear browser. That browser opens on the user's favorite Linear views, in the user's rank (see Tracker browser). The Planner shapes the backlog in Linear with the `linear` CLI. It has four jobs:

| Job | Example | `linear` command |
|---|---|---|
| Read | "show ENG-614", "what is in this Project?" | `linear issue view`, `linear project view`, `linear issue query` |
| Split | "split this Project into Issues" | `linear issue create` |
| Criteria | "write acceptance criteria for ENG-614" | `linear issue update` |
| Order | "ENG-615 needs ENG-614 first" | `linear issue relation add ENG-614 blocks ENG-615` |

The Planner never starts Tasks. It must not run `lineup add`, `lineup start`, `linear issue start`, or `linear issue pull-request`. Its settings deny these commands. To start work, the user presses `s` in the Linear browser, or asks the Orchestrator in the `work` tab. The Planner can read `lineup ls --json` to see what is in flight.

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
| The Scheduler reads program status correctly | An integration test that runs `claude` in a throwaway Rex session and reads `working`, `done`, and `blocked` (`kind: permission`) |
| Recovery restarts a lost Worker | An integration test that kills a Task tab and restarts `lineup serve` |
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
| A conversation that `--continue` resumes keeps its claude.ai title. `--name` and `--remote-control` set only the name in the prompt box. `/rename` sets the claude.ai title, and the title stays through later resumes | A probe session titled `old-title`, resumed with `--name new-title --remote-control new-title`, then `/rename new-title`, then resumed again, read on claude.ai after each step |
| Greptile keeps one summary comment on a PR. The comment holds `<!-- greptile_confidence_score:N -->` and `Last reviewed commit: …/commit/<sha>`. | `gh api repos/better-auth/better-auth/issues/12303/comments`: the `greptile-apps[bot]` comment holds score 4 and the head commit. `internal/github/testdata/review_bot.json` is a trimmed copy. |
| GraphQL names Greptile's author `greptile-apps`, with no `[bot]` suffix | `Client.Snapshot` against better-auth/better-auth#12303 |
| The `Greptile Review` check run passes at a score below 5 | `gh api repos/better-auth/better-auth/commits/eb6b436/check-runs`: conclusion `success` at score 4/5 |
| Greptile edits its summary about 2 s after its check run completes | The same PR: the check run completed at 18:00:46Z, and the comment changed at 18:00:48Z |
| Some Greptile installs write the summary into the PR body, which the PR author can edit | `gh api repos/synnaxlabs/synnax/pulls/4881`: the body holds the score, and Greptile wrote no comment |
| A viewer with read access can read the required checks of classic branch protection in `refUpdateRule` | The poll's GraphQL query against cli/cli#13013: `requiredStatusCheckContexts` names the three `build (…)` checks. `internal/github/testdata/classic_protection.json` is a trimmed copy. |
| `refUpdateRule` does not show a ruleset's required checks. `rules` does, by branch name, with no PR number. | The poll's GraphQL query against GClunies/lineup#9: `refUpdateRule` is `null`, and an `ACTIVE` `REQUIRED_STATUS_CHECKS` rule names `test`. `internal/github/testdata/ruleset.json`. |
| A review thread's first comment tells a bot from a person: its author's `__typename` is `Bot` or `User` | The poll's GraphQL query against cli/cli#12942: 7 threads that bots opened and 4 that users opened. `internal/github/testdata/threads.json`. |
