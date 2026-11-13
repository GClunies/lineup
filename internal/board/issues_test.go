package board

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

var acme = linear.Workspace{ID: "w1", Name: "acme", URLKey: "acme"}

// mineIssue is a Linear Issue of the user's in a state of type typ.
func mineIssue(id, typ string, pri int, blockedBy ...string) linear.Issue {
	is := lissue(id, "Title of "+id, typ, blockedBy...)
	is.State.Type, is.Priority = typ, pri
	return is
}

func localIssue(n int, st localissues.Status, blockedBy ...string) localissues.Issue {
	return localissues.Issue{N: n, Title: fmt.Sprintf("Local %d", n), Status: st, BlockedBy: blockedBy}
}

func feed(m Model, msgs ...tea.Msg) Model {
	for _, msg := range msgs {
		t, _ := m.Update(msg)
		m = t.(Model)
	}
	return m
}

func rowIDs(rows []issueRow) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func rowKeys(rows []issueRow) []string {
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.ID+" "+r.URL)
	}
	return keys
}

func TestEachStateLandsInItsGroupOrInNone(t *testing.T) {
	linearCases := map[string]string{"triage": groupBacklog, "backlog": groupBacklog, "unstarted": groupReady,
		"started": "", "completed": "", "canceled": ""}
	localCases := map[localissues.Status]string{localissues.Backlog: groupBacklog, localissues.Todo: groupReady,
		localissues.Started: "", localissues.InReview: "", localissues.Done: "", localissues.Canceled: ""}
	for _, st := range localissues.Statuses {
		if _, ok := localCases[st]; !ok {
			t.Fatalf("local status %q has no case", st)
		}
	}
	group := func(rows []issueRow) string {
		if len(rows) == 0 {
			return ""
		}
		return rows[0].Group
	}
	for typ, want := range linearCases {
		if got := group(issueRows([]linear.Issue{mineIssue("ENG-1", typ, 0)}, nil, nil)); got != want {
			t.Errorf("Linear state type %q: group %q, want %q", typ, got, want)
		}
	}
	for st, want := range localCases {
		if got := group(issueRows(nil, []localissues.Issue{localIssue(1, st)}, nil)); got != want {
			t.Errorf("local status %q: group %q, want %q", st, got, want)
		}
	}
}

// A Task's ID hides only the Issue with that exact ID: a Linear ID is in upper
// case and a local ID in lower case.
func TestATaskHidesOnlyTheIssueWithItsExactID(t *testing.T) {
	for _, c := range []struct{ task, kept string }{{"local-5", "LOCAL-5"}, {"LOCAL-5", "local-5"}} {
		v := scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{tv(c.task, lifecycle.Working, "data", "The Task", "", time.Minute)}}
		m := feed(newModel(&fakeClient{}, v),
			mineIssuesMsg{ws: acme, issues: []linear.Issue{mineIssue("LOCAL-5", "unstarted", 0)}, live: true},
			mineLocalMsg{issues: []localissues.Issue{localIssue(5, localissues.Todo)}})
		if got := rowIDs(m.issues); !slices.Equal(got, []string{c.kept}) {
			t.Errorf("Task %s: Issue rows %v, want only %s", c.task, got, c.kept)
		}
		out := ansi.Strip(m.View())
		if n := strings.Count(out, c.task); n != 1 {
			t.Errorf("Task %s shows %d times, want once as the Task row:\n%s", c.task, n, out)
		}
		if !strings.Contains(out, c.kept+" ") {
			t.Errorf("Task %s hid %s:\n%s", c.task, c.kept, out)
		}
	}
}

