
## Local

The Local Tracker keeps each Issue as one Markdown file in `{{issues}}`. The file `local-7.md` holds the Issue `local-7`:

```markdown
---
title: Retry failed loads before alerting
repo: data
status: todo
blocked_by: [local-3]
---

The description, in Markdown.
```

| Field | Values | Required |
|---|---|---|
| `title` | Text | Yes |
| `repo` | A Repo name or path, as `lineup add --repo` takes | No |
| `status` | `todo`, `started`, `in review`, `done`, `canceled` | Yes |
| `blocked_by` | Local Issue IDs | No |

| Job | User says | What to do |
|---|---|---|
| Read | "show local-7" | Read `{{issues}}/local-7.md` |
| Read | "what is open?" | List `{{issues}}`, then read the files whose `status` is `todo`, `started`, or `in review` |
| Split | "split local-7 into Issues" | Write one new file for each deliverable, with `status: todo` |
| Criteria | "write acceptance criteria for local-7" | Edit the description below the closing `---` |
| Order | "local-8 needs local-7 first" | Add `local-7` to `blocked_by` in `local-8.md` |
| Drop | "drop local-7" | Set `status: canceled` |

- To name a new file, list `{{issues}}` and take one more than the highest number. Never write over a file that exists.
- Keep each field on one line. Write `title` as plain text. If it contains a colon or a `#`, put it in double quotes.
- Set `status` only to `todo` or `canceled`.
