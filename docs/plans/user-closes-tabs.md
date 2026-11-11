# User closes tabs plan

Lineup stops closing Task tabs by itself. The user is Greg, who starts every Task, merges every PR, and now closes every Task tab. Each Task keeps one Worker for its whole life. The Scheduler types each new Stage's prompt into that Worker when it is idle. A Task that ends keeps its tab and worktree until Greg closes it. A tab that Greg closes stays closed until he restores it. The Orchestrator asks him what to do through `AskUserQuestion`. The rule the program enforces is that only the user closes a Task tab, and only the user opens one again. The PR ids in order are S1a, S1b, S1c, and S1d, all in one stack.

## How to read this

One box is one unit of work. Every box names the evidence that checks it. A nested box is a sub-step of the box above it. Check a box only when its evidence exists, a file, a log line, a screenshot, a test run, or a SHA. The body is a how-to. The appendices explain and record.

The program runs `pstack/skills/poteto-mode/playbooks/autopilot-stack.md`. The operator lands the stack with `gh stack merge <top PR> --yes`. Every PR is the operator's item and stops at merge-ready.

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

Live lanes run on this machine, not on cloud VMs. Lineup is a solo TUI on Rex, and its surface exists only here. Screenshots are `rex capture` text dumps saved as `.txt` where a lane names `.png`, because the surface is a terminal.

## Program checklist

### Arm the program