func TestIssueRowsSortUnblockedThenPriorityThenLevelThenID(t *testing.T) {
	for _, c := range []struct {
		name string
		lin  []linear.Issue
		loc  []localissues.Issue
		want []string
	}{
		{"no open blocker first, whatever the priority",
			[]linear.Issue{mineIssue("ENG-1", "unstarted", 1, "ENG-50"), mineIssue("ENG-2", "unstarted", 4)}, nil,
			[]string{"ENG-2", "ENG-1"}},
		{"urgent, high, medium, low, then no priority",
			[]linear.Issue{mineIssue("ENG-1", "unstarted", 0), mineIssue("ENG-2", "unstarted", 4), mineIssue("ENG-3", "unstarted", 3),
				mineIssue("ENG-4", "unstarted", 2), mineIssue("ENG-5", "unstarted", 1)}, nil,
			[]string{"ENG-5", "ENG-4", "ENG-3", "ENG-2", "ENG-1"}},
		{"a local Issue has no priority",
			[]linear.Issue{mineIssue("ENG-7", "unstarted", 4), mineIssue("ENG-8", "unstarted", 0)},
			[]localissues.Issue{localIssue(1, localissues.Todo)},
			[]string{"ENG-7", "ENG-8", "local-1"}},
		{"a lower dependency level first, against the ID order",
			[]linear.Issue{mineIssue("ENG-9", "unstarted", 0, "ENG-10"), mineIssue("ENG-10", "unstarted", 0, "ENG-11"),
				mineIssue("ENG-11", "started", 0)}, nil,
			[]string{"ENG-10", "ENG-9"}},
		{"local blockers set the level of local Issues",
			nil, []localissues.Issue{localIssue(1, localissues.Todo, "local-2"), localIssue(2, localissues.Todo, "local-3"), localIssue(3, localissues.Started)},
			[]string{"local-2", "local-1"}},
		{"natural ID order last",
			[]linear.Issue{mineIssue("ENG-10", "unstarted", 2), mineIssue("ENG-9", "unstarted", 2), mineIssue("APP-30", "unstarted", 2)}, nil,
			[]string{"APP-30", "ENG-9", "ENG-10"}},
		{"Backlog before Ready",
			[]linear.Issue{mineIssue("ENG-1", "backlog", 1), mineIssue("ENG-2", "triage", 1), mineIssue("ENG-3", "unstarted", 4, "ENG-1")},
			[]localissues.Issue{localIssue(1, localissues.Backlog), localIssue(2, localissues.Todo)},
			[]string{"ENG-1", "ENG-2", "local-1", "local-2", "ENG-3"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := rowIDs(issueRows(c.lin, c.loc, nil)); !slices.Equal(got, c.want) {
				t.Fatalf("order %v, want %v", got, c.want)
			}
		})
	}
}

// genMine draws the user's Issues. Two workspaces can hold the same ID, with
// their own URLs. A blocker names only an earlier Issue, so the graph has no
// cycle to cut in input order.
func genMine(t *rapid.T) ([]linear.Issue, []localissues.Issue) {
	types := []string{"triage", "backlog", "unstarted", "started"}
	var lin []linear.Issue
	seen := map[string]bool{}
	for range rapid.IntRange(0, 12).Draw(t, "linear") {
		ws := rapid.SampledFrom([]string{"acme", "globex"}).Draw(t, "workspace")
		id := fmt.Sprintf("%s-%d", rapid.SampledFrom([]string{"ENG", "APP", "LOCAL"}).Draw(t, "team"), rapid.IntRange(1, 6).Draw(t, "number"))
		if seen[ws+id] {
			continue
		}
		seen[ws+id] = true
		is := mineIssue(id, rapid.SampledFrom(types).Draw(t, "type"), rapid.IntRange(0, 4).Draw(t, "priority"))
		is.ID, is.URL = ws+"/"+id, "https://linear.app/"+ws+"/issue/"+id
		if len(lin) > 0 && rapid.Bool().Draw(t, "blocked") {
			b := rapid.SampledFrom(lin).Draw(t, "blocker")
			is.Blockers = []linear.Ref{{ID: b.ID, Identifier: b.Identifier}}
		}
		is.Unblocked = len(is.Blockers) == 0 && rapid.Bool().Draw(t, "unblocked")
		lin = append(lin, is)
	}
	var loc []localissues.Issue
	for n := 1; n <= rapid.IntRange(0, 8).Draw(t, "locals"); n++ {
		var blockers []string
		if n > 1 && rapid.Bool().Draw(t, "local blocked") {
			blockers = append(blockers, fmt.Sprintf("local-%d", rapid.IntRange(1, n-1).Draw(t, "local blocker")))
		}
		loc = append(loc, localIssue(n, rapid.SampledFrom(localissues.Statuses).Draw(t, "status"), blockers...))
	}
	return lin, loc
}

