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
	"github.com/gclunies/lineup/internal/scheduler"
)

var epoch = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

type fakeClient struct {
	mu      sync.Mutex
	calls   []scheduler.Request
	errs    map[string]error
	capture string
	review  scheduler.ReviewResult // the answer to each review op
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
	if !slices.Equal(ids[:2], []string{"ENG-588", "ENG-590"}) || ids[2] != "WEB-12" || ids[4] != "ENG-571" || ids[5] != "ENG-601" || ids[6] != "ENG-560" {
		t.Fatalf("order = %v", ids)
	}
	if m.sel != "ENG-588" {
		t.Fatalf("initial selection = %s", m.sel)
	}
	m, _ = press(m, "j", "j")
	if m.sel != "WEB-12" {
		t.Fatalf("after jj = %s", m.sel)
	}
	m, _ = press(m, "k")
	if m.sel != "ENG-590" {
		t.Fatalf("after k = %s", m.sel)
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
		{"ENG-590", "enter", "focus"},
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
	h := m.header()
	for _, w := range []string{"WIP 4/5", "+1 over", "●2 need you", "◐2 working", "◷1 waiting", "1 queued"} {
		if !strings.Contains(h, w) {
			t.Errorf("header %q lacks %q", h, w)
		}
	}
	m.online = false
	if !strings.Contains(m.header(), "scheduler offline") {
		t.Error("offline header")
	}
}

func TestOnlyTheReviewBoardShowsThatReviewsArePaused(t *testing.T) {
	for _, paused := range []bool{false, true} {
		v := fixture()
		v.ReviewsPaused = paused
		m := newModel(&fakeClient{}, v)
		if strings.Contains(m.header(), "reviews paused") {
			t.Errorf("paused %v: the work board shows reviews paused", paused)
		}
		rm := New(v, Options{Review: true, Client: &fakeClient{}})
		rm.width = 200
		if got := strings.Contains(ansi.Strip(rm.header()), "reviews paused"); got != paused {
			t.Errorf("paused %v: the review board shows reviews paused: %v", paused, got)
		}
		if strings.Contains(rm.header(), "WIP") {
			t.Errorf("the review board shows the WIP cap: %q", rm.header())
		}
	}
}

// The work board lists, counts, and toasts only build Tasks.
func TestTheWorkBoardHoldsOnlyBuildTasks(t *testing.T) {
	var toasts []string
	v := fixture()
	v.Tasks = append(v.Tasks, rtv("R-3", lifecycle.Reviewing, "data#7 Fix the tooltip", urlBase, time.Hour))
	m := New(v, Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
	m.width, m.height = 160, 30
	if out := m.View(); strings.Contains(out, "R-3") || !strings.Contains(m.header(), "◐2 working") {
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
	m := newModel(&fakeClient{}, fixture())
	out := m.View()
	for _, w := range []string{"NEEDS YOU", "WORKING", "WAITING", "QUEUED", "DONE", "Working ⚑", "12m", "1d", "today", "merged"} {
		if !strings.Contains(out, w) {
			t.Errorf("view lacks %q", w)
		}
	}
}

func TestTheBoardStaysOnTheBoard(t *testing.T) {
	m, _ := press(newModel(&fakeClient{}, fixture()), "2")
	if !strings.Contains(m.View(), "NEEDS YOU") {
		t.Fatalf("2 left the Board:\n%s", m.View())
	}
}

func TestTheLinearBrowserStaysOnLinear(t *testing.T) {
	e := newTrkEnv(t)
	e.open()
	e.press("1", "esc", "esc")
	if v := e.m.View(); strings.Contains(v, "NEEDS YOU") || !strings.Contains(v, "My Issues (all workspaces)") {
		t.Fatalf("the Linear browser left Linear:\n%s", v)
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

func firstPaint(path string) {
	v, _ := LoadCache(path)
	m := New(v, Options{Client: &fakeClient{}})
	m.width, m.height = 120, 40
	_ = m.View()
}

func BenchmarkFirstPaint(b *testing.B) {
	path := writeCache(b, 50)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		firstPaint(path)
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
	if got := median(21, func() { firstPaint(path) }); got > 100*time.Millisecond {
		t.Errorf("first paint median %v, budget 100ms", got)
	} else {
		t.Logf("first paint median %v", got)
	}
	m := newModel(&fakeClient{}, cacheFixture(50))
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

func TestTrackerSpeedBudget(t *testing.T) {
	e := newTrkEnv(t)
	var cached []linear.Issue
	for i := 1; i <= 200; i++ {
		cached = append(cached, lissue(fmt.Sprintf("ENG-%d", i), "Some Issue title", "Todo"))
	}
	e.cache["w1/project:p1"] = cached
	e.api.gate = make(chan struct{})
	defer close(e.api.gate)
	e.open()
	e.press("j", "enter", "enter")
	m, cmd := press(e.m, "enter")
	read := cmd().(tea.BatchMsg)[0]
	if got := median(21, func() {
		t2, _ := m.Update(read())
		_ = t2.(Model).View()
	}); got > 100*time.Millisecond {
		t.Errorf("tracker first paint from cache median %v, budget 100ms", got)
	} else {
		t.Logf("tracker first paint median %v", got)
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
