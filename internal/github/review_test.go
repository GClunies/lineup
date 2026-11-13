package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestQuery(t *testing.T) {
	const base = "is:pr archived:false sort:created-asc draft:false -author:@me"
	const open = base + " is:open"
	members := map[string][]string{
		"umbrella/platform": {"kernighan", "hopper"},
		"umbrella/data-eng": {"hopper", "knuth"},
		"umbrella/empty":    {},
	}
	for name, c := range map[string]struct {
		f    Filter
		want string
	}{
		"repo":   {Filter{Base: QueuePRs, Repo: []string{"umbrella/data", "umbrella/backend"}}, open + " repo:umbrella/data repo:umbrella/backend"},
		"author": {Filter{Base: QueuePRs, Author: []string{"octo", "hubot"}}, open + " author:octo author:hubot"},
		"author team": {Filter{Base: QueuePRs, AuthorTeam: []string{"umbrella/platform"}},
			open + " author:kernighan author:hopper"},
		"author and teams merged": {Filter{Base: QueuePRs, Author: []string{"hopper", "octo"}, AuthorTeam: []string{"umbrella/platform", "umbrella/data-eng"}},
			open + " author:hopper author:octo author:kernighan author:knuth"},
		"review requested": {Filter{Base: QueuePRs, ReviewRequested: "@me"}, open + " review-requested:@me"},
		"label": {Filter{Base: QueuePRs, Label: []string{"needs-review", "breaking change", "frontend"}},
			open + ` label:needs-review,"breaking change",frontend`},
		"two filters": {Filter{Base: QueuePRs, Repo: []string{"umbrella/data"}, Label: []string{"needs-review"}},
			open + " repo:umbrella/data label:needs-review"},
		"empty team with author": {Filter{Base: QueuePRs, Author: []string{"octo"}, AuthorTeam: []string{"umbrella/empty"}},
			open + " author:octo"},
		"regex adds nothing": {Filter{Base: QueuePRs, Repo: []string{"o/r"}, IncludeRegex: "DATA-", ExcludeRegex: "WIP"}, open + " repo:o/r"},
	} {
		got, ok := c.f.Query(members)
		if !ok || got != c.want {
			t.Errorf("%s:\n got %q %v\nwant %q", name, got, ok, c.want)
		}
	}
	if q, ok := (Filter{Base: QueuePRs, AuthorTeam: []string{"umbrella/empty"}}).Query(members); ok {
		t.Errorf("a filter whose only team is empty searched %q, which matches every author", q)
	}
}

func TestKeepAppliesTheTitleRegexes(t *testing.T) {
	prs := []PullRequest{{Number: 1, Title: "HLESS-12 Move the loaders"}, {Number: 2, Title: "hless-13 lower case"},
		{Number: 3, Title: "Fix the HLESS- prefix parser"}, {Number: 4, Title: "DATA-7 Write the usage logs"}, {Number: 5, Title: "WIP: DATA-8 draft"}}
	numbers := func(prs []PullRequest) []int {
		out := []int{}
		for _, pr := range prs {
			out = append(out, pr.Number)
		}
		return out
	}
	for name, c := range map[string]struct {
		include, exclude string
		want             []int
	}{
		"no regex":             {"", "", []int{1, 2, 3, 4, 5}},
		"exclude":              {"", "HLESS-", []int{2, 4, 5}},
		"exclude anywhere":     {"", "usage", []int{1, 2, 3, 5}},
		"exclude alternation":  {"", "HLESS-|WIP", []int{2, 4}},
		"exclude ignore case":  {"", "(?i)hless-", []int{4, 5}},
		"exclude anchored":     {"", `^HLESS-\d`, []int{2, 3, 4, 5}},
		"include":              {`\bDATA-\d+`, "", []int{4, 5}},
		"include and exclude":  {`DATA-\d+`, "^WIP", []int{4}},
		"include matches none": {"NOPE", "", []int{}},
	} {
		f := Filter{Base: QueuePRs, Repo: []string{"umbrella/data"}, IncludeRegex: c.include, ExcludeRegex: c.exclude}
		if got := numbers(f.Keep(prs)); !slices.Equal(got, c.want) {
			t.Errorf("%s: kept %v, want %v", name, got, c.want)
		}
	}
	if len(prs) != 5 || prs[0].Number != 1 {
		t.Errorf("Keep changed its input: %v", numbers(prs))
	}
	if got := (Filter{ExcludeRegex: "HLESS-"}).Keep([]PullRequest{}); got == nil {
		t.Error("Keep of an empty list gave nil, want an empty list")
	}
}

