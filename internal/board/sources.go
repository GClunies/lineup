package board

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// Item is an Issue from a Tracker other than Linear, as the browser shows it.
type Item struct {
	Kind   string // scheduler.KindGitHub or scheduler.KindLocal
	ID     string // owner/repo#12 or local-7
	Title  string
	Status string
	Repo   string
	URL    string
	Path   string // the file of a local Issue
	Group  string // the header the row sits under
	Broken string // why a local file does not parse
}

// Source is the list behind one GitHub or Local tab.
type Source interface {
	Name() string
	// Key places the tab in the saved rank of tabs.
	Key() string
	List(ctx context.Context) ([]Item, error)
	Body(ctx context.Context, it Item) (string, error)
	Statuses(it Item) []string
	SetStatus(ctx context.Context, it Item, status string) error
	// CanCreate is false for a list with no single place to add an Issue.
	CanCreate() bool
	Create(ctx context.Context, title string) error
	// SetRepo keeps the Repo that the user picked for the Issue.
	SetRepo(it Item, repo string) error
}

// GitHubSource lists the open Issues of one repository, or with an empty
// repo, the open Issues assigned to the user.
type GitHubSource struct {
	C    *github.Client
	Repo string
}

func (g GitHubSource) Name() string {
	if g.Repo == "" {
		return "GitHub: mine"
	}
	return "GitHub: " + g.Repo[strings.Index(g.Repo, "/")+1:]
}

func (g GitHubSource) Key() string { return "github:" + firstNonEmpty(g.Repo, "mine") }

func (g GitHubSource) List(ctx context.Context) ([]Item, error) {
	var is []github.Issue
	var err error
	if g.Repo == "" {
		is, err = g.C.MyIssues(ctx)
	} else {
		is, err = g.C.RepoIssues(ctx, g.Repo)
	}
	out := make([]Item, len(is))
	for i, x := range is {
		out[i] = Item{Kind: scheduler.KindGitHub, ID: x.ID(), Title: x.Title, Status: strings.ToLower(x.State), Repo: x.Repo, URL: x.URL}
		if g.Repo == "" {
			out[i].Group = x.Repo
		}
	}
	slices.SortStableFunc(out, func(a, b Item) int { return cmp.Compare(a.Group, b.Group) })
	return out, err
}

func (g GitHubSource) Body(ctx context.Context, it Item) (string, error) {
	repo, n, err := github.ParseIssueID(it.ID)
	if err != nil {
		return "", err
	}
	is, err := g.C.Issue(ctx, repo, n)
	return is.Body, err
}

func (GitHubSource) Statuses(Item) []string { return []string{"open", "closed"} }

func (g GitHubSource) SetStatus(ctx context.Context, it Item, status string) error {
	repo, n, err := github.ParseIssueID(it.ID)
	if err != nil {
		return err
	}
	return g.C.SetOpen(ctx, repo, n, status == "open")
}

func (g GitHubSource) CanCreate() bool { return g.Repo != "" }

func (g GitHubSource) Create(ctx context.Context, title string) error {
	if g.Repo == "" {
		return errors.New("open a GitHub: <repo> tab to add an Issue there")
	}
	_, err := g.C.CreateIssue(ctx, g.Repo, title)
	return err
}

// SetRepo keeps nothing: a GitHub Issue's repository picks its Repo.
func (GitHubSource) SetRepo(Item, string) error { return nil }

// LocalSource lists the Local Tracker's Issues, grouped by status, then by repo.
type LocalSource struct{ S localissues.Store }

func (LocalSource) Name() string { return "Local" }
func (LocalSource) Key() string  { return "local" }

func (l LocalSource) List(context.Context) ([]Item, error) {
	is, broken, err := l.S.List()
	rank := func(s localissues.Status) int { return slices.Index(localissues.Statuses, s) }
	slices.SortStableFunc(is, func(a, b localissues.Issue) int {
		return cmp.Or(cmp.Compare(rank(a.Status), rank(b.Status)), cmp.Compare(a.Repo, b.Repo))
	})
	var out []Item
	for _, x := range is {
		group := string(x.Status) + " · " + firstNonEmpty(x.Repo, "no repo")
		out = append(out, Item{Kind: scheduler.KindLocal, ID: x.ID(), Title: x.Title, Status: string(x.Status),
			Repo: x.Repo, Path: filepath.Join(l.S.Dir, x.ID()+".md"), Group: group})
	}
	for _, b := range broken {
		id := strings.TrimSuffix(b.File, ".md")
		out = append(out, Item{Kind: scheduler.KindLocal, ID: id, Title: b.File, Path: filepath.Join(l.S.Dir, b.File),
			Group: "files that don't parse", Broken: b.Err.Error()})
	}
	return out, err
}

func (l LocalSource) Body(_ context.Context, it Item) (string, error) {
	is, err := l.S.Get(it.ID)
	return is.Body, err
}

func (LocalSource) Statuses(Item) []string {
	out := make([]string, len(localissues.Statuses))
	for i, s := range localissues.Statuses {
		out[i] = string(s)
	}
	return out
}

func (l LocalSource) SetStatus(_ context.Context, it Item, status string) error {
	return l.S.SetStatus(it.ID, localissues.Status(status))
}

func (LocalSource) CanCreate() bool { return true }

func (l LocalSource) Create(_ context.Context, title string) error {
	_, err := l.S.Create(title, "", "")
	return err
}

func (l LocalSource) SetRepo(it Item, repo string) error { return l.S.SetRepo(it.ID, repo) }
