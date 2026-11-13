package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

var epoch = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

type fakeClient struct {
	mu      sync.Mutex
	calls   []scheduler.Request
	errs    map[string]error
	capture string
	review  scheduler.ReviewResult // the answer to each review op
	open    scheduler.OpenResult   // the answer to each open op
}

func (f *fakeClient) Do(req scheduler.Request, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if err := f.errs[req.Op]; err != nil {
		delete(f.errs, req.Op)
		return err
	}
	switch req.Op {
	case "capture":
		*(out.(*string)) = f.capture
	case "show":
		*(out.(*scheduler.TaskDetail)) = scheduler.TaskDetail{TaskView: scheduler.TaskView{ID: req.Task, Stage: "Working"}, Branch: "b"}
	case "review":
		*(out.(*scheduler.ReviewResult)) = f.review
	case "open":
		*(out.(*scheduler.OpenResult)) = f.open
	}
	return nil
}
func (f *fakeClient) Watch(func(scheduler.BoardView) bool) error { return nil }

func (f *fakeClient) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s []string
	for _, c := range f.calls {
		s = append(s, c.Op)
	}
	return s
}

var groupOf = map[lifecycle.Stage]string{
	lifecycle.Queued: "Queued", lifecycle.Working: "Working", lifecycle.CIRunning: "Waiting", lifecycle.CIDebug: "Working",
	lifecycle.InReview: "Waiting", lifecycle.Responding: "Working", lifecycle.ReadyToLand: "Needs you",
	lifecycle.Done: "Done", lifecycle.Reviewing: "Working", lifecycle.ReviewReady: "Needs you",
}

func tv(id string, st lifecycle.Stage, repo, title, note string, age time.Duration) scheduler.TaskView {
	return scheduler.TaskView{ID: id, Title: title, Repo: repo, Stage: st.String(), StageKey: st.Key(),
		Group: groupOf[st], Note: note, Since: epoch.Add(-age), Live: true}
}

func fixture() scheduler.BoardView {
	flagged := tv("ENG-588", lifecycle.Working, "data", "Backfill events", "permission: Bash(make deploy)", 3*time.Minute)
	flagged.Flag, flagged.Group = "permission", "Needs you"
	question := tv("ENG-590", lifecycle.Working, "data", "Add billing model", "question", 12*time.Minute)
	question.Flag, question.Group = "question", "Needs you"
	done := tv("ENG-560", lifecycle.Done, "data", "Rename config options", "#4601", 2*time.Hour)
	done.Outcome = "merged"
	return scheduler.BoardView{Cap: 5, Started: 4, Tasks: []scheduler.TaskView{
		tv("ENG-601", lifecycle.Queued, "data", "Drop legacy views", "", 0),
		done,
		tv("ENG-571", lifecycle.InReview, "data", "Rate limit login", "#4636 2 reviewers", 26*time.Hour),
		tv("WEB-12", lifecycle.Responding, "web", "Fix map tooltip", "round 1/3", 5*time.Minute),
		flagged,
		question,
		tv("ENG-592", lifecycle.CIDebug, "data", "Fix flaky test", "attempt 2/3", time.Minute),
	}}
}

const (
	urlLogs = "https://github.com/umbrella/data/pull/4945"
	urlBase = "https://github.com/umbrella/data/pull/4955"
)

// rtv is a review Task.
func rtv(id string, st lifecycle.Stage, title, url string, age time.Duration) scheduler.TaskView {
	t := tv(id, st, "data", title, "", age)
	t.Kind, t.PRURL, t.ReviewHead = lifecycle.KindReview, url, "h1"
	return t
}

func newModel(c Client, v scheduler.BoardView) Model {
	m := New(v, Options{Client: c, Now: func() time.Time { return epoch }})
	m.online = true
	m.width, m.height = 100, 30
	m.layout()
	return m
}

func press(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var t tea.Model
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		}
		t, cmd = m.Update(msg)
		m = t.(Model)
	}
	return m, cmd
}

