package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

var queueDay = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// queuePR is an open PR that wants the user's review. A larger age is older,
// so it ranks higher.
func queuePR(repo string, n, age int) github.PullRequest {
	return github.PullRequest{Repo: repo, Number: n, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, n),
		Title: "PR", Author: "hopper", Head: fmt.Sprintf("h%d", n), Base: "main",
		CreatedAt: queueDay.Add(-time.Duration(age) * time.Hour)}
}

func reviewRec(id, queue, repo string, stage lifecycle.Stage, pr github.PullRequest) *Record {
	return &Record{Task: lifecycle.Task{ID: id, Kind: lifecycle.KindReview, Queue: queue, Stage: stage}, Repo: repo,
		Review: &ReviewPR{Repo: pr.Repo, Number: pr.Number, URL: pr.URL, Head: pr.Head}}
}

func addsOf(p fillPlan) []string {
	var out []string
	for _, a := range p.Add {
		out = append(out, fmt.Sprintf("%s %s#%d in %s", a.Queue, a.PR.Repo, a.PR.Number, a.Repo.Repo))
	}
	return out
}

func TestPlanFill(t *testing.T) {
	a1, a2, b3, c4 := queuePR("acme/a", 1, 30), queuePR("acme/a", 2, 20), queuePR("acme/b", 3, 10), queuePR("acme/c", 4, 5)
	x9 := queuePR("acme/x", 9, 99)
	rereview := b3
	rereview.Viewer = github.ViewerState{Reviewed: true, Requested: true}
	pushed := a1
	pushed.Head = "h1-new"
	repos := map[string]queueRepo{"acme/a": {Repo: "/repos/a"}, "acme/b": {Repo: "/repos/b"}, "acme/c": {Repo: "/repos/c"}}
	q := func(slots int) []Queue { return []Queue{{Name: "q", Slots: slots}} }
	two := []Queue{{Name: "q1", Slots: 1}, {Name: "q2", Slots: 1}}
	roomy := []Queue{{Name: "q1", Slots: 5}, {Name: "q2", Slots: 5}}
	rec := func(id, queue, repo string, st lifecycle.Stage, pr github.PullRequest) *Record {
		return reviewRec(id, queue, repo, st, pr)
	}

	cases := []struct {
		name      string
		in        fillInput
		wantStart []string
		wantAdd   []string
	}{
		{"fills the free slots with the top-ranked PRs",
			fillInput{Queues: q(2), Found: map[string][]github.PullRequest{"q": {c4, b3, a1}}},
			nil, []string{"q acme/a#1 in /repos/a", "q acme/b#3 in /repos/b"}},
		{"starts at most one review in each Repo",
			fillInput{Queues: q(3), Found: map[string][]github.PullRequest{"q": {a1, a2, b3, c4}}},
			nil, []string{"q acme/a#1 in /repos/a", "q acme/b#3 in /repos/b", "q acme/c#4 in /repos/c"}},
		{"starts the Queued review Tasks first, and their Repo is busy",
			fillInput{Queues: q(2), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Queued, a2)},
				Found: map[string][]github.PullRequest{"q": {a1, b3}}},
			[]string{"R-1"}, []string{"q acme/b#3 in /repos/b"}},
		{"starts one Queued review Task in each Repo",
			fillInput{Queues: q(3), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Queued, a1), reviewRec("R-2", "q", "/repos/a", lifecycle.Queued, a2), reviewRec("R-3", "q", "/repos/b", lifecycle.Queued, b3)}},
			[]string{"R-1", "R-3"}, nil},
		{"starts the oldest Queued review Task first",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Queued, a1), reviewRec("R-2", "q", "/repos/b", lifecycle.Queued, b3)}},
			[]string{"R-1"}, nil},
		{"started reviews fill the slots",
			fillInput{Queues: q(2), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Reviewing, a1), reviewRec("R-2", "q", "/repos/b", lifecycle.ReviewReady, b3)},
				Found: map[string][]github.PullRequest{"q": {a1, b3, c4}}},
			nil, nil},
		{"a lowered slots count stops nothing and starts nothing",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Reviewing, a1), reviewRec("R-2", "q", "/repos/b", lifecycle.Reviewing, b3), reviewRec("R-3", "q", "/repos/c", lifecycle.Queued, c4)},
				Found: map[string][]github.PullRequest{"q": {a2}}},
			nil, nil},
		{"a Queued review Task waits for a free slot",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Reviewing, a1), reviewRec("R-2", "q", "/repos/b", lifecycle.Queued, b3)},
				Found: map[string][]github.PullRequest{"q": {c4}}},
			nil, nil},
		{"a pause fills nothing",
			fillInput{Paused: true, Queues: q(3), Records: []*Record{reviewRec("R-1", "q", "/repos/b", lifecycle.Queued, b3)},
				Found: map[string][]github.PullRequest{"q": {a1, c4}}},
			nil, nil},
		{"a queue that left the config fills nothing and takes no slot",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "old", "/repos/b", lifecycle.Queued, b3), reviewRec("R-2", "old", "/repos/c", lifecycle.Reviewing, c4)},
				Found: map[string][]github.PullRequest{"q": {a1}, "old": {a2}}},
			nil, []string{"q acme/a#1 in /repos/a"}},
		{"a PR with no [[repos]] checkout is skipped",
			fillInput{Queues: q(1), Found: map[string][]github.PullRequest{"q": {x9, a1}}},
			nil, []string{"q acme/a#1 in /repos/a"}},
		{"two queues that find one PR add it once",
			fillInput{Queues: two, Found: map[string][]github.PullRequest{"q1": {a1}, "q2": {a1, b3}}},
			nil, []string{"q1 acme/a#1 in /repos/a", "q2 acme/b#3 in /repos/b"}},
		{"two queues share the limit of one start in each Repo",
			fillInput{Queues: two, Found: map[string][]github.PullRequest{"q1": {a1}, "q2": {a2}}},
			nil, []string{"q1 acme/a#1 in /repos/a"}},
		{"a queue-1 PR is added above an older queue-2 PR",
			fillInput{Queues: []Queue{{Name: "q1", Slots: 1}, {Name: "q2", Slots: 1}}, Slots: 1,
				Found: map[string][]github.PullRequest{"q1": {c4}, "q2": {b3}}},
			nil, []string{"q1 acme/c#4 in /repos/c"}},
		{"a PR that both queues match ranks and is added in queue 1 only",
			fillInput{Queues: roomy, Found: map[string][]github.PullRequest{"q1": {a1}, "q2": {a1, b3}}},
			nil, []string{"q1 acme/a#1 in /repos/a", "q2 acme/b#3 in /repos/b"}},
		{"the shared slots cap the started reviews of all queues",
			fillInput{Queues: roomy, Slots: 2, Records: []*Record{rec("R-1", "q1", "/repos/a", lifecycle.Reviewing, a1)},
				Found: map[string][]github.PullRequest{"q1": {b3}, "q2": {c4}}},
			nil, []string{"q1 acme/b#3 in /repos/b"}},
		{"a review that the user asked for takes no shared slot",
			fillInput{Queues: roomy, Slots: 1, Records: []*Record{rec("R-1", "", "/repos/a", lifecycle.Reviewing, a1)},
				Found: map[string][]github.PullRequest{"q1": {b3}}},
			nil, []string{"q1 acme/b#3 in /repos/b"}},
		{"the shared slots also cap the Queued review Tasks that start",
			fillInput{Queues: roomy, Slots: 1, Records: []*Record{rec("R-1", "q1", "/repos/a", lifecycle.Queued, a1), rec("R-2", "q2", "/repos/b", lifecycle.Queued, b3)}},
			[]string{"R-1"}, nil},
		{"a full queue leaves a new queue-1 PR waiting",
			fillInput{Queues: roomy, Slots: 2, Records: []*Record{rec("R-1", "q2", "/repos/a", lifecycle.Reviewing, a1), rec("R-2", "q2", "/repos/b", lifecycle.Reviewing, b3)},
				Found: map[string][]github.PullRequest{"q1": {c4}, "q2": {x9}}},
			nil, nil},
		{"a freed shared slot goes to queue 1 before an older queue-2 PR",
			fillInput{Queues: roomy, Slots: 2, Records: []*Record{rec("R-1", "q2", "/repos/a", lifecycle.Reviewing, a1), rec("R-2", "q2", "/repos/b", lifecycle.Done, b3)},
				Found: map[string][]github.PullRequest{"q1": {c4}, "q2": {queuePR("acme/b", 7, 99)}}},
			nil, []string{"q1 acme/c#4 in /repos/c"}},
		{"a review that the user asked for keeps its PR out and takes no slot",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "", "/repos/a", lifecycle.Reviewing, a1)},
				Found: map[string][]github.PullRequest{"q": {a1, b3}}},
			nil, []string{"q acme/b#3 in /repos/b"}},
		{"a Done review at the PR's head keeps it out",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Done, a1)},
				Found: map[string][]github.PullRequest{"q": {a1}}},
			nil, nil},
		{"a Done review at an older head lets the PR back in",
			fillInput{Queues: q(1), Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Done, a1)},
				Found: map[string][]github.PullRequest{"q": {pushed}}},
			nil, []string{"q acme/a#1 in /repos/a"}},
		{"a re-review goes first, though another PR is older",
			fillInput{Queues: q(1), Found: map[string][]github.PullRequest{"q": {a1, rereview}}},
			nil, []string{"q acme/b#3 in /repos/b"}},
	}
	for _, c := range cases {
		c.in.Repos = repos
		p := planFill(c.in)
		if !slices.Equal(p.Start, c.wantStart) || !slices.Equal(addsOf(p), c.wantAdd) {
			t.Errorf("%s: start %v add %v, want start %v add %v", c.name, p.Start, addsOf(p), c.wantStart, c.wantAdd)
		}
	}
}

