package board

import (
	"errors"
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
