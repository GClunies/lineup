package github

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// RepoFacts is what GitHub says about a repository that lineup sets up.
type RepoFacts struct {
	HasIssues     bool
	DefaultBranch string
	// RequiredChecks are the checks that the default branch requires, sorted.
	RequiredChecks []string
	// ReviewBots are the names in ReviewBots of each bot that commented on
	// one of the 20 newest PRs, sorted.
	ReviewBots []string
}

// repoFactsQuery reads the classic branch protection twice: refUpdateRule
// is what any reader sees, and branchProtectionRule is what an admin sees.
// Each can hold a required check that the other leaves out.
const repoFactsQuery = `query($o: String!, $n: String!) { repository(owner: $o, name: $n) {
  hasIssuesEnabled
  defaultBranchRef { name
    branchProtectionRule { requiredStatusCheckContexts }
    ` + refRules + `
  }
  pullRequests(last: 20, states: [OPEN, MERGED, CLOSED]) { nodes { comments(first: 50) { nodes { author { login } } } } }
} }`

type gqlRepoFacts struct {
	Data struct {
		Repository *struct {
			HasIssuesEnabled bool `json:"hasIssuesEnabled"`
			DefaultBranchRef *struct {
				Name                 string `json:"name"`
				BranchProtectionRule *struct {
					RequiredStatusCheckContexts []string `json:"requiredStatusCheckContexts"`
				} `json:"branchProtectionRule"`
				gqlRefRules
			} `json:"defaultBranchRef"`
			PullRequests struct {
				Nodes []struct {
					Comments struct {
						Nodes []gqlComment `json:"nodes"`
					} `json:"comments"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// RepoFacts reads the facts of the repository owner/name in one query.
func (c *Client) RepoFacts(ctx context.Context, repo string) (RepoFacts, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return RepoFacts{}, fmt.Errorf("repo %q is not owner/name", repo)
	}
	out, err := c.run(ctx, "api", "graphql", "-f", "query="+repoFactsQuery, "-f", "o="+owner, "-f", "n="+name)
	if err != nil {
		return RepoFacts{}, err
	}
	return parseRepoFacts(out, repo)
}

func parseRepoFacts(raw []byte, repo string) (RepoFacts, error) {
	var resp gqlRepoFacts
	if err := json.Unmarshal(raw, &resp); err != nil {
		return RepoFacts{}, fmt.Errorf("parse GraphQL response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return RepoFacts{}, fmt.Errorf("GraphQL: %s", resp.Errors[0].Message)
	}
	r := resp.Data.Repository
	if r == nil {
		return RepoFacts{}, fmt.Errorf("GitHub has no repository %s", repo)
	}
	f := RepoFacts{HasIssues: r.HasIssuesEnabled}
	if b := r.DefaultBranchRef; b != nil {
		f.DefaultBranch = b.Name
		f.RequiredChecks = b.required()
		if p := b.BranchProtectionRule; p != nil {
			f.RequiredChecks = append(f.RequiredChecks, p.RequiredStatusCheckContexts...)
			slices.Sort(f.RequiredChecks)
			f.RequiredChecks = slices.Compact(f.RequiredChecks)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(ReviewBots)) {
		logins := ReviewBots[name].Logins
		for _, pr := range r.PullRequests.Nodes {
			if slices.ContainsFunc(pr.Comments.Nodes, func(c gqlComment) bool { return c.Author != nil && slices.Contains(logins, c.Author.Login) }) {
				f.ReviewBots = append(f.ReviewBots, name)
				break
			}
		}
	}
	return f, nil
}
