# A Landing bar decides Ready to land

A build Task is in Ready to land only while its PR meets a Landing bar: the required checks pass, the review bot's score meets the minimum, every review thread is done, and the required reviews approve. A `[landing_bar]` table can change the checks, threads, and approvals items. We chose this because the old rules sent a PR to Ready to land with open review threads, and they sent a failed optional check to CI debug. The user wants Ready to land to mean that the PR can merge now.

The bar rests on three choices:

1. **One level Signal.** Each poll sends `pr-seen` with the PR's current facts. The reducer measures the facts against the bar, and the first gap decides the Stage. `pr-seen` replaces `ci-passed`, `ci-failed`, `review-approved`, and `pr-conflict`.
2. **Required checks come from the base branch's rules.** The poll reads the classic branch protection's `requiredStatusCheckContexts` and each active ruleset's `REQUIRED_STATUS_CHECKS` rule, by branch name. If the base branch requires no check, every check gates.
3. **A done thread is resolved and replied.** A thread that someone other than the user opened needs a reply by the user's login. Workers post as the user.

## Considered Options

- **Keep the edge Signals and add the bar's facts to `ci-passed`.** Rejected. `review-approved`, `pr-conflict`, and a new thread edge would each need the same facts. A restart would lose a wait that no new edge repeats.
- **Read `isRequired` on each check.** Rejected. It needs a PR number, so the first poll of a PR needs a second query. A wrong number fails the whole batched query. It also cannot see a required check that GitHub has not made yet. The base branch's rules give the same checks with none of these costs.
- **Count a thread as done when it is resolved.** Rejected as the default. A Worker could resolve a person's thread with no answer. `threads = "resolved"` keeps this rule for a Repo that wants it.
- **Spend the Responding budget for bot threads on a score that passes.** Rejected. People's reviews and the bot's rounds would starve each other, as [ADR 0013](0013-a-review-bot-gates-ci-passed.md) says.

## Consequences

- The reducer can move a Task out of Ready to land when the PR stops meeting the bar, for example when a new thread opens.
- An optional check that failed never starts CI debug. The Board, the detail pane, and `lineup ls --json` name it.
- A merge conflict is the first gap, so a conflict in CI running starts Responding.
- A Responding round that pushes nothing meets its contract when every thread is done. A Worker can answer "won't fix" and resolve the thread.
- A budget that a gap spent clears at the next poll after the gap closes.
- The poll reads the first 100 check contexts and the first 50 review threads of each PR. A thread past the read window counts as open.
- A Task stored before the Landing bar keeps the old rules: every check gates, threads never gate, and every requested reviewer must approve.
