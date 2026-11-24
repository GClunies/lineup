# QA Product Intent

This document states the current intended experience for how QA Tasks start after a merge, check the merged change, and end.

## PROD-QA-001: A merge starts a QA Task when QA is on

With `enabled = true` in the `[qa]` table of a Repo's layer, the merge of each build Task in that Repo adds one QA Task. A merge in any Stage counts, also a merge outside lineup. The build Task still goes to Done at the merge, and `[merged]` still applies to its tab. QA is off by default, so a merge then ends the work and adds nothing.

## PROD-QA-002: A QA Task names its build Task

A QA Task has the ID `QA-` and the build Task's ID, for example `QA-ENG-12`, `QA-AH-65`, or `QA-GClunies/lineup#123`. Its title is the build Task's title. It opens in its own tab of the `qa` Rex session. Its tab label drops the owner of a GitHub ID, for example `QA-lineup#123 Add billing model`.

## PROD-QA-003: The QA Stages and their owners

A QA Task moves through four Stages. It starts in QA waiting, which lineup's clock owns and the Board shows in the Waiting group. The agent owns Verifying, which shows in the Working group. The user owns QA failed, which shows in the Needs you group. Done has no owner and shows in the Done group.

## PROD-QA-004: A new Worker checks the merged change

Each QA Task has its own Worker, with a new conversation in its own tab, never the Worker of the build Task. Its worktree is detached at the merge commit. The Verifying prompt gives the Issue with its description, the PR, the merge commit, and the merge time. It asks the Worker to find out if the merged change ran, and to check the result against the Issue's acceptance criteria. `[stage.verifying]` can set its `skill`, `command`, `model`, and `effort`.

## PROD-QA-005: The Worker reports each check with lineup qa

The Verifying contract is one verdict. `lineup qa <task> wait <when> "<reason>"` sets the next check, where `<when>` is a duration such as `2h`, a clock time such as `03:00`, or an RFC 3339 time. `lineup qa <task> pass "<summary>"` ends the Task. `lineup qa <task> fail "<findings>"` sends it to QA failed. A Worker that reports `done` with no verdict gets the flag `stopped`. A Worker can also give a verdict in QA waiting or QA failed, for example after the user steers it.

## PROD-QA-006: A waiting QA Task keeps its Worker

While a QA Task waits, its Worker stays idle in its tab, and lineup does not end its process. At the next check time, lineup types the check prompt into the same Worker, so the Worker keeps what its earlier checks found. The note shows the next check and the reason, for example `next check Nov 25 03:00: the DAG has not run`. If the tab is gone when a check comes due, the Task gets the flag `tab closed`.

## PROD-QA-007: QA slots cap the checks that run

`slots` in the top-level `[qa]` table caps the QA Tasks in Verifying. With no `slots`, there is no cap. A check that comes due while the slots are full waits, and the check that came due first starts first when a slot frees. A QA Task in QA waiting or QA failed holds no slot. QA Tasks never count toward the WIP cap, and a full WIP cap never stops a QA Task.

## PROD-QA-008: A QA Task waits at most max_wait

`max_wait` in the `[qa]` table of a Repo's layer is the longest time that a QA Task can wait for a verdict, 48h from the merge by default. When a QA Task in QA waiting reaches it, the Task goes to QA failed with the note `no verdict after 48h`. A wait that the Worker sets past it ends at it. A QA Task always gets its first check, also when its merge is older than `max_wait`.

## PROD-QA-009: A pass ends the QA Task and tells the Tracker

A pass moves the QA Task to Done with the outcome passed, and lineup writes the Tracker event `qa_passed` for the Task's Issue. The summary becomes the note. lineup closes the tab once the Worker stops working, and removes the QA worktree. A worktree with an uncommitted change stays, with the note that names its path.

## PROD-QA-010: A failed QA Task offers three choices

QA failed waits for the user in Needs you, with the findings as its note. `lineup decide` offers Start a fix Task, Accept the result, Check again, and Open the tab, in this order. Accept the result ends the QA Task with the outcome accepted, and writes `qa_passed`. Check again moves the Task to Verifying at once, whatever the slots say. It also gives the Task a new `max_wait` from now.

## PROD-QA-011: A fix Issue goes to the build Task's Tracker

Start a fix Task creates a new Issue in the Tracker of the build Task's Issue: in its Linear team and Project, its GitHub repository, or the Local Tracker with its Repo. An ad hoc build Task's fix Issue goes to its Repo Tracker: the first of its `teams`, its GitHub repository, or else the Local Tracker. The title is `Fix QA of <build ID>: <title>`. The description holds the findings and links to the Issue, the PR, and the merge commit. lineup assigns the Issue to the user. The QA Task ends with the outcome fix filed, and its note names the new Issue.

## PROD-QA-012: The usual commands work on a QA Task

`lineup open`, `lineup close`, `lineup rerun`, `lineup show`, and `lineup ls` work on a QA Task. `lineup rerun <task> verifying` starts a check at once, whatever the slots and the next check say. `lineup close` ends the QA Task with the outcome closed, closes its tab, removes its worktree, and writes nothing to the Tracker. `lineup ls --json` gives each QA Task its `next_check` and its `deadline`.
