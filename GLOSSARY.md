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
One unit of work on the Board, in exactly one Repo, done by Workers in one isolated tab and worktree. A Task links to zero or one Issue, and it ends when its PR merges or the user closes it.
_Avoid_: Job, thread, card, item

**Ad hoc Task**:
A Task with no Issue, made from the user's own words. Its ID has the form `B-<n>`, and its tab opens in the ad hoc session instead of its Repo's session.
_Avoid_: Text Task, one-off

**Intake**:
The act of putting a Task on the Board, from the tracker browser, the Orchestrator, or the CLI. Auto-intake is Intake that the Scheduler does by itself from a Tracker status.
_Avoid_: Import, claim, enqueue

### Surfaces

**Board**:
The view of the build Tasks in the `work` tab, grouped as Needs you, Working, Waiting, Queued, and Done. The review tab's Reviews tab shows the review Tasks in the same groups. A Done Task leaves the Board when it is archived or 7 days after it ends.
_Avoid_: Dashboard, kanban, inbox

**Archived**:
A Done Task that the user hid from the Board. Its record and events stay in the store.
_Avoid_: Deleted, removed

**Hub**:
The Rex session of lineup's own tabs. Each tab owns one kind of work: `plan` holds the tracker browser and the Planner, `work` holds the Board and the Orchestrator, and `review` holds the review board and the Reviewer.
_Avoid_: Home, control tab

### Actors

**Scheduler**:
The deterministic part of lineup that owns the queue, the WIP cap, Worker startup, and Task transitions. It never judges the work.
_Avoid_: Coordinator, daemon, engine

**Orchestrator**:
The long-lived agent that the user talks to in the Hub's `work` tab. It does Intake, reports status, passes the user's words to Workers, and applies overrides for build Tasks, always through the Scheduler. It does not plan Projects or touch review Tasks.
_Avoid_: Coordinator, manager agent

**Worker**:
An agent that does one Stage of a Task inside that Task's own tab and worktree.
_Avoid_: Thread, sub-agent, runner

**Harness**:
The agent program that a Worker or the Orchestrator runs in, for example Claude Code, pi, or Codex.
_Avoid_: Provider, runner, vendor

### Lifecycle

**Stage**:
The place in the lifecycle where a Task waits, with exactly one owner: an agent, CI, other reviewers, or the user. The Stages are Queued, Working, CI running, CI debug, In review, Responding, Ready to land, and Done.
_Avoid_: Status, state, column, phase

**Stage contract**:
The output that an agent Stage must leave for the Scheduler to observe, for example an open PR after Working. It says what to produce, never how.
_Avoid_: Definition of done, exit criteria, skill

**Signal**:
An observed event that moves a Task from one Stage to another, for example `pr-open`, `ci-failed`, `review-approved`, `review-commented`, `review-changes-requested`, or `review-additional-authors`.
_Avoid_: Trigger, transition, state

**Needs you**:
A flag on a Task, in any Stage, that says the user must act and gives the reason: a question, a permission, an auth request, an error, a Worker that stopped before its Stage contract, a draft PR that passed CI, a review bot that did not score a passing PR, or a spent Loop budget.
_Avoid_: Blocked, waiting, attention state

**Ready to land**:
The Stage where an approved PR with passing CI waits for the user to merge it.
_Avoid_: Mergeable, approved

**Loop budget**:
The most automatic attempts that a Stage gets before the Task gets the Needs you flag, for example 3 CI debug attempts.
_Avoid_: Retry limit, max attempts

**Review bot**:
A code review bot, for example Greptile, that scores each push to a PR. A Repo's review bot gate keeps a passing PR in CI running until the bot scores its head commit, and a score below the minimum starts a review cycle.
_Avoid_: Reviewer, AI reviewer

**Review cycle**:
A Responding round that a review bot score below the minimum starts. Review cycles have their own Loop budget, apart from the Responding rounds that people's reviews start.
_Avoid_: Bot round, greploop, review round

### Capacity

**WIP cap**:
The most started Tasks, from Working through Ready to land, that can exist at one time. To start a Task past it, the user must confirm or raise the cap.
_Avoid_: Lane count, concurrency limit
