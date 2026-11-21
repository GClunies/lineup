package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

var userBar = lifecycle.LandingBar{Checks: lifecycle.RequiredChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals}

// loadWith loads a fixture after edit changes its one PR.
func loadWith(t *testing.T, fixture string, edit func(pr map[string]any)) *PR {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	edit(d["data"].(map[string]any)["a0"].(map[string]any)["pullRequests"].(map[string]any)["nodes"].([]any)[0].(map[string]any))
	out, _ := json.Marshal(d)
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeGH(t, path, "")
	prs, err := (&Client{Bin: bin}).Snapshot(context.Background(), []Key{anyKey})
	if err != nil {
		t.Fatal(err)
	}
	return prs[anyKey]
}

func required(checks []lifecycle.Check) []string {
	var names []string
	for _, c := range checks {
		if c.Required {
			names = append(names, c.Name)
		}
	}
	return names
}

// classic_protection.json is cli/cli#13013. Branch protection on trunk
// requires the three build checks. govulncheck failed, and no rule requires it.
func TestClassicProtectionNamesTheRequiredChecks(t *testing.T) {
	f := load(t, "classic_protection").Facts("dsanders11")
	if got, want := required(f.Checks), []string{"build (ubuntu-latest)", "build (windows-latest)", "build (macos-latest)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required %q, want %q", got, want)
	}
	s := userBar.Measure(f)
	if s.Failed != nil || s.Pending != 0 || !reflect.DeepEqual(s.Optional, []string{"govulncheck"}) {
		t.Errorf("standing %+v, want only govulncheck failed, as optional", s)
	}
	if !s.Conflict || !s.Unapproved || s.Next() != lifecycle.GapConflict {
		t.Errorf("standing %+v, want a conflict first and no approval", s)
	}
	all := userBar
	all.Checks = lifecycle.AllChecks
	if s := all.Measure(f); !reflect.DeepEqual(s.Failed, []string{"govulncheck"}) || s.Optional != nil {
		t.Errorf("all checks: standing %+v, want govulncheck to gate", s)
	}
}

// ruleset.json is GClunies/lineup#9. A ruleset on review-tab requires test,
// and the branch has no classic protection.
func TestARulesetNamesTheRequiredChecks(t *testing.T) {
	f := load(t, "ruleset").Facts("GClunies")
	want := []lifecycle.Check{
		{Name: "enable", State: lifecycle.CheckPassed},
		{Name: "test", State: lifecycle.CheckPassed, Required: true},
	}
	if !reflect.DeepEqual(f.Checks, want) {
		t.Errorf("checks %+v, want %+v", f.Checks, want)
	}
	if s := userBar.Measure(f); !s.Met() {
		t.Errorf("standing %+v, want the bar met", s)
	}
}

func TestARequiredCheckNotReportedYetIsPending(t *testing.T) {
	pr := loadWith(t, "ruleset", func(pr map[string]any) {
		rollup := pr["head"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)["statusCheckRollup"].(map[string]any)
		contexts := rollup["contexts"].(map[string]any)
		contexts["nodes"] = contexts["nodes"].([]any)[:1]
	})
	f := pr.Facts("GClunies")
	want := []lifecycle.Check{
		{Name: "enable", State: lifecycle.CheckPassed},
		{Name: "test", State: lifecycle.CheckPending, Required: true},
	}
	if !reflect.DeepEqual(f.Checks, want) {
		t.Errorf("checks %+v, want %+v", f.Checks, want)
	}
	if s := userBar.Measure(f); s.Pending != 1 || s.Next() != lifecycle.GapPending {
		t.Errorf("standing %+v, want one check pending", s)
	}
}

