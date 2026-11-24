# Trackers Product Intent

This document states the current intended experience for the Trackers that lineup works with, their sign-in, their IDs, and what lineup writes to them.

## PROD-TRACK-001: lineup works with Linear, GitHub Issues, and a Local Tracker

lineup takes Issues from three kinds of Tracker: Linear workspaces, the GitHub Issues of the user's account, and the Local Tracker on the user's machine. The user can plan and start work from each of them. A user with no Linear workspace and no GitHub Tracker still gets Issue rows on the Board, from the Local Tracker.

## PROD-TRACK-002: The user signs in to each Tracker with lineup

`lineup login` asks which Tracker to sign in to, Linear or GitHub. `lineup login linear [workspace]` signs in to a Linear workspace in the browser. `lineup logout linear [workspace]` revokes the sign-in at Linear and then forgets the tokens. With `--local`, it only forgets the tokens on this machine. `lineup trackers` names the Trackers that are on.

## PROD-TRACK-003: GitHub becomes a Tracker only when the user signs it in

GitHub is a Tracker only after `lineup login github`. A signed-in `gh` alone does not make it one. `lineup login github` records the account that `gh` is signed in to as the GitHub Tracker. If `gh` is not signed in, the command tells the user to run `gh auth login`. `lineup logout github` removes the GitHub Tracker and leaves `gh` signed in.

## PROD-TRACK-004: "On" decides where the Orchestrator plans

Linear is on while at least one workspace is signed in. GitHub is on while it is signed in as a Tracker. The Local Tracker is on when no other Tracker is on, or when the config file sets `local_tracker = true`. `lineup trackers` names the Trackers that are on, and the Orchestrator plans only in those. The Board shows the local Issues whatever "on" says. A Task keeps its Issue when another Tracker comes on, for example a local Issue after a Linear sign-in.

## PROD-TRACK-005: A Linear sign-in can use the OAuth app of the workspace

By default, a Linear sign-in uses lineup's own Linear OAuth app, which any workspace can use. The top-level key `linear_client_id` sets the user's own app, registered with the redirect URI `http://localhost:47821/callback` and no client credentials. The key must come before the first table of the file. For a workspace that allows only apps that an admin approved, `client_id` under `[linear.<url key>]` sets the app of that workspace. If the user then picks another workspace on Linear's consent screen, lineup refuses the sign-in. Tokens keep the app that issued them, so a new client ID applies at the next sign-in.

## PROD-TRACK-006: A typed ID names exactly one Tracker

`lineup add` takes three forms of Issue ID: `<TEAM>-<n>` for Linear, for example `ENG-614`, `<owner>/<repo>#<n>` for GitHub, and `LOCAL-<n>` or `<PREFIX>-<n>` for the Local Tracker. An ID that starts with `LOCAL-` always names a local Issue. lineup refuses a Repo prefix that equals a Linear team key, so a `<PREFIX>-<n>` ID never names a Linear Issue. A start from a Board row reaches the Tracker of that row, also for a Linear team keyed `LOCAL`.

## PROD-TRACK-007: An ID ignores case and shows in one form

Each command that takes an Issue or a Task takes its ID in any case, so `eng-614`, `Eng-614`, and `ENG-614` name one Task. lineup shows Linear IDs, local Issue IDs, ad hoc Task IDs, review Task IDs, and QA Task IDs in upper case, for example `HOD-3`, `AH-65`, and `QA-ENG-12`. It shows a GitHub ID in the form that GitHub gives, also after `QA-`. Branch names and the file names of local Issues are in lower case. The Board, `lineup ls`, `lineup show`, `lineup next-id`, and the Orchestrator's instructions use these forms.

## PROD-TRACK-008: Each Repo takes the Issues of one Tracker

The `tracker` key of a Repo's table names the one Tracker whose Issues the Repo takes: `linear`, `github`, or `local`. The Repo's own table beats the table of its owner. For a Repo with no `tracker`, `lineup add <ISSUE>` fails with `repo has no tracker: <owner>/<repo>`. It refuses an Issue of another Tracker, and the error names the Repo, its Tracker, and the Issue's Tracker. The Repo picker offers a Repo only for an Issue of its Tracker. An ad hoc Task can use each Repo. `teams` needs `tracker = "linear"`. The Orchestrator plans the Issues of a Repo only in its Tracker.

