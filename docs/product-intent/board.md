# Board Product Intent

This document states the current intended experience for what the Board shows and how the user moves on it.

## PROD-BOARD-001: The Board is one view of the user's build Tasks and Issues

The Board is one view at the top of the `my-lineup` tab. It lists the user's build Tasks, the user's own Issues that have no Task, and the unassigned Issues of the user's teams. So the user sees all of their own work on one Board, from a captured idea to a merged PR. The Board lists no review Task. It has no other view, so the header names no view, and `1`, `2`, and `3` do nothing.

## PROD-BOARD-002: The groups and their default order

The Board puts each row in one of eight groups. By default the groups show in this order: Needs you, Follow-up, Backlog, Team, Up next, Working, Waiting, and Done. Needs you comes first with the rows that wait for the user, and Follow-up comes next. The other groups follow the development cycle, from an idea to a merged PR. Backlog, Team, and Up next hold Issue rows. Needs you, Follow-up, Working, Waiting, and Done hold build Task rows.

## PROD-BOARD-003: A group heading shows the name and the count

Each group with at least one row has a heading that shows the group name in sentence case and its row count in the dim style, for example `Backlog 7`. A closed group adds ` …` to its heading and hides its rows. A group with no row has no heading. The user can select each heading except the Needs you heading.

## PROD-BOARD-004: The user sets the group layout in the config

The `[board]` table in the config sets the layout of the groups `needs_you`, `follow_up`, `backlog`, `team`, `up_next`, `working`, `waiting`, and `done`. `order` lists the groups first to last, and each group that it leaves out follows in the default order. `collapsed` lists the groups that start closed, by default `backlog`, `team`, and `done`. `hidden` lists the groups that show nothing, by default none. An absent list takes its default, and an empty list means none. `needs_you` cannot be in `collapsed` or `hidden`. A bad table gives the default layout and an error line that stays until the Board quits.

## PROD-BOARD-005: The user opens and closes groups for one run

`enter` or a tap on a heading opens or closes its group for this run only. Needs you never closes. `d` shows and opens a hidden Done, and hides a shown Done, for this run only, whatever `hidden` says. When a closed group or the Team limit hides the selected row, the selection goes to that heading or the `+ N more rows` line. Any other lost selection goes to the first row, or to the first heading when every group is closed. A capture into a closed or hidden Backlog shows and opens it for this run, and selects the new row.

## PROD-BOARD-006: Each row shows one status word

Each row shows one status word in its own column after the title, and that column never drops. A Task in Needs you with a flag shows its reason: `question`, `permission`, `auth`, `error`, `setup failed`, `stopped`, `budget spent`, `tab closed`, `worker silent`, `draft`, `drafts`, `PR closed`, or `bot late`. A Task in Follow-up shows its Tracker state. Any other Task shows its Stage: `working`, `CI running`, `CI debug`, `in review`, `responding`, `ready`, or `done`. An Issue row shows `blocked`, `unblocked`, `up next`, `backlog`, or `team`. A flagged Task and a blocked Issue show the word in pink, and the word reads the same with `NO_COLOR=1`.

## PROD-BOARD-007: The strip shows the selected row in full

The strip under the rows shows a rule, the selected row's full title, and its facts joined by ` · `: the ID, the Stage, the Repo, the skill, the PR number, and the note. An empty fact does not show, and the PR number shows only when the note does not name it. An Issue row shows its group in place of the Stage. The title and the facts wrap, so the strip never cuts them. Then come the Board message, until the next key, and the key hints. On a heading, the strip shows the group name and row count.

## PROD-BOARD-008: The note of a Task says where its PR stands

The note of a build Task with a PR starts with the PR number and the first review state that holds: `✗ changes requested`, `✓ approved`, `● commented`, or `○ no review yet`. A review by a bot does not count. Then the note says what happens: `fixing CI` in CI debug, `answering review` in Responding, and `ready to land` in Ready to land. In CI running and In review, it names the next gap of the Landing bar, for example `1 open thread`, but never the approval gap. A failed optional check adds `optional failed: <names>`. A flag replaces the note with `<flag>: <detail>`, for example `drafts: 2 replies to approve`.

## PROD-BOARD-009: The header shows the alerts and the live state

The header shows `lineup` and its alerts in pink at the left: `reviews paused` while the review queues are paused, and the offline alert. At the right, it joins the live state with ` · `. `● N working` counts the Tasks in Working. `+R in reviews` counts the review Tasks in Needs you, and shows only when R is above 0. `D done since yesterday` counts the Tasks that reached Done since 00:00 yesterday, archived Tasks included. `WIP s/c` comes last, with ` +o over` in pink past the cap. A narrow header drops live items from the start, always keeps the WIP item, and then cuts with `…`.

## PROD-BOARD-010: A band marks the selected row

The selected row has the `▸` marker and a dark teal band over the full width of its pane. The band covers the marker, each cell, the separators, and the spaces to the end of the pane. A selected heading and a selected item in a picker or a menu get the same band. A pink status word and the dim count of a heading keep their color on the band.

## PROD-BOARD-011: A title shows as one plain line

The rows, the strip, and the menu of a tap show a plain title. It is the first line of the title that keeps a word after lineup drops its URLs and a trailing `:`, `,`, `;`, `-`, `–`, `—`, or `(`. One space separates its words. A URL is a word that holds `://` or starts with `www.`. A title of only URLs shows its first line. A Task with no Issue stores its plain title, cut at a word to at most 60 characters. Its tab label, its branch name, the filter, and `lineup ls --json` use that title.

## PROD-BOARD-012: The keys on a Task row

