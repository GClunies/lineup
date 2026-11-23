package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/harness/claude"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/rex"
	"github.com/gclunies/lineup/internal/scheduler"
	"github.com/gclunies/lineup/internal/worktree"
)

// These adapters turn the internal packages into the Scheduler's ports.

type rexPort struct{ c *rex.Client }

func (p rexPort) EnsureSession(ctx context.Context, label string) (string, error) {
	return p.c.EnsureSession(ctx, label)
}

func (p rexPort) NewWindow(ctx context.Context, session, label, cwd string, env map[string]string, argv []string, keep bool) (scheduler.Window, error) {
	w, err := p.c.NewWindow(ctx, session, label, cwd, env, argv, keep)
	return scheduler.Window(w), err
}

func (p rexPort) Split(ctx context.Context, session, block, cwd string, argv []string) (string, error) {
	return p.c.Split(ctx, session, block, cwd, argv)
}

func (p rexPort) NewBlock(ctx context.Context, session, block, cwd string, env map[string]string, argv []string) (string, error) {
	return p.c.NewBlock(ctx, session, block, cwd, env, argv)
}

func (p rexPort) CloseBlock(ctx context.Context, session, block string) error {
	return p.c.CloseBlock(ctx, session, block)
}

func (p rexPort) CloseWindow(ctx context.Context, session, window string) error {
	return p.c.CloseWindow(ctx, session, window)
}

func (p rexPort) DestroySession(ctx context.Context, session string) error {
	return p.c.DestroySession(ctx, session)
}

func (p rexPort) IdleShell(ctx context.Context, session, block string) (bool, error) {
	return p.c.IdleShell(ctx, session, block)
}

func (p rexPort) LastExit(ctx context.Context, session, block string) (int, bool, error) {
	return p.c.LastExit(ctx, session, block)
}

func (p rexPort) Blocks(ctx context.Context, session string) (map[string]bool, error) {
	live := map[string]bool{}
	if session == "" {
		return live, nil
	}
	v, err := p.c.View(ctx, session)
	if err != nil {
		if rex.IsNotFound(err) {
			return live, nil
		}
		return nil, err
	}
	for _, w := range v.Windows {
		for _, b := range w.BlockIDs {
			live[b] = true
		}
	}
	return live, nil
}

func (p rexPort) ProgramStatus(ctx context.Context, session, block string) (scheduler.Status, error) {
	rs, err := p.c.ProgramStatus(ctx, session, block)
	if err != nil {
		return scheduler.Status{}, err
	}
	for _, r := range rs {
		if r.State != "" {
			return scheduler.Status{State: r.State, Kind: r.Kind, Msg: r.Msg}, nil
		}
	}
	return scheduler.Status{}, nil
}

func (p rexPort) Type(ctx context.Context, session, block, text string) error {
	return p.c.Type(ctx, session, block, claude.Input(text)...)
}

func (p rexPort) Focus(ctx context.Context, session, window, block string) error {
	if err := p.c.Focus(ctx, session, block); err != nil {
		return err
	}
	return p.c.Show(ctx, session, window)
}

func (p rexPort) Capture(ctx context.Context, session, block string) (string, error) {
	return p.c.Capture(ctx, session, block)
}

// rexEvents follows the Rex event stream and reconnects when it drops.
func rexEvents(ctx context.Context, c *rex.Client, log func(string, ...any)) <-chan scheduler.RexEvent {
	out := make(chan scheduler.RexEvent, 256)
	go func() {
		for ctx.Err() == nil {
			in, err := c.Events(ctx)
			if err != nil {
				log("rex events", "err", err)
				time.Sleep(time.Second)
				continue
			}
			for ev := range in {
				var se scheduler.RexEvent
				switch ev.Kind {
				case rex.ProgramStatusChanged:
					se = scheduler.RexEvent{Kind: scheduler.EvStatus, BlockID: ev.BlockID,
						Status: scheduler.Status{State: ev.Status.State, Kind: ev.Status.Kind, Msg: ev.Status.Msg}}
				case rex.ChildExited:
					se = scheduler.RexEvent{Kind: scheduler.EvExited, BlockID: ev.BlockID, ExitCode: ev.ExitCode}
				case rex.BlockClosed:
					se = scheduler.RexEvent{Kind: scheduler.EvClosed, BlockID: ev.BlockID}
				default:
					continue
				}
				out <- se
			}
			log("rex events", "err", "stream ended, reconnecting")
			time.Sleep(time.Second)
		}
	}()
	return out
}

type githubPort struct {
	c      *github.Client
	viewer string
}

