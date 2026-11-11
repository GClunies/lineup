# lineup

## Merging

- A PR merges only after the CI suite (the `test` job) passes on it. The ruleset "CI must pass" requires `test` on `main`. For a stack, push each parent branch first, then add it to that ruleset before its child PR opens. The ruleset rejects a direct push of an unchecked commit, so a branch that is already in it cannot receive its first push. Remove a branch from the ruleset after its stack lands.
- The Auto-merge workflow arms auto-merge with the `LINEUP_PAT` secret, a fine-grained token of the repo owner with Contents and Pull requests write on this repo. The merge is credited to the owner, so GitHub deletes the merged branch at once. If the token expires, every new PR's `enable` job fails with that message. Create a new token and run `gh secret set LINEUP_PAT`.
- Before you open a PR, run the CI suite locally: `test -z "$(gofmt -l .)"`, `go vet ./...`, and `go test -race -count=1 ./...`.
- If a change depends on another open change, put both in one stack with `gh stack`, based on the branch they merge into. Land a stack with `gh stack merge <top PR> --yes`. Never merge a stack child into its parent branch by itself.
- Open each PR ready for review. The auto-merge workflow arms auto-merge when the PR's base requires `test` and the PR is in no stack. GitHub has no auto-merge for a stacked PR.
