package scheduler

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

func doneReview(id string, pr github.PullRequest, outcome lifecycle.Outcome) *Record {
	r := reviewRec(id, "q", "/repos/a", lifecycle.Done, pr)
	r.Outcome = outcome
	return r
}

func TestPlanFillSkips(t *testing.T) {
	a1, a2, b3 := queuePR("acme/a", 1, 30), queuePR("acme/a", 2, 20), queuePR("acme/b", 3, 10)
	repos := map[string]queueRepo{"acme/a": {Repo: "/repos/a"}, "acme/b": {Repo: "/repos/b"}}
	skip := func(prs ...github.PullRequest) []github.Skip {
		var out []github.Skip
		for _, p := range prs {
			out = append(out, github.Skip{URL: strings.ToUpper(p.URL)})
		}
		return out
	}
	pushed := a1
	pushed.Head = "h1-pushed"
	one := []Queue{{Name: "q", Slots: 1}}
	cases := []struct {
		name string
		in   fillInput
		want []string
	}{
		{"a skipped PR is not added, and the next PR takes its slot",
			fillInput{Queues: one, Skips: skip(a1), Found: map[string][]github.PullRequest{"q": {a1, b3}}},
			[]string{"q acme/b#3 in /repos/b"}},
		{"a skip holds at a new head",
			fillInput{Queues: one, Skips: skip(a1), Found: map[string][]github.PullRequest{"q": {pushed}}}, nil},
		{"a skip holds in every queue",
			fillInput{Queues: []Queue{{Name: "q1", Slots: 1}, {Name: "q2", Slots: 1}}, Skips: skip(a1),
				Found: map[string][]github.PullRequest{"q1": {a1}, "q2": {a1}}}, nil},
		{"a skip holds while the queues have free slots and no other PR",
			fillInput{Queues: []Queue{{Name: "q", Slots: 5}}, Skips: skip(a1, a2), Found: map[string][]github.PullRequest{"q": {a1, a2}}}, nil},
		{"a skip of one PR leaves the others",
			fillInput{Queues: []Queue{{Name: "q", Slots: 2}}, Skips: skip(a2), Found: map[string][]github.PullRequest{"q": {a1, a2, b3}}},
			[]string{"q acme/a#1 in /repos/a", "q acme/b#3 in /repos/b"}},
		{"a review that a skip ended holds the PR through the skip",
			fillInput{Queues: one, Skips: skip(a1), Records: []*Record{doneReview("pr-1", a1, lifecycle.OutcomeSkipped)},
				Found: map[string][]github.PullRequest{"q": {a1}}}, nil},
		{"a review that a skip ended lets the PR back in at the same head after the unskip",
			fillInput{Queues: one, Records: []*Record{doneReview("pr-1", a1, lifecycle.OutcomeSkipped)},
				Found: map[string][]github.PullRequest{"q": {a1}}},
			[]string{"q acme/a#1 in /repos/a"}},
		{"a review that the user closed holds the PR at the same head",
			fillInput{Queues: one, Records: []*Record{doneReview("pr-1", a1, lifecycle.OutcomeClosed)},
				Found: map[string][]github.PullRequest{"q": {a1}}}, nil},
	}
	for _, c := range cases {
		c.in.Repos = repos
		p := planFill(c.in)
		if got := addsOf(p); !slices.Equal(got, c.want) {
			t.Errorf("%s: adds %v, want %v", c.name, got, c.want)
		}
		for _, cs := range p.Ranked {
			for _, cand := range cs {
				if (github.Holds{Skips: c.in.Skips}).IsSkipped(cand.PR.URL) {
					t.Errorf("%s: ranked a skipped PR %s", c.name, cand.PR.URL)
				}
			}
		}
	}
}