func (p *githubPort) RepoSlug(ctx context.Context, repoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh repo view in %s: %w", repoPath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *githubPort) Poll(ctx context.Context, keys []scheduler.PRKey) (map[scheduler.PRKey]scheduler.PRSnapshot, error) {
	gk := make([]github.Key, len(keys))
	for i, k := range keys {
		gk[i] = github.Key{Repo: k.Repo, Branch: k.Branch, Bot: k.Bot}
	}
	prs, err := p.c.Snapshot(ctx, gk)
	if err != nil {
		return nil, err
	}
	out := map[scheduler.PRKey]scheduler.PRSnapshot{}
	for k, pr := range prs {
		raw, err := json.Marshal(pr)
		if err != nil {
			return nil, err
		}
		out[scheduler.PRKey{Repo: k.Repo, Branch: k.Branch, Bot: k.Bot}] = scheduler.PRSnapshot{Number: pr.Number, URL: pr.URL, Raw: raw}
	}
	return out, nil
}

func (p *githubPort) ReviewPRs(ctx context.Context, keys []scheduler.ReviewKey) (map[scheduler.ReviewKey]scheduler.ReviewSnapshot, error) {
	refs := make([]github.PRRef, len(keys))
	for i, k := range keys {
		refs[i] = github.PRRef{Repo: k.Repo, Number: k.Number}
	}
	prs, err := p.c.ReviewedPRs(ctx, p.viewer, refs)
	if err != nil {
		return nil, err
	}
	out := map[scheduler.ReviewKey]scheduler.ReviewSnapshot{}
	for ref, pr := range prs {
		out[scheduler.ReviewKey{Repo: ref.Repo, Number: ref.Number}] = scheduler.ReviewSnapshot{
			Repo: pr.Repo, Number: pr.Number, URL: pr.URL, Title: pr.Title, State: pr.State,
			Fork: pr.Fork, Head: pr.Head, Base: pr.Base, ReviewedAt: pr.ReviewedAt,
		}
	}
	return out, nil
}

func (p *githubPort) Signals(prev, cur *scheduler.PRSnapshot) []lifecycle.Signal {
	decode := func(s *scheduler.PRSnapshot) *github.PR {
		if s == nil {
			return nil
		}
		var pr github.PR
		if json.Unmarshal(s.Raw, &pr) != nil {
			return nil
		}
		return &pr
	}
	return github.Signals(decode(prev), decode(cur), p.viewer)
}

func (p *githubPort) Merge(ctx context.Context, repo string, n int) error {
	return p.c.Merge(ctx, repo, n)
}

func (p *githubPort) SearchPRs(ctx context.Context, searches []github.Search) (map[string][]github.PullRequest, error) {
	return p.c.SearchPRs(ctx, p.viewer, searches)
}

func (p *githubPort) TeamMembers(ctx context.Context, teams []string) (map[string][]string, error) {
	return p.c.TeamMembers(ctx, teams)
}

type worktreePort struct{ bin string }

func (p worktreePort) CreateArgv(repo, branch string) []string {
	return worktree.CreateArgv(p.bin, repo, branch)
}

func (p worktreePort) Path(ctx context.Context, repo, branch string) (string, error) {
	path, err := worktree.Path(ctx, p.bin, repo, branch)
	if err == nil && path == "" {
		err = fmt.Errorf("worktrunk made no worktree for %s", branch)
	}
	return path, err
}

func (p worktreePort) Remove(ctx context.Context, repo, branch string) error {
	err := worktree.Remove(ctx, p.bin, repo, branch)
	if errors.Is(err, worktree.ErrUncommitted) {
		return fmt.Errorf("%w: %w", scheduler.ErrUncommitted, err)
	}
	return err
}

func (p worktreePort) AddReview(ctx context.Context, repo string, pr scheduler.ReviewPR) (string, error) {
	path := worktree.ReviewPath(repo, pr.Number)
	return path, worktree.AddReview(ctx, repo, path, pr.Number, pr.Base, pr.Head)
}

func (p worktreePort) RemoveReview(ctx context.Context, repo, path string, pr scheduler.ReviewPR) (bool, error) {
	return worktree.RemoveReview(ctx, repo, path, pr.Number)
}

type harnessPort struct {
	cfg     *config.Global
	tracker trackerMux
}

func (p harnessPort) Prepare(ctx context.Context, repo, wt string) error {
	dir, err := claude.ConfigDir(ctx, repo)
	if err != nil {
		return err
	}
	return claude.Trust(dir, wt)
}

