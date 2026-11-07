package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Key struct {
	Repo   string // owner/name
	Branch string
}

type CheckState string

const (
	ChecksNone    CheckState = "none"
	ChecksPending CheckState = "pending"
	ChecksPassed  CheckState = "passed"
	ChecksFailed  CheckState = "failed"
)

type Review struct {
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	SubmittedAt time.Time
}

type PR struct {
	Number           int
	URL              string
	State            string // OPEN, MERGED, CLOSED
	IsDraft          bool
	HeadSHA          string
	MergeStateStatus string
	Checks           CheckState
	ReviewDecision   string
	ReviewRequests   int
	LatestReviews    []Review
	// CommitAuthors holds the GitHub login of each commit's author, newest
	// last. A commit whose author has no GitHub account has an empty login.
	// It covers at most the newest 100 commits, so CommitCount carries the total.
	CommitAuthors []string
	CommitCount   int
}

// Client runs the gh CLI. Bin is the path to gh, and "gh" when empty.
type Client struct{ Bin string }

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	bin := c.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("gh %s: %w: %s", strings.Join(args[:min(len(args), 2)], " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (c *Client) Viewer(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	login := strings.TrimSpace(string(out))
	if login == "" {
		return "", errors.New("gh api user returned no login")
	}
	return login, nil
}

const prFields = `
fragment F on PullRequest {
  number url state isDraft headRefOid mergeStateStatus reviewDecision
  reviewRequests { totalCount }
  latestReviews(first: 50) { nodes { author { login } state submittedAt } }
  commits(last: 100) {
    totalCount
    nodes { commit { author { user { login } } statusCheckRollup { state } } }
  }
  baseRef { target { ... on Commit { statusCheckRollup { state } } } }
}`

// snapshotQuery builds one query that asks for the newest PR on each Key's
// head branch. Values travel as variables, so branch names need no escaping.
func snapshotQuery(n int) string {
	var decl, body strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&decl, "$o%[1]d: String!, $n%[1]d: String!, $b%[1]d: String!, ", i)
		fmt.Fprintf(&body, "a%[1]d: repository(owner: $o%[1]d, name: $n%[1]d) { pullRequests(headRefName: $b%[1]d, first: 1, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { ...F } } }\n", i)
	}
	return "query(" + strings.TrimSuffix(decl.String(), ", ") + ") {\n" + body.String() + "}\n" + prFields
}

// Snapshot returns the newest PR on each Key's head branch in one GraphQL
// query. A Key with no PR is absent from the map.
func (c *Client) Snapshot(ctx context.Context, keys []Key) (map[Key]*PR, error) {
	prs := map[Key]*PR{}
	if len(keys) == 0 {
		return prs, nil
	}
	args := []string{"api", "graphql", "-f", "query=" + snapshotQuery(len(keys))}
	for i, k := range keys {
		owner, name, ok := strings.Cut(k.Repo, "/")
		if !ok {
			return nil, fmt.Errorf("repo %q is not owner/name", k.Repo)
		}
		args = append(args, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name), "-f", fmt.Sprintf("b%d=%s", i, k.Branch))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseSnapshot(out, keys)
}

