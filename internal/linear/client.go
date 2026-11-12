// Package linear is lineup's Linear client: OAuth sign-in, the GraphQL calls
// the tracker browser and the Scheduler need, the browser's Issue order, and a
// disk cache. It is a trimmed copy of the herdr-linear fork.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gclunies/lineup/internal/termsafe"
)

var graphqlURL = "https://api.linear.app/graphql" // a var so tests can point it elsewhere

// Client calls Linear as one signed-in workspace.
type Client struct {
	ws Workspace

	mu     sync.Mutex
	viewer string
}

func NewClient(ws Workspace) *Client { return &Client{ws: ws} }

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

func isAuthError(status int, errs []gqlError) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	for _, e := range errs {
		if e.Extensions.Code == "AUTHENTICATION_ERROR" {
			return true
		}
	}
	return false
}

// query runs one GraphQL operation. A rejected token is refreshed once and the
// request retried. A second rejection means the grant is gone.
func (c *Client) query(ctx context.Context, q string, vars map[string]any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := accessToken(ctx, account(c.ws.ID), attempt > 0)
		if err != nil {
			return err
		}
		status, errs, data, err := c.post(ctx, token, q, vars)
		if err != nil {
			return err
		}
		if isAuthError(status, errs) {
			if attempt == 0 {
				continue
			}
			return ErrSignedOut
		}
		if len(errs) > 0 {
			return fmt.Errorf("Linear: %s", termsafe.Clean(errs[0].Message, false))
		}
		if status != http.StatusOK {
			return fmt.Errorf("Linear: HTTP %d", status)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
		termsafe.Sanitize(out) // other people's text: no escape sequences reach the terminal
		return nil
	}
	return ErrSignedOut
}

func (c *Client) post(ctx context.Context, token, q string, vars map[string]any) (int, []gqlError, json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil && res.StatusCode == http.StatusOK {
		return 0, nil, nil, fmt.Errorf("Linear: bad response: %w", err)
	}
	return res.StatusCode, reply.Errors, reply.Data, nil
}

// whoami asks Linear which workspace a token belongs to. A var for tests.
var whoami = func(ctx context.Context, accessToken string) (Workspace, error) {
	var reply struct {
		Organization Workspace `json:"organization"`
	}
	status, errs, data, err := (&Client{}).post(ctx, accessToken, `query { organization { id name urlKey } }`, nil)
	switch {
	case err != nil:
		return Workspace{}, err
	case isAuthError(status, errs):
		return Workspace{}, ErrSignedOut
	case len(errs) > 0:
		return Workspace{}, fmt.Errorf("Linear: %s", termsafe.Clean(errs[0].Message, false))
	case status != http.StatusOK:
		return Workspace{}, fmt.Errorf("Linear: HTTP %d", status)
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return Workspace{}, err
	}
	termsafe.Sanitize(&reply)
	if reply.Organization.ID == "" {
		return Workspace{}, errors.New("Linear didn't say which workspace this is")
	}
	return reply.Organization, nil
}

// ── model ─────────────────────────────────────────────────────────────────────

type State struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"` // triage | backlog | unstarted | started | completed | canceled
	Position float64 `json:"position"`
}

// Team is a Linear team. Name is set only by Teams.
type Team struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Person struct {
	ID   string `json:"id"`
	Name string `json:"displayName"`
	// FullName is the person's name, such as "Ada Lovelace".
	FullName string `json:"name"`
	IsMe     bool   `json:"isMe"`
}

// Ref names another Issue.
type Ref struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
}

