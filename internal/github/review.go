package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// QueuePRs are the qualifiers that a queue's search starts with. Filter.Query
// adds the state. In search(type: ISSUE), a repeated repo: or author:
// qualifier is OR, a repeated label: is AND, and boolean OR returns nothing.
// A queue lists the oldest PRs first, so that its oldest PRs are inside the
// first 100.
const QueuePRs = "is:pr archived:false sort:created-asc draft:false -author:@me"

// Filter picks PRs. Base is QueuePRs. State is open,
// closed, merged, or all, and "" is open. Draft nil matches drafts and other
// PRs. ReviewRequested is @me or a login, and GitHub matches a request to
// that user by name or through any of their teams. The items of one list
// combine with OR, and two keys combine with AND. Author and the members of
// AuthorTeam are one list. IncludeRegex and ExcludeRegex are RE2 patterns on
// the title, which config checked at load.
type Filter struct {
	Base            string
	Draft           *bool
	Repo            []string // owner/name
	Author          []string
	AuthorTeam      []string // org/slug
	ReviewRequested string
	Label           []string
	IncludeRegex    string
	ExcludeRegex    string
}

// Keep returns the PRs of a search for f whose titles match IncludeRegex,
// when it is set, and do not match ExcludeRegex. GitHub search has no regex.
func (f Filter) Keep(prs []PullRequest) []PullRequest {
	if f.IncludeRegex == "" && f.ExcludeRegex == "" {
		return prs
	}
	include, exclude := regexp.MustCompile(f.IncludeRegex), regexp.MustCompile(f.ExcludeRegex)
	return slices.DeleteFunc(slices.Clone(prs), func(pr PullRequest) bool {
		return !include.MatchString(pr.Title) || f.ExcludeRegex != "" && exclude.MatchString(pr.Title)
	})
}

// Query builds the search for f, given the members of each team in
// AuthorTeam. ok is false when the author list is empty because each of its
// teams is empty. With no author: qualifier the search would match every
// author, so it must not run.
func (f Filter) Query(members map[string][]string) (q string, ok bool) {
	parts := []string{f.Base}
	parts = append(parts, "is:open")
	if f.Draft != nil {
		parts = append(parts, "draft:"+strconv.FormatBool(*f.Draft))
	}
	for _, r := range f.Repo {
		parts = append(parts, "repo:"+r)
	}
	logins := slices.Clone(f.Author)
	for _, t := range f.AuthorTeam {
		for _, l := range members[t] {
			if !slices.Contains(logins, l) {
				logins = append(logins, l)
			}
		}
	}
	if len(logins) == 0 && len(f.AuthorTeam) > 0 {
		return "", false
	}
	for _, l := range logins {
		parts = append(parts, "author:"+l)
	}
	if f.ReviewRequested != "" {
		parts = append(parts, "review-requested:"+f.ReviewRequested)
	}
	if len(f.Label) > 0 {
		quoted := make([]string, len(f.Label))
		for i, l := range f.Label {
			quoted[i] = l
			if strings.ContainsAny(l, " ,") {
				quoted[i] = `"` + l + `"`
			}
		}
		parts = append(parts, "label:"+strings.Join(quoted, ","))
	}
	return strings.Join(parts, " "), true
}

// Search is one queue's search. Name is for error messages.
type Search struct{ Key, Name, Query string }

// PullRequest is one PR that a queue's search found.
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
	Head, Base    string
	CreatedAt     time.Time
	LatestReviews []Review
	Viewer        ViewerState
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
  number title url isDraft createdAt isCrossRepository headRefOid baseRefName viewerDidAuthor
  author { __typename login }
  repository { nameWithOwner }
  reviewRequests(first: 20) { nodes { requestedReviewer { ... on User { isViewer } } } }
  viewerLatestReviewRequest { id }
  viewerReviews: reviews(author: $viewer, states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED]) { totalCount }
  latestReviews(first: 20) { nodes { author { __typename login } state submittedAt viewerDidAuthor commit { oid } } }
  latestOpinionatedReviews(first: 20) { nodes { author { login } state } }
}`

const searchQuery = "query($viewer: String!, $q: String!) {\n" +
	"s: search(type: ISSUE, query: $q, first: 100) { nodes { ...R } }\n}\n" + searchFields

// SearchPRs runs every search and returns each Key's PRs. viewer is the
// user's login. A search that matches nothing maps to an empty list. Each
// search is its own GraphQL call, and the calls run at the same time: GitHub
// stops a call at about 10 seconds, and one call that holds every search of a
// typical config takes longer. If a search fails, SearchPRs returns the error
// of each failed search and no lists.
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
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out := map[string][]PullRequest{}
	for i, s := range searches {
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
	IsCrossRepository bool      `json:"isCrossRepository"`
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
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *struct {
				IsViewer bool `json:"isViewer"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	ViewerLatestReviewRequest *struct{} `json:"viewerLatestReviewRequest"`
	ViewerReviews             struct {
		TotalCount int `json:"totalCount"`
	} `json:"viewerReviews"`
	LatestReviews struct {
		Nodes []struct {
			Author *struct {
				Typename string `json:"__typename"`
				Login    string `json:"login"`
			} `json:"author"`
			State           string    `json:"state"`
			SubmittedAt     time.Time `json:"submittedAt"`
			ViewerDidAuthor bool      `json:"viewerDidAuthor"`
			Commit          *struct {
				OID string `json:"oid"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	LatestOpinionatedReviews struct {
		Nodes []struct {
			Author *gqlLogin `json:"author"`
			State  string    `json:"state"`
		} `json:"nodes"`
	} `json:"latestOpinionatedReviews"`
}

func (g gqlSearchPR) toPullRequest() PullRequest {
	pr := PullRequest{
		Repo: g.Repository.NameWithOwner, Number: g.Number, Title: g.Title, URL: g.URL,
		Draft: g.IsDraft, Fork: g.IsCrossRepository, Head: g.HeadRefOid, Base: g.BaseRefName, CreatedAt: g.CreatedAt,
		Viewer: ViewerState{Author: g.ViewerDidAuthor, Requested: g.ViewerLatestReviewRequest != nil, Reviewed: g.ViewerReviews.TotalCount > 0},
	}
	if g.Author != nil {
		pr.Author, pr.Bot = g.Author.Login, g.Author.Typename == "Bot"
	}
	for _, n := range g.ReviewRequests.Nodes {
		if r := n.RequestedReviewer; r != nil && r.IsViewer {
			pr.Viewer.ByName = true
		}
	}
	opinions := map[string]string{}
	for _, r := range g.LatestOpinionatedReviews.Nodes {
		if r.Author != nil {
			opinions[r.Author.Login] = r.State
		}
	}
	for _, r := range g.LatestReviews.Nodes {
		rv := Review{State: r.State, SubmittedAt: r.SubmittedAt, Viewer: r.ViewerDidAuthor}
		if r.Author != nil {
			rv.Author, rv.Bot, rv.Opinion = r.Author.Login, r.Author.Typename == "Bot", opinions[r.Author.Login]
		}
		if r.Commit != nil {
			rv.Commit = r.Commit.OID
		}
		pr.LatestReviews = append(pr.LatestReviews, rv)
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
