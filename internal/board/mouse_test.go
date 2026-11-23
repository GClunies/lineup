package board

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// lineWith is the screen line that shows text, as the user sees it.
func lineWith(t *testing.T, m Model, text string) int {
	t.Helper()
	for i, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		if strings.Contains(l, text) {
			return i
		}
	}
	t.Fatalf("no line shows %q:\n%s", text, ansi.Strip(m.View()))
	return -1
}

func tapAt(m Model, x, y int) (Model, tea.Cmd) {
	t, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	return t.(Model), cmd
}

// tap taps the line that shows text, near its left end.
func tap(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	return tapAt(m, 4, lineWith(t, m, text))
}

func wheel(m Model, b tea.MouseButton) Model {
	t, _ := m.Update(tea.MouseMsg{X: 4, Y: 5, Action: tea.MouseActionPress, Button: b})
	return t.(Model)
}

func shows(m Model, text string) bool { return strings.Contains(ansi.Strip(m.View()), text) }

func TestATapOnATaskRowOpensItsActionsAndATapRunsOne(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m, _ = tap(t, m, "ENG-592")
	if m.sel != "ENG-592" {
		t.Fatalf("selection = %s, want the tapped row", m.sel)
	}
	for _, s := range []string{"go to the Worker", "preview", "details", "close", "back"} {
		if !shows(m, s) {
			t.Errorf("the menu has no %q:\n%s", s, ansi.Strip(m.View()))
		}
	}
	if shows(m, "merge") || shows(m, "Rate limit login") {
		t.Errorf("the menu shows an action or a row that is not ENG-592's:\n%s", ansi.Strip(m.View()))
	}
	m, cmd := tap(t, m, "go to the Worker")
	m = run(m, cmd)
	if got := f.ops(); !slices.Equal(got, []string{"open"}) || f.calls[0].Task != "ENG-592" {
		t.Fatalf("ops = %v %+v, want open ENG-592", got, f.calls)
	}
	if !shows(m, "Rate limit login") {
		t.Fatalf("the rows did not come back after the pick:\n%s", ansi.Strip(m.View()))
	}
}

func TestBackClosesTheMenuAndRunsNothing(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m, _ = tap(t, m, "ENG-592")
	m, cmd := tap(t, m, "back")
	m = run(m, cmd)
	if len(f.calls) != 0 || !shows(m, "Rate limit login") {
		t.Fatalf("back ran %v or left the menu open:\n%s", f.ops(), ansi.Strip(m.View()))
	}
	m, _ = tap(t, m, "ENG-592")
	m, _ = press(m, "esc")
	if !shows(m, "Rate limit login") {
		t.Fatal("esc did not close the menu")
	}
}

func TestATapOnAHeadingOpensOrClosesTheGroup(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	if shows(m, "ENG-560") {
		t.Fatal("Done starts closed")
	}
	m, _ = tap(t, m, "Done 1")
	if !shows(m, "ENG-560") {
		t.Fatalf("a tap on Done did not open it:\n%s", ansi.Strip(m.View()))
	}
	m, _ = tap(t, m, "Done 1")
	if shows(m, "ENG-560") {
		t.Fatal("a second tap on Done did not close it")
	}
}

func TestATapFindsTheRowUnderTheFilterLine(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	m, _ = press(m, "/", "F", "i", "x", "enter")
	if !shows(m, "Fix flaky test") || shows(m, "Rate limit login") {
		t.Fatalf("the filter did not apply:\n%s", ansi.Strip(m.View()))
	}
	m, _ = tap(t, m, "Fix flaky test")
	if m.sel != "ENG-592" || !shows(m, "go to the Worker") {
		t.Fatalf("selection = %s, want ENG-592 with its menu", m.sel)
	}
}