func (p harnessPort) Prompt(ctx context.Context, w scheduler.WorkerSpec) (string, error) {
	res, err := p.cfg.Resolve(w.Repo, w.Overrides)
	if err != nil {
		return "", err
	}
	st := res.Stages[w.Stage]
	info := claude.TaskInfo{ID: w.Task, Ask: w.Text, PRURL: w.PRURL, Branch: w.Branch, Repo: w.Repo, PR: res.PR,
		ReviewCycle: w.ReviewCycle, Bar: w.Bar, Standing: w.Standing, Restart: w.Restart, Steers: w.Steers}
	if w.Kind == lifecycle.KindReview {
		info.ReviewWorktree = w.Worktree
	}
	if w.Issue != nil {
		info.IssueID, info.IssueTitle, info.IssueURL = w.Issue.Identifier, w.Issue.Title, w.Issue.URL
		info.Tracker = trackerNames[w.Issue.Tracker()]
		if w.Issue.Tracker() == scheduler.KindGitHub {
			info.Fixes = "Fixes " + w.Issue.Identifier
		}
		if d, err := p.tracker.Description(ctx, *w.Issue); err == nil {
			info.IssueDescription = d
		}
	}
	extra := ""
	if st.Command != "" {
		extra = "Run " + st.Command + " first."
	}
	return claude.Prompt(w.Stage, info, st.Skill, extra), nil
}

func (p harnessPort) Command(ctx context.Context, w scheduler.WorkerSpec, prompt string) ([]string, map[string]string, error) {
	res, err := p.cfg.Resolve(w.Repo, w.Overrides)
	if err != nil {
		return nil, nil, err
	}
	var addDirs []string
	if w.Kind == lifecycle.KindReview {
		addDirs = []string{w.Worktree}
	}
	if prompt == "" && w.Conversation == "" {
		return nil, nil, fmt.Errorf("%s has no prompt and no conversation to resume", w.Stage)
	}
	dir, err := claude.ConfigDir(ctx, w.Repo)
	if err != nil {
		return nil, nil, err
	}
	argv, env := claude.Command(res.Claude, dir, res.Stages[w.Kind.FirstStage()], w.Conversation, w.Name, prompt, w.ReadOnly, addDirs...)
	return argv, env, nil
}

// HasConversation reports true when it cannot look, so a resume still tries.
func (p harnessPort) HasConversation(ctx context.Context, w scheduler.WorkerSpec) bool {
	dir, err := claude.ConfigDir(ctx, w.Repo)
	if err != nil {
		return true
	}
	return claude.HasConversation(dir, w.Conversation)
}

// Usage runs direnv once for each Repo, not once for each Task, because each
// run loads the Repo's .envrc.
func (p harnessPort) Usage(ctx context.Context, ws []scheduler.WorkerSpec) []*scheduler.Usage {
	dirs := map[string]func() (string, error){}
	for _, w := range ws {
		if dirs[w.Repo] == nil {
			dirs[w.Repo] = sync.OnceValues(func() (string, error) { return claude.ConfigDir(ctx, w.Repo) })
		}
	}
	out := make([]*scheduler.Usage, len(ws))
	var wg sync.WaitGroup
	for i, w := range ws {
		if w.Conversation == "" {
			continue
		}
		wg.Go(func() {
			dir, err := dirs[w.Repo]()
			if err != nil {
				return
			}
			u, err := claude.ReadUsage(dir, w.Conversation)
			if err != nil || u == nil {
				return
			}
			out[i] = &scheduler.Usage{Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead,
				AgentSeconds: int64(u.AgentTime / time.Second)}
		})
	}
	wg.Wait()
	return out
}

// issueTracker is one Tracker behind the mux.
type issueTracker interface {
	Lookup(ctx context.Context, identifier string) (scheduler.Issue, error)
	// Begin runs when the Task starts, for example to assign the Issue.
	Begin(ctx context.Context, is scheduler.Issue) error
	SetStatus(ctx context.Context, is scheduler.Issue, event string) error
	Description(ctx context.Context, is scheduler.Issue) (string, error)
}

// trackerMux is the Scheduler's Tracker port. It routes each call to the
// Tracker that the Issue's Kind names. prefixes are the Repo prefixes, so
// that an ID such as HOD-3 reads as a local Issue.
type trackerMux struct {
	kinds    map[string]issueTracker
	prefixes []string
}

var trackerNames = map[string]string{scheduler.KindLinear: "Linear", scheduler.KindGitHub: "GitHub", scheduler.KindLocal: "local"}

func (m trackerMux) of(kind string) (issueTracker, error) {
	if t, ok := m.kinds[kind]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("no %s Tracker", kind)
}