func TestAnInactiveRulesetRequiresNothing(t *testing.T) {
	pr := loadWith(t, "ruleset", func(pr map[string]any) {
		rule := pr["baseRef"].(map[string]any)["rules"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
		rule["repositoryRuleset"] = map[string]any{"enforcement": "EVALUATE"}
	})
	if got := required(pr.Facts("GClunies").Checks); got != nil {
		t.Errorf("required %q, want none", got)
	}
}

// no_protection.json is cli/cli#14921, a draft on a branch with no
// protection. Every check gates, so the failed Windows build does.
func TestWithNoRequiredCheckEveryCheckGates(t *testing.T) {
	s := userBar.Measure(load(t, "no_protection").Facts("williammartin"))
	if !reflect.DeepEqual(s.Failed, []string{"build (windows-latest)"}) || s.Optional != nil || !s.Draft {
		t.Errorf("standing %+v, want the Windows build to gate a draft", s)
	}
}

// threads.json is cli/cli#12942 by maxbeizer. Copilot opened seven threads.
// maxbeizer replied to the four that are resolved. BagToad opened four
// threads, resolved two, and got no reply on any.
func TestEachThreadRuleCountsTheOpenThreads(t *testing.T) {
	pr := load(t, "threads")
	cases := []struct {
		viewer string
		rule   lifecycle.ThreadRule
		want   lifecycle.OpenThreads
	}{
		{"maxbeizer", lifecycle.RepliedThreads, lifecycle.OpenThreads{Bot: 3, People: 4}},
		{"maxbeizer", lifecycle.ResolvedThreads, lifecycle.OpenThreads{Bot: 3, People: 2}},
		{"maxbeizer", lifecycle.IgnoredThreads, lifecycle.OpenThreads{}},
		{"BagToad", lifecycle.RepliedThreads, lifecycle.OpenThreads{Bot: 7, People: 2}},
		{"", lifecycle.RepliedThreads, lifecycle.OpenThreads{Bot: 7, People: 4}},
	}
	for _, c := range cases {
		bar := userBar
		bar.Threads = c.rule
		if got := bar.Measure(pr.Facts(c.viewer)).Threads; got != c.want {
			t.Errorf("%q under %s: open %+v, want %+v", c.viewer, c.rule, got, c.want)
		}
	}
}

func TestAThreadPastTheReadWindowIsOpen(t *testing.T) {
	pr := loadWith(t, "threads", func(pr map[string]any) {
		pr["reviewThreads"].(map[string]any)["totalCount"] = 60
	})
	bar := userBar
	bar.Threads = lifecycle.IgnoredThreads
	if got := userBar.Measure(pr.Facts("maxbeizer")).Threads; got != (lifecycle.OpenThreads{Bot: 3, People: 4, Unread: true}) {
		t.Errorf("open %+v, want the read threads and unread ones", got)
	}
	if got := bar.Measure(pr.Facts("maxbeizer")).Threads; got != (lifecycle.OpenThreads{}) {
		t.Errorf("ignored: open %+v, want none", got)
	}
}

// changes_requested_dirty.json is cli/cli#13982: Copilot commented, then
// BagToad requested changes. The poll asks for each author's __typename.
func TestReviewFactsCountOnlyPeopleOtherThanTheUser(t *testing.T) {
	bot := func(pr map[string]any) {
		for _, n := range pr["latestReviews"].(map[string]any)["nodes"].([]any) {
			a := n.(map[string]any)["author"].(map[string]any)
			if a["login"] == "copilot-pull-request-reviewer" {
				a["__typename"] = "Bot"
			}
		}
	}
	for _, c := range []struct {
		fixture, viewer   string
		reviewed, changes bool
	}{
		{"changes_requested_dirty", "offbyone", true, true},
		{"changes_requested_dirty", "BagToad", false, true}, // GitHub's decision still requests changes
		{"draft", "mrecachinas", false, false},              // only Copilot reviewed
		{"ci_passed_approved", "BagToad", true, false},
	} {
		f := loadWith(t, c.fixture, bot).Facts(c.viewer)
		if f.Reviewed != c.reviewed || f.ChangesRequested != c.changes {
			t.Errorf("%s as %s: reviewed %v changes %v, want %v %v", c.fixture, c.viewer, f.Reviewed, f.ChangesRequested, c.reviewed, c.changes)
		}
	}
}

func at(stamp string) time.Time {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		panic(err)
	}
	return t
}

// feedback.json is cli/cli#12942, trimmed. maxbeizer wrote the PR, and
// BagToad and maxbeizer wrote 4 comments in its conversation. Of its 8
// reviews, Copilot's 2 come from a bot, 5 hold only thread replies with no
// body, and 1 is maxbeizer's with a body.
func TestFeedbackIsPeoplesCommentsOutsideThreads(t *testing.T) {
	pr := load(t, "feedback")
	if len(pr.Remarks) != 7 {
		t.Fatalf("remarks %+v, want the 4 comments and the 3 reviews with a body", pr.Remarks)
	}
	if got, want := pr.Facts("maxbeizer").Feedback, []time.Time{at("2026-03-25T18:01:25Z"), at("2026-07-02T19:51:20Z")}; !reflect.DeepEqual(got, want) {
		t.Errorf("feedback to maxbeizer %v, want BagToad's 2 comments", got)
	}
	if got := pr.Facts("BagToad").Feedback; len(got) != 3 || !got[2].Equal(at("2026-05-06T19:42:25Z")) {
		t.Errorf("feedback to BagToad %v, want maxbeizer's 2 comments and review body", got)
	}
}

// A hidden comment asks for nothing. A request for changes asks for a change
// with no body. A pending or a dismissed review asks for nothing.
func TestFeedbackSkipsHiddenCommentsAndKeepsRequestsForChanges(t *testing.T) {
	pr := loadWith(t, "feedback", func(pr map[string]any) {
		for _, n := range pr["remarks"].(map[string]any)["nodes"].([]any) {
			n.(map[string]any)["isMinimized"] = true
		}
		reviews := pr["reviews"].(map[string]any)["nodes"].([]any)
		reviews[1].(map[string]any)["state"] = "CHANGES_REQUESTED"
		reviews[6].(map[string]any)["state"] = "DISMISSED"
		reviews[2].(map[string]any)["state"] = "PENDING"
		reviews[2].(map[string]any)["body"] = "draft text"
	})
	if got, want := pr.Facts("BagToad").Feedback, []time.Time{at("2026-05-01T18:34:29Z")}; !reflect.DeepEqual(got, want) {
		t.Errorf("feedback %v, want only the request for changes", got)
	}
}

// A gated bot's comments are its own review, which the gate counts.
func TestAGatedBotWritesNoFeedback(t *testing.T) {
	pr := load(t, "feedback")
	pr.Remarks = append(pr.Remarks, Remark{Author: "greptile-apps", At: at("2026-11-01T00:00:00Z")})
	pr.Bot = "greptile"
	if got := pr.Facts("maxbeizer").Feedback; len(got) != 2 {
		t.Errorf("feedback %v, want BagToad's 2 comments only", got)
	}
}
