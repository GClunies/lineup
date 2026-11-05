package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The qualifiers that each kind of PR search starts with. In search(type:
// ISSUE), a repeated repo:, author:, or team-review-requested: qualifier is
// OR, a repeated label: is AND, and boolean OR returns nothing.
const (
	openPRs      = "is:pr is:open archived:false sort:created-desc"
	MyPRs        = openPRs + " author:@me"
	RequestedPRs = openPRs + " review-requested:@me draft:false"
	ViewPRs      = openPRs
	QueuePRs     = openPRs + " draft:false -author:@me"
)

// Filter picks open PRs. Base is one of the searches above. The items of one
// list combine with OR, and two lists combine with AND. Authors and the
// members of AuthorTeams are one list.
type Filter struct {
	Base           string
	Repos          []string // owner/name
	Authors        []string
	AuthorTeams    []string // org/slug
	RequestedTeams []string // org/slug
	Labels         []string
}

// Query builds the search for f, given the members of each team in
// AuthorTeams. ok is false when the author list is empty because each of its
// teams is empty. With no author: qualifier the search would match every
// author, so it must not run.
func (f Filter) Query(members map[string][]string) (q string, ok bool) {
	parts := []string{f.Base}
	for _, r := range f.Repos {
		parts = append(parts, "repo:"+r)
	}
	logins := slices.Clone(f.Authors)
	for _, t := range f.AuthorTeams {
		for _, l := range members[t] {
			if !slices.Contains(logins, l) {
				logins = append(logins, l)
			}
		}
	}
	if len(logins) == 0 && len(f.AuthorTeams) > 0 {
		return "", false
	}
	for _, l := range logins {
		parts = append(parts, "author:"+l)
	}
	for _, t := range f.RequestedTeams {
		parts = append(parts, "team-review-requested:"+t)
	}
	if len(f.Labels) > 0 {
		quoted := make([]string, len(f.Labels))
		for i, l := range f.Labels {
			quoted[i] = l
			if strings.ContainsAny(l, " ,") {
				quoted[i] = `"` + l + `"`
			}
		}
		parts = append(parts, "label:"+strings.Join(quoted, ","))
	}
	return strings.Join(parts, " "), true
}

// Search is one view's search. Name is for error messages.
type Search struct{ Key, Name, Query string }

// PullRequest is one row of a PR view.
type PullRequest struct {
	Repo           string // owner/name
	Number         int
	Title          string
	URL            string
	Author         string
	Draft          bool
	Fork           bool
	CreatedAt      time.Time
	Checks         CheckState
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or ""
	Labels         []string
	RequestedUsers []string
	RequestedTeams []string // org/slug
	LatestReviews  []Review
}

const searchFields = `
fragment R on PullRequest {
  number title url isDraft createdAt isCrossRepository reviewDecision
  author { login }
  repository { nameWithOwner }
  labels(first: 20) { nodes { name } }
  reviewRequests(first: 20) { nodes { requestedReviewer { ... on Team { combinedSlug } ... on User { login } } } }
  latestReviews(first: 20) { nodes { author { login } state submittedAt } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

func searchQuery(n int) string {
	var decl, body strings.Builder
	for i := range n {
		fmt.Fprintf(&decl, "$q%d: String!, ", i)
		fmt.Fprintf(&body, "s%[1]d: search(type: ISSUE, query: $q%[1]d, first: 100) { nodes { ...R } }\n", i)
	}
	return "query(" + strings.TrimSuffix(decl.String(), ", ") + ") {\n" + body.String() + "}\n" + searchFields
}

// SearchPRs runs every search in one GraphQL call and returns each Key's PRs.
// A search that matches nothing maps to an empty list.
func (c *Client) SearchPRs(ctx context.Context, searches []Search) (map[string][]PullRequest, error) {
	out := map[string][]PullRequest{}
	if len(searches) == 0 {
		return out, nil
	}
	args := []string{"api", "graphql", "-f", "query=" + searchQuery(len(searches))}
	for i, s := range searches {
		args = append(args, "-f", fmt.Sprintf("q%d=%s", i, s.Query))
	}
	raw, runErr := c.run(ctx, args...)
	var resp struct {
		Data map[string]*struct {
			Nodes []gqlSearchPR `json:"nodes"`
		} `json:"data"`
		Errors []struct {
			Message string   `json:"message"`
			Path    []string `json:"path"`
		} `json:"errors"`
	}
	parseErr := json.Unmarshal(raw, &resp)
	if len(resp.Errors) > 0 {
		e := resp.Errors[0]
		names := searchNames(searches)
		if len(e.Path) > 0 {
			if i, err := strconv.Atoi(strings.TrimPrefix(e.Path[0], "s")); err == nil && i < len(searches) {
				names = searches[i].Name
			}
		}
		return nil, fmt.Errorf("search %s: %s", names, e.Message)
	}
	if runErr != nil {
		return nil, fmt.Errorf("search %s: %w", searchNames(searches), runErr)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("search %s: parse GraphQL response: %w", searchNames(searches), parseErr)
	}
	for i, s := range searches {
		prs := []PullRequest{}
		if r := resp.Data["s"+strconv.Itoa(i)]; r != nil {
			for _, n := range r.Nodes {
				if n.Number != 0 { // an Issue that matched is an empty node
					prs = append(prs, n.toPullRequest())
				}
			}
		}
		out[s.Key] = prs
	}
	return out, nil
}

func searchNames(searches []Search) string {
	names := make([]string, len(searches))
	for i, s := range searches {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

type gqlSearchPR struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	IsDraft           bool      `json:"isDraft"`
	CreatedAt         time.Time `json:"createdAt"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	ReviewDecision    string    `json:"reviewDecision"`
	Author            *gqlLogin `json:"author"`
	Repository        struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *struct {
				CombinedSlug string `json:"combinedSlug"`
				Login        string `json:"login"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []struct {
			Author      *gqlLogin `json:"author"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *gqlRollup `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (g gqlSearchPR) toPullRequest() PullRequest {
	pr := PullRequest{
		Repo: g.Repository.NameWithOwner, Number: g.Number, Title: g.Title, URL: g.URL,
		Draft: g.IsDraft, Fork: g.IsCrossRepository, CreatedAt: g.CreatedAt,
		ReviewDecision: g.ReviewDecision, Checks: ChecksNone,
	}
	if g.Author != nil {
		pr.Author = g.Author.Login
	}
	for _, l := range g.Labels.Nodes {
		pr.Labels = append(pr.Labels, l.Name)
	}
	for _, n := range g.ReviewRequests.Nodes {
		switch r := n.RequestedReviewer; {
		case r == nil:
		case r.CombinedSlug != "":
			pr.RequestedTeams = append(pr.RequestedTeams, r.CombinedSlug)
		case r.Login != "":
			pr.RequestedUsers = append(pr.RequestedUsers, r.Login)
		}
	}
	for _, r := range g.LatestReviews.Nodes {
		rv := Review{State: r.State, SubmittedAt: r.SubmittedAt}
		if r.Author != nil {
			rv.Author = r.Author.Login
		}
		pr.LatestReviews = append(pr.LatestReviews, rv)
	}
	if n := len(g.Commits.Nodes); n > 0 {
		pr.Checks = g.Commits.Nodes[n-1].Commit.StatusCheckRollup.checks()
	}
	return pr
}

// TeamMembers returns the logins of each org/slug team, in one GraphQL call.
func (c *Client) TeamMembers(ctx context.Context, teams []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(teams) == 0 {
		return out, nil
	}
	var orgs []string
	byOrg := map[string][]int{} // team indices for each org
	for i, t := range teams {
		org, _, ok := strings.Cut(t, "/")
		if !ok {
			return nil, fmt.Errorf("team %q is not org/slug", t)
		}
		if _, seen := byOrg[org]; !seen {
			orgs = append(orgs, org)
		}
		byOrg[org] = append(byOrg[org], i)
	}
	var decl, body strings.Builder
	var vars []string
	for oi, org := range orgs {
		fmt.Fprintf(&decl, "$o%d: String!, ", oi)
		fmt.Fprintf(&body, "o%[1]d: organization(login: $o%[1]d) {\n", oi)
		vars = append(vars, "-f", fmt.Sprintf("o%d=%s", oi, org))
		for _, ti := range byOrg[org] {
			_, slug, _ := strings.Cut(teams[ti], "/")
			fmt.Fprintf(&decl, "$s%d: String!, ", ti)
			fmt.Fprintf(&body, "  t%[1]d: team(slug: $s%[1]d) { members(first: 100) { nodes { login } } }\n", ti)
			vars = append(vars, "-f", fmt.Sprintf("s%d=%s", ti, slug))
		}
		body.WriteString("}\n")
	}
	query := "query(" + strings.TrimSuffix(decl.String(), ", ") + ") {\n" + body.String() + "}"
	raw, err := c.run(ctx, append([]string{"api", "graphql", "-f", "query=" + query}, vars...)...)
	if err != nil {
		return nil, fmt.Errorf("read members of %s: %w", strings.Join(teams, ", "), err)
	}
	var resp struct {
		Data map[string]map[string]*struct {
			Members struct {
				Nodes []gqlLogin `json:"nodes"`
			} `json:"members"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("read members of %s: %w", strings.Join(teams, ", "), err)
	}
	for oi, org := range orgs {
		for _, ti := range byOrg[org] {
			team := resp.Data["o"+strconv.Itoa(oi)]["t"+strconv.Itoa(ti)]
			if team == nil {
				return nil, fmt.Errorf("team %s not found, or you cannot see it", teams[ti])
			}
			logins := []string{}
			for _, m := range team.Members.Nodes {
				logins = append(logins, m.Login)
			}
			out[teams[ti]] = logins
		}
	}
	return out, nil
}
