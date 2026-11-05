package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/board"
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

func (p rexPort) NewWindow(ctx context.Context, session, label, cwd string, env map[string]string, argv []string) (scheduler.Window, error) {
	w, err := p.c.NewWindow(ctx, session, label, cwd, env, argv)
	return scheduler.Window(w), err
}

func (p rexPort) CloseWindow(ctx context.Context, session, window string) error {
	return p.c.CloseWindow(ctx, session, window)
}

func (p rexPort) DestroySession(ctx context.Context, session string) error {
	return p.c.DestroySession(ctx, session)
}

func (p rexPort) Blocks(ctx context.Context, session string) (map[string]bool, error) {
	live := map[string]bool{}
	if session == "" {
		return live, nil
	}
	v, err := p.c.View(ctx, session)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
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
	return p.c.Type(ctx, session, block, text)
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
		gk[i] = github.Key{Repo: k.Repo, Branch: k.Branch}
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
		out[scheduler.PRKey{Repo: k.Repo, Branch: k.Branch}] = scheduler.PRSnapshot{Number: pr.Number, URL: pr.URL, Raw: raw}
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

func (p *githubPort) MarkReady(ctx context.Context, repo string, n int) error {
	return p.c.MarkReady(ctx, repo, n)
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
	return worktree.Remove(ctx, p.bin, repo, branch)
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

func (p harnessPort) Command(ctx context.Context, w scheduler.WorkerSpec) ([]string, map[string]string, error) {
	res, err := p.cfg.Resolve(w.Repo, w.Overrides)
	if err != nil {
		return nil, nil, err
	}
	stage := w.Stage
	if stage == lifecycle.ReviewReady {
		// The Worker resumes the Reviewing conversation, so it keeps its settings.
		stage = lifecycle.Reviewing
	}
	st := res.Stages[stage]
	if st.Harness != "claude" {
		return nil, nil, fmt.Errorf("harness %q is not in the MVP: only claude", st.Harness)
	}
	dir, err := claude.ConfigDir(ctx, w.Repo)
	if err != nil {
		return nil, nil, err
	}
	info := claude.TaskInfo{Ask: w.Text, PRURL: w.PRURL, Branch: w.Branch, Repo: w.Repo, PR: res.PR}
	var addDirs []string
	if lifecycle.KindReview.Has(w.Stage) {
		info.ReviewWorktree, addDirs = w.Worktree, []string{w.Worktree}
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
	prompt := claude.Prompt(w.Stage, info, st.Skill, extra)
	if prompt == "" && w.Conversation == "" {
		return nil, nil, fmt.Errorf("%s has no prompt and no conversation to resume", w.Stage)
	}
	argv, env := claude.Command(res.Harnesses[st.Harness], dir, st, w.Conversation, prompt, addDirs...)
	return argv, env, nil
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
// Tracker that the Issue's Kind names.
type trackerMux map[string]issueTracker

var trackerNames = map[string]string{scheduler.KindLinear: "Linear", scheduler.KindGitHub: "GitHub", scheduler.KindLocal: "local"}

func (m trackerMux) of(kind string) (issueTracker, error) {
	if t, ok := m[kind]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("no %s Tracker", kind)
}

func (m trackerMux) Lookup(ctx context.Context, kind, identifier string) (scheduler.Issue, error) {
	if kind == "" {
		kind = scheduler.KindOf(identifier)
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
	return scheduler.Issue{Identifier: is.ID(), Title: is.Title, URL: "file://" + filepath.Join(l.s.Dir, is.ID()+".md"),
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

func (p configPort) RepoForIssue(is scheduler.Issue) (string, bool) {
	switch is.Tracker() {
	case scheduler.KindGitHub:
		slug, _, err := github.ParseIssueID(is.Identifier)
		if err != nil {
			return "", false
		}
		return p.RepoForGitHub(slug)
	case scheduler.KindLocal:
		if is.RepoHint == "" {
			return "", false
		}
		repo, err := p.ResolveRepo(is.RepoHint)
		return repo, err == nil
	}
	if repo, ok := p.cfg.RepoForTeam(is.TeamKey); ok {
		return repo, true
	}
	if is.ProjectID != "" {
		if repo, ok, _ := p.choices.Get(is.ProjectID); ok {
			return repo, true
		}
	}
	return "", false
}

// RepoForGitHub finds the checkout whose origin is the repository owner/name:
// a [[repos]] path first, then a [[repos]] checkout with the repository's
// name. The name ignores case, as GitHub names do.
func (p configPort) RepoForGitHub(slug string) (string, bool) {
	_, name, _ := strings.Cut(slug, "/")
	paths := p.cfg.RepoPaths()
	for _, c := range p.cfg.Checkouts() {
		if strings.EqualFold(filepath.Base(c), name) {
			paths = append(paths, c)
		}
	}
	for _, path := range paths {
		if strings.EqualFold(originSlug(path), slug) {
			return path, true
		}
	}
	return "", false
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?/?$`)

// originSlug is owner/name from the checkout's origin remote, or "".
func originSlug(path string) string {
	out, err := exec.Command("git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	if m := githubRemote.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
		return m[1]
	}
	return ""
}

// ResolveRepo accepts a path, or the folder name of one [[repos]] checkout.
func (p configPort) ResolveRepo(name string) (string, error) {
	if strings.HasPrefix(name, "~/") {
		home, _ := os.UserHomeDir()
		name = filepath.Join(home, name[2:])
	}
	if strings.Contains(name, "/") {
		abs, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
			return "", fmt.Errorf("%s is not a git checkout", abs)
		}
		return abs, nil
	}
	var matches []string
	for _, c := range p.cfg.Checkouts() {
		if filepath.Base(c) == name {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no [[repos]] checkout named %s: add a [[repos]] match or pass a path", name)
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("%d [[repos]] checkouts named %s: pass a path", len(matches), name)
}

// boardSources lists the browser's GitHub and Local tabs. GitHub tabs show
// after lineup login github: one for the user's Issues, and one for each
// [[repos]] checkout whose origin is on GitHub.
func boardSources(cfg *config.Global, stateDir string) func(linearOn bool) []board.Source {
	return func(linearOn bool) []board.Source {
		var out []board.Source
		_, githubOn, _ := github.TrackerLogin(stateDir)
		if githubOn {
			c := &github.Client{}
			out = append(out, board.GitHubSource{C: c})
			var seen []string
			for _, path := range cfg.RepoPaths() {
				if slug := originSlug(path); slug != "" && !slices.Contains(seen, slug) {
					seen = append(seen, slug)
					out = append(out, board.GitHubSource{C: c, Repo: slug})
				}
			}
		}
		if cfg.LocalTrackerOn(linearOn, githubOn) {
			out = append(out, board.LocalSource{S: localissues.Store{Dir: cfg.LocalIssuesDir()}})
		}
		return out
	}
}

// reviewStores keeps the review tab's cache and tab rank in the state
// folder. The rank has its own file, apart from the Linear browser's.
func reviewStores(stateDir string) board.Reviews {
	cache := github.PRCache{Dir: filepath.Join(stateDir, "cache", "github")}
	order := config.NewViewOrder(filepath.Join(stateDir, "review_views.json"))
	return board.Reviews{ReadCache: cache.Read, WriteCache: cache.Write, Order: order.Get, SaveOrder: order.Set}
}

// reviewViews are the review tabs of the config, after My PRs and Requested.
func reviewViews(g *config.Global) []board.ReviewView {
	var out []board.ReviewView
	for _, v := range g.ReviewViews() {
		out = append(out, board.ReviewView{Key: "view:" + v.Name, Name: v.Name, Filter: prFilter(github.ViewPRs, v.PRFilter)})
	}
	for _, q := range reviewQueues(g) {
		out = append(out, board.ReviewView{Key: "queue:" + q.Name, Name: "Q: " + q.Name, Filter: q.Filter, Queue: true})
	}
	return out
}

// reviewQueues are the config's queues, as the Scheduler fills them.
func reviewQueues(g *config.Global) []scheduler.Queue {
	var out []scheduler.Queue
	for _, q := range g.ReviewQueues() {
		out = append(out, scheduler.Queue{Name: q.Name, Filter: prFilter(github.QueuePRs, q.PRFilter), Slots: q.Slots})
	}
	return out
}

func prFilter(base string, f config.PRFilter) github.Filter {
	return github.Filter{Base: base, Repos: f.Repos, Authors: f.Authors, AuthorTeams: f.AuthorTeams,
		RequestedTeams: f.RequestedTeams, Labels: f.Labels}
}
