package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The qualifiers that each kind of PR search starts with. In search(type:
// ISSUE), a repeated repo:, author:, or team-review-requested: qualifier is
// OR, a repeated label: is AND, and boolean OR returns nothing.
// A view lists the newest PRs first. A queue lists the oldest first, so that
// its oldest PRs are inside the first 100.
const (
	openPRs      = "is:pr is:open archived:false"
	newestFirst  = openPRs + " sort:created-desc"
	MyPRs        = newestFirst + " author:@me"
	RequestedPRs = newestFirst + " review-requested:@me draft:false"
	ViewPRs      = newestFirst
	QueuePRs     = openPRs + " sort:created-asc draft:false -author:@me"
)

// MyMergedPRs is the search for the user's PRs merged at since or later.
// GitHub compares merged: to the second.
func MyMergedPRs(since time.Time) string {
	return "is:pr is:merged archived:false sort:updated-desc author:@me merged:>=" + since.UTC().Format(time.RFC3339)
}

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
	Repo   string // owner/name
	Number int
	Title  string
	URL    string
	Author string
	// Bot is true when a GitHub App opened the PR.
	Bot   bool
	Draft bool
	Fork  bool
	// Head is the head commit, and Base is the base branch.
	Head, Base     string
	CreatedAt      time.Time
	MergedAt       time.Time // zero for an open PR
	Checks         CheckState
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or ""
	Labels         []string
	RequestedUsers []string
	RequestedTeams []string // org/slug
	LatestReviews  []Review
	Viewer         ViewerState
}

// ViewerState is the user's relation to a PR, as GitHub reports it to the
// user who runs the search.
type ViewerState struct {
	Author bool
	// Requested is true when the PR requests the user's review, by name or
	// through a team. GitHub drops the request when the user submits a review.
	Requested bool
	ByName    bool
	// Reviewed is true when the user submitted a review. A pending review is
	// not submitted.
	Reviewed bool
}

// The states filter leaves out a pending review, so viewerReviews counts only
// the reviews that the user submitted.
const searchFields = `
fragment R on PullRequest {
  number title url isDraft createdAt mergedAt isCrossRepository reviewDecision headRefOid baseRefName viewerDidAuthor
  author { __typename login }
  repository { nameWithOwner }
  labels(first: 20) { nodes { name } }
  reviewRequests(first: 20) { nodes { requestedReviewer { ... on Team { combinedSlug } ... on User { login isViewer } } } }
  viewerLatestReviewRequest { id }
  viewerReviews: reviews(author: $viewer, states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED]) { totalCount }
  latestReviews(first: 20) { nodes { author { login } state submittedAt } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

const searchQuery = "query($viewer: String!, $q: String!) {\n" +
	"s: search(type: ISSUE, query: $q, first: 100) { nodes { ...R } }\n}\n" + searchFields

// SearchPRs runs every search and returns each Key's PRs. viewer is the
// user's login. A search that matches nothing maps to an empty list. Each
// search is its own GraphQL call, and the calls run at the same time: GitHub
// stops a call at about 10 seconds, and one call that holds every search of a
// typical config takes longer. A failed search fails them all, and the error
// names the first one in order.
func (c *Client) SearchPRs(ctx context.Context, viewer string, searches []Search) (map[string][]PullRequest, error) {
	lists := make([][]PullRequest, len(searches))
	errs := make([]error, len(searches))
	var wg sync.WaitGroup
	for i, s := range searches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i], errs[i] = c.searchOne(ctx, viewer, s)
		}()
	}
	wg.Wait()
	out := map[string][]PullRequest{}
	for i, s := range searches {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out[s.Key] = lists[i]
	}
	return out, nil
}

func (c *Client) searchOne(ctx context.Context, viewer string, s Search) ([]PullRequest, error) {
	raw, runErr := c.run(ctx, "api", "graphql", "-f", "query="+searchQuery, "-f", "viewer="+viewer, "-f", "q="+s.Query)
	var resp struct {
		Data struct {
			S *struct {
				Nodes []gqlSearchPR `json:"nodes"`
			} `json:"s"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	parseErr := json.Unmarshal(raw, &resp)
	switch {
	case len(resp.Errors) > 0:
		return nil, fmt.Errorf("search %s: %s", s.Name, resp.Errors[0].Message)
	case runErr != nil:
		return nil, fmt.Errorf("search %s: %w", s.Name, runErr)
	case parseErr != nil:
		return nil, fmt.Errorf("search %s: parse GraphQL response: %w", s.Name, parseErr)
	}
	prs := []PullRequest{}
	if r := resp.Data.S; r != nil {
		for _, n := range r.Nodes {
			if n.Number != 0 { // an Issue that matched is an empty node
				prs = append(prs, n.toPullRequest())
			}
		}
	}
	return prs, nil
}

type gqlSearchPR struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	IsDraft           bool      `json:"isDraft"`
	CreatedAt         time.Time `json:"createdAt"`
	MergedAt          time.Time `json:"mergedAt"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	ReviewDecision    string    `json:"reviewDecision"`
	HeadRefOid        string    `json:"headRefOid"`
	BaseRefName       string    `json:"baseRefName"`
	ViewerDidAuthor   bool      `json:"viewerDidAuthor"`
	Author            *struct {
		Typename string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
	Repository struct {
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
				IsViewer     bool   `json:"isViewer"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	ViewerLatestReviewRequest *struct{} `json:"viewerLatestReviewRequest"`
	ViewerReviews             struct {
		TotalCount int `json:"totalCount"`
	} `json:"viewerReviews"`
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
		Draft: g.IsDraft, Fork: g.IsCrossRepository, Head: g.HeadRefOid, Base: g.BaseRefName, CreatedAt: g.CreatedAt,
		MergedAt: g.MergedAt, ReviewDecision: g.ReviewDecision, Checks: ChecksNone,
		Viewer: ViewerState{Author: g.ViewerDidAuthor, Requested: g.ViewerLatestReviewRequest != nil, Reviewed: g.ViewerReviews.TotalCount > 0},
	}
	if g.Author != nil {
		pr.Author, pr.Bot = g.Author.Login, g.Author.Typename == "Bot"
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
			pr.Viewer.ByName = pr.Viewer.ByName || r.IsViewer
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
