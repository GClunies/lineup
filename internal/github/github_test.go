package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// fakeGH writes a gh stand-in that logs one line of arguments per call, joined
// by "|". A graphql call prints settings when set and reply otherwise.
func fakeGH(t *testing.T, reply, settings string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "gh"), filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
printf '%s\n' "$(IFS='|'; echo "$*" | tr '\n' ' ')" >> "` + log + `"
case "$2" in
graphql) if [ -n "` + settings + `" ]; then echo "` + settings + `"; else cat "` + reply + `"; fi ;;
user) echo "octo" ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

var anyKey = Key{Repo: "o/r", Branch: "b"}

func load(t *testing.T, fixture string) *PR {
	t.Helper()
	bin, _ := fakeGH(t, filepath.Join("testdata", fixture+".json"), "")
	prs, err := (&Client{Bin: bin}).Snapshot(context.Background(), []Key{anyKey})
	if err != nil {
		t.Fatal(err)
	}
	pr := prs[anyKey]
	if pr == nil {
		t.Fatalf("%s: no PR parsed", fixture)
	}
	return pr
}

func clone(pr *PR) *PR {
	c := *pr
	c.LatestReviews = append([]Review(nil), pr.LatestReviews...)
	c.CommitAuthors = append([]string(nil), pr.CommitAuthors...)
	return &c
}

func sigs(s ...lifecycle.Signal) []lifecycle.Signal { return s }

func check(t *testing.T, got, want []lifecycle.Signal) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestParseEachState(t *testing.T) {
	cases := []struct {
		fixture string
		want    PR // the fields that identify the state
	}{
		{"draft", PR{Number: 14783, State: "OPEN", IsDraft: true, Checks: ChecksPassed, ReviewDecision: "REVIEW_REQUIRED", CommitCount: 4}},
		{"ci_failed", PR{Number: 13788, State: "OPEN", Checks: ChecksFailed, ReviewDecision: "REVIEW_REQUIRED", ReviewRequests: 2, CommitCount: 1}},
		{"pending", PR{Number: 344637, State: "OPEN", Checks: ChecksPending, ReviewDecision: "REVIEW_REQUIRED", CommitCount: 3}},
		{"pending_review_required", PR{Number: 14845, State: "OPEN", Checks: ChecksPassed, ReviewDecision: "REVIEW_REQUIRED", ReviewRequests: 2, CommitCount: 3}},
		{"ci_passed_approved", PR{Number: 14825, State: "OPEN", Checks: ChecksPassed, ReviewDecision: "APPROVED", CommitCount: 1}},
		{"approved_review_requested", PR{Number: 14716, State: "OPEN", Checks: ChecksPassed, ReviewDecision: "APPROVED", ReviewRequests: 1, CommitCount: 1}},
		{"changes_requested_dirty", PR{Number: 13982, State: "OPEN", Checks: ChecksNone, ReviewDecision: "CHANGES_REQUESTED", MergeStateStatus: "DIRTY", CommitCount: 1}},
		{"merged", PR{Number: 14840, State: "MERGED", Checks: ChecksPassed, ReviewDecision: "APPROVED", CommitCount: 1}},
		{"multi_author", PR{Number: 389, State: "OPEN", Checks: ChecksPassed, CommitCount: 4}},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			got := load(t, c.fixture)
			if got.Number != c.want.Number || got.State != c.want.State || got.IsDraft != c.want.IsDraft ||
				got.Checks != c.want.Checks || got.ReviewDecision != c.want.ReviewDecision ||
				got.ReviewRequests != c.want.ReviewRequests || got.CommitCount != c.want.CommitCount ||
				(c.want.MergeStateStatus != "" && got.MergeStateStatus != c.want.MergeStateStatus) {
				t.Errorf("got %+v, want fields of %+v", got, c.want)
			}
			if got.HeadSHA == "" || got.URL == "" || len(got.CommitAuthors) == 0 {
				t.Errorf("missing head, url, or commit authors: %+v", got)
			}
		})
	}
}

