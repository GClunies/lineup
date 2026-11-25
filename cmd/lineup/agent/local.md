
## Local

The Local Tracker keeps each Issue as one Markdown file in `{{issues}}`. The file `local-7.md` holds the Issue `LOCAL-7`:

```markdown
---
title: Retry failed loads before alerting
repo: data
status: todo
blocked_by: [LOCAL-3]
---

The description, in Markdown.
```

A Repo with a prefix names its Issues `<PREFIX>-<n>`, so the file `hod-3.md` holds the Issue `HOD-3`. An ID is in upper case, and a file name in lower case. The Repo prefixes section names each prefix.

| Field | Values | Required |
|---|---|---|
| `title` | Text | Yes |
| `repo` | A Repo name or path, as `lineup add --repo` takes | No |
| `status` | `backlog`, `todo`, `started`, `in review`, `done`, `canceled` | Yes |
| `blocked_by` | Local Issue IDs | No |

| Job | User says | What to do |
|---|---|---|
| Read | "show LOCAL-7" | Read `{{issues}}/local-7.md` |
| Read | "what is open?" | List `{{issues}}`, then read the files whose `status` is `backlog`, `todo`, `started`, or `in review` |
| Split | "split LOCAL-7 into Issues" | Write one new file for each deliverable, with `status: todo` |
| Criteria | "write acceptance criteria for LOCAL-7" | Edit the description below the closing `---` |
| Order | "LOCAL-8 needs LOCAL-7 first" | Add `LOCAL-7` to `blocked_by` in `local-8.md` |
| Link | "LOCAL-8 touches the same file as LOCAL-7" | Add the line `Related: LOCAL-7` to the description in `local-8.md` |
| Drop | "drop LOCAL-7" | Set `status: canceled` |

- To name a new file, run `lineup next-id <repo>`. It prints the next free ID of the Repo, for example `HOD-4`, or `LOCAL-8` for a Repo with no prefix. Name the file after the ID in lower case, for example `hod-4.md`. Never write over a file that exists.
- Keep each field on one line. Write `title` as plain text. If it contains a colon or a `#`, put it in double quotes.
- Record each link in the file of an Issue:
  - Blocked by: add the blocker's ID to `blocked_by`. `blocked_by: [LOCAL-7]` in `local-8.md` records that LOCAL-7 blocks LOCAL-8.
  - Blocks: to record that LOCAL-8 blocks LOCAL-9, add `LOCAL-8` to `blocked_by` in `local-9.md`.
  - Related: add the line `Related: LOCAL-7` to the description, for two Issues that change one file, with no block.
  - Write the reason of each link on its own line in the description of the same file, for example `Blocked by LOCAL-7, overlap: both rewrite openHub`.
  - `blocked_by` names only local Issue IDs. For a Linear or GitHub Issue, a PR, or a Task, write a line `Related: <ID, PR URL, or Task ID>, <reason>` in the description.
- Set `status` only to `backlog`, `todo`, or `canceled`.
