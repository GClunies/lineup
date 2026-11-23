package board

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// withClosed sets the closed groups, so a test names the state it reads.
func withClosed(m Model, groups ...string) Model {
	m.lay.closed = map[string]bool{}
	for _, g := range groups {
		m.lay.closed[g] = true
	}
	m.setBoard(m.board)
	return m
}

// openAll opens every group, for a test that reads the rows of Backlog or Done.
func openAll(m Model) Model { return withClosed(m) }

// groupEnv is mineEnv with the fixture Board. The work view holds ENG-20 in
// Backlog and ENG-9 and local-1 in Ready.
func groupEnv(t *testing.T) *workEnv {
	t.Helper()
	e, _ := mineEnv(t)
	e.m = feed(e.m, boardMsg(fixture()))
	e.m.sel = "ENG-588"
	return e
}

// layoutEnv is groupEnv under the [board] table b, 81 columns wide.
func layoutEnv(t *testing.T, b config.Board) *workEnv {
	t.Helper()
	e := groupEnv(t)
	e.m.opts.Board = b
	e.m.lay = newSections(b)
	e.m = feed(e.m, tea.WindowSizeMsg{Width: 81, Height: 40}, boardMsg(fixture()))
	return e
}

// userBoard is the layout that the user asked for.
var userBoard = config.Board{
	Order:     []string{"Done", "Needs you", "Follow-up", "Working", "Waiting", "Queued", "Ready", "Backlog"},
	Collapsed: []string{"Backlog"},
	Hidden:    []string{"Done"},
}

func screen(m Model) string { return ansi.Strip(m.View()) }

func TestBacklogAndDoneStartClosedWithTheirCounts(t *testing.T) {
	e := groupEnv(t)
	out := screen(e.m)
	for _, want := range []string{"NEEDS YOU  2", "BACKLOG  1 …", "READY  2", "ENG-9", "local-1", "QUEUED  1", "WORKING  2", "WAITING  1", "DONE  1 …"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"ENG-20", "ENG-560"} {
		if strings.Contains(out, hidden) {
			t.Errorf("screen shows %s in a closed group:\n%s", hidden, out)
		}
	}
	if strings.Contains(out, "READY  2 …") {
		t.Errorf("an open heading shows the closed mark:\n%s", out)
	}
}

func TestEnterOnAHeadingTogglesTheGroupForThisRunOnly(t *testing.T) {
	e := groupEnv(t)
	e.m.sel = headingKey("Backlog")
	if f := footerAt(e.m, 100); !strings.Contains(f, "\n enter open   j/k move   n new Issue") {
		t.Fatalf("footer %q, want enter open, j/k move, then n new Issue", f)
	}
	e.press("enter")
	if out := screen(e.m); !strings.Contains(out, "BACKLOG  1\n") || !strings.Contains(out, "ENG-20") {
		t.Fatalf("Backlog did not open:\n%s", out)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "\n enter close   j/k move") {
		t.Fatalf("footer %q, want enter close first", f)
	}

	restart := New(fixture(), e.m.opts)
	restart = drain(restart, restart.loadMine())
	if out := screen(restart); strings.Contains(out, "ENG-20") {
		t.Fatalf("a new Board kept Backlog open:\n%s", out)
	}

	e.press("enter")
	if out := screen(e.m); !strings.Contains(out, "BACKLOG  1 …") || strings.Contains(out, "ENG-20") {
		t.Fatalf("Backlog did not close:\n%s", out)
	}
}

func TestJAndKStopOnEachHeadingExceptNeedsYouAndSkipClosedRows(t *testing.T) {
	e := groupEnv(t)
	var seen []string
	for range 16 {
		seen = append(seen, e.m.sel)
		e.press("j")
	}
	want := []string{"ENG-588", "ENG-590", "group:Backlog", "group:Ready", "ENG-9", "local-1", "group:Queued", "ENG-601",
		"group:Working", "WEB-12", "ENG-592", "group:Waiting", "ENG-571", "group:Done", "group:Done", "group:Done"}
	if !slices.Equal(seen, want) {
		t.Fatalf("j moved through %v, want %v", seen, want)
	}
	if !strings.Contains(screen(e.m), "▸DONE  1 …") {
		t.Fatalf("the selected heading has no marker:\n%s", screen(e.m))
	}
	e.press("k")
	if e.m.sel != "ENG-571" {
		t.Fatalf("k went to %s, want ENG-571", e.m.sel)
	}
}