func TestFirstSightingOfEachState(t *testing.T) {
	cases := []struct {
		fixture string
		want    []lifecycle.Signal
	}{
		{"draft", sigs(lifecycle.PROpen{}, lifecycle.CIPassed{Draft: true, NeedsReview: true})},
		{"ci_failed", sigs(lifecycle.PROpen{}, lifecycle.CIFailed{})},
		{"pending", sigs(lifecycle.PROpen{})},
		{"pending_review_required", sigs(lifecycle.PROpen{}, lifecycle.CIPassed{NeedsReview: true})},
		{"ci_passed_approved", sigs(lifecycle.PROpen{}, lifecycle.CIPassed{})},
		{"approved_review_requested", sigs(lifecycle.PROpen{}, lifecycle.CIPassed{})},
		{"changes_requested_dirty", sigs(lifecycle.PROpen{}, lifecycle.CIPassed{NeedsReview: true})},
		{"multi_author", sigs(lifecycle.PROpen{}, lifecycle.CIPassed{})},
		{"merged", sigs(lifecycle.PRMerged{})},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			check(t, Signals(nil, load(t, c.fixture), "octo"), c.want)
		})
	}
}

func TestNothingChangedNothingSignaled(t *testing.T) {
	for _, f := range []string{"draft", "ci_failed", "pending", "ci_passed_approved", "changes_requested_dirty", "merged", "multi_author"} {
		pr := load(t, f)
		check(t, Signals(clone(pr), clone(pr), "octo"), nil)
	}
	check(t, Signals(load(t, "draft"), nil, "octo"), nil)
}

func TestChecksEdges(t *testing.T) {
	pending := load(t, "pending")
	passed, failed := clone(pending), clone(pending)
	passed.Checks, failed.Checks = ChecksPassed, ChecksFailed

	check(t, Signals(pending, passed, "v"), sigs(lifecycle.CIPassed{NeedsReview: true}))
	check(t, Signals(pending, failed, "v"), sigs(lifecycle.CIFailed{}))
	check(t, Signals(failed, failed, "v"), nil)
	check(t, Signals(failed, pending, "v"), nil)

	newHead := clone(failed)
	newHead.HeadSHA = "new"
	check(t, Signals(failed, newHead, "v"), sigs(lifecycle.Pushed{}, lifecycle.CIFailed{}))

	newPassed := clone(passed)
	newPassed.HeadSHA = "new"
	check(t, Signals(passed, newPassed, "v"), sigs(lifecycle.Pushed{}, lifecycle.CIPassed{NeedsReview: true}))

	none := clone(passed)
	none.Checks = ChecksNone
	check(t, Signals(pending, none, "v"), sigs(lifecycle.CIPassed{NeedsReview: true}))
	check(t, Signals(none, passed, "v"), nil)
}

func TestPushWhilePending(t *testing.T) {
	pending := load(t, "pending")
	pushed := clone(pending)
	pushed.HeadSHA = "new"
	check(t, Signals(pending, pushed, "v"), sigs(lifecycle.Pushed{}))
}

func TestDraftBecomingReadyRepeatsCIPassed(t *testing.T) {
	ready := load(t, "pending_review_required")
	draft := clone(ready)
	draft.IsDraft = true
	check(t, Signals(draft, ready, "v"), sigs(lifecycle.CIPassed{NeedsReview: true}))
}

func TestApproval(t *testing.T) {
	cur := load(t, "ci_passed_approved")
	prev := clone(cur)
	prev.ReviewDecision = "REVIEW_REQUIRED"
	prev.LatestReviews = prev.LatestReviews[:1] // the approving review is the second
	check(t, Signals(prev, cur, "octo"), sigs(lifecycle.ReviewApproved{}))
}

func TestChangesRequestedAndConflict(t *testing.T) {
	cur := load(t, "changes_requested_dirty")
	prev := clone(cur)
	prev.ReviewDecision = "REVIEW_REQUIRED"
	prev.MergeStateStatus = "BLOCKED"
	prev.LatestReviews = prev.LatestReviews[:1] // the CHANGES_REQUESTED review is the second

	check(t, Signals(prev, cur, "octo"), sigs(lifecycle.ReviewChangesRequested{}, lifecycle.PRConflict{}))
	check(t, Signals(prev, cur, "BagToad"), sigs(lifecycle.PRConflict{}))
	check(t, Signals(cur, cur, "octo"), nil)
}

func TestCommentedReview(t *testing.T) {
	cur := load(t, "pending")
	prev := clone(cur)
	prev.LatestReviews = nil
	check(t, Signals(prev, cur, "octo"), sigs(lifecycle.ReviewCommented{}))
	check(t, Signals(prev, cur, "copilot-pull-request-reviewer"), nil)
}

func TestReviewerUpdatingTheirReviewIsNew(t *testing.T) {
	cur := load(t, "ci_passed_approved")
	cur.LatestReviews[0].SubmittedAt = cur.LatestReviews[1].SubmittedAt.Add(time.Hour)
	prev := clone(load(t, "ci_passed_approved"))
	check(t, Signals(prev, cur, "octo"), sigs(lifecycle.ReviewCommented{}))
}