func TestPropertyPlanFillNeverRanksOrAddsASkippedPR(t *testing.T) {
	pool := []github.PullRequest{queuePR("acme/a", 1, 30), queuePR("acme/a", 2, 20), queuePR("acme/b", 3, 10), queuePR("acme/c", 4, 5)}
	repos := map[string]queueRepo{"acme/a": {Repo: "/repos/a"}, "acme/b": {Repo: "/repos/b"}, "acme/c": {Repo: "/repos/c"}}
	outcomes := []lifecycle.Outcome{lifecycle.OutcomeClosed, lifecycle.OutcomeSkipped, lifecycle.OutcomeReviewed}
	rapid.Check(t, func(t *rapid.T) {
		in := fillInput{Repos: repos, Slots: rapid.IntRange(0, 3).Draw(t, "shared"), Found: map[string][]github.PullRequest{}}
		for _, name := range []string{"q1", "q2"} {
			in.Queues = append(in.Queues, Queue{Name: name, Slots: rapid.IntRange(0, 3).Draw(t, "slots")})
			in.Found[name] = rapid.SliceOf(rapid.SampledFrom(pool)).Draw(t, "found "+name)
		}
		for _, p := range pool {
			if rapid.Bool().Draw(t, "task") {
				in.Records = append(in.Records, doneReview("pr-"+p.Head, p, rapid.SampledFrom(outcomes).Draw(t, "outcome")))
			}
			if rapid.Bool().Draw(t, "skip") {
				u := p.URL
				if rapid.Bool().Draw(t, "upper") {
					u = strings.ToUpper(u)
				}
				in.Skips = append(in.Skips, github.Skip{URL: u})
			}
		}
		holds := github.Holds{Skips: in.Skips}
		plan := planFill(in)
		for _, a := range plan.Add {
			if holds.IsSkipped(a.PR.URL) {
				t.Fatalf("the plan adds the skipped PR %s", a.PR.URL)
			}
		}
		for _, cs := range plan.Ranked {
			for _, c := range cs {
				if holds.IsSkipped(c.PR.URL) {
					t.Fatalf("the plan ranks the skipped PR %s", c.PR.URL)
				}
			}
		}
	})
}

func (h *harness) skip(url string) SkipResult {
	return h.do(Request{Op: "reviews-skip", URL: url}).(SkipResult)
}

func (h *harness) unskip(url string) SkipResult {
	return h.do(Request{Op: "reviews-unskip", URL: url}).(SkipResult)
}

func (h *harness) skips() []string {
	var out []string
	for _, k := range h.do(Request{Op: "ls"}).(BoardView).Skips {
		out = append(out, k.URL)
	}
	return out
}

// startedReview fills a queue with PR 7 and waits for its review to start.
func startedReview(t *testing.T) (*harness, github.PullRequest) {
	h := queueHarness(t, authorQueue("q", 2, "hopper"))
	p7 := queuePR("acme/data", 7, 30)
	h.gh.found = map[string][]github.PullRequest{"q": {p7}}
	h.gh.setReview(openPR(7, "h7"))
	h.s.fill(context.Background())
	h.waitTab("claude reviewing")
	return h, p7
}

func TestASkipEndsTheOpenReviewAndHoldsEveryHead(t *testing.T) {
	h, p7 := startedReview(t)
	ctx := context.Background()

	res := h.skip("acme/data#7")
	if !res.Changed || res.Closed != "pr-7" || res.URL != prURL7 || res.Title != "Fix the tooltip" {
		t.Fatalf("skip = %+v, want pr-7 closed and the PR kept with its title", res)
	}
	if v := h.task("pr-7"); v.Stage != lifecycle.Done.String() || v.Outcome != "skipped" {
		t.Fatalf("pr-7 = %+v, want Done with the outcome skipped", v)
	}
	eventually(t, "the tab closed", func() bool { return len(h.rex.open("claude reviewing")) == 0 })
	eventually(t, "the review worktree removed", func() bool { return len(h.reviewsRemoved()) == 1 })
	if !slices.Equal(h.skips(), []string{prURL7}) {
		t.Fatalf("skips = %v", h.skips())
	}
	h.gh.mu.Lock()
	merges := len(h.gh.merged)
	h.gh.mu.Unlock()
	if merges != 0 {
		t.Fatalf("the skip wrote to GitHub: %d merges", merges)
	}

	for _, head := range []string{"h7", "h7-pushed"} {
		pr := p7
		pr.Head = head
		h.gh.found = map[string][]github.PullRequest{"q": {pr}}
		h.s.fill(ctx)
		if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done"}) {
			t.Fatalf("at head %s: reviews %v, want only the ended pr-7", head, got)
		}
	}
	v := h.do(Request{Op: "reviews"}).(ReviewsView)
	if len(v.Queues[0].Ranked) != 0 || len(v.Skips) != 1 {
		t.Fatalf("reviews = %+v, want no waiting PR and one skip", v)
	}
}

