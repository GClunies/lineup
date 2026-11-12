package board

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
)

const local7 = "---\ntitle: Split billing ingest\nstatus: todo\n---\n\nThe ingest job does two things.\n"

// promoteEnv is captureEnv with local-7 on the work view and selected.
func promoteEnv(t *testing.T) (*trkEnv, string) {
	t.Helper()
	e, store := captureEnv(t)
	path := filepath.Join(store.Dir, "local-7.md")
	if err := os.WriteFile(path, []byte(local7), 0o644); err != nil {
		t.Fatal(err)
	}
	e.m = drain(e.m, e.m.loadMine())
	e.m.sel = "local-7"
	return e, path
}

// listRows is the Destination list as text, one row per line.
func listRows(m Model) []string {
	var rows []string
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for _, l := range lines[3 : len(lines)-1] {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			rows = append(rows, strings.TrimPrefix(l, "▸ "))
		}
	}
	return rows
}

func TestPPromotesALocalIssueToTheLinearDestination(t *testing.T) {
	e, path := promoteEnv(t)
	e.press("P")
	if f := footerAt(e.m, 200); !strings.Contains(f, "promote local-7 to Linear") {
		t.Fatalf("footer %q, want the Promote named", f)
	}
	delete(e.wrote, "w1/my")
	e.press("bill", "enter")
	want := billing
	want.Title, want.Description = "Split billing ingest", "The ingest job does two things.\n"
	if !slices.Equal(e.api.created, []linear.NewIssue{want}) {
		t.Fatalf("created %+v, want %+v", e.api.created, want)
	}
	b, _ := os.ReadFile(path)
	is, err := localissues.Parse(b)
	if err != nil || is.Status != localissues.Canceled || !strings.HasSuffix(string(b), "\n\nPromoted to ENG-900.\n") {
		t.Fatalf("local-7 is\n%s(%v), want canceled with the Promoted line", b, err)
	}
	if f := footerAt(e.m, 200); !strings.Contains(f, "promoted local-7 to ENG-900") {
		t.Fatalf("footer %q, want both Issues named", f)
	}
	if _, ok := e.wrote["w1/my"]; !ok {
		t.Fatal("the Promote did not fetch the Issue rows again")
	}
	if slices.Contains(rowIDs(e.m.issues), "local-7") {
		t.Fatalf("Issue rows %v, want local-7 gone", rowIDs(e.m.issues))
	}
	if got := savedRecent(t, e.m.opts.Destinations); !slices.Equal(got, []string{"acme / ENG / Billing"}) {
		t.Fatalf("destinations.json %q, want the Destination saved", got)
	}
}

func TestTheListUnderPHasNoLocalRowAndEscChangesNothing(t *testing.T) {
	e, path := promoteEnv(t)
	e.press("P")
	rows := listRows(e.m)
	if want := []string{"acme / ENG", "acme / ENG / Billing"}; len(rows) < len(want) || !slices.Equal(rows[:len(want)], want) {
		t.Fatalf("list rows %q, want the Linear Destinations first", rows)
	}
	if slices.ContainsFunc(rows, func(r string) bool { return strings.HasPrefix(r, "local") }) {
		t.Fatalf("list rows %q, want no local row", rows)
	}
	e.press("esc")
	if b, _ := os.ReadFile(path); string(b) != local7 || len(e.api.created) != 0 || e.m.mode != modeNormal {
		t.Fatalf("after esc: mode %v, created %+v, file\n%s", e.m.mode, e.api.created, b)
	}
	if f := footerAt(e.m, 200); !strings.Contains(f, "P promote to Linear") {
		t.Fatalf("footer %q, want the key hints back", f)
	}
}

func TestAFailedCreateLeavesTheLocalFileAsItIs(t *testing.T) {
	e, path := promoteEnv(t)
	e.api.createErr = errors.New("rate limited")
	e.press("P", "bill", "enter")
	if b, _ := os.ReadFile(path); string(b) != local7 {
		t.Fatalf("local-7 changed after a failed create:\n%s", b)
	}
	if f := footerAt(e.m, 200); !strings.Contains(f, "promote local-7 to acme / ENG / Billing: rate limited") {
		t.Fatalf("footer %q, want the error", f)
	}
	if _, err := os.Stat(e.m.opts.Destinations); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed create saved the Destination: %v", err)
	}
}

func TestAFailedWriteAfterTheCreateNamesBothIssues(t *testing.T) {
	e, path := promoteEnv(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	e.press("P", "bill", "enter")
	if len(e.api.created) != 1 {
		t.Fatalf("created %+v, want one Issue", e.api.created)
	}
	if f := footerAt(e.m, 300); !strings.Contains(f, "created ENG-900, but local-7 did not change: ") {
		t.Fatalf("footer %q, want both Issues named", f)
	}
	if b, _ := os.ReadFile(path); string(b) != local7 {
		t.Fatalf("local-7 is\n%s, want it unchanged", b)
	}
}

func TestPOnALinearOrTaskRowDoesNothing(t *testing.T) {
	e, _ := promoteEnv(t)
	e.m = feed(e.m, boardMsg(fixture()))
	for _, sel := range []string{"ENG-9", "ENG-590"} {
		e.m.sel = sel
		m, cmd := press(e.m, "P")
		if cmd != nil || m.mode != modeNormal || len(e.api.created) != 0 || len(e.sched.ops()) != 0 {
			t.Fatalf("P on %s: cmd %v, mode %v, created %+v, ops %v, want nothing", sel, cmd != nil, m.mode, e.api.created, e.sched.ops())
		}
	}
}
