package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PRRef names one PR by its repository, owner/name, and its number.
type PRRef struct {
	Repo   string
	Number int
}

func (r PRRef) String() string { return r.Repo + "#" + strconv.Itoa(r.Number) }

// ReviewedPR is what a review Task reads of the PR that it reviews.
type ReviewedPR struct {
	// Repo is owner/name as GitHub spells it.
	Repo   string
	Number int
	URL    string
	Title  string
	State  string // OPEN, MERGED, or CLOSED
	// Fork is true when the head branch lives in another repository.
	Fork bool
	Head string
	Base string
	// ReviewedAt is when the viewer last submitted a review, or zero. A
	// pending review is not submitted.
	ReviewedAt time.Time
}

const reviewedFields = `
fragment R on PullRequest {
  number url title state isCrossRepository headRefOid baseRefName
  repository { nameWithOwner }
  reviews(last: 20, author: $viewer) { nodes { state submittedAt } }
}`

// reviewedQuery reads n PRs by number. The viewer's reviews come back with
// each, so one call answers every review Task.
func reviewedQuery(n int) string {
	var decl, body strings.Builder
	decl.WriteString("$viewer: String!")
	for i := range n {
		fmt.Fprintf(&decl, ", $o%[1]d: String!, $n%[1]d: String!, $p%[1]d: Int!", i)
		fmt.Fprintf(&body, "r%[1]d: repository(owner: $o%[1]d, name: $n%[1]d) { pullRequest(number: $p%[1]d) { ...R } }\n", i)
	}
	return "query(" + decl.String() + ") {\n" + body.String() + "}\n" + reviewedFields
}

// ReviewedPRs reads each PR by number in one GraphQL call. viewer is the
// user's login. A PR that GitHub does not return is absent from the map, and
// the call fails only when GitHub returns none of them.
func (c *Client) ReviewedPRs(ctx context.Context, viewer string, refs []PRRef) (map[PRRef]*ReviewedPR, error) {
	prs := map[PRRef]*ReviewedPR{}
	if len(refs) == 0 {
		return prs, nil
	}
	args := []string{"api", "graphql", "-f", "query=" + reviewedQuery(len(refs)), "-f", "viewer=" + viewer}
	for i, r := range refs {
		owner, name, ok := strings.Cut(r.Repo, "/")
		if !ok {
			return nil, fmt.Errorf("repo %q is not owner/name", r.Repo)
		}
		args = append(args, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name), "-F", fmt.Sprintf("p%d=%d", i, r.Number))
	}
	// gh exits with an error when one PR is missing, and still prints the others.
	out, err := c.run(ctx, args...)
	if err != nil && !json.Valid(out) {
		return nil, err
	}
	return parseReviewed(out, refs)
}

type gqlReviewed struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	Title             string `json:"title"`
	State             string `json:"state"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	HeadRefOid        string `json:"headRefOid"`
	BaseRefName       string `json:"baseRefName"`
	Repository        struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Reviews struct {
		Nodes []struct {
			State       string     `json:"state"`
			SubmittedAt *time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"reviews"`
}

func parseReviewed(raw []byte, refs []PRRef) (map[PRRef]*ReviewedPR, error) {
	var resp struct {
		Data map[string]*struct {
			PullRequest *gqlReviewed `json:"pullRequest"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse GraphQL response: %w", err)
	}
	prs := map[PRRef]*ReviewedPR{}
	for i, ref := range refs {
		repo := resp.Data["r"+strconv.Itoa(i)]
		if repo == nil || repo.PullRequest == nil {
			continue
		}
		prs[ref] = repo.PullRequest.toReviewed()
	}
	if len(prs) == 0 && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL: %s", resp.Errors[0].Message)
	}
	return prs, nil
}

func (g *gqlReviewed) toReviewed() *ReviewedPR {
	pr := &ReviewedPR{
		Repo: g.Repository.NameWithOwner, Number: g.Number, URL: g.URL, Title: g.Title, State: g.State,
		Fork: g.IsCrossRepository, Head: g.HeadRefOid, Base: g.BaseRefName,
	}
	for _, r := range g.Reviews.Nodes {
		if r.State == "PENDING" || r.SubmittedAt == nil {
			continue
		}
		if r.SubmittedAt.After(pr.ReviewedAt) {
			pr.ReviewedAt = *r.SubmittedAt
		}
	}
	return pr
}
