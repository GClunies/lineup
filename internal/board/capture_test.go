package board

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
)

// captureEnv is mineEnv with destinations.json in a temporary folder.
func captureEnv(t *testing.T) (*trkEnv, localissues.Store) {
	t.Helper()
	e, store := mineEnv(t)
	e.m.opts.Destinations = filepath.Join(t.TempDir(), "destinations.json")
	return e, store
}

// footLines is the last two lines of the screen, where the capture box shows.
func footLines(m Model) string {
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	return strings.Join(lines[len(lines)-2:], "\n")
}

func localIDs(t *testing.T, store localissues.Store) []string {
	t.Helper()
	is, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range is {
		ids = append(ids, x.ID())
	}
	return ids
}

func savedRecent(t *testing.T, path string) []string {
	t.Helper()
	recent, err := readRecent(path)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, d := range recent {
		labels = append(labels, d.label())
	}
	return labels
}

var billing = linear.NewIssue{TeamID: "t1", ProjectID: "p1", AssignToMe: true}

func TestNIntoAClosedBacklogOpensItAndSavesTheFile(t *testing.T) {
	e, _ := captureEnv(t)
	e.m.opts.Groups = filepath.Join(t.TempDir(), "groups.json")
	e.m = feed(e.m, boardMsg(fixture()))
	if out := ansi.Strip(e.m.View()); !strings.Contains(out, "BACKLOG  1 …") || strings.Contains(out, "ENG-20") {
		t.Fatalf("Backlog starts open:\n%s", out)
	}
	e.press("n", "Retry failed loads", "enter")
	out := ansi.Strip(e.m.View())
	inOrder(t, out, "BACKLOG  2\n", "ENG-20", "Retry failed loads")
	if got := savedFolds(t, e.m.opts.Groups)[lifecycle.KindBuild]; !slices.Equal(got, []string{"Done"}) {
		t.Fatalf("groups.json holds %v for build, want [Done]", got)
	}
}

func TestAFailedAddLeavesABacklogClosed(t *testing.T) {
	e, _ := captureEnv(t)
	e.m.opts.LocalIssues = ""
	e.press("n", "Retry failed loads", "enter")
	if out := ansi.Strip(e.m.View()); strings.Contains(out, "ENG-20") {
		t.Fatalf("a failed add opened Backlog:\n%s", out)
	}
}

func TestNCapturesALocalIssueInTheBacklog(t *testing.T) {
	e, store := captureEnv(t)
	e.m.sel = "ENG-9"
	e.press("n", "Retry failed loads")
	if f := footLines(e.m); !strings.Contains(f, "new issue: Retry failed loads") || !strings.Contains(f, "to: local   tab: change   enter: add   esc: cancel") {
		t.Fatalf("footer %q, want the capture box with the local Destination", f)
	}
	e.press("enter")
	is, err := store.Get("local-2")
	if err != nil || is.Title != "Retry failed loads" || is.Status != localissues.Backlog {
		t.Fatalf("local-2 is %+v (%v), want the title in the backlog", is, err)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "added local-2") {
		t.Fatalf("footer %q, want the new Issue named", f)
	}
	body := strings.Split(ansi.Strip(e.m.View()), "\n")
	inOrder(t, strings.Join(body[:len(body)-1], "\n"), "BACKLOG", "ENG-20", "local-2")
	if len(e.api.created) != 0 {
		t.Fatalf("a local capture created Linear Issues: %+v", e.api.created)
	}
}

