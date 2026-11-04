
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
| Group | "put these in milestone M2" | `linear issue update <issue>`, `linear milestone` |
| Discuss | "leave a note on ENG-12" | `linear issue comment add ENG-12` |

- Never run `linear issue start` or `linear issue pull-request`.
- You cannot delete Issues. If an Issue must go, tell the user to cancel or delete it in Linear.
