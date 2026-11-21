package board

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	// Issue in Ready.
	Backlog string
}

// load reads the user's own GitHub Issues, off the render path.
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
	return mineGitHubMsg{issues: is, err: err}
}

// issueRows makes the Issue rows from every Tracker that the Board reads.
func (m Model) issueRows(tasks []scheduler.TaskView) []issueRow {
	return issueRows(m.mine.linearIssues(), m.mine.loc, m.mine.gh, m.opts.GitHub.Backlog, tasks)
}