func TestATaskThatMovesIntoAClosedGroupSelectsItsHeading(t *testing.T) {
	e := groupEnv(t)
	e.m.sel = "ENG-571"
	v := fixture()
	for i, x := range v.Tasks {
		if x.ID == "ENG-571" {
			v.Tasks[i] = tv("ENG-571", lifecycle.Done, "data", "Rate limit login", "#4636", time.Minute)
		}
	}
	e.m = feed(e.m, boardMsg(v))
	if e.m.sel != headingKey("Done") {
		t.Fatalf("selection %q, want the Done heading", e.m.sel)
	}
	if !strings.Contains(screen(e.m), "▸DONE  2 …") {
		t.Fatalf("the Done heading is not marked:\n%s", screen(e.m))
	}
}

func TestALostSelectionFallsBackToTheFirstRowNotAHeading(t *testing.T) {
	e := groupEnv(t)
	e.m.sel = "ENG-601"
	v := fixture()
	v.Tasks = slices.DeleteFunc(v.Tasks, func(x scheduler.TaskView) bool { return x.ID == "ENG-601" })
	e.m = feed(e.m, boardMsg(v))
	if e.m.sel != "ENG-588" {
		t.Fatalf("selection %q, want ENG-588", e.m.sel)
	}
	e.m = feed(e.m, boardMsg(scheduler.BoardView{Cap: 5}))
	if e.m.sel != "ENG-9" {
		t.Fatalf("with no Task, selection %q, want the first Issue row ENG-9", e.m.sel)
	}
}

func TestTheInitialSelectionIsTheFirstRowNotAHeading(t *testing.T) {
	e, _ := mineEnv(t)
	if e.m.sel != "ENG-9" {
		t.Fatalf("selection %q, want ENG-9 and not the Backlog heading", e.m.sel)
	}
	done := tv("ENG-560", lifecycle.Done, "data", "Rename config options", "#4601", time.Hour)
	m := New(scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{done}}, Options{Client: &fakeClient{}})
	if m.sel != headingKey("Done") {
		t.Fatalf("selection %q, want the only heading when every group is closed", m.sel)
	}
}

// For random rows, orders, closed sets, and hidden sets, the selection stops
// are the headings of the non-empty shown groups except Needs you, then the
// rows of the open ones, in the drawn order.
func TestRowIDsHoldEachHeadingAndTheRowsOfOpenGroupsOnce(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var tasks []scheduler.TaskView
		inGroup := map[string][]string{}
		for _, g := range []string{groupNeedsYou, "Queued", "Working", "Waiting", groupDone} {
			for i := range rapid.IntRange(0, 3).Draw(t, "tasks in "+g) {
				id := fmt.Sprintf("%s-%d", strings.ToUpper(g[:2]), i)
				tk := tv(id, lifecycle.Working, "data", "t", "", 0)
				tk.Group = g
				tasks = append(tasks, tk)
				inGroup[g] = append(inGroup[g], id)
			}
		}
		var lin []linear.Issue
		for i := range rapid.IntRange(0, 3).Draw(t, "tasks in "+groupFollowUp) {
			id := fmt.Sprintf("FO-%d", i)
			tasks = append(tasks, tv(id, lifecycle.Done, "data", "t", "", 0))
			lin = append(lin, mineIssue(id, "started", 0))
			inGroup[groupFollowUp] = append(inGroup[groupFollowUp], id)
		}
		var loc []localissues.Issue
		for i := range rapid.IntRange(0, 4).Draw(t, "local Issues") {
			st := rapid.SampledFrom([]localissues.Status{localissues.Todo, localissues.Backlog}).Draw(t, "status")
			loc = append(loc, localIssue(i+1, st))
			g := groupBacklog
			if st == localissues.Todo {
				g = groupReady
			}
			inGroup[g] = append(inGroup[g], fmt.Sprintf("local-%d", i+1))
		}
		all := config.DefaultBoard().Order
		order := rapid.SliceOfNDistinct(rapid.SampledFrom(all), len(all), len(all), func(g string) string { return g }).Draw(t, "order")
		movable := rapid.SampledFrom(slices.DeleteFunc(slices.Clone(all), func(g string) bool { return g == groupNeedsYou }))
		same := func(g string) string { return g }
		closed := rapid.SliceOfDistinct(movable, same).Draw(t, "closed")
		hidden := rapid.SliceOfDistinct(movable, same).Draw(t, "hidden")

		m := newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Tasks: tasks})
		m.mine.loc = loc
		m.mine.lin[acme.ID] = lin
		m.lay = newSections(config.Board{Order: order, Collapsed: closed, Hidden: hidden})
		m.setBoard(m.board)
		ids := m.rowIDs()

		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("%q is listed twice in %v", id, ids)
			}
			seen[id] = true
		}
		var wantHeads []string
		for _, g := range order {
			rows := inGroup[g]
			shown := !slices.Contains(hidden, g)
			if len(rows) == 0 {
				if seen[headingKey(g)] {
					t.Fatalf("the empty group %s has a heading in %v", g, ids)
				}
				continue
			}
			if g != groupNeedsYou && shown {
				wantHeads = append(wantHeads, headingKey(g))
			}
			if seen[headingKey(g)] != (g != groupNeedsYou && shown) {
				t.Fatalf("heading of %s: listed %v in %v", g, seen[headingKey(g)], ids)
			}
			open := shown && !slices.Contains(closed, g)
			for _, r := range rows {
				if seen[r] != open {
					t.Fatalf("row %s of %s (open %v): listed %v in %v", r, g, open, seen[r], ids)
				}
			}
		}
		var heads []string
		for _, id := range ids {
			if _, ok := headingName(id); ok {
				heads = append(heads, id)
			}
		}
		if !slices.Equal(heads, wantHeads) {
			t.Fatalf("headings %v, want %v in group order", heads, wantHeads)
		}
	})
}

