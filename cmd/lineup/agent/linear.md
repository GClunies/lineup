
## Linear

Use the `linear` CLI. Run `linear auth list` to see the workspaces. The default workspace has a `*`. For any other workspace, put `--workspace <slug>` at the end of the command. Run `linear <command> --help` for the flags.

| Job | User says | Command |
|---|---|---|
| Read | "show ENG-12" | `linear issue view ENG-12` |
| Read | "what is in the Billing Project?" | `linear project view <project>`, then `linear issue query` |
| Read | "what is on my plate?" | `linear issue mine` |
| Split | "split this Project into Issues" | `linear issue create`, one Issue for each deliverable |
| Criteria | "write acceptance criteria for ENG-12" | `linear issue update ENG-12`, with the criteria in the description |
| Order | "ENG-13 needs ENG-12 first" | `linear issue relation add ENG-12 blocks ENG-13` |
| Link | "ENG-13 touches the same file as ENG-12" | `linear issue relation add ENG-13 related ENG-12` |
| Group | "put these in milestone M2" | `linear issue update <issue>`, `linear milestone` |
| Discuss | "leave a note on ENG-12" | `linear issue comment add ENG-12` |

- Name the one Repo of each Issue with a line `Repo: <owner>/<repo>` in its description, for example `Repo: crexi/data`. When the Issue's team is on more than one Repo, lineup reads this line to pick the Repo.
- If work spans Repos, split it into one Issue for each Repo. Join the Issues with `blocks` relations, in the order of the work.
- Record each link as a relation with `linear issue relation add <issue> <kind> <other>`:
  - `blocked-by`: `linear issue relation add ENG-13 blocked-by ENG-12` records that ENG-12 blocks ENG-13.
  - `blocks`: `linear issue relation add ENG-12 blocks ENG-13` records the same link from ENG-12.
  - `related`: `linear issue relation add ENG-13 related ENG-12` records that both change one file, with no block. It is the `Related:` link.
  - Write the reason of each link on its own line in the description of the blocked Issue, or of the new Issue for a `related` link, for example `Blocked by ENG-12, overlap: both rewrite openHub`.
  - A relation names only Linear Issues. For a PR or a Task that no Issue tracks, write a line `Related: <PR URL or Task ID>, <reason>` in the description.
- Never run `linear issue start` or `linear issue pull-request`.
- You cannot delete Issues. If an Issue must go, tell the user to cancel or delete it in Linear.
