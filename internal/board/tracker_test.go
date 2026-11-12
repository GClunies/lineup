package board

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

type fakeLinear struct {
	mu       sync.Mutex
	issues   []linear.Issue
	gate     chan struct{} // when set, ProjectIssues waits for it
	statuses []string
	assigned []string
	created  []linear.NewIssue
	// createErr is what Create returns.
	createErr error
	// teamsErr is what Teams returns, by workspace ID.
	teamsErr map[string]error
	// projectsErr comes back with the Projects, as a truncated list does.
	projectsErr error
	// views are each workspace's favorite views, by workspace ID.
	views        map[string][]linear.View
	viewIssues   map[string][]linear.Issue       // by View ID
	viewProjects map[string][]linear.ProjectInfo // by View ID
	projIssues   []string                        // the Project IDs read
	teamReads    int
	projectReads int
}

// wsLinear is fakeLinear as one workspace sees it.
type wsLinear struct {
	*fakeLinear
	ws string
}

func (f wsLinear) Views(context.Context) ([]linear.View, error) { return f.views[f.ws], nil }
func (f wsLinear) Teams(ctx context.Context) ([]linear.Team, error) {
	if err := f.teamsErr[f.ws]; err != nil {
		return nil, err
	}
	return f.fakeLinear.Teams(ctx)
}
func (f *fakeLinear) ViewIssues(_ context.Context, v linear.View) ([]linear.Issue, error) {
	return f.viewIssues[v.ID], nil
}
func (f *fakeLinear) ViewProjects(_ context.Context, v linear.View) ([]linear.ProjectInfo, error) {
	return f.viewProjects[v.ID], nil
}

