package github

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func row(number int, created string, viewer ViewerState) PullRequest {
	at, _ := time.Parse(time.DateOnly, created)
	return PullRequest{Repo: "umbrella/data", Number: number, URL: "https://github.com/umbrella/data/pull/" + strconv.Itoa(number),
		Head: "h" + strconv.Itoa(number), CreatedAt: at, Viewer: viewer}
}

func numbers(cs []Candidate) []int {
	var out []int
	for _, c := range cs {
		out = append(out, c.PR.Number)
	}
	return out
}

var approved = []Review{{Author: "knuth", State: "APPROVED"}}

func TestCandidateRules(t *testing.T) {
	asked := ViewerState{Requested: true}
	reviewed := ViewerState{Reviewed: true}
	cases := []struct {
		name  string
		pr    func(PullRequest) PullRequest
		tasks []ReviewTask
		want  bool
	}{
		{"a new PR", func(p PullRequest) PullRequest { return p }, nil, true},
		{"draft", func(p PullRequest) PullRequest { p.Draft = true; return p }, nil, false},
		{"fork", func(p PullRequest) PullRequest { p.Fork = true; return p }, nil, false},
		{"bot", func(p PullRequest) PullRequest { p.Bot = true; return p }, nil, false},
		{"the user's own PR", func(p PullRequest) PullRequest { p.Viewer.Author = true; return p }, nil, false},
		{"approved by someone", func(p PullRequest) PullRequest { p.LatestReviews = approved; return p }, nil, false},
		{"requested and approved, never reviewed", func(p PullRequest) PullRequest { p.Viewer = asked; p.LatestReviews = approved; return p }, nil, false},
		{"reviewed, not requested again", func(p PullRequest) PullRequest { p.Viewer = reviewed; return p }, nil, false},
		{"reviewed, re-requested by name", func(p PullRequest) PullRequest {
			p.Viewer = ViewerState{Reviewed: true, Requested: true, ByName: true}
			return p
		}, nil, true},
		{"reviewed, re-requested through a team", func(p PullRequest) PullRequest {
			p.Viewer = ViewerState{Reviewed: true, Requested: true}
			return p
		}, nil, true},
		{"a re-review keeps its place after an approval", func(p PullRequest) PullRequest {
			p.Viewer, p.LatestReviews = ViewerState{Reviewed: true, Requested: true}, approved
			return p
		}, nil, true},
		{"a re-review from a fork", func(p PullRequest) PullRequest {
			p.Viewer, p.Fork = ViewerState{Reviewed: true, Requested: true}, true
			return p
		}, nil, false},
		{"another person's comments on the head", func(p PullRequest) PullRequest {
			p.LatestReviews = []Review{{Author: "knuth", State: "COMMENTED", Commit: "h7"}}
			return p
		}, nil, false},
		{"another person's comments on an older head", func(p PullRequest) PullRequest {
			p.LatestReviews = []Review{{Author: "knuth", State: "COMMENTED", Commit: "h6"}}
			return p
		}, nil, true},
		{"another person's change request on an older head", func(p PullRequest) PullRequest {
			p.LatestReviews = []Review{{Author: "knuth", State: "CHANGES_REQUESTED", Commit: "h6"}}
			return p
		}, nil, false},
		{"another person's change request, then comments on an older head", func(p PullRequest) PullRequest {
			p.LatestReviews = []Review{{Author: "knuth", State: "COMMENTED", Commit: "h6", Opinion: "CHANGES_REQUESTED"}}
			return p
		}, nil, false},
		{"approved, then comments on the head, never reviewed", func(p PullRequest) PullRequest {
			p.LatestReviews = []Review{{Author: "knuth", State: "COMMENTED", Commit: "h7", Opinion: "APPROVED"}}
			return p
		}, nil, false},
		{"a re-review after another person approved, then commented on the head", func(p PullRequest) PullRequest {
			p.Viewer = ViewerState{Reviewed: true, Requested: true}
			p.LatestReviews = []Review{{Author: "knuth", State: "COMMENTED", Commit: "h7", Opinion: "APPROVED"}}
			return p
		}, nil, true},
		{"a bot's comments on the head", func(p PullRequest) PullRequest {
			p.LatestReviews = []Review{{Author: "greptile-apps", State: "COMMENTED", Commit: "h7", Bot: true}}
			return p
		}, nil, true},
		{"the author's comments on the head", func(p PullRequest) PullRequest {
			p.Author, p.LatestReviews = "hopper", []Review{{Author: "hopper", State: "COMMENTED", Commit: "h7"}}
			return p
		}, nil, true},
		{"a re-review after the user's own comments on the head", func(p PullRequest) PullRequest {
			p.Viewer = ViewerState{Reviewed: true, Requested: true}
			p.LatestReviews = []Review{{Author: "octocat", State: "COMMENTED", Commit: "h7", Viewer: true}}
			return p
		}, nil, true},
		{"a re-review while another person's comments on the head are open", func(p PullRequest) PullRequest {
			p.Viewer = ViewerState{Reviewed: true, Requested: true}
			p.LatestReviews = []Review{{Author: "knuth", State: "COMMENTED", Commit: "h7"}}
			return p
		}, nil, false},
		// GitHub reports only submitted reviews in Viewer.Reviewed, so a
		// pending review leaves a PR that nobody approved wanted.
		{"only a pending review", func(p PullRequest) PullRequest { p.Viewer = ViewerState{}; return p }, nil, true},
		{"an open review Task at the head", func(p PullRequest) PullRequest { return p },
			[]ReviewTask{{URL: "https://github.com/umbrella/data/pull/7", Head: "h7"}}, false},
		{"an open review Task at an older head", func(p PullRequest) PullRequest { return p },
			[]ReviewTask{{URL: "https://github.com/umbrella/data/pull/7", Head: "h6"}}, false},
		{"a Done review Task at the head", func(p PullRequest) PullRequest { return p },
			[]ReviewTask{{URL: "https://github.com/umbrella/data/pull/7", Head: "h7", Done: true}}, false},
		{"a new head after a closed review", func(p PullRequest) PullRequest { return p },
			[]ReviewTask{{URL: "https://github.com/umbrella/data/pull/7", Head: "h6", Done: true}}, true},
		{"a Task URL in another case", func(p PullRequest) PullRequest { return p },
			[]ReviewTask{{URL: "https://github.com/Umbrella/Data/pull/7", Head: "h7", Done: true}}, false},
		{"a review Task of another PR", func(p PullRequest) PullRequest { return p },
			[]ReviewTask{{URL: "https://github.com/umbrella/data/pull/8", Head: "h7"}}, true},
	}
	for _, c := range cases {
		got := Rank([]PullRequest{c.pr(row(7, "2026-01-01", ViewerState{}))}, Holds{Tasks: c.tasks}).Ranked
		if (len(got) == 1) != c.want {
			t.Errorf("%s: candidates %v, want in=%v", c.name, numbers(got), c.want)
		}
	}
}

