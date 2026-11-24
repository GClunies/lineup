package board

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

// teamIssue is an unassigned Issue of team PD in a state of type typ.
func teamIssue(id, typ string, pri int, blockedBy ...string) linear.Issue {
	is := mineIssue(id, typ, pri, blockedBy...)
	is.Team, is.Project = linear.Team{ID: "t2", Key: "PD"}, nil
	return is
}

// teamEnv is a work view of acme whose teams are PD. ENG-9 is in My Issues,
// and team is the unassigned Issues of PD.
func teamEnv(t *testing.T, team ...linear.Issue) *workEnv {
	t.Helper()
	e := newWorkEnv(t, acme)
	e.m.opts.Teams = []string{"PD"}
	e.api.issues = []linear.Issue{mineIssue("ENG-9", "unstarted", 2)}
	e.api.team = team
	e.m = drain(e.m, e.m.loadMine())
	return e
}

// rowCount counts the rows on the screen whose ID is id.
func rowCount(out, id string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(strings.TrimLeft(l, " ▸")); len(f) > 0 && f[0] == id {
			n++
		}
	}
	return n
}

// An Issue that is in My Issues too shows only there: the Team list can lag
// a start.
func TestAnUnassignedTeamIssueShowsInTeamAndAnIssueOfMineDoesNot(t *testing.T) {
	e := teamEnv(t, teamIssue("PD-1", "backlog", 0), mineIssue("ENG-9", "unstarted", 2))
	if got := e.api.teamKeys; !reflect.DeepEqual(got, [][]string{{"PD"}}) {
		t.Fatalf("TeamIssues got the keys %v, want PD", got)
	}
	out := screen(e.m)
	inOrder(t, out, "Team 1 …", "Up next 1", "ENG-9")
	if strings.Contains(out, "PD-1") {
		t.Fatalf("Team starts closed, but it shows its row:\n%s", out)
	}
	e.m.sel = headingKey(groupTeam)
	e.press("enter")
	out = screen(e.m)
	inOrder(t, out, "Team 1", "PD-1", "Up next 1", "ENG-9")
	if line := lineOf(t, out, "Team 1"); strings.Contains(line, "…") {
		t.Fatalf("enter left Team closed: %q", line)
	}
	if line := lineOf(t, out, "PD-1"); !strings.Contains(line, " team ") {
		t.Fatalf("the PD-1 row %q has no team status word", line)
	}
	if n := rowCount(out, "ENG-9"); n != 1 {
		t.Fatalf("ENG-9 shows %d times, want once in Up next:\n%s", n, out)
	}
}

func TestTeamRowsSortUnblockedFirstThenByPriorityLevelAndID(t *testing.T) {
	e := teamEnv(t,
		teamIssue("PD-9", "backlog", 2, "PD-11"),
		teamIssue("PD-10", "triage", 0),
		teamIssue("PD-11", "unstarted", 2, "PD-6"),
		teamIssue("PD-6", "backlog", 4),
		teamIssue("PD-3", "unstarted", 1),
		teamIssue("PD-2", "backlog", 1),
	)
	e.m = openAll(e.m)
	inOrder(t, screen(e.m), "Team 6", "PD-2", "PD-3", "PD-6", "PD-10", "PD-11", "PD-9", "Up next 1")
}

func TestATaskThatHoldsATeamIssueLeavesNoTeamRow(t *testing.T) {
	e := teamEnv(t, teamIssue("PD-1", "backlog", 0))
	if out := screen(openAll(e.m)); !strings.Contains(out, "Team 1") {
		t.Fatalf("no Team row before the Task:\n%s", out)
	}
	v := scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{tv("PD-1", lifecycle.Working, "data", "The Task", "", time.Minute)}}
	e.m = openAll(feed(e.m, boardMsg(v)))
	out := screen(e.m)
	if strings.Contains(out, "Team 1") || rowCount(out, "PD-1") != 1 {
		t.Fatalf("want PD-1 once, as the Task row, and no Team group:\n%s", out)
	}
}

// lineup add starts the Task, and the start assigns the Issue to the user.
func TestSOnATeamRowAddsItAsLinear(t *testing.T) {
	e := teamEnv(t, teamIssue("PD-1", "backlog", 0))
	e.m.sel = headingKey(groupTeam)
	e.press("enter", "j")
	if e.m.sel != "PD-1" {
		t.Fatalf("selection %s, want PD-1", e.m.sel)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "s start") {
		t.Fatalf("footer %q, want the Linear row's keys", f)
	}
	e.press("s")
	if v := e.m.View(); !strings.Contains(v, "Which skill for PD-1?") {
		t.Fatalf("no skill picker:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add"}) {
		t.Fatalf("ops %v, want add", got)
	}
	if add := e.sched.calls[0]; add.Issue != "PD-1" || add.Kind != scheduler.KindLinear || add.Repo != "" {
		t.Fatalf("add %+v, want PD-1 with Kind linear", add)
	}
}