func shuffled[T any](t *rapid.T, xs []T, label string) []T {
	out := slices.Clone(xs)
	for i := len(out) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, label)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// The order is total: the input order never changes it, no two rows compare
// equal, and sorting the rows again leaves them as they are. Rows are told
// apart by URL, as two workspaces can share an ID.
func TestIssueRowOrderIsTotalAndStable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lin, loc := genMine(t)
		want := issueRows(lin, loc, nil)
		got := issueRows(shuffled(t, lin, "lin"), shuffled(t, loc, "loc"), nil)
		if !slices.Equal(rowKeys(got), rowKeys(want)) {
			t.Fatalf("the input order changed the rows: %v, want %v", rowKeys(got), rowKeys(want))
		}
		for i := range want {
			for j := range want {
				c := cmpIssueRows(want[i], want[j])
				if (i == j) != (c == 0) || (i < j) != (c < 0) {
					t.Fatalf("compare %s with %s = %d at %d, %d", want[i].ID, want[j].ID, c, i, j)
				}
			}
		}
		again := slices.Clone(want)
		slices.SortStableFunc(again, cmpIssueRows)
		if !slices.Equal(rowKeys(again), rowKeys(want)) {
			t.Fatalf("a second sort moved rows: %v, want %v", rowKeys(again), rowKeys(want))
		}
	})
}

// unblocked shows only when the Issue has blockers and every one is done or
// canceled. A local blocker with no file stays open.
func TestUnblockedNeedsEveryBlockerDoneOrCanceled(t *testing.T) {
	others := []localissues.Issue{localIssue(1, localissues.Done), localIssue(2, localissues.Canceled), localIssue(3, localissues.Started)}
	for _, c := range []struct {
		name      string
		blockedBy []string
		want      string
	}{
		{"no blockers", nil, ""},
		{"a done blocker", []string{"local-1"}, "unblocked"},
		{"a canceled blocker", []string{"local-2"}, "unblocked"},
		{"a blocker ID in upper case", []string{"LOCAL-1"}, "unblocked"},
		{"done and canceled blockers", []string{"local-1", "local-2"}, "unblocked"},
		{"a done and an open blocker", []string{"local-1", "local-3"}, "blocked by local-3"},
		{"a blocker with no file", []string{"local-9"}, "blocked by local-9"},
		{"a Linear blocker", []string{"ENG-5"}, "blocked by ENG-5"},
	} {
		t.Run("local "+c.name, func(t *testing.T) {
			rows := issueRows(nil, append([]localissues.Issue{localIssue(7, localissues.Todo, c.blockedBy...)}, others...), nil)
			if len(rows) != 1 || rows[0].note() != c.want {
				t.Fatalf("rows %+v, want local-7 with note %q", rows, c.want)
			}
		})
	}
	unblocked := mineIssue("ENG-1", "unstarted", 0)
	unblocked.Unblocked = true
	for _, c := range []struct {
		name string
		is   linear.Issue
		want string
	}{
		{"every blocks relation closed", unblocked, "unblocked"},
		{"an open blocker", mineIssue("ENG-1", "unstarted", 0, "ENG-2"), "blocked by ENG-2"},
		{"no relations", mineIssue("ENG-1", "unstarted", 0), ""},
	} {
		t.Run("Linear "+c.name, func(t *testing.T) {
			rows := issueRows([]linear.Issue{c.is}, nil, nil)
			if len(rows) != 1 || rows[0].note() != c.want {
				t.Fatalf("rows %+v, want ENG-1 with note %q", rows, c.want)
			}
		})
	}
}

