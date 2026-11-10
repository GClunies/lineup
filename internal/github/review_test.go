package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestQuery(t *testing.T) {
	const open = "is:pr is:open archived:false sort:created-desc"
	const oldest = "is:pr is:open archived:false sort:created-asc"
	members := map[string][]string{
		"umbrella/platform": {"kernighan", "hopper"},
		"umbrella/data-eng": {"hopper", "knuth"},
		"umbrella/empty":    {},
	}
	for name, c := range map[string]struct {
		f    Filter
		want string
	}{
		"my PRs":    {Filter{Base: MyPRs}, open + " author:@me"},
		"requested": {Filter{Base: RequestedPRs}, open + " review-requested:@me draft:false"},
		"repos":     {Filter{Base: ViewPRs, Repos: []string{"umbrella/data", "umbrella/backend"}}, open + " repo:umbrella/data repo:umbrella/backend"},
		"authors":   {Filter{Base: ViewPRs, Authors: []string{"octo", "hubot"}}, open + " author:octo author:hubot"},
		"author teams": {Filter{Base: ViewPRs, AuthorTeams: []string{"umbrella/platform"}},
			open + " author:kernighan author:hopper"},
		"authors and teams merged": {Filter{Base: ViewPRs, Authors: []string{"hopper", "octo"}, AuthorTeams: []string{"umbrella/platform", "umbrella/data-eng"}},
			open + " author:hopper author:octo author:kernighan author:knuth"},
		"requested teams": {Filter{Base: ViewPRs, RequestedTeams: []string{"umbrella/data", "umbrella/data-eng"}},
			open + " team-review-requested:umbrella/data team-review-requested:umbrella/data-eng"},
		"labels": {Filter{Base: ViewPRs, Labels: []string{"needs-review", "breaking change", "frontend"}},
			open + ` label:needs-review,"breaking change",frontend`},
		"two filters": {Filter{Base: ViewPRs, Repos: []string{"umbrella/data"}, Labels: []string{"needs-review"}},
			open + " repo:umbrella/data label:needs-review"},
		"queue": {Filter{Base: QueuePRs, Repos: []string{"umbrella/data"}, Authors: []string{"hopper"}},
			oldest + " draft:false -author:@me repo:umbrella/data author:hopper"},
		"empty team with authors": {Filter{Base: ViewPRs, Authors: []string{"octo"}, AuthorTeams: []string{"umbrella/empty"}},
			open + " author:octo"},
	} {
		got, ok := c.f.Query(members)
		if !ok || got != c.want {
			t.Errorf("%s:\n got %q %v\nwant %q", name, got, ok, c.want)
		}
	}
	if q, ok := (Filter{Base: ViewPRs, AuthorTeams: []string{"umbrella/empty"}}).Query(members); ok {
		t.Errorf("a filter whose only team is empty searched %q, which matches every author", q)
	}
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
func TestSearchPRsSplitsEachView(t *testing.T) {
	bin, log := fakeSearchGH(t, splitReply(t, "search_three_views.json", "q-mine", "q-req", "q-team"))
	got, err := (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", []Search{
		{"mine", "My PRs", "q-mine"}, {"requested", "Requested", "q-req"}, {"view:team", "My teammates", "q-team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := calls(t, log)
	if len(lines) != 3 {
		t.Fatalf("want one gh call for each of three views, got %d", len(lines))
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
		t.Fatalf("PR numbers by view = %v, want %v", numbers, want)
	}

	mine := got["mine"]
	first := mine[0]
	if first.Repo != "umbrella/data" || first.Author != "octocat" || first.URL != "https://github.com/umbrella/data/pull/4945" ||
		first.Checks != ChecksPassed || first.ReviewDecision != "REVIEW_REQUIRED" ||
		!reflect.DeepEqual(first.RequestedUsers, []string{"umbrella-ci-bot", "dijkstra"}) ||
		!reflect.DeepEqual(first.RequestedTeams, []string{"umbrella/data", "umbrella/search"}) {
		t.Errorf("data#4945 = %+v", first)
	}
	if !first.CreatedAt.Equal(time.Date(2026, 11, 4, 22, 6, 53, 0, time.UTC)) {
		t.Errorf("data#4945 created %v", first.CreatedAt)
	}
	if pr := mine[1]; pr.Checks != ChecksPending || pr.ReviewDecision != "CHANGES_REQUESTED" ||
		!reflect.DeepEqual(pr.LatestReviews[1], Review{Author: "thompson", State: "CHANGES_REQUESTED", SubmittedAt: pr.LatestReviews[1].SubmittedAt}) {
		t.Errorf("data#4919 = %+v", pr)
	}
	if pr := mine[2]; pr.Repo != "octocat/hello-world" || pr.Checks != ChecksNone || pr.ReviewDecision != "" {
		t.Errorf("hello-world#6 = %+v", pr)
	}
	if pr := mine[3]; !pr.Draft || pr.Checks != ChecksFailed {
		t.Errorf("data#4744 = %+v", pr)
	}
	if pr := mine[4]; !pr.Fork || pr.Repo != "globex/toolkit" {
		t.Errorf("toolkit#94 = %+v", pr)
	}
	if pr := got["requested"][0]; pr.Author != "goldberg" || !reflect.DeepEqual(pr.RequestedUsers, []string{"octocat", "umbrella-ci-bot"}) {
		t.Errorf("data#4955 = %+v", pr)
	}
	if pr := got["requested"][1]; pr.ReviewDecision != "APPROVED" || pr.Checks != ChecksFailed || pr.Author != "dependabot" {
		t.Errorf("data#4901 = %+v", pr)
	}
}

func TestSearchErrorNamesTheView(t *testing.T) {
	bin, _ := fakeSearchGH(t, map[string]string{
		"a": `{"data":{"s":{"nodes":[]}}}`,
		"b": `{"errors":[{"type":"EXCESSIVE_PAGINATION","path":["s"],"message":"too many records"}]}`,
	})
	searches := []Search{{"mine", "My PRs", "a"}, {"view:team", "My teammates", "b"}}
	got, err := (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", searches)
	if err == nil || !strings.Contains(err.Error(), "search My teammates: too many records") {
		t.Errorf("a GraphQL error on My teammates: err = %v", err)
	}
	var errs SearchErrors
	if !errors.As(err, &errs) || len(errs) != 1 || errs["view:team"] == nil {
		t.Errorf("SearchErrors = %v, want only view:team", errs)
	}
	if prs, ok := got["mine"]; !ok || prs == nil {
		t.Errorf("the passed search has no list: %v", got)
	}
	if _, ok := got["view:team"]; ok {
		t.Errorf("the failed search has a list: %v", got)
	}

	searches = []Search{{"mine", "My PRs", "a"}, {"view:team", "My teammates", "missing"}}
	_, err = (&Client{Bin: bin}).SearchPRs(context.Background(), "octocat", searches)
	if err == nil || !strings.Contains(err.Error(), "search My teammates") || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("a failed call: err = %v", err)
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

func TestPRCacheKeepsAnEmptyList(t *testing.T) {
	c := PRCache{Dir: filepath.Join(t.TempDir(), "cache", "github")}
	if _, ok := c.Read("view:My teammates"); ok {
		t.Error("an empty cache read as a list")
	}
	c.Write("view:My teammates", nil)
	if prs, ok := c.Read("view:My teammates"); !ok || len(prs) != 0 {
		t.Errorf("a stored empty list read as %v %v", prs, ok)
	}
	want := []PullRequest{{Repo: "o/r", Number: 7, Checks: ChecksFailed, RequestedTeams: []string{"o/t"}}}
	c.Write("mine", want)
	if prs, ok := c.Read("mine"); !ok || !reflect.DeepEqual(prs, want) {
		t.Errorf("read back %+v", prs)
	}
}
