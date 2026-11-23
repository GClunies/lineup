# lineup

## Intent

- Before you change or judge lineup's behavior, read `docs/product-intent/README.md` and the relevant domain file. Before you change or judge lineup's internal design, read `docs/architecture-intent/README.md` and the relevant domain file.
- A conflict with a statement is a bug or a blocker. If the user states a different intent, change the statement to match it, in the same PR as the code.
- If no statement covers a behavior, ask the user. Do not treat the gap as a "no". Add the answer as a new statement.
- Write no ADR and no spec.

## Merging

- A PR merges only after the CI suite (the `test` job) passes on it. The ruleset "CI must pass" requires `test` on `main`. For a stack, push each parent branch first, then add it to that ruleset before its child PR opens. The ruleset rejects a direct push of an unchecked commit, so a branch that is already in it cannot receive its first push. Remove a branch from the ruleset after its stack lands.
- The Auto-merge workflow arms auto-merge with the `LINEUP_PAT` secret, a fine-grained token of the repo owner with Contents and Pull requests write on this repo. The merge is credited to the owner, so GitHub deletes the merged branch at once. If the token expires, every new PR's `enable` job fails with that message. Create a new token and run `gh secret set LINEUP_PAT`.
- Before you open a PR, run the CI suite locally: `test -z "$(gofmt -l .)"`, `go vet ./...`, and `go test -race -count=1 ./...`.
- If a change depends on another open change, put both in one stack with `gh stack`, based on the branch they merge into. Land a stack with `gh stack merge <top PR> --yes`. Never merge a stack child into its parent branch by itself.
- Open each PR ready for review. The auto-merge workflow arms auto-merge when the PR's base requires `test` and the PR is in no stack. GitHub has no auto-merge for a stacked PR.

## The running Scheduler

- launchd runs the `lineup` binary that `go install ./cmd/lineup` writes. That Scheduler runs old code until you rebuild it and run `lineup restart`. A restart keeps the tab of each Worker. It also reopens the Hub tab, so the Board runs the new build and the Orchestrator starts a new session.
- At the start of a session, and each time you see that a PR merged, compare the installed build with `main`:

  ```bash
  git fetch origin main
  built=$(go version -m "$(go env GOPATH)/bin/lineup" | awk '$2 ~ /^vcs.revision=/ {sub("vcs.revision=", "", $2); print $2}')
  git diff --stat "$built" origin/main -- . ':!*_test.go' ':!*.md' ':!docs'
  ```

- If the diff is not empty, or `go version -m` shows `vcs.modified=true`, update the Scheduler. In a checkout of `origin/main` with no local changes, run `go install ./cmd/lineup`, then `lineup restart`. Tell the user the old and the new commit.
