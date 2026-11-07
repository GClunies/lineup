# A review bot gates ci-passed

A Repo can name a review bot, Greptile, in a `[review_bot]` table. For a Task in such a Repo, `ci-passed` goes on only when the bot's summary comment scores the PR's head commit at or above the minimum. A lower score starts a Responding round that spends a review cycle, a Loop budget of its own. We chose this because Greptile reviews each push by itself, and its score is the bar that the user wants each PR to clear before people review it.

## Considered Options

- **Read the score from Greptile's check run.** Rejected. The `Greptile Review` check run passes at a score of 4/5, and its output names no score.
- **Read the score from the PR body too.** Rejected. Some installs write the summary there, but the PR author can edit the body, and GitHub names only the last editor. Such a Repo gets the flag `no bot review` instead.
- **Spend the Responding budget.** Rejected. People's reviews and the bot's rounds would starve each other, and `max_review_cycles` would mean nothing.
- **A global `[review_bot]` table.** Rejected as the documented form. Greptile is installed on some Repos only, so the table sits in a `[[repos]]` entry or a `.lineup.toml`.

## Consequences

- The poll reads the first 20 PR comments, only for gated Tasks.
- A gated Task in CI running whose checks passed with no head score for 15 minutes gets the flag `no bot review`, so a Repo where the bot does not review never holds a Task forever.
- Greptile's own PR reviews do not send `review-commented` for a gated Task.
- A Task keeps the gate that it had at Intake. A change to the configuration reaches only the Tasks that come on the Board later.
