package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// withClosed sets the closed groups of both Kinds, so a test names the state it reads.
func withClosed(m Model, groups ...string) Model {
	m.folds = folds{lifecycle.KindBuild: groups, lifecycle.KindReview: groups}
	m.setBoard(m.board)
	return m
}

// openAll opens every group, for a test that reads the rows of Backlog or Done.
func openAll(m Model) Model { return withClosed(m) }

// groupEnv is mineEnv with the fixture Board and groups.json in a temporary
// folder. The work view holds ENG-20 in Backlog and ENG-9 and local-1 in Ready.
func groupEnv(t *testing.T) *trkEnv {
	t.Helper()
	e, _ := mineEnv(t)
	e.m.opts.Groups = filepath.Join(t.TempDir(), "groups.json")
	e.m = feed(e.m, boardMsg(fixture()))
	e.m.sel = "ENG-588"
	return e
}

func screen(m Model) string { return ansi.Strip(m.View()) }

func savedFolds(t *testing.T, path string) folds {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f folds
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("groups.json %q is not JSON: %v", data, err)
	}
	return f
}

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

func TestEnterOnAHeadingTogglesTheGroupAndSavesTheFile(t *testing.T) {
	e := groupEnv(t)
	e.m.sel = headingKey("Backlog")
	if f := footerAt(e.m, 100); !strings.HasPrefix(f, " enter open   j/k move   n new Issue") {
		t.Fatalf("footer %q, want enter open, j/k move, then n new Issue", f)
	}
	e.press("enter")
	if out := screen(e.m); !strings.Contains(out, "BACKLOG  1\n") || !strings.Contains(out, "ENG-20") {
		t.Fatalf("Backlog did not open:\n%s", out)
	}
	if f := footerAt(e.m, 100); !strings.HasPrefix(f, " enter close   j/k move") {
		t.Fatalf("footer %q, want enter close first", f)
	}
	if got := savedFolds(t, e.m.opts.Groups)[lifecycle.KindBuild]; !slices.Equal(got, []string{"Done"}) {
		t.Fatalf("groups.json holds %v for build, want [Done]", got)
	}

	restart := New(fixture(), e.m.opts)
	restart = drain(restart, restart.loadMine())
	if out := screen(restart); !strings.Contains(out, "ENG-20") || strings.Contains(out, "ENG-560") {
		t.Fatalf("a new Board lost the saved groups:\n%s", out)
	}

	e.press("enter")
	if out := screen(e.m); !strings.Contains(out, "BACKLOG  1 …") || strings.Contains(out, "ENG-20") {
		t.Fatalf("Backlog did not close:\n%s", out)
	}
	if got := savedFolds(t, e.m.opts.Groups)[lifecycle.KindBuild]; !slices.Equal(got, []string{"Backlog", "Done"}) {
		t.Fatalf("groups.json holds %v for build, want [Backlog Done]", got)
	}
}

func TestAnUnreadableGroupsFileGivesTheDefaults(t *testing.T) {
	for name, content := range map[string]*string{"missing": nil, "broken": new("not json"), "unknown Kind": new(`{"deploy":[]}`)} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "groups.json")
			if content != nil {
				if err := os.WriteFile(path, []byte(*content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := readFolds(path)
			if want := defaultFolds(); !slices.Equal(got[lifecycle.KindBuild], want[lifecycle.KindBuild]) || !slices.Equal(got[lifecycle.KindReview], want[lifecycle.KindReview]) {
				t.Fatalf("folds %v, want the defaults %v", got, want)
			}
		})
	}
}

func TestAKindTheFileLeavesOutKeepsItsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.json")
	if err := os.WriteFile(path, []byte(`{"build":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readFolds(path)
	if len(got[lifecycle.KindBuild]) != 0 || !slices.Equal(got[lifecycle.KindReview], []string{"Done"}) {
		t.Fatalf("folds %v, want build open and review at its default", got)
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

func TestNeedsYouNeverClosesEvenWhenTheFileListsIt(t *testing.T) {
	e := groupEnv(t)
	if err := os.WriteFile(e.m.opts.Groups, []byte(`{"build":["Needs you","Done"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(fixture(), e.m.opts)
	m.width, m.height = 100, 30
	out := screen(m)
	if !strings.Contains(out, "NEEDS YOU  2\n") || !strings.Contains(out, "ENG-588") || !strings.Contains(out, "ENG-590") {
		t.Fatalf("Needs you hides its rows:\n%s", out)
	}
	if slices.Contains(m.rowIDs(), headingKey("Needs you")) {
		t.Fatalf("Needs you is a selection stop: %v", m.rowIDs())
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

func TestTheReviewTasksTabKeepsItsOwnClosedGroups(t *testing.T) {
	e := newRvEnv(t)
	e.m.opts.Groups = filepath.Join(t.TempDir(), "groups.json")
	v := reviewBoard()
	v.Tasks = append(v.Tasks,
		rtv("R-20", lifecycle.Done, "data#4001 Old review", urlLogs, 5*time.Hour),
		tv("ENG-1", lifecycle.Done, "data", "Old build", "#1", 5*time.Hour))
	e.m = feed(e.m, boardMsg(v))
	e.open()
	if out := screen(e.m); !strings.Contains(out, "DONE  1 …") || strings.Contains(out, "R-20") {
		t.Fatalf("the Reviews tab does not start with Done closed:\n%s", out)
	}
	e.m.sel = headingKey("Done")
	e.press("enter")
	if out := screen(e.m); !strings.Contains(out, "R-20") {
		t.Fatalf("enter on the Done heading did not open it:\n%s", out)
	}
	e.press("2")
	if out := screen(e.m); !strings.Contains(out, "DONE  1 …") || strings.Contains(out, "ENG-1 ") {
		t.Fatalf("a toggle on the Reviews tab changed the work view:\n%s", out)
	}
	got := savedFolds(t, e.m.opts.Groups)
	if len(got[lifecycle.KindReview]) != 0 || !slices.Equal(got[lifecycle.KindBuild], []string{"Backlog", "Done"}) {
		t.Fatalf("groups.json holds %v, want review open and build unchanged", got)
	}
}

// For random rows and random closed sets, the selection stops are the headings
// of the non-empty groups except Needs you, then the rows of the open groups.
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
		closed := rapid.SliceOfDistinct(rapid.SampledFrom(groupOrder), func(g string) string { return g }).Draw(t, "closed")

		m := newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Tasks: tasks})
		m.mine.loc = loc
		m = withClosed(m, closed...)
		ids := m.rowIDs()

		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("%q is listed twice in %v", id, ids)
			}
			seen[id] = true
		}
		var wantHeads []string
		for _, g := range groupOrder {
			rows := inGroup[g]
			if len(rows) == 0 {
				if seen[headingKey(g)] {
					t.Fatalf("the empty group %s has a heading in %v", g, ids)
				}
				continue
			}
			if g != groupNeedsYou {
				wantHeads = append(wantHeads, headingKey(g))
			}
			if seen[headingKey(g)] != (g != groupNeedsYou) {
				t.Fatalf("heading of %s: listed %v in %v", g, seen[headingKey(g)], ids)
			}
			open := g == groupNeedsYou || !slices.Contains(closed, g)
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
