package linear

import (
	"context"
	"errors"
	"sort"
)

type ViewKind int

const (
	ViewIssues ViewKind = iota
	ViewProjects
)

// View is one of the user's favorite Linear views. The planner shows each as a tab.
type View struct {
	// ID is the stable key of the user's rank: the custom view's ID, or
	// "<predefinedViewType>:<teamID>".
	ID string
	// Name is the custom view's name. A predefined view is named by its team
	// key and its kind, such as "ENG Issues".
	Name string
	Kind ViewKind
	URL  string
	Team Team // zero when the view has no team
	// CustomID is the custom view's ID. It is empty for a predefined view.
	CustomID string
	// Predefined is the predefinedViewType, such as "activeIssues". It is empty for a custom view.
	Predefined string
}

type predefinedView struct {
	kind   ViewKind
	name   string
	filter map[string]any // the Issues of an Issue view
}

// predefinedViews lists the predefined team views that lineup can show. A
// favorite of any other predefined type is left out.
var predefinedViews = map[string]predefinedView{
	"activeIssues": {ViewIssues, "Issues", map[string]any{"state": map[string]any{"type": map[string]any{"in": []string{"unstarted", "started"}}}}},
	"projects":     {ViewProjects, "Projects", nil},
}

// customKinds maps a custom view's modelName to the kind lineup shows.
var customKinds = map[string]ViewKind{"Issue": ViewIssues, "Project": ViewProjects}

type favoriteWire struct {
	SortOrder          float64 `json:"sortOrder"`
	URL                string  `json:"url"`
	PredefinedViewType string  `json:"predefinedViewType"`
	PredefinedViewTeam *Team   `json:"predefinedViewTeam"`
	CustomView         *struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ModelName string `json:"modelName"`
		Team      *Team  `json:"team"`
	} `json:"customView"`
}

// view returns the View a favorite names. ok is false for a favorite that is
// not a view lineup can show.
func (f favoriteWire) view() (View, bool) {
	if cv := f.CustomView; cv != nil {
		kind, ok := customKinds[cv.ModelName]
		if !ok {
			return View{}, false
		}
		v := View{ID: cv.ID, Name: cv.Name, Kind: kind, URL: f.URL, CustomID: cv.ID}
		if cv.Team != nil {
			v.Team = *cv.Team
		}
		return v, true
	}
	p, ok := predefinedViews[f.PredefinedViewType]
	// Both supported predefined views read one team's Issues or Projects.
	if !ok || f.PredefinedViewTeam == nil {
		return View{}, false
	}
	team := *f.PredefinedViewTeam
	return View{ID: f.PredefinedViewType + ":" + team.ID, Name: team.Key + " " + p.name, Kind: p.kind,
		URL: f.URL, Team: team, Predefined: f.PredefinedViewType}, true
}

// Views lists the user's favorite views that lineup can show, in the order of
// Linear's sidebar.
func (c *Client) Views(ctx context.Context) ([]View, error) {
	type reply struct {
		Favorites connection[favoriteWire] `json:"favorites"`
	}
	q := `query($after: String) { favorites(first: 100, after: $after) { nodes {
  sortOrder url predefinedViewType predefinedViewTeam { id key }
  customView { id name modelName team { id key } } } ` + pageFields + ` } }`
	fs, err := fetchAll(ctx, c, q, nil, func(r *reply) *connection[favoriteWire] { return &r.Favorites })
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].SortOrder < fs[j].SortOrder })
	var out []View
	for _, f := range fs {
		if v, ok := f.view(); ok {
			out = append(out, v)
		}
	}
	return out, err
}

var errNotIssueView = errors.New("this view does not list Issues")

// ViewIssues lists the Issues of an Issue view. Linear applies a custom
// view's own filter.
func (c *Client) ViewIssues(ctx context.Context, v View) ([]Issue, error) {
	if v.CustomID != "" {
		type reply struct {
			CustomView struct {
				Issues connection[issueWire] `json:"issues"`
			} `json:"customView"`
		}
		q := `query($id: String!, $after: String) { customView(id: $id) { issues(first: 100, after: $after) {
  nodes { ...IssueFields } ` + pageFields + ` } } }` + issueFields
		ws, err := fetchAll(ctx, c, q, map[string]any{"id": v.CustomID}, func(r *reply) *connection[issueWire] { return &r.CustomView.Issues })
		return issues(ws), err
	}
	p, ok := predefinedViews[v.Predefined]
	if !ok || p.kind != ViewIssues {
		return nil, errNotIssueView
	}
	type reply struct {
		Team struct {
			Issues connection[issueWire] `json:"issues"`
		} `json:"team"`
	}
	q := `query($id: String!, $after: String, $filter: IssueFilter) { team(id: $id) { issues(first: 100, after: $after, filter: $filter) {
  nodes { ...IssueFields } ` + pageFields + ` } } }` + issueFields
	ws, err := fetchAll(ctx, c, q, map[string]any{"id": v.Team.ID, "filter": p.filter}, func(r *reply) *connection[issueWire] { return &r.Team.Issues })
	return issues(ws), err
}

// ViewProjects lists the Projects of a Project view.
func (c *Client) ViewProjects(ctx context.Context, v View) ([]ProjectInfo, error) {
	if v.CustomID == "" {
		return c.TeamProjects(ctx, v.Team.ID)
	}
	type reply struct {
		CustomView struct {
			Projects connection[projectWire] `json:"projects"`
		} `json:"customView"`
	}
	q := `query($id: String!, $after: String) { customView(id: $id) { projects(first: 100, after: $after) {
  nodes { ...ProjectFields } ` + pageFields + ` } } }` + projectFields
	ws, err := fetchAll(ctx, c, q, map[string]any{"id": v.CustomID}, func(r *reply) *connection[projectWire] { return &r.CustomView.Projects })
	return projects(ws), err
}
