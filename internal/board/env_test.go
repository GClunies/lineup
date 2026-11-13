package board

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

type fakeLinear struct {
	mu      sync.Mutex
	issues  []linear.Issue
	created []linear.NewIssue
	// createErr is what Create returns.
	createErr error
	// teamsErr is what Teams returns, by workspace ID.
	teamsErr  map[string]error
	teamReads int
	described []string // the identifiers that Issue read
	// issueErr is what Issue returns.
	issueErr error
}

// wsLinear is fakeLinear as one workspace sees it.
type wsLinear struct {
	*fakeLinear
	ws string
}

func (f wsLinear) Teams(ctx context.Context) ([]linear.Team, error) {
	if err := f.teamsErr[f.ws]; err != nil {
		return nil, err
	}
	return f.fakeLinear.Teams(ctx)
}

func (f *fakeLinear) MyIssues(context.Context) ([]linear.Issue, error) { return f.issues, nil }
func (f *fakeLinear) Teams(context.Context) ([]linear.Team, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamReads++
	return []linear.Team{{ID: "t1", Key: "ENG", Name: "Engineering"}}, nil
}
func (f *fakeLinear) TeamProjects(_ context.Context, team string) ([]linear.ProjectInfo, error) {
	projects := map[string][]linear.ProjectInfo{
		"t1": {{ID: "p1", Name: "Billing", Status: "Started"}},
		"t2": {{ID: "p2", Name: "Web", Status: "Started"}},
	}
	return projects[team], nil
}
func (f *fakeLinear) Issue(_ context.Context, id string) (linear.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.described = append(f.described, id)
	if f.issueErr != nil {
		return linear.Issue{}, f.issueErr
	}
	for _, is := range f.issues {
		if is.Identifier == id {
			is.Description = "# Goal\n\nBuild the **billing** model."
			return is, nil
		}
	}
	return linear.Issue{}, errors.New("not found")
}
func (f *fakeLinear) Create(_ context.Context, n linear.NewIssue) (linear.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return linear.Issue{}, f.createErr
	}
	f.created = append(f.created, n)
	return linear.Issue{Identifier: "ENG-900", Title: n.Title}, nil
}

// lissue makes a Linear Issue in the Billing Project of team ENG.
func lissue(id, title, state string, blockedBy ...string) linear.Issue {
	is := linear.Issue{ID: "id-" + id, Identifier: id, Title: title, URL: "https://linear.app/x/" + id,
		State: linear.State{ID: "s1", Name: state}, Team: linear.Team{ID: "t1", Key: "ENG"},
		Project: &linear.Project{ID: "p1", Name: "Billing"}}
	for _, b := range blockedBy {
		is.Blockers = append(is.Blockers, linear.Ref{ID: "id-" + b, Identifier: b})
	}
	return is
}

// workEnv is a Model on the work view with a fake Scheduler, a fake Linear,
// and a cache in memory.
type workEnv struct {
	m      Model
	sched  *fakeClient
	api    *fakeLinear
	cache  map[string][]linear.Issue
	wrote  map[string][]linear.Issue
	remem  map[string]string
	opened []string
}

func newWorkEnv(t *testing.T, workspaces ...linear.Workspace) *workEnv {
	t.Helper()
	e := &workEnv{sched: &fakeClient{errs: map[string]error{}}, api: &fakeLinear{},
		cache: map[string][]linear.Issue{}, wrote: map[string][]linear.Issue{}, remem: map[string]string{}}
	if workspaces == nil {
		workspaces = []linear.Workspace{{ID: "w1", Name: "acme", URLKey: "acme"}, {ID: "w2", Name: "Globex", URLKey: "globex"}}
	}
	e.m = New(scheduler.BoardView{Cap: 5}, Options{
		Client: e.sched,
		Open:   func(u string) error { e.opened = append(e.opened, u); return nil },
		Repos:  func() []string { return []string{"/Users/g/repos/acme/data", "/Users/g/repos/acme/web"} },
		Linear: Linear{
			Workspaces: func() ([]linear.Workspace, error) { return workspaces, nil },
			Client:     func(w linear.Workspace) LinearAPI { return wsLinear{e.api, w.ID} },
			ReadCache:  func(k string) ([]linear.Issue, bool) { is, ok := e.cache[k]; return is, ok },
			WriteCache: func(k string, is []linear.Issue) { e.wrote[k] = is },
			RememberRepo: func(p, r string) error {
				e.remem[p] = r
				return nil
			},
		},
	})
	e.m.online = true
	e.m.width, e.m.height = 100, 30
	e.m.layout()
	return e
}

// drain runs a Cmd and every Cmd it leads to, as the tea runtime does.
func drain(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			m = drain(m, c)
		}
		return m
	}
	t, next := m.Update(msg)
	return drain(t.(Model), next)
}

func (e *workEnv) press(keys ...string) {
	for _, k := range keys {
		var cmd tea.Cmd
		e.m, cmd = press(e.m, k)
		e.m = drain(e.m, cmd)
	}
}

// inOrder fails unless each of want is in s, after the one before it.
func inOrder(t *testing.T, s string, want ...string) {
	t.Helper()
	at := -1
	for _, w := range want {
		i := strings.Index(s, w)
		if i < 0 || i <= at {
			t.Fatalf("want %q after %q in:\n%s", w, want, s)
		}
		at = i
	}
}
