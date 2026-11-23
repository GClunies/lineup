# Store Architecture Intent

This file states how lineup records Tasks and their history.

## ARCH-STATE-001: A Task is a local record that links to at most one Issue

The local store holds each Task and its execution state. A Task links to zero or one Tracker Issue, so a Task can exist with no Issue. The Tracker stays the source of truth for planned work, and it holds no Stage and no Needs you flag. The Board shows each Task from the store with no Tracker round trip.

Because: A Linear team has about five statuses, and copies of lineup's Stages flood the team's views and fight Linear's GitHub automation.
Not: The Tracker as the database, as in lerp, where a Linear status is the queue and no local store exists.

## ARCH-STATE-002: One table holds the Tasks, and an append-only table holds their events

The store is one SQLite database in the state directory. One table holds the Record of each Task by its ID. An append-only table holds the events of each Task: the Signal, the Stage before and after, the time, and a detail. The Scheduler saves a Record and its new events in one transaction. A settings table holds the Scheduler state that belongs to no Task, for example the WIP cap that the user set. An archived Task keeps its Record and its events.

## ARCH-STATE-003: The store saves Stages by key and converts removed Stages at load

The store saves each Stage by its key, for example `ci_debug`, and never by its position in the code. When the Scheduler loads the store, it converts each Record that holds the key `queued`. Such a Task never started, so it becomes an archived Done Task with the outcome `removed`, and its Issue shows as an Issue row again. The conversion saves the Record with an event, so a second load finds nothing to change. Any other Stage key that the Scheduler does not know stops the load with an error.

## ARCH-STATE-004: The store keeps each ID in one canonical form

The store keys each Task by its canonical ID. A GitHub ID keeps the form that GitHub gives, and every other ID is in upper case, for example `ENG-614`, `LOCAL-7`, or `AH-65`. The Scheduler converts the ID of each request to that form first. At start, the Scheduler moves each Record stored under another case to its canonical ID, with its events, in one transaction. The Task keeps its number and its branch.

## ARCH-STATE-005: A Task keeps a copy of its bar and gate from Intake

At Intake, the Scheduler resolves the Loop budgets, the Landing bar, and the review bot gate of the Task from the configuration, and it stores them in the Record. The reducer reads only that copy. A change to the configuration reaches only the Tasks that come on the Board after the change. A Record with no Landing bar decodes to the zero bar: every check gates, threads never gate, and every requested reviewer must approve.

## ARCH-STATE-006: The Record holds the Rex IDs, the conversation, and the pending prompt

Each Record keeps the Rex session, tab, and block IDs of its Task, and the block ID, folder, and command of its side pane. Each Rex event and each recovery finds its Task through these IDs. The Record keeps the conversation ID of the Task, which its first Worker starts and every later Worker resumes. It keeps the pending Stage, whose prompt the Worker has not received, and the last program status of the Worker.

Because: Rex has no key-value metadata on its objects.

## ARCH-STATE-007: Skips and the review pause live in the store

The store holds each skip in its own table, with the PR URL, its title, and the time of the skip. The URL key ignores case, and the first write of a skip wins. The settings table holds the pause of `lineup reviews pause`. Both hold across a restart of the Scheduler, and neither writes to GitHub. A skip never expires.