// run executes a Cmd and feeds its message back, as the tea runtime does.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); ok {
		return m
	}
	t, _ := m.Update(msg)
	return t.(Model)
}

func TestGroupOrderAndSelection(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	var ids []string
	for _, x := range m.tasks {
		ids = append(ids, x.ID)
	}
	if !slices.Equal(ids[:2], []string{"ENG-588", "ENG-590"}) || ids[2] != "ENG-601" || ids[3] != "WEB-12" || ids[5] != "ENG-571" || ids[6] != "ENG-560" {
		t.Fatalf("order = %v", ids)
	}
	if m.sel != "ENG-588" {
		t.Fatalf("initial selection = %s", m.sel)
	}
	m, _ = press(m, "j", "j")
	if m.sel != headingKey("Queued") {
		t.Fatalf("after jj = %s, want the Queued heading", m.sel)
	}
	m, _ = press(m, "j")
	if m.sel != "ENG-601" {
		t.Fatalf("after jjj = %s", m.sel)
	}
	m, _ = press(m, "k", "k")
	if m.sel != "ENG-590" {
		t.Fatalf("after kk = %s", m.sel)
	}
	m, _ = press(m, "k", "k", "k")
	if m.sel != "ENG-588" {
		t.Fatalf("k clamps at the top, got %s", m.sel)
	}
}

func TestSelectionSurvivesUpdate(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	m, _ = press(m, "j")
	v := fixture()
	v.Tasks = append(v.Tasks, tv("ENG-100", lifecycle.Working, "data", "New", "", 0))
	t2, _ := m.Update(boardMsg(v))
	if got := t2.(Model).sel; got != "ENG-590" {
		t.Fatalf("selection moved to %s", got)
	}
}

func TestKeyGating(t *testing.T) {
	cases := []struct {
		sel, key, op string
	}{
		{"ENG-590", "a", ""},
		{"ENG-601", "s", "start"},
		{"WEB-12", "s", ""},
		{"WEB-12", "m", ""},
		{"ENG-590", "m", ""},
		{"ENG-590", "enter", "open"},
	}
	for _, c := range cases {
		f := &fakeClient{}
		m := newModel(f, fixture())
		m.sel = c.sel
		_, cmd := press(m, c.key)
		if c.op == "" {
			if cmd != nil {
				t.Errorf("%s on %s: want no call", c.key, c.sel)
			}
			continue
		}
		if cmd == nil {
			t.Errorf("%s on %s: want %s", c.key, c.sel, c.op)
			continue
		}
		cmd()
		if got := f.ops(); len(got) != 1 || got[0] != c.op {
			t.Errorf("%s on %s: ops = %v, want %s", c.key, c.sel, got, c.op)
		}
	}
}

func TestMergeOnReadyToLand(t *testing.T) {
	v := fixture()
	v.Tasks = append(v.Tasks, tv("ENG-700", lifecycle.ReadyToLand, "data", "Ship", "#1", time.Minute))
	f := &fakeClient{}
	m := newModel(f, v)
	m.sel = "ENG-700"
	_, cmd := press(m, "m")
	cmd()
	if got := f.ops(); !slices.Equal(got, []string{"merge"}) {
		t.Fatalf("ops = %v", got)
	}
}

func TestCloseNeedsConfirm(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m, cmd := press(m, "x")
	if cmd != nil || m.mode != modeConfirm {
		t.Fatal("x should only ask")
	}
	m, cmd = press(m, "n")
	if cmd != nil || len(f.calls) != 0 {
		t.Fatal("n must not close")
	}
	m, _ = press(m, "x")
	_, cmd = press(m, "y")
	cmd()
	if got := f.ops(); !slices.Equal(got, []string{"close"}) {
		t.Fatalf("ops = %v", got)
	}
}