func (m trackerMux) Lookup(ctx context.Context, kind, identifier string) (scheduler.Issue, error) {
	if kind == "" {
		kind = scheduler.KindOf(identifier, m.prefixes)
	}
	t, err := m.of(kind)
	if err != nil {
		return scheduler.Issue{}, err
	}
	is, err := t.Lookup(ctx, identifier)
	is.Kind = kind
	return is, err
}

func (m trackerMux) Begin(ctx context.Context, is scheduler.Issue) error {
	t, err := m.of(is.Tracker())
	if err != nil {
		return err
	}
	return t.Begin(ctx, is)
}

func (m trackerMux) SetStatus(ctx context.Context, is scheduler.Issue, event string) error {
	t, err := m.of(is.Tracker())
	if err != nil {
		return err
	}
	return t.SetStatus(ctx, is, event)
}

func (m trackerMux) Description(ctx context.Context, is scheduler.Issue) (string, error) {
	t, err := m.of(is.Tracker())
	if err != nil {
		return "", err
	}
	return t.Description(ctx, is)
}

type trackerPort struct{ cfg *config.Global }

func (p *trackerPort) client(wsID string) (*linear.Client, linear.Workspace, error) {
	wss, err := linear.Workspaces()
	if err != nil {
		return nil, linear.Workspace{}, err
	}
	for _, ws := range wss {
		if ws.ID == wsID {
			return linear.NewClient(ws), ws, nil
		}
	}
	return nil, linear.Workspace{}, fmt.Errorf("not signed in to Linear workspace %s: run `lineup login`", wsID)
}

func (p *trackerPort) issue(ctx context.Context, wsID, identifier string) (linear.Issue, error) {
	c, _, err := p.client(wsID)
	if err != nil {
		return linear.Issue{}, err
	}
	return c.Issue(ctx, identifier)
}

func (p *trackerPort) Lookup(ctx context.Context, identifier string) (scheduler.Issue, error) {
	wss, err := linear.Workspaces()
	if err != nil {
		return scheduler.Issue{}, err
	}
	if len(wss) == 0 {
		return scheduler.Issue{}, errors.New("not signed in to Linear: run `lineup login`")
	}
	var errs []error
	for _, ws := range wss {
		is, err := linear.NewClient(ws).Issue(ctx, identifier)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ws.Name, err))
			continue
		}
		out := scheduler.Issue{
			Workspace: ws.ID, ID: is.ID, Identifier: is.Identifier, Title: is.Title, URL: is.URL,
			BranchName: is.BranchName, TeamKey: is.Team.Key,
		}
		if is.Project != nil {
			out.ProjectID = is.Project.ID
		}
		return out, nil
	}
	return scheduler.Issue{}, fmt.Errorf("issue %s: %w", identifier, errors.Join(errs...))
}

func (p *trackerPort) Begin(ctx context.Context, is scheduler.Issue) error {
	c, _, err := p.client(is.Workspace)
	if err != nil {
		return err
	}
	return c.AssignToMe(ctx, is.ID)
}

func (p *trackerPort) SetStatus(ctx context.Context, is scheduler.Issue, event string) error {
	c, ws, err := p.client(is.Workspace)
	if err != nil {
		return err
	}
	status, ok := p.cfg.LinearStatus(ws.URLKey, is.TeamKey, event)
	if !ok {
		return nil
	}
	full, err := c.Issue(ctx, is.Identifier)
	if err != nil {
		return err
	}
	return c.SetStatusByName(ctx, full, status)
}

func (p *trackerPort) Description(ctx context.Context, is scheduler.Issue) (string, error) {
	full, err := p.issue(ctx, is.Workspace, is.Identifier)
	return full.Description, err
}

// githubTracker reaches GitHub Issues through gh. labels maps a lifecycle
// event to the label it sets.
type githubTracker struct {
	c      *github.Client
	labels map[string]string
}

func (g githubTracker) Lookup(ctx context.Context, identifier string) (scheduler.Issue, error) {
	repo, n, err := github.ParseIssueID(identifier)
	if err != nil {
		return scheduler.Issue{}, err
	}
	is, err := g.c.Issue(ctx, repo, n)
	if err != nil {
		return scheduler.Issue{}, err
	}
	return scheduler.Issue{Identifier: is.ID(), Title: is.Title, URL: is.URL, BranchName: is.Branch()}, nil
}

func (g githubTracker) Begin(ctx context.Context, is scheduler.Issue) error {
	repo, n, err := github.ParseIssueID(is.Identifier)
	if err != nil {
		return err
	}
	return g.c.AssignToMe(ctx, repo, n)
}

