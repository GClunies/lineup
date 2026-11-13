package board

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// withLocal2 adds local-2 in Ready, with a Markdown body, to a mineEnv.
func withLocal2(t *testing.T, e *workEnv, store localissues.Store) {
	t.Helper()
	is, err := store.Create("Split the log writer", "", "The writer holds **two** jobs.\n\n- rotate\n- flush")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(is.ID(), localissues.Todo); err != nil {
		t.Fatal(err)
	}
	e.m = drain(e.m, e.m.loadMine())
}

func TestTheFooterOfAnIssueRowNamesFDetails(t *testing.T) {
	e, _ := mineEnv(t)
	for _, id := range []string{"ENG-9", "local-1"} {
		e.m.sel = id
		if f := footerAt(e.m, 200); !strings.Contains(f, "f details") {
			t.Fatalf("%s: footer %q, want f details", id, f)
		}
	}
}

func TestFOnALinearIssueRowShowsItsDescriptionAndFGoesBack(t *testing.T) {
	e, _ := mineEnv(t)
	e.m.sel = "ENG-9"
	e.press("f")
	v := ansi.Strip(e.m.View())
	for _, want := range []string{"ENG-9  Title of ENG-9", "Goal", "Build the billing model."} {
		if !strings.Contains(v, want) {
			t.Fatalf("the detail pane lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "**") || strings.Contains(v, "# Goal") {
		t.Fatalf("the description is not rendered from Markdown:\n%s", v)
	}
	if !slices.Equal(e.api.described, []string{"ENG-9"}) {
		t.Fatalf("Issue reads %v, want ENG-9", e.api.described)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "f back") {
		t.Fatalf("footer %q, want f back", f)
	}
	e.press("f")
	v = ansi.Strip(e.m.View())
	if strings.Contains(v, "Goal") || !strings.Contains(v, "▸ ENG-9") {
		t.Fatalf("f did not go back to the rows:\n%s", v)
	}
}

func TestFOnALocalIssueRowShowsTheFileBody(t *testing.T) {
	e, store := mineEnv(t)
	withLocal2(t, e, store)
	e.m.sel = "local-2"
	e.press("f")
	v := ansi.Strip(e.m.View())
	for _, want := range []string{"local-2  Split the log writer", "todo", "The writer holds two jobs.", "rotate", "flush"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the detail pane lacks %q:\n%s", want, v)
		}
	}
	if len(e.api.described) != 0 {
		t.Fatalf("a local row read Linear: %v", e.api.described)
	}
	e.press("esc")
	if v := ansi.Strip(e.m.View()); strings.Contains(v, "rotate") || !strings.Contains(v, "▸ local-2") {
		t.Fatalf("esc did not go back to the rows:\n%s", v)
	}
}

func TestAFailedDescriptionReadShowsTheErrorInThePane(t *testing.T) {
	e, _ := mineEnv(t)
	e.api.issueErr = errors.New("Linear is down")
	e.m.sel = "ENG-9"
	e.press("f")
	if v := ansi.Strip(e.m.View()); !strings.Contains(v, "ENG-9  Title of ENG-9") || !strings.Contains(v, "Linear is down") {
		t.Fatalf("the pane does not name the error:\n%s", v)
	}
}

// rowOf is the Issue row id on the work view, if any.
func rowOf(m Model, id string) (issueRow, bool) {
	for _, r := range m.issues {
		if r.ID == id {
			return r, true
		}
	}
	return issueRow{}, false
}

func TestTheFooterOfAnIssueRowNamesCStatus(t *testing.T) {
	e, _ := mineEnv(t)
	for _, id := range []string{"ENG-9", "local-1"} {
		e.m.sel = id
		if f := footerAt(e.m, 200); !strings.Contains(f, "c status") {
			t.Fatalf("%s: footer %q, want c status", id, f)
		}
	}
}

func TestCOnALocalRowWritesTheNewStatusAndTheRowMovesGroup(t *testing.T) {
	e, store := mineEnv(t)
	e.m.sel = "local-1"
	e.press("c")
	v := ansi.Strip(e.m.View())
	if e.m.mode != modeStatus {
		t.Fatalf("mode %d, want the status list:\n%s", e.m.mode, v)
	}
	inOrder(t, v, "Status of local-1", "backlog", "▸ todo", "started", "in review", "done", "canceled")
	e.press("k", "enter")
	if is, _ := store.Get("local-1"); is.Status != localissues.Backlog {
		t.Fatalf("local-1 has status %q, want backlog", is.Status)
	}
	if r, ok := rowOf(e.m, "local-1"); !ok || r.Group != groupBacklog {
		t.Fatalf("local-1 row %+v %v, want it in Backlog", r, ok)
	}
	if !strings.Contains(e.m.status, "status local-1 ok · backlog") {
		t.Fatalf("status %q", e.m.status)
	}
	e.m.sel = "local-1"
	e.press("c", "j", "j", "j", "enter")
	if _, ok := rowOf(e.m, "local-1"); ok {
		t.Fatal("local-1 in done is still on the board")
	}
}

// For any status and any target, the file gets the target, and the row sits
// in the group of the target or leaves the board.
func TestCOnALocalRowPutsTheRowInTheGroupOfAnyStatus(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		e, store := mineEnv(t)
		from := rapid.SampledFrom([]localissues.Status{localissues.Backlog, localissues.Todo}).Draw(rt, "from")
		to := rapid.SampledFrom(localissues.Statuses).Draw(rt, "to")
		store.SetStatus("local-1", from)
		e.m = drain(e.m, e.m.loadMine())
		e.m.sel = "local-1"
		e.press("c")
		step := "j"
		d := slices.Index(localissues.Statuses, to) - slices.Index(localissues.Statuses, from)
		if d < 0 {
			step, d = "k", -d
		}
		for range d {
			e.press(step)
		}
		e.press("enter")
		if is, _ := store.Get("local-1"); is.Status != to {
			rt.Fatalf("from %s to %s: the file has %q", from, to, is.Status)
		}
		r, ok := rowOf(e.m, "local-1")
		if want := issueGroup[scheduler.KindLocal][string(to)]; (want != "") != ok || r.Group != want {
			rt.Fatalf("from %s to %s: row %+v on the board %v, want group %q", from, to, r, ok, want)
		}
	})
}

