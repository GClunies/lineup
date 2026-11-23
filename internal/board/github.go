package board

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/scheduler"
)

// GitHub holds the Board's outside world in GitHub. Tests replace it.
type GitHub struct {
	// Login reports the account while the GitHub Tracker is on. The Board
	// asks at each load, so a login or a logout shows within a minute. A nil
	// Login reads no GitHub Issue.
	Login func() (login string, on bool, err error)
	// Client runs gh. Nil runs the gh on PATH.
	Client *github.Client
	// Backlog is the label that puts an Issue in Backlog. Empty puts every
	// Issue in Up next.
	Backlog string
	// Checkouts lists the checkouts below the roots. The Board reads it at
	// each load, so a new clone shows its Issues within a minute. Nil means
	// no checkout.
	Checkouts func() []config.Checkout
}

// load reads the user's own GitHub Issues, off the render path. It keeps an
// Issue only when a checkout's Repo key, which ignores case, is the Issue's
// repository.
func (g GitHub) load() tea.Msg {
	login, on, err := g.Login()
	switch {
	case err != nil:
		return mineGitHubMsg{err: err}
	case !on:
		return mineGitHubMsg{}
	}
	c := g.Client
	if c == nil {
		c = &github.Client{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	is, err := c.OwnIssues(ctx, login)
	var cs []config.Checkout
	if g.Checkouts != nil {
		cs = g.Checkouts()
	}
	is = slices.DeleteFunc(is, func(o github.OwnIssue) bool {
		return !slices.ContainsFunc(cs, func(c config.Checkout) bool { return strings.EqualFold(c.Key, o.Repo) })
	})
	return mineGitHubMsg{issues: is, err: err}
}

// issueRows makes the Issue rows from every Tracker that the Board reads.
func (m Model) issueRows(tasks []scheduler.TaskView) []issueRow {
	return issueRows(m.mine.linearIssues(), m.mine.loc, m.mine.gh, m.opts.GitHub.Backlog, tasks)
}