// Every PR that Keep returns matches IncludeRegex and not ExcludeRegex, and
// Keep drops no other PR.
func TestKeepIsTheRegexRule(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		word := rapid.StringMatching(`[A-Za-z]{1,3}`)
		include := rapid.OneOf(rapid.Just(""), word).Draw(t, "include")
		exclude := rapid.OneOf(rapid.Just(""), word).Draw(t, "exclude")
		titles := rapid.SliceOf(rapid.StringMatching(`[A-Za-z -]{0,12}`)).Draw(t, "titles")
		var prs []PullRequest
		for i, title := range titles {
			prs = append(prs, PullRequest{Number: i, Title: title})
		}
		kept := map[int]bool{}
		for _, pr := range (Filter{IncludeRegex: include, ExcludeRegex: exclude}).Keep(prs) {
			kept[pr.Number] = true
		}
		for _, pr := range prs {
			want := strings.Contains(pr.Title, include) && (exclude == "" || !strings.Contains(pr.Title, exclude))
			if kept[pr.Number] != want {
				t.Fatalf("title %q, include %q, exclude %q: kept %v, want %v", pr.Title, include, exclude, kept[pr.Number], want)
			}
		}
	})
}

// fakeSearchGH is a gh that answers each search with the reply for its query.
// A query with no reply fails, as gh does on an HTTP error.
func fakeSearchGH(t *testing.T, replies map[string]string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	for q, reply := range replies {
		if err := os.WriteFile(filepath.Join(dir, q+".json"), []byte(reply), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin, log = filepath.Join(dir, "gh"), filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
printf '%s\n' "$(IFS='|'; echo "$*" | tr '\n' ' ')" >> "` + log + `"
for a in "$@"; do
  case "$a" in q=*) f="` + dir + `/${a#q=}.json" ;; esac
done
if [ -f "$f" ]; then cat "$f"; else echo 'HTTP 502' >&2; exit 1; fi
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// splitReply turns a recorded reply for searches s0, s1, ... into one reply
// for each search, keyed by the query that the test gives it.
func splitReply(t *testing.T, fixture string, queries ...string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct{ Data map[string]json.RawMessage }
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for i, q := range queries {
		out[q] = `{"data":{"s":` + string(rec.Data[fmt.Sprintf("s%d", i)]) + `}}`
	}
	return out
}

// GitHub stops a GraphQL call at about 10 seconds, so each search is its own call.
func TestSearchPRsSplitsEachSearch(t *testing.T) {
	bin, log := fakeSearchGH(t, splitReply(t, "search_three_views.json", "q-mine", "q-req", "q-team"))
	got, err := (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", []Search{
		{"mine", "My PRs", "q-mine"}, {"requested", "Requested", "q-req"}, {"view:team", "My teammates", "q-team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := calls(t, log)
	if len(lines) != 3 {
		t.Fatalf("want one gh call for each of three searches, got %d", len(lines))
	}
	for _, v := range []string{"q=q-mine", "q=q-req", "q=q-team"} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "|"+v) && strings.Contains(l, "|viewer=octocat") }) {
			t.Errorf("no call has the variables %s and viewer=octocat: %q", v, lines)
		}
	}
	numbers := map[string][]int{}
	for k, prs := range got {
		for _, pr := range prs {
			numbers[k] = append(numbers[k], pr.Number)
		}
	}
	want := map[string][]int{"mine": {4945, 4919, 6, 4744, 94}, "requested": {4955, 4901}, "view:team": {4954, 4873}}
	if !reflect.DeepEqual(numbers, want) {
		t.Fatalf("PR numbers by search = %v, want %v", numbers, want)
	}

	mine := got["mine"]
	first := mine[0]
	if first.Repo != "umbrella/data" || first.Author != "octocat" || first.URL != "https://github.com/umbrella/data/pull/4945" || first.Viewer.ByName {
		t.Errorf("data#4945 = %+v", first)
	}
	if !first.CreatedAt.Equal(time.Date(2026, 11, 4, 22, 6, 53, 0, time.UTC)) {
		t.Errorf("data#4945 created %v", first.CreatedAt)
	}
	if pr := mine[1]; !reflect.DeepEqual(pr.LatestReviews[1], Review{Author: "thompson", State: "CHANGES_REQUESTED", SubmittedAt: pr.LatestReviews[1].SubmittedAt}) {
		t.Errorf("data#4919 = %+v", pr)
	}
	if pr := mine[2]; pr.Repo != "octocat/hello-world" {
		t.Errorf("hello-world#6 = %+v", pr)
	}
	if pr := mine[3]; !pr.Draft {
		t.Errorf("data#4744 = %+v", pr)
	}
	if pr := mine[4]; !pr.Fork || pr.Repo != "globex/toolkit" {
		t.Errorf("toolkit#94 = %+v", pr)
	}
	if pr := got["requested"][0]; pr.Author != "goldberg" || !pr.Viewer.ByName {
		t.Errorf("data#4955 = %+v", pr)
	}
	if pr := got["requested"][1]; pr.Author != "dependabot" {
		t.Errorf("data#4901 = %+v", pr)
	}
}

