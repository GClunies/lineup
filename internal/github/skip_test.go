package github

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const url7 = "https://github.com/umbrella/data/pull/7"

func TestASkipKeepsAPRInTheSkippedList(t *testing.T) {
	skip := func(u string) []Skip { return []Skip{{URL: u}} }
	same := func(p PullRequest) PullRequest { return p }
	ended := func(skipped bool) []ReviewTask {
		return []ReviewTask{{URL: url7, Head: "h7", Done: true, Skipped: skipped}}
	}
	cases := []struct {
		name        string
		pr          func(PullRequest) PullRequest
		holds       Holds
		ranked, hit bool
	}{
		{"no skip", same, Holds{}, true, false},
		{"a skip", same, Holds{Skips: skip(url7)}, false, true},
		{"a skip holds at any head", func(p PullRequest) PullRequest { p.Head = "h7-pushed"; return p }, Holds{Skips: skip(url7)}, false, true},
		{"a skip in another case", same, Holds{Skips: skip(strings.ToUpper(url7))}, false, true},
		{"a skip of another PR", same, Holds{Skips: skip("https://github.com/umbrella/data/pull/8")}, true, false},
		{"a skip and an open Task: the Task holds the PR", same,
			Holds{Skips: skip(url7), Tasks: []ReviewTask{{URL: url7, Head: "h6"}}}, false, false},
		{"a skip and a Done Task at an older head: the skip holds the PR", same,
			Holds{Skips: skip(url7), Tasks: []ReviewTask{{URL: url7, Head: "h6", Done: true}}}, false, true},
		{"a skip of a draft: no rule wants the PR", func(p PullRequest) PullRequest { p.Draft = true; return p }, Holds{Skips: skip(url7)}, false, false},
		{"a skip of an approved PR: no rule wants the PR", func(p PullRequest) PullRequest { p.LatestReviews = approved; return p }, Holds{Skips: skip(url7)}, false, false},
		{"a Done Task that the user closed holds the PR at its head", same, Holds{Tasks: ended(false)}, false, false},
		{"a Done Task that a skip ended holds nothing", same, Holds{Tasks: ended(true)}, true, false},
		{"a Done Task that a skip ended, and the skip: the PR is in the skipped list", same,
			Holds{Skips: skip(url7), Tasks: ended(true)}, false, true},
		{"a Done Task that the user closed, and the skip: no list", same,
			Holds{Skips: skip(url7), Tasks: ended(false)}, false, false},
	}
	for _, c := range cases {
		r := Rank([]PullRequest{c.pr(row(7, "2026-01-01", ViewerState{}))}, c.holds)
		if (len(r.Ranked) == 1) != c.ranked || (len(r.Skipped) == 1) != c.hit {
			t.Errorf("%s: ranked %v, skipped %v, want ranked=%v skipped=%v", c.name, numbers(r.Ranked), numbers(r.Skipped), c.ranked, c.hit)
		}
	}
}

func TestSkippedCandidatesKeepTheRankOrder(t *testing.T) {
	prs := []PullRequest{row(41, "2026-02-01", ViewerState{}), row(42, "2026-03-01", ViewerState{Requested: true, ByName: true}), row(40, "2026-01-01", ViewerState{})}
	r := Rank(prs, Holds{Skips: []Skip{{URL: prs[0].URL}, {URL: prs[1].URL}}})
	if !slices.Equal(numbers(r.Ranked), []int{40}) || !slices.Equal(numbers(r.Skipped), []int{42, 41}) {
		t.Fatalf("ranked %v skipped %v, want [40] and [42 41]", numbers(r.Ranked), numbers(r.Skipped))
	}
}

// drawPRs makes PRs of one repository with random numbers, heads, and rank fields.
func drawPRs(t *rapid.T) []PullRequest {
	n := rapid.IntRange(0, 8).Draw(t, "n")
	var prs []PullRequest
	for i := range n {
		p := row(i+1, "2026-01-01", ViewerState{Requested: rapid.Bool().Draw(t, "requested"), ByName: rapid.Bool().Draw(t, "byName"),
			Reviewed: rapid.Bool().Draw(t, "reviewed")})
		p.Draft = rapid.Bool().Draw(t, "draft")
		p.Head = rapid.SampledFrom([]string{"a", "b"}).Draw(t, "head")
		p.CreatedAt = p.CreatedAt.AddDate(0, 0, rapid.IntRange(0, 5).Draw(t, "age"))
		prs = append(prs, p)
	}
	return prs
}

