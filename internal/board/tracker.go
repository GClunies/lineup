package board

import (
	"context"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/markdown"
	"github.com/gclunies/lineup/internal/scheduler"
)

// LinearAPI is the part of linear.Client that the tracker browser uses.
type LinearAPI interface {
	MyIssues(ctx context.Context) ([]linear.Issue, error)
	ProjectIssues(ctx context.Context, projectID string) ([]linear.Issue, error)
	Teams(ctx context.Context) ([]linear.Team, error)
	TeamProjects(ctx context.Context, teamID string) ([]linear.ProjectInfo, error)
	Issue(ctx context.Context, identifier string) (linear.Issue, error)
	TeamStates(ctx context.Context, teamID string) ([]linear.State, error)
	SetStatusByName(ctx context.Context, is linear.Issue, statusName string) error
	AssignToMe(ctx context.Context, issueID string) error
	Create(ctx context.Context, n linear.NewIssue) (linear.Issue, error)
	Views(ctx context.Context) ([]linear.View, error)
	ViewIssues(ctx context.Context, v linear.View) ([]linear.Issue, error)
	ViewProjects(ctx context.Context, v linear.View) ([]linear.ProjectInfo, error)
}

// Tracker holds the tracker browser's outside world. Tests replace it.
type Tracker struct {
	Workspaces func() ([]linear.Workspace, error)
	Client     func(linear.Workspace) LinearAPI
	ReadCache  func(key string) ([]linear.Issue, bool)
	WriteCache func(key string, issues []linear.Issue)
	// Repos lists the git checkouts that the Repo picker offers.
	Repos func() []string
	// RememberRepo stores the Repo picked for a Project.
	RememberRepo func(projectID, repo string) error
	// ViewOrder reads the user's rank of their Linear views, as View IDs.
	ViewOrder func() ([]string, error)
	// SaveViewOrder stores the user's rank of their tabs.
	SaveViewOrder func(ids []string) error
	// Sources lists the GitHub and Local tabs that are on, given whether a
	// Linear workspace is signed in.
	Sources func(linearOn bool) []Source
}

func defaultTracker() Tracker {
	path, pathErr := config.DefaultViewOrderPath()
	order := config.NewViewOrder(path) // one store, so two saves never write at once
	return Tracker{
		Workspaces: linear.Workspaces,
		Client:     func(ws linear.Workspace) LinearAPI { return linear.NewClient(ws) },
		ReadCache:  linear.ReadCached,
		WriteCache: linear.WriteCache,
		Repos:      func() []string { return nil },
		RememberRepo: func(projectID, repo string) error {
			path, err := config.DefaultRepoChoicesPath()
			if err != nil {
				return err
			}
			return config.NewRepoChoices(path).Set(projectID, repo)
		},
		ViewOrder: func() ([]string, error) {
			if pathErr != nil {
				return nil, pathErr
			}
			return order.Get()
		},
		SaveViewOrder: func(ids []string) error {
			if pathErr != nil {
				return pathErr
			}
			return order.Set(ids)
		},
	}
}

type trkLevel int

const (
	lvlRoot trkLevel = iota
	lvlTeams
	lvlProjects
	lvlIssues
	lvlDetail
)

type overlay int

const (
	ovNone overlay = iota
	ovAdd
	ovStatus
	ovRepo
	ovSkill
)

// noSkill is the first row of the skill picker.
const noSkill = "just go"

const noMapping = "no Repo mapping"

// tab is one tab of the browser: a Linear favorite view, a GitHub or Local
// Source, or Browse, which has neither.
type tab struct {
	ws   int // index into wss
	view *linear.View
	src  Source
}

func (tb tab) browse() bool { return tb.view == nil && tb.src == nil }

// key places the tab in the saved rank.
func (tb tab) key() string {
	if tb.src != nil {
		return tb.src.Key()
	}
	return tb.view.ID
}

// viewHome is the level at which each kind of view opens. esc never goes above it.
var viewHome = map[linear.ViewKind]trkLevel{linear.ViewIssues: lvlIssues, linear.ViewProjects: lvlProjects}

func (tb tab) home() trkLevel {
	if tb.src != nil {
		return lvlIssues
	}
	if tb.view == nil {
		return lvlRoot
	}
	return viewHome[tb.view.Kind]
}

func (tb tab) name() string {
	if tb.src != nil {
		return tb.src.Name()
	}
	if tb.view == nil {
		return "Browse"
	}
	return tb.view.Name
}

// source is one Issue list that the browser keeps in the cache.
type source struct {
	ws    linear.Workspace
	key   string
	fetch func(ctx context.Context, api LinearAPI) ([]linear.Issue, error)
}

