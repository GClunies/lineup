package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Issue is a GitHub Issue. Repo is owner/name.
type Issue struct {
	Repo   string
	Number int
	Title  string
	Body   string
	URL    string
	State  string // OPEN or CLOSED
	Labels []string
}

// ID is owner/name#number, the form that lineup add takes.
func (is Issue) ID() string { return is.Repo + "#" + strconv.Itoa(is.Number) }

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Branch is gh-<number>-<title slug>.
func (is Issue) Branch() string {
	slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(is.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	b := "gh-" + strconv.Itoa(is.Number)
	if slug != "" {
		b += "-" + slug
	}
	return b
}

var issueID = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)

// ParseIssueID reads owner/name#number.
func ParseIssueID(id string) (repo string, number int, err error) {
	m := issueID.FindStringSubmatch(id)
	if m == nil {
		return "", 0, fmt.Errorf("%q is not a GitHub Issue ID such as owner/repo#12", id)
	}
	n, _ := strconv.Atoi(m[2])
	return m[1], n, nil
}

type ghIssue struct {
	Number     int                     `json:"number"`
	Title      string                  `json:"title"`
	Body       string                  `json:"body"`
	URL        string                  `json:"url"`
	State      string                  `json:"state"`
	Labels     []struct{ Name string } `json:"labels"`
	Repository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

func (g ghIssue) issue(repo string) Issue {
	is := Issue{Repo: repo, Number: g.Number, Title: g.Title, Body: g.Body, URL: g.URL, State: strings.ToUpper(g.State)}
	if g.Repository != nil {
		is.Repo = g.Repository.NameWithOwner
	}
	for _, l := range g.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

func (c *Client) Issue(ctx context.Context, repo string, number int) (Issue, error) {
	out, err := c.run(ctx, "issue", "view", strconv.Itoa(number), "--repo", repo, "--json", "number,title,body,url,state,labels")
	if err != nil {
		return Issue{}, err
	}
	var g ghIssue
	if err := json.Unmarshal(out, &g); err != nil {
		return Issue{}, fmt.Errorf("gh issue view %s#%d: %w", repo, number, err)
	}
	return g.issue(repo), nil
}

// RepoIssues lists the repository's open Issues.
func (c *Client) RepoIssues(ctx context.Context, repo string) ([]Issue, error) {
	out, err := c.run(ctx, "issue", "list", "--repo", repo, "--state", "open", "--limit", "200", "--json", "number,title,url,state,labels")
	if err != nil {
		return nil, err
	}
	return decodeIssues(out, repo)
}

// MyIssues lists the open Issues assigned to the user, in every repository.
func (c *Client) MyIssues(ctx context.Context) ([]Issue, error) {
	out, err := c.run(ctx, "search", "issues", "--assignee", "@me", "--state", "open", "--limit", "100", "--json", "number,title,url,state,labels,repository")
	if err != nil {
		return nil, err
	}
	return decodeIssues(out, "")
}

func decodeIssues(out []byte, repo string) ([]Issue, error) {
	var gs []ghIssue
	if err := json.Unmarshal(out, &gs); err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	is := make([]Issue, len(gs))
	for i, g := range gs {
		is[i] = g.issue(repo)
	}
	return is, nil
}

// CreateIssue opens an Issue with an empty body. gh prints the new Issue's URL.
func (c *Client) CreateIssue(ctx context.Context, repo, title string) (Issue, error) {
	out, err := c.run(ctx, "issue", "create", "--repo", repo, "--title", title, "--body", "")
	if err != nil {
		return Issue{}, err
	}
	url := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
	if err != nil {
		return Issue{}, fmt.Errorf("gh issue create printed %q, not an Issue URL", url)
	}
	return Issue{Repo: repo, Number: n, Title: title, URL: url, State: "OPEN"}, nil
}

func (c *Client) AssignToMe(ctx context.Context, repo string, number int) error {
	_, err := c.run(ctx, "issue", "edit", strconv.Itoa(number), "--repo", repo, "--add-assignee", "@me")
	return err
}

// SetOpen closes or reopens the Issue.
func (c *Client) SetOpen(ctx context.Context, repo string, number int, open bool) error {
	verb := "close"
	if open {
		verb = "reopen"
	}
	_, err := c.run(ctx, "issue", verb, strconv.Itoa(number), "--repo", repo)
	return err
}

// LabelChange is the edit that a lifecycle event makes: add the event's
// label and remove the labels of the other events, so the Issue carries at
// most one lifecycle label. merged and closed with no label remove them all,
// so a finished Issue does not keep "in progress". ok is false when nothing
// changes.
func LabelChange(labels map[string]string, event string) (add string, remove []string, ok bool) {
	add = labels[event]
	if add == "" && event != "merged" && event != "closed" {
		return "", nil, false
	}
	for _, l := range labels {
		if l != "" && l != add && !slices.Contains(remove, l) {
			remove = append(remove, l)
		}
	}
	slices.Sort(remove)
	return add, remove, true
}

// SetLifecycleLabel writes the label for an event. A label that the
// repository lacks is created, then the edit runs once more.
func (c *Client) SetLifecycleLabel(ctx context.Context, repo string, number int, labels map[string]string, event string) error {
	add, remove, ok := LabelChange(labels, event)
	if !ok {
		return nil
	}
	cur, err := c.Issue(ctx, repo, number)
	if err != nil {
		return err
	}
	remove = slices.DeleteFunc(remove, func(l string) bool { return !slices.Contains(cur.Labels, l) })
	if add == "" && len(remove) == 0 {
		return nil
	}
	args := []string{"issue", "edit", strconv.Itoa(number), "--repo", repo}
	if add != "" {
		args = append(args, "--add-label", add)
	}
	if len(remove) > 0 {
		args = append(args, "--remove-label", strings.Join(remove, ","))
	}
	_, err = c.run(ctx, args...)
	if err != nil && add != "" && strings.Contains(err.Error(), "not found") {
		if _, cerr := c.run(ctx, "label", "create", add, "--repo", repo, "--description", "Set by lineup"); cerr != nil {
			return fmt.Errorf("%w; creating the label: %v", err, cerr)
		}
		_, err = c.run(ctx, args...)
	}
	return err
}