func TestPlanFillRanksEachQueueAlsoWhilePaused(t *testing.T) {
	a1, b3, x9 := queuePR("acme/a", 1, 30), queuePR("acme/b", 3, 10), queuePR("acme/x", 9, 99)
	p := planFill(fillInput{
		Paused: true, Queues: []Queue{{Name: "q", Slots: 3}},
		Records: []*Record{reviewRec("R-1", "q", "/repos/a", lifecycle.Reviewing, a1)},
		Found:   map[string][]github.PullRequest{"q": {b3, a1, x9}},
		Repos:   map[string]queueRepo{"acme/a": {Repo: "/repos/a"}, "acme/b": {Repo: "/repos/b"}},
	})
	var got []int
	for _, c := range p.Ranked["q"] {
		got = append(got, c.PR.Number)
	}
	if !slices.Equal(got, []int{3}) {
		t.Fatalf("ranked %v, want only #3: #1 has an open review, and #9 has no checkout", got)
	}
}

// queueHarness runs a Scheduler with the queues and no poll tick, so that a
// test runs each fill itself.
func queueHarness(t *testing.T, queues ...Queue) *harness {
	return newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		d.Queues, d.PollInterval = queues, time.Hour
	})
}

func authorQueue(name string, slots int, author string) Queue {
	return Queue{Name: name, Slots: slots, Filter: github.Filter{Base: github.QueuePRs, Repos: []string{"acme/data"}, Authors: []string{author}}}
}

