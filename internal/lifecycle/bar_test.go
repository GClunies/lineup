package lifecycle

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// userBar is the configuration's default bar.
var userBar = LandingBar{Checks: RequiredChecks, Threads: RepliedThreads, Approvals: RequiredApprovals}

func greptile(min int) *ReviewGate {
	return &ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: min, TopScore: 5}
}

var anyCheck = rapid.Custom(func(t *rapid.T) Check {
	return Check{
		Name:     rapid.SampledFrom([]string{"test", "lint", "enable"}).Draw(t, "name"),
		State:    rapid.SampledFrom([]CheckState{CheckPending, CheckPassed, CheckFailed}).Draw(t, "state"),
		Required: rapid.Bool().Draw(t, "required"),
	}
})

var anyThread = rapid.Custom(func(t *rapid.T) Thread {
	return Thread{Bot: rapid.Bool().Draw(t, "bot"), Mine: rapid.Bool().Draw(t, "mine"),
		Resolved: rapid.Bool().Draw(t, "resolved"), Replied: rapid.Bool().Draw(t, "replied")}
})

var anyScore = rapid.Custom(func(t *rapid.T) *int {
	if rapid.Bool().Draw(t, "scored") {
		return nil
	}
	n := rapid.IntRange(0, 5).Draw(t, "score")
	return &n
})

// anyFacts draws a PR as one poll can see it.
var anyFacts = rapid.Custom(func(t *rapid.T) PRFacts {
	return PRFacts{
		Head:      "h",
		Draft:     rapid.Bool().Draw(t, "draft"),
		Conflict:  rapid.Bool().Draw(t, "conflict"),
		Checks:    rapid.SliceOfN(anyCheck, 0, 3).Draw(t, "checks"),
		ChecksDue: rapid.Bool().Draw(t, "checks due"),
		Score:     anyScore.Draw(t, "score"),
		Threads:   rapid.SliceOfN(anyThread, 0, 3).Draw(t, "threads"),
		Decision:  rapid.SampledFrom([]Decision{DecisionNone, DecisionRequired, DecisionApproved, DecisionChanges}).Draw(t, "decision"),
		Requested: rapid.IntRange(0, 2).Draw(t, "requested"),
	}
})

// passingFacts draws a PR that meets every bar, so that runs reach Ready to
// land often.
var passingFacts = rapid.Custom(func(t *rapid.T) PRFacts {
	top := 5
	var checks []Check
	for _, name := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"test", "lint"}), 0, 2, rapid.ID[string]).Draw(t, "checks") {
		checks = append(checks, Check{Name: name, State: CheckPassed, Required: rapid.Bool().Draw(t, "required")})
	}
	var threads []Thread
	for range rapid.IntRange(0, 2).Draw(t, "threads") {
		threads = append(threads, Thread{Bot: rapid.Bool().Draw(t, "bot"), Mine: true, Resolved: true})
	}
	return PRFacts{Head: "h", Checks: checks, Score: &top, Threads: threads,
		Decision: rapid.SampledFrom([]Decision{DecisionNone, DecisionApproved}).Draw(t, "decision")}
})

var anyBar = rapid.Custom(func(t *rapid.T) LandingBar {
	b := LandingBar{
		Checks:    rapid.SampledFrom([]CheckRule{AllChecks, RequiredChecks}).Draw(t, "check rule"),
		Threads:   rapid.SampledFrom([]ThreadRule{IgnoredThreads, ResolvedThreads, RepliedThreads}).Draw(t, "thread rule"),
		Approvals: rapid.SampledFrom([]ApprovalRule{RequestedApprovals, RequiredApprovals}).Draw(t, "approval rule"),
	}
	if rapid.Bool().Draw(t, "gated") {
		b.Bot = greptile(rapid.IntRange(1, 5).Draw(t, "min score"))
	}
	return b
})

var allGaps = []Gap{GapConflict, GapFailed, GapPending, GapDraft, GapScoreDue, GapScoreLow, GapThreads, GapApproval}

// plainGaps is the bar written out plainly, apart from Measure, so that the
// properties do not check Measure against itself.
func plainGaps(b LandingBar, f PRFacts) map[Gap]bool {
	g := map[Gap]bool{GapConflict: f.Conflict, GapDraft: f.Draft, GapPending: f.ChecksDue}
	anyRequired := false
	for _, c := range f.Checks {
		anyRequired = anyRequired || c.Required
	}
	for _, c := range f.Checks {
		if gates(b, c, anyRequired) {
			g[GapFailed] = g[GapFailed] || c.State == CheckFailed
			g[GapPending] = g[GapPending] || c.State == CheckPending
		}
	}
	if b.Bot != nil {
		g[GapScoreDue] = f.Score == nil
		g[GapScoreLow] = f.Score != nil && *f.Score < b.Bot.MinScore
	}
	for _, t := range f.Threads {
		open := b.Threads == ResolvedThreads && !t.Resolved || b.Threads == RepliedThreads && (!t.Resolved || !t.Mine && !t.Replied)
		g[GapThreads] = g[GapThreads] || open
	}
	g[GapApproval] = f.Decision == DecisionRequired || f.Decision == DecisionChanges ||
		b.Approvals == RequestedApprovals && f.Decision != DecisionApproved && f.Requested > 0
	return g
}

