# One front agent plan

Lineup gets one agent in front of its three boards, one screen that cycles them, and a Worker that can state its question to the Scheduler. The user is Greg, who starts every Task and merges every PR. The rule the program enforces is that every alert reaches one chat and every answer leaves from it. The PR ids in order are S1a, S1b, and S1c in Stack 1, then S2a, S2b, and S2c in Stack 2, then F standalone. H was withdrawn, see Appendix B. Branch names are the PR ids' long forms below.

The program completed on 2026-11-10. Appendix E maps each PR id to its merged PR.

## How to read this

One box is one unit of work. Every box names the evidence that checks it. A nested box is a sub-step of the box above it. Check a box only when its evidence exists, a file, a log line, a screenshot, a test run, or a SHA. The body is a how-to. The appendices explain and record.

The program runs `pstack/skills/poteto-mode/playbooks/autopilot-stack.md`. The operator lands each stack with `gh stack merge <top PR> --yes`. F and H auto-merge when `test` passes. Every PR stops at merge-ready.

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

Live lanes run on this machine, not on cloud VMs. Lineup is a solo TUI on Rex, and its surface exists only here. Screenshots are `rex capture` text dumps saved as `.txt` where the lane names `.png`, because the surface is a terminal.

## Program checklist

### Arm the program