func TestCOnATeamRowAsksTheRowsWorkspace(t *testing.T) {
	e := teamEnv(t, teamIssue("PD-1", "backlog", 0))
	e.m = openAll(e.m)
	e.m.sel = "PD-1"
	e.press("c")
	if got := e.api.statesFrom; !slices.Equal(got, []string{acme.ID}) {
		t.Fatalf("TeamStates ran in the workspaces %q, want acme's", got)
	}
}

func TestWithNoTeamsTheBoardNeverReadsTeamIssues(t *testing.T) {
	e := newWorkEnv(t, acme)
	e.api.team = []linear.Issue{teamIssue("PD-1", "backlog", 0)}
	e.m = drain(e.m, e.m.loadMine())
	_, cmd := e.m.Update(mineTick{})
	e.m = drain(e.m, cmd().(tea.BatchMsg)[0])
	if len(e.api.teamKeys) != 0 {
		t.Fatalf("TeamIssues ran with the keys %v, want no call", e.api.teamKeys)
	}
	if out := screen(openAll(e.m)); strings.Contains(out, "Team 1") || strings.Contains(out, "PD-1") {
		t.Fatalf("a Team row with no teams:\n%s", out)
	}
}

// The Team list paints from its own cache key after My Issues came back live.
// A failed live read keeps the cached rows and names the list.
func TestTeamRowsPaintFromTheirCacheAndKeepItWhenTheLiveReadFails(t *testing.T) {
	e := newWorkEnv(t, acme)
	e.m.opts.Teams = []string{"PD"}
	e.cache["w1/team"] = []linear.Issue{teamIssue("PD-1", "backlog", 0)}
	e.api.teamErr = errors.New("timeout")
	e.m = drain(e.m, e.m.loadMine())
	if out := screen(openAll(e.m)); !strings.Contains(out, "PD-1") {
		t.Fatalf("no cached Team row:\n%s", out)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "team Issues in acme: timeout") {
		t.Fatalf("footer %q, want the failed list and workspace", f)
	}
	if _, ok := e.wrote["w1/team"]; ok {
		t.Fatal("a failed read wrote the cache")
	}
}

// A cached Team list that comes back after the live one does not replace it.
func TestALateCachedTeamListDoesNotReplaceTheLiveOne(t *testing.T) {
	e := newWorkEnv(t, acme)
	e.m.opts.Teams = []string{"PD"}
	e.cache["w1/team"] = []linear.Issue{teamIssue("PD-1", "backlog", 0)}
	e.api.team = []linear.Issue{teamIssue("PD-2", "backlog", 0)}
	m, cmd, _ := e.m.mineMsg(mineWorkspacesMsg{wss: []linear.Workspace{acme}})
	// Each list reads its cache first, then live. Fed last to first, each
	// cached list lands after its live one.
	for _, read := range slices.Backward(cmd().(tea.BatchMsg)) {
		if msg := read(); msg != nil {
			m = feed(m, msg)
		}
	}
	if out := screen(openAll(m)); !strings.Contains(out, "PD-2") || strings.Contains(out, "PD-1") {
		t.Fatalf("want the live PD-2 and not the cached PD-1:\n%s", out)
	}
	if got := e.wrote["w1/team"]; len(got) != 1 || got[0].Identifier != "PD-2" {
		t.Fatalf("cache written %v, want the live list", got)
	}
}

func TestASignedOutWorkspaceLosesItsTeamRows(t *testing.T) {
	e := teamEnv(t, teamIssue("PD-1", "backlog", 0))
	if out := screen(openAll(e.m)); !strings.Contains(out, "PD-1") {
		t.Fatalf("no Team row before the sign-out:\n%s", out)
	}
	e.m = openAll(feed(e.m, mineWorkspacesMsg{}))
	if out := screen(e.m); strings.Contains(out, "PD-1") || strings.Contains(out, "ENG-9") {
		t.Fatalf("rows of a signed-out workspace:\n%s", out)
	}
}

// bigTeam is teamEnv with the Team rows PD-1 to PD-n, in that order, and
// Team open with its heading selected.
func bigTeam(t *testing.T, n int) *workEnv {
	t.Helper()
	e := teamEnv(t, pdIssues(1, n)...)
	e.m.sel = headingKey(groupTeam)
	e.press("enter")
	return e
}

