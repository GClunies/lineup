package scheduler

import (
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

func TestTheNoteSaysWhatHappensAndWhereTheReviewStands(t *testing.T) {
	bar := lifecycle.LandingBar{Checks: lifecycle.RequiredChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals}
	pass := []lifecycle.Check{{Name: "test", State: lifecycle.CheckPassed, Required: true}}
	failing := []lifecycle.Check{{Name: "test", State: lifecycle.CheckFailed, Required: true}}
	pending := []lifecycle.Check{{Name: "test", State: lifecycle.CheckPending, Required: true}}
	facts := func(d lifecycle.Decision, reviewed bool, checks []lifecycle.Check, threads ...lifecycle.Thread) lifecycle.Standing {
		return bar.Measure(lifecycle.PRFacts{Head: testHead, Decision: d, Reviewed: reviewed, Checks: checks, Threads: threads})
	}
	build := func(stage lifecycle.Stage, s lifecycle.Standing) *Record {
		return &Record{Task: lifecycle.Task{ID: "ENG-1", Stage: stage, Standing: s, Policy: lifecycle.Policy{Bar: bar}}, PR: &PRSnapshot{Number: 42}}
	}
	review := func(stage lifecycle.Stage, outcome lifecycle.Outcome) *Record {
		return &Record{Task: lifecycle.Task{ID: "r-1", Kind: lifecycle.KindReview, Stage: stage, Outcome: outcome}, Review: &ReviewPR{Number: 7}}
	}
	moved := review(lifecycle.ReviewReady, "")
	moved.ReviewedHead, moved.LatestHead = "a1", "b2"
	flagged := build(lifecycle.InReview, facts(lifecycle.DecisionApproved, true, pass))
	flagged.Flag, flagged.FlagDetail = lifecycle.ReasonDrafts, "2 replies to approve"
	kept := review(lifecycle.Done, lifecycle.OutcomeReviewed)
	kept.WorktreeKept, kept.Worktree = true, "/tmp/wt"
	merged := func(worktree string, until time.Time) *Record {
		r := build(lifecycle.Done, facts(lifecycle.DecisionApproved, true, pass))
		r.Outcome, r.Worktree, r.KeepUntil = lifecycle.OutcomeMerged, worktree, until
		return r
	}
	nov14 := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	dirty := merged("/tmp/wt", nov14)
	dirty.WorktreeKept = true
	keptWithTab := merged("/tmp/wt", nov14)
	keptWithTab.WindowID = "window:1"

	for _, c := range []struct {
		name string
		r    *Record
		want string
	}{
		{"in review, no review yet", build(lifecycle.InReview, facts(lifecycle.DecisionRequired, false, pass)), "#42 ○ no review yet"},
		{"in review, commented", build(lifecycle.InReview, facts(lifecycle.DecisionRequired, true, pass)), "#42 ● commented"},
		{"in review, changes requested", build(lifecycle.InReview, facts(lifecycle.DecisionChanges, true, pass)), "#42 ✗ changes requested"},
		{"in review, approved but a thread open", build(lifecycle.InReview, facts(lifecycle.DecisionApproved, true, pass, lifecycle.Thread{})), "#42 ✓ approved · 1 open thread"},
		{"CI running", build(lifecycle.CIRunning, facts(lifecycle.DecisionRequired, false, pending)), "#42 ○ no review yet · 1 check pending"},
		{"CI debug", build(lifecycle.CIDebug, facts(lifecycle.DecisionApproved, true, failing)), "#42 ✓ approved · fixing CI"},
		{"responding", build(lifecycle.Responding, facts(lifecycle.DecisionChanges, true, pass)), "#42 ✗ changes requested · answering review"},
		{"ready to land", build(lifecycle.ReadyToLand, facts(lifecycle.DecisionApproved, true, pass)), "#42 ✓ approved · ready to land"},
		{"build done", func() *Record {
			r := build(lifecycle.Done, facts(lifecycle.DecisionApproved, true, pass))
			r.Outcome = lifecycle.OutcomeMerged
			return r
		}(), "#42 merged"},
		{"build done, worktree kept", merged("/tmp/wt", nov14), "#42 merged · worktree until Nov 14"},
		{"build done, worktree kept, with a tab", keptWithTab, "#42 merged"},
		{"build done, worktree removed", merged("", time.Time{}), "#42 merged"},
		{"build done, uncommitted changes", dirty, "#42 merged · worktree kept: uncommitted changes in /tmp/wt"},
		{"working with a PR", build(lifecycle.Working, facts(lifecycle.DecisionRequired, false, pass)), "#42 ○ no review yet"},
		{"working with a PR, approved", build(lifecycle.Working, facts(lifecycle.DecisionApproved, true, pass)), "#42 ✓ approved"},
		{"working with no PR", &Record{Task: lifecycle.Task{ID: "ENG-1", Stage: lifecycle.Working}}, ""},
		{"a flag keeps its reason and detail", flagged, "drafts: 2 replies to approve"},
		{"reviewing", review(lifecycle.Reviewing, ""), "#7 reviewing"},
		{"review ready", review(lifecycle.ReviewReady, ""), "#7 review ready"},
		{"review ready, head moved", moved, "#7 review ready · head moved"},
		{"review done, merged", review(lifecycle.Done, lifecycle.OutcomeMerged), "#7 merged"},
		{"review done, skipped", review(lifecycle.Done, lifecycle.OutcomeSkipped), "#7 skipped"},
		{"review done, worktree kept", kept, "#7 reviewed · worktree kept: /tmp/wt"},
	} {
		if got := c.r.view().Note; got != c.want {
			t.Errorf("%s: note %q, want %q", c.name, got, c.want)
		}
	}
}

// The Board reads the worktree note apart from the note, because a Follow-up
// row puts the Tracker state in the note's place.
func TestTheViewNamesTheWorktreeNoteApart(t *testing.T) {
	r := Record{Task: lifecycle.Task{Stage: lifecycle.Done, Outcome: lifecycle.OutcomeMerged}, Worktree: "/wt/a", WorktreeKept: true}
	if got, want := r.view().WorktreeNote, "worktree kept: uncommitted changes in /wt/a"; got != want {
		t.Fatalf("WorktreeNote = %q, want %q", got, want)
	}
	if r.Worktree, r.WorktreeKept = "", false; r.view().WorktreeNote != "" {
		t.Fatalf("WorktreeNote = %q for a Task that keeps no worktree", r.view().WorktreeNote)
	}
}