func TestXArchivesADoneTask(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m.sel = "ENG-560"
	if !strings.Contains(m.footer(), "archive") {
		t.Fatalf("footer = %q, want the archive hint", m.footer())
	}
	m, _ = press(m, "x")
	if !strings.Contains(m.footer(), "archive ENG-560? (y/n)") {
		t.Fatalf("footer = %q", m.footer())
	}
	_, cmd := press(m, "y")
	cmd()
	if f.calls[0].Op != "archive" || f.calls[0].Task != "ENG-560" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestXClosesTheTabOfADoneTask(t *testing.T) {
	f := &fakeClient{}
	v := fixture()
	for i := range v.Tasks {
		if v.Tasks[i].ID == "ENG-560" {
			v.Tasks[i].Tab = true
		}
	}
	m := newModel(f, v)
	m.sel = "ENG-560"
	if !strings.Contains(m.footer(), "x close") {
		t.Fatalf("footer = %q, want the close hint", m.footer())
	}
	m, _ = press(m, "x")
	if !strings.Contains(m.footer(), "close ENG-560's tab and worktree? (y/n)") {
		t.Fatalf("footer = %q", m.footer())
	}
	_, cmd := press(m, "y")
	cmd()
	if len(f.calls) != 1 || f.calls[0].Op != "close" || f.calls[0].Task != "ENG-560" {
		t.Fatalf("calls = %+v, want one close", f.calls)
	}
}

func TestNOnTheCloseOfADoneTaskKeepsTheTab(t *testing.T) {
	f := &fakeClient{}
	v := fixture()
	for i := range v.Tasks {
		v.Tasks[i].Tab = v.Tasks[i].ID == "ENG-560"
	}
	m := newModel(f, v)
	m.sel = "ENG-560"
	m, _ = press(m, "x")
	m, cmd := press(m, "n")
	if cmd != nil || len(f.calls) != 0 || !strings.Contains(m.footer(), "close cancelled") {
		t.Fatalf("n sent %+v, footer %q, want nothing sent", f.calls, m.footer())
	}
}

func startFull(t *testing.T) (*fakeClient, Model) {
	f := &fakeClient{errs: map[string]error{"start": errors.New("wip cap full")}}
	m := newModel(f, fixture())
	m.sel = "ENG-601"
	m, cmd := press(m, "s")
	m = run(m, cmd)
	if m.mode != modeCap {
		t.Fatalf("mode = %v, want the cap prompt", m.mode)
	}
	return f, m
}

func TestCapPromptOverCap(t *testing.T) {
	f, m := startFull(t)
	if !strings.Contains(m.footer(), "start once over the cap (o) or raise the cap to 6 (c)?") {
		t.Fatalf("footer = %q", m.footer())
	}
	_, cmd := press(m, "o")
	cmd()
	last := f.calls[len(f.calls)-1]
	if last.Op != "start" || !last.OverCap || last.Task != "ENG-601" {
		t.Fatalf("last call = %+v", last)
	}
}

func TestCapPromptRaise(t *testing.T) {
	f, m := startFull(t)
	_, cmd := press(m, "c")
	cmd()
	if got := f.ops(); !slices.Equal(got, []string{"start", "cap", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if f.calls[1].N != 6 || f.calls[2].OverCap {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestCapPromptCancel(t *testing.T) {
	f, m := startFull(t)
	m, cmd := press(m, "esc")
	if cmd != nil || m.mode != modeNormal || len(f.calls) != 1 {
		t.Fatal("any other key cancels")
	}
}

func TestOverCapHeaderAndOffline(t *testing.T) {
	v := fixture()
	v.OverCap = 1
	m := newModel(&fakeClient{}, v)
	m.width = 160
	h := m.header()
	for _, w := range []string{"WIP 4/5", "+1 over", "●2 need you"} {
		if !strings.Contains(h, w) {
			t.Errorf("header %q lacks %q", h, w)
		}
	}
	m.online = false
	if !strings.Contains(m.header(), "scheduler offline") {
		t.Error("offline header")
	}
}

func TestTheHeaderShowsThatReviewsArePaused(t *testing.T) {
	for _, paused := range []bool{false, true} {
		v := fixture()
		v.ReviewsPaused = paused
		m := newModel(&fakeClient{}, v)
		m.width = 200
		if got := strings.Contains(ansi.Strip(m.header()), "WIP 4/5   ●2 need you   reviews paused"); got != paused {
			t.Errorf("paused %v: the header %q shows reviews paused: %v", paused, ansi.Strip(m.header()), got)
		}
	}
}

// The work view lists and counts only build Tasks. Every view toasts both Kinds.
func TestTheWorkViewHoldsOnlyBuildTasks(t *testing.T) {
	var toasts []string
	v := fixture()
	v.Tasks = append(v.Tasks, rtv("R-3", lifecycle.Reviewing, "data#7 Fix the tooltip", urlBase, time.Hour))
	m := New(v, Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
	m.width, m.height = 160, 30
	if out := m.View(); strings.Contains(out, "R-3") || !strings.Contains(m.header(), "●2 need you") {
		t.Fatalf("the work board shows the review Task:\n%s", out)
	}
	m, _ = press(m, "j", "j", "j", "j", "j", "j", "j")
	if m.sel == "R-3" {
		t.Fatal("j reached the review Task")
	}

	v.Tasks[len(v.Tasks)-1] = rtv("R-3", lifecycle.ReviewReady, "data#7 Fix the tooltip", urlBase, 0)
	v.Tasks[2] = tv("ENG-571", lifecycle.ReadyToLand, "data", "Rate limit login", "#4636", 0)
	t2, cmd := m.Update(boardMsg(v))
	m = t2.(Model)
	if cmd != nil {
		cmd()
	}
	if want := []string{"ENG-571 Ready to land, #4636: Rate limit login"}; !slices.Equal(toasts, want) {
		t.Fatalf("toasts %q, want %q", toasts, want)
	}
}

func TestAgeAndRows(t *testing.T) {
	m := openAll(newModel(&fakeClient{}, fixture()))
	out := m.View()
	for _, w := range []string{"NEEDS YOU", "WORKING", "WAITING", "QUEUED", "DONE", "Working ⚑", "12m", "1d", "today", "merged"} {
		if !strings.Contains(out, w) {
			t.Errorf("view lacks %q", w)
		}
	}
}

func TestTheHeaderNamesNoViewAndADigitDoesNothing(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	if h := ansi.Strip(m.header()); !strings.HasPrefix(h, " lineup  WIP 4/5   ●2 need you") {
		t.Fatalf("header %q, want lineup, then the WIP cap, then the Needs you count", h)
	}
	for _, k := range []string{"1", "2", "3"} {
		before := m.View()
		var cmd tea.Cmd
		m, cmd = press(m, k)
		if cmd != nil || m.View() != before {
			t.Fatalf("%s changed the screen or ran a command:\n%s", k, m.View())
		}
	}
}

func TestPreviewRefreshes(t *testing.T) {
	f := &fakeClient{capture: "worker screen"}
	m := newModel(f, fixture())
	m, cmd := press(m, "p")
	m = run(m, cmd)
	if !strings.Contains(m.View(), "worker screen") {
		t.Fatal("preview missing")
	}
	if _, cmd := m.Update(captureTick(m.previewGen)); cmd == nil {
		t.Fatal("tick must refetch")
	}
	if _, cmd := m.Update(captureTick(m.previewGen - 1)); cmd != nil {
		t.Fatal("stale tick must stop")
	}
	m, _ = press(m, "p")
	if _, cmd := m.Update(captureTick(m.previewGen)); cmd != nil {
		t.Fatal("tick after close must stop")
	}
}

func TestEventsDetailForOtherTasks(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m.sel = "WEB-12"
	m, cmd := press(m, "f")
	m = run(m, cmd)
	if got := f.ops(); !slices.Equal(got, []string{"show"}) || !strings.Contains(m.View(), "WEB-12") {
		t.Fatalf("ops = %v", got)
	}
	m, _ = press(m, "f")
	if m.detail {
		t.Fatal("f toggles back")
	}
}

func TestRPollsNow(t *testing.T) {
	f := &fakeClient{}
	m, cmd := press(newModel(f, fixture()), "R")
	m = run(m, cmd)
	if got := f.ops(); !slices.Equal(got, []string{"poll"}) {
		t.Fatalf("R sent %v, want poll", got)
	}
	if l := footerAt(m, 100); !strings.Contains(l, "refresh ok") {
		t.Fatalf("status line = %q", l)
	}
}

func TestZoomHook(t *testing.T) {
	n := 0
	m := newModel(&fakeClient{}, fixture())
	m.opts.Zoom = func() { n++ }
	press(m, "z")
	if n != 1 {
		t.Fatal("zoom hook")
	}
}

func cacheFixture(n int) scheduler.BoardView {
	groups := []lifecycle.Stage{lifecycle.ReadyToLand, lifecycle.Working, lifecycle.InReview, lifecycle.Queued, lifecycle.Done}
	v := scheduler.BoardView{Cap: 8, Started: 20}
	for i := 0; i < n; i++ {
		v.Tasks = append(v.Tasks, tv(fmt.Sprintf("ENG-%d", 500+i), groups[i%len(groups)], "data", fmt.Sprintf("A task title number %d", i), "note text", time.Duration(i)*time.Minute))
	}
	return v
}

func writeCache(tb testing.TB, n int) string {
	p := filepath.Join(tb.TempDir(), "board.json")
	raw, _ := json.Marshal(cacheFixture(n))
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		tb.Fatal(err)
	}
	return p
}

// mineFixture is n Issue rows: Linear Issues in Ready and Backlog, half of
// them blocked by the one before, and n/2 local Issues.
func mineFixture(n int) []tea.Msg {
	var lin []linear.Issue
	var loc []localissues.Issue
	types := []string{"unstarted", "backlog", "triage"}
	for i := range n / 2 {
		is := mineIssue(fmt.Sprintf("ENG-%d", 1000+i), types[i%len(types)], i%5)
		if i%2 == 1 {
			is.Blockers = []linear.Ref{{ID: "id-" + fmt.Sprintf("ENG-%d", 999+i), Identifier: fmt.Sprintf("ENG-%d", 999+i)}}
		}
		lin = append(lin, is)
		loc = append(loc, localIssue(i+1, []localissues.Status{localissues.Todo, localissues.Backlog}[i%2]))
	}
	return []tea.Msg{mineIssuesMsg{ws: acme, issues: lin, live: true}, mineLocalMsg{issues: loc}}
}

func firstPaint(path string, mine []tea.Msg) {
	v, _ := LoadCache(path)
	m := feed(New(v, Options{Client: &fakeClient{}}), mine...)
	m.width, m.height = 120, 40
	_ = m.View()
}

func BenchmarkFirstPaint(b *testing.B) {
	path := writeCache(b, 50)
	b.ResetTimer()
	mine := mineFixture(200)
	for i := 0; i < b.N; i++ {
		firstPaint(path, mine)
	}
}

func BenchmarkKeypress(b *testing.B) {
	m := newModel(&fakeClient{}, cacheFixture(50))
	m.width, m.height = 120, 40
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = t.(Model)
		_ = m.View()
		if i%10 == 9 {
			m.sel = m.tasks[0].ID
		}
	}
}

func median(n int, f func()) time.Duration {
	d := make([]time.Duration, n)
	for i := range d {
		s := time.Now()
		f()
		d[i] = time.Since(s)
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[n/2]
}

func TestSpeedBudget(t *testing.T) {
	path := writeCache(t, 50)
	mine := mineFixture(200)
	if got := len(feed(New(scheduler.BoardView{}, Options{Client: &fakeClient{}}), mine...).issues); got != 200 {
		t.Fatalf("the fixture makes %d Issue rows, want 200", got)
	}
	if got := median(21, func() { firstPaint(path, mine) }); got > 100*time.Millisecond {
		t.Errorf("first paint median %v, budget 100ms", got)
	} else {
		t.Logf("first paint median %v", got)
	}
	m := feed(newModel(&fakeClient{}, cacheFixture(50)), mine...)
	m.width, m.height = 120, 40
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}
	if got := median(101, func() {
		t2, _ := m.Update(key)
		m = t2.(Model)
		_ = m.View()
	}); got > 16*time.Millisecond {
		t.Errorf("keypress render median %v, budget 16ms", got)
	} else {
		t.Logf("keypress render median %v", got)
	}
}

func footerAt(m Model, width int) string {
	m.width = width
	lines := strings.Split(m.View(), "\n")
	return ansi.Strip(lines[len(lines)-1])
}

// The footer names the keys that work on the selected Task, and "? all keys"
// stays on screen at any width.
func TestFooterShowsTheKeysForTheSelection(t *testing.T) {
	v := fixture()
	v.Tasks = append(v.Tasks,
		tv("ENG-700", lifecycle.ReadyToLand, "data", "Ship", "#1", time.Minute))
	m := newModel(&fakeClient{}, v)
	cases := []struct {
		sel       string
		want, not []string
	}{
		{"ENG-700", []string{"m merge"}, []string{"s start"}},
		{"ENG-601", []string{"s start"}, []string{"m merge"}},
		{"ENG-560", []string{"f details"}, []string{"x close", "s start"}},
	}
	for _, c := range cases {
		m.sel = c.sel
		for _, w := range []int{60, 80, 160} {
			f := footerAt(m, w)
			if !strings.HasSuffix(f, "? all keys") {
				t.Errorf("%s at %d: no \"? all keys\": %q", c.sel, w, f)
			}
			for _, s := range c.not {
				if strings.Contains(f, s) {
					t.Errorf("%s at %d: %q must not show: %q", c.sel, w, s, f)
				}
			}
		}
		for _, s := range c.want {
			if f := footerAt(m, 80); !strings.Contains(f, s) {
				t.Errorf("%s at 80: %q missing: %q", c.sel, s, f)
			}
		}
	}
}

func TestStatusGivesWayToHintsOnTheNextKey(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	m.setStatus("start ENG-601 ok", false)
	if f := footerAt(m, 100); !strings.Contains(f, "start ENG-601 ok") {
		t.Fatalf("status not shown: %q", f)
	}
	m, _ = press(m, "j")
	if f := footerAt(m, 100); !strings.HasSuffix(f, "? all keys") {
		t.Fatalf("hints did not come back: %q", f)
	}
}

func TestRowShowsTheSkillByItsShortName(t *testing.T) {
	v := fixture()
	for i := range v.Tasks {
		switch v.Tasks[i].ID {
		case "ENG-590":
			v.Tasks[i].Skill = "kit:test-first"
		case "WEB-12":
			v.Tasks[i].Skill = "diagnosing-bugs"
		}
	}
	out := newModel(&fakeClient{}, v).View()
	for _, w := range []string{"Working ⚑ · test-first", "Responding · diagnosing-bugs"} {
		if !strings.Contains(out, w) {
			t.Errorf("view lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "kit:") {
		t.Errorf("the row must show the short name:\n%s", out)
	}
}

func TestTheBoardToastsEachNewAlertOnce(t *testing.T) {
	var toasts []string
	m := New(fixture(), Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
	send := func(v scheduler.BoardView) {
		t2, cmd := m.Update(boardMsg(v))
		m = t2.(Model)
		if cmd != nil {
			cmd()
		}
	}
	send(fixture())
	if len(toasts) != 0 {
		t.Fatalf("the view the Board opened on toasted %q", toasts)
	}
	v := fixture()
	v.Tasks[2] = tv("ENG-571", lifecycle.ReadyToLand, "data", "Rate limit login", "#4636", 0)
	send(v)
	send(v)
	if want := []string{"ENG-571 Ready to land, #4636: Rate limit login"}; !slices.Equal(toasts, want) {
		t.Fatalf("toasts %q, want %q", toasts, want)
	}
}

func TestTheHeaderCountsTheReviewTasksThatNeedYou(t *testing.T) {
	flagged := func(id string) scheduler.TaskView {
		r := rtv(id, lifecycle.Reviewing, "data#7 Fix the tooltip", urlBase, time.Hour)
		r.Flag, r.Group = "question", "Needs you"
		return r
	}
	header := func(v scheduler.BoardView) string {
		m := New(v, Options{Client: &fakeClient{}})
		m.width = 200
		return ansi.Strip(m.header())
	}
	v := fixture()
	v.Tasks = append(v.Tasks, flagged("R-3"), flagged("R-5"), rtv("R-4", lifecycle.Reviewing, "data#8 Fix the map", urlBase, time.Hour))
	if h := header(v); !strings.Contains(h, "●2 need you (+2 in reviews)") {
		t.Errorf("header %q, want the 2 review Tasks that need you", h)
	}
	quiet := fixture()
	quiet.Tasks = append(quiet.Tasks, rtv("R-4", lifecycle.Reviewing, "data#8 Fix the map", urlBase, time.Hour))
	if h := header(quiet); strings.Contains(h, "(+") {
		t.Errorf("no review Task needs you, but the header shows a count: %q", h)
	}
}

func TestTheBoardToastsTheNextReadyIssueWhenASlotFrees(t *testing.T) {
	ready := mineIssuesMsg{ws: acme, live: true, issues: []linear.Issue{
		mineIssue("ENG-620", "backlog", 1), mineIssue("ENG-611", "unstarted", 2), mineIssue("ENG-612", "unstarted", 3),
	}}
	ready.issues[1].Title = "Move the retry job"
	landing := tv("ENG-571", lifecycle.ReadyToLand, "data", "Rate limit login", "#4636", 0)
	landing.Group = "Needs you"
	done := tv("ENG-571", lifecycle.Done, "data", "Rate limit login", "#4636", 0)
	done.Outcome = "merged"
	review := rtv("R-3", lifecycle.Reviewing, "data#7 Fix the tooltip", urlBase, time.Hour)
	board := func(started int, tasks ...scheduler.TaskView) scheduler.BoardView {
		v := fixture()
		v.Started = started
		v.Tasks = append(v.Tasks[:2:2], tasks...)
		return v
	}
	for _, c := range []struct {
		name         string
		issues       bool
		before, then scheduler.BoardView
		want         string
	}{
		{"a free slot names the first Ready row", true,
			board(4, landing, review), board(3, done, review), "2 slots free, next ENG-611: Move the retry job"},
		{"one free slot", true,
			board(5, landing, review), board(4, done, review), "1 slot free, next ENG-611: Move the retry job"},
		{"no free slot", true, board(5, landing, review), board(5, done, review), ""},
		{"no Ready row", false, board(4, landing, review), board(3, done, review), ""},
	} {
		var toasts []string
		m := New(c.before, Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
		if c.issues {
			m = feed(m, ready)
		}
		send := func(v scheduler.BoardView) {
			t2, cmd := m.Update(boardMsg(v))
			m = t2.(Model)
			if cmd != nil {
				cmd()
			}
		}
		send(c.before)
		send(c.then)
		send(c.then)
		var hints []string
		for _, s := range toasts {
			if strings.Contains(s, "free, next") {
				hints = append(hints, s)
			}
		}
		var want []string
		if c.want != "" {
			want = []string{c.want}
		}
		if !slices.Equal(hints, want) {
			t.Errorf("%s: slot toasts %q, want %q (all toasts %q)", c.name, hints, want, toasts)
		}
		if len(toasts) == 0 {
			t.Errorf("%s: the Task that reached Done made no toast", c.name)
		}
	}
}

func TestOneSlotToastForSeveralDoneTasks(t *testing.T) {
	var toasts []string
	v := fixture()
	m := New(v, Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
	m = feed(m, mineIssuesMsg{ws: acme, live: true, issues: []linear.Issue{mineIssue("ENG-611", "unstarted", 2)}})
	then := fixture()
	then.Started = 2
	for _, i := range []int{3, 6} {
		then.Tasks[i].Stage, then.Tasks[i].StageKey, then.Tasks[i].Group = lifecycle.Done.String(), lifecycle.Done.Key(), groupOf[lifecycle.Done]
		then.Tasks[i].Outcome = "merged"
	}
	_, cmd := m.Update(boardMsg(then))
	cmd()
	if want := []string{
		"WEB-12 Done, merged: Fix map tooltip",
		"ENG-592 Done, merged: Fix flaky test",
		"3 slots free, next ENG-611: Title of ENG-611",
	}; !slices.Equal(toasts, want) {
		t.Fatalf("toasts %q, want %q", toasts, want)
	}
}

func TestTheDetailPaneShowsTheLandingBar(t *testing.T) {
	d := scheduler.TaskDetail{
		TaskView: scheduler.TaskView{ID: "ENG-590", Title: "Add billing model", Stage: "Responding",
			Flag: "budget spent", FlagDetail: "2 open threads (1 bot) after 3 rounds"},
		Branch: "ada/eng-590-billing", Bar: []string{"✓ required checks passed", "✗ 2 open threads (1 bot)"},
	}
	want := "ENG-590  Add billing model\nResponding  ada/eng-590-billing\n⚑ budget spent 2 open threads (1 bot) after 3 rounds\n" +
		"Landing bar\n  ✓ required checks passed\n  ✗ 2 open threads (1 bot)\n\n"
	if got := formatDetail(d); got != want {
		t.Errorf("detail %q, want %q", got, want)
	}
}

func TestEnterRestoresAClosedWorker(t *testing.T) {
	f := &fakeClient{open: scheduler.OpenResult{Action: scheduler.OpenRestored}}
	v := fixture()
	closed := tv("ENG-700", lifecycle.Working, "data", "Backfill events", "tab closed", time.Minute)
	closed.Live, closed.Flag = false, "tab closed"
	v.Tasks = append(v.Tasks, closed)
	m := newModel(f, v)
	m.sel = "ENG-700"
	if !strings.Contains(m.footer(), "enter restore the Worker") || strings.Contains(m.footer(), "go to the Worker") {
		t.Fatalf("footer = %q, want the restore hint", m.footer())
	}
	m2, cmd := press(m, "enter")
	if cmd == nil {
		t.Fatal("enter sent nothing")
	}
	m2 = run(m2, cmd)
	if len(f.calls) != 1 || f.calls[0].Op != "open" || f.calls[0].Task != "ENG-700" {
		t.Fatalf("calls = %+v, want one open of ENG-700", f.calls)
	}
	if m2.status != "open ENG-700 ok · restored" {
		t.Fatalf("status = %q", m2.status)
	}
}

func TestEnterOnALiveWorkerOffersTheWorker(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m.sel = "ENG-590"
	if !strings.Contains(m.footer(), "enter go to the Worker") || strings.Contains(m.footer(), "restore the Worker") {
		t.Fatalf("footer = %q, want the go-to hint", m.footer())
	}
}

func TestEnterOnAQueuedTaskOffersNoRestore(t *testing.T) {
	f := &fakeClient{}
	v := fixture()
	queued := tv("ENG-701", lifecycle.Queued, "data", "Later", "", time.Minute)
	queued.Live = false
	v.Tasks = append(v.Tasks, queued)
	m := newModel(f, v)
	m.sel = "ENG-701"
	if strings.Contains(m.footer(), "restore the Worker") {
		t.Fatalf("footer = %q, want no restore for a Task that has not started", m.footer())
	}
}
