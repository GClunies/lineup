package board

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// fakeSource is a GitHub list that records each write.
type fakeSource struct {
	mu      sync.Mutex
	name    string
	items   []Item
	create  bool
	created []string
	status  []string
}

func (f *fakeSource) Name() string                         { return f.name }
func (f *fakeSource) Key() string                          { return "fake:" + f.name }
func (f *fakeSource) List(context.Context) ([]Item, error) { return f.items, nil }
func (f *fakeSource) Body(_ context.Context, it Item) (string, error) {
	return "Body of **" + it.ID + "**", nil
}
func (f *fakeSource) Statuses(Item) []string { return []string{"open", "closed"} }
func (f *fakeSource) SetStatus(_ context.Context, it Item, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = append(f.status, it.ID+"="+s)
	return nil
}
func (f *fakeSource) CanCreate() bool { return f.create }
func (f *fakeSource) Create(_ context.Context, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, title)
	return nil
}
func (f *fakeSource) SetRepo(Item, string) error { return nil }

func ghSource() *fakeSource {
	return &fakeSource{name: "GitHub: sandbox", create: true, items: []Item{
		{Kind: scheduler.KindGitHub, ID: "o/sandbox#2", Title: "Add a greeting", Status: "open", Repo: "o/sandbox", URL: "https://github.com/o/sandbox/issues/2"},
	}}
}

// sourceEnv is a browser whose Sources are on, with or without Linear.
func sourceEnv(t *testing.T, linearOn bool, srcs ...Source) *trkEnv {
	t.Helper()
	var e *trkEnv
	if linearOn {
		e = newViewsEnv(t)
	} else {
		e = newTrkEnv(t, []linear.Workspace{}...)
		e.m.tr.deps.Workspaces = func() ([]linear.Workspace, error) { return nil, nil }
	}
	e.m.tr.deps.Sources = func(on bool) []Source {
		if on != linearOn {
			t.Errorf("Sources got linearOn=%v, want %v", on, linearOn)
		}
		return srcs
	}
	e.open()
	return e
}

func lastAdd(t *testing.T, e *trkEnv) scheduler.Request {
	t.Helper()
	for _, c := range slices.Backward(e.sched.calls) {
		if c.Op == "add" {
			return c
		}
	}
	t.Fatalf("no add in %v", e.sched.ops())
	return scheduler.Request{}
}

func TestWithNoLinearTheBrowserOpensOnTheLocalTab(t *testing.T) {
	store := localissues.Store{Dir: t.TempDir()}
	store.Create("Retry failed loads", "data", "")
	store.Create("Write the runbook", "", "")
	two, _ := store.Create("Ship the billing model", "data", "")
	store.SetStatus(two.ID(), localissues.Started)
	e := sourceEnv(t, false, LocalSource{S: store})
	v := e.m.View()
	if strings.Contains(v, "lineup login") || strings.Contains(tabLine(e.m), "Browse") {
		t.Fatalf("with a Local Tracker there is nothing to sign in to, and no Browse:\n%s", v)
	}
	inOrder(t, v, "backlog · no repo", "local-2", "backlog · data", "local-1", "started · data", "local-3")

	e.press("s", "enter")
	add := lastAdd(t, e)
	if add.Issue != "local-2" || add.Kind != scheduler.KindLocal {
		t.Fatalf("add = %+v, want local-2 with Kind local", add)
	}
	if ops := e.sched.ops(); ops[len(ops)-1] != "start" {
		t.Fatalf("ops = %v", ops)
	}
}

func TestStartingALocalIssueWithNoRepoAsksAndWritesTheRepoBack(t *testing.T) {
	store := localissues.Store{Dir: t.TempDir()}
	store.Create("Write the runbook", "", "")
	e := sourceEnv(t, false, LocalSource{S: store})
	e.sched.errs["add"] = errors.New(noMapping + " for local-1: pass a repo")
	e.press("s", "enter")
	if !strings.Contains(e.m.View(), "Which Repo for local-1?") {
		t.Fatalf("no Repo picker:\n%s", e.m.View())
	}
	e.press("enter")
	if add := lastAdd(t, e); add.Repo != "/Users/g/repos/acme/data" || add.Kind != scheduler.KindLocal {
		t.Fatalf("add = %+v", add)
	}
	if is, _ := store.Get("local-1"); is.Repo != "/Users/g/repos/acme/data" {
		t.Fatalf("repo not written back: %+v", is)
	}
}

func TestASourceTabReadsChangesStatusAndCreates(t *testing.T) {
	gh := ghSource()
	e := sourceEnv(t, false, gh)
	e.press("enter")
	if v := e.m.View(); !strings.Contains(v, "o/sandbox#2") || !strings.Contains(v, "Body of") {
		t.Fatalf("detail:\n%s", v)
	}
	e.press("esc", "c", "j", "enter")
	if !slices.Equal(gh.status, []string{"o/sandbox#2=closed"}) {
		t.Fatalf("status writes %q", gh.status)
	}
	e.press("n")
	for _, r := range "Add a farewell" {
		e.press(string(r))
	}
	e.press("enter")
	if !slices.Equal(gh.created, []string{"Add a farewell"}) {
		t.Fatalf("created %q", gh.created)
	}
	e.press("o")
	if !slices.Equal(e.opened, []string{"https://github.com/o/sandbox/issues/2"}) {
		t.Fatalf("opened %q", e.opened)
	}
	mine := ghSource()
	mine.create = false
	e = sourceEnv(t, false, mine)
	e.press("n")
	if e.m.tr.ov == ovAdd || !strings.Contains(e.m.View(), "open a GitHub: <repo> tab") {
		t.Fatalf("GitHub: mine has no repository to add to:\n%s", e.m.View())
	}
}