- [ ] State the protocol and this plan to the operator, then stop. Start execution only on the operator's explicit go.
- [ ] Read these from trunk at program start. Re-read them at every tick.
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/autopilot-stack.md`
  - [ ] `git show origin/main:pstack/skills/swarm/SKILL.md`
  - [ ] `git show origin/main:cursor-team-kit/skills/control-cli/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/opening-a-pr.md`
  - [ ] `git show origin/main:pstack/skills/gh-stack/SKILL.md`
- [ ] On the operator's go, arm the audit tick as `/loop 1h` with the tick prompt below. Never leave the cadence to memory.
- [ ] Use this tick prompt, verbatim. "Re-read the execution playbook from trunk. Audit the operation against it and fix drift in this tick. Probe every active lane and judge progress by side effects only. Stand down a stuck lane and dispatch its replacement now. Then post a short status message to the operator in chat only when the audit found a tracked change that no earlier status message reported, such as a PR opened, a code-ready head, a round launched or closed, a verdict, a merge, a stuck agent and the action taken, a blocker added or cleared, or a decision only the operator can make. Name every such change and nothing else. Do not repeat a table, the merged list, or an unchanged blocker. If the audit found none, end the turn with no reply text. Either way, log this tick's row in your decision trail. The row names the items reported, or none."
- [ ] On the operator's hold or stand-down, send every owner a zero-writes order at once.

### Spawn owners

- [ ] Spawn one owner per PR with the full lifecycle the execution playbook names.
- [ ] Follow this dependency graph. Start dependent work only after its parent merges, or base it on the parent branch when the execution playbook stacks.
  - [ ] S1a, S2a, and F are independent and first. All branch from `main`.
  - [ ] S1b after S1a. S1c after S1b. Stack 1 is `watch-all-kinds`, `one-front-agent`, `one-screen`.
  - [ ] S2b after S2a. S2c after S2b. Stack 2 is `worker-ask`, `steer-replay`, `resume-fallback`.
  - [ ] F is `drop-harness-knob`.
- [ ] Hold the file boundaries. Stack 1 touches only `cmd/lineup/watch.go`, `cmd/lineup/watch_test.go`, `cmd/lineup/hub.go`, `cmd/lineup/hub_test.go`, `cmd/lineup/*/CLAUDE.md`, `internal/board/model.go`, `internal/board/*_test.go`, `docs/adr/*`, `GLOSSARY.md`. Stack 2 touches only `cmd/lineup/cli.go`, `internal/scheduler/api.go`, `internal/scheduler/alerts.go`, `internal/scheduler/scheduler.go`, `internal/scheduler/*_test.go`, `internal/harness/claude/*`, `internal/lifecycle/*`. F touches only `internal/config/*`, `cmd/lineup/wire.go`.
- [ ] Hold the review gate. S1b, S1c, and S2a change an interaction. They wait for the operator's review in chat with screenshots and a video before merge.

### PR mechanics, for every PR

- [ ] Resolve the forge once. Default to `gh`; if `command -v origin` succeeds and Origin can resolve the repository, use `origin pr` for every PR operation. Record any fallback to `gh`. Never require `gt`.
- [ ] Open the PR ready, never draft, per **Opening a PR**. Use `gh stack submit --auto --open` for a stack and `gh pr create --base main` for F and H. A stack child targets its parent branch.
- [ ] Run the repo's lint and typecheck once before the PR-facing push. Push with hooks on. The repo's CI trio is `test -z "$(gofmt -l .)"`, `go vet ./...`, `go test -race -count=1 ./...`.
- [ ] Run `/deslop` before each commit and `/no-comments` before review.
- [ ] Triage every Bugbot and security-reviewer comment per `../references/bugbot-triage.md`.
- [ ] Rebase onto current trunk before the code-ready report and babysit. Keep that merge base in fix rounds. Rebase again only at merge prep, on a `git merge-tree` conflict with trunk, on a CI failure that comes from a change on trunk, or when a known concurrent change to the same files lands.

### Verdict and merge, for every PR

- [ ] A full swarm runs once, at the code-ready head. Another full swarm runs only for new behavior, a redesign, or a large new diff. A rebase, a merge-conflict resolution, or a minor fix gets a single verifier at the new head, and a wording-only change gets none. Run each full swarm per `pstack/skills/swarm/SKILL.md`. One gates lane. The ten live lanes from the PR's **Verify, live** block. The perf lane from its **Verify, perf** block. Two or more audit lanes, each with its own focus, that read the diff and the receipts and distrust the PR body. The root audits the receipts in the merge-ready report before the verdict.
- [ ] Judge every finding by the execution playbook's blocking rule. The verdict is clean when none blocks. A failure on trunk behavior in a lane marked `Regression lane against trunk.` is recorded against trunk and does not block the PR. A new head gets a fresh verdict, except for results that stay valid under the patch-id rule in `playbooks/shipping.md`.
- [ ] The root appends each verified PR to its stack. The operator lands a stack bottom-up with `gh stack merge <top PR> --yes` once every PR in it has a clean verdict at its head SHA, per the patch-id rule in `playbooks/shipping.md`.

### Boot recipe, for every live lane

Each live lane runs on this machine at the PR head, in a worktree that `wt` creates. Drive through `control-cli` from `cursor-team-kit`.

- [ ] `git fetch origin <head-branch> && git checkout <head SHA>`.
- [ ] `go build -o /tmp/lineup-<pr-id> ./cmd/lineup`. Stop the live Scheduler with `lineup stop`. Start the head Scheduler with `LINEUP_STATE=/tmp/lineup-<pr-id>-state /tmp/lineup-<pr-id> serve &`. Wait for `lineup.sock` to appear.
- [ ] Deliver input only through `rex type` and `rex capture` on the lane's own Rex session. Read-only diagnostics are `lineup ls --json`, `lineup show <id>`, and `sqlite3 state.db 'select * from events'`.
- [ ] Save every capture to `/tmp/swarm-<pr-id>/worker-<n>/<slug>.txt` and return the paths with the report.

## Send every Kind's alerts to the watch (S1a)

**Depends on.** None.

**Files.**

- [ ] Edit `cmd/lineup/watch.go`.
- [ ] Edit `cmd/lineup/watch_test.go`.
- [ ] Edit `cmd/lineup/orchestrator/CLAUDE.md`.

**Build.**

- [ ] Delete `v = v.OfKind(lifecycle.KindBuild)` at `cmd/lineup/watch.go` line 64. Drop the `lifecycle` import if it becomes unused.
- [ ] Rewrite the `cmdWatch` comment at lines 21-27 so it says the watch prints one line each time any Task starts to need the user or ends.
- [ ] Seed `seen` on first run with every Done Task of either Kind, which the loop at lines 66-72 already does once the filter is gone.
- [ ] Change `cmd/lineup/orchestrator/CLAUDE.md` line 45 from "alerts of build Tasks" to "alerts of every Task".

**You see.**

- [ ] `lineup watch --once` prints `R-1 Review ready: data#7` when a review Task reaches Review ready.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `cmd/lineup/watch_test.go` replaces `TestWatchSkipsReviewTasks` with `TestWatchPrintsReviewTasks`, which feeds a Reviewing then a Review ready view and asserts one `R-1 Review ready` line. Run `go test -race -count=1 ./cmd/lineup/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Start a build Task that stops with a question at trunk and at head. Save `build-alert.png`. Pass when both print `<id> Working, question: <title>` once.
- [ ] Lane 2. `lineup review <url>` on a small PR until Review ready. Save `review-alert.png`. Pass when the head prints `<id> Review ready: <title>` and trunk prints nothing.
- [ ] Lane 3. Close that review Task. Save `review-done.png`. Pass when the head prints `<id> Done: reviewed`.
- [ ] Lane 4. Run `lineup watch --once` twice across one Review ready. Save `once-exits.png`. Pass when the first run exits after the line and the second blocks.
- [ ] Lane 5. Delete `alerted.json`, then run the watch with one open Review ready and two Done review Tasks. Save `seed.png`. Pass when the Review ready Task prints once and the Done Tasks print nothing.
- [ ] Lane 6. Start a second `lineup watch` while one runs. Save `lock.png`. Pass when the second exits with the `lineup watch` lock message.
- [ ] Lane 7. `lineup restart` while the watch runs. Save `reconnect.png`. Pass when the watch stays up and prints the next alert.
- [ ] Lane 8. Open the Hub. Trigger a Review ready. Save `hub-chat.png`. Pass when the Orchestrator pane reports the review Task in one or two sentences.
- [ ] Lane 9. Same Review ready with the review board open. Save `toast.png`. Pass when the review board toasts once and the chat reports once, and nothing prints twice.
- [ ] Lane 10. A build Task reaches Ready to land. Save `land-alert.png`. Pass when the watch prints `<id> Ready to land: <title>` exactly as trunk does.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Wall time from a `BoardView` publish to the alert line on stdout, for one alert among 50 open Tasks, at trunk and at head.
- [ ] Probe. `go test -run TestWatchPrintsEachAlertOnceAcrossRestarts -bench . -count=5 ./cmd/lineup/` with a 50-Task feed, interleaved trunk then head five times.
- [ ] Baseline. Record the trunk ns/op first.
- [ ] Rule. Head ns/op at most 1.2 times trunk.

**Review gate.** None. S1a is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1a to Stack 1 and the operator lands it bottom-up.

## Put one front agent over the boards (S1b)

**Depends on.** S1a.

**Files.**

- [ ] Edit `cmd/lineup/hub.go`.
- [ ] Edit `cmd/lineup/hub_test.go`.
- [ ] Create `cmd/lineup/agent/CLAUDE.md`.
- [ ] Delete `cmd/lineup/orchestrator/CLAUDE.md`, `cmd/lineup/planner/CLAUDE.md`, `cmd/lineup/reviewer/CLAUDE.md`. Move `cmd/lineup/planner/{github,linear,local}.md` under `cmd/lineup/agent/`.
- [ ] Create `docs/adr/0016-one-front-agent.md`.
- [ ] Edit `docs/adr/0004-fresh-worker-per-stage.md`, `docs/adr/0009-orchestrator-alerts-come-from-a-monitor.md`, `docs/adr/0012-a-review-is-a-task-kind.md`, `GLOSSARY.md`.

**Build.**

- [ ] Write one role prompt in `cmd/lineup/agent/CLAUDE.md`. Keep the Orchestrator job table and add the review rows from `reviewer/CLAUDE.md` and the plan rows from `planner/CLAUDE.md`. Keep "You never do the work yourself" and "Do not act on an alert yourself." Answer "what needs me?" from `lineup ls --json` with no Kind filter. Keep `composePlanner` so the tracker files append to the one prompt.
- [ ] Write one `agentSettings` in `hub.go`. Allow is the union of `orchestratorSettings`, `plannerSettings`, and `reviewerSettings` allow lists at lines 57, 68, and 119. Deny keeps `gh pr review/comment/merge/close/edit`, `gh api`, `linear issue delete`, `gh issue delete`. Drop the denies on `lineup add/start/review/reviews/watch`.
- [ ] Name the agent `LINEUP` so `stateDir()/lineup` is a fresh role dir and `--continue` finds no old conversation (`hub.go` lines 212 and 471).
- [ ] In `agentTabs` at lines 164-167, give the `work` tab the `LINEUP` agent and make `plan` and `review` board-only, as `review` was at commit cd58b74. Keep `assist` as is.
- [ ] Write ADR 0016. Decision, one front agent and one board per Kind. Reason, every alert reaches one chat and every answer leaves from it. Rejected, four role agents (the Nov 5-6 commits 018be9a, 8f25938, bd42fdb had no ADR) and a dsh-style router with no chat.
- [ ] Fix stale text. ADR 0009 says the watch covers every Kind. ADR 0012 drops "This is what the Orchestrator does today". ADR 0004 drops Planning from its Stage list. GLOSSARY entries for Hub, Board, and Orchestrator name the one agent.

**You see.**

- [ ] In the `work` tab's agent pane, "review https://github.com/x/y/pull/1" runs `lineup review` and "start ENG-34" runs `lineup add` then `lineup start`, both from the same conversation.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `cmd/lineup/hub_test.go` gains `TestAgentSettingsUnionAllowsEveryLineupCommand` and `TestAgentSettingsDenyKeepsGitHubWrites`, and `TestAgentTabsHaveOneAgent` asserts that only `work` and `assist` carry an agent. Run `go test -race -count=1 ./cmd/lineup/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. "start ENG-x" in the work agent pane at trunk and at head. Save `start.png`. Pass when both reply with the Task ID in one sentence.
- [ ] Lane 2. "review <url>" in the work agent pane. Save `review.png`. Pass when the head runs `lineup review` and trunk redirects to the review tab.
- [ ] Lane 3. "what needs me?" with one build and one review Task in Needs you. Save `needs-me.png`. Pass when the head names both and trunk names one.
- [ ] Lane 4. "move ENG-x to In Progress" (a Planner job). Save `plan.png`. Pass when the head edits Linear from the work pane.
- [ ] Lane 5. "approve that PR" after a review. Save `gh-deny.png`. Pass when `gh pr review` is refused by settings and the agent says so.
- [ ] Lane 6. Open the Hub twice. Save `continue.png`. Pass when the second open continues the `LINEUP` conversation and no "build Tasks only" text appears.
- [ ] Lane 7. Plan and review tabs. Save `board-only.png`. Pass when each shows a board at full height and no agent pane.
- [ ] Lane 8. A Review ready alert. Save `alert.png`. Pass when the work pane reports it and offers `lineup steer` or enter.
- [ ] Lane 9. `/pr-review <url>` typed to the agent. Save `slash.png`. Pass when it becomes `lineup review`, not a followed skill.
- [ ] Lane 10. "merge ENG-x" on a Ready to land Task. Save `merge.png`. Pass when `lineup merge` runs only on that named Task.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Tokens in the agent's first turn after Hub open, from the Claude transcript's first usage record, at trunk (Orchestrator) and at head (LINEUP).
- [ ] Probe. Open the Hub, send "what needs me?", read `input_tokens` from the newest `~/.claude/projects/<slug>/*.jsonl`, interleaved trunk then head three times.
- [ ] Baseline. Record the trunk Orchestrator's first-turn `input_tokens` first.
- [ ] Rule. Head at most 1.5 times trunk. The merged prompt is longer by design, and the Planner's per-prompt `lineup trackers` hook must not run on non-plan turns.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 3 and lane 7 captures into `docs/plans/media/s1b-review-needs-me.png` and `docs/plans/media/s1b-review-board-only.png`.
- [ ] Record a 30 to 60 second video of start, review, and what-needs-me from one pane. Save it as `docs/plans/media/s1b-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1b to Stack 1 and the operator lands it bottom-up.

## Cycle the three boards in one tab (S1c)

**Depends on.** S1b.

**Files.**

- [ ] Edit `internal/board/model.go`.
- [ ] Edit `internal/board/board_test.go`.
- [ ] Edit `cmd/lineup/hub.go`.
- [ ] Edit `cmd/lineup/hub_test.go`.
- [ ] Edit `docs/adr/0016-one-front-agent.md`.

**Build.**

- [ ] In `model.go`, handle `1`, `2`, `3` in `modeNormal` only. `1` sets `m.view = viewTracker`. `2` sets `m.view, m.kind = viewBoard, lifecycle.KindBuild`. `3` sets `m.view, m.kind = viewReview, lifecycle.KindReview`. The guard at line 255 keeps digits as text inside a filter.
- [ ] Draw the three names in the header with the active one bright, `1 plan  2 work  3 review`.
- [ ] In `hub.go` `agentTabs`, replace `plan`, `work`, `review` with one `hub` tab that runs `board` over the `LINEUP` agent. Keep `assist`. Add `hub` to `formerLabels` so a running `work` tab is renamed in place.
- [ ] Add the one-tab layout to ADR 0016.

**You see.**

- [ ] In the `hub` tab, pressing `1`, `2`, `3` switches the board above the agent pane with no tab change.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `board_test.go` gains `TestDigitKeysCycleViews`, which sends `1`, `2`, `3` and asserts `m.view` and `m.kind`, and `TestDigitInFilterStaysText`. Run `go test -race -count=1 ./internal/board/ ./cmd/lineup/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Press `enter` on a Working Task at trunk (work tab) and at head (hub tab, view 2). Save `focus.png`. Pass when both jump to the Worker's tab.
- [ ] Lane 2. Press `1`. Save `plan-view.png`. Pass when the tracker browser draws and `s` still starts an Issue.
- [ ] Lane 3. Press `3`. Save `review-view.png`. Pass when the Reviews tab draws and `r` on a PR row starts a review Task.
- [ ] Lane 4. Press `/` in view 1, type `12`, press `esc`. Save `filter-digits.png`. Pass when the filter reads `12` and the view did not change.
- [ ] Lane 5. Press `z` in each view. Save `zoom.png`. Pass when the agent pane zooms from all three.
- [ ] Lane 6. Open the Hub with an old `work` tab running. Save `rename.png`. Pass when the tab is renamed `hub` and the agent keeps its conversation.
- [ ] Lane 7. Resize the Rex pane to 80 columns. Save `narrow.png`. Pass when the header `1 plan  2 work  3 review` still fits on one line.
- [ ] Lane 8. A toast while on view 1. Save `toast-any-view.png`. Pass when the toast fires for a build Task that needs you.
- [ ] Lane 9. Press `?` in each view. Save `help.png`. Pass when the help lists `1`, `2`, `3`.
- [ ] Lane 10. `lineup` with the Scheduler stopped. Save `offline.png`. Pass when every view shows the "scheduler offline, showing cache" header.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Frame time of one `View()` call for each of the three views with 50 Tasks, at trunk (three processes) and at head (one process).
- [ ] Probe. `go test -bench BenchmarkView -count=5 ./internal/board/` with a 50-Task model per view, interleaved trunk then head.
- [ ] Baseline. Record the trunk ns/op per view first.
- [ ] Rule. Head ns/op at most 1.1 times trunk for each view.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 2, 3, and 7 captures into `docs/plans/media/s1c-review-<slug>.png`.
- [ ] Record a 30 to 60 second video of `1`, `2`, `3` over a live agent pane. Save it as `docs/plans/media/s1c-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S1c to Stack 1 and the operator lands it bottom-up.

## Let a Worker state its question (S2a)

**Depends on.** None.

**Files.**

- [ ] Edit `cmd/lineup/cli.go`.
- [ ] Edit `internal/scheduler/api.go`.
- [ ] Edit `internal/scheduler/api_test.go`.
- [ ] Edit `internal/scheduler/alerts.go`.
- [ ] Edit `internal/harness/claude/claude.go`.
- [ ] Edit `internal/harness/claude/claude_test.go`.

**Build.**

- [ ] Add `ask` to `clientCommands` in `cli.go` lines 22-35. Usage `lineup ask <task> "<question>"`.
- [ ] Add op `ask` to `api.go`. It sets `r.FlagDetail` to the first 300 characters of the question and sets the flag `question` if no flag is set. It refuses with `contract output exists` when the Task's Stage contract is already met, and refuses when `r.Job != JobWorker`.
- [ ] In `alerts.go` `AlertText()`, print `FlagDetail` after the flag when it is set, `ENG-34 Working, question: migrate with a dual write first?`.
- [ ] In `claude.go`, append one sentence to every agent Stage Contract. "If you must stop for the user, run `lineup ask <task> "<one question>"` first." Add `Bash(lineup ask:*)` to the allow rules that `Trust()` writes at line 61, because Workers launch with no permission flags and the user's global allow list is empty.
- [ ] Clear `FlagDetail` where the flag clears, when `program_status` returns to working.

**You see.**

- [ ] The watch prints `ENG-34 Working, question: migrate with a dual write first?` and the Hub chat repeats the question.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `api_test.go` gains `TestAskSetsFlagDetail`, `TestAskRefusedWhenContractMet`, `TestAskRefusedWithoutWorker`. `alerts_test.go` gains `TestAlertTextCarriesQuestion`. `claude_test.go` asserts the contract sentence and the `Trust()` allow rule. Run `go test -race -count=1 ./internal/scheduler/ ./internal/harness/claude/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. A Worker stops with a plan at trunk and at head. Save `stopped.png`. Pass when trunk prints `stopped` and head prints the question text.
- [ ] Lane 2. From a Worker tab run `lineup ask ENG-x "ok?"`. Save `ask-ok.png`. Pass when the board row shows the note and no permission prompt appears.
- [ ] Lane 3. Run `lineup ask` on a Task whose PR is open. Save `ask-refused.png`. Pass when it exits 1 with `contract output exists`.
- [ ] Lane 4. Run `lineup ask` on a Queued Task. Save `ask-no-worker.png`. Pass when it exits 1 with `has no live Worker`.
- [ ] Lane 5. Ask, then `lineup steer ENG-x "yes"`. Save `answer.png`. Pass when the Worker resumes and the flag clears within one poll.
- [ ] Lane 6. Ask twice. Save `ask-twice.png`. Pass when the row and the alert show the second question only.
- [ ] Lane 7. A 1000-character question. Save `ask-long.png`. Pass when the row shows the first line and `lineup show` shows 300 characters.
- [ ] Lane 8. Ask from a review Worker. Save `ask-review.png`. Pass when the review board row carries the question.
- [ ] Lane 9. `lineup restart` after an ask. Save `ask-survives.png`. Pass when the question is still on the row.
- [ ] Lane 10. Hub chat after an ask. Save `chat-question.png`. Pass when the agent's one or two sentences include the question verbatim.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Round trip of one socket op, `lineup show`, with 50 Tasks, at trunk and at head. Trunk lacks `ask`, so also time `lineup ask` at head alone with a budget of 50 ms.
- [ ] Probe. `hyperfine -w 3 -r 20 'lineup show ENG-1'` at trunk and head, interleaved, then `hyperfine -r 20 'lineup ask ENG-1 q'` at head.
- [ ] Baseline. Record the trunk `lineup show` mean first.
- [ ] Rule. Head `show` mean at most 1.1 times trunk. `ask` mean at most 50 ms.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 10 captures into `docs/plans/media/s2a-review-<slug>.png`.
- [ ] Record a 30 to 60 second video of ask, alert, steer, resume. Save it as `docs/plans/media/s2a-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S2a to Stack 2 and the operator lands it bottom-up.

## Replay steers and name the restart (S2b)

**Depends on.** S2a.

**Files.**

- [ ] Edit `internal/scheduler/api.go`.
- [ ] Edit `internal/scheduler/scheduler.go`.
- [ ] Edit `internal/scheduler/scheduler_test.go`.
- [ ] Edit `internal/harness/claude/claude.go`.
- [ ] Edit `internal/harness/claude/claude_test.go`.

**Build.**

- [ ] In `api.go` `toWorker` case `steer` at line 230, append an `events` row with signal `steer` and the text as detail before `Rex.Type`.
- [ ] In `scheduler.go` `recover()` at line 720, collect the steer events since the Stage started and pass them with a `Restart` bit in `TaskInfo`.
- [ ] In `claude.go` `Prompt()` at line 301, when `Restart` is set, append "This is a restart. Inspect the worktree, `git log`, and the PR before you repeat any outside action." and then "You were told, in order:" with each steer on its own line.

**You see.**

- [ ] After a Rex restart, the restarted Worker's first prompt ends with the restart sentence and the steers you sent.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `scheduler_test.go` gains `TestRecoverReplaysSteers`. `claude_test.go` gains `TestPromptNamesRestart`. Run `go test -race -count=1 ./internal/scheduler/ ./internal/harness/claude/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Steer a Worker, kill its tab, at trunk and at head. Save `restart.png`. Pass when both restart the Worker and head's prompt carries the steer.
- [ ] Lane 2. Two steers then a kill. Save `two-steers.png`. Pass when both appear in order.
- [ ] Lane 3. Steer in the Working Stage, let it reach CI debug, kill. Save `stage-scope.png`. Pass when the CI debug prompt carries no Working steer.
- [ ] Lane 4. Kill with no steers. Save `no-steers.png`. Pass when the restart sentence appears and no "You were told" line.
- [ ] Lane 5. `sqlite3 state.db "select signal, detail from events where signal='steer'"`. Save `events.png`. Pass when each steer is one row.
- [ ] Lane 6. A steer of 2000 characters. Save `long-steer.png`. Pass when the row holds it whole and the prompt holds it whole.
- [ ] Lane 7. Restart budget spent. Save `budget.png`. Pass when the flag is `budget spent` and no prompt is built.
- [ ] Lane 8. `lineup show` after steers. Save `show-steers.png`. Pass when the events list shows them.
- [ ] Lane 9. Steer a review Worker, kill. Save `review-restart.png`. Pass when `--resume` carries the conversation and the prompt names the restart.
- [ ] Lane 10. Scheduler restart mid-turn. Save `serve-restart.png`. Pass when the Worker restarts once, not twice.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. `lineup steer` round trip with 50 Tasks, at trunk and at head.
- [ ] Probe. `hyperfine -w 3 -r 20 'lineup steer ENG-1 hi'` interleaved trunk then head.
- [ ] Baseline. Record the trunk mean first.
- [ ] Rule. Head mean at most 1.2 times trunk.

**Review gate.** None. S2b is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S2b to Stack 2 and the operator lands it bottom-up.

## Fall back when a conversation is gone (S2c)

**Depends on.** S2b.

**Files.**

- [ ] Edit `internal/harness/claude/claude.go`.
- [ ] Edit `internal/harness/claude/claude_test.go`.
- [ ] Edit `internal/scheduler/scheduler.go`.
- [ ] Edit `internal/scheduler/scheduler_test.go`.

**Build.**

- [ ] In `scheduler.go`, when a Worker whose argv held `--resume` exits non-zero within 10 seconds and its captured output contains `No conversation found`, start it again with `--session-id` and the same id. One retry per start.
- [ ] In `claude.go`, export the two strings `No conversation found` and `already in use` so the test and the Scheduler share them.

**You see.**

- [ ] A review Task whose conversation file is deleted still starts its Worker.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `scheduler_test.go` gains `TestResumeFallsBackToSessionID` with a fake harness that fails once with the string. Run `go test -race -count=1 ./internal/scheduler/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Delete a review conversation file, rerun, at trunk and at head. Save `fallback.png`. Pass when trunk's Worker exits with the error and head's Worker starts.
- [ ] Lane 2. Rerun with the file present. Save `resume-ok.png`. Pass when `--resume` is used and no retry runs.
- [ ] Lane 3. A Worker that fails for another reason. Save `other-error.png`. Pass when no retry runs and the flag is `error`.
- [ ] Lane 4. A Worker that exits non-zero after 30 seconds. Save `late-exit.png`. Pass when no retry runs.
- [ ] Lane 5. `lineup show` after a fallback. Save `events-fallback.png`. Pass when the events list has one `resume fallback` row.
- [ ] Lane 6. Fallback then kill. Save `restart-after-fallback.png`. Pass when the restart uses `--resume` with the same id.
- [ ] Lane 7. Two review Tasks fall back at once. Save `two-fallbacks.png`. Pass when both start.
- [ ] Lane 8. Fallback on a build Task restart. Save `build-fallback.png`. Pass when build restarts, which never resume, are untouched.
- [ ] Lane 9. Fallback with the Scheduler offline cache. Save `offline-fallback.png`. Pass when the board shows the Task Working after reconnect.
- [ ] Lane 10. `claude` missing from PATH. Save `no-claude.png`. Pass when the error is the existing one and no retry runs.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Time from `lineup rerun` to the Worker's first Rex `program_status`, with the file present, at trunk and at head.
- [ ] Probe. Ten reruns each, interleaved trunk then head, timestamps from `events`.
- [ ] Baseline. Record the trunk median first.
- [ ] Rule. Head median at most 1.1 times trunk.

**Review gate.** None. S2c is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends S2c to Stack 2 and the operator lands it bottom-up.

## Drop the harness setting (F)

**Depends on.** None.

**Files.**

- [ ] Edit `internal/config/config.go`.
- [ ] Edit `internal/config/config_test.go`.
- [ ] Edit `cmd/lineup/wire.go`.

**Build.**

- [ ] Remove `Harness` from `StageConfig` at `config.go` line 53, the `harness` key at line 104, `harnessFile` and the `Harness` map at lines 127-142, and the path validation at lines 303-308.
- [ ] Make the load fail with `lineup.toml: harness is no longer a setting, lineup runs claude` when either key is present.
- [ ] Remove the harness name switch in `wire.go` `harnessPort.Command` and call the claude harness directly.

**You see.**

- [ ] `lineup` with a `[harness]` table in `lineup.toml` exits with the message above.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `config_test.go` gains `TestHarnessKeyIsRejected` and drops the harness path cases. Run `go test -race -count=1 ./internal/config/ ./cmd/lineup/`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `session model (fable)` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Start a build Task at trunk and at head with no harness keys. Save `start.png`. Pass when both start a `claude` Worker.
- [ ] Lane 2. `[harness.claude] path = "/x"` in the file. Save `reject-table.png`. Pass when head exits with the message and trunk starts.
- [ ] Lane 3. `[stage.working] harness = "claude"`. Save `reject-stage.png`. Pass when head exits with the message.
- [ ] Lane 4. A repo-level `lineup.toml` with the key. Save `reject-repo.png`. Pass when the message names the repo file.
- [ ] Lane 5. `lineup config` or the equivalent dump. Save `dump.png`. Pass when no harness field prints.
- [ ] Lane 6. `[stage.working] model = "opus"`. Save `model-kept.png`. Pass when the Worker argv has `--model opus`.
- [ ] Lane 7. `lineup restart` with the old file. Save `service-reject.png`. Pass when `lineup logs` shows the message once.
- [ ] Lane 8. Remove the key, `lineup restart`. Save `recover.png`. Pass when the Scheduler starts.
- [ ] Lane 9. `claude` missing from PATH. Save `no-claude.png`. Pass when the error names `claude`.
- [ ] Lane 10. A review Task. Save `review-start.png`. Pass when its Worker starts as before.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Config load time, `go test -bench BenchmarkLoad ./internal/config/`, at trunk and at head.
- [ ] Probe. `-count=5` interleaved trunk then head.
- [ ] Baseline. Record the trunk ns/op first.
- [ ] Rule. Head at most 1.0 times trunk plus noise, since code was removed.

**Review gate.** None. F is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The owner squash-merges F through the auto-merge workflow.

## Close the program

- [ ] Every box above is checked with its evidence.
- [ ] Reply to the operator with the report the execution playbook names.

## Appendix A. Prototype evidence

Four probes ran before this plan, on 2026-11-10, in the main checkout at commit c53ed66.

- Worker permission. `rg -n 'permission|allowedTools' internal/harness/claude/claude.go` finds no permission flag. `~/.claude/settings.json` has `defaultMode: auto` and an empty allow list. No Stage prompt names `lineup`. Result, S2a adds its own allow rule through `Trust()`.
- Free key. `rg -n 'case "' internal/board/{model,tracker,review}.go` lists every bound key. `1`, `2`, `3` are unbound in all three. Result, S1c uses digits.
- Resume failure. `claude -p --resume 20a586d4-21ec-4454-ae17-239bbd2abfab --output-format json hi` in an empty dir printed `No conversation found with session ID: ...` and exited 1. Result, S2c is real and the string is exact.
- Conversation leak. `hub.go` line 212 builds the role dir from the agent name, and line 471 globs that dir for `--continue`. Result, a new agent name gets a fresh dir, no prototype needed.

Unproven. Whether `auto` permission mode would have approved `lineup ask` without an allow rule. S2a does not depend on it.

## Appendix B. Alternatives rejected

- H, hide empty groups. Withdrawn on 2026-11-10. `rows()` in `internal/board/view.go` lines 242-246 writes a header only when a Task's group differs from the previous Task's, so an empty group never had a header. The live capture of the Hub with `0 working` showed `NEEDS YOU`, `WAITING`, `DONE` only. The plan row came from reading line 246 without the loop above it.

- One linear stack of eight. Rejected because Stack 1 and Stack 2 share no file and the project rule stacks only dependent changes. A rejected S1c would have blocked five unrelated PRs.
- Keep four agents and add cross-Kind alerts only (option A). Rejected because an Orchestrator that sees review alerts and holds `lineup review` in its deny list must redirect the user to another tab.
- A dsh-style router with no chat (option C). Rejected because mvp.md line 562 makes "what needs me?" an Orchestrator job and the mockup's chat answers it.
- One agent pane shown in three tabs (B1). Rejected because the three boards are one program (`model.go` lines 51-53), so one tab with a view key is cheaper and matches the mockup.
- Pipe full plans to the front agent. Rejected. ADR 0010 stands. The one-line question travels through `lineup ask`, the plan body stays in the Worker's tab.

## Appendix C. Risks

- S1a. The first watch run after the change alerts every open review Task once, because `alerted.json` seeds only Done Tasks. The owner watches lane 5.
- S1b. The merged prompt is longer and the Planner's per-prompt hook runs `lineup trackers` on every turn. The owner watches the perf rule and moves tracker context into the composed prompt if it fails.
- S1b. A prompt line does not stop a `gh pr` write. The denies stay in settings. The owner watches lane 5.
- S1c. `formerLabels` renames a running tab. If the rename misses, the user gets two agent panes. The owner watches lane 6.
- S2a. A Worker under a permission mode that prompts would hang in a background tab. `Trust()` writes the allow rule. The owner watches lane 2.
- S2b. Steer events grow the `events` table. One row per steer is small. The owner watches lane 5.
- S2c. A retry loop if both strings appear in turn. One retry per start. The owner watches lane 3.
- F. A user with a `[harness]` table loses the Scheduler until they edit the file. The message names the key. The owner watches lane 7.
- Control skill. `control-cli` drives a TUI through `rex type` and `rex capture`. No video tool exists for Rex. The review gate's video is a screen recording of the pane.

## Appendix D. Links and reading list

- `docs/spec/mvp.md`, the Orchestrator jobs table at line 562.
- `docs/adr/0001-scheduler-and-orchestrator-are-separate.md`, `0004`, `0009`, `0010`, `0012`, `0015`.
- `cmd/lineup/hub.go` lines 55-145 (role settings), 164-167 (tabs), 212 (role dir), 406-408 and 464-471 (`--continue`).
- `internal/board/model.go` lines 51-53 and 137-142 (views).
- `cmd/lineup/watch.go` lines 21-27 and 62-72.
- `internal/scheduler/api.go` lines 181 and 230-238 (`toWorker`).
- `internal/harness/claude/claude.go` lines 61 (`Trust`), 172-174 (session flags), 301 (`Prompt`).
- dsh reference, `scratchpad/repos/dsh-superagent-prototype/src/core/stage.ts` and `docs/000-philosophy.md`. Guide reference, `scratchpad/repos/deepseek-superagent-guide/README.md` lines 93, 125-135, 168, 326.
- S1b and S2a get `pstack/skills/how/SKILL.md` before editing. No PR gets `interrogate`. The design was settled by the investigation on 2026-11-09.
- The decision trail is this file's Appendix A plus the commit messages, per `pstack/skills/show-me-your-work/SKILL.md`.

## Appendix E. Outcome

Every PR below merged into `main` on 2026-11-10. The checklist above is the plan as written before execution. Its boxes stay unchecked, because the evidence for each lives in the PR, not in this file. `docs/adr/0016-one-front-agent.md` records the decision.

| Plan id | PR | Merge commit | What landed |
|---|---|---|---|
| S1a | [#18](https://github.com/GClunies/lineup/pull/18) | `32ed81a` | `lineup watch` prints every Kind |
| S1b | [#19](https://github.com/GClunies/lineup/pull/19) | `56c5a98` | one front agent, `LINEUP`, over the boards |
| S1c | [#20](https://github.com/GClunies/lineup/pull/20) | `aa4dfe2` | one `hub` tab cycles the boards with `1`, `2`, `3` |
| F | [#22](https://github.com/GClunies/lineup/pull/22) | `d1f870e` | the `harness` setting is gone |
| S2a | [#23](https://github.com/GClunies/lineup/pull/23) | `ba263fc` | `lineup ask` puts a Worker's question on its row |
| S2b | [#24](https://github.com/GClunies/lineup/pull/24) | `84c73ca` | steers replay when a Worker restarts |
| S2c | [#25](https://github.com/GClunies/lineup/pull/25) | `ae9653d` | a review whose conversation is gone starts afresh |
| H | withdrawn | | empty groups were already hidden, see Appendix B |

Two follow-ups came out of the live checks and merged the same day. [#27](https://github.com/GClunies/lineup/pull/27) puts a stopped Worker's last line on its flag. [#28](https://github.com/GClunies/lineup/pull/28) rewrites the agent's files on every Hub run.