- [ ] State the protocol and this plan to the operator, then stop. Start execution only on the operator's explicit go.
- [ ] Read these from trunk at program start. Re-read them at every tick.
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/autopilot-stack.md`
  - [ ] `git show origin/main:pstack/skills/swarm/SKILL.md`
  - [ ] `git show origin/main:cursor-team-kit/skills/control-cli/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/opening-a-pr.md`
  - [ ] `git show origin/main:pstack/skills/gh-stack/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/architect/SKILL.md`
- [ ] On the operator's go, arm the audit tick as `/loop 1h` with the tick prompt below. Never leave the cadence to memory.
- [ ] Use this tick prompt, verbatim. "Re-read the execution playbook from trunk. Audit the operation against it and fix drift in this tick. Probe every active lane and judge progress by side effects only. Stand down a stuck lane and dispatch its replacement now. Then post a short status message to the operator in chat only when the audit found a tracked change that no earlier status message reported, such as a PR opened, a code-ready head, a round launched or closed, a verdict, a merge, a stuck agent and the action taken, a blocker added or cleared, or a decision only the operator can make. Name every such change and nothing else. Do not repeat a table, the merged list, or an unchanged blocker. If the audit found none, end the turn with no reply text. Either way, log this tick's row in your decision trail. The row names the items reported, or none."
- [ ] On the operator's hold or stand-down, send every owner a zero-writes order at once.

### Spawn owners

- [ ] Spawn one owner per PR with the full lifecycle the execution playbook names.
- [ ] Follow this dependency graph. Start dependent work only after its parent merges, or base it on the parent branch when the execution playbook stacks.
  - [ ] S1a is first. It branches from `main`.
  - [ ] S1b after S1a. S1c after S1b. S1d after S1c. The stack is `keep-tab-at-end`, `one-worker`, `restore-tab`, `hub-asks`.
- [ ] Hold the file boundaries. S1a touches only `internal/lifecycle/*`, `internal/scheduler/*`, `internal/board/*`, `GLOSSARY.md`, `docs/spec/*`, `docs/adr/0017-*`. S1b touches only `internal/lifecycle/*`, `internal/scheduler/*`, `internal/harness/claude/*`, `internal/config/*`, `cmd/lineup/wire.go`, `GLOSSARY.md`, `docs/spec/*`, `docs/adr/0004-*`, `docs/adr/0018-*`. S1c touches only `internal/lifecycle/*`, `internal/scheduler/*`, `internal/config/*`, `internal/board/*`, `cmd/lineup/cli.go`, `cmd/lineup/main.go`, `cmd/lineup/*_test.go`, `GLOSSARY.md`, `docs/spec/*`. S1d touches only `cmd/lineup/agent/CLAUDE.md`, `cmd/lineup/hub_test.go`, `docs/spec/mvp.md`.
- [ ] Hold the review gate. S1a, S1b, S1c, and S1d change an interaction. They wait for the operator's review in chat with screenshots and a video before merge.

### PR mechanics, for every PR

- [ ] Resolve the forge once. Default to `gh`; if `command -v origin` succeeds and Origin can resolve the repository, use `origin pr` for every PR operation. Record any fallback to `gh`. Never require `gt`.
- [ ] Open the PR ready, never draft, per **Opening a PR**. Use `gh stack submit --auto --open`. A stack child targets its parent branch.
- [ ] Before a child PR opens, push its parent branch, then add the parent to the ruleset "CI must pass", per `CLAUDE.md`. Remove each branch from that ruleset after the stack lands.
- [ ] Run the repo's lint and typecheck once before the PR-facing push. Push with hooks on. The repo's CI trio is `test -z "$(gofmt -l .)"`, `go vet ./...`, `go test -race -count=1 ./...`.
- [ ] Run `/deslop` before each commit and `/no-comments` before review.
- [ ] Triage every Bugbot and security-reviewer comment per `../references/bugbot-triage.md`.
- [ ] Rebase onto current trunk before the code-ready report and babysit. Keep that merge base in fix rounds. Rebase again only at merge prep, on a `git merge-tree` conflict with trunk, on a CI failure that comes from a change on trunk, or when a known concurrent change to the same files lands.

### Verdict and merge, for every PR

- [ ] A full swarm runs once, at the code-ready head. Another full swarm runs only for new behavior, a redesign, or a large new diff. A rebase, a merge-conflict resolution, or a minor fix gets a single verifier at the new head, and a wording-only change gets none. Run each full swarm per `pstack/skills/swarm/SKILL.md`. One gates lane. The ten live lanes from the PR's **Verify, live** block. The perf lane from its **Verify, perf** block. Two or more audit lanes, each with its own focus, that read the diff and the receipts and distrust the PR body. The root audits the receipts in the merge-ready report before the verdict.
- [ ] Judge every finding by the execution playbook's blocking rule. The verdict is clean when none blocks. A failure on trunk behavior in a lane marked `Regression lane against trunk.` is recorded against trunk and does not block the PR. A new head gets a fresh verdict, except for results that stay valid under the patch-id rule in `playbooks/shipping.md`.
- [ ] The root appends each verified PR to the stack. The operator lands the stack bottom-up with `gh stack merge <top PR> --yes` once every PR in it has a clean verdict at its head SHA, per the patch-id rule in `playbooks/shipping.md`.

### Boot recipe, for every live lane

Each live lane runs on this machine at the PR head, in a worktree that `wt` creates. Drive through `control-cli` from `cursor-team-kit`.

- [ ] Once per program, create the private repo `GClunies/lineup-sandbox` with one file and a `test` workflow that fails when the file `FAIL` exists. Record the repo URL in the decision trail.
- [ ] `git fetch origin <head-branch> && git checkout <head SHA>`.
- [ ] Clone the sandbox to `/tmp/swarm-<pr-id>/worker-<n>/sandbox-<pr-id>-<n>`, so each lane's Repo session has its own name.
- [ ] Write `/tmp/swarm-<pr-id>/worker-<n>/config.toml` with `[stage.default] model = "claude-haiku-5-5"`, one `[[repos]]` entry that matches only that clone, no `[linear]` table, and no `[review]` queues. The operator's config polls real repos, Linear, and review queues, so a lane never loads it.
- [ ] `go build -o /tmp/lineup-<pr-id> ./cmd/lineup`. Leave the operator's Scheduler running. Start the head Scheduler with `LINEUP_STATE_DIR=/tmp/swarm-<pr-id>/worker-<n>/state LINEUP_CONFIG=/tmp/swarm-<pr-id>/worker-<n>/config.toml /tmp/lineup-<pr-id> serve &`. Wait for `lineup.sock` in that state folder. Run every `lineup` command of the lane with the same two variables.
- [ ] Use ad hoc Tasks in the lane's clone.
- [ ] Deliver input only through `rex type`, `rex window close`, and `rex capture` on the lane's own Rex session, and through `gh pr merge` on the sandbox repo. Read-only diagnostics are `lineup ls --json`, `lineup show <id>`, `rex events --json -s <session>`, `rex window list -s <session> --json`, and `sqlite3 state.db 'select * from events'`.
- [ ] Save every capture to `/tmp/swarm-<pr-id>/worker-<n>/<slug>.txt` and return the paths with the report.
- [ ] Never run `lineup stop`, `lineup restart`, or `rex server stop` in a lane. The first two act on the operator's launchd Scheduler, and the third closes every tab the operator has. To restart the head Scheduler, kill the pid in the lane's `serve.lock` and start it again with the same command.
- [ ] After the lane, kill the head Scheduler, then run `rex session kill` on each Rex session that the lane's Scheduler made.

## Keep the tab when a Task ends (S1a)

**Depends on.** None.

**Files.**

- [ ] Edit `internal/lifecycle/board.go`.
- [ ] Edit `internal/lifecycle/signal.go`.
- [ ] Edit `internal/lifecycle/board_test.go`.
- [ ] Edit `internal/lifecycle/review_test.go`.
- [ ] Edit `internal/scheduler/scheduler.go`.
- [ ] Edit `internal/scheduler/api.go`.
- [ ] Edit `internal/scheduler/scheduler_test.go`.
- [ ] Edit `internal/scheduler/review_test.go`.
- [ ] Edit `internal/board/model.go`.
- [ ] Edit `internal/board/view.go`.
- [ ] Edit `internal/board/review_view.go`.
- [ ] Edit `internal/board/board_test.go`.
- [ ] Edit `GLOSSARY.md`, `docs/spec/mvp.md`, and `docs/spec/reviews.md`.
- [ ] Create `docs/adr/0017-the-user-closes-every-task-tab.md`.

**Build.**

- [ ] In `board.go` `finish()`, stop emitting `Cleanup{}`. A build Task emits only `TrackerStatus`. A review Task emits nothing.
- [ ] In `board.go`, the `TaskClosed` case on a started or Queued Task returns `finish(OutcomeClosed)` plus `Cleanup{}`. On a Done Task it returns `Cleanup{}` alone and keeps the Outcome.
- [ ] Add the Signal `TabClosed{}` to `signal.go`. On a Done Task it returns `Cleanup{}`. On every other Stage it returns nothing in S1a.
- [ ] In `scheduler.go` `onRex`, the `EvClosed` case applies `TabClosed{}` when the Record is Done.
- [ ] In `api.go`, `archive` on a Done Task with a `WindowID` applies `TaskClosed{}` first, then sets `Archived`.
- [ ] In `scheduler.go`, the Done filter near `doneShownFor` keeps a Done Task on the Board while its `WindowID` is set.
- [ ] In `internal/board/model.go`, `x` on a Done Task with a live tab asks `close <task>'s tab and worktree? (y/n)` and sends `close`. On a Done Task with no tab it sends `archive`, as today. Update the hints in `view.go` and the help text in `view.go` and `review_view.go`.
- [ ] Write ADR 0017. Only the user closes a Task tab. Task end keeps the tab and worktree. A Done Task's tab that the user closes removes its worktree and an empty Repo session.
- [ ] Update `GLOSSARY.md` (Task, Board, Archived), `docs/spec/mvp.md` lines 15, 461, and 468, and `docs/spec/reviews.md` line 276.

**You see.**

- [ ] After `gh pr merge` on a sandbox PR, the watch prints `adhoc-<n> Done, merged: <title>`, and `rex window list` still lists the Task's tab.
- [ ] `x` then `y` on that Done row removes the tab, and `wt list` in `lineup-sandbox` no longer shows the branch.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `internal/lifecycle/board_test.go` drops `Cleanup{}` from the merged and closed rows near line 58 and gains `TestClosingADoneTaskCleansUpOnly` and `TestTabClosedCleansUpADoneTask`. `internal/lifecycle/review_test.go` line 18 drops `Cleanup{}` from the review end effects.
- [ ] `internal/scheduler/scheduler_test.go` `TestTaskFromStartToMerged` asserts that the tab and worktree stay after the merge, and gains `TestADoneTabTheUserClosesRemovesTheWorktree`, `TestArchiveClosesAnOpenTab`, and `TestADoneTaskWithAnOpenTabStaysOnTheBoard`. `internal/scheduler/review_test.go` `TestReviewFromStartToReviewedAndCleanedUp` becomes `TestAReviewedTaskKeepsItsTab`.
- [ ] `internal/board/board_test.go` gains `TestXClosesTheTabOfADoneTask` next to `TestXArchivesADoneTask`.
- [ ] Run `go test -race -count=1 ./internal/lifecycle/ ./internal/scheduler/ ./internal/board/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (opus)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Merge a sandbox Task's PR with `gh pr merge` at trunk and at head. Save `merged.png`. Pass when trunk's tab is gone and head's tab is still in `rex window list`, with the Task in Done on both.
- [ ] Lane 2. Turn on auto-merge for a sandbox PR and let it merge. Save `auto-merge.png`. Pass when the tab and the worktree stay.
- [ ] Lane 3. `x` then `y` on a Done Task with a tab. Save `close-done.png`. Pass when the tab and the worktree are gone and the row stays in Done.
- [ ] Lane 4. `x` on a Done Task with no tab. Save `archive-done.png`. Pass when the row leaves the Board and `lineup show` still prints its events.
- [ ] Lane 5. Close a Done Task's tab with `rex window close`. Save `rex-close-done.png`. Pass when `wt list` drops the branch within one event and the Repo session is gone when it held no other tab.
- [ ] Lane 6. Close a Done Task's tab while the Repo session holds a second Task's tab. Save `session-kept.png`. Pass when the session and the second tab stay.
- [ ] Lane 7. `lineup archive` on a Done Task with a tab. Save `archive-open.png`. Pass when the tab closes, the worktree goes, and the row leaves the Board.
- [ ] Lane 8. With one Done Task holding a tab and one without, stop the head Scheduler, move both Tasks' `stage_since` 8 days back in the `data` JSON of `state.db` with `sqlite3`, and start it again. Save `age-off.png`. Pass when only the Task without a tab leaves the Board.
- [ ] Lane 9. Submit the review on a sandbox review Task. Save `review-done.png`. Pass when the review Task is Done and its tab, with the hunk pane, stays.
- [ ] Lane 10. `x` on a Working Task, then `y`. Save `close-live.png`. Pass when the Task is Done, marked closed, and its tab and worktree are gone, as on trunk.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Mean `lineup ls --json` time with 50 Tasks, 20 of them Done with an open tab, at trunk and head. Trunk lacks the close of a Done tab, so also time `lineup close` on a Done Task at head, from the command to `window_closed` in `rex events`.
- [ ] Probe. `hyperfine -w 3 -r 20 'lineup ls --json'` at trunk and head, interleaved. Then time 10 runs of `lineup close` on Done Tasks at head against the `window_closed` timestamps.
- [ ] Baseline. Record the trunk `lineup ls --json` mean first.
- [ ] Rule. Head `ls` mean at most 1.1 times trunk. Each Done close reaches `window_closed` within 2 s.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 3 captures into `docs/plans/media/s1a-review-<slug>.png`.
- [ ] Record a 30 to 60 second video of a merge that keeps the tab, then `x` and `y`. Save it as `docs/plans/media/s1a-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1a to the stack and the operator lands it bottom-up.

## Keep one Worker for each Task (S1b)

**Depends on.** S1a.

**Files.**

- [ ] Edit `internal/lifecycle/signal.go`.
- [ ] Edit `internal/lifecycle/board.go`.
- [ ] Edit `internal/lifecycle/board_test.go`.
- [ ] Edit `internal/scheduler/scheduler.go`.
- [ ] Edit `internal/scheduler/record.go`.
- [ ] Edit `internal/scheduler/scheduler_test.go`.
- [ ] Edit `internal/scheduler/review_test.go`.
- [ ] Edit `internal/harness/claude/claude.go`.
- [ ] Edit `internal/harness/claude/claude_test.go`.
- [ ] Edit `internal/config/config.go`.
- [ ] Edit `internal/config/config_test.go`.
- [ ] Edit `cmd/lineup/wire.go`.
- [ ] Edit `GLOSSARY.md` and `docs/spec/mvp.md`.
- [ ] Edit `docs/adr/0004-fresh-worker-per-stage.md`.
- [ ] Create `docs/adr/0018-one-worker-for-each-task.md`.

**Build.**

- [ ] Run the **architect** skill on the delivery rule before code. The data shape is `Record.Pending *lifecycle.Stage`, the Stage whose prompt the Worker has not received yet. It already holds the Stage that waits for setup.
- [ ] Rename the Effect `StartWorker` to `PromptWorker` in `signal.go`. Migrate every caller and delete the old name.
- [ ] In `scheduler.go`, `PromptWorker` sets `Pending` to its Stage, then runs `deliver`. A rerun of the same Stage sets `Pending` again.
- [ ] Write `deliver` in `scheduler.go` as one table from the Worker's state to an action.
  - [ ] No tab and no `Conversation`. Open the Task's first tab, as `startWorker` does now. The prompt is the argv.
  - [ ] A live Worker whose `WorkerState` is `idle` or `done`. Type `claude.Prompt` for the Stage with `Rex.Type`, clear `Pending`, and set `WorkerStage`.
  - [ ] A live Worker in any other state. Do nothing. `onRex` runs `deliver` again on each `EvStatus` of `idle` or `done`.
  - [ ] No tab with a `Conversation`. Open a tab with `claude --resume <Conversation>`, then deliver on its first `idle`. S1c replaces this row with a flag.
- [ ] Remove the `detach` call from `startWorker`. Delete `detach` and fold its body into `cleanup`, its one remaining caller.
- [ ] In `scheduler.go` `checkContract`, return early while `Pending` is set. A Worker that the user talks to between Stages reports `done` before it has the new Stage's prompt.
- [ ] Set `Record.Conversation` to a new UUID when a build Task's first Worker starts. Pass it as `--session-id` in `wire.go`. Update the comment in `record.go` lines 45-47.
- [ ] For a review Task, a rerun of Reviewing keeps the Conversation and types the review prompt. Delete the new UUID at `scheduler.go` lines 405-410.
- [ ] In `config.go`, reject `model` and `effort` under every `[stage.<key>]` other than `default`, `working`, and `reviewing`, with the message `stage.<key>.model: a Task keeps the model of the Stage its Worker starts in`. `skill` stays per Stage, because the typed prompt carries it.
- [ ] Write ADR 0018. One Worker for each Task. It supersedes ADR 0004. Mark ADR 0004 superseded.
- [ ] Update `GLOSSARY.md` (Worker, Stage) and `docs/spec/mvp.md` lines 231 and 304.

**You see.**

- [ ] When CI fails on a sandbox PR, the Task's tab stays the same window ID, and `rex capture` shows the CI debug prompt as a new message under the Working conversation.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `internal/scheduler/scheduler_test.go` gains `TestTheNextStageTypesIntoTheSameWorker`, `TestThePromptWaitsForABusyWorker`, `TestARerunTypesThePromptAgain`, and `TestAWorkerBetweenStagesMeetsNoContract`. The fake Rex records `Type` calls and fails the test on any `CloseWindow` before cleanup.
- [ ] `internal/scheduler/review_test.go` gains `TestARerunReviewKeepsTheConversation`.
- [ ] `internal/harness/claude/claude_test.go` asserts `--session-id` for a build Worker.
- [ ] `internal/config/config_test.go` gains `TestALaterStageModelIsRejected`.
- [ ] Run `go test -race -count=1 ./internal/lifecycle/ ./internal/scheduler/ ./internal/harness/claude/ ./internal/config/ ./cmd/lineup/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (opus)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Push a failing `FAIL` file through a sandbox Task at trunk and at head. Save `ci-debug.png`. Pass when trunk opens a new window ID for CI debug and head keeps the Working window ID with the CI debug prompt in the same conversation.
- [ ] Lane 2. Type a message into the Worker while CI fails. Save `busy.png`. Pass when the CI debug prompt arrives only after the Worker's status is `done`, as `rex events` shows.
- [ ] Lane 3. Leave review comments on the sandbox PR. Save `responding.png`. Pass when the Responding prompt arrives in the same Worker.
- [ ] Lane 4. `lineup rerun <task> ci_debug` while the Worker is idle. Save `rerun.png`. Pass when the prompt is typed a second time into the same Worker.
- [ ] Lane 5. A Stage prompt whose Issue text holds 40 lines. Save `long-prompt.png`. Pass when the Worker receives one message with all 40 quoted lines.
- [ ] Lane 6. A Task with a skill set. Save `skill.png`. Pass when the typed CI debug prompt starts with `/<skill>` and the skill loads.
- [ ] Lane 7. `lineup rerun R-<n> reviewing` on a sandbox review Task. Save `review-rerun.png`. Pass when the review prompt is typed into the same Worker and `Conversation` is unchanged in `lineup show --json`.
- [ ] Lane 8. Kill the Worker's `claude` process with `kill -9`. Save `crash.png`. Pass when the Task gets the `error` flag and opens no tab.
- [ ] Lane 9. Load a config with `[stage.ci_debug] model = "x"`. Save `config.png`. Pass when `lineup serve` exits 1 with the `stage.ci_debug.model` message.
- [ ] Lane 10. Restart the head Scheduler during CI running, per the boot recipe. Save `restart.png`. Pass when the Worker tab is the same window ID after the restart and the next Stage types into it.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Seconds from the poll that moves a Task to CI debug to the Worker's first `working` status, at trunk and head.
- [ ] Probe. Run the lane 1 scenario 5 times at trunk and 5 times at head, interleaved, and read both timestamps from `rex events --json` and the Scheduler log.
- [ ] Baseline. Record the trunk median first.
- [ ] Rule. Head median at most the trunk median. Each head handoff reaches `working` within 3 s of an idle Worker.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 captures into `docs/plans/media/s1b-review-<slug>.png`.
- [ ] Record a 30 to 60 second video of Working, a CI failure, and the CI debug prompt in the same tab. Save it as `docs/plans/media/s1b-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1b to the stack and the operator lands it bottom-up.

## Let the user restore a closed tab (S1c)

**Depends on.** S1b.

**Files.**

- [ ] Edit `internal/lifecycle/stage.go`.
- [ ] Edit `internal/lifecycle/signal.go`.
- [ ] Edit `internal/lifecycle/board.go`.
- [ ] Edit `internal/lifecycle/board_test.go`.
- [ ] Edit `internal/lifecycle/review_test.go`.
- [ ] Edit `internal/lifecycle/property_test.go`.
- [ ] Edit `internal/scheduler/scheduler.go`.
- [ ] Edit `internal/scheduler/api.go`.
- [ ] Edit `internal/scheduler/review.go`.
- [ ] Edit `internal/scheduler/scheduler_test.go`.
- [ ] Edit `internal/scheduler/review_test.go`.
- [ ] Edit `internal/config/config.go`.
- [ ] Edit `internal/config/config_test.go`.
- [ ] Edit `internal/board/model.go`, `internal/board/review.go`, `internal/board/view.go`, and `internal/board/review_view.go`.
- [ ] Edit `internal/board/board_test.go` and `internal/board/review_test.go`.
- [ ] Edit `cmd/lineup/cli.go` and `cmd/lineup/main.go`.
- [ ] Create `cmd/lineup/open_test.go`.
- [ ] Edit `GLOSSARY.md`, `docs/spec/mvp.md`, and `docs/spec/reviews.md`.

**Build.**

- [ ] Add the Reason `ReasonTabClosed`, `tab closed`, to `stage.go`. It is not Transient.
- [ ] Extend `TabClosed{}` in `board.go`. In an agent Stage it flags `tab closed`. In a waiting Stage it sets no flag.
- [ ] In `scheduler.go` `deliver`, replace the row "no tab with a `Conversation`" with a `tab closed` flag whose detail names the waiting Stage, for example `CI debug waits for the Worker`. `Pending` stays set.
- [ ] In `onRex`, the `EvClosed` case applies `TabClosed{}` for every Record whose Job is `JobWorker`. Rex sends `block_closed` with no `child_exited` when the user closes a tab. Appendix A has the probe.
- [ ] In `recover`, a started Task whose tab is gone applies `TabClosed{}`. Delete the `WorkerLost` path at `scheduler.go` line 845.
- [ ] Delete the Signal `WorkerLost`, the Reason `worker lost`, `Budgets.Restarts`, and the config key `restarts`, with their tests and docs. Update the generator in `property_test.go` and the invariant of `TestEveryStartedTaskEndsInDoneOrNeedsYou`.
- [ ] Add the op `open` to `api.go`.
  - [ ] A live tab gets `Rex.Focus`.
  - [ ] A started Task with no tab gets a new tab that runs `claude --resume <Conversation>` in the folder of its first Worker. Clear the `tab closed` flag. Close a tab that Rex kept open after a fast exit first, because the user asked for the restore.
  - [ ] An empty `Conversation`, or a resume that prints `claude.NoConversation`, starts a fresh Worker with the Stage prompt and the replayed steers. Generalize `resumeFallback` for this.
  - [ ] A Done Task with no tab fails with `<id> is Done and its worktree is gone`. A Queued Task fails with `<id> has not started`.
- [ ] Delete the Signal `Reopen` and `Stage.Resumable()`. Migrate `startOpenReview` in `review.go` and `reopens()` in `internal/board/review.go` to the op `open`.
- [ ] Add `open` to `clientCommands` in `cli.go`. Add the usage line `lineup open <task>  focus the Task's tab, or restore its closed Worker` to `main.go`.
- [ ] In `internal/board/model.go`, `enter` sends `open` for every started Task. The hint reads `restore the Worker` when the row has no live tab.
- [ ] Back up `~/.config/lineup/config.toml`, then delete its `restarts = 2` line before the head Scheduler starts. The loader rejects unknown keys at `config.go` line 407.
- [ ] Update `GLOSSARY.md` (Needs you, Loop budget), `docs/spec/mvp.md` lines 172, 184, 231, 265, 283-285, 325, and 610, and `docs/spec/reviews.md` lines 233-241, 262, and 369.

**You see.**

- [ ] `rex window close` on a Working Task's tab gives the row `Needs you · tab closed`, and no new tab opens.
- [ ] `lineup open <task>` opens a tab that shows the earlier conversation, and the flag clears.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `internal/lifecycle/board_test.go` replaces the `WorkerLost` rows near line 192 with `TabClosed` rows and deletes `TestWorkerRestartBudgetIsPerStage`. `internal/lifecycle/review_test.go` deletes `TestReopenFitsOnlyAResumableStage` and `TestReopenClearsTheFlag`.
- [ ] `internal/scheduler/scheduler_test.go` replaces `TestRecoveryRestartsALostWorker` with `TestRecoveryFlagsATaskWhoseTabIsGone`, and gains `TestAClosedTabOpensNoTab`, `TestOpenResumesTheConversation`, `TestOpenStartsAFreshWorkerWithNoConversation`, `TestOpenFocusesALiveTab`, and `TestAPendingStageWaitsForTheRestore`.
- [ ] `internal/scheduler/review_test.go` turns `TestALostTabWhileReviewingStartsAFreshWorker` and `TestRecoveryWithNoTabWhileReviewingStartsAFreshWorker` into flag tests.
- [ ] `cmd/lineup/open_test.go` gains `TestLineupOpenSendsOpen` with `fakeScheduler`.
- [ ] `internal/board/board_test.go` gains `TestEnterRestoresAClosedWorker`.
- [ ] Run `go test -race -count=1 ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (opus)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Close a Working Task's tab with `rex window close` at trunk and at head. Save `closed-working.png`. Pass when trunk opens a new tab and head opens none and flags `tab closed`.
- [ ] Lane 2. `lineup open` on that Task. Save `restore.png`. Pass when the new tab shows the earlier messages and the Worker continues the Stage.
- [ ] Lane 3. `enter` on the flagged row in the Board. Save `enter-restore.png`. Pass when the result matches lane 2.
- [ ] Lane 4. Close the tab during CI running, then fail CI. Save `pending.png`. Pass when no tab opens, the flag reads `CI debug waits for the Worker`, and `lineup open` delivers the CI debug prompt.
- [ ] Lane 5. Close the tab during Ready to land. Save `ready-closed.png`. Pass when the row has no flag and the Task stays in Ready to land.
- [ ] Lane 6. Start a second Rex server with `rex server run` on its own socket, and start the head Scheduler with `REX_SERVER` set to that socket. Start two Tasks, then stop and start that server only. Never stop the operator's Rex server, because that closes every tab the operator has. Save `rex-restart.png`. Pass when both Tasks get `tab closed` and no tab opens until `lineup open`.
- [ ] Lane 7. Delete the Task's conversation file, then `lineup open`. Save `fallback.png`. Pass when a fresh Worker starts with the Stage prompt.
- [ ] Lane 8. `lineup open` on a live Task and on a Done Task with no tab. Save `open-errors.png`. Pass when the first focuses the tab and the second exits 1 with `is Done and its worktree is gone`.
- [ ] Lane 9. Close a Review ready Worker's tab, then `lineup review <url>`. Save `review-reopen.png`. Pass when the review conversation comes back, as on trunk.
- [ ] Lane 10. Start `lineup serve` with `restarts = 2` still in the config. Save `stale-key.png`. Pass when it exits 1 with `unknown keys: budgets.restarts`.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Mean `lineup show` round trip with 50 Tasks at trunk and head. Trunk lacks `open`, so also time `lineup open` at head from the command to the restored Worker's first `idle` status.
- [ ] Probe. `hyperfine -w 3 -r 20 'lineup show adhoc-1'` at trunk and head, interleaved. Then 10 restores at head, timed from `rex events --json`.
- [ ] Baseline. Record the trunk `lineup show` mean first.
- [ ] Rule. Head `show` mean at most 1.1 times trunk. Each restore reaches `idle` within 10 s.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 captures into `docs/plans/media/s1c-review-<slug>.png`.
- [ ] Record a 30 to 60 second video of a tab close, the flag, and the restore. Save it as `docs/plans/media/s1c-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1c to the stack and the operator lands it bottom-up.

## Make the Orchestrator ask with AskUserQuestion (S1d)

**Depends on.** S1c.

**Files.**

- [ ] Edit `cmd/lineup/agent/CLAUDE.md`.
- [ ] Edit `cmd/lineup/hub_test.go`.
- [ ] Edit `docs/spec/mvp.md`.

**Build.**

- [ ] Add a `## Questions` section to `cmd/lineup/agent/CLAUDE.md`. Ask every question to the user with `AskUserQuestion`, never in plain chat. Give 2 to 4 options. The tool adds "Type something" and "Chat about this" by itself. Put up to 4 questions in one call when several alerts arrive together.
- [ ] Start the watch again before you ask, because the question holds the turn.
- [ ] For a Done alert, ask one question per Task. The options are **Close the tab** (`lineup close <task>`), **Open the tab** (`lineup open <task>`), and **Keep it open** (no command).
- [ ] For a `tab closed` alert, the options are **Restore** (`lineup open <task>`) and **Close the Task** (`lineup close <task>`).
- [ ] For a flag that carries a Worker's question, the options are **Open the tab** and **Answer here**. "Answer here" passes the user's words with `lineup steer`.
- [ ] Turn the `wip cap full` and `no Repo mapping` rules into `AskUserQuestion` calls with the same choices.
- [ ] Replace "Do not act on an alert yourself" with "Act on an alert only with the user's answer."
- [ ] Add rows for `lineup open` to the Jobs table, and change the Close row to say that it also closes a Done Task's tab and worktree.

**You see.**

- [ ] After a sandbox merge, the `LINEUP` pane shows an `AskUserQuestion` prompt with the three options plus "Type something" and "Chat about this".

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `cmd/lineup/hub_test.go` gains `TestAgentRoleAsksWithAskUserQuestion`, which asserts the `## Questions` section, both option sets, and no plain-chat question rule. Run `go test -race -count=1 ./cmd/lineup/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (opus)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Merge a sandbox PR with the Hub open at trunk and at head. Save `done-alert.png`. Pass when trunk replies in plain chat and head shows the `AskUserQuestion` prompt with Close the tab, Open the tab, and Keep it open.
- [ ] Lane 2. Pick **Close the tab**. Save `pick-close.png`. Pass when the Orchestrator runs `lineup close` and the tab and worktree are gone.
- [ ] Lane 3. Pick **Open the tab**. Save `pick-open.png`. Pass when Rex focuses the Task's tab.
- [ ] Lane 4. Pick **Keep it open**. Save `pick-keep.png`. Pass when the Orchestrator runs no `lineup` command and the tab stays.
- [ ] Lane 5. Pick "Chat about this". Save `chat.png`. Pass when the Orchestrator asks in chat what to clarify and runs no command.
- [ ] Lane 6. Close a Working Task's tab. Save `tab-closed-alert.png`. Pass when the prompt offers Restore and Close the Task, and Restore runs `lineup open`.
- [ ] Lane 7. Merge 3 sandbox PRs within one poll. Save `batch.png`. Pass when one `AskUserQuestion` call holds 3 questions.
- [ ] Lane 8. A Worker runs `lineup ask`. Save `worker-question.png`. Pass when the prompt quotes the question and "Answer here" passes the typed text through `lineup steer`.
- [ ] Lane 9. Start a sixth Task with a WIP cap of 5. Save `wip-cap.png`. Pass when the prompt offers "Start once over the cap" and "Raise the cap".
- [ ] Lane 10. After lane 4, merge one more PR. Save `watch-rearmed.png`. Pass when its alert arrives, which proves that the watch was armed before the question.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Seconds from the watch line to the Orchestrator's first visible output in the `LINEUP` pane, at trunk and head.
- [ ] Probe. Run the lane 1 scenario 5 times at trunk and 5 times at head, interleaved, and read the times from `rex capture` polled every 500 ms.
- [ ] Baseline. Record the trunk median first.
- [ ] Rule. Head median at most the trunk median plus 5 s.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 6 captures into `docs/plans/media/s1d-review-<slug>.png`.
- [ ] Record a 30 to 60 second video of a merge alert, the question, and the Close pick. Save it as `docs/plans/media/s1d-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1d to the stack and the operator lands it bottom-up.

## Close the program

- [ ] Every box above is checked with its evidence.
- [ ] Remove `keep-tab-at-end`, `one-worker`, and `restore-tab` from the ruleset "CI must pass".
- [ ] Reply to the operator with the report the execution playbook names.

## Appendix A. Prototype evidence

All three probes ran on 2026-11-11 on this machine, in scratch Rex sessions that each probe deleted. The scripts and output are in the session scratchpad, not on a branch.

**A user's tab close sends no child_exited.** A tab that `rex window close` closed sent `window_closed`, then `block_closed`, and no `child_exited`. A command that exited with code 3 sent `child_exited` with `exit_code: 3`, then `block_closed`, then `window_closed`. So `EvClosed` while the Job is `JobWorker` means that a person or Rex closed the tab, not that the Worker exited.

**A typed Stage prompt of many lines arrives as one message.** An interactive `claude` in a Rex tab took a 7-line prompt with blank lines and `>` quotes through `block call write`, then `\r` 150 ms later. That is the same path as `Rex.Type`. Claude answered `STEER-OK LINES=7`. Its `program_status` went from `done` to `working` and back to `done`.

**A resume after a tab close restores the conversation.** After `rex window close`, `claude --resume <uuid>` in a new tab in the same folder showed every earlier message. The restored Worker reported `state: idle`. So `deliver` treats `idle` and `done` as ready.

**The user's config sets no per-Stage model.** `~/.config/lineup/config.toml` sets only `[stage.default]`, so the rejection in S1b breaks no current config.

Unproven. The hunk pane's behavior after a resumed review Worker, which lane S1c-9 covers. Claude's context limit over a Task's whole life, which Claude Code compacts by itself. Appendix C lists it.

## Appendix B. Alternatives rejected

**A fresh Worker for each Stage in the same tab.** ADR 0004 chose it for a model per Stage and a focused context. No config uses a per-Stage model, and the Working Worker holds the context that CI debug needs. The fresh Worker also forces a new tab each Stage, which is the behavior the user wants gone.

**Split the tab and keep the old Worker's pane.** It closes nothing, but panes pile up with each Stage, and two agents share one worktree.

**Ask before each Stage.** It gives the most control, but CI debug and Responding stop being automatic. The user wants control of tabs, not of every Stage.

**Flag `tab closed` in every Stage.** A tab closed in Ready to land is the normal end of the user's work. A flag there nags. The flag fires only when an agent Stage needs the Worker.

**Keep `restarts` as an ignored key.** The loader rejects unknown keys on purpose. One line in one config file is cheaper than a key that does nothing.

**Close the dead setup tab.** The worktrunk setup tab exits by itself, and Rex closes it before the Worker's tab opens. lineup does not close it, and the setup tab is not a Worker's tab, so it stays out of scope.

**The Hub's own tab closes.** `cmd/lineup/hub.go` closes only retired Hub tabs and the first shell of a new Hub. They are not Task tabs.

## Appendix C. Risks

- A Worker's context fills over a long Task. It lands in S1b. The owner watches lane S1b-5 and a Task with 3 CI debug rounds for a compaction, and records the `ctx:` value from the status line.
- The user types into a Worker between Stages, and the Worker reports `done` with unrelated work. It lands in S1b. `checkContract` waits while `Pending` is set, and lane S1b-2 checks the order.
- A Task from before S1b has no `Conversation`. It lands in S1c. A restore starts a fresh Worker, and lane S1c-7 covers it.
- The live Scheduler fails to start after S1c lands if `restarts` stays in the config. It lands in S1c. The owner edits the config before `lineup restart`, and lane S1c-10 proves the message.
- Done Tasks with open tabs stack up in Rex. It lands in S1a. S1d's question puts the close in front of the user at each Task end.
- `AskUserQuestion` holds the Orchestrator's turn, and a later alert could wait. It lands in S1d. Lane S1d-10 proves that the watch stays armed.

## Appendix D. Links and reading list

Read before editing. `GLOSSARY.md`, `docs/spec/mvp.md`, `docs/spec/reviews.md`, `docs/adr/0004-fresh-worker-per-stage.md`, `docs/adr/0012-a-review-is-a-task-kind.md`, `docs/adr/0016-one-front-agent.md`, and `CLAUDE.md`.

S1b and S1c get `pstack/skills/architect/SKILL.md` for `deliver` and the op `open`. S1b gets `pstack/skills/interrogate/SKILL.md`, because it reverses ADR 0004.

The trail follows `pstack/skills/show-me-your-work/SKILL.md`. Keep it local unless a PR's verdict is contested.