type Issue struct {
	ID          string   `json:"id"`
	Identifier  string   `json:"identifier"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	BranchName  string   `json:"branchName"`
	Description string   `json:"description,omitempty"` // set by Issue only
	Priority    int      `json:"priority"`              // 1 urgent … 4 low, 0 none
	State       State    `json:"state"`
	Team        Team     `json:"team"`
	Project     *Project `json:"project"`
	Assignee    *Person  `json:"assignee"`
	// Blockers are the open Issues that block this one through a "blocks"
	// relation, in any Project. Linear's first 20 relations are read.
	Blockers []Ref `json:"blockers,omitempty"`
	// Unblocked is true when one or more "blocks" relations point at the
	// Issue and every one of their Issues is done or canceled.
	Unblocked         bool      `json:"unblocked,omitempty"`
	PrioritySortOrder float64   `json:"prioritySortOrder"`
	Estimate          float64   `json:"estimate"` // 0 is no estimate
	CreatedAt         time.Time `json:"createdAt"`
	Cycle             *Cycle    `json:"cycle"`
	Labels            []Label   `json:"labels,omitempty"`
	// PRs are the GitHub pull requests linked to the Issue, from its first 10 attachments.
	PRs []PR `json:"prs,omitempty"`
}

// Cycle is the Issue's cycle. Name is Linear's display name, such as
// "Cycle 10". Linear's cycle number can differ from the number in the name.
type Cycle struct {
	Name     string    `json:"name"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
}

type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"` // hex, such as "#bec2c8"
}

// PR is a GitHub pull request linked to an Issue.
type PR struct {
	Number int    `json:"number"`
	Status string `json:"status"` // Linear's word, such as draft, inReview, merged, closed
	URL    string `json:"url"`
}

// BlockedBy lists the identifiers of the open Issues that block is, for "blocked by ENG-590".
func BlockedBy(is Issue) []string {
	out := make([]string, len(is.Blockers))
	for i, b := range is.Blockers {
		out[i] = b.Identifier
	}
	return out
}

func isOpen(stateType string) bool { return stateType != "completed" && stateType != "canceled" }

// issueWire is an Issue as Linear sends it, with the relations pointing at
// it. Its connection fields shadow the Issue fields of the same JSON name.
type issueWire struct {
	Issue
	Labels struct {
		Nodes []Label `json:"nodes"`
	} `json:"labels"`
	Attachments struct {
		Nodes []struct {
			URL        string          `json:"url"`
			SourceType string          `json:"sourceType"`
			Metadata   json.RawMessage `json:"metadata"`
		} `json:"nodes"`
	} `json:"attachments"`
	InverseRelations struct {
		Nodes []struct {
			Type  string `json:"type"` // blocks | duplicate | related | similar
			Issue struct {
				Ref
				State State `json:"state"`
			} `json:"issue"`
		} `json:"nodes"`
	} `json:"inverseRelations"`
}

func (w issueWire) issue() Issue {
	is := w.Issue
	closed := 0
	for _, r := range w.InverseRelations.Nodes {
		switch {
		case r.Type != "blocks":
		case isOpen(r.Issue.State.Type):
			is.Blockers = append(is.Blockers, r.Issue.Ref)
		default:
			closed++
		}
	}
	is.Unblocked = closed > 0 && len(is.Blockers) == 0
	is.Labels = w.Labels.Nodes
	for _, a := range w.Attachments.Nodes {
		// metadata is free-form JSON, so one odd attachment must not fail the list.
		var md struct {
			Number json.Number `json:"number"`
			Status string      `json:"status"`
		}
		if a.SourceType != "github" || json.Unmarshal(a.Metadata, &md) != nil {
			continue
		}
		if n, err := md.Number.Int64(); err == nil && n > 0 {
			is.PRs = append(is.PRs, PR{Number: int(n), Status: md.Status, URL: a.URL})
		}
	}
	return is
}

func issues(ws []issueWire) []Issue {
	out := make([]Issue, len(ws))
	for i, w := range ws {
		out[i] = w.issue()
	}
	return out
}

const issueFields = `
fragment IssueFields on Issue {
  id identifier title branchName url priority prioritySortOrder estimate createdAt
  state { id name type position }
  team { id key }
  project { id name }
  assignee { id displayName name isMe }
  inverseRelations(first: 20) { nodes { type issue { id identifier state { type } } } }
  cycle { name startsAt endsAt }
  labels(first: 10) { nodes { name color } }
  attachments(first: 10) { nodes { url sourceType metadata } }
}`