type tracker struct {
	deps    Tracker
	clients map[string]LinearAPI
	md      *markdown.Renderer

	wss       []linear.Workspace
	wssLoaded bool
	err       string

	tabs       []tab // the favorite views in the user's rank, then Browse
	tab        int
	viewsAsked bool
	viewsWait  int                      // view lists and the saved rank still to come back
	wsViews    map[string][]linear.View // by workspace ID
	rank       []string                 // the saved rank of View IDs

	lvl      trkLevel
	ws       int  // index into wss, when my is false
	my       bool // "My Issues" across all workspaces
	team     linear.Team
	proj     linear.Project                  // ID is empty above the Issues level
	teams    map[string][]linear.Team        // by workspace ID
	projects map[string][]linear.ProjectInfo // by projKey, in linear.ProjectOrder
	projKey  string                          // a team ID, or "view:" and a View ID

	srcs       []source
	lists      map[string][]linear.Issue
	fresh      map[string]bool
	wsOf       map[string]string
	issues     []linear.Issue
	gen        int
	refreshing int

	cur       int
	filter    textinput.Model
	filtering bool

	ov      overlay
	input   textinput.Model // quick add
	states  []linear.State
	stCur   int
	pick    linear.Issue // the Issue that the overlay is about
	repos   []string
	repoIn  textinput.Model
	repoCur int
	skillIn textinput.Model
	skCur   int    // the row of the skill picker
	skill   string // the skill chosen for the start that waits on the Repo picker

	detail      linear.Issue
	detailLines []string
	vp          viewport.Model

	sources    []Source // the GitHub and Local lists that are on
	items      []Item   // the open Source tab's list
	item       *Item    // the Item that the overlay is about; nil for a Linear Issue
	itemSrc    Source   // the Source that keeps the Repo picked for item
	itemDetail Item
	itemStates []string
}

func newTracker(deps Tracker) *tracker {
	d := defaultTracker()
	if deps.Workspaces != nil {
		d.Workspaces = deps.Workspaces
	}
	if deps.Client != nil {
		d.Client = deps.Client
	}
	if deps.ReadCache != nil {
		d.ReadCache = deps.ReadCache
	}
	if deps.WriteCache != nil {
		d.WriteCache = deps.WriteCache
	}
	if deps.Repos != nil {
		d.Repos = deps.Repos
	}
	if deps.RememberRepo != nil {
		d.RememberRepo = deps.RememberRepo
	}
	if deps.ViewOrder != nil {
		d.ViewOrder = deps.ViewOrder
	}
	if deps.SaveViewOrder != nil {
		d.SaveViewOrder = deps.SaveViewOrder
	}
	if deps.Sources != nil {
		d.Sources = deps.Sources
	}
	f := textinput.New()
	f.Prompt = "/ "
	in := textinput.New()
	in.Prompt = "new issue: "
	in.CharLimit = 250
	ri := textinput.New()
	ri.Prompt = "repo: "
	si := textinput.New()
	si.Prompt = "skill: "
	for _, x := range []*textinput.Model{&f, &in, &ri, &si} {
		x.Cursor.SetMode(cursor.CursorStatic)
	}
	return &tracker{deps: d, clients: map[string]LinearAPI{}, md: markdown.New(true),
		teams: map[string][]linear.Team{}, projects: map[string][]linear.ProjectInfo{},
		lists: map[string][]linear.Issue{}, fresh: map[string]bool{}, wsOf: map[string]string{},
		tabs: []tab{{}}, wsViews: map[string][]linear.View{},
		filter: f, input: in, repoIn: ri, skillIn: si}
}

type (
	trkWorkspacesMsg struct {
		wss []linear.Workspace
		err error
	}
	trkTeamsMsg struct {
		ws    string
		teams []linear.Team
		err   error
	}
	trkViewsMsg struct {
		ws    string
		views []linear.View
		err   error
	}
	trkRankMsg struct {
		ids []string
		err error
	}
	trkRankSavedMsg struct{ err error }
	trkProjectsMsg  struct {
		key      string
		projects []linear.ProjectInfo
		err      error
	}
	trkIssuesMsg struct {
		gen    int
		key    string
		wsID   string
		issues []linear.Issue
		live   bool
		err    error
	}
	trkDetailMsg struct {
		issue linear.Issue
		lines []string
		err   error
	}
	trkStatesMsg struct {
		issue  linear.Issue
		states []linear.State
		err    error
	}
	trkWriteMsg struct {
		label string
		err   error
	}
	trkNeedRepoMsg struct {
		issue linear.Issue
		item  *Item
		skill string
	}
	trkReposMsg struct{ repos []string }
)

