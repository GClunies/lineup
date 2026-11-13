package github

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const url7 = "https://github.com/umbrella/data/pull/7"

func TestASkipKeepsAPROutOfTheQueue(t *testing.T) {
	skip := func(u string) []Skip { return []Skip{{URL: u}} }
	same := func(p PullRequest) PullRequest { return p }
	ended := func(skipped bool) []ReviewTask {
		return []ReviewTask{{URL: url7, Head: "h7", Done: true, Skipped: skipped}}
	}
	cases := []struct {
		name   string
		pr     func(PullRequest) PullRequest
		holds  Holds
		ranked bool
	}{
		{"no skip", same, Holds{}, true},
		{"a skip", same, Holds{Skips: skip(url7)}, false},
		{"a skip holds at any head", func(p PullRequest) PullRequest { p.Head = "h7-pushed"; return p }, Holds{Skips: skip(url7)}, false},
		{"a skip in another case", same, Holds{Skips: skip(strings.ToUpper(url7))}, false},
		{"a skip of another PR", same, Holds{Skips: skip("https://github.com/umbrella/data/pull/8")}, true},
		{"a Done Task that the user closed holds the PR at its head", same, Holds{Tasks: ended(false)}, false},
		{"a Done Task that a skip ended holds nothing", same, Holds{Tasks: ended(true)}, true},
		{"a Done Task that a skip ended, and the skip", same, Holds{Skips: skip(url7), Tasks: ended(true)}, false},
	}
	for _, c := range cases {
		r := Rank([]PullRequest{c.pr(row(7, "2026-01-01", ViewerState{}))}, c.holds)
		if (len(r) == 1) != c.ranked {
			t.Errorf("%s: ranked %v, want ranked=%v", c.name, numbers(r), c.ranked)
		}
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

// A skip only takes the skipped PRs out of the rules' own result. It never
// changes which other PRs the rules pass, or their order.
func TestPropertyASkipOnlyRemovesTheSkippedPRs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prs := drawPRs(t)
		base := drawHolds(t, prs)
		want := Rank(prs, base)
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
		var keep []int
		for _, c := range want {
			if !skippedURL[strings.ToLower(c.PR.URL)] {
				keep = append(keep, c.PR.Number)
			}
		}
		if got := Rank(prs, withSkips); !slices.Equal(numbers(got), keep) {
			t.Fatalf("ranked %v, want %v", numbers(got), keep)
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
		if a, b := Rank(prs, h), Rank(prs, twice); !slices.Equal(numbers(a), numbers(b)) {
			t.Fatalf("a repeated skip changed the ranking: %v then %v", numbers(a), numbers(b))
		}
		pushed := slices.Clone(prs)
		for i := range pushed {
			pushed[i].Head += fmt.Sprint("-pushed")
		}
		for _, c := range Rank(pushed, h) {
			if slices.ContainsFunc(h.Skips, func(s Skip) bool { return strings.EqualFold(s.URL, c.PR.URL) }) {
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
		if got, want := Rank(prs, h), Rank(prs, Holds{}); !slices.Equal(numbers(got), numbers(want)) {
			t.Fatalf("ranked %v, want %v", numbers(got), numbers(want))
		}
	})
}
