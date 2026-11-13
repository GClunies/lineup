package board

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/localissues"
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