func drawHolds(t *rapid.T, prs []PullRequest) Holds {
	var h Holds
	for _, p := range prs {
		if rapid.Bool().Draw(t, "task") {
			done := rapid.Bool().Draw(t, "done")
			h.Tasks = append(h.Tasks, ReviewTask{URL: p.URL, Head: rapid.SampledFrom([]string{"a", "b"}).Draw(t, "taskHead"), Done: done,
				Skipped: done && rapid.Bool().Draw(t, "skipped")})
		}
	}
	return h
}

// A skip is a split of the rules' own result, so it never changes which PRs
// the other rules pass, or their order.
func TestPropertySkipSplitsWhatTheOtherRulesPass(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prs := drawPRs(t)
		base := drawHolds(t, prs)
		want := Rank(prs, base)
		if len(want.Skipped) != 0 {
			t.Fatalf("no skips, yet %v are skipped", numbers(want.Skipped))
		}
		withSkips := base
		skippedURL := map[string]bool{}
		for _, p := range prs {
			if rapid.Bool().Draw(t, "skip") {
				u := p.URL
				if rapid.Bool().Draw(t, "upper") {
					u = strings.ToUpper(u)
				}
				withSkips.Skips = append(withSkips.Skips, Skip{URL: u})
				skippedURL[strings.ToLower(p.URL)] = true
			}
		}
		got := Rank(prs, withSkips)
		var keep, hold []int
		for _, c := range want.Ranked {
			if skippedURL[strings.ToLower(c.PR.URL)] {
				hold = append(hold, c.PR.Number)
			} else {
				keep = append(keep, c.PR.Number)
			}
		}
		if !slices.Equal(numbers(got.Ranked), keep) || !slices.Equal(numbers(got.Skipped), hold) {
			t.Fatalf("ranked %v skipped %v, want %v and %v", numbers(got.Ranked), numbers(got.Skipped), keep, hold)
		}
	})
}

// The same skip twice, or in another case, changes nothing, and a PR's head
// never matters to a skip.
func TestPropertyASkipIgnoresDuplicatesAndHeads(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prs := drawPRs(t)
		h := drawHolds(t, prs)
		for _, p := range prs {
			if rapid.Bool().Draw(t, "skip") {
				h.Skips = append(h.Skips, Skip{URL: p.URL})
			}
		}
		twice := h
		for _, s := range h.Skips {
			twice.Skips = append(twice.Skips, Skip{URL: strings.ToUpper(s.URL), Title: "again"})
		}
		if a, b := Rank(prs, h), Rank(prs, twice); !slices.Equal(numbers(a.Ranked), numbers(b.Ranked)) || !slices.Equal(numbers(a.Skipped), numbers(b.Skipped)) {
			t.Fatalf("a repeated skip changed the ranking: %v/%v then %v/%v", numbers(a.Ranked), numbers(a.Skipped), numbers(b.Ranked), numbers(b.Skipped))
		}
		pushed := slices.Clone(prs)
		for i := range pushed {
			pushed[i].Head += fmt.Sprint("-pushed")
		}
		for _, c := range Rank(pushed, h).Ranked {
			if h.IsSkipped(c.PR.URL) {
				t.Fatalf("PR %d enters at a new head though the user skipped it", c.PR.Number)
			}
		}
	})
}

// A Task that a skip ended only stands in for the skip. Once the user
// unskips the PR, the rules treat it as if the Task never existed.
func TestPropertyATaskThatASkipEndedHoldsNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prs := drawPRs(t)
		var h Holds
		for _, p := range prs {
			if rapid.Bool().Draw(t, "task") {
				h.Tasks = append(h.Tasks, ReviewTask{URL: p.URL, Head: p.Head, Done: true, Skipped: true})
			}
		}
		if got, want := Rank(prs, h), Rank(prs, Holds{}); !slices.Equal(numbers(got.Ranked), numbers(want.Ranked)) || len(got.Skipped) != 0 {
			t.Fatalf("ranked %v skipped %v, want %v and none", numbers(got.Ranked), numbers(got.Skipped), numbers(want.Ranked))
		}
	})
}