func TestSearchErrorNamesTheSearch(t *testing.T) {
	bin, _ := fakeSearchGH(t, map[string]string{
		"a": `{"data":{"s":{"nodes":[]}}}`,
		"b": `{"errors":[{"type":"EXCESSIVE_PAGINATION","path":["s"],"message":"too many records"}]}`,
	})
	searches := []Search{{"mine", "My PRs", "a"}, {"view:team", "My teammates", "b"}}
	got, err := (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", searches)
	if err == nil || !strings.Contains(err.Error(), "search My teammates: too many records") {
		t.Errorf("a GraphQL error on My teammates: err = %v", err)
	}
	if got != nil {
		t.Errorf("a failed search returned lists %v, want none: the fill starts nothing without every queue", got)
	}

	searches = []Search{{"mine", "My PRs", "b"}, {"view:team", "My teammates", "missing"}}
	_, err = (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", searches)
	if err == nil || !strings.Contains(err.Error(), "search My PRs: too many records") || !strings.Contains(err.Error(), "search My teammates") || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("two failed searches: err = %v, want both named", err)
	}
}

func TestTeamMembers(t *testing.T) {
	bin, log := fakeGH(t, filepath.Join("testdata", "team_members.json"), "")
	got, err := (&Client{Bin: bin}).TeamMembers(context.Background(), []string{"umbrella/data", "umbrella/data-eng", "umbrella/platform"})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(calls(t, log)); n != 1 {
		t.Errorf("want one gh call for three teams, got %d", n)
	}
	if want := []string{"kernighan", "hopper", "octocat", "noether"}; !reflect.DeepEqual(got["umbrella/platform"], want) {
		t.Errorf("umbrella/platform = %v, want %v", got["umbrella/platform"], want)
	}
	if len(got["umbrella/data"]) != 15 || len(got["umbrella/data-eng"]) != 9 {
		t.Errorf("team sizes = %d, %d, want 15, 9", len(got["umbrella/data"]), len(got["umbrella/data-eng"]))
	}

	if _, err := (&Client{Bin: bin}).TeamMembers(context.Background(), []string{"umbrella/data", "umbrella/data-eng", "umbrella/platform", "umbrella/gone"}); err == nil || !strings.Contains(err.Error(), "umbrella/gone") {
		t.Errorf("a team missing from the reply: err = %v", err)
	}
}