func TestATapFindsTheRowOnAScrolledList(t *testing.T) {
	v := fixture()
	for i := range 40 {
		v.Tasks = append(v.Tasks, tv(fmt.Sprintf("ENG-%d", 800+i), lifecycle.Working, "data", fmt.Sprintf("Job %d", i), "", time.Minute))
	}
	m := newModel(&fakeClient{}, v)
	m.height = 15
	for m.sel != "ENG-830" {
		m, _ = press(m, "j")
	}
	if shows(m, "ENG-588") {
		t.Fatal("the list did not scroll")
	}
	m, _ = tap(t, m, "ENG-827")
	if m.sel != "ENG-827" {
		t.Fatalf("selection = %s, want ENG-827", m.sel)
	}
}

func TestATapBelowTheRowsDoesNothing(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	m, _ = tapAt(m, 4, m.height-3)
	if m.sel != "ENG-588" || !shows(m, "Rate limit login") {
		t.Fatalf("a tap on an empty line changed the Board: sel %s", m.sel)
	}
}

func TestATapOnThePreviewDoesNothing(t *testing.T) {
	f := &fakeClient{capture: "worker screen"}
	m := newModel(f, fixture())
	m, cmd := press(m, "p")
	m = run(m, cmd)
	m, _ = tapAt(m, m.width-5, lineWith(t, m, "ENG-592"))
	if m.sel != "ENG-588" || !shows(m, "ENG-571") {
		t.Fatalf("a tap on the preview changed the Board: sel %s", m.sel)
	}
}

func TestTheWheelMovesTheSelection(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	m = wheel(m, tea.MouseButtonWheelDown)
	if m.sel != "ENG-590" {
		t.Fatalf("wheel down: selection = %s, want ENG-590", m.sel)
	}
	m = wheel(m, tea.MouseButtonWheelUp)
	if m.sel != "ENG-588" {
		t.Fatalf("wheel up: selection = %s, want ENG-588", m.sel)
	}
}

func TestATappedCloseAsksYesOrNoByTap(t *testing.T) {
	f := &fakeClient{}
	m := newModel(f, fixture())
	m, _ = tap(t, m, "ENG-592")
	m, _ = tap(t, m, "close")
	if !shows(m, "yes, close ENG-592") || !shows(m, "no, keep it") {
		t.Fatalf("no yes and no to tap:\n%s", ansi.Strip(m.View()))
	}
	m, cmd := tap(t, m, "no, keep it")
	m = run(m, cmd)
	if len(f.calls) != 0 {
		t.Fatalf("no ran %v", f.ops())
	}
	m, _ = tap(t, m, "ENG-592")
	m, _ = tap(t, m, "close")
	m, cmd = tap(t, m, "yes, close ENG-592")
	run(m, cmd)
	if got := f.ops(); !slices.Equal(got, []string{"close"}) || f.calls[0].Task != "ENG-592" {
		t.Fatalf("ops = %v, want close ENG-592", got)
	}
}

func TestATappedStartAtAFullCapAsksByTap(t *testing.T) {
	f := &fakeClient{errs: map[string]error{"start": errors.New(capFull)}}
	m := newModel(f, fixture())
	m, _ = tap(t, m, "ENG-601")
	m, cmd := tap(t, m, "start")
	m = run(m, cmd)
	if !shows(m, "start once over the cap") {
		t.Fatalf("no cap choice to tap:\n%s", ansi.Strip(m.View()))
	}
	m, cmd = tap(t, m, "start once over the cap")
	run(m, cmd)
	last := f.calls[len(f.calls)-1]
	if last != (scheduler.Request{Op: "start", Task: "ENG-601", OverCap: true}) {
		t.Fatalf("last call = %+v, want a start over the cap", last)
	}
}

func TestATapOnTheDetailPaneGoesBack(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	m, _ = tap(t, m, "ENG-592")
	m, cmd := tap(t, m, "details")
	m = run(m, cmd)
	if !m.detail {
		t.Fatal("details did not open the detail pane")
	}
	m, _ = tapAt(m, 4, 5)
	if m.detail {
		t.Fatal("a tap on the detail pane did not go back")
	}
}