// reviews lists the review Tasks on the Board as "<id> #<number> <stage>".
func (h *harness) reviews() []string {
	h.t.Helper()
	var out []string
	for _, v := range h.do(Request{Op: "ls"}).(BoardView).Tasks {
		if v.Kind == lifecycle.KindReview {
			out = append(out, fmt.Sprintf("%s #%d %s", v.ID, v.PRNumber, v.Stage))
		}
	}
	return out
}

func TestRepeatedFillsInOneStateAddEachReviewOnce(t *testing.T) {
	h := queueHarness(t, authorQueue("q1", 1, "hopper"), authorQueue("q2", 1, "ada"))
	p7, p8 := queuePR("acme/data", 7, 30), queuePR("acme/data", 8, 20)
	h.gh.found = map[string][]github.PullRequest{"q1": {p7, p8}, "q2": {p8, p7}}
	// q1 owns both PRs, so #8 waits for q1's one slot and never takes one of q2's.
	want := [][]string{
		{"pr-7 #7 Reviewing"},
		{"pr-7 #7 Reviewing"},
		{"pr-7 #7 Reviewing"},
		{"pr-7 #7 Reviewing"},
	}
	for i, w := range want {
		h.s.fill(context.Background())
		if got := h.reviews(); !slices.Equal(got, w) {
			t.Fatalf("after fill %d: %v, want %v", i+1, got, w)
		}
	}
	for i, ss := range h.gh.searchCalls() {
		if len(ss) != 2 || ss[0].Key != "q1" || ss[1].Key != "q2" || !strings.Contains(ss[0].Query, "author:hopper") {
			t.Fatalf("search %d = %+v, want both queues in one call", i+1, ss)
		}
	}
}

