package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// OwnIssue is one of the user's own open Issues.
type OwnIssue struct {
	Issue
	Assignees []string
	// Blockers are the Issues that the body's Blocked by lines name, in
	// the order of the body.
	Blockers []Blocker
}

// Blocker is an Issue that blocks an OwnIssue. ID is owner/name#number.
// Open is false only when GitHub reports the Issue closed or its PR merged.
type Blocker struct {
	ID   string
	Open bool
}

// ownLimit is the most Issues that each search reads.
const ownLimit = 200

const ownFields = "repository,number,title,body,url,state,labels,assignees,author,isPullRequest"

type ghUser struct {
	Login string `json:"login"`
}

type ghOwn struct {
	ghIssue
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Assignees     []ghUser `json:"assignees"`
	Author        *ghUser  `json:"author"`
	IsPullRequest bool     `json:"isPullRequest"`
}

// mine applies the rule for the user's own Issue: the user is an
// assignee, or the Issue has no assignee and the user opened it. A pull
// request is never an Issue.
func (g ghOwn) mine(login string) bool {
	if g.IsPullRequest {
		return false
	}
	if len(g.Assignees) == 0 {
		return g.Author != nil && strings.EqualFold(g.Author.Login, login)
	}
	return slices.ContainsFunc(g.Assignees, func(u ghUser) bool { return strings.EqualFold(u.Login, login) })
}

// OwnIssues lists the open Issues of the account login that are its own,
// in every repository, with the state of each blocker. It runs two
// searches, one for the assigned Issues and one for the unassigned Issues
// that login opened, then one GraphQL call for the blockers that the
// searches did not return. A failed blocker read still returns the Issues,
// with each of those blockers open, and the error.
func (c *Client) OwnIssues(ctx context.Context, login string) ([]OwnIssue, error) {
	var own []OwnIssue
	seen := map[string]bool{}
	for _, filter := range [][]string{{"--assignee", login}, {"--author", login, "--no-assignee"}} {
		args := append([]string{"search", "issues", "--state", "open", "--limit", strconv.Itoa(ownLimit), "--json", ownFields}, filter...)
		out, err := c.run(ctx, args...)
		if err != nil {
			return nil, err
		}
		var found []ghOwn
		if err := json.Unmarshal(out, &found); err != nil {
			return nil, fmt.Errorf("gh search issues: %w", err)
		}
		for _, g := range found {
			is := OwnIssue{Issue: g.issue(g.Repository.NameWithOwner)}
			if !g.mine(login) || seen[is.ID()] {
				continue
			}
			seen[is.ID()] = true
			for _, a := range g.Assignees {
				is.Assignees = append(is.Assignees, a.Login)
			}
			own = append(own, is)
		}
	}
	var unknown []string
	for i := range own {
		for _, id := range BlockedBy(own[i].Repo, own[i].Body) {
			own[i].Blockers = append(own[i].Blockers, Blocker{ID: id, Open: true})
			if !seen[id] && !slices.Contains(unknown, id) {
				unknown = append(unknown, id)
			}
		}
	}
	if len(unknown) == 0 {
		return own, nil
	}
	closed, err := c.closedIssues(ctx, unknown)
	for i := range own {
		for j, b := range own[i].Blockers {
			own[i].Blockers[j].Open = !closed[b.ID]
		}
	}
	return own, err
}

var (
	blockedLine = regexp.MustCompile(`(?im)^[\s>*-]*blocked by\b(.*)$`)
	issueRef    = regexp.MustCompile(`(?:\b([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+))?#([1-9][0-9]*)\b`)
)

// BlockedBy lists the Issues that the body's Blocked by lines name, as
// owner/name#number. A bare #12 is in repo, the Issue's own repository.
func BlockedBy(repo, body string) []string {
	var ids []string
	for _, line := range blockedLine.FindAllStringSubmatch(body, -1) {
		for _, m := range issueRef.FindAllStringSubmatch(line[1], -1) {
			id := cmp.Or(m[1], repo) + "#" + m[2]
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// closedIssues reads the state of each Issue in one GraphQL call. It
// reports the closed Issues, and the merged or closed PRs, since #12 can
// name a PR. An ID that GitHub does not return is absent.
func (c *Client) closedIssues(ctx context.Context, ids []string) (map[string]bool, error) {
	var decl, body strings.Builder
	var vars []string
	for i, id := range ids {
		repo, n, err := ParseIssueID(id)
		if err != nil {
			return nil, err
		}
		owner, name, _ := strings.Cut(repo, "/")
		fmt.Fprintf(&decl, "$o%[1]d: String!, $n%[1]d: String!, $p%[1]d: Int!, ", i)
		fmt.Fprintf(&body, "i%[1]d: repository(owner: $o%[1]d, name: $n%[1]d) { issueOrPullRequest(number: $p%[1]d) { ... on Issue { state } ... on PullRequest { state } } }\n", i)
		vars = append(vars, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name), "-F", fmt.Sprintf("p%d=%d", i, n))
	}
	query := "query(" + strings.TrimSuffix(decl.String(), ", ") + ") {\n" + body.String() + "}"
	args := append([]string{"api", "graphql", "-f", "query=" + query}, vars...)
	// gh exits with an error when one repository is missing, and still prints the others.
	out, err := c.run(ctx, args...)
	if err != nil && !json.Valid(out) {
		return nil, err
	}
	var resp struct {
		Data map[string]*struct {
			Item *struct {
				State string `json:"state"`
			} `json:"issueOrPullRequest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse GraphQL response: %w", err)
	}
	closed := map[string]bool{}
	for i, id := range ids {
		if r := resp.Data["i"+strconv.Itoa(i)]; r != nil && r.Item != nil {
			closed[id] = r.Item.State == "CLOSED" || r.Item.State == "MERGED"
		}
	}
	return closed, nil
}
