# lineup

## Merging

- A PR merges only after the CI suite (the `test` job) passes on it. The ruleset "CI must pass" requires `test` on `main` and `review-tab`. Add each new branch that receives merges to that ruleset before its first PR.
- Before you open a PR, run the CI suite locally: `test -z "$(gofmt -l .)"`, `go vet ./...`, and `go test -race -count=1 ./...`.
- If a change depends on another open change, put both in one stack with `gh stack`, based on the branch they merge into. Land a stack with `gh stack merge <top PR> --yes`. Never merge a stack child into its parent branch by itself.
- Open each PR ready for review. The auto-merge workflow arms auto-merge when the PR's base requires `test` and the PR is in no stack. GitHub has no auto-merge for a stacked PR.