func TestSourceTabsSitBetweenLinearViewsAndBrowse(t *testing.T) {
	e := sourceEnv(t, true, ghSource(), &fakeSource{name: "Local"})
	inOrder(t, tabLine(e.m), "[Current+ (Ada)]", "ENG Projects", "GitHub: sandbox", "Local", "Browse")
	for range 4 {
		e.press("tab")
	}
	if !strings.Contains(tabLine(e.m), "[GitHub: sandbox]") {
		t.Fatalf("tab line %q", tabLine(e.m))
	}
	e.press("<")
	want := []string{"cv-ada", "cv-team", "activeIssues:t1", "fake:GitHub: sandbox", "projects:t1", "fake:Local"}
	if got := e.saved[len(e.saved)-1]; !slices.Equal(got, want) {
		t.Fatalf("the rank keeps Source tabs by key: %q", got)
	}
	e.press(">", ">", ">")
	if got := strings.TrimSpace(tabLine(e.m)); !strings.HasSuffix(got, "Local  [GitHub: sandbox]  Browse") {
		t.Fatalf("Browse stays last: %q", got)
	}
}

func TestStartingALinearIssueSendsKindLinear(t *testing.T) {
	e := newTrkEnv(t)
	e.toIssues()
	e.press("s", "enter")
	if add := lastAdd(t, e); add.Kind != scheduler.KindLinear {
		t.Fatalf("add = %+v: the browser must name Linear, so a team keyed LOCAL still reaches it", add)
	}
}

func TestOOpensALocalIssuesFileInTheEditor(t *testing.T) {
	t.Setenv("EDITOR", "hx")
	store := localissues.Store{Dir: t.TempDir()}
	store.Create("Write the runbook", "", "")
	e := sourceEnv(t, false, LocalSource{S: store})
	_, cmd, _ := e.m.sourceKey("o")
	// Bubble Tea keeps the *exec.Cmd in unexported fields, so reflect reads its Args.
	c := reflect.ValueOf(cmd()).FieldByName("cmd").Elem().Elem().Field(0).Elem()
	var args []string
	for i := range c.FieldByName("Args").Len() {
		args = append(args, c.FieldByName("Args").Index(i).String())
	}
	if want := []string{"hx", filepath.Join(store.Dir, "local-1.md")}; !slices.Equal(args, want) {
		t.Fatalf("o must run $EDITOR on the file: args %q, want %q", args, want)
	}
}

func TestALinearStatusPickAfterALocalOneStaysInTheTeamsStates(t *testing.T) {
	store := localissues.Store{Dir: t.TempDir()}
	store.Create("Write the runbook", "", "")
	e := sourceEnv(t, true, LocalSource{S: store})
	e.press("tab", "tab", "tab", "tab", "c")
	if len(e.m.tr.itemStates) != 6 || !strings.Contains(e.m.View(), "canceled") {
		t.Fatalf("Local status picker:\n%s", e.m.View())
	}
	e.press("esc", "tab", "j", "enter", "enter", "enter", "c")
	if !strings.Contains(e.m.View(), "In Progress") {
		t.Fatalf("Linear status picker:\n%s", e.m.View())
	}
	e.press("j", "j", "j", "j", "enter")
	if !slices.Equal(e.api.statuses, []string{"ENG-9=In Progress"}) {
		t.Fatalf("j must stop at the team's last state, In Progress: %v\n%s", e.api.statuses, e.m.View())
	}
}

func TestTheTabRowKeepsTheOpenTabInView(t *testing.T) {
	e := sourceEnv(t, true, ghSource(), &fakeSource{name: "GitHub: mine"}, &fakeSource{name: "Local"})
	e.m.width = 60
	for range 6 {
		e.press("tab")
	}
	if got := tabLine(e.m); !strings.Contains(got, "[Local]") || !strings.HasPrefix(strings.TrimSpace(got), "…") {
		t.Fatalf("the open tab is off screen: %q", got)
	}
	e.press("tab")
	if got := tabLine(e.m); !strings.Contains(got, "[Browse]") {
		t.Fatalf("Browse is off screen: %q", got)
	}
	e.press("tab")
	if got := tabLine(e.m); !strings.HasPrefix(got, " [Current+ (Ada)]") {
		t.Fatalf("the first tab: %q", got)
	}
}

func TestRReadsASourceTabAgain(t *testing.T) {
	gh := ghSource()
	e := sourceEnv(t, false, gh)
	gh.mu.Lock()
	gh.items = append(gh.items, Item{Kind: scheduler.KindGitHub, ID: "o/sandbox#3", Title: "Add a farewell", Status: "open", Repo: "o/sandbox"})
	gh.mu.Unlock()
	e.press("R")
	if v := e.m.View(); !strings.Contains(v, "Add a farewell") {
		t.Fatalf("R did not read the list again:\n%s", v)
	}
}