func TestTheHeaderDropsTheGroupCounts(t *testing.T) {
	e := groupEnv(t)
	h := ansi.Strip(e.m.header())
	for _, gone := range []string{"working", "waiting", "queued", "ready", "backlog", "◐", "◷"} {
		if strings.Contains(h, gone) {
			t.Errorf("header %q still shows %q", h, gone)
		}
	}
	for _, want := range []string{"WIP 4/5", "●2 need you"} {
		if !strings.Contains(h, want) {
			t.Errorf("header %q lacks %q", h, want)
		}
	}
}

func TestTheHeaderFitsEightyColumnsWithTheWipOverflowAndTheReviewCount(t *testing.T) {
	v := fixture()
	v.OverCap = 1
	flagged := rtv("R-3", lifecycle.Reviewing, "data#7 Fix the tooltip", urlBase, time.Hour)
	flagged.Flag, flagged.Group = "question", "Needs you"
	v.Tasks = append(v.Tasks, flagged)
	m := newModel(&fakeClient{}, v)
	m.width = 80
	h := ansi.Strip(m.header())
	if !strings.Contains(h, "WIP 4/5 +1 over   ●2 need you (+1 in reviews)") || strings.Contains(h, "…") {
		t.Fatalf("header %q is cut at 80 columns", h)
	}
}

func TestTheDefaultOrderIsNeedsYouThenFollowUpThenTheDevelopmentCycle(t *testing.T) {
	want := []string{groupNeedsYou, "Follow-up", groupBacklog, groupReady, "Queued", "Working", "Waiting", groupDone}
	if got := newSections(config.Board{}).order; !slices.Equal(got, want) {
		t.Fatalf("default order %v, want %v", got, want)
	}
}

func TestTheConfiguredOrderSetsTheSectionsOnScreen(t *testing.T) {
	b := userBoard
	b.Hidden = nil
	e := layoutEnv(t, b)
	out := screen(e.m)
	inOrder(t, out, "DONE  1\n", "ENG-560", "NEEDS YOU  2", "ENG-588", "WORKING  2", "WAITING  1", "QUEUED  1", "READY  2", "BACKLOG  1 …")
	var heads []string
	for _, id := range e.m.rowIDs() {
		if _, ok := headingName(id); ok {
			heads = append(heads, id)
		}
	}
	want := []string{"group:Done", "group:Working", "group:Waiting", "group:Queued", "group:Ready", "group:Backlog"}
	if !slices.Equal(heads, want) {
		t.Fatalf("heading stops %v, want %v", heads, want)
	}
}

