# Lifecycle Architecture Intent

This file states how the lifecycle reducer turns Signals into Stages and effects.

## ARCH-LIFE-001: A pure reducer applies every transition

The lifecycle reducer holds every Task transition, and it does no I/O. It takes a Task and one Signal, changes the Stage and the flag, and returns a list of Effects. The Scheduler feeds it Signals, saves each change with its event, and runs the Effects in order: prompt the Worker, merge, clean up, and write the Tracker status. A Worker or GitHub Signal that does not fit the Stage changes nothing. A user Signal that does not fit returns an error.

## ARCH-LIFE-002: One level Signal carries the facts of the PR

Each poll sends `pr-seen` for each open build PR, after the edge Signals of that poll. It carries the current facts of the PR: the head, draft, conflict, checks, score, threads, Feedback, and review decision. The reducer measures the facts against the Landing bar of the Task and keeps the result as its Standing. A second `pr-seen` with the same facts changes nothing. The other GitHub Signals are the edges `pr-open`, `pushed`, `review-*`, and `pr-merged`, and the level `pr-closed`.

Because: A restart loses a wait that no new edge repeats, and each edge needs the same facts.
Not: The edge Signals `ci-passed`, `ci-failed`, `review-approved`, and `pr-conflict`, with the bar facts added to `ci-passed`.

## ARCH-LIFE-003: One table orders the gaps

One table in the lifecycle reducer lists the gaps of the Landing bar in order, each with its text: conflict, failed check, pending check, draft, score due, low score, open thread, new Feedback, and approval. The first gap of the Standing decides, through one route for each gap, the Stage of a Task in CI running, In review, or Ready to land. It also settles an agent Stage whose budget the Standing spent, so that flag clears at the first poll after the gap closes. A Task already in that Stage stays there. A failed optional check is no gap.

## ARCH-LIFE-004: The Kind keeps build Stages and review Stages apart

Each Task has a Kind, `build` or `review`, and each Stage names the Kinds that can enter it. A build Task moves through Working, CI running, CI debug, In review, Responding, and Ready to land. A review Task moves through Reviewing and Review ready. Done belongs to both. Review ready is a user Stage, so recovery never flags a finished review. A rerun takes only a Stage of the Kind of the Task. A review Task gets no Effect that writes to GitHub or to a Tracker, because those Effects come from build Stages.

Because: A review needs the tab, Worker, recovery, and Needs you group of a Task, and it must never get the GitHub effects of a build Task.
Not: An ad hoc Task with `skill=pr-review`, which never learns the PR that it reviews; a build Task on the head branch of the PR, which the branch poll treats as the user's own PR.

## ARCH-LIFE-005: Only build Tasks count toward the WIP cap, and the reducer checks review slots

The WIP cap counts only the started build Tasks. An add at a full cap fails unless the user said yes, and the Task then records that it went over the cap. The reducer refuses a review that a queue adds when the started reviews of that queue reach its `slots`. It also refuses one when the started reviews of all queues reach `review.slots`. A review that `lineup review` adds has no queue and takes no slot. A refused add leaves the Board as it was.

## ARCH-LIFE-006: A pending prompt suspends the contract check

When an agent Stage starts, the Record marks the prompt of that Stage as pending until the Worker receives it. While a prompt is pending, the Scheduler skips the Stage contract check, and a `done` sets no flag.

Because: A `done` while a prompt is pending belongs to the Stage before it or to the user's own message.

## ARCH-LIFE-007: Cleanup comes from the user's ends of a build Task and from each end of a review

The cleanup Effect closes the tab of the Task, removes its worktree, and removes the Repo session when it holds no other tab. A build Task gets it only from the user: `task-closed` in any Stage, or `tab-closed` on a Done Task. A build Task that ends at a merge gets no cleanup and keeps its tab and its worktree. A review Task gets cleanup at each end: the user's review, a merge, a close of the PR, `lineup close`, and a skip.

## ARCH-LIFE-008: Review ends are levels that each poll computes again

For a review Task, `pr-reviewed`, `pr-merged`, and `pr-closed` are levels that each poll computes again from the current state of the PR, so a Signal that waits is not lost. The Scheduler applies `pr-merged` and `pr-closed` at once, also while the Worker works. It applies `pr-reviewed` only while the Worker does not work, and then before a merge or a close. The Worker works unless its last status is `done` or `idle`, it exited, or the Task has no tab. A title that the queue drops is a level too, and in Review ready it also waits for the Worker.

Because: `pr-review` posts the review in a late step, and a Done in between closes the tab before that step ends.

## ARCH-LIFE-009: The reducer holds its invariants under random Signals

These invariants hold for every sequence of Signals. The started build Tasks never pass the WIP cap without a recorded yes. Each started Task ends in Done or in Needs you. No Task is in Ready to land while its Standing has a gap. An optional check never starts CI debug. A second `pr-seen` with the same facts changes nothing. Review cycles never pass their budget. Each Kind stays in its own Stages. Review Tasks never change the WIP count, never pass their slots, and get no Effect that writes outside lineup.

## ARCH-LIFE-010: Review cycles and Responding rounds are separate budgets

Each Task keeps its CI debug attempts, Responding rounds, and review cycles apart. A low score of the review bot spends a review cycle. Open threads that only bots opened, on a score that passes, spend a review cycle too. People's reviews, a thread that a person opened, unread threads, new Feedback, and a merge conflict spend Responding rounds. A PR with no review bot gate has no score, so its bot threads spend Responding rounds. Ready to land gives the Responding budget back, because that budget is per review.

Because: People's reviews and the rounds of the bot starve each other on one budget, and `max_review_cycles` then means nothing.
Not: Bot rounds that spend the Responding budget.