func TestTabPicksALinearProjectAndTheIssueIsAssignedToTheUser(t *testing.T) {
	e, store := captureEnv(t)
	e.press("n", "Split billing ingest", "tab", "bill", "enter")
	if f := footLines(e.m); !strings.Contains(f, "new issue: Split billing ingest") || !strings.Contains(f, "to: acme / ENG / Billing") {
		t.Fatalf("footer %q, want the title kept and the picked Destination", f)
	}
	delete(e.wrote, "w1/my")
	e.press("enter")
	want := billing
	want.Title = "Split billing ingest"
	if !slices.Equal(e.api.created, []linear.NewIssue{want}) {
		t.Fatalf("created %+v, want one Issue in ENG's Billing, assigned to the user, with no status", e.api.created)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "added ENG-900") {
		t.Fatalf("footer %q, want the new Issue named", f)
	}
	if _, ok := e.wrote["w1/my"]; !ok {
		t.Fatal("the capture did not fetch the Issue rows again")
	}
	if got := localIDs(t, store); !slices.Equal(got, []string{"local-1"}) {
		t.Fatalf("local Issues %v, want no new file", got)
	}
}

// After a Linear pick the box opens on local again, and the pick leads the
// recent Destinations: in the list and in destinations.json, which keeps 5
// and names each Destination once.
func TestTheDefaultStaysLocalAndThePickLeadsTheRecentDestinations(t *testing.T) {
	e, _ := captureEnv(t)
	var seed []destination
	for _, p := range []string{"A", "B", "C", "D", "E"} {
		seed = append(seed, destination{Workspace: "w9", WorkspaceName: "globex", TeamID: "t9", TeamKey: "OPS", ProjectID: "p" + p, ProjectName: p})
	}
	if err := config.WriteJSON(e.m.opts.Destinations, seed); err != nil {
		t.Fatal(err)
	}
	e.press("n", "One", "tab", "bill", "enter", "enter")
	e.press("n")
	if f := footLines(e.m); !strings.Contains(f, "to: local ") {
		t.Fatalf("footer %q, want local after a Linear pick", f)
	}
	e.press("tab")
	var rows []string
	for _, l := range strings.Split(ansi.Strip(e.m.View()), "\n")[3:] {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			rows = append(rows, l)
		}
	}
	want := []string{"▸ local just me", "acme / ENG / Billing recent", "globex / OPS / A recent", "globex / OPS / B recent",
		"globex / OPS / C recent", "globex / OPS / D recent", "acme / ENG"}
	if !slices.Equal(rows[:len(want)], want) {
		t.Fatalf("list rows %q, want %q", rows, want)
	}
	if got, want := savedRecent(t, e.m.opts.Destinations), []string{"acme / ENG / Billing", "globex / OPS / A", "globex / OPS / B", "globex / OPS / C", "globex / OPS / D"}; !slices.Equal(got, want) {
		t.Fatalf("destinations.json %q, want %q", got, want)
	}
	e.press("esc", "esc", "n", "Two", "tab", "D", "enter", "enter", "n", "Three", "tab", "bill", "enter", "enter")
	if got, want := savedRecent(t, e.m.opts.Destinations), []string{"acme / ENG / Billing", "globex / OPS / D", "globex / OPS / A", "globex / OPS / B", "globex / OPS / C"}; !slices.Equal(got, want) {
		t.Fatalf("destinations.json %q, want %q", got, want)
	}
}

func TestEscInTheListKeepsTheTitleAndEscInTheBoxCancels(t *testing.T) {
	e, store := captureEnv(t)
	e.press("n", "Keep me", "tab", "acme", "esc")
	if f := footLines(e.m); !strings.Contains(f, "new issue: Keep me") || !strings.Contains(f, "to: local") {
		t.Fatalf("footer %q, want the box with the title kept", f)
	}
	e.press("esc")
	if f := footerAt(e.m, 200); !strings.Contains(f, "n new Issue") {
		t.Fatalf("footer %q, want the key hints back", f)
	}
	e.press("n", "enter")
	e.press("n", "   ", "enter")
	e.press("n", "tab", "bill", "enter", "enter")
	if f := footerAt(e.m, 200); !strings.Contains(f, "n new Issue") {
		t.Fatalf("footer %q, want the key hints after an empty title", f)
	}
	if got := localIDs(t, store); !slices.Equal(got, []string{"local-1"}) || len(e.api.created) != 0 {
		t.Fatalf("local Issues %v and Linear Issues %+v, want no new Issue", got, e.api.created)
	}
}

