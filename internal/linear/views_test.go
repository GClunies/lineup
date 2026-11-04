package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// body decodes the query and variables of a request, then answers with r.
func body(q *string, vs *map[string]any, r func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		var b struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(req.Body).Decode(&b)
		*q, *vs = b.Query, b.Variables
		r(w, req)
	}
}

func TestViewsAreTheShowableFavoritesInSidebarOrder(t *testing.T) {
	c := signedInFor(t)
	pd := map[string]any{"id": "t1", "key": "ENG"}
	fakeLinear(t, reply(200, data(map[string]any{"favorites": map[string]any{"nodes": []any{
		map[string]any{"sortOrder": 4, "url": "u/projects", "predefinedViewType": "projects", "predefinedViewTeam": pd},
		map[string]any{"sortOrder": 1, "url": "u/ada", "customView": map[string]any{"id": "cv1", "name": "Current+ (Ada)", "modelName": "Issue"}},
		map[string]any{"sortOrder": 2, "url": "u/team", "customView": map[string]any{"id": "cv2", "name": "Roadmap", "modelName": "Project", "team": pd}},
		map[string]any{"sortOrder": 3, "url": "u/active", "predefinedViewType": "activeIssues", "predefinedViewTeam": pd},
		map[string]any{"sortOrder": 0, "url": "u/issue"},
		map[string]any{"sortOrder": 5, "predefinedViewType": "backlog", "predefinedViewTeam": pd},
		map[string]any{"sortOrder": 6, "predefinedViewType": "activeIssues"},
		map[string]any{"sortOrder": 7, "customView": map[string]any{"id": "cv3", "name": "Goals", "modelName": "Initiative"}},
	}}})))
	vs, err := c.Views(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []View{
		{ID: "cv1", Name: "Current+ (Ada)", Kind: ViewIssues, URL: "u/ada", CustomID: "cv1"},
		{ID: "cv2", Name: "Roadmap", Kind: ViewProjects, URL: "u/team", Team: Team{ID: "t1", Key: "ENG"}, CustomID: "cv2"},
		{ID: "activeIssues:t1", Name: "ENG Issues", Kind: ViewIssues, URL: "u/active", Team: Team{ID: "t1", Key: "ENG"}, Predefined: "activeIssues"},
		{ID: "projects:t1", Name: "ENG Projects", Kind: ViewProjects, URL: "u/projects", Team: Team{ID: "t1", Key: "ENG"}, Predefined: "projects"},
	}
	if len(vs) != len(want) {
		t.Fatalf("got %+v", vs)
	}
	for i := range want {
		if vs[i] != want[i] {
			t.Errorf("view %d = %+v, want %+v", i, vs[i], want[i])
		}
	}
}

func TestViewIssuesOfACustomViewReadsTheViewAndItsChips(t *testing.T) {
	c := signedInFor(t)
	var q string
	var sent map[string]any
	fakeLinear(t, body(&q, &sent, reply(200, data(map[string]any{"customView": map[string]any{"issues": map[string]any{"nodes": []any{
		map[string]any{"id": "i1", "identifier": "ENG-418", "prioritySortOrder": -5.5, "estimate": 5, "createdAt": "2026-10-17T15:04:05Z",
			"cycle":  map[string]any{"name": "Cycle 10", "number": 9, "startsAt": "2026-10-28T07:00:00Z", "endsAt": "2026-11-11T08:00:00Z"},
			"labels": map[string]any{"nodes": []any{map[string]any{"name": "Bug", "color": "#eb5757"}}},
			"attachments": map[string]any{"nodes": []any{
				map[string]any{"url": "https://github.com/o/r/pull/4810", "sourceType": "github", "metadata": map[string]any{"number": 4810, "status": "inReview"}},
				map[string]any{"url": "https://slack.com/x", "sourceType": "slack", "metadata": map[string]any{"number": 3}},
				map[string]any{"url": "https://github.com/o/r/commit/1", "sourceType": "github", "metadata": map[string]any{"sha": "abc"}},
				map[string]any{"url": "https://github.com/o/r/pull/9", "sourceType": "github", "metadata": map[string]any{"number": "odd"}},
			}}},
	}}}}))))
	is, err := c.ViewIssues(context.Background(), View{ID: "cv1", CustomID: "cv1", Kind: ViewIssues})
	if err != nil || len(is) != 1 {
		t.Fatalf("%+v %v", is, err)
	}
	if !strings.Contains(q, "customView(id: $id)") || sent["id"] != "cv1" || sent["filter"] != nil {
		t.Fatalf("query %s vars %v", q, sent)
	}
	x := is[0]
	if x.PrioritySortOrder != -5.5 || x.Estimate != 5 || !x.CreatedAt.Equal(time.Date(2026, 10, 17, 15, 4, 5, 0, time.UTC)) {
		t.Errorf("fields %+v", x)
	}
	if x.Cycle == nil || x.Cycle.Name != "Cycle 10" || x.Cycle.EndsAt.Day() != 11 {
		t.Errorf("cycle %+v", x.Cycle)
	}
	if len(x.Labels) != 1 || x.Labels[0] != (Label{"Bug", "#eb5757"}) {
		t.Errorf("labels %+v", x.Labels)
	}
	if len(x.PRs) != 1 || x.PRs[0] != (PR{4810, "inReview", "https://github.com/o/r/pull/4810"}) {
		t.Errorf("PRs %+v", x.PRs)
	}
}

func TestViewIssuesOfPredefinedActiveIssuesFiltersTheTeam(t *testing.T) {
	c := signedInFor(t)
	var q string
	var sent map[string]any
	fakeLinear(t, body(&q, &sent, reply(200, data(map[string]any{"team": map[string]any{"issues": map[string]any{"nodes": []any{
		map[string]any{"id": "i1", "identifier": "ENG-1"},
	}}}}))))
	is, err := c.ViewIssues(context.Background(), View{ID: "activeIssues:t1", Predefined: "activeIssues", Team: Team{ID: "t1", Key: "ENG"}})
	if err != nil || len(is) != 1 {
		t.Fatalf("%+v %v", is, err)
	}
	filter, _ := json.Marshal(sent["filter"])
	if !strings.Contains(q, "team(id: $id)") || sent["id"] != "t1" || string(filter) != `{"state":{"type":{"in":["unstarted","started"]}}}` {
		t.Fatalf("query %s vars %v", q, sent)
	}
}

func TestViewProjectsOfACustomViewReadsHealthAndLead(t *testing.T) {
	c := signedInFor(t)
	var q string
	var sent map[string]any
	fakeLinear(t, body(&q, &sent, reply(200, data(map[string]any{"customView": map[string]any{"projects": map[string]any{"nodes": []any{
		map[string]any{"id": "p1", "name": "Billing", "url": "u", "priority": 2, "prioritySortOrder": 7.5, "health": "atRisk",
			"targetDate": "2026-11-15", "progress": 0.42, "lead": map[string]any{"name": "ada"},
			"status": map[string]any{"name": "In Progress", "type": "started", "position": 4000}},
	}}}}))))
	ps, err := c.ViewProjects(context.Background(), View{ID: "cv2", CustomID: "cv2", Kind: ViewProjects})
	if err != nil || len(ps) != 1 {
		t.Fatalf("%+v %v", ps, err)
	}
	if !strings.Contains(q, "customView(id: $id)") || sent["id"] != "cv2" {
		t.Fatalf("query %s vars %v", q, sent)
	}
	want := ProjectInfo{ID: "p1", Name: "Billing", URL: "u", Status: "In Progress", StatusType: "started", StatusPosition: 4000,
		Priority: 2, PrioritySortOrder: 7.5, Health: "atRisk", Lead: "ada", TargetDate: "2026-11-15", Progress: 0.42}
	if ps[0] != want {
		t.Fatalf("got  %+v\nwant %+v", ps[0], want)
	}
}

func TestViewProjectsOfPredefinedProjectsReadsTheTeam(t *testing.T) {
	c := signedInFor(t)
	var q string
	var sent map[string]any
	fakeLinear(t, body(&q, &sent, reply(200, data(map[string]any{"team": map[string]any{"projects": map[string]any{"nodes": []any{
		map[string]any{"id": "p1", "name": "Billing", "status": map[string]any{"name": "Planned", "type": "planned"}},
	}}}}))))
	ps, err := c.ViewProjects(context.Background(), View{ID: "projects:t1", Predefined: "projects", Kind: ViewProjects, Team: Team{ID: "t1"}})
	if err != nil || len(ps) != 1 || ps[0].StatusType != "planned" {
		t.Fatalf("%+v %v", ps, err)
	}
	if !strings.Contains(q, "team(id: $id)") || sent["id"] != "t1" {
		t.Fatalf("query %s vars %v", q, sent)
	}
}

// The disk cache stores Issues as JSON, so every field must come back.
func TestIssueFieldsSurviveTheCache(t *testing.T) {
	dir := t.TempDir()
	old := stateDir
	stateDir = func() string { return dir }
	t.Cleanup(func() { stateDir = old })
	is := Issue{ID: "i1", Identifier: "ENG-1", PrioritySortOrder: -3, Estimate: 2, CreatedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC),
		Cycle:  &Cycle{Name: "Cycle 10", StartsAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)},
		Labels: []Label{{"Bug", "#eb5757"}}, PRs: []PR{{4810, "merged", "u"}}}
	WriteCache("w1/view:cv1", []Issue{is})
	got, ok := ReadCached("w1/view:cv1")
	if !ok || len(got) != 1 {
		t.Fatalf("%v %v", got, ok)
	}
	g := got[0]
	if g.PrioritySortOrder != -3 || g.Estimate != 2 || !g.CreatedAt.Equal(is.CreatedAt) || g.Cycle == nil || g.Cycle.Name != "Cycle 10" ||
		len(g.Labels) != 1 || g.Labels[0] != is.Labels[0] || len(g.PRs) != 1 || g.PRs[0] != is.PRs[0] {
		t.Fatalf("got %+v", g)
	}
}
