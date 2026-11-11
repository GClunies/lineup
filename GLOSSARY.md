# lineup

A board and an orchestrator agent that move agent work from a tracker issue to a landed pull request, across all of the user's repos, on top of Rex.

## Language

### Scope

**Repo**:
A git repository that Tasks run in. A Repo maps to zero or one Tracker workspace and to one or more of its teams.
_Avoid_: Project, codebase, area

**Tracker**:
The system that holds planned work. One Tracker is a Linear workspace, for example `acme`, the user's GitHub account, or the Local Tracker.
_Avoid_: Backlog, issue source, provider, area

**Local Tracker**:
The Tracker that lineup keeps on the user's machine: one folder of Markdown Issues, outside every Repo. It is the Tracker for a user who has no Linear workspace and no GitHub Tracker.
_Avoid_: Local board, backlog file

**Kind**:
Which sort of Tracker an Issue comes from: Linear, GitHub, or local.
_Avoid_: Provider, type, source

**Issue**:
A unit of planned work in a Tracker.
_Avoid_: Ticket, card

**Project**:
A Tracker's own group of Issues, for example a Linear project.
_Avoid_: Area, epic, repo

**Task**:
One unit of work on the Board, in exactly one Repo, done by Workers in one isolated tab and worktree. A Task links to zero or one Issue, and it ends when its PR merges or the user closes it. A Task that ends goes to Done and keeps its tab and worktree. Only the user closes a Task's tab.
_Avoid_: Job, thread, card, item

**Ad hoc Task**:
A Task with no Issue, made from the user's own words. Its ID has the form `adhoc-<n>`, and its tab opens in the ad hoc session instead of its Repo's session.
_Avoid_: Text Task, one-off

**Intake**:
The act of putting a Task on the Board, from the tracker browser, the Orchestrator, or the CLI. Auto-intake is Intake that the Scheduler does by itself from a Tracker status.
_Avoid_: Import, claim, enqueue

### Surfaces

**Board**:
The top of the `hub` tab, above the Orchestrator. It has three views, and the keys `1`, `2`, and `3` pick one. `1 plan` is the tracker browser. `2 work` lists the build Tasks, grouped as Needs you, Working, Waiting, Queued, and Done. `3 review` has a Reviews tab that shows the review Tasks in the same groups. A Done Task leaves the Board when it is archived. It also leaves 7 days after it ends, unless its tab is still open.
_Avoid_: Dashboard, kanban, inbox

**Archived**:
A Done Task that the user hid from the Board. Archiving closes the Task's tab and removes its worktree, if they are still there. Its record and events stay in the store.
_Avoid_: Deleted, removed

**Hub**:
The Rex session of lineup's own tabs. `assist` holds a general agent, and `hub` holds the Board and the Orchestrator in the `LINEUP` pane. The Orchestrator is the one agent for the Board.
_Avoid_: Home, control tab

### Actors

**Scheduler**:
The deterministic part of lineup that owns the queue, the WIP cap, Worker startup, and Task transitions. It never judges the work.
_Avoid_: Coordinator, daemon, engine

**Orchestrator**:
The long-lived agent that the user talks to in the `LINEUP` pane of the Hub's `work` tab. It plans Issues in the Trackers, does Intake, starts reviews, reports status and alerts, passes the user's words to Workers, and applies overrides, for build Tasks and review Tasks alike, always through the Scheduler.
_Avoid_: Coordinator, manager agent

**Worker**:
The agent that does every agent Stage of one Task, inside that Task's own tab and worktree. The Scheduler types each new Stage's prompt into the same Worker when it is idle.
_Avoid_: Thread, sub-agent, runner

**Harness**:
The agent program that a Worker or the Orchestrator runs in, for example Claude Code, pi, or Codex.
_Avoid_: Provider, runner, vendor

### Lifecycle

**Stage**:
The place in the lifecycle where a Task waits, with exactly one owner: an agent, CI, other reviewers, or the user. The Stages are Queued, Working, CI running, CI debug, In review, Responding, Ready to land, and Done. Each agent Stage is a new prompt to the Task's one Worker, not a new Worker.
_Avoid_: Status, state, column, phase

**Stage contract**:
The output that an agent Stage must leave for the Scheduler to observe, for example an open PR after Working. It says what to produce, never how.
_Avoid_: Definition of done, exit criteria, skill

**Signal**:
An observed event that moves a Task from one Stage to another, for example `pr-open`, `pushed`, `pr-seen`, `review-commented`, `review-changes-requested`, or `review-additional-authors`. `pr-seen` is a level: each poll sends it with the PR's current facts.
_Avoid_: Trigger, transition, state

**Needs you**:
A flag on a Task, in any Stage, that says the user must act and gives the reason: a question, a permission, an auth request, an error, a Worker that stopped before its Stage contract, a draft PR that passed CI, a review bot that did not score a passing PR, or a spent Loop budget.
_Avoid_: Blocked, waiting, attention state

**Ready to land**:
The Stage where a PR that meets its Landing bar waits for the user to merge it. The Task leaves it when the PR stops meeting the bar.
_Avoid_: Mergeable, approved

**Landing bar**:
What a PR must meet for its build Task to be in Ready to land: the required checks pass, the review bot's score meets the minimum, every review thread is done, and the required reviews approve. A Repo's `[landing_bar]` table can change each item. A Task keeps the Landing bar that it had at Intake.
_Avoid_: Definition of done, merge criteria, readiness

**Standing**:
A PR measured against its Task's Landing bar at the latest poll. Its first gap decides where the Task goes, for example CI debug for a required check that failed.
_Avoid_: PR status, readiness

**Required check**:
A check that the base branch's rules require. If the base branch requires no check, every check is a required check. Every other check is optional: the Board shows an optional check that failed, and lineup does not act on it.
_Avoid_: Blocking check, mandatory check

**Review thread**:
A thread of review comments on a line of a PR. Under the default Landing bar, a thread is done when it is resolved and the user replied in it. A thread that the user opened needs only the resolve.
_Avoid_: Conversation, comment

**Loop budget**:
The most automatic attempts that a Stage gets before the Task gets the Needs you flag, for example 3 CI debug attempts.
_Avoid_: Retry limit, max attempts

**Review bot**:
A code review bot, for example Greptile, that scores each push to a PR. A Repo's review bot gate keeps a passing PR in CI running until the bot scores its head commit, and a score below the minimum starts a review cycle.
_Avoid_: Reviewer, AI reviewer

**Review cycle**:
A Responding round that the review bot starts: a score below the minimum, or open threads that only bots opened on a score that passes. Review cycles have their own Loop budget, apart from the Responding rounds that people's reviews start.
_Avoid_: Bot round, greploop, review round

### Capacity

**WIP cap**:
The most started Tasks, from Working through Ready to land, that can exist at one time. To start a Task past it, the user must confirm or raise the cap.
_Avoid_: Lane count, concurrency limit
