package board

import (
	"context"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/linear"
)

// LinearAPI is the part of linear.Client that the Board uses.
type LinearAPI interface {
	MyIssues(ctx context.Context) ([]linear.Issue, error)
	Teams(ctx context.Context) ([]linear.Team, error)
	TeamProjects(ctx context.Context, teamID string) ([]linear.ProjectInfo, error)
	Create(ctx context.Context, n linear.NewIssue) (linear.Issue, error)
}

// Linear holds the Board's outside world in Linear. Tests replace it.
type Linear struct {
	Workspaces func() ([]linear.Workspace, error)
	Client     func(linear.Workspace) LinearAPI
	ReadCache  func(key string) ([]linear.Issue, bool)
	WriteCache func(key string, issues []linear.Issue)
	// RememberRepo stores the Repo that the user picked for a Project.
	RememberRepo func(projectID, repo string) error
}

// withDefaults fills each hook that l leaves out with the real call.
func (l Linear) withDefaults() Linear {
	if l.Workspaces == nil {
		l.Workspaces = linear.Workspaces
	}
	if l.Client == nil {
		l.Client = func(ws linear.Workspace) LinearAPI { return linear.NewClient(ws) }
	}
	if l.ReadCache == nil {
		l.ReadCache = linear.ReadCached
	}
	if l.WriteCache == nil {
		l.WriteCache = linear.WriteCache
	}
	if l.RememberRepo == nil {
		l.RememberRepo = func(projectID, repo string) error {
			path, err := config.DefaultRepoChoicesPath()
			if err != nil {
				return err
			}
			return config.NewRepoChoices(path).Set(projectID, repo)
		}
	}
	return l
}

// linearClients makes one client for each workspace and keeps it.
type linearClients struct {
	Linear
	made map[string]LinearAPI
}

func newLinearClients(l Linear) linearClients {
	return linearClients{Linear: l.withDefaults(), made: map[string]LinearAPI{}}
}

func (c linearClients) api(ws linear.Workspace) LinearAPI {
	a, ok := c.made[ws.ID]
	if !ok {
		a = c.Client(ws)
		c.made[ws.ID] = a
	}
	return a
}