func TestAnUnskipLetsAPRBackInAtTheSameHead(t *testing.T) {
	h, _ := startedReview(t)
	ctx := context.Background()
	h.skip(prURL7)
	h.s.fill(ctx)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done"}) {
		t.Fatalf("reviews %v, want only the ended pr-7", got)
	}

	h.unskip("ACME/DATA#7")
	h.s.fill(ctx)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("after the unskip: %v, want pr-7 reviewing again at the same head", got)
	}
}

func TestACloseStillHoldsThePRAtItsHead(t *testing.T) {
	h, p7 := startedReview(t)
	ctx := context.Background()
	h.do(Request{Op: "close", Task: "pr-7"})
	if v := h.task("pr-7"); v.Outcome != "closed" {
		t.Fatalf("pr-7 = %+v, want the outcome closed", v)
	}
	h.s.fill(ctx)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done"}) {
		t.Fatalf("reviews %v, want the close to hold the PR at its head", got)
	}

	pushed := p7
	pushed.Head = "h7-pushed"
	h.gh.found = map[string][]github.PullRequest{"q": {pushed}}
	h.s.fill(ctx)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("reviews %v, want a new head to bring the PR back", got)
	}
}

func TestASkipHoldsAcrossARestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	opts := func(d *Deps) { d.Queues, d.PollInterval = []Queue{authorQueue("q", 1, "hopper")}, time.Hour }
	found := map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30), queuePR("acme/data", 8, 20)}}
	h := newHarness(t, db, nil, opts)
	h.gh.found = found
	h.gh.setReview(openPR(7, "h7"))
	ctx := context.Background()
	h.skip(prURL7)
	h.cancel()

	h = newHarness(t, db, nil, opts)
	h.gh.found = found
	if !slices.Equal(h.skips(), []string{prURL7}) {
		t.Fatalf("after the restart: skips = %v", h.skips())
	}
	h.s.fill(ctx)
	if got := h.reviews(); !slices.Equal(got, []string{"pr-8 #8 Reviewing"}) {
		t.Fatalf("reviews %v, want #8 only: #7 is still skipped", got)
	}
}

func TestASkipRefusesAPRThatIsNotOpenAndStoresNothing(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	merged := openPR(9, "h9")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	for url, want := range map[string]string{"acme/data#9": "merged", "acme/data#404": "no PR", "not a PR": "not a GitHub PR URL"} {
		_, err := h.s.Handle(context.Background(), Request{Op: "reviews-skip", URL: url})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("skip %q: err = %v, want %q", url, err, want)
		}
	}
	if got := h.skips(); len(got) != 0 {
		t.Fatalf("skips = %v, want none", got)
	}
}

func TestSkipAndUnskipAreIdempotent(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	h.gh.setReview(openPR(7, "h7"))
	if r := h.skip(prURL7); !r.Changed || r.Closed != "" {
		t.Fatalf("first skip = %+v", r)
	}
	if r := h.skip(prURL7); r.Changed {
		t.Fatalf("second skip = %+v, want no change", r)
	}
	if r := h.skip("ACME/data#7"); r.Changed || r.URL != prURL7 {
		t.Fatalf("a skip in another case = %+v, want the same skip", r)
	}
	if got := h.skips(); len(got) != 1 {
		t.Fatalf("skips = %v, want one", got)
	}
	if r := h.unskip(prURL7); !r.Changed {
		t.Fatalf("first unskip = %+v", r)
	}
	if r := h.unskip(prURL7); r.Changed {
		t.Fatalf("second unskip = %+v, want no change", r)
	}
}

