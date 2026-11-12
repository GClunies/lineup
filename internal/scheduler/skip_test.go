package scheduler

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
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