// SetStatus writes nothing on merged: the PR's Fixes line closes the Issue.
func (g githubTracker) SetStatus(ctx context.Context, is scheduler.Issue, event string) error {
	repo, n, err := github.ParseIssueID(is.Identifier)
	if err != nil {
		return err
	}
	return g.c.SetLifecycleLabel(ctx, repo, n, g.labels, event)
}

func (g githubTracker) Description(ctx context.Context, is scheduler.Issue) (string, error) {
	repo, n, err := github.ParseIssueID(is.Identifier)
	if err != nil {
		return "", err
	}
	full, err := g.c.Issue(ctx, repo, n)
	return full.Body, err
}

// localTracker is the Local Tracker's folder.
type localTracker struct{ s localissues.Store }

// localStatus is the status that each lifecycle event writes. closed writes none.
var localStatus = map[string]localissues.Status{
	"started":   localissues.Started,
	"in_review": localissues.InReview,
	"merged":    localissues.Done,
}

func (l localTracker) Lookup(_ context.Context, identifier string) (scheduler.Issue, error) {
	is, err := l.s.Get(identifier)
	if err != nil {
		return scheduler.Issue{}, err
	}
	return scheduler.Issue{Identifier: is.ID(), Title: is.Title, URL: "file://" + filepath.Join(l.s.Dir, is.File()),
		BranchName: is.Branch(), RepoHint: is.Repo}, nil
}

func (l localTracker) Begin(context.Context, scheduler.Issue) error { return nil }

func (l localTracker) SetStatus(_ context.Context, is scheduler.Issue, event string) error {
	st, ok := localStatus[event]
	if !ok {
		return nil
	}
	return l.s.SetStatus(is.Identifier, st)
}

func (l localTracker) Description(_ context.Context, is scheduler.Issue) (string, error) {
	full, err := l.s.Get(is.Identifier)
	return full.Body, err
}

type configPort struct {
	cfg     *config.Global
	choices *config.RepoChoices
}

func (p configPort) Policy(repo string, overrides map[string]string) (lifecycle.Policy, error) {
	res, err := p.cfg.Resolve(repo, overrides)
	return res.Policy(), err
}

func (p configPort) RepoForIssue(is scheduler.Issue) (string, error) {
	noRepo := fmt.Errorf("%w for %s", scheduler.ErrNoRepo, is.Identifier)
	switch is.Tracker() {
	case scheduler.KindGitHub:
		slug, _, err := github.ParseIssueID(is.Identifier)
		if err != nil {
			return "", noRepo
		}
		if repo, ok := p.RepoForGitHub(slug); ok {
			return repo, nil
		}
		if c := p.cfg.KeyClash(slug); c != "" {
			return "", fmt.Errorf("%w: %s", noRepo, c)
		}
		return "", noRepo
	case scheduler.KindLocal:
		if prefix, _, ok := scheduler.SplitID(is.Identifier); ok {
			if repo, ok := p.cfg.RepoForPrefix(prefix); ok {
				return repo, nil
			}
		}
		if is.RepoHint == "" {
			return "", noRepo
		}
		repo, err := p.ResolveRepo(is.RepoHint)
		if err != nil {
			return "", fmt.Errorf("%w: %v", noRepo, err)
		}
		return repo, nil
	}
	if repo, ok := p.cfg.RepoForTeam(is.TeamKey); ok {
		return repo, nil
	}
	if is.ProjectID != "" {
		if repo, ok, _ := p.choices.Get(is.ProjectID); ok {
			return repo, nil
		}
	}
	return "", noRepo
}

func (p configPort) RepoTracker(repo string) (string, string) { return p.cfg.RepoTracker(repo) }

func (p configPort) RepoPrefix(repo string) string { return p.cfg.RepoPrefix(repo) }

func (p configPort) RepoForGitHub(slug string) (string, bool) { return p.cfg.CheckoutFor(slug) }

func (p configPort) ResolveRepo(name string) (string, error) { return p.cfg.FindCheckout(name) }

// reviewQueues are the config's queues, as the Scheduler fills them.
func reviewQueues(g *config.Global) []scheduler.Queue {
	var out []scheduler.Queue
	for _, q := range g.ReviewQueues() {
		f := q.PRFilter
		out = append(out, scheduler.Queue{Name: q.Name, Slots: q.Slots, Filter: github.Filter{Base: github.QueuePRs,
			Repo: f.Repo, Author: f.Author, AuthorTeam: f.AuthorTeam, ReviewRequested: f.ReviewRequested, Label: f.Label,
			IncludeRegex: f.IncludeRegex, ExcludeRegex: f.ExcludeRegex}})
	}
	return out
}