func TestTheConfiguredCollapsedSetStartsClosed(t *testing.T) {
	b := config.DefaultBoard()
	b.Collapsed = []string{"Ready", "Waiting"}
	e := layoutEnv(t, b)
	out := screen(e.m)
	for _, want := range []string{"READY  2 …", "WAITING  1 …", "BACKLOG  1\n", "ENG-20", "DONE  1\n", "ENG-560"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"ENG-9", "ENG-571"} {
		if strings.Contains(out, gone) {
			t.Errorf("screen shows %s of a collapsed section:\n%s", gone, out)
		}
	}
	e.m.sel = headingKey("Ready")
	e.press("enter")
	if !strings.Contains(screen(e.m), "ENG-9") {
		t.Fatalf("enter did not open Ready:\n%s", screen(e.m))
	}
}

func TestAHiddenSectionShowsNoHeadingNoRowsAndNoStopAndKeepsTheHeaderCounts(t *testing.T) {
	shown := layoutEnv(t, config.DefaultBoard())
	e := layoutEnv(t, userBoard)
	out := screen(e.m)
	for _, gone := range []string{"DONE", "ENG-560"} {
		if strings.Contains(out, gone) {
			t.Errorf("screen shows %q of a hidden section:\n%s", gone, out)
		}
	}
	for range 20 {
		if e.m.sel == headingKey("Done") || e.m.sel == "ENG-560" {
			t.Fatalf("the selection landed in the hidden section: %s", e.m.sel)
		}
		e.press("j")
	}
	if got, want := ansi.Strip(e.m.header()), ansi.Strip(shown.m.header()); got != want {
		t.Fatalf("header %q, want %q as before the section hid", got, want)
	}
}

func TestDShowsAndHidesDoneAndOpensIt(t *testing.T) {
	e := layoutEnv(t, userBoard)
	if out := screen(e.m); strings.Contains(out, "DONE") {
		t.Fatalf("Done shows before d:\n%s", out)
	}
	e.press("d")
	out := screen(e.m)
	inOrder(t, out, "DONE  1\n", "ENG-560", "NEEDS YOU  2")
	e.press("d")
	if out := screen(e.m); strings.Contains(out, "DONE") || strings.Contains(out, "ENG-560") {
		t.Fatalf("Done shows after the second d:\n%s", out)
	}
}

func TestDHidesDoneEvenWhenTheFileHidesNothing(t *testing.T) {
	e := layoutEnv(t, config.DefaultBoard())
	if !strings.Contains(screen(e.m), "DONE  1 …") {
		t.Fatalf("Done does not show by default:\n%s", screen(e.m))
	}
	e.press("d")
	if strings.Contains(screen(e.m), "DONE") {
		t.Fatalf("d did not hide Done:\n%s", screen(e.m))
	}
	e.press("d")
	if !strings.Contains(screen(e.m), "DONE  1\n") || !strings.Contains(screen(e.m), "ENG-560") {
		t.Fatalf("d did not show Done open:\n%s", screen(e.m))
	}
}

func TestHidingDoneUnderItsSelectedRowFallsBackToAShownRow(t *testing.T) {
	e := layoutEnv(t, config.Board{Order: config.DefaultBoard().Order})
	e.m.sel = "ENG-560"
	e.press("d")
	if e.m.sel != "ENG-588" {
		t.Fatalf("selection %q, want the first row ENG-588", e.m.sel)
	}
}

func TestTheFilterFindsARowInAHiddenSection(t *testing.T) {
	e := layoutEnv(t, userBoard)
	e.press("/", "rename", "enter")
	if out := screen(e.m); !strings.Contains(out, "ENG-560") {
		t.Fatalf("the filter did not find ENG-560 in the hidden Done:\n%s", out)
	}
	e.press("esc")
	if out := screen(e.m); strings.Contains(out, "ENG-560") || strings.Contains(out, "DONE") {
		t.Fatalf("Done shows after the filter cleared:\n%s", out)
	}
}

func TestTheHelpListsD(t *testing.T) {
	e := groupEnv(t)
	e.press("?")
	if out := screen(e.m); !strings.Contains(out, "d        show or hide Done") {
		t.Fatalf("help lacks d:\n%s", out)
	}
}
