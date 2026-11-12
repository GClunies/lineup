package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeLinear serves GraphQL; each call gets the next canned reply.
func fakeLinear(t *testing.T, replies ...func(w http.ResponseWriter, r *http.Request)) *[]string {
	t.Helper()
	var auths []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if n >= len(replies) {
			t.Errorf("unexpected extra request %d", n+1)
			w.WriteHeader(500)
			return
		}
		replies[n](w, r)
		n++
	}))
	old := graphqlURL
	graphqlURL = srv.URL
	t.Cleanup(func() { graphqlURL = old; srv.Close() })
	return &auths
}

func reply(status int, body any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

func data(d any) map[string]any { return map[string]any{"data": d} }

// vars decodes the variables of a request, then answers with r.
func vars(into *map[string]any, r func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		*into = body.Variables
		r(w, req)
	}
}

func signedInFor(t *testing.T) *Client {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	return NewClient(acme)
}

func TestQueryRefreshesOnceOnAuthErrorThenRetries(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "fresh", "refresh_token": "rt2", "expires_in": 3600}
	})
	auths := fakeLinear(t,
		reply(400, map[string]any{"errors": []any{map[string]any{"message": "auth", "extensions": map[string]any{"code": "AUTHENTICATION_ERROR"}}}}),
		reply(200, data(map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{
			map[string]any{"identifier": "ACT-1", "title": "x", "state": map[string]any{"type": "started", "name": "In Progress"}},
		}}}})),
	)
	is, err := NewClient(acme).MyIssues(context.Background())
	if err != nil || len(is) != 1 || is[0].Identifier != "ACT-1" {
		t.Fatalf("got %v, %v", is, err)
	}
	if (*auths)[0] != "Bearer stale" || (*auths)[1] != "Bearer fresh" {
		t.Errorf("auth headers %v", *auths)
	}
}

func TestQueryGivesUpAfterSecondAuthError(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "b", "refresh_token": "rt2", "expires_in": 3600}
	})
	fakeLinear(t, reply(401, map[string]any{}), reply(401, map[string]any{}))
	if _, err := NewClient(acme).MyIssues(context.Background()); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("got %v, want ErrSignedOut", err)
	}
}

func TestQuerySurfacesGraphQLErrors(t *testing.T) {
	c := signedInFor(t)
	fakeLinear(t, reply(200, map[string]any{"errors": []any{map[string]any{"message": "Cannot query field"}}}))
	if _, err := c.TeamProjects(context.Background(), "t1"); err == nil || !strings.Contains(err.Error(), "Cannot query field") {
		t.Fatalf("got %v", err)
	}
}

func relation(typ, id, stateType string) map[string]any {
	return map[string]any{"type": typ, "issue": map[string]any{"id": id, "identifier": strings.ToUpper(id), "state": map[string]any{"type": stateType}}}
}

// Only open "blocks" relations become Blockers: done and canceled ones, and
// other relation types, do not.
func TestBlockersAreTheOpenBlocksRelations(t *testing.T) {
	c := signedInFor(t)
	fakeLinear(t, reply(200, data(map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{
		map[string]any{"id": "p-1", "identifier": "P-1", "state": map[string]any{"type": "unstarted"},
			"inverseRelations": map[string]any{"nodes": []any{
				relation("blocks", "x-1", "started"), relation("blocks", "x-2", "completed"),
				relation("blocks", "x-3", "canceled"), relation("related", "x-4", "started"),
				relation("blocks", "x-5", "unstarted"),
			}}},
	}}}})))
	is, err := c.MyIssues(context.Background())
	if err != nil || len(is) != 1 {
		t.Fatalf("%v %v", is, err)
	}
	if got := strings.Join(BlockedBy(is[0]), " "); got != "X-1 X-5" {
		t.Fatalf("blocked by %q", got)
	}
}

