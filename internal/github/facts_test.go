package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
	if got := userBar.Measure(pr.Facts("maxbeizer")).Threads; got != (lifecycle.OpenThreads{Bot: 3, People: 5}) {
		t.Errorf("open %+v, want one more open thread", got)
	}
	if got := bar.Measure(pr.Facts("maxbeizer")).Threads; got.Total() != 0 {
		t.Errorf("ignored: open %+v, want none", got)
	}
}