func meets(b LandingBar, f PRFacts) bool {
	for _, has := range plainGaps(b, f) {
		if has {
			return false
		}
	}
	return true
}

// gates is whether a check keeps the PR from landing when it fails.
func gates(b LandingBar, c Check, anyRequired bool) bool {
	return b.Checks == AllChecks || c.Required || !anyRequired
}

// stricter reports whether a holds everything that b holds, key by key.
func stricter(a, b LandingBar) bool {
	checks := a.Checks == AllChecks || b.Checks == RequiredChecks
	threads := a.Threads >= b.Threads
	approvals := a.Approvals == RequestedApprovals || b.Approvals == RequiredApprovals
	bot := b.Bot == nil || a.Bot != nil && a.Bot.MinScore >= b.Bot.MinScore
	return checks && threads && approvals && bot
}

func TestMeasureAgreesWithThePlainBar(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, f := anyBar.Draw(t, "bar"), anyFacts.Draw(t, "facts")
		s, plain := b.Measure(f), plainGaps(b, f)
		for _, g := range allGaps {
			if s.Has(g) != plain[g] {
				t.Fatalf("gap %d: Measure %v, plain bar %v, standing %+v", g, s.Has(g), plain[g], s)
			}
		}
		if s.Met() != meets(b, f) {
			t.Fatalf("Measure met %v, plain bar met %v", s.Met(), meets(b, f))
		}
	})
}

func TestALooserBarMeetsWhatAStricterBarMeets(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b, f := anyBar.Draw(t, "a"), anyBar.Draw(t, "b"), anyFacts.Draw(t, "facts")
		if stricter(a, b) && a.Measure(f).Met() && !b.Measure(f).Met() {
			t.Fatalf("%+v meets %+v and the looser %+v does not: %+v", f, a, b, b.Measure(f))
		}
	})
}

func TestPassingFactsMeetEveryBar(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b, f := anyBar.Draw(t, "bar"), passingFacts.Draw(t, "facts")
		if !b.Measure(f).Met() {
			t.Fatalf("%+v misses %+v: %+v", f, b, b.Measure(f))
		}
	})
}

func TestOnlyGatingChecksFail(t *testing.T) {
	f := PRFacts{Checks: []Check{
		{Name: "test", State: CheckFailed, Required: true},
		{Name: "lint", State: CheckFailed},
		{Name: "enable", State: CheckPending},
	}}
	s := userBar.Measure(f)
	if !slices.Equal(s.Failed, []string{"test"}) || s.Pending != 0 || !slices.Equal(s.Optional, []string{"lint"}) {
		t.Errorf("required: %+v, want test failed, lint optional, and enable not counted", s)
	}
	s = LandingBar{}.Measure(f)
	if !slices.Equal(s.Failed, []string{"test", "lint"}) || s.Pending != 1 || s.Optional != nil {
		t.Errorf("all: %+v, want test and lint failed and enable pending", s)
	}

	// With no required check, every check gates.
	s = userBar.Measure(PRFacts{Checks: []Check{{Name: "lint", State: CheckFailed}}})
	if !slices.Equal(s.Failed, []string{"lint"}) {
		t.Errorf("no required check: %+v, want lint failed", s)
	}
}

func TestEachThreadRule(t *testing.T) {
	f := PRFacts{Threads: []Thread{
		{Bot: true, Resolved: true, Replied: true},
		{Resolved: true},
		{Resolved: true, Mine: true},
		{Bot: true},
	}}
	cases := map[ThreadRule]OpenThreads{
		IgnoredThreads:  {},
		ResolvedThreads: {Bot: 1},
		RepliedThreads:  {Bot: 1, People: 1},
	}
	for rule, want := range cases {
		if got := (LandingBar{Threads: rule}).Measure(f).Threads; got != want {
			t.Errorf("%s: open %+v, want %+v", rule, got, want)
		}
	}
}