func TestAdditionalAuthors(t *testing.T) {
	cur := load(t, "multi_author") // authors: dependabot[bot], Devender0077, kktejas07, kktejas07
	prev := clone(cur)
	prev.CommitCount = 2
	prev.CommitAuthors = cur.CommitAuthors[:2]

	check(t, Signals(prev, cur, "octo"), sigs(lifecycle.ReviewAdditionalAuthors{}))
	check(t, Signals(prev, cur, "kktejas07"), nil)

	prev.CommitCount = 3
	prev.CommitAuthors = cur.CommitAuthors[:3]
	check(t, Signals(prev, cur, "kktejas07"), nil)

	unlinked := clone(cur)
	unlinked.CommitAuthors[3] = ""
	check(t, Signals(prev, unlinked, "kktejas07"), nil)
}

func TestMerged(t *testing.T) {
	merged := load(t, "merged")
	open := clone(merged)
	open.State = "OPEN"
	check(t, Signals(open, merged, "v"), sigs(lifecycle.PRMerged{}))
	check(t, Signals(merged, merged, "v"), nil)

	closed := clone(open)
	closed.State = "CLOSED"
	check(t, Signals(open, closed, "v"), nil)

	// A merge brings other changes in the same poll. Only PRMerged matters.
	changed := clone(merged)
	changed.HeadSHA = "new"
	check(t, Signals(open, changed, "v"), sigs(lifecycle.PRMerged{}))
}

func TestSnapshotSendsOneQueryForManyKeys(t *testing.T) {
	bin, log := fakeGH(t, filepath.Join("testdata", "many.json"), "")
	keys := []Key{
		{Repo: "microsoft/vscode", Branch: "connor4312/ephemeral-hidden-sessions"},
		{Repo: "cli/cli", Branch: "release-download-verify"},
		{Repo: "cli/cli", Branch: "fix-percent-encoded-url"},
		{Repo: "cli/cli", Branch: "bagtoad/ghsa-submission-template"},
		{Repo: "cli/cli", Branch: "dependabot/github_actions/actions/create-github-app-token-3.3.0"},
		{Repo: "cli/cli", Branch: "o1/add-latest-pre-release-and-pin-flags-to-gh-extension-upgrade/nysoxynolqlo"},
		{Repo: "cli/cli", Branch: "no-such-branch-xyz"},
		{Repo: "kktejas07/CodeSpectra-Master", Branch: "dependabot/npm_and_yarn/frontend/radix-ui/react-hover-card-1.2.1"},
	}
	prs, err := (&Client{Bin: bin}).Snapshot(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}

	lines := calls(t, log)
	if len(lines) != 1 {
		t.Fatalf("want 1 gh call, got %d: %v", len(lines), lines)
	}
	args := strings.Split(lines[0], "|")
	if args[0] != "api" || args[1] != "graphql" {
		t.Fatalf("args %v", args)
	}
	joined := strings.Join(args, "\n")
	for i, k := range keys {
		owner, name, _ := strings.Cut(k.Repo, "/")
		for _, want := range []string{
			"a" + string(rune('0'+i)) + ": repository(",
			"o" + string(rune('0'+i)) + "=" + owner,
			"n" + string(rune('0'+i)) + "=" + name,
			"b" + string(rune('0'+i)) + "=" + k.Branch,
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("call lacks %q", want)
			}
		}
	}

	wantNumbers := map[Key]int{keys[0]: 344637, keys[1]: 14783, keys[2]: 13788, keys[3]: 14825, keys[4]: 14716, keys[5]: 13982, keys[7]: 389}
	if len(prs) != len(wantNumbers) {
		t.Errorf("got %d PRs, want %d", len(prs), len(wantNumbers))
	}
	for k, n := range wantNumbers {
		if prs[k] == nil || prs[k].Number != n {
			t.Errorf("%v: got %+v, want #%d", k, prs[k], n)
		}
	}
	if _, ok := prs[keys[6]]; ok {
		t.Error("a Key with no PR must be absent")
	}
}

