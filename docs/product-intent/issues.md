# Issues Product Intent

This document states the current intended experience for which Issues the Board shows, in which order, and how the user captures and promotes them.

## PROD-ISSUE-001: The Board holds the user's own Issues that have no Task

The Board shows the user's own Issues that have no Task as Issue rows, so the user plans the what and the Worker plans the how. The user's own Issues are the Linear Issues assigned to the user in each signed-in workspace, the local Issues, and the user's own GitHub Issues. An Issue row shows the ID, the title, a status word, the Linear priority, and the Repo when it is known. A Linear Issue row names its Repo, for example `data` for `crexi/data`, when exactly one Repo lists its team in `teams`.

## PROD-ISSUE-002: The Tracker state puts an Issue in Up next or Backlog

The Tracker state of an Issue puts it in Up next or Backlog. A Linear Issue is in Up next in an `unstarted` state type, and in Backlog in a `backlog` or `triage` state type. A local Issue is in Up next with `status: todo`, and in Backlog with `status: backlog`. A GitHub Issue is in Backlog with the backlog label, and in Up next without it. The `backlog` key of `[github.labels]` names that label, `backlog` by default, matched without case. An empty value puts every GitHub Issue in Up next. An Issue in any other state is in neither group.

## PROD-ISSUE-003: Team holds the unassigned Issues of the user's teams

Team holds the open Linear Issues of the user's teams that have no assignee, in a `triage`, `backlog`, or `unstarted` state type. The teams are the keys of every `teams` list in the config. The Board reads each team in each signed-in workspace, at most the 100 Issues that changed last. With no `teams` list, Team is empty. An Issue that a Task holds, or that is the user's own, has no Team row. `s` on a Team row starts a Task as on an Up next row, and the start assigns the Issue to the user.

## PROD-ISSUE-004: The Board shows the user's own GitHub Issues of repositories with a checkout

While the GitHub Tracker is on, the Board shows the user's own open GitHub Issues: each Issue with the user as an assignee, and each Issue with no assignee that the user opened. A pull request gets no row. An Issue shows only when a checkout below the roots has the Issue's owner/repo as its Repo key, matched without case. A sign-in, a sign-out, or a new clone shows within 60 s. A GitHub row names its repository as its Repo. `lineup add --repo <repo> owner/repo#n` adds an Issue from any repository.

## PROD-ISSUE-005: An Issue with a Task has no Issue row

An Issue whose ID a Task on the Board holds has no Issue row. The Task row shows that Issue. So an Issue row leaves its group when its Task comes on the Board.

## PROD-ISSUE-006: Blockers mark an Issue blocked or unblocked

An Issue with an open blocker shows `blocked` in pink, and the strip names its open blockers as `blocked by <IDs>`. An Issue that has blockers, all done or canceled, shows `unblocked`. A Linear blocker is a "blocks" relation, and a local blocker is an entry in `blocked_by`. A GitHub blocker is an Issue that a `Blocked by #12` or `Blocked by owner/repo#12` line of the body names. It stays open until GitHub reports the Issue closed, or the PR merged or closed. The strip names a blocker in the same repository as `#12`.

## PROD-ISSUE-007: The Issue groups sort by blockers, priority, level, and ID

Up next, Backlog, and Team each sort their rows by four keys, in this order. First come the Issues with no open blocker. Then priority sorts urgent, high, medium, low, and no priority. A local or GitHub Issue has no priority. Then the dependency level sorts lower first: an Issue with no open blocker has level 0, and any other Issue has a level one higher than its highest open blocker. Blockers in other Projects count too. Last, natural ID order sorts the rest.

## PROD-ISSUE-008: Issue rows paint from the cache and refresh every minute

The Board paints the Linear Issue rows from the cache of each workspace's lists, then fetches them live. It fetches again every 60 s, and right after a capture, a promote, a start, a status change, or an edit in `$EDITOR`. A failed fetch keeps the rows of the last fetch, and the footer names the list and the workspace, for example `team Issues in acme: timeout`. GitHub Issues have no cache. A failed GitHub read keeps the last GitHub rows, and the footer shows `GitHub Issues:` and the error. The Board reads the Local Tracker whatever `local_tracker` says.

## PROD-ISSUE-009: Follow-up holds the merged Tasks whose Issues are still open

Follow-up holds the Done build Tasks whose Issues are open. A Linear Issue is open in an `unstarted` or `started` state type, and a local Issue is open unless it is `done` or `canceled`. The status word is the Tracker state, for example `Waiting for QA`, and the note names a kept worktree. When the Tracker closes the Issue, or the Issue stops being the user's own, the row moves to Done. Follow-up starts open, and its rows do not fade. `x` and the `today` age work as on a Done row. Follow-up rows add nothing to the Needs you count.

## PROD-ISSUE-010: The user reads, opens, and changes an Issue from its row

On an Issue row, `f` shows the Issue's description rendered from Markdown, and `f` or `esc` goes back. `o` opens a Linear or GitHub Issue in the browser, and a local Issue's file in `$EDITOR`. `c` opens the status list at the Issue's status: the workflow states of its Linear team, or `backlog`, `todo`, `started`, `in review`, `done`, and `canceled` for a local Issue. A GitHub row has no `c`. `enter` sets the picked status, and `esc` cancels. The row then moves to its new group or leaves the Board. A failed read or write names the error and changes nothing.

## PROD-ISSUE-011: A capture goes to the Local Tracker by default

`n` opens the capture box at the Destination `local`. `enter` creates the Issue there, and an empty title creates nothing. `tab` opens one fuzzy list of Destinations: `local`, then the last 5 picks, then each Linear workspace, team, and open Project. The default stays `local` after every pick. A local capture creates a local Issue with `status: backlog`. A Linear capture creates the Issue in the team and Project, assigned to the user, in the team's default status. The footer names the new Issue, for example `added LOCAL-9`, and its row shows after the next fetch.

## PROD-ISSUE-012: A promote moves a local Issue to Linear once

`P` on a local Issue row opens the Destination list without `local`. When the user picks a Destination, lineup creates a Linear Issue there with the local Issue's title and body, assigned to the user. Then lineup sets the local Issue to `canceled`, adds the line `Promoted to ENG-612.` to its body, and the footer names the new Issue. If the create fails, the local Issue does not change. If the local write fails, the footer names both Issues, so the user can cancel the local Issue by hand. lineup never syncs the two Issues after the promote.