// pdIssues is the Team Issues PD-from to PD-to, all of priority 2.
func pdIssues(from, to int) []linear.Issue {
	var team []linear.Issue
	for i := from; i <= to; i++ {
		team = append(team, teamIssue(fmt.Sprintf("PD-%d", i), "backlog", 2))
	}
	return team
}

// pdIDs is the IDs PD-from to PD-to.
func pdIDs(from, to int) []string {
	var ids []string
	for _, is := range pdIssues(from, to) {
		ids = append(ids, is.Identifier)
	}
	return ids
}

// pdRows lists the PD rows above the strip, in screen order.
func pdRows(m Model) []string {
	var ids []string
	for _, l := range strings.Split(bodyOf(m), "\n") {
		if f := strings.Fields(strings.TrimLeft(l, " ▸")); len(f) > 0 && strings.HasPrefix(f[0], "PD-") {
			ids = append(ids, f[0])
		}
	}
	return ids
}

// An open Team shows its first 10 rows and a more line. j stops on the more
// line, and enter on it shows every row and selects the first one that was
// hidden (PROD-BOARD-024).
func TestAnOpenTeamShowsTenRowsAndEnterOnTheMoreLineShowsTheRest(t *testing.T) {
	e := bigTeam(t, 15)
	out := screen(e.m)
	if got := pdRows(e.m); !slices.Equal(got, pdIDs(1, 10)) {
		t.Fatalf("Team shows %v, want PD-1 to PD-10:\n%s", got, out)
	}
	inOrder(t, out, "Team 15", "PD-10", "+ 5 more rows", "Up next 1")
	for range 11 {
		e.press("j")
	}
	out = screen(e.m)
	if l := lineOf(t, out, "+ 5 more rows"); !strings.Contains(l, "▸") {
		t.Fatalf("11 j from the Team heading must select the more line:\n%s", out)
	}
	if f := footerAt(e.m, 100); !strings.Contains(f, "\n Team\n 5 more rows\n") || !strings.Contains(f, "enter show all") {
		t.Fatalf("the strip on the more line must show Team, 5 more rows, and enter show all:\n%s", f)
	}
	e.press("enter")
	out = screen(e.m)
	if got := pdRows(e.m); !slices.Equal(got, pdIDs(1, 15)) || strings.Contains(out, "more row") {
		t.Fatalf("enter on the more line shows %v, want PD-1 to PD-15 and no more line:\n%s", got, out)
	}
	if l := lineOf(t, out, "PD-11 "); !strings.Contains(l, "▸") {
		t.Fatalf("enter must select PD-11, the first row that was hidden:\n%s", out)
	}
}

// Closing Team brings its limit back (PROD-BOARD-024).
func TestClosingTeamBringsBackItsLimit(t *testing.T) {
	e := bigTeam(t, 15)
	for range 11 {
		e.press("j")
	}
	e.press("enter")
	if got := pdRows(e.m); len(got) != 15 {
		t.Fatalf("show all shows %v, want 15 rows", got)
	}
	e.m.sel = headingKey(groupTeam)
	e.press("enter", "enter")
	out := screen(e.m)
	if got := pdRows(e.m); !slices.Equal(got, pdIDs(1, 10)) || !strings.Contains(out, "+ 5 more rows") {
		t.Fatalf("after a close and an open, Team shows %v, want PD-1 to PD-10 and the more line:\n%s", got, out)
	}
}

// The filter shows every matching Team row, with no more line, and esc
// gives the limit back (PROD-BOARD-024).
func TestTheFilterShowsEveryMatchingTeamRow(t *testing.T) {
	e := bigTeam(t, 15)
	e.press("/", "P", "D", "enter")
	out := screen(e.m)
	if got := pdRows(e.m); !slices.Equal(got, pdIDs(1, 15)) || strings.Contains(out, "more row") {
		t.Fatalf("the filter shows %v, want every PD row and no more line:\n%s", got, out)
	}
	e.press("esc")
	out = screen(e.m)
	if got := pdRows(e.m); !slices.Equal(got, pdIDs(1, 10)) || !strings.Contains(out, "+ 5 more rows") {
		t.Fatalf("esc must give back the limit, Team shows %v:\n%s", got, out)
	}
}