func TestCandidateRanking(t *testing.T) {
	again := ViewerState{Reviewed: true, Requested: true}
	prs := []PullRequest{
		row(41, "2026-02-01", ViewerState{}),
		row(42, "2026-03-01", ViewerState{Requested: true, ByName: true}),
		row(40, "2026-01-01", ViewerState{}),
		row(2, "2026-04-01", again),
		row(1, "2026-05-01", again),
		row(3, "2026-06-01", ViewerState{Reviewed: true, Requested: true, ByName: true}),
	}
	prs[4].LatestReviews = approved
	got := Rank(prs, Holds{}).Ranked
	if want := []int{3, 2, 1, 42, 40, 41}; !slices.Equal(numbers(got), want) {
		t.Fatalf("rank %v, want %v", numbers(got), want)
	}
	if c := got[0]; !c.Rereview || !c.ByName {
		t.Errorf("#3 = %+v, want a re-review by name", c)
	}
	if c := got[3]; c.Rereview || !c.ByName {
		t.Errorf("#42 = %+v, want a new review by name", c)
	}
}

// The fixture is a recorded search: s0 is a queue on umbrella/data for two
// authors, s1 holds Dependabot PRs, and s2 holds one of the user's own PRs.
func TestCandidatesOfARecordedQueueSearch(t *testing.T) {
	bin, log := fakeSearchGH(t, splitReply(t, "queue_search.json", "q-queue", "q-bots", "q-mine"))
	got, err := (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", []Search{
		{"queue", "platform", "q-queue"}, {"bots", "bots", "q-bots"}, {"mine", "mine", "q-mine"},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := calls(t, log)[0]
	if !strings.Contains(call, "|viewer=octocat") || !strings.Contains(call, "reviews(author: $viewer, states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED])") {
		t.Fatalf("the search does not ask for the user's submitted reviews only: %s", call)
	}

	queue := got["queue"]
	byNumber := map[int]PullRequest{}
	for _, pr := range queue {
		byNumber[pr.Number] = pr
	}
	if pr := byNumber[4873]; !pr.Viewer.Reviewed || pr.Viewer.Requested || pr.Head != "03a760efcc39ec0f7fe67f8d41516b244b47b76c" || pr.Base != "develop" {
		t.Errorf("data#4873, approved by the user = %+v", pr)
	}
	if pr := byNumber[4880]; pr.Viewer.Reviewed || !pr.Viewer.Requested || pr.Viewer.ByName || pr.Base != "hopper/state-check" {
		t.Errorf("data#4880, requested through a team = %+v", pr)
	}
	if pr := got["bots"][0]; !pr.Bot || pr.Author != "dependabot" {
		t.Errorf("a Dependabot PR = %+v", pr)
	}
	if pr := got["mine"][0]; !pr.Viewer.Author {
		t.Errorf("the user's own PR = %+v", pr)
	}

	if r := byNumber[4944].LatestReviews; len(r) != 2 || !r[0].Bot || r[1].Bot || r[1].Commit != byNumber[4944].Head || r[1].Viewer {
		t.Errorf("data#4944 reviews = %+v, want a bot and liskov on the head", r)
	}
	if r := byNumber[4873].LatestReviews; len(r) != 2 || !r[1].Viewer {
		t.Errorf("data#4873 reviews = %+v, want the user's approval", r)
	}

	want := []int{4790, 4849, 4851, 4880, 4939, 4940, 4954}
	if c := Rank(queue, Holds{}).Ranked; !slices.Equal(numbers(c), want) {
		t.Errorf("candidates %v, want %v: oldest first, without the two the user approved and the one liskov reviews", numbers(c), want)
	}
	for _, k := range []string{"bots", "mine"} {
		if c := Rank(got[k], Holds{}).Ranked; len(c) != 0 {
			t.Errorf("%s: candidates %v, want none", k, numbers(c))
		}
	}
}
