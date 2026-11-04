# Every Tracker sits behind one port, and the Local Tracker lives outside the Repos

lineup supports Linear, GitHub Issues, and a Local Tracker of Markdown files. Each one is an adapter behind the Scheduler's existing `Tracker` port and behind one `Source` interface for the Tracker browser. A mux routes each call by the Issue's Kind. We chose this because the Scheduler already speaks only the port, and the browser's start, status, and create actions are the same for every Tracker. Separate browsers would build those actions three times.

## Considered Options

- **A separate browser for each kind of Tracker.** Rejected: it duplicates the start, status, and create actions. It also gives the `plan` tab one more choice to make before the user sees any work.
- **Local Issues in each Repo, in `.lineup/issues/`.** Rejected: planning files would enter the git history of work repositories that other people share. The Local Tracker uses one folder in `~/.local/share/lineup/issues/`, and each Issue names its Repo.
- **GitHub on whenever `gh` is signed in.** Rejected: lineup always needs `gh` for PRs, so GitHub would always be on, and the Local Tracker could never be the fallback. GitHub becomes a Tracker only after `lineup login github`.

## Consequences

- The Local Tracker is on only when no other Tracker is, or when `local_tracker = true`. A Task keeps its local Issue after the user signs in to Linear, because "on" decides only which tabs the browser shows.
- A local Issue ID is `local-<n>`, so a typed ID names exactly one Tracker. The browser sends each Issue's Kind, so it can still start an Issue from a Linear team keyed `LOCAL`.