func TestReviewOfASkippedPRKeepsTheSkipAndTheSkipHoldsAfterItEnds(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	h.gh.setReview(openPR(7, "h7"))
	h.skip(prURL7)
	if res := h.review(prURL7); res.Action != ReviewAdded {
		t.Fatalf("review of a skipped PR = %+v, want a new review", res)
	}
	if !slices.Equal(h.skips(), []string{prURL7}) {
		t.Fatalf("the review dropped the skip: %v", h.skips())
	}
	h.do(Request{Op: "close", Task: "pr-7"})
	pushed := queuePR("acme/data", 7, 30)
	pushed.Head = "h7-pushed"
	h.gh.found = map[string][]github.PullRequest{"q": {pushed}}
	h.s.fill(context.Background())
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Done"}) {
		t.Fatalf("reviews %v, want the skip to hold the PR after the review ended", got)
	}
}

func TestASkipThatLandsMidFillWins(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	hold := make(chan struct{})
	h.gh.found, h.gh.hold = map[string][]github.PullRequest{"q": {queuePR("acme/data", 7, 30)}}, hold
	h.gh.setReview(openPR(7, "h7"))
	done := make(chan struct{})
	go func() { h.s.fill(context.Background()); close(done) }()
	eventually(t, "the fill's search", func() bool { return len(h.gh.searchCalls()) == 1 })
	h.skip(prURL7)
	close(hold)
	<-done
	if got := h.reviews(); len(got) != 0 {
		t.Fatalf("reviews %v, want none: the skip landed while the fill searched", got)
	}
}

func TestASkipLeavesADoneReviewAsItIs(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.gh.setReview(openPR(7, "h1"))
	h.review(prURL7)
	h.waitTab("claude reviewing")
	merged := openPR(7, "h1")
	merged.State = "MERGED"
	h.gh.setReview(merged)
	h.waitStage("pr-7", lifecycle.Done)
	h.gh.setReview(openPR(7, "h2"))

	if r := h.skip(prURL7); r.Closed != "" {
		t.Fatalf("skip = %+v, want no Task closed", r)
	}
	if v := h.task("pr-7"); v.Outcome != "merged" {
		t.Fatalf("pr-7 = %+v, want the merged outcome kept", v)
	}
}

func TestAPublishedViewKeepsItsSkips(t *testing.T) {
	h := queueHarness(t, authorQueue("q", 1, "hopper"))
	h.gh.setReview(openPR(7, "h7"))
	h.gh.setReview(openPR(8, "h8"))
	h.skip(prURL7)
	h.skip("acme/data#8")
	v := h.do(Request{Op: "ls"}).(BoardView)
	h.unskip(prURL7)
	if len(v.Skips) != 2 || v.Skips[0].URL != prURL7 {
		t.Fatalf("an earlier view lost a skip: %+v", v.Skips)
	}
}

// The skip is stored before its Task ends. If the process stops between the
// two, a second skip ends the Task.
func TestASecondSkipEndsATaskThatSurvivedACrashAfterTheStoreWrite(t *testing.T) {
	h, _ := startedReview(t)
	if _, err := h.s.d.Store.AddSkip(store.Skip{URL: prURL7, Title: "Fix the tooltip", At: h.s.now()}); err != nil {
		t.Fatal(err)
	}
	h.s.call(func() {
		rows, _ := h.s.d.Store.Skips()
		h.s.skips = []github.Skip{github.Skip(rows[0])}
	})
	if got := h.reviews(); !slices.Equal(got, []string{"pr-7 #7 Reviewing"}) {
		t.Fatalf("reviews %v, want pr-7 still open", got)
	}
	if r := h.skip(prURL7); r.Closed != "pr-7" || !r.Changed {
		t.Fatalf("second skip = %+v, want pr-7 closed", r)
	}
	h.waitStage("pr-7", lifecycle.Done)
}