// A tap on the more line shows every Team row, as enter does, and opens no
// menu (PROD-BOARD-024).
func TestATapOnTheMoreLineShowsEveryTeamRow(t *testing.T) {
	e := bigTeam(t, 15)
	e.m, _ = tap(t, e.m, "+ 5 more rows")
	out := screen(e.m)
	if got := pdRows(e.m); !slices.Equal(got, pdIDs(1, 15)) || strings.Contains(out, "back") {
		t.Fatalf("a tap on the more line shows %v, want PD-1 to PD-15 and no menu:\n%s", got, out)
	}
	if l := lineOf(t, out, "PD-11 "); !strings.Contains(l, "▸") {
		t.Fatalf("the tap must select PD-11, the first row that was hidden:\n%s", out)
	}
}

// A selected Team row that moves past the limit selects the more line
// (PROD-BOARD-005).
func TestATeamRowThatMovesPastTheLimitSelectsTheMoreLine(t *testing.T) {
	e := bigTeam(t, 15)
	e.m.sel = "PD-2"
	team := pdIssues(1, 15)
	team[1].Priority = 0 // PD-2 sorts last
	e.m = feed(e.m, mineIssuesMsg{ws: acme, live: true, team: true, issues: team})
	out := screen(e.m)
	if l := lineOf(t, out, "+ 5 more rows"); !strings.Contains(l, "▸") {
		t.Fatalf("PD-2 moved past the limit, so the more line must be selected:\n%s", out)
	}
}

// Rows past the limit of Team are not on screen, so they widen no column and
// fill no Repo column (PROD-BOARD-023, PROD-BOARD-024).
func TestRowsPastTheTeamLimitDoNotFitTheColumns(t *testing.T) {
	ops := func(i int) linear.Issue {
		is := teamIssue(fmt.Sprintf("OPS-%d", i), "backlog", 0)
		is.Team.Key, is.Unblocked = "OPS", true
		return is
	}
	board := func(team []linear.Issue) *workEnv {
		e := teamEnv(t, team...)
		e.m.opts.TeamRepos = func(key string) []string {
			if key == "OPS" {
				return []string{"acme/ops"}
			}
			return nil
		}
		e.m.height = 40
		e.m = openAll(e.m)
		e.m.sel = headingKey(groupTeam)
		return e
	}
	only := board(pdIssues(1, 10))
	team := pdIssues(1, 10)
	for i := 1; i <= 20; i++ {
		team = append(team, ops(i))
	}
	more := board(team)
	want := lineOf(t, screen(only.m), "PD-1 ")
	if got := lineOf(t, screen(more.m), "PD-1 "); got != want {
		t.Errorf("rows past the limit changed the columns:\n got %q\nwant %q", got, want)
	}
	// With every row on screen, the OPS rows widen the status column and fill
	// the Repo column, so the check above can fail.
	for range 11 {
		more.press("j")
	}
	more.press("enter")
	out := screen(more.m)
	if got := lineOf(t, out, "PD-1 "); got == want {
		t.Errorf("with every row on screen the PD-1 line must change: %q", got)
	}
	if l := lineOf(t, out, "OPS-1 "); !strings.HasSuffix(strings.TrimRight(l, " "), "  ops") {
		t.Errorf("with every row on screen the OPS-1 row must name its Repo: %q", l)
	}
}

// The Team heading adds + to its count when the Board read 100 Issues of one
// team, and so does the strip on it (PROD-BOARD-025).
func TestTheTeamHeadingMarksTheFetchCap(t *testing.T) {
	ops := func(i int) linear.Issue {
		is := teamIssue(fmt.Sprintf("OPS-%d", i), "backlog", 2)
		is.Team.Key = "OPS"
		return is
	}
	for _, c := range []struct {
		name          string
		pd, ops       int
		heading, rows string
	}{
		{"100 of PD", 100, 0, " Team 100+ …", " 100+ rows"},
		{"99 of PD", 99, 0, " Team 99 …", " 99 rows"},
		{"60 of PD and 40 of OPS", 60, 40, " Team 100 …", " 100 rows"},
	} {
		t.Run(c.name, func(t *testing.T) {
			team := pdIssues(1, c.pd)
			for i := 1; i <= c.ops; i++ {
				team = append(team, ops(i))
			}
			e := teamEnv(t, team...)
			out := screen(e.m)
			if !slices.Contains(strings.Split(out, "\n"), c.heading) {
				t.Errorf("no line is the heading %q:\n%s", c.heading, out)
			}
			e.m.sel = headingKey(groupTeam)
			if f := footerAt(e.m, 100); !strings.Contains(f, "\n Team\n"+c.rows+"\n") {
				t.Errorf("the strip on the Team heading must count %q:\n%s", c.rows, f)
			}
		})
	}
}