On a Task row, `enter` focuses the Task's tab in Rex. When the Task has no live Worker, `enter` restores the Worker, as `lineup open` does, also on a Done Task with no tab. `p` shows the Worker's live screen beside the Board. `f` shows the detail pane at full screen, and `f` or `esc` goes back. `m` merges the PR in Ready to land. `o` opens the PR, or else the Issue, in the browser. `x` closes the Task after a confirmation. On a Done Task, `x` also removes the worktree, and archives a Task with no tab.

## PROD-BOARD-013: The keys on every row

On every row, `j` and `k` move the selection through the headings, the rows that show, and the `+ N more rows` line. `d` shows or hides Done, `/` opens the filter, and `n` captures a new Issue. `R` makes the Scheduler poll GitHub for every started Task at once. `z` zooms the Orchestrator pane to full height, and back. `?` shows all keys, and `q` quits. A key that does not apply to the selected row does nothing. The footer hints name the keys of the selected row, drop hints from the end to fit, and always keep `? all keys`.

## PROD-BOARD-014: Mouse and touch work as the keys do

The Board works by mouse and touch, for example from Rex on a phone. A tap on a heading opens or closes its group. A tap on a row selects it and opens a menu of the actions that its footer hints name. A tap on an action runs it as its key does, and `back` closes the menu. When an action asks yes or no, or hits the WIP cap, the question is a menu too. A tap on the detail pane goes back. The scroll wheel moves the selection as `j` and `k` do.

## PROD-BOARD-015: The filter narrows the rows

`/` opens a filter line above the rows. The filter fuzzy-matches the ID and the title of each Task row and Issue row. While it holds text, only the matching rows show, and every group with a match shows open, also a closed or hidden group. Each heading counts its matches, and `j` and `k` skip the headings. Letters and digits are filter text, so `q` does not quit. `enter` keeps the filter and gives the keys back. `esc` clears it and gives back the rows and the selection from before. lineup does not save the filter.

## PROD-BOARD-016: The skill picker opens before a start

`s` on an Issue row opens the skill picker. Its first row, `just go`, starts with no skill, and the other rows are the skills in `start_skills`. Typed text filters the rows, and a typed name that is not listed becomes its own row. The picker drops a leading `/`. The chosen skill becomes the Task override `skill`. With no Repo mapping, the Repo picker opens next with each Repo of the row's Tracker, or only the Repos that the Issue fits when it fits more than one. Then the Board adds the Issue as a Task, which starts it.

## PROD-BOARD-017: The Board stays inside its speed budget

The Board paints its first screen from the cache in under 100 ms, and it renders each keypress in under 16 ms. It makes no network call before it draws, and it refreshes its data in the background. A new Task tab opens in under 2 s, without hook time. A Rex event reaches the Board in under 100 ms, and a GitHub change reaches it in under 35 s.

## PROD-BOARD-018: The my-lineup tab shows how many Tasks need the user

While one or more build Tasks are in Needs you, Rex shows a dot and a message on the `my-lineup` tab, for example `2 Tasks need you`. The count leaves out review Tasks and Follow-up rows. The Board sends the mark at start, and again each time a build Task joins or leaves Needs you or changes its Stage or its flag. When no build Task needs the user, the mark clears. The mark also clears when the Board quits. The Dock badge counts the `my-lineup` tab once, whatever the number of Tasks.

## PROD-BOARD-019: The Board toasts Ready to land and Done

The Board sends a desktop toast when a build Task reaches Ready to land or Done, for example `ENG-571 #4636 ✓ approved · ready to land | Rate limit login`. A click on the toast opens the Board's pane. A build Task that enters Needs you for a flag gets no toast, and a flagged Task in Ready to land gets none either. A Task toasts again only after it leaves that state and enters it again. The view that the Board opens on makes no toast. A review Task never toasts.

## PROD-BOARD-020: A free slot names the next Issue

When a build Task reaches Done and the started build Tasks are fewer than the WIP cap, the Board sends one more toast that names the first Up next row, for example `1 slot free, next ENG-611: Move the retry job`. One Board update sends at most one such toast. With no Up next row, the Board sends none. The Board does not start the Issue, so the user presses `s` to start it.

## PROD-BOARD-021: The Board shows its cache when the Scheduler is offline

While the Board cannot reach the Scheduler, it shows the view that the Scheduler last saved, and the header shows `scheduler offline, showing cache: run lineup restart` in pink. The Board tries to connect again every 2 s. When it connects, the alert leaves and the live view replaces the cache.

## PROD-BOARD-022: A Task names its Repo by the Repo's folder

The Repo of a Task on the Board, in `lineup ls`, and in `lineup show` is the folder name of the Repo, for example `data`. If two Repos of Tasks on the Board share a folder name, each name adds the parent folder, for example `acme-data`.

## PROD-BOARD-023: Each column fits the rows on screen

Each column is as wide as its widest cell among the rows on screen, so a row out of view does not widen it. The column of the age or the priority, the Repo column, and the skill column each show only when at least half the rows on screen fill them. The title takes the rest of the width.

## PROD-BOARD-024: An open Team shows at most 10 rows

An open Team shows its first 10 rows. When it holds more, a dim line follows them, for example `+ 90 more rows`. The user can select that line, and the strip then shows Team and `90 more rows`. `enter` or a tap on the line shows every row of Team for this run and selects the first row that was hidden. Closing Team brings the limit back. While the filter holds text, Team shows every match.

## PROD-BOARD-025: The Team heading marks the fetch cap

When the Board reads 100 Issues of one team, the Team heading adds `+` to its count, for example `Team 100+`. The strip on that heading shows the same count, for example `100+ rows`.
