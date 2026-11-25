
## GitHub

Use the `gh` CLI. Pass each body on stdin with `--body-file -` and a quoted heredoc, so Markdown reaches GitHub unchanged.

| Job | User says | Command |
|---|---|---|
| Read | "show owner/repo#12" | `gh issue view 12 --repo owner/repo` |
| Read | "what is open in repo?" | `gh issue list --repo <owner>/<repo> --state open` |
| Read | "what is on my plate?" | `gh search issues --assignee @me --state open` |
| Split | "split #12 into Issues" | `gh issue create --repo <owner>/<repo> --title "<title>" --body-file -`, one Issue for each deliverable |
| Criteria | "write acceptance criteria for #12" | `gh issue edit 12 --repo <owner>/<repo> --body-file -`, with the whole new body |
| Order | "#13 needs #12 first" | Add the line `Blocked by #12` to the body of #13 with `gh issue edit 13 --repo <owner>/<repo> --body-file -` |
| Link | "#13 touches the same file as #12" | Add the line `Related: #12` to the body of #13 with `gh issue edit 13 --repo <owner>/<repo> --body-file -` |
| Discuss | "leave a note on #12" | `gh issue comment 12 --repo <owner>/<repo> --body-file -` |

- Read the body with `gh issue view` before you edit it. `--body-file` replaces the whole body.
- Record each link as a line in the body of an Issue:
  - `Blocked by #12`: #12 blocks this Issue. To record that this Issue blocks #12, add `Blocked by #<this Issue>` to the body of #12.
  - `Related: #12`: this Issue and #12 change one file, with no block.
  - Name an Issue of another Repo as `owner/repo#12`.
  - `#12` can name a PR. Link a PR that no Issue tracks, or the PR of a Task, by its number. A Task with no PR gets no link, so name its Task ID in the reason of a `Related:` line.
  - Put the reason after the link on the same line, for example `Blocked by #12, overlap: both rewrite openHub`. Keep `#` out of the reason, because lineup reads each `#<n>` on a `Blocked by` line as a blocker.
- Do not close, reopen, or delete Issues. If an Issue must go, tell the user to close it on GitHub.