func TestCOnALinearRowSetsTheChosenTeamStateAndReadsTheRowsAgain(t *testing.T) {
	e, _ := mineEnv(t)
	e.m.sel = "ENG-9"
	e.press("c")
	inOrder(t, ansi.Strip(e.m.View()), "Status of ENG-9", "Todo", "In Progress")
	delete(e.wrote, "w1/my")
	e.press("j", "enter")
	if !slices.Equal(e.api.statuses, []string{"ENG-9=In Progress"}) {
		t.Fatalf("status writes %v, want ENG-9=In Progress", e.api.statuses)
	}
	if _, ok := e.wrote["w1/my"]; !ok {
		t.Fatal("the status change did not read the Issue rows again")
	}
	if e.m.mode != modeNormal {
		t.Fatalf("mode %d after enter, want the keys back", e.m.mode)
	}
}

func TestAFailedStatusUpdateLeavesTheRowAndTheFooterNamesTheError(t *testing.T) {
	e, _ := mineEnv(t)
	e.api.setStatusErr = errors.New("rate limited")
	e.m.sel = "ENG-9"
	before, _ := rowOf(e.m, "ENG-9")
	delete(e.wrote, "w1/my")
	e.press("c", "j", "enter")
	if f := footerAt(e.m, 200); !strings.Contains(f, "status ENG-9: rate limited") {
		t.Fatalf("footer %q, want the error", f)
	}
	if r, ok := rowOf(e.m, "ENG-9"); !ok || r.Group != before.Group {
		t.Fatalf("ENG-9 row %+v %v, want it in %s", r, ok, before.Group)
	}
	if _, ok := e.wrote["w1/my"]; ok {
		t.Fatal("a failed status change read the Issue rows again")
	}
}

func TestAFailedStatusListReadNamesTheErrorAndOpensNoList(t *testing.T) {
	e, _ := mineEnv(t)
	e.api.statesErr = errors.New("token expired")
	e.m.sel = "ENG-9"
	e.press("c")
	if e.m.mode != modeNormal || !strings.Contains(footerAt(e.m, 200), "status ENG-9: token expired") {
		t.Fatalf("mode %d footer %q, want no list and the error", e.m.mode, footerAt(e.m, 200))
	}
}

func TestEscInTheStatusListWritesNothing(t *testing.T) {
	e, store := mineEnv(t)
	e.m.sel = "local-1"
	e.press("c", "k", "esc")
	if is, _ := store.Get("local-1"); is.Status != localissues.Todo || e.m.mode != modeNormal {
		t.Fatalf("status %q mode %d, want todo and the keys back", is.Status, e.m.mode)
	}
	e.m.sel = "ENG-9"
	e.press("c", "j", "esc")
	if len(e.api.statuses) != 0 {
		t.Fatalf("esc wrote %v", e.api.statuses)
	}
}