func (f *fakeLinear) MyIssues(context.Context) ([]linear.Issue, error) { return f.issues, nil }
func (f *fakeLinear) ProjectIssues(_ context.Context, id string) ([]linear.Issue, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projIssues = append(f.projIssues, id)
	return f.issues, nil
}
func (f *fakeLinear) Teams(context.Context) ([]linear.Team, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamReads++
	return []linear.Team{{ID: "t1", Key: "ENG", Name: "Engineering"}}, nil
}
func (f *fakeLinear) TeamProjects(_ context.Context, team string) ([]linear.ProjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectReads++
	projects := map[string][]linear.ProjectInfo{
		"t1": {{ID: "p1", Name: "Billing", Status: "Started"}},
		"t2": {{ID: "p2", Name: "Web", Status: "Started"}},
	}
	return projects[team], f.projectsErr
}
func (f *fakeLinear) Issue(_ context.Context, id string) (linear.Issue, error) {
	all := slices.Clone(f.issues)
	for _, is := range f.viewIssues {
		all = append(all, is...)
	}
	for _, is := range all {
		if is.Identifier == id {
			is.Description = "# Goal\n\nBuild the **billing** model."
			return is, nil
		}
	}
	return linear.Issue{}, errors.New("not found")
}
func (f *fakeLinear) TeamStates(context.Context, string) ([]linear.State, error) {
	return []linear.State{{ID: "s1", Name: "Todo", Type: "unstarted"}, {ID: "s2", Name: "In Progress", Type: "started"}}, nil
}
func (f *fakeLinear) SetStatusByName(_ context.Context, is linear.Issue, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, is.Identifier+"="+name)
	return nil
}
func (f *fakeLinear) AssignToMe(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assigned = append(f.assigned, id)
	return nil
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

func lissue(id, title, state string, blockedBy ...string) linear.Issue {
	is := linear.Issue{ID: "id-" + id, Identifier: id, Title: title, URL: "https://linear.app/x/" + id,
		State: linear.State{ID: "s1", Name: state}, Team: linear.Team{ID: "t1", Key: "ENG"},
		Project: &linear.Project{ID: "p1", Name: "Billing"}}
	for _, b := range blockedBy {
		is.Blockers = append(is.Blockers, linear.Ref{ID: "id-" + b, Identifier: b})
	}
	return is
}

func linearFixture() []linear.Issue {
	return []linear.Issue{
		lissue("ENG-601", "Drop legacy views", "Todo", "ENG-590"),
		lissue("ENG-10", "Rename columns", "Todo"),
		lissue("ENG-590", "Add billing model", "In Progress"),
		lissue("ENG-9", "Fix typo", "Todo"),
	}
}

type trkEnv struct {
	m      Model
	sched  *fakeClient
	api    *fakeLinear
	cache  map[string][]linear.Issue
	wrote  map[string][]linear.Issue
	remem  map[string]string
	opened []string
	rank   []string   // what ViewOrder reads
	saved  [][]string // each SaveViewOrder call
	// saveErr is what SaveViewOrder returns.
	saveErr error
}

func newTrkEnv(t *testing.T, workspaces ...linear.Workspace) *trkEnv {
	t.Helper()
	e := &trkEnv{sched: &fakeClient{errs: map[string]error{}}, api: &fakeLinear{issues: linearFixture()},
		cache: map[string][]linear.Issue{}, wrote: map[string][]linear.Issue{}, remem: map[string]string{}}
	if workspaces == nil {
		workspaces = []linear.Workspace{{ID: "w1", Name: "acme", URLKey: "acme"}, {ID: "w2", Name: "Globex", URLKey: "globex"}}
	}
	e.m = New(scheduler.BoardView{Cap: 5}, Options{
		Client: e.sched,
		Open:   func(u string) error { e.opened = append(e.opened, u); return nil },
		Tracker: Tracker{
			Workspaces: func() ([]linear.Workspace, error) { return workspaces, nil },
			Client:     func(w linear.Workspace) LinearAPI { return wsLinear{e.api, w.ID} },
			ReadCache:  func(k string) ([]linear.Issue, bool) { is, ok := e.cache[k]; return is, ok },
			WriteCache: func(k string, is []linear.Issue) { e.wrote[k] = is },
			Repos:      func() []string { return []string{"/Users/g/repos/acme/data", "/Users/g/repos/acme/web"} },
			RememberRepo: func(p, r string) error {
				e.remem[p] = r
				return nil
			},
			ViewOrder: func() ([]string, error) { return e.rank, nil },
			SaveViewOrder: func(ids []string) error {
				e.saved = append(e.saved, ids)
				return e.saveErr
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

func (e *trkEnv) press(keys ...string) {
	for _, k := range keys {
		var cmd tea.Cmd
		e.m, cmd = press(e.m, k)
		e.m = drain(e.m, cmd)
	}
}

// open shows the Linear browser, as the 1 key does.
func (e *trkEnv) open() {
	var cmd tea.Cmd
	e.m, cmd = press(e.m, "1")
	e.m = drain(e.m, cmd)
}

func (e *trkEnv) toIssues() {
	e.open()
	e.press("j", "enter", "enter", "enter")
}

func TestKey1OpensTheBrowser(t *testing.T) {
	e := newTrkEnv(t)
	e.open()
	if v := e.m.View(); !strings.Contains(v, "My Issues (all workspaces)") || !strings.Contains(v, "Globex (globex)") {
		t.Fatalf("switcher:\n%s", v)
	}
}

func TestTrackerTree(t *testing.T) {
	e := newTrkEnv(t)
	e.open()
	if v := e.m.View(); !strings.Contains(v, "My Issues (all workspaces)") || !strings.Contains(v, "Globex (globex)") {
		t.Fatalf("switcher:\n%s", v)
	}
	e.press("j", "enter")
	if v := e.m.View(); !strings.Contains(v, "ENG  Engineering") {
		t.Fatalf("teams:\n%s", v)
	}
	e.press("enter")
	v := e.m.View()
	if !strings.Contains(v, "Billing") || strings.Contains(v, "Web") {
		t.Fatalf("projects must list only the team's:\n%s", v)
	}
	e.press("enter")
	var ids []string
	for _, is := range e.m.tr.issues {
		ids = append(ids, is.Identifier)
	}
	if want := []string{"ENG-9", "ENG-10", "ENG-590", "ENG-601"}; !slices.Equal(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	v = e.m.View()
	if !strings.Contains(v, "blocked by ENG-590") {
		t.Fatalf("blocked note missing:\n%s", v)
	}
	if len(e.wrote["w1/project:p1"]) != 4 {
		t.Fatalf("live result not cached: %v", e.wrote)
	}
	e.press("esc", "esc", "esc")
	if v := e.m.View(); !strings.Contains(v, "My Issues (all workspaces)") {
		t.Fatalf("esc x3 should reach the switcher:\n%s", v)
	}
	e.press("tab")
	if v := e.m.View(); !strings.Contains(v, "My Issues (all workspaces)") || strings.Contains(v, "Browse") {
		t.Fatalf("with no favorite views, Browse is the only tab and has no tab strip:\n%s", v)
	}
}

// A workspace can have more open Projects than one list holds. The team's
// list must still show what came back, with the reason it is short.
func TestTrackerShowsACutShortProjectList(t *testing.T) {
	e := newTrkEnv(t)
	e.api.projectsErr = linear.ErrTruncated
	e.open()
	e.press("j", "enter")
	if v := e.m.View(); !strings.Contains(v, "ENG  Engineering") {
		t.Fatalf("teams:\n%s", v)
	}
	e.press("enter")
	if v := e.m.View(); !strings.Contains(v, "Billing") || !strings.Contains(v, linear.ErrTruncated.Error()) {
		t.Fatalf("projects:\n%s", v)
	}
}

func TestTrackerMyIssuesSpansWorkspaces(t *testing.T) {
	e := newTrkEnv(t)
	e.open()
	e.press("enter")
	if len(e.m.tr.srcs) != 2 || len(e.wrote) != 2 {
		t.Fatalf("My Issues should load every workspace: srcs=%d wrote=%d", len(e.m.tr.srcs), len(e.wrote))
	}
}

func TestTrackerSignedOut(t *testing.T) {
	e := newTrkEnv(t, []linear.Workspace{}...)
	e.m.tr.deps.Workspaces = func() ([]linear.Workspace, error) { return nil, nil }
	e.open()
	if v := e.m.View(); !strings.Contains(v, "Sign in with `lineup login`") {
		t.Fatalf("no sign-in hint:\n%s", v)
	}
}

func TestTrackerStartHappyPath(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.press("j", "j", "s", "enter") // ENG-590, just go
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[0]; c.Issue != "ENG-590" || c.Repo != "" {
		t.Fatalf("add = %+v", c)
	}
	if c := e.sched.calls[1]; c.Task != "ENG-590" {
		t.Fatalf("start = %+v", c)
	}
	if !strings.Contains(e.m.status, "start ENG-590 ok") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestTrackerStartAsksForRepoAndRemembers(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.sched.errs["add"] = errors.New("no Repo mapping for ENG-9: pass a repo")
	e.press("s", "enter")
	if e.m.tr.ov != ovRepo {
		t.Fatalf("expected the Repo picker, overlay %d, status %q", e.m.tr.ov, e.m.status)
	}
	if v := e.m.View(); !strings.Contains(v, "acme/data") || !strings.Contains(v, "acme/web") {
		t.Fatalf("picker:\n%s", v)
	}
	e.press("w", "e")
	vis := rank(e.m.tr.repoIn.Value(), repoLabels(e.m.tr.repos))
	if len(vis) != 1 {
		t.Fatalf("fuzzy 'we' should leave web, got %v", vis)
	}
	e.press("enter")
	if got := e.remem["p1"]; got != "/Users/g/repos/acme/web" {
		t.Fatalf("remembered %v", e.remem)
	}
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[1]; c.Repo != "/Users/g/repos/acme/web" || c.Issue != "ENG-9" {
		t.Fatalf("second add = %+v", c)
	}
}

func TestTrackerStartOverCapUsesCapPrompt(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.sched.errs["start"] = errors.New(capFull)
	e.press("s", "enter")
	if e.m.mode != modeCap || e.m.pendingTask != "ENG-9" {
		t.Fatalf("mode=%d pending=%q", e.m.mode, e.m.pendingTask)
	}
	e.press("o")
	last := e.sched.calls[len(e.sched.calls)-1]
	if last.Op != "start" || !last.OverCap || last.Task != "ENG-9" {
		t.Fatalf("over-cap start = %+v", last)
	}
}

func TestTrackerChangeStatus(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.press("c")
	if e.m.tr.ov != ovStatus || !strings.Contains(e.m.View(), "In Progress") {
		t.Fatalf("status picker missing:\n%s", e.m.View())
	}
	e.press("j", "enter")
	if !slices.Equal(e.api.statuses, []string{"ENG-9=In Progress"}) {
		t.Fatalf("statuses = %v", e.api.statuses)
	}
}

func TestTrackerAssignOpenAndAdd(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.press("A")
	if !slices.Equal(e.api.assigned, []string{"id-ENG-9"}) {
		t.Fatalf("assigned = %v", e.api.assigned)
	}
	e.press("o")
	if !slices.Equal(e.opened, []string{"https://linear.app/x/ENG-9"}) {
		t.Fatalf("opened = %v", e.opened)
	}
	e.press("n", "h", "i", "enter")
	if len(e.api.created) != 1 || e.api.created[0] != (linear.NewIssue{TeamID: "t1", Title: "hi", ProjectID: "p1"}) {
		t.Fatalf("created = %+v", e.api.created)
	}
}

func TestTrackerSearchAndDetail(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.press("/", "b", "i", "l")
	if got := e.m.tr.visible(); len(got) != 1 || e.m.tr.issues[got[0]].Identifier != "ENG-590" {
		t.Fatalf("search 'bil' = %v", got)
	}
	e.press("enter", "enter")
	v := e.m.View()
	if e.m.tr.lvl != lvlDetail || !strings.Contains(v, "billing") || !strings.Contains(v, "Goal") {
		t.Fatalf("detail:\n%s", v)
	}
	e.press("esc")
	if e.m.tr.lvl != lvlIssues {
		t.Fatalf("esc from detail, level %d", e.m.tr.lvl)
	}
}

func TestTrackerPaintsFromCacheFirst(t *testing.T) {
	e := newTrkEnv(t)
	e.cache["w1/project:p1"] = []linear.Issue{lissue("ENG-77", "Cached row", "Todo")}
	e.api.gate = make(chan struct{})
	e.open()
	e.press("j", "enter", "enter")
	m, cmd := press(e.m, "enter")
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("want cache and live Cmds, got %T", msg)
	}
	// the cache Cmd alone paints before Linear answers
	cached := batch[0]()
	t2, _ := m.Update(cached)
	v := t2.(Model).View()
	if !strings.Contains(v, "Cached row") || !strings.Contains(v, "refreshing") {
		t.Fatalf("cache paint:\n%s", v)
	}
	close(e.api.gate)
	t3, _ := t2.(Model).Update(batch[1]())
	v = t3.(Model).View()
	if strings.Contains(v, "Cached row") || !strings.Contains(v, "ENG-590") {
		t.Fatalf("live result should replace the cache:\n%s", v)
	}
	// a late cache answer must not replace the live list
	t4, _ := t3.(Model).Update(cached)
	if strings.Contains(t4.(Model).View(), "Cached row") {
		t.Fatalf("stale cache replaced live data")
	}
}

func TestTrackerFooterSaysHowToChangeTheProject(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	if f := footerAt(e.m, 80); !strings.Contains(f, "esc other Projects") || !strings.HasSuffix(f, "? all keys") {
		t.Fatalf("footer: %q", f)
	}
	e.press("?")
	if v := e.m.View(); !strings.Contains(v, "To change the Project, press esc") {
		t.Fatalf("help:\n%s", v)
	}
}

func TestSearchPutsAnExactWordFirst(t *testing.T) {
	labels := []string{"ENGX  Engineering Experience", "ENG  Engineering", "XENG  xENG (Platform Chapter)"}
	if got := rank("eng", labels); got[0] != 1 {
		t.Fatalf("rank = %v, want ENG first", got)
	}
}

func (e *trkEnv) withSkills(skills ...string) {
	e.m.opts.StartSkills = skills
}

func TestTrackerSkillPickerListsJustGoFirst(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first", "diagnosing-bugs")
	e.toIssues()
	e.press("s")
	if e.m.tr.ov != ovSkill || len(e.sched.calls) != 0 {
		t.Fatalf("overlay %d, ops %v", e.m.tr.ov, e.sched.ops())
	}
	v := e.m.View()
	i, j, k := strings.Index(v, "just go"), strings.Index(v, "kit:test-first"), strings.Index(v, "diagnosing-bugs")
	if i < 0 || i > j || j > k {
		t.Fatalf("picker rows out of order:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "start"}) {
		t.Fatalf("just go ops = %v", got)
	}
	if strings.Contains(e.m.status, "·") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestTrackerSkillStartSetsSkill(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first", "diagnosing-bugs")
	e.toIssues()
	e.press("s", "down", "enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[1]; c.Task != "ENG-9" || c.Key != "skill" || c.Value != "kit:test-first" {
		t.Fatalf("set = %+v", c)
	}
	if !strings.Contains(e.m.status, "start ENG-9 ok · kit:test-first") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestTrackerStartsFromTheIssueScreen(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first")
	e.toIssues()
	e.press("enter")
	if e.m.tr.lvl != lvlDetail {
		t.Fatalf("enter did not open the Issue, level %d", e.m.tr.lvl)
	}
	if v := e.m.View(); !strings.Contains(v, "s start") {
		t.Fatalf("the Issue screen offers no start:\n%s", v)
	}
	e.press("s")
	if v := e.m.View(); !strings.Contains(v, "Which skill for ENG-9") {
		t.Fatalf("no picker on the Issue screen:\n%s", v)
	}
	e.press("down", "enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[0]; c.Issue != "ENG-9" {
		t.Fatalf("add = %+v", c)
	}
}

func TestTrackerSkillPickerEscCancels(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first")
	e.toIssues()
	e.press("s", "down", "esc")
	if e.m.tr.ov != ovNone || len(e.sched.calls) != 0 {
		t.Fatalf("overlay %d, ops %v", e.m.tr.ov, e.sched.ops())
	}
}

func TestTrackerPickerOpensWithNoListedSkills(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.press("s")
	if e.m.tr.ov != ovSkill || len(e.sched.calls) != 0 {
		t.Fatalf("overlay %d, ops %v", e.m.tr.ov, e.sched.ops())
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "start"}) {
		t.Fatalf("just go ops = %v", got)
	}
}

func TestTrackerTypedTextFiltersTheListedSkills(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first", "diagnosing-bugs")
	e.toIssues()
	e.press("s", "test")
	v := e.m.View()
	if strings.Contains(v, "just go") || strings.Contains(v, "diagnosing-bugs") {
		t.Fatalf("typing must hide just go and the rows that do not match:\n%s", v)
	}
	e.press("enter")
	if c := e.sched.calls[1]; c.Key != "skill" || c.Value != "kit:test-first" {
		t.Fatalf("set = %+v", c)
	}
}

func TestTrackerStartsWithATypedSkill(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first")
	e.toIssues()
	e.press("s", "/kit:plan")
	if v := e.m.View(); !strings.Contains(v, "kit:plan") {
		t.Fatalf("the typed skill is not a row:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[1]; c.Value != "kit:plan" {
		t.Fatalf("set = %+v, want the typed skill without its slash", c)
	}
	if !strings.Contains(e.m.status, "start ENG-9 ok · kit:plan") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestSkillRows(t *testing.T) {
	listed := []string{"kit:test-first", "diagnosing-bugs"}
	for _, c := range []struct {
		typed string
		want  []string
	}{
		{"", []string{"", "kit:test-first", "diagnosing-bugs"}},
		{"test", []string{"kit:test-first", "test"}},
		{"diagnosing-bugs", []string{"diagnosing-bugs"}},
		{" /tdd ", []string{"tdd"}},
	} {
		if got := skillRows(c.typed, listed); !slices.Equal(got, c.want) {
			t.Errorf("skillRows(%q) = %q, want %q", c.typed, got, c.want)
		}
	}
}

func TestTrackerSkillCarriesThroughRepoPicker(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first")
	e.toIssues()
	e.sched.errs["add"] = errors.New("no Repo mapping for ENG-9: pass a repo")
	e.press("s", "down", "enter")
	if e.m.tr.ov != ovRepo {
		t.Fatalf("expected the Repo picker, overlay %d, status %q", e.m.tr.ov, e.m.status)
	}
	delete(e.sched.errs, "add")
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[2]; c.Key != "skill" || c.Value != "kit:test-first" {
		t.Fatalf("set = %+v", c)
	}
}

func TestTrackerSkillStartOverCapUsesCapPrompt(t *testing.T) {
	e := newTrkEnv(t)
	e.withSkills("kit:test-first")
	e.toIssues()
	e.sched.errs["start"] = errors.New(capFull)
	e.press("s", "down", "enter")
	if e.m.mode != modeCap || e.m.pendingTask != "ENG-9" {
		t.Fatalf("mode=%d pending=%q", e.m.mode, e.m.pendingTask)
	}
}

func TestTrackerRReadsWhatIsOnScreenAgain(t *testing.T) {
	e := newTrkEnv(t)
	e.open()
	e.press("j", "enter")
	e.press("R")
	if e.api.teamReads != 2 {
		t.Fatalf("R on the teams read them %d times, want 2", e.api.teamReads)
	}
	e.press("enter", "R")
	if e.api.projectReads != 2 {
		t.Fatalf("R on the Projects read them %d times, want 2", e.api.projectReads)
	}
	e.press("enter")
	e.api.issues = append(e.api.issues, lissue("ENG-700", "Add the invoice export", "Todo"))
	e.press("R")
	if v := e.m.View(); !strings.Contains(v, "Add the invoice export") {
		t.Fatalf("R on the Issues did not read them again:\n%s", v)
	}
	e.press("enter")
	for i := range e.api.issues {
		e.api.issues[i].Title += " v2"
	}
	e.press("R")
	if v := e.m.View(); !strings.Contains(v, " v2") {
		t.Fatalf("R on the Issue did not read it again:\n%s", v)
	}
}

func TestTrackerSearchTakesR(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	reads := len(e.api.projIssues)
	e.press("/", "R")
	if got := e.m.tr.filter.Value(); got != "R" || len(e.api.projIssues) != reads {
		t.Fatalf("R in the search: search = %q, %d Issue reads, want R and %d", got, len(e.api.projIssues), reads)
	}
}
