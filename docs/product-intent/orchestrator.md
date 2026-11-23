# Orchestrator Product Intent

This document states the current intended experience for the one agent that the user talks to in the Hub.

## PROD-ORCH-001: One agent takes every request in the Hub

The Orchestrator is the one agent that the user talks to. It runs in the `LINEUP` pane of the `lineup` tab, below the Board. It plans Issues, takes build Tasks and review Tasks, and answers about both Kinds. The Hub starts it with no first message.

## PROD-ORCH-002: The Orchestrator maps each request to a lineup command

The Orchestrator changes Tasks only through the `lineup` CLI. It maps "start ENG-123" to `lineup add`, an ad hoc job to `lineup add --repo`, and "what needs me?" to `lineup decide`. A steer goes to `lineup steer`, a cap or a rerun to `lineup cap` or `lineup rerun`, and a restore to `lineup open`. "review <url>" and `/pr-review <url>` go to `lineup review`. `/pr-explain` for a PR with an open review Task goes to that Task's Worker. Every other slash command becomes a Task that runs the skill. The Orchestrator never follows a skill itself.

## PROD-ORCH-003: The Orchestrator asks nothing about a Task on its own

The Orchestrator asks the user nothing about a Task on its own, and it reports no alert. It never asks about a review Task and never passes on the questions of a review Worker. When the user asks about a Task that needs them, it runs `lineup decide`, and then only the choice that the user picks. The user decides each Task in Needs you with What Needs Me… in Rex's command palette.

## PROD-ORCH-004: The Orchestrator knows each PR that is ready to land

Before each turn, the Orchestrator gets the output of `lineup ready`: one line for each build Task in Ready to land with no flag. An example line is `Ready to land: ENG-571, PR #4636 <url>, Rate limit login`. No line means that no PR is ready. So the Orchestrator learns of a ready PR at the user's next message. It uses the lines to answer "what is ready?" and to find the Task for "merge it". If more than one PR is ready, it asks the user which one to merge.

## PROD-ORCH-005: A full WIP cap goes to the user

If `lineup add` fails with `wip cap full`, the Orchestrator asks the user. The options are to start the Task once over the cap, or to raise the cap by one and add the Task again. The Orchestrator never passes `--over-cap` without the user's pick.

## PROD-ORCH-006: The Orchestrator plans in the Trackers that are on

The Orchestrator shapes the backlog in each Tracker that is on. It reads Issues, splits work into Issues, writes acceptance criteria, and records the order of Issues in the Tracker's own form. Before each turn, it learns from `lineup trackers` which Trackers are on, and it plans only in those. It writes each Issue so that one Worker can finish it in one Repo. It shows the user a split or an order before it creates or changes many Issues.

## PROD-ORCH-007: A plan starts no Task

A plan starts no Task, in any Tracker. The Orchestrator starts a Task only when the user asks for it. lineup moves each Issue through its lifecycle, and the Orchestrator never sets those statuses or labels. Its settings deny `linear issue start` and `linear issue pull-request`. To start work, the user presses `s` on an Issue row of the Board, or asks the Orchestrator.

## PROD-ORCH-008: The Orchestrator names the Destination before it writes an Issue

Before the Orchestrator writes an Issue from the user's words, it names the Destination to the user, for example `local` or `acme / ENG / Billing`.

## PROD-ORCH-009: Only the user posts to a PR

The Orchestrator never writes to a PR. Its settings deny each GitHub write to a PR, `gh api`, and each delete of an Issue. It never writes code, opens a PR, or reviews, explains, comments on, or approves a PR itself. Workers do that work in their own tabs. A review reaches GitHub only after the user decides it.

## PROD-ORCH-010: The Orchestrator runs the Repo setup

When `lineup add` fails with `repo not set up: <owner>/<repo>`, the Orchestrator runs `lineup repo detect <owner>/<repo> --json`. It asks for the Tracker, the review bot gate, and the Landing bar checks, with the detected value as the first option. It offers a Task that writes worktrunk hooks when the Repo has none. If the user picks Linear, it asks for the Linear teams next. Then it runs `lineup repo init <owner>/<repo>` with the answers, and runs the same `lineup add` again.

## PROD-ORCH-011: The Orchestrator edits the review queues for the user

When the user asks to add or change a review queue, the Orchestrator edits only `[review]` and `[[review.queues]]` in the config file. Then it runs `lineup restart`. If the restart prints a load error, the Scheduler keeps the old file, and the Orchestrator fixes or undoes its edit. Then it shows the changed lines and `lineup reviews`. It also pauses, resumes, skips, and unskips through `lineup reviews`. It asks the user before a skip that ends an open review Task.