// Unblocked needs one or more "blocks" relations, each done or canceled.
// Other relation types never count.
func TestUnblockedMeansEveryBlocksRelationIsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		rels []any
		want bool
	}{
		{"no relations", nil, false},
		{"one done blocker", []any{relation("blocks", "x-1", "completed")}, true},
		{"one canceled blocker", []any{relation("blocks", "x-1", "canceled")}, true},
		{"one done and one open blocker", []any{relation("blocks", "x-1", "completed"), relation("blocks", "x-2", "started")}, false},
		{"one open blocker", []any{relation("blocks", "x-1", "unstarted")}, false},
		{"a done related Issue only", []any{relation("related", "x-1", "completed")}, false},
		{"a done blocker and an open related Issue", []any{relation("blocks", "x-1", "completed"), relation("related", "x-2", "started")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := signedInFor(t)
			fakeLinear(t, reply(200, data(map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{
				map[string]any{"id": "p-1", "identifier": "P-1", "state": map[string]any{"type": "unstarted"},
					"inverseRelations": map[string]any{"nodes": tc.rels}},
			}}}})))
			is, err := c.MyIssues(context.Background())
			if err != nil || len(is) != 1 {
				t.Fatalf("%v %v", is, err)
			}
			if is[0].Unblocked != tc.want {
				t.Fatalf("unblocked = %v, want %v", is[0].Unblocked, tc.want)
			}
		})
	}
}

func TestMyIssuesAsksForOpenIssuesAndPages(t *testing.T) {
	c := signedInFor(t)
	var first, second map[string]any
	page := func(has bool, cursor string, id string) map[string]any {
		return data(map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
			"nodes":    []any{map[string]any{"id": id, "identifier": id}},
			"pageInfo": map[string]any{"hasNextPage": has, "endCursor": cursor},
		}}})
	}
	fakeLinear(t,
		vars(&first, func(w http.ResponseWriter, r *http.Request) { reply(200, page(true, "c1", "A-1"))(w, r) }),
		vars(&second, func(w http.ResponseWriter, r *http.Request) { reply(200, page(false, "", "A-2"))(w, r) }),
	)
	is, err := c.MyIssues(context.Background())
	if err != nil || len(is) != 2 {
		t.Fatalf("%v %v", is, err)
	}
	filter, _ := json.Marshal(first["filter"])
	if string(filter) != `{"state":{"type":{"nin":["completed","canceled"]}}}` || first["after"] != nil || second["after"] != "c1" {
		t.Errorf("filter %s, after %v then %v", filter, first["after"], second["after"])
	}
}

func TestPagingThatStopsMovingIsIncomplete(t *testing.T) {
	c := signedInFor(t)
	stuck := reply(200, data(map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
		"nodes":    []any{map[string]any{"id": "A-1"}},
		"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "same"},
	}}}))
	fakeLinear(t, stuck, stuck, stuck)
	is, err := c.MyIssues(context.Background())
	if !errors.Is(err, ErrIncomplete) || len(is) == 0 {
		t.Fatalf("%v %v", is, err)
	}
}

func TestIssueReadsByIdentifierWithDescription(t *testing.T) {
	c := signedInFor(t)
	var sent map[string]any
	fakeLinear(t, vars(&sent, reply(200, data(map[string]any{"issue": map[string]any{
		"id": "uuid-1", "identifier": "ENG-123", "title": "T", "description": "line one\n\x1b]52;c;AA\x07line two",
		"branchName": "ada/eng-123-t", "team": map[string]any{"id": "t1", "key": "ENG"},
	}}))))
	is, err := c.Issue(context.Background(), "ENG-123")
	if err != nil {
		t.Fatal(err)
	}
	if sent["id"] != "ENG-123" || is.ID != "uuid-1" || is.BranchName != "ada/eng-123-t" || is.Team.Key != "ENG" {
		t.Fatalf("sent %v, got %+v", sent, is)
	}
	if is.Description != "line one\nline two" {
		t.Fatalf("description %q", is.Description)
	}
}

func TestHostileTextIsCleanedAtDecode(t *testing.T) {
	const osc52 = "\x1b]52;c;cm0gLXJmIH4=\x07"
	c := signedInFor(t)
	fakeLinear(t, reply(200, data(map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{map[string]any{
		"id": "i1", "identifier": "ENG-1" + osc52, "title": "Fix\x1b[2Jit" + osc52 + "\nnow",
		"state":    map[string]any{"name": "In Progress" + osc52, "type": "started"},
		"project":  map[string]any{"id": "p", "name": "Proj" + osc52},
		"assignee": map[string]any{"id": "u", "displayName": "Mallory" + osc52},
	}}}}})))
	is, err := c.MyIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if x := is[0]; x.Title != "Fixit now" || x.Identifier != "ENG-1" || x.Assignee.Name != "Mallory" || x.Project.Name != "Proj" || x.State.Name != "In Progress" {
		t.Fatalf("%+v", x)
	}
}

