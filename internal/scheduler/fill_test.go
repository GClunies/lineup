package scheduler

import (
	"fmt"
	"slices"
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