func TestEachApprovalRule(t *testing.T) {
	cases := []struct {
		decision   Decision
		requested  int
		unapproved map[ApprovalRule]bool
	}{
		{DecisionNone, 0, map[ApprovalRule]bool{RequiredApprovals: false, RequestedApprovals: false}},
		{DecisionNone, 1, map[ApprovalRule]bool{RequiredApprovals: false, RequestedApprovals: true}},
		{DecisionApproved, 1, map[ApprovalRule]bool{RequiredApprovals: false, RequestedApprovals: false}},
		{DecisionRequired, 0, map[ApprovalRule]bool{RequiredApprovals: true, RequestedApprovals: true}},
		{DecisionChanges, 0, map[ApprovalRule]bool{RequiredApprovals: true, RequestedApprovals: true}},
	}
	for _, c := range cases {
		for rule, want := range c.unapproved {
			if got := (LandingBar{Approvals: rule}).Measure(PRFacts{Decision: c.decision, Requested: c.requested}).Unapproved; got != want {
				t.Errorf("%s with decision %d and %d requests: unapproved %v, want %v", rule, c.decision, c.requested, got, want)
			}
		}
	}
}

func TestTheGapsComeInOrder(t *testing.T) {
	low := 3
	all := Standing{Conflict: true, Failed: []string{"test"}, Pending: 2, Draft: true, ScoreLow: true, Score: &low,
		Threads: OpenThreads{Bot: 1, People: 1}, Unapproved: true, Review: DecisionChanges}
	b := userBar
	b.Bot = greptile(5)
	want := []struct {
		gap  Gap
		text string
	}{
		{GapConflict, "conflict"},
		{GapFailed, "test failed"},
		{GapPending, "2 checks pending"},
		{GapDraft, "draft"},
		{GapScoreLow, "Greptile 3/5"},
		{GapThreads, "2 open threads (1 bot)"},
		{GapApproval, "changes requested"},
	}
	clear := map[Gap]func(*Standing){
		GapConflict: func(s *Standing) { s.Conflict = false },
		GapFailed:   func(s *Standing) { s.Failed = nil },
		GapPending:  func(s *Standing) { s.Pending = 0 },
		GapDraft:    func(s *Standing) { s.Draft = false },
		GapScoreLow: func(s *Standing) { s.ScoreLow = false },
		GapThreads:  func(s *Standing) { s.Threads = OpenThreads{} },
		GapApproval: func(s *Standing) { s.Unapproved = false },
	}
	s := all
	for _, w := range want {
		if s.Next() != w.gap || b.Explain(s) != w.text {
			t.Fatalf("next %d %q, want %d %q", s.Next(), b.Explain(s), w.gap, w.text)
		}
		clear[w.gap](&s)
	}
	if !s.Met() || b.Explain(s) != "" {
		t.Fatalf("all gaps closed: next %d %q", s.Next(), b.Explain(s))
	}
	due := Standing{ScoreDue: true}
	if due.Next() != GapScoreDue || b.Explain(due) != "waiting for Greptile" {
		t.Errorf("score due: %d %q", due.Next(), b.Explain(due))
	}
}

func TestTheChecklistShowsWhatTheBarHolds(t *testing.T) {
	five := 5
	b := userBar
	b.Bot = greptile(5)
	s := b.Measure(PRFacts{Score: &five, Decision: DecisionApproved, Threads: []Thread{{Bot: true}},
		Checks: []Check{{Name: "test", State: CheckPassed, Required: true}, {Name: "lint", State: CheckFailed}}})
	got := b.Checklist(s)
	want := []Item{
		{NoGap, "required checks passed"},
		{NoGap, "Greptile 5/5"},
		{GapThreads, "1 open thread (1 bot)"},
		{NoGap, "required reviews approved"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("checklist %+v, want %+v", got, want)
	}
	if got := (LandingBar{}).Checklist(Standing{Conflict: true}); !reflect.DeepEqual(got, []Item{{GapConflict, "conflict"}, {NoGap, "checks passed"}, {NoGap, "no review required"}}) {
		t.Errorf("the zero bar's checklist %+v", got)
	}
}

func TestARuleSavesByNameAndRefusesAnUnknownName(t *testing.T) {
	raw, err := json.Marshal(userBar)
	if err != nil || string(raw) != `{"checks":"required","threads":"replied","approvals":"required"}` {
		t.Fatalf("saved %s, %v", raw, err)
	}
	var back LandingBar
	if err := json.Unmarshal(raw, &back); err != nil || back != userBar {
		t.Fatalf("loaded %+v, %v", back, err)
	}
	err = json.Unmarshal([]byte(`{"threads":"any"}`), &back)
	if err == nil || !strings.Contains(err.Error(), `"any" is not one of ignored, resolved, replied`) {
		t.Fatalf("an unknown rule: %v", err)
	}
}