func (t *tracker) capturing() bool { return t.filtering || t.ov != ovNone }

func (t *tracker) curTab() tab { return t.tabs[t.tab] }

// grouped reports whether the Issue list is a Linear view's, which groups by cycle and state.
func (t *tracker) grouped() bool {
	v := t.curTab().view
	return v != nil && v.Kind == linear.ViewIssues
}

func (t *tracker) api(ws linear.Workspace) LinearAPI {
	c, ok := t.clients[ws.ID]
	if !ok {
		c = t.deps.Client(ws)
		t.clients[ws.ID] = c
	}
	return c
}

func (t *tracker) wsFor(is linear.Issue) linear.Workspace {
	id := t.wsOf[is.ID]
	for _, w := range t.wss {
		if w.ID == id {
			return w
		}
	}
	if !t.my && t.ws < len(t.wss) {
		return t.wss[t.ws]
	}
	return linear.Workspace{}
}

func (t *tracker) curWS() linear.Workspace {
	if t.ws < len(t.wss) {
		return t.wss[t.ws]
	}
	return linear.Workspace{}
}

// selected is the Issue that an action applies to.
func (t *tracker) selected() (linear.Issue, bool) {
	if t.lvl == lvlDetail {
		return t.detail, t.detail.ID != ""
	}
	if t.lvl != lvlIssues {
		return linear.Issue{}, false
	}
	vis := t.visible()
	if t.cur < 0 || t.cur >= len(vis) {
		return linear.Issue{}, false
	}
	return t.issues[vis[t.cur]], true
}