func TestSnapshotWithNoKeysRunsNothing(t *testing.T) {
	bin, log := fakeGH(t, filepath.Join("testdata", "many.json"), "")
	prs, err := (&Client{Bin: bin}).Snapshot(context.Background(), nil)
	if err != nil || len(prs) != 0 {
		t.Fatalf("got %v, %v", prs, err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("gh ran")
	}
}

func TestMergeCommands(t *testing.T) {
	cases := []struct {
		settings string
		want     string
	}{
		{"true,true,true", "pr|merge|7|--repo|o/r|--squash"},
		{"false,true,true", "pr|merge|7|--repo|o/r|--merge"},
		{"false,false,true", "pr|merge|7|--repo|o/r|--rebase"},
	}
	for _, c := range cases {
		bin, log := fakeGH(t, filepath.Join("testdata", "many.json"), c.settings)
		if err := (&Client{Bin: bin}).Merge(context.Background(), "o/r", 7); err != nil {
			t.Fatal(err)
		}
		got := calls(t, log)
		if len(got) != 2 || got[1] != c.want {
			t.Errorf("settings %s: calls %v, want last %s", c.settings, got, c.want)
		}
	}

	bin, _ := fakeGH(t, "", "false,false,false")
	if err := (&Client{Bin: bin}).Merge(context.Background(), "o/r", 7); err == nil {
		t.Error("a repo with no merge method must fail")
	}
}

func TestViewer(t *testing.T) {
	bin, _ := fakeGH(t, "", "")
	got, err := (&Client{Bin: bin}).Viewer(context.Background())
	if err != nil || got != "octo" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestGHFailureCarriesStderr(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'not logged in' >&2\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (&Client{Bin: bin}).Viewer(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("got %v", err)
	}
}

func TestMergeRetriesWhenAnotherMergeMovedTheBase(t *testing.T) {
	mergeRetryWait = 0
	dir := t.TempDir()
	bin, log, tried := filepath.Join(dir, "gh"), filepath.Join(dir, "calls.log"), filepath.Join(dir, "tried")
	script := `#!/bin/sh
printf '%s\n' "$(IFS='|'; echo "$*")" >> "` + log + `"
case "$1 $2" in
"api graphql") echo "true,true,true" ;;
"pr merge") if [ ! -f "` + tried + `" ]; then touch "` + tried + `"; echo "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)" >&2; exit 1; fi ;;
esac
`
	os.WriteFile(bin, []byte(script), 0o755)
	if err := (&Client{Bin: bin}).Merge(context.Background(), "o/r", 7); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); len(got) != 3 || got[1] != got[2] {
		t.Fatalf("calls %q, want graphql then the merge twice", got)
	}
	bin, log = fakeGH(t, "", "true,true,true")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+log+"\ncase \"$1 $2\" in \"api graphql\") echo true,true,true ;; *) echo 'Pull request is not mergeable' >&2; exit 1 ;; esac\n"), 0o755)
	if err := (&Client{Bin: bin}).Merge(context.Background(), "o/r", 7); err == nil || len(calls(t, log)) != 2 {
		t.Fatalf("another error must not retry: %v %q", err, calls(t, log))
	}
}

// A PR whose checks GitHub has not created yet must not pass CI in a repo
// whose base branch has checks, or a merge on green lands it before CI runs.
func TestNoChecksYetIsPendingWhenTheBaseHasChecks(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "ci_passed_approved.json"))
	var d map[string]any
	json.Unmarshal(raw, &d)
	pr := d["data"].(map[string]any)["a0"].(map[string]any)["pullRequests"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	for _, n := range pr["head"].(map[string]any)["nodes"].([]any) {
		n.(map[string]any)["commit"].(map[string]any)["statusCheckRollup"] = nil
	}
	for base, want := range map[string]CheckState{`{"state":"SUCCESS"}`: ChecksPending, `null`: ChecksNone} {
		var rollup any
		json.Unmarshal([]byte(base), &rollup)
		pr["baseRef"] = map[string]any{"target": map[string]any{"statusCheckRollup": rollup}}
		out, _ := json.Marshal(d)
		path := filepath.Join(t.TempDir(), "snap.json")
		os.WriteFile(path, out, 0o644)
		bin, _ := fakeGH(t, path, "")
		prs, err := (&Client{Bin: bin}).Snapshot(context.Background(), []Key{anyKey})
		if err != nil {
			t.Fatal(err)
		}
		if got := prs[anyKey].Checks; got != want {
			t.Errorf("base rollup %s: checks %s, want %s", base, got, want)
		}
		if f := prs[anyKey].Facts("me"); f.ChecksDue != (want == ChecksPending) || len(f.Checks) != 0 {
			t.Errorf("base rollup %s: facts %+v, want checks due only when the base has checks", base, f)
		}
		if want == ChecksPending {
			for _, s := range Signals(nil, prs[anyKey], "me") {
				if _, ok := s.(lifecycle.CIPassed); ok {
					t.Error("CI passed before GitHub created the PR's checks")
				}
			}
		}
	}
}

var gatedKey = Key{Repo: "o/r", Branch: "b", Bot: "greptile"}