func TestTheCaptureBoxTakesEveryKeyAsText(t *testing.T) {
	e, store := captureEnv(t)
	e.m = feed(e.m, boardMsg(fixture()))
	e.m.sel = "ENG-590"
	e.press("n")
	for _, k := range []string{"1", "2", "3", "q", "s", "x"} {
		var cmd tea.Cmd
		e.m, cmd = press(e.m, k)
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Fatalf("%s quit the Board", k)
			}
		}
	}
	if e.m.view != viewBoard || len(e.sched.ops()) != 0 {
		t.Fatalf("view %v and ops %v, want the work view and no op", e.m.view, e.sched.ops())
	}
	if f := footLines(e.m); !strings.Contains(f, "new issue: 123qsx") {
		t.Fatalf("footer %q, want every key as text", f)
	}
	e.press("enter")
	if is, err := store.Get("local-2"); err != nil || is.Title != "123qsx" {
		t.Fatalf("local-2 is %+v (%v), want the typed title", is, err)
	}
}

func TestAFailedLinearCreateLeavesNoLocalFileAndNamesTheError(t *testing.T) {
	e, store := captureEnv(t)
	e.api.createErr = errors.New("rate limited")
	e.press("n", "Split billing ingest", "tab", "bill", "enter", "enter")
	if f := footerAt(e.m, 200); !strings.Contains(f, "add Issue at acme / ENG / Billing: rate limited") {
		t.Fatalf("footer %q, want the error", f)
	}
	if got := localIDs(t, store); !slices.Equal(got, []string{"local-1"}) {
		t.Fatalf("local Issues %v, want no new file", got)
	}
	if _, err := os.Stat(e.m.opts.Destinations); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed create saved the Destination: %v", err)
	}
}

// The list offers local while the Linear Destinations load, loads them once,
// and shows a workspace that fails as one error line.
func TestTheListLoadsOnceAndAFailedWorkspaceIsOneLine(t *testing.T) {
	e := newTrkEnv(t)
	e.api.teamsErr = map[string]error{"w2": errors.New("token expired")}
	e.m.opts.LocalIssues = t.TempDir()
	m, _ := press(e.m, "n", "Title")
	m, load := press(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "▸ local") || !strings.Contains(v, "loading…") {
		t.Fatalf("want local and loading… while the list loads:\n%s", v)
	}
	m, _ = press(m, "enter")
	if f := footLines(m); !strings.Contains(f, "to: local") {
		t.Fatalf("footer %q, want local picked while the list loads", f)
	}
	m = drain(m, load)
	m, _ = press(m, "tab")
	v := ansi.Strip(m.View())
	inOrder(t, v, "local", "acme / ENG", "acme / ENG / Billing", "Globex: token expired")
	if strings.Contains(v, "loading…") || strings.Count(v, "token expired") != 1 {
		t.Fatalf("want the list loaded with one error line:\n%s", v)
	}
	reads := e.api.teamReads
	m, cmd := press(m, "esc", "tab")
	drain(m, cmd)
	if e.api.teamReads != reads {
		t.Fatalf("the second tab read the teams again: %d reads, want %d", e.api.teamReads, reads)
	}
}

func TestALongProjectNameKeepsTheTagOnScreen(t *testing.T) {
	e, _ := captureEnv(t)
	long := destination{Workspace: "w1", WorkspaceName: "acme", TeamID: "t1", TeamKey: "ENG", ProjectID: "p9", ProjectName: strings.Repeat("Long name ", 12)}
	e.m = feed(e.m, destRecentMsg{recent: []destination{long}})
	e.m.width = 60
	m, _ := press(e.m, "n", "tab")
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for _, row := range []struct{ label, tag string }{{"local", "just me"}, {"acme / ENG / Long name", "recent"}} {
		i := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, row.label) })
		if i < 0 || !strings.HasSuffix(strings.TrimSpace(lines[i]), row.tag) {
			t.Fatalf("the %s row lost its tag %q at width 60:\n%s", row.label, row.tag, strings.Join(lines, "\n"))
		}
	}
}