// mineEnv is a tracker env whose work view has Issue rows: two Linear Issues
// in Ready and Backlog, and local-1 in Ready.
func mineEnv(t *testing.T) (*trkEnv, localissues.Store) {
	t.Helper()
	e := newTrkEnv(t, acme)
	e.api.issues = []linear.Issue{mineIssue("ENG-9", "unstarted", 2), mineIssue("ENG-20", "backlog", 0)}
	store := localissues.Store{Dir: t.TempDir()}
	is, _ := store.Create("Rename the log flag", "", "")
	store.SetStatus(is.ID(), localissues.Todo)
	e.m.opts.LocalIssues = store.Dir
	e.m = drain(e.m, e.m.loadMine())
	return e, store
}

func TestTheWorkViewShowsReadyAndBacklogInTheirPlaces(t *testing.T) {
	e, _ := mineEnv(t)
	v := fixture()
	e.m = openAll(feed(e.m, boardMsg(v)))
	e.m.width = 200
	out := ansi.Strip(e.m.View())
	inOrder(t, out, "NEEDS YOU", "BACKLOG", "ENG-20", "READY", "ENG-9", "local-1", "QUEUED", "WORKING", "WAITING", "DONE")
	if h := ansi.Strip(e.m.header()); !strings.Contains(h, "1 queued   2 ready   1 backlog") {
		t.Fatalf("header %q, want the ready and backlog counts after queued", h)
	}
	rm, _ := press(e.m, "3")
	if h := ansi.Strip(rm.header()); strings.Contains(h, "ready") || strings.Contains(ansi.Strip(rm.View()), "ENG-9") {
		t.Fatalf("the review view shows Issue rows:\n%s", rm.View())
	}
}

func TestJAndKMoveAcrossTaskAndIssueRows(t *testing.T) {
	e, _ := mineEnv(t)
	e.m = openAll(feed(e.m, boardMsg(fixture())))
	e.m.sel = "ENG-588"
	var seen []string
	for range 18 {
		seen = append(seen, e.m.sel)
		e.press("j")
	}
	want := []string{"ENG-588", "ENG-590", "group:Backlog", "ENG-20", "group:Ready", "ENG-9", "local-1", "group:Queued", "ENG-601",
		"group:Working", "WEB-12", "ENG-592", "group:Waiting", "ENG-571", "group:Done", "ENG-560", "ENG-560", "ENG-560"}
	if !slices.Equal(seen, want) {
		t.Fatalf("j moved through %v, want %v", seen, want)
	}
	if !strings.Contains(e.m.View(), "▸ ENG-560") {
		t.Fatalf("the last row is not marked:\n%s", e.m.View())
	}
	e.press("k")
	if e.m.sel != "group:Done" {
		t.Fatalf("k went to %s, want the Done heading", e.m.sel)
	}
}

func TestSOnALinearIssueRowAddsItAsLinearThenStarts(t *testing.T) {
	e, _ := mineEnv(t)
	e.m.sel = "ENG-9"
	if f := footerAt(e.m, 100); !strings.Contains(f, "s start") || !strings.Contains(f, "o open the Issue") {
		t.Fatalf("footer %q, want the Issue row's keys", f)
	}
	e.press("s")
	if v := e.m.View(); !strings.Contains(v, "Which skill for ENG-9?") {
		t.Fatalf("no skill picker on the work view:\n%s", v)
	}
	delete(e.wrote, "w1/my")
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "start"}) {
		t.Fatalf("ops %v, want add then start", got)
	}
	if add := e.sched.calls[0]; add.Issue != "ENG-9" || add.Kind != scheduler.KindLinear || add.Repo != "" {
		t.Fatalf("add %+v, want ENG-9 with Kind linear", add)
	}
	if _, ok := e.wrote["w1/my"]; !ok {
		t.Fatal("the start did not fetch the Issue rows again")
	}
}