type gqlResponse struct {
	Data   map[string]*gqlRepo `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type gqlRepo struct {
	PullRequests struct {
		Nodes []gqlPR `json:"nodes"`
	} `json:"pullRequests"`
}

type gqlLogin struct {
	Login string `json:"login"`
}

type gqlRollup struct {
	State string `json:"state"`
}

func (r *gqlRollup) checks() CheckState {
	switch {
	case r == nil:
		return ChecksNone
	case r.State == "SUCCESS":
		return ChecksPassed
	case r.State == "FAILURE", r.State == "ERROR":
		return ChecksFailed
	}
	return ChecksPending
}

type gqlPR struct {
	Number           int    `json:"number"`
	URL              string `json:"url"`
	State            string `json:"state"`
	IsDraft          bool   `json:"isDraft"`
	HeadRefOid       string `json:"headRefOid"`
	MergeStateStatus string `json:"mergeStateStatus"`
	ReviewDecision   string `json:"reviewDecision"`
	ReviewRequests   struct {
		TotalCount int `json:"totalCount"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []struct {
			Author      *gqlLogin `json:"author"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	Commits struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Commit struct {
				Author *struct {
					User *gqlLogin `json:"user"`
				} `json:"author"`
				StatusCheckRollup *gqlRollup `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	BaseRef *struct {
		Target struct {
			StatusCheckRollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"target"`
	} `json:"baseRef"`
}

func parseSnapshot(raw []byte, keys []Key) (map[Key]*PR, error) {
	var resp gqlResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse GraphQL response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL: %s", resp.Errors[0].Message)
	}
	prs := map[Key]*PR{}
	for i, k := range keys {
		repo := resp.Data["a"+strconv.Itoa(i)]
		if repo == nil || len(repo.PullRequests.Nodes) == 0 {
			continue
		}
		prs[k] = repo.PullRequests.Nodes[0].toPR()
	}
	return prs, nil
}

func (g gqlPR) toPR() *PR {
	pr := &PR{
		Number:           g.Number,
		URL:              g.URL,
		State:            g.State,
		IsDraft:          g.IsDraft,
		HeadSHA:          g.HeadRefOid,
		MergeStateStatus: g.MergeStateStatus,
		ReviewDecision:   g.ReviewDecision,
		ReviewRequests:   g.ReviewRequests.TotalCount,
		CommitCount:      g.Commits.TotalCount,
		Checks:           ChecksNone,
	}
	for _, r := range g.LatestReviews.Nodes {
		rv := Review{State: r.State, SubmittedAt: r.SubmittedAt}
		if r.Author != nil {
			rv.Author = r.Author.Login
		}
		pr.LatestReviews = append(pr.LatestReviews, rv)
	}
	for _, n := range g.Commits.Nodes {
		login := ""
		if a := n.Commit.Author; a != nil && a.User != nil {
			login = a.User.Login
		}
		pr.CommitAuthors = append(pr.CommitAuthors, login)
	}
	// The checks of the PR are the checks of its head commit, the newest node.
	if n := len(g.Commits.Nodes); n > 0 {
		pr.Checks = g.Commits.Nodes[n-1].Commit.StatusCheckRollup.checks()
	}
	// GitHub creates a push's checks a few seconds after the push. In a repo
	// whose base branch has checks, none yet means they are on their way.
	if pr.Checks == ChecksNone && g.BaseRef != nil && g.BaseRef.Target.StatusCheckRollup != nil {
		pr.Checks = ChecksPending
	}
	return pr
}

// Merge merges with the repo's preferred allowed method: squash, then merge
// commit, then rebase.
func (c *Client) Merge(ctx context.Context, repo string, number int) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return fmt.Errorf("repo %q is not owner/name", repo)
	}
	// GraphQL reports these flags to any reader. The REST fields are null without push access.
	out, err := c.run(ctx, "api", "graphql",
		"-f", "query=query($o: String!, $n: String!) { repository(owner: $o, name: $n) { squashMergeAllowed mergeCommitAllowed rebaseMergeAllowed } }",
		"-f", "o="+owner, "-f", "n="+name,
		"--jq", `.data.repository | [.squashMergeAllowed, .mergeCommitAllowed, .rebaseMergeAllowed] | map(tostring) | join(",")`)
	if err != nil {
		return err
	}
	flag, err := mergeFlag(strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("%s: %w", repo, err)
	}
	for try := 1; ; try++ {
		_, err = c.run(ctx, "pr", "merge", strconv.Itoa(number), "--repo", repo, flag)
		// GitHub refuses a merge while another merge moves the base branch.
		if err == nil || try == 3 || !strings.Contains(err.Error(), "Base branch was modified") {
			return err
		}
		time.Sleep(mergeRetryWait)
	}
}

var mergeRetryWait = 3 * time.Second

// mergeFlag takes "squash,merge,rebase" permissions as true or false.
func mergeFlag(allowed string) (string, error) {
	parts := strings.Split(allowed, ",")
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected merge settings %q", allowed)
	}
	for i, flag := range []string{"--squash", "--merge", "--rebase"} {
		if parts[i] == "true" {
			return flag, nil
		}
	}
	return "", errors.New("the repo allows no merge method")
}
