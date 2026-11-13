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

	"github.com/gclunies/lineup/internal/lifecycle"
)

type Key struct {
	Repo   string // owner/name
	Branch string
	// Bot is the review bot that gates the Task, a key of ReviewBots, or "".
	Bot string
}

type Review struct {
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	SubmittedAt time.Time
	// Commit, Bot, Viewer, and Opinion come only from the review queue's
	// search. Commit is the head that the review read, Bot is true when a
	// GitHub App wrote it, and Viewer is true when the user wrote it.
	// Opinion is the author's last APPROVED or CHANGES_REQUESTED, or "".
	// GitHub's latest review of a person is their last review of any kind,
	// so a comment after an approval hides the approval in State.
	Commit, Opinion string
	Bot, Viewer     bool
}

type PR struct {
	Number           int
	URL              string
	State            string // OPEN, MERGED, CLOSED
	IsDraft          bool
	HeadSHA          string
	MergeStateStatus string
	ReviewDecision   string
	ReviewRequests   int
	LatestReviews    []Review
	// CommitAuthors holds the GitHub login of each commit's author, newest
	// last. A commit whose author has no GitHub account has an empty login.
	// It covers at most the newest 100 commits, so CommitCount carries the total.
	CommitAuthors []string
	CommitCount   int
	// Bot is the review bot that gates the Task, or "". BotSummary is its
	// summary comment, or nil before its first review.
	Bot        string
	BotSummary *BotSummary
	// CheckRuns are the head's checks, each marked when the base branch's
	// rules require it, plus each required check that GitHub has not made
	// yet, as pending.
	CheckRuns []lifecycle.Check
	// ChecksDue is true when the head has no checks yet and the base branch has some.
	ChecksDue bool
	Threads   []Thread
	// ThreadsOver is true when the PR has more review threads than the poll reads.
	ThreadsOver bool
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
  commits(last: 100) { totalCount nodes { commit { author { user { login } } } } }
  head: commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
    __typename
    ... on CheckRun { name status conclusion }
    ... on StatusContext { context state }
  } } } } } }
  baseRef {
    refUpdateRule { requiredStatusCheckContexts }
    rules(first: 50) { nodes { type repositoryRuleset { enforcement }
      parameters { ... on RequiredStatusChecksParameters { requiredStatusChecks { context } } } } }
    target { ... on Commit { statusCheckRollup { state } } }
  }
  reviewThreads(first: 50) { totalCount nodes { isResolved
    opener: comments(first: 1) { nodes { author { __typename login } } }
    recent: comments(last: 5) { nodes { author { login } } }
  } }
}`

// botComments reads the comments that hold a review bot's summary. The bot
// posts its summary at its first review and edits it after, so the summary
// is among the PR's first comments. Only a gated Task's PR pays for the bodies.
const botComments = " comments(first: 20) { nodes { author { login } body } }"

// snapshotQuery builds one query that asks for the newest PR on each Key's
// head branch. Values travel as variables, so branch names need no escaping.
func snapshotQuery(keys []Key) string {
	var decl, body strings.Builder
	for i, k := range keys {
		extra := ""
		if k.Bot != "" {
			extra = botComments
		}
		fmt.Fprintf(&decl, "$o%[1]d: String!, $n%[1]d: String!, $b%[1]d: String!, ", i)
		fmt.Fprintf(&body, "a%[1]d: repository(owner: $o%[1]d, name: $n%[1]d) { pullRequests(headRefName: $b%[1]d, first: 1, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { ...F%[2]s } } }\n", i, extra)
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
	args := []string{"api", "graphql", "-f", "query=" + snapshotQuery(keys)}
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

type gqlComment struct {
	Author *gqlLogin `json:"author"`
	Body   string    `json:"body"`
}

type gqlRollup struct {
	State string `json:"state"`
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
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Head struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []gqlContext `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"head"`
	BaseRef *struct {
		RefUpdateRule *struct {
			RequiredStatusCheckContexts []string `json:"requiredStatusCheckContexts"`
		} `json:"refUpdateRule"`
		Rules struct {
			Nodes []struct {
				Type              string `json:"type"`
				RepositoryRuleset *struct {
					Enforcement string `json:"enforcement"`
				} `json:"repositoryRuleset"`
				Parameters struct {
					RequiredStatusChecks []struct {
						Context string `json:"context"`
					} `json:"requiredStatusChecks"`
				} `json:"parameters"`
			} `json:"nodes"`
		} `json:"rules"`
		Target struct {
			StatusCheckRollup *gqlRollup `json:"statusCheckRollup"`
		} `json:"target"`
	} `json:"baseRef"`
	ReviewThreads struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			IsResolved bool `json:"isResolved"`
			Opener     struct {
				Nodes []struct {
					Author *struct {
						Typename string `json:"__typename"`
						Login    string `json:"login"`
					} `json:"author"`
				} `json:"nodes"`
			} `json:"opener"`
			Recent struct {
				Nodes []struct {
					Author *gqlLogin `json:"author"`
				} `json:"nodes"`
			} `json:"recent"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	Comments struct {
		Nodes []gqlComment `json:"nodes"`
	} `json:"comments"`
}

// gqlContext is one check on a commit: a CheckRun or a StatusContext.
type gqlContext struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Context    string `json:"context"`
	State      string `json:"state"`
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
		node := repo.PullRequests.Nodes[0]
		pr := node.toPR()
		if bot, ok := ReviewBots[k.Bot]; ok {
			pr.Bot, pr.BotSummary = k.Bot, bot.summary(node.Comments.Nodes)
		}
		prs[k] = pr
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
	pr.CheckRuns = g.checkRuns()
	// GitHub creates a push's checks a few seconds after the push. In a repo
	// whose base branch has checks, none yet means they are on their way.
	pr.ChecksDue = len(pr.CheckRuns) == 0 && g.BaseRef != nil && g.BaseRef.Target.StatusCheckRollup != nil
	pr.Threads = g.threads()
	pr.ThreadsOver = g.ReviewThreads.TotalCount > len(g.ReviewThreads.Nodes)
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