// review_bot.json is better-auth/better-auth#12303, where Greptile scored the
// head 4/5. Its comments come from vercel, Codex, pkg-pr-new, and Greptile.
func loadGated(t *testing.T) *PR {
	t.Helper()
	bin, _ := fakeGH(t, filepath.Join("testdata", "review_bot.json"), "")
	prs, err := (&Client{Bin: bin}).Snapshot(context.Background(), []Key{gatedKey})
	if err != nil {
		t.Fatal(err)
	}
	return prs[gatedKey]
}

const reviewedHead = "eb6b436954c2e1f46ec8adbdb0c21a12c4802d22"

func TestParseAReviewBotSummary(t *testing.T) {
	pr := loadGated(t)
	if pr.Bot != "greptile" || !reflect.DeepEqual(pr.BotSummary, &BotSummary{Score: 4, Commit: reviewedHead}) {
		t.Fatalf("bot %q summary %+v, want greptile 4 at %s", pr.Bot, pr.BotSummary, reviewedHead)
	}
	if ungated := load(t, "review_bot"); ungated.Bot != "" || ungated.BotSummary != nil {
		t.Errorf("a Key with no bot read a summary: %q %+v", ungated.Bot, ungated.BotSummary)
	}
}

func TestOnlyTheBotWritesItsScore(t *testing.T) {
	body := "<!-- greptile_confidence_score:5 -->\nReviews (1) · Last reviewed commit: [\"x\"](https://github.com/o/r/commit/" + reviewedHead + ")"
	bot := ReviewBots["greptile"]
	if s := bot.summary([]gqlComment{{Author: &gqlLogin{"octocat"}, Body: body}}); s != nil {
		t.Errorf("a person's comment gave the score %+v", s)
	}
	if s := bot.summary([]gqlComment{{Author: &gqlLogin{"greptile-apps-staging"}, Body: body}}); s == nil || s.Score != 5 {
		t.Errorf("the staging bot's comment gave %+v, want 5", s)
	}
}

// The comment bodies are the costly part of the query, so only a gated Key asks for them.
func TestOnlyAGatedKeyAsksForComments(t *testing.T) {
	bin, log := fakeGH(t, filepath.Join("testdata", "review_bot.json"), "")
	if _, err := (&Client{Bin: bin}).Snapshot(context.Background(), []Key{anyKey, gatedKey}); err != nil {
		t.Fatal(err)
	}
	query := calls(t, log)[0]
	a0, a1, _ := strings.Cut(query[strings.Index(query, "a0:"):], "a1:")
	if strings.Contains(a0, "comments(") || !strings.Contains(a1, "comments(first: 20)") {
		t.Errorf("query:\n%s", query)
	}
}

func TestReviewBotSignals(t *testing.T) {
	scoredAtHead := loadGated(t)
	four := 4
	check(t, Signals(nil, scoredAtHead, "octo"), sigs(lifecycle.PROpen{},
		lifecycle.CIPassed{NeedsReview: true, Bot: &lifecycle.HeadScore{Head: reviewedHead, Score: &four}}))

	pushed := clone(scoredAtHead)
	pushed.HeadSHA = "f00d"
	check(t, Signals(nil, pushed, "octo"), sigs(lifecycle.PROpen{},
		lifecycle.CIPassed{NeedsReview: true, Bot: &lifecycle.HeadScore{Head: "f00d"}}))

	// Greptile edits its summary a few seconds after its check run passes.
	waiting := clone(scoredAtHead)
	waiting.BotSummary = &BotSummary{Score: 5, Commit: "older"}
	check(t, Signals(waiting, scoredAtHead, "octo"), sigs(
		lifecycle.CIPassed{NeedsReview: true, Bot: &lifecycle.HeadScore{Head: reviewedHead, Score: &four}}))
	check(t, Signals(clone(scoredAtHead), clone(scoredAtHead), "octo"), nil)
}

// The gate owns a bot's reviews, so they start no Responding round of their
// own. With no gate, the bot is one more reviewer.
func TestAGatedBotsReviewIsNotReviewCommented(t *testing.T) {
	prev := loadGated(t)
	prev.LatestReviews = nil
	cur := clone(prev)
	cur.LatestReviews = []Review{{Author: "greptile-apps", State: "COMMENTED", SubmittedAt: time.Now()}}
	check(t, Signals(prev, cur, "octo"), nil)

	prev.Bot, cur.Bot = "", ""
	check(t, Signals(prev, cur, "octo"), sigs(lifecycle.ReviewCommented{}))
}