func (t *tracker) labels() []string {
	var out []string
	if t.curSource() != nil {
		for _, it := range t.items {
			out = append(out, it.ID+" "+it.Title+" "+it.Status+" "+it.Repo)
		}
		return out
	}
	switch t.lvl {
	case lvlRoot:
		out = append(out, "My Issues (all workspaces)")
		for _, w := range t.wss {
			out = append(out, w.Name+" ("+w.URLKey+")")
		}
	case lvlTeams:
		for _, x := range t.teams[t.curWS().ID] {
			out = append(out, x.Key+"  "+x.Name)
		}
	case lvlProjects:
		for _, p := range t.teamProjects() {
			out = append(out, p.Name+"  "+p.Status+"  "+p.Lead)
		}
	case lvlIssues:
		for _, is := range t.issues {
			out = append(out, is.Identifier+" "+is.Title+" "+is.State.Name)
		}
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (t *tracker) teamProjects() []linear.ProjectInfo { return t.projects[t.projKey] }

// loaded reports whether the list for the current level has come back.
func (t *tracker) loaded() bool {
	switch t.lvl {
	case lvlTeams:
		_, ok := t.teams[t.curWS().ID]
		return ok
	case lvlProjects:
		_, ok := t.projects[t.projKey]
		return ok
	}
	return true
}

// visible returns the indices of the rows that match the filter, best match first.
func (t *tracker) visible() []int {
	labels := t.labels()
	q := t.filter.Value()
	return rank(q, labels)
}

// skillRows lists the skill picker's rows for the typed text. An empty row
// starts with no skill. Typed text filters the listed skills, and a name that
// is not listed becomes its own row.
func skillRows(typed string, listed []string) []string {
	q := strings.TrimPrefix(strings.TrimSpace(typed), "/")
	if q == "" {
		return append([]string{""}, listed...)
	}
	var rows []string
	for _, i := range rank(q, listed) {
		rows = append(rows, listed[i])
	}
	if !slices.Contains(listed, q) {
		rows = append(rows, q)
	}
	return rows
}

func rank(q string, labels []string) []int {
	idx := make([]int, 0, len(labels))
	if q == "" {
		for i := range labels {
			idx = append(idx, i)
		}
		return idx
	}
	score := make([]int, len(labels))
	for i, l := range labels {
		if s, ok := fuzzy(q, l); ok {
			score[i] = s
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return score[idx[a]] < score[idx[b]] })
	return idx
}

// fuzzy matches q as a case-insensitive subsequence of s. A lower score is a tighter match.
func fuzzy(q, s string) (int, bool) {
	// A whole word that equals the query wins, so "ENG" finds ENG before ENGX.
	for _, w := range strings.Fields(s) {
		if strings.EqualFold(w, q) {
			return -1, true
		}
	}
	qr, sr := []rune(strings.ToLower(q)), []rune(strings.ToLower(s))
	score, last, qi := 0, -1, 0
	for i, r := range sr {
		if qi < len(qr) && r == qr[qi] {
			if last >= 0 {
				score += i - last - 1
			} else {
				score += i
			}
			last = i
			qi++
		}
	}
	return score, qi == len(qr)
}

func (t *tracker) clamp() {
	t.cur = max(min(t.cur, len(t.visible())-1), 0)
}

func (t *tracker) rebuild() {
	var keep string
	if is, ok := t.selected(); ok {
		keep = is.ID
	}
	var all []linear.Issue
	for _, s := range t.srcs {
		all = append(all, t.lists[s.key]...)
	}
	if t.grouped() {
		t.issues = linear.ViewOrder(all)
	} else {
		t.issues = linear.Order(all)
	}
	if keep != "" {
		for i, v := range t.visible() {
			if t.issues[v].ID == keep {
				t.cur = i
			}
		}
	}
	t.clamp()
}

func (t *tracker) setSources() {
	t.srcs = t.srcs[:0]
	switch {
	case t.my:
		for _, w := range t.wss {
			t.srcs = append(t.srcs, source{ws: w, key: w.ID + "/my",
				fetch: func(ctx context.Context, a LinearAPI) ([]linear.Issue, error) { return a.MyIssues(ctx) }})
		}
	default:
		id := t.proj.ID
		w := t.curWS()
		t.srcs = append(t.srcs, source{ws: w, key: w.ID + "/project:" + id,
			fetch: func(ctx context.Context, a LinearAPI) ([]linear.Issue, error) { return a.ProjectIssues(ctx, id) }})
	}
}

func viewSource(ws linear.Workspace, v linear.View) source {
	return source{ws: ws, key: ws.ID + "/view:" + v.ID,
		fetch: func(ctx context.Context, a LinearAPI) ([]linear.Issue, error) { return a.ViewIssues(ctx, v) }}
}

func (m Model) loadWorkspaces() tea.Cmd {
	f := m.tr.deps.Workspaces
	return func() tea.Msg {
		wss, err := f()
		return trkWorkspacesMsg{wss: wss, err: err}
	}
}

// loadIssues paints each list from the cache and refreshes it from Linear. The
// two Cmds race, and a cached list never replaces a live one.
func (m Model) loadIssues(initial bool) tea.Cmd {
	t := m.tr
	t.gen++
	t.fresh = map[string]bool{}
	t.refreshing = len(t.srcs)
	if initial {
		t.lists = map[string][]linear.Issue{}
		t.issues = nil
		t.cur = 0
	}
	var cmds []tea.Cmd
	gen := t.gen
	for _, s := range t.srcs {
		s, api := s, t.api(s.ws)
		if initial {
			cmds = append(cmds, func() tea.Msg {
				is, ok := t.deps.ReadCache(s.key)
				if !ok {
					return nil
				}
				return trkIssuesMsg{gen: gen, key: s.key, wsID: s.ws.ID, issues: is}
			})
		}
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			is, err := s.fetch(ctx, api)
			if err == nil {
				t.deps.WriteCache(s.key, is)
			}
			return trkIssuesMsg{gen: gen, key: s.key, wsID: s.ws.ID, issues: is, live: true, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// loadViews reads each workspace's favorite views and the saved rank.
func (m Model) loadViews() tea.Cmd {
	t := m.tr
	t.viewsWait = len(t.wss) + 1
	order := t.deps.ViewOrder
	cmds := []tea.Cmd{func() tea.Msg {
		ids, err := order()
		return trkRankMsg{ids: ids, err: err}
	}}
	for _, w := range t.wss {
		api := t.api(w)
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			vs, err := api.Views(ctx)
			return trkViewsMsg{ws: w.ID, views: vs, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// rankTabs puts the views in the saved rank first, in that order, then the
// rest in workspace and Linear order, then Browse.
func (t *tracker) rankTabs() {
	at := map[string]int{}
	for i, id := range t.rank {
		if _, ok := at[id]; !ok {
			at[id] = i
		}
	}
	var tabs []tab
	for i, w := range t.wss {
		for _, v := range t.wsViews[w.ID] {
			tabs = append(tabs, tab{ws: i, view: &v})
		}
	}
	for _, s := range t.sources {
		tabs = append(tabs, tab{src: s})
	}
	sort.SliceStable(tabs, func(a, b int) bool {
		ra, oka := at[tabs[a].key()]
		rb, okb := at[tabs[b].key()]
		if oka != okb {
			return oka
		}
		return oka && ra < rb
	})
	if len(t.wss) > 0 {
		tabs = append(tabs, tab{})
	}
	t.tabs = tabs
}

// saveRank stores the tab order. A saved View that is not a tab now, such as
// one from a workspace that failed to load, keeps its place after the tabs.
func (m Model) saveRank() tea.Cmd {
	t := m.tr
	var ids []string
	shown := map[string]bool{}
	for _, tb := range t.tabs {
		if !tb.browse() {
			ids = append(ids, tb.key())
			shown[tb.key()] = true
		}
	}
	for _, id := range t.rank {
		if !shown[id] {
			ids = append(ids, id)
		}
	}
	t.rank = ids
	save := t.deps.SaveViewOrder
	return func() tea.Msg { return trkRankSavedMsg{err: save(ids)} }
}

// openTab shows tab i at its home level.
func (m Model) openTab(i int) (Model, tea.Cmd) {
	t := m.tr
	t.tab, t.cur, t.err = i, 0, ""
	t.filtering = false
	t.filter.SetValue("")
	t.filter.Blur()
	t.my, t.proj = false, linear.Project{}
	tb := t.curTab()
	t.lvl = tb.home()
	if tb.src != nil {
		t.items = nil
		return m, m.loadItems()
	}
	if tb.view == nil {
		return m, nil
	}
	v := *tb.view
	t.ws = tb.ws
	if v.Kind == linear.ViewProjects {
		t.team, t.projKey = v.Team, "view:"+v.ID
		return m, m.loadProjects()
	}
	// Quick add needs a team, and only a predefined view names the one it shows.
	t.team = linear.Team{}
	if v.Predefined != "" {
		t.team = v.Team
	}
	t.srcs = []source{viewSource(t.curWS(), v)}
	return m, m.loadIssues(true)
}

// moveTab moves the current view tab by d places and saves the rank. Browse
// stays last.
func (m Model) moveTab(d int) (tea.Model, tea.Cmd) {
	t := m.tr
	j := t.tab + d
	if t.curTab().browse() || j < 0 || j >= len(t.tabs) || t.tabs[j].browse() {
		return m, nil
	}
	t.tabs[t.tab], t.tabs[j] = t.tabs[j], t.tabs[t.tab]
	t.tab = j
	return m, m.saveRank()
}

func (m Model) loadTeams() tea.Cmd {
	t := m.tr
	w := t.curWS()
	api := t.api(w)
	if len(t.teams[w.ID]) > 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		teams, err := api.Teams(ctx)
		return trkTeamsMsg{ws: w.ID, teams: teams, err: err}
	}
}

// loadProjects reads the Project list of a view each time, and a team's once.
func (m Model) loadProjects() tea.Cmd {
	t := m.tr
	key, team, view := t.projKey, t.team.ID, t.curTab().view
	api := t.api(t.curWS())
	if view == nil && len(t.projects[key]) > 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var ps []linear.ProjectInfo
		var err error
		if view != nil {
			ps, err = api.ViewProjects(ctx, *view)
		} else {
			ps, err = api.TeamProjects(ctx, team)
		}
		return trkProjectsMsg{key: key, projects: ps, err: err}
	}
}

func (m Model) loadIssueDetail(is linear.Issue) tea.Cmd {
	t := m.tr
	api := t.api(t.wsFor(is))
	width := max(m.width-4, 20)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		full, err := api.Issue(ctx, is.Identifier)
		if err != nil {
			return trkDetailMsg{issue: is, err: err}
		}
		return trkDetailMsg{issue: full, lines: t.md.Lines(full.ID, full.Description, width)}
	}
}

func (m Model) loadStates(is linear.Issue) tea.Cmd {
	t := m.tr
	api := t.api(t.wsFor(is))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, err := api.TeamStates(ctx, is.Team.ID)
		return trkStatesMsg{issue: is, states: s, err: err}
	}
}

// write runs one Linear write, then refreshes the list.
func (m Model) write(label string, f func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return trkWriteMsg{label: label, err: f(ctx)}
	}
}

// startIssue adds the Issue as a Task, sets its skill if any, then starts it.
// An empty repo lets the Scheduler choose; without a mapping it replies with
// ErrNoRepo.
func (m Model) startIssue(is linear.Issue, repo, skill string) tea.Cmd {
	return m.startTask(is.Identifier, scheduler.KindLinear, repo, skill, trkNeedRepoMsg{issue: is, skill: skill})
}

func (m Model) loadRepos() tea.Cmd {
	f := m.tr.deps.Repos
	return func() tea.Msg { return trkReposMsg{repos: f()} }
}

// tracker handles the tracker's messages. ok is false for any other message.
func (m Model) tracker(msg tea.Msg) (Model, tea.Cmd, bool) {
	t := m.tr
	switch msg := msg.(type) {
	case trkWorkspacesMsg:
		t.wssLoaded, t.wss = true, msg.wss
		if msg.err != nil {
			t.err = msg.err.Error()
		}
		t.clamp()
		if t.deps.Sources != nil {
			t.sources = t.deps.Sources(len(t.wss) > 0)
		}
		if (len(t.wss) > 0 || len(t.sources) > 0) && !t.viewsAsked {
			t.viewsAsked = true
			return m, m.loadViews(), true
		}
	case trkViewsMsg:
		if msg.err != nil {
			t.err = "favorite views: " + msg.err.Error()
		}
		t.wsViews[msg.ws] = msg.views
		return m.viewsBack()
	case trkRankMsg:
		if msg.err != nil {
			t.err = "view rank: " + msg.err.Error()
		}
		t.rank = msg.ids
		return m.viewsBack()
	case trkRankSavedMsg:
		if msg.err != nil {
			m.setStatus("save the view rank: "+msg.err.Error(), true)
		}
	case trkTeamsMsg:
		t.err = errText(msg.err)
		t.teams[msg.ws] = msg.teams
		t.clamp()
	case trkProjectsMsg:
		t.err = errText(msg.err)
		t.projects[msg.key] = linear.ProjectOrder(msg.projects)
		t.clamp()
	case trkIssuesMsg:
		if msg.gen != t.gen {
			return m, nil, true
		}
		if msg.live {
			t.refreshing--
			if msg.err != nil {
				t.err = msg.err.Error()
				if len(msg.issues) == 0 {
					return m, nil, true
				}
			} else {
				t.err = ""
			}
			t.fresh[msg.key] = true
		} else if t.fresh[msg.key] {
			return m, nil, true
		}
		t.lists[msg.key] = msg.issues
		for _, is := range msg.issues {
			t.wsOf[is.ID] = msg.wsID
		}
		t.rebuild()
	case trkItemsMsg:
		return m.itemsBack(msg), nil, true
	case trkItemBodyMsg:
		if t.lvl != lvlDetail || t.itemDetail.ID != msg.id {
			return m, nil, true
		}
		t.err = errText(msg.err)
		t.detailLines = msg.lines
		m.fillDetail()
	case trkDetailMsg:
		if t.lvl != lvlDetail || t.detail.ID != msg.issue.ID {
			return m, nil, true
		}
		if msg.err != nil {
			t.err = msg.err.Error()
			return m, nil, true
		}
		t.detail, t.detailLines = msg.issue, msg.lines
		m.fillDetail()
	case trkStatesMsg:
		if msg.err != nil {
			m.setStatus("status: "+msg.err.Error(), true)
			break
		}
		t.ov, t.pick, t.item, t.states, t.stCur = ovStatus, msg.issue, nil, msg.states, 0
		for i, s := range msg.states {
			if s.ID == msg.issue.State.ID {
				t.stCur = i
			}
		}
	case trkWriteMsg:
		if msg.err != nil {
			m.setStatus(msg.label+": "+msg.err.Error(), true)
			break
		}
		m.setStatus(msg.label+" ok", false)
		if t.curSource() != nil {
			return m, m.loadItems(), true
		}
		if t.lvl == lvlIssues || t.lvl == lvlDetail {
			return m, m.loadIssues(false), true
		}
	case trkNeedRepoMsg:
		t.ov, t.pick, t.item, t.skill = ovRepo, msg.issue, msg.item, msg.skill
		t.repoIn.SetValue("")
		t.repoCur = 0
		return m, tea.Batch(m.loadRepos(), t.repoIn.Focus()), true
	case trkReposMsg:
		t.repos = msg.repos
		t.repoCur = 0
	default:
		return m, nil, false
	}
	return m, nil, true
}

// viewsBack counts one view list or the rank as back. When all are back, it
// ranks the tabs and opens the first.
func (m Model) viewsBack() (Model, tea.Cmd, bool) {
	t := m.tr
	if t.viewsWait--; t.viewsWait > 0 {
		return m, nil, true
	}
	err := t.err
	t.rankTabs()
	m, cmd := m.openTab(0)
	t.err = err
	return m, cmd, true
}

func (m *Model) fillDetail() {
	t := m.tr
	t.vp.Width, t.vp.Height = m.width, max(m.height-3, 1)
	if t.curSource() != nil {
		t.vp.SetContent(m.itemDetailText())
		return
	}
	t.vp.SetContent(m.detailText())
}

func (m Model) trackerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := m.tr
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	switch t.ov {
	case ovAdd:
		switch k {
		case "esc":
			t.ov = ovNone
		case "enter":
			title := strings.TrimSpace(t.input.Value())
			t.ov = ovNone
			if title == "" {
				return m, nil
			}
			if src := t.curSource(); src != nil {
				return m, m.write("add Issue", func(ctx context.Context) error { return src.Create(ctx, title) })
			}
			n := linear.NewIssue{TeamID: t.team.ID, Title: title, ProjectID: t.proj.ID}
			api := t.api(t.curWS())
			return m, m.write("add Issue", func(ctx context.Context) error { _, err := api.Create(ctx, n); return err })
		default:
			var cmd tea.Cmd
			t.input, cmd = t.input.Update(msg)
			return m, cmd
		}
		return m, nil
	case ovStatus:
		switch k {
		case "esc":
			t.ov = ovNone
		case "j", "down":
			n := len(t.states)
			if t.item != nil {
				n = len(t.itemStates)
			}
			t.stCur = min(t.stCur+1, n-1)
		case "k", "up":
			t.stCur = max(t.stCur-1, 0)
		case "enter":
			t.ov = ovNone
			if it, src := t.item, t.curSource(); it != nil && src != nil && t.stCur < len(t.itemStates) {
				name := t.itemStates[t.stCur]
				return m, m.write("status "+it.ID, func(ctx context.Context) error { return src.SetStatus(ctx, *it, name) })
			}
			if t.stCur < len(t.states) {
				is, name := t.pick, t.states[t.stCur].Name
				api := t.api(t.wsFor(is))
				return m, m.write("status "+is.Identifier, func(ctx context.Context) error { return api.SetStatusByName(ctx, is, name) })
			}
		}
		return m, nil
	case ovSkill:
		rows := skillRows(t.skillIn.Value(), m.opts.StartSkills)
		switch k {
		case "esc":
			t.ov = ovNone
			t.skillIn.Blur()
			m.setStatus("start cancelled", false)
		case "down", "ctrl+n":
			t.skCur = min(t.skCur+1, len(rows)-1)
		case "up", "ctrl+p":
			t.skCur = max(t.skCur-1, 0)
		case "enter":
			t.ov = ovNone
			t.skillIn.Blur()
			if t.item != nil {
				return m, m.startItem(*t.item, "", rows[t.skCur])
			}
			return m, m.startIssue(t.pick, "", rows[t.skCur])
		default:
			var cmd tea.Cmd
			t.skillIn, cmd = t.skillIn.Update(msg)
			t.skCur = 0
			return m, cmd
		}
		return m, nil
	case ovRepo:
		vis := rank(t.repoIn.Value(), repoLabels(t.repos))
		switch k {
		case "esc":
			t.ov = ovNone
			t.repoIn.Blur()
			m.setStatus("start cancelled", false)
		case "down", "ctrl+n":
			t.repoCur = min(t.repoCur+1, len(vis)-1)
		case "up", "ctrl+p":
			t.repoCur = max(t.repoCur-1, 0)
		case "enter":
			if t.repoCur >= len(vis) {
				return m, nil
			}
			repo := t.repos[vis[t.repoCur]]
			is := t.pick
			t.ov = ovNone
			t.repoIn.Blur()
			if it, src := t.item, t.itemSrc; it != nil && src != nil {
				start := m.startItem(*it, repo, t.skill)
				return m, func() tea.Msg {
					if err := src.SetRepo(*it, repo); err != nil {
						return opMsg{label: "remember Repo", task: it.ID, err: err}
					}
					return start()
				}
			}
			remember := t.deps.RememberRepo
			start := m.startIssue(is, repo, t.skill)
			return m, func() tea.Msg {
				if is.Project != nil {
					if err := remember(is.Project.ID, repo); err != nil {
						return opMsg{label: "remember Repo", task: is.Identifier, err: err}
					}
				}
				return start()
			}
		default:
			var cmd tea.Cmd
			t.repoIn, cmd = t.repoIn.Update(msg)
			t.repoCur = 0
			return m, cmd
		}
		return m, nil
	}
	if t.filtering {
		switch k {
		case "esc":
			t.filtering = false
			t.filter.SetValue("")
			t.filter.Blur()
		case "enter":
			t.filtering = false
			t.filter.Blur()
		case "down", "ctrl+n":
			t.cur = min(t.cur+1, len(t.visible())-1)
		case "up", "ctrl+p":
			t.cur = max(t.cur-1, 0)
		default:
			var cmd tea.Cmd
			t.filter, cmd = t.filter.Update(msg)
			t.cur = 0
			return m, cmd
		}
		t.clamp()
		return m, nil
	}

	if t.viewsWait > 0 {
		return m, nil
	}
	if t.curSource() != nil {
		if m, cmd, ok := m.sourceKey(k); ok {
			return m, cmd
		}
	}
	switch k {
	case "esc":
		return m.back()
	case "tab", "shift+tab":
		if len(t.tabs) < 2 {
			return m, nil
		}
		d := 1
		if k == "shift+tab" {
			d = len(t.tabs) - 1
		}
		return m.openTab((t.tab + d) % len(t.tabs))
	case "<":
		return m.moveTab(-1)
	case ">":
		return m.moveTab(1)
	case "R":
		return m, m.reload()
	case "z":
		if m.opts.Zoom != nil {
			m.opts.Zoom()
		}
	case "/":
		if t.lvl != lvlDetail {
			t.filtering = true
			return m, t.filter.Focus()
		}
	case "o":
		if is, ok := t.selected(); ok && is.URL != "" {
			open := m.opts.Open
			return m, func() tea.Msg { return opMsg{label: "open", task: is.Identifier, err: open(is.URL)} }
		}
	case "s":
		if is, ok := t.selected(); ok {
			t.ov, t.pick, t.item, t.skCur = ovSkill, is, nil, 0
			t.skillIn.SetValue("")
			return m, t.skillIn.Focus()
		}
	case "c":
		if is, ok := t.selected(); ok {
			return m, m.loadStates(is)
		}
	case "A":
		if is, ok := t.selected(); ok {
			api := t.api(t.wsFor(is))
			return m, m.write("assign "+is.Identifier, func(ctx context.Context) error { return api.AssignToMe(ctx, is.ID) })
		}
	case "n":
		if t.lvl == lvlIssues && !t.my && t.team.ID != "" {
			t.ov = ovAdd
			t.input.SetValue("")
			return m, t.input.Focus()
		}
		m.setStatus("open a team's Project to add an Issue", true)
	}
	if t.lvl == lvlDetail {
		var cmd tea.Cmd
		t.vp, cmd = t.vp.Update(msg)
		return m, cmd
	}
	switch k {
	case "j", "down":
		t.cur++
		t.clamp()
	case "k", "up":
		t.cur--
		t.clamp()
	case "enter":
		return m.enter()
	}
	return m, nil
}

// reload reads what is on screen again.
func (m Model) reload() tea.Cmd {
	t := m.tr
	switch {
	case t.curSource() != nil && t.lvl == lvlDetail:
		return m.loadItemBody(t.itemDetail)
	case t.curSource() != nil:
		return m.loadItems()
	}
	switch t.lvl {
	case lvlDetail:
		return m.loadIssueDetail(t.detail)
	case lvlIssues:
		return m.loadIssues(false)
	case lvlProjects:
		delete(t.projects, t.projKey)
		return m.loadProjects()
	case lvlTeams:
		delete(t.teams, t.curWS().ID)
		return m.loadTeams()
	}
	return m.loadWorkspaces()
}

func repoLabels(repos []string) []string {
	home, _ := os.UserHomeDir()
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = strings.Replace(r, home, "~", 1)
	}
	return out
}

func (m Model) enter() (tea.Model, tea.Cmd) {
	t := m.tr
	vis := t.visible()
	if t.cur >= len(vis) {
		return m, nil
	}
	i := vis[t.cur]
	t.filter.SetValue("")
	t.cur = 0
	switch t.lvl {
	case lvlRoot:
		if i == 0 {
			t.my, t.lvl = true, lvlIssues
			t.setSources()
			return m, m.loadIssues(true)
		}
		t.my, t.ws, t.lvl = false, i-1, lvlTeams
		return m, m.loadTeams()
	case lvlTeams:
		t.team, t.lvl = t.teams[t.curWS().ID][i], lvlProjects
		t.projKey = t.team.ID
		return m, m.loadProjects()
	case lvlProjects:
		t.proj = linear.Project{ID: t.teamProjects()[i].ID, Name: t.teamProjects()[i].Name}
		t.lvl = lvlIssues
		t.setSources()
		return m, m.loadIssues(true)
	case lvlIssues:
		t.detail, t.detailLines, t.lvl = t.issues[i], nil, lvlDetail
		m.fillDetail()
		return m, m.loadIssueDetail(t.detail)
	}
	return m, nil
}

func (m Model) back() (tea.Model, tea.Cmd) {
	t := m.tr
	t.filter.SetValue("")
	t.err = ""
	if tb := t.curTab(); tb.view != nil && t.lvl == tb.home() {
		return m, nil
	}
	switch t.lvl {
	case lvlDetail:
		t.lvl = lvlIssues
	case lvlIssues:
		t.cur = 0
		if t.my {
			t.my, t.lvl = false, lvlRoot
		} else {
			t.lvl = lvlProjects
		}
	case lvlProjects:
		t.lvl, t.cur = lvlTeams, 0
	case lvlTeams:
		t.lvl, t.cur = lvlRoot, 0
	}
	return m, nil
}