## PROD-TRACK-009: A Repo prefix gives a Repo its own IDs

The `prefix` key of a `[repos."<owner>/<repo>"]` table gives the local Issues and ad hoc Tasks of the Repo IDs of the form `<PREFIX>-<n>`, for example `HOD-3`. A Repo with no prefix uses `LOCAL-<n>` and `AH-<n>`. The prefix can be in any case. Each prefix numbers on its own. The next number is one more than the highest number of the prefix among the local Issues and every Task, archived Tasks too. `lineup next-id <repo>` prints the next free ID. `lineup add` refuses a local Issue whose ID another Task holds.

## PROD-TRACK-010: lineup sends the same five lifecycle events to each Tracker

lineup sends each Tracker the events `started`, `in_review`, `merged`, `closed`, and `qa_passed`. `closed` is the user's close, and `qa_passed` is a QA pass. When a Task starts, lineup assigns its Linear or GitHub Issue to the user. A Linear Issue takes the status that its team's status map gives, and a GitHub Issue takes the label that the label map gives. A local Issue takes `status: started`, `status: in review`, and `status: done`, and nothing for `closed` or `qa_passed`. The status changes when the Task ends, not when the user closes its tab. The Needs you flag writes nothing to a Tracker.

## PROD-TRACK-011: The status map of a Linear team decides each status write

Each Linear team has its own map from a lifecycle event to a status, in a `[linear.<url key>.status.<TEAM>]` table of the config file. lineup writes a Linear status only for an event that the map names. It writes nothing for an event that the map leaves out, so Linear's GitHub automation keeps the statuses that it sets. For example, a team that tests each change after the merge leaves out `merged` and maps `qa_passed` to Done.

## PROD-TRACK-012: A GitHub Issue carries at most one lifecycle label

The `[github.labels]` table maps each lifecycle event to a label, for every repository. For an event, lineup adds its label and removes the labels of the other events. If the map leaves out `started`, `in_review`, or `qa_passed`, lineup writes nothing for that event. If it leaves out `merged` or `closed`, lineup removes every lifecycle label. lineup creates a label that the repository does not have. It never adds or removes the `backlog` label. The PR of a GitHub Issue carries `Fixes <owner>/<repo>#<n>` in its body, so GitHub closes the Issue at the merge.

## PROD-TRACK-013: The Local Tracker is one folder of Markdown files outside every Repo

The Local Tracker is one folder in lineup's data directory, outside every Repo, and `local_issues_dir` moves it. Each Issue is one Markdown file, named by its ID in lower case. The front matter of the file holds `title`, `status`, an optional `repo` with a Repo name or path, and an optional `blocked_by` list of local Issue IDs. The statuses are `backlog`, `todo`, `started`, `in review`, `done`, and `canceled`. A new Issue starts in `backlog`. Two writers never take one ID. The Board leaves out a file that does not parse.

## PROD-TRACK-014: The branch name comes from the Issue

A Linear Issue's branch is the branch name that Linear gives the Issue. A GitHub Issue's branch is `gh-<n>-<title slug>`. A local Issue's branch is its ID and title slug, for example `local-7-retry-failed-loads`. An ad hoc Task's branch is `lineup/ah-<n>-<title slug>`, or `<prefix>-<n>-<title slug>` in a Repo with a prefix. Each slug is the title in lower case, with each run of other characters as one dash, cut to 40 characters.

## PROD-TRACK-015: lineup refuses a Repo prefix that can clash

lineup refuses to load a bad prefix, and the error names it. A prefix holds only letters, digits, and single dashes, with no dash at either end. It is not `LOCAL`, `AH`, `PR`, or `QA`, it does not start with `QA-`, and no two tables share it. An owner table holds no prefix. A prefix never equals a team key of a `teams` list. `lineup restart` also checks the team keys of each signed-in Linear workspace, and if Linear does not answer, it prints a warning and checks only the `teams` lists. Case does not count in these checks.