func TestTheTickFillsAQueueOnce(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		d.Queues = []Queue{authorQueue("q", 1, "hopper")}
	})
	h.gh.mu.Lock()
	h.gh.found = map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30), queuePR("acme/data", 8, 20)}}
	h.gh.mu.Unlock()
	eventually(t, "the tick's fill", func() bool { return len(h.reviews()) == 1 })
	after := len(h.gh.searchCalls())
	eventually(t, "five more fills", func() bool { return len(h.gh.searchCalls()) >= after+5 })
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("reviews %v, want one review of #7", got)
	}
	eventually(t, "the review worktree", func() bool { h.wt.mu.Lock(); defer h.wt.mu.Unlock(); return len(h.wt.added) == 1 })
	h.wt.mu.Lock()
	defer h.wt.mu.Unlock()
	if !slices.Equal(h.wt.added, []int{7}) {
		t.Fatalf("review worktrees for %v, want #7 once", h.wt.added)
	}
}

func TestAFillThatIsDueWhileAnotherRunsIsSkipped(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 3, "hopper"))
	hold := make(chan struct{})
	h.gh.found, h.gh.hold = map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30)}}, hold
	first := make(chan struct{})
	go func() { h.s.fill(context.Background()); close(first) }()
	eventually(t, "the first fill's search", func() bool { return len(h.gh.searchCalls()) == 1 })

	second := make(chan struct{})
	go func() { h.s.fill(context.Background()); close(second) }()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("the second fill waited for the first")
	}
	if n := len(h.gh.searchCalls()); n != 1 {
		t.Fatalf("%d searches, want the second fill skipped", n)
	}
	close(hold)
	<-first
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("reviews %v, want the first fill's review", got)
	}
	h.s.fill(context.Background())
	if n := len(h.gh.searchCalls()); n != 2 {
		t.Fatalf("%d searches, want a fill after the first one ends to run", n)
	}
}

func TestOnlyAFullPollFills(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	h.gh.found = map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30)}}
	h.s.poll(context.Background(), []string{"R-9"})
	if n, got := len(h.gh.searchCalls()), h.reviews(); n != 0 || len(got) != 0 {
		t.Fatalf("a poll for one Task searched %d times and added %v", n, got)
	}
	h.s.poll(context.Background(), nil)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("a full poll added %v, want pr-7", got)
	}
}