var openIssues = map[string]any{"state": map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}}}

// ── paging ────────────────────────────────────────────────────────────────────

// maxItems bounds any one list. Past it the list comes with ErrTruncated:
// a missing Issue in a task browser is worse than a visible limit.
const maxItems = 1000

var ErrTruncated = fmt.Errorf("showing the first %d; open Linear for the rest", maxItems)

// ErrIncomplete: Linear's paging stopped moving, so the list may lack items.
var ErrIncomplete = errors.New("Linear's list didn't page through to the end, so some items may be missing")

type connection[T any] struct {
	Nodes    []T `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

const pageFields = `pageInfo { hasNextPage endCursor }`

// fetchAll follows a connection's cursor to the end or to maxItems. q must
// take `$after: String` and select pageFields. conn finds the connection in
// the decoded reply R. A list cut short comes back with its error.
func fetchAll[T, R any](ctx context.Context, c *Client, q string, vars map[string]any, conn func(*R) *connection[T]) ([]T, error) {
	var all []T
	var after any
	seen := map[string]bool{}
	for {
		v := map[string]any{"after": after}
		for k, x := range vars {
			v[k] = x
		}
		var res R
		if err := c.query(ctx, q, v, &res); err != nil {
			return nil, err
		}
		page := conn(&res)
		all = append(all, page.Nodes...)
		if !page.PageInfo.HasNextPage || page.PageInfo.EndCursor == "" {
			return all, nil
		}
		// A page with nothing new, or a repeated cursor, would page forever.
		if len(page.Nodes) == 0 || seen[page.PageInfo.EndCursor] {
			return all, ErrIncomplete
		}
		seen[page.PageInfo.EndCursor] = true
		if len(all) >= maxItems {
			return all[:maxItems], ErrTruncated
		}
		after = page.PageInfo.EndCursor
	}
}

// ── reads ─────────────────────────────────────────────────────────────────────

// MyIssues lists the open Issues assigned to the user, in Linear's order.
func (c *Client) MyIssues(ctx context.Context) ([]Issue, error) {
	type reply struct {
		Viewer struct {
			AssignedIssues connection[issueWire] `json:"assignedIssues"`
		} `json:"viewer"`
	}
	q := `query($after: String, $filter: IssueFilter) { viewer { assignedIssues(first: 100, after: $after, orderBy: updatedAt, filter: $filter) {
  nodes { ...IssueFields } ` + pageFields + ` } } }` + issueFields
	ws, err := fetchAll(ctx, c, q, map[string]any{"filter": openIssues}, func(r *reply) *connection[issueWire] { return &r.Viewer.AssignedIssues })
	return issues(ws), err
}

// ProjectIssues lists the open Issues in a Project, anyone's.
func (c *Client) ProjectIssues(ctx context.Context, projectID string) ([]Issue, error) {
	type reply struct {
		Project struct {
			Issues connection[issueWire] `json:"issues"`
		} `json:"project"`
	}
	q := `query($id: String!, $after: String, $filter: IssueFilter) { project(id: $id) { issues(first: 100, after: $after, orderBy: updatedAt, filter: $filter) {
  nodes { ...IssueFields } ` + pageFields + ` } } }` + issueFields
	ws, err := fetchAll(ctx, c, q, map[string]any{"id": projectID, "filter": openIssues}, func(r *reply) *connection[issueWire] { return &r.Project.Issues })
	return issues(ws), err
}

// Issue reads one Issue, with its description, by identifier such as "ENG-123".
func (c *Client) Issue(ctx context.Context, identifier string) (Issue, error) {
	var res struct {
		Issue issueWire `json:"issue"`
	}
	q := `query($id: String!) { issue(id: $id) { ...IssueFields description } }` + issueFields
	if err := c.query(ctx, q, map[string]any{"id": identifier}, &res); err != nil {
		return Issue{}, err
	}
	return res.Issue.issue(), nil
}

// Teams lists the workspace's teams by name.
func (c *Client) Teams(ctx context.Context) ([]Team, error) {
	type reply struct {
		Teams connection[Team] `json:"teams"`
	}
	q := `query($after: String) { teams(first: 100, after: $after) { nodes { id key name } ` + pageFields + ` } }`
	ts, err := fetchAll(ctx, c, q, nil, func(r *reply) *connection[Team] { return &r.Teams })
	sort.SliceStable(ts, func(i, j int) bool { return strings.ToLower(ts[i].Name) < strings.ToLower(ts[j].Name) })
	return ts, err
}

// ProjectInfo is a Project as the browser lists it.
type ProjectInfo struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	URL               string  `json:"url"`
	Status            string  `json:"status"`
	StatusType        string  `json:"statusType"` // backlog | planned | started | paused | completed | canceled
	StatusPosition    float64 `json:"statusPosition"`
	Priority          int     `json:"priority"` // 1 urgent … 4 low, 0 none
	PrioritySortOrder float64 `json:"prioritySortOrder"`
	Health            string  `json:"health"` // onTrack | atRisk | offTrack, or empty
	Lead              string  `json:"lead"`
	TargetDate        string  `json:"targetDate"` // YYYY-MM-DD, or empty
	Progress          float64 `json:"progress"`   // 0 to 1
}

// projectWire is a Project as projectFields selects it.
type projectWire struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	URL               string  `json:"url"`
	Priority          int     `json:"priority"`
	PrioritySortOrder float64 `json:"prioritySortOrder"`
	Health            string  `json:"health"`
	TargetDate        string  `json:"targetDate"`
	Progress          float64 `json:"progress"`
	Lead              *struct {
		Name string `json:"name"`
	} `json:"lead"`
	Status struct {
		Name     string  `json:"name"`
		Type     string  `json:"type"`
		Position float64 `json:"position"`
	} `json:"status"`
}

const projectFields = `
fragment ProjectFields on Project {
  id name url priority prioritySortOrder health targetDate progress
  lead { name }
  status { name type position }
}`

func projects(ws []projectWire) []ProjectInfo {
	out := make([]ProjectInfo, len(ws))
	for i, w := range ws {
		out[i] = ProjectInfo{ID: w.ID, Name: w.Name, URL: w.URL, Status: w.Status.Name,
			StatusType: w.Status.Type, StatusPosition: w.Status.Position, Priority: w.Priority,
			PrioritySortOrder: w.PrioritySortOrder, Health: w.Health, TargetDate: w.TargetDate, Progress: w.Progress}
		if w.Lead != nil {
			out[i].Lead = w.Lead.Name
		}
	}
	return out
}

// TeamProjects lists the team's open Projects. A large workspace has more
// open Projects than one list holds, so the browser asks one team at a time.
func (c *Client) TeamProjects(ctx context.Context, teamID string) ([]ProjectInfo, error) {
	type reply struct {
		Team struct {
			Projects connection[projectWire] `json:"projects"`
		} `json:"team"`
	}
	q := `query($id: String!, $after: String, $filter: ProjectFilter) { team(id: $id) { projects(first: 100, after: $after, orderBy: updatedAt, filter: $filter) {
  nodes { ...ProjectFields } ` + pageFields + ` } } }` + projectFields
	filter := map[string]any{"status": map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}}}
	ws, err := fetchAll(ctx, c, q, map[string]any{"id": teamID, "filter": filter}, func(r *reply) *connection[projectWire] { return &r.Team.Projects })
	return projects(ws), err
}

// TeamStates lists a team's workflow states in the order work flows: triage,
// backlog, unstarted, started, completed, canceled; Linear's position within each.
func (c *Client) TeamStates(ctx context.Context, teamID string) ([]State, error) {
	var res struct {
		Team struct {
			States struct {
				Nodes []State `json:"nodes"`
			} `json:"states"`
		} `json:"team"`
	}
	q := `query($id: String!) { team(id: $id) { states(first: 250) { nodes { id name type position } } } }`
	if err := c.query(ctx, q, map[string]any{"id": teamID}, &res); err != nil {
		return nil, err
	}
	states := res.Team.States.Nodes
	sort.SliceStable(states, func(i, j int) bool {
		if a, b := lifecycleRank(states[i].Type), lifecycleRank(states[j].Type); a != b {
			return a < b
		}
		return states[i].Position < states[j].Position
	})
	return states, nil
}

func lifecycleRank(t string) int {
	switch t {
	case "triage":
		return 0
	case "backlog":
		return 1
	case "unstarted":
		return 2
	case "started":
		return 3
	case "completed":
		return 4
	case "canceled":
		return 5
	}
	return 6
}

// ViewerID is the signed-in user's ID.
func (c *Client) ViewerID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.viewer != "" {
		return c.viewer, nil
	}
	var me struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	if err := c.query(ctx, `query { viewer { id } }`, nil, &me); err != nil {
		return "", err
	}
	c.viewer = me.Viewer.ID
	return c.viewer, nil
}

// ── writes ────────────────────────────────────────────────────────────────────

// AssignToMe makes the signed-in user the Issue's assignee.
func (c *Client) AssignToMe(ctx context.Context, issueID string) error {
	me, err := c.ViewerID(ctx)
	if err != nil {
		return err
	}
	return c.updateIssue(ctx, issueID, map[string]any{"assigneeId": me})
}

// SetStatusByName moves the Issue to the workflow state of its team with that
// name, ignoring case. The names come from the Linear status map.
func (c *Client) SetStatusByName(ctx context.Context, is Issue, statusName string) error {
	states, err := c.TeamStates(ctx, is.Team.ID)
	if err != nil {
		return err
	}
	names := make([]string, len(states))
	for i, s := range states {
		if strings.EqualFold(s.Name, statusName) {
			return c.updateIssue(ctx, is.ID, map[string]any{"stateId": s.ID})
		}
		names[i] = s.Name
	}
	return fmt.Errorf("team %s has no status %q (it has: %s)", is.Team.Key, statusName, strings.Join(names, ", "))
}

// NewIssue is what quick add sends. Only TeamID and Title are required.
type NewIssue struct {
	TeamID      string
	Title       string
	Description string
	ProjectID   string
	StateID     string
	Priority    int
	AssignToMe  bool
}

// Create files a new Issue and returns it.
func (c *Client) Create(ctx context.Context, n NewIssue) (Issue, error) {
	input := map[string]any{"teamId": n.TeamID, "title": n.Title}
	if n.Description != "" {
		input["description"] = n.Description
	}
	if n.ProjectID != "" {
		input["projectId"] = n.ProjectID
	}
	if n.StateID != "" {
		input["stateId"] = n.StateID
	}
	if n.Priority > 0 {
		input["priority"] = n.Priority
	}
	if n.AssignToMe {
		me, err := c.ViewerID(ctx)
		if err != nil {
			return Issue{}, err
		}
		input["assigneeId"] = me
	}
	var res struct {
		IssueCreate struct {
			Success bool       `json:"success"`
			Issue   *issueWire `json:"issue"`
		} `json:"issueCreate"`
	}
	q := `mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { ...IssueFields } } }` + issueFields
	if err := c.query(ctx, q, map[string]any{"input": input}, &res); err != nil {
		return Issue{}, err
	}
	if !res.IssueCreate.Success || res.IssueCreate.Issue == nil {
		return Issue{}, errors.New("Linear didn't create the issue")
	}
	return res.IssueCreate.Issue.issue(), nil
}

func (c *Client) updateIssue(ctx context.Context, issueID string, input map[string]any) error {
	var res struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	q := `mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`
	if err := c.query(ctx, q, map[string]any{"id": issueID, "input": input}, &res); err != nil {
		return err
	}
	if !res.IssueUpdate.Success {
		return errors.New("Linear refused the update")
	}
	return nil
}