func states() map[string]any {
	return data(map[string]any{"team": map[string]any{"states": map[string]any{"nodes": []any{
		map[string]any{"id": "done", "name": "Done", "type": "completed", "position": 1},
		map[string]any{"id": "review", "name": "In Review", "type": "started", "position": 3},
		map[string]any{"id": "progress", "name": "In Progress", "type": "started", "position": 2},
		map[string]any{"id": "todo", "name": "Todo", "type": "unstarted", "position": 1},
	}}}})
}

func TestTeamStatesComeInLifecycleOrder(t *testing.T) {
	c := signedInFor(t)
	fakeLinear(t, reply(200, states()))
	ss, err := c.TeamStates(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range ss {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "Todo,In Progress,In Review,Done" {
		t.Fatalf("%s", got)
	}
}

func TestSetStatusByNameFindsTheTeamsStateIgnoringCase(t *testing.T) {
	c := signedInFor(t)
	var mutation map[string]any
	fakeLinear(t,
		reply(200, states()),
		vars(&mutation, reply(200, data(map[string]any{"issueUpdate": map[string]any{"success": true}}))),
	)
	is := Issue{ID: "issue-9", Identifier: "ACT-9", Team: Team{ID: "t1", Key: "ACT"}}
	if err := c.SetStatusByName(context.Background(), is, "in review"); err != nil {
		t.Fatal(err)
	}
	input, _ := mutation["input"].(map[string]any)
	if mutation["id"] != "issue-9" || input["stateId"] != "review" || len(input) != 1 {
		t.Fatalf("mutation %v", mutation)
	}
}

func TestSetStatusByNameNamesTheStatesWhenOneIsMissing(t *testing.T) {
	c := signedInFor(t)
	fakeLinear(t, reply(200, states())) // no update follows
	err := c.SetStatusByName(context.Background(), Issue{ID: "i", Team: Team{ID: "t1", Key: "ACT"}}, "Planning")
	if err == nil || !strings.Contains(err.Error(), `"Planning"`) || !strings.Contains(err.Error(), "In Progress") {
		t.Fatalf("%v", err)
	}
}

func TestAssignToMeSendsTheViewerID(t *testing.T) {
	c := signedInFor(t)
	var mutation map[string]any
	fakeLinear(t,
		reply(200, data(map[string]any{"viewer": map[string]any{"id": "me"}})),
		vars(&mutation, reply(200, data(map[string]any{"issueUpdate": map[string]any{"success": true}}))),
	)
	if err := c.AssignToMe(context.Background(), "issue-9"); err != nil {
		t.Fatal(err)
	}
	input, _ := mutation["input"].(map[string]any)
	if mutation["id"] != "issue-9" || input["assigneeId"] != "me" || len(input) != 1 {
		t.Fatalf("mutation %v", mutation)
	}
	// The viewer is read once per client.
	fakeLinear(t, vars(&mutation, reply(200, data(map[string]any{"issueUpdate": map[string]any{"success": true}}))))
	if err := c.AssignToMe(context.Background(), "issue-10"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateSendsOnlyWhatWasGiven(t *testing.T) {
	c := signedInFor(t)
	var mutation map[string]any
	fakeLinear(t,
		reply(200, data(map[string]any{"viewer": map[string]any{"id": "me"}})),
		vars(&mutation, reply(200, data(map[string]any{"issueCreate": map[string]any{"success": true, "issue": map[string]any{
			"id": "new-1", "identifier": "ENG-700", "title": "Quick", "team": map[string]any{"id": "t1", "key": "ENG"},
		}}}))),
	)
	is, err := c.Create(context.Background(), NewIssue{TeamID: "t1", Title: "Quick", AssignToMe: true})
	if err != nil || is.Identifier != "ENG-700" {
		t.Fatalf("%+v %v", is, err)
	}
	input, _ := mutation["input"].(map[string]any)
	if len(input) != 3 || input["teamId"] != "t1" || input["title"] != "Quick" || input["assigneeId"] != "me" {
		t.Fatalf("input %v", input)
	}
}

func TestCreateReportsARefusal(t *testing.T) {
	c := signedInFor(t)
	fakeLinear(t, reply(200, data(map[string]any{"issueCreate": map[string]any{"success": false}})))
	if _, err := c.Create(context.Background(), NewIssue{TeamID: "t1", Title: "x"}); err == nil {
		t.Fatal("want an error")
	}
}