func TestSOnALocalIssueRowWithNoRepoAsksAndWritesTheRepoBack(t *testing.T) {
	e, store := mineEnv(t)
	e.sched.errs["add"] = errors.New(noMapping + " for local-1: pass a repo")
	e.m.sel = "local-1"
	e.press("s", "enter")
	if v := e.m.View(); !strings.Contains(v, "Which Repo for local-1?") {
		t.Fatalf("no Repo picker on the work view:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add", "start"}) {
		t.Fatalf("ops %v, want add, add with the Repo, then start", got)
	}
	if add := lastAdd(t, e); add.Issue != "local-1" || add.Kind != scheduler.KindLocal || add.Repo != "/Users/g/repos/acme/data" {
		t.Fatalf("add %+v, want local-1 with Kind local and the picked Repo", add)
	}
	if is, _ := store.Get("local-1"); is.Repo != "/Users/g/repos/acme/data" {
		t.Fatalf("the Repo was not written back: %+v", is)
	}
}

func TestOOnAnIssueRowOpensTheIssue(t *testing.T) {
	t.Setenv("EDITOR", "hx")
	e, store := mineEnv(t)
	e.m.sel = "ENG-9"
	e.press("o")
	if !slices.Equal(e.opened, []string{"https://linear.app/x/ENG-9"}) {
		t.Fatalf("opened %v, want ENG-9 in the browser", e.opened)
	}
	e.m.sel = "local-1"
	_, cmd := press(e.m, "o")
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

// The rows paint from the cache, a live list replaces them, and a cached list
// that comes back late does not replace the live one.
func TestIssueRowsPaintFromTheCacheThenLive(t *testing.T) {
	e := newTrkEnv(t, acme)
	e.cache["w1/my"] = []linear.Issue{mineIssue("ENG-1", "unstarted", 0)}
	e.api.issues = []linear.Issue{mineIssue("ENG-2", "unstarted", 0)}
	m, cmd, _ := e.m.mineMsg(mineWorkspacesMsg{wss: []linear.Workspace{acme}})
	reads := cmd().(tea.BatchMsg)
	cached, live := reads[0](), reads[1]()
	m = feed(m, cached)
	if got := rowIDs(m.issues); !slices.Equal(got, []string{"ENG-1"}) {
		t.Fatalf("rows from the cache %v, want ENG-1", got)
	}
	m = feed(m, live, cached)
	if got := rowIDs(m.issues); !slices.Equal(got, []string{"ENG-2"}) {
		t.Fatalf("rows after the live list %v, want ENG-2", got)
	}
	if !slices.Equal(rowIDs(issueRows(e.wrote["w1/my"], nil, nil)), []string{"ENG-2"}) {
		t.Fatalf("cache written %v, want the live list", e.wrote["w1/my"])
	}
}

func TestAFailedFetchKeepsTheRowsAndNamesTheWorkspace(t *testing.T) {
	e, _ := mineEnv(t)
	e.m = feed(e.m, mineIssuesMsg{ws: acme, live: true, err: errors.New("timeout")})
	if got := rowIDs(e.m.issues); !slices.Equal(got, []string{"ENG-20", "ENG-9", "local-1"}) {
		t.Fatalf("rows %v, want the rows of the last fetch", got)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "my Issues in acme: timeout") {
		t.Fatalf("footer %q, want the failed workspace", f)
	}
}

func TestASignedOutWorkspaceLosesItsRows(t *testing.T) {
	e, _ := mineEnv(t)
	e.m = feed(e.m, mineWorkspacesMsg{})
	if got := rowIDs(e.m.issues); !slices.Equal(got, []string{"local-1"}) {
		t.Fatalf("rows %v, want only the local Issue", got)
	}
}

func TestTheMinuteTickFetchesTheRowsAgain(t *testing.T) {
	e, _ := mineEnv(t)
	e.api.issues = []linear.Issue{mineIssue("ENG-30", "unstarted", 0)}
	_, cmd := e.m.Update(mineTick{})
	load := cmd().(tea.BatchMsg)[0]
	e.m = drain(e.m, load)
	if got := rowIDs(e.m.issues); !slices.Equal(got, []string{"ENG-30", "local-1"}) {
		t.Fatalf("rows after the tick %v, want ENG-30 and local-1", got)
	}
}