func TestAFillSkipsAPRWithNoCheckout(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	h.gh.found = map[string][]github.PullRequest{"q": {queuePR("acme/other", 9, 99), queuePR("acme/data", 7, 30)}}
	h.s.fill(context.Background())
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("reviews %v, want #7: acme/other has no checkout", got)
	}
	v := h.do(Request{Op: "reviews"}).(ReviewsView)
	if len(v.Skipped) != 1 || !strings.Contains(v.Skipped[0], "acme/other") {
		t.Fatalf("skipped %q, want acme/other named", v.Skipped)
	}
}

func TestAFillReadsTeamMembersOnceAnHour(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) {
		d.Queues, d.PollInterval = []Queue{{Name: "q", Slots: 1, Filter: github.Filter{Base: github.QueuePRs, AuthorTeams: []string{"acme/eng"}}}}, time.Hour
		d.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	h.gh.members = map[string][]string{"acme/eng": {"ada"}}
	for range 3 {
		h.s.fill(context.Background())
	}
	mu.Lock()
	now = now.Add(time.Hour)
	mu.Unlock()
	h.s.fill(context.Background())
	if h.gh.memberCalls != 2 {
		t.Fatalf("%d team reads in 4 fills over an hour, want 2", h.gh.memberCalls)
	}
	for _, ss := range h.gh.searchCalls() {
		if !strings.Contains(ss[0].Query, "author:ada") {
			t.Fatalf("query %q lacks the team's member", ss[0].Query)
		}
	}
}

func TestAPauseHoldsEveryFillAcrossARestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	opts := func(d *Deps) { d.Queues, d.PollInterval = []Queue{authorQueue("q", 1, "hopper")}, time.Hour }
	found := map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30), queuePR("acme/data", 8, 20)}}
	h := newHarness(t, db, nil, opts)
	h.gh.found = found
	ctx := context.Background()

	h.s.fill(ctx)
	v := h.do(Request{Op: "reviews"}).(ReviewsView)
	if q := v.Queues[0]; v.Paused || len(q.Started) != 1 || q.Started[0].ID != "pr-7" || len(q.Ranked) != 1 || q.Ranked[0].PR.Number != 8 {
		t.Fatalf("reviews = %+v, want pr-7 started and #8 waiting", v)
	}

	h.do(Request{Op: "reviews-pause"})
	if !h.do(Request{Op: "ls"}).(BoardView).ReviewsPaused {
		t.Fatal("the Board does not show the pause")
	}
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("after the pause: %v, want pr-7 still reviewing", got)
	}
	h.waitTab("claude reviewing")
	h.do(Request{Op: "close", Task: "pr-7"})
	h.waitStage("pr-7", lifecycle.Done)
	eventually(t, "pr-7's cleanup", func() bool { return len(h.reviewsRemoved()) == 1 })
	searched := len(h.gh.searchCalls())
	h.s.fill(ctx)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done"}) || len(h.gh.searchCalls()) != searched {
		t.Fatalf("a paused fill gave %v and searched %d times, want nothing", got, len(h.gh.searchCalls())-searched)
	}
	if v := h.do(Request{Op: "reviews"}).(ReviewsView); !v.Paused || len(v.Queues[0].Ranked) != 1 || v.Queues[0].Ranked[0].PR.Number != 8 {
		t.Fatalf("paused reviews = %+v, want #8 waiting", v)
	}

	h.cancel()
	h2 := newHarness(t, db, nil, opts)
	h2.gh.found = found
	if !h2.do(Request{Op: "ls"}).(BoardView).ReviewsPaused {
		t.Fatal("the pause did not hold across a restart")
	}
	h2.s.fill(ctx)
	if got := h2.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done"}) {
		t.Fatalf("a fill after the restart gave %v", got)
	}
	h2.do(Request{Op: "reviews-resume"})
	h2.s.fill(ctx)
	if got := h2.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done", "pr-8 #8 Reviewing"}) {
		t.Fatalf("after the resume: %v, want pr-8 for #8", got)
	}
	if h2.do(Request{Op: "ls"}).(BoardView).ReviewsPaused {
		t.Fatal("the Board still shows the pause")
	}
}
