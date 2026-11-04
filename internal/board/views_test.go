package board

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/linear"
)

var (
	engTeam     = linear.Team{ID: "t1", Key: "ENG"}
	adaView     = linear.View{ID: "cv-ada", Name: "Current+ (Ada)", Kind: linear.ViewIssues, CustomID: "cv-ada"}
	teamView    = linear.View{ID: "cv-team", Name: "Current+ (Team)", Kind: linear.ViewIssues, CustomID: "cv-team"}
	engIssues   = linear.View{ID: "activeIssues:t1", Name: "ENG Issues", Kind: linear.ViewIssues, Team: engTeam, Predefined: "activeIssues"}
	engProjects = linear.View{ID: "projects:t1", Name: "ENG Projects", Kind: linear.ViewProjects, Team: engTeam, Predefined: "projects"}
	cycle10     = &linear.Cycle{Name: "Cycle 10",
		StartsAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
)

func vissue(id, state, typ string, pos float64, pri int, pso float64) linear.Issue {
	is := lissue(id, "Work on "+id, state)
	is.State = linear.State{ID: state, Name: state, Type: typ, Position: pos}
	is.Priority, is.PrioritySortOrder, is.Cycle = pri, pso, cycle10
	return is
}

// adaIssues is Current+ (Ada) as Linear returned it.
func adaIssues() []linear.Issue {
	return []linear.Issue{
		vissue("ENG-603", "Code Review", "started", 4000, 0, -208134),
		vissue("ENG-587", "Code Review", "started", 4000, 0, -192237),
		vissue("ENG-537", "In QA", "started", 5000, 0, -186249),
		vissue("ENG-486", "Ready", "unstarted", 2000, 2, -154547.88),
		vissue("ENG-478", "Code Review", "started", 4000, 0, -174336.43),
		vissue("ENG-428", "Ready", "unstarted", 2000, 4, -42549.59),
		vissue("ENG-34", "Ready", "unstarted", 2000, 4, 3120.53),
	}
}

func engProjectList() []linear.ProjectInfo {
	return []linear.ProjectInfo{
		{ID: "p-idea", Name: "Dark mode", Status: "Idea", StatusType: "backlog", StatusPosition: 5016.23, Health: "offTrack"},
		{ID: "p-plan", Name: "Search revamp", Status: "Planned", StatusType: "planned", StatusPosition: 1, Health: "atRisk"},
		{ID: "p-design", Name: "Pricing", Status: "Shaping", StatusType: "backlog", StatusPosition: 7076.09},
		{ID: "p-prog", Name: "Billing model", Status: "In Progress", StatusType: "started", StatusPosition: 4000, Priority: 2,
			Health: "onTrack", Lead: "ada", TargetDate: "2026-11-15", Progress: 0.42},
	}
}

// newViewsEnv has the four favorite views in w1, in Linear's sidebar order.
func newViewsEnv(t *testing.T) *trkEnv {
	e := newTrkEnv(t)
	e.api.views = map[string][]linear.View{"w1": {adaView, teamView, engIssues, engProjects}}
	e.api.viewIssues = map[string][]linear.Issue{
		"cv-ada":          adaIssues(),
		"cv-team":         {vissue("ENG-700", "Todo", "unstarted", 1, 0, 0)},
		"activeIssues:t1": {vissue("ENG-800", "Todo", "unstarted", 1, 0, 0)},
	}
	e.api.viewProjects = map[string][]linear.ProjectInfo{"projects:t1": engProjectList()}
	return e
}

// inOrder fails unless each of want appears in s after the one before it.
func inOrder(t *testing.T, s string, want ...string) {
	t.Helper()
	at := -1
	for _, w := range want {
		i := strings.Index(s, w)
		if i < 0 || i <= at {
			t.Fatalf("want %q after %q in:\n%s", w, want, s)
		}
		at = i
	}
}

func tabLine(m Model) string { return strings.Split(m.View(), "\n")[2] }

func TestTrackerOpensOnTheFirstFavoriteViewGroupedAsLinear(t *testing.T) {
	e := newViewsEnv(t)
	e.open()
	v := e.m.View()
	inOrder(t, tabLine(e.m), "[Current+ (Ada)]", "Current+ (Team)", "ENG Issues", "ENG Projects", "Browse")
	if strings.Contains(v, "Linear ›") {
		t.Fatalf("a view at its home has no crumbs:\n%s", v)
	}
	inOrder(t, v, "Cycle 10  Oct 28 → Nov 11  7 Issues",
		"In QA  1", "ENG-537 ",
		"Code Review  3", "ENG-603 ", "ENG-587 ", "ENG-478 ",
		"Ready  3", "ENG-486 ", "ENG-428 ", "ENG-34 ")
	if _, ok := e.wrote["w1/view:cv-ada"]; !ok {
		t.Fatalf("the view's Issues must be cached for the next paint: %v", e.wrote)
	}
	e.press("/", "6", "0", "3")
	if v := e.m.View(); strings.Contains(v, "Cycle 10") || !strings.Contains(v, "ENG-603") || strings.Contains(v, "ENG-587") {
		t.Fatalf("a search lists the matches without headers:\n%s", v)
	}
}

func TestTrackerAppliesTheSavedRank(t *testing.T) {
	e := newViewsEnv(t)
	e.rank = []string{"projects:t1", "gone", "cv-team"}
	e.open()
	inOrder(t, tabLine(e.m), "[ENG Projects]", "Current+ (Team)", "Current+ (Ada)", "ENG Issues", "Browse")
	v := e.m.View()
	inOrder(t, v, "In Progress  1", "Billing model", "On track", "ada", "Nov 15", "42%",
		"Planned  1", "Search revamp", "At risk",
		"Shaping  1", "Pricing",
		"Idea  1", "Dark mode", "Off track")
}

func TestTrackerMovesAViewTabAndSavesTheRank(t *testing.T) {
	e := newViewsEnv(t)
	e.rank = []string{"gone"}
	e.open()
	e.press(">")
	inOrder(t, tabLine(e.m), "Current+ (Team)", "[Current+ (Ada)]", "ENG Issues", "ENG Projects", "Browse")
	want := []string{"cv-team", "cv-ada", "activeIssues:t1", "projects:t1", "gone"}
	if len(e.saved) != 1 || !slices.Equal(e.saved[0], want) {
		t.Fatalf("saved %v, want %v", e.saved, want)
	}
	e.press(">", ">", ">")
	inOrder(t, tabLine(e.m), "Current+ (Team)", "ENG Issues", "ENG Projects", "[Current+ (Ada)]", "Browse")
	if len(e.saved) != 3 {
		t.Fatalf("a view never moves past Browse: %d saves", len(e.saved))
	}
	e.press("tab", "<")
	if !strings.Contains(tabLine(e.m), "[Browse]") || len(e.saved) != 3 {
		t.Fatalf("Browse does not move: %d saves\n%s", len(e.saved), tabLine(e.m))
	}
	e.saveErr = errors.New("disk full")
	e.press("tab", ">")
	if f := footerAt(e.m, 100); !strings.Contains(f, "disk full") {
		t.Fatalf("a failed save must say so: %q", f)
	}
}

func TestTrackerTabCyclesTheViewsThenBrowse(t *testing.T) {
	e := newViewsEnv(t)
	e.open()
	e.press("tab")
	if v := e.m.View(); !strings.Contains(tabLine(e.m), "[Current+ (Team)]") || !strings.Contains(v, "ENG-700") {
		t.Fatalf("tab opens the next view:\n%s", v)
	}
	e.press("tab", "tab", "tab")
	if v := e.m.View(); !strings.Contains(tabLine(e.m), "[Browse]") || !strings.Contains(v, "My Issues (all workspaces)") {
		t.Fatalf("Browse is the last tab and opens at the workspaces:\n%s", v)
	}
	e.press("tab")
	if !strings.Contains(tabLine(e.m), "[Current+ (Ada)]") {
		t.Fatalf("tab wraps to the first view:\n%s", e.m.View())
	}
	e.press("shift+tab")
	if !strings.Contains(tabLine(e.m), "[Browse]") {
		t.Fatalf("shift+tab wraps back to Browse:\n%s", e.m.View())
	}
}

func TestTrackerViewRowOpensAndEscStopsAtTheView(t *testing.T) {
	e := newViewsEnv(t)
	e.open()
	e.press("enter")
	if v := e.m.View(); !strings.Contains(v, "ENG-537") || !strings.Contains(v, "billing") {
		t.Fatalf("enter reads the first row of the view:\n%s", v)
	}
	e.press("esc")
	if v := e.m.View(); !strings.Contains(tabLine(e.m), "[Current+ (Ada)]") || !strings.Contains(v, "Cycle 10") {
		t.Fatalf("esc returns to the view:\n%s", v)
	}
	e.press("esc")
	if v := e.m.View(); e.m.view != viewTracker || !strings.Contains(v, "Cycle 10") {
		t.Fatalf("esc at the view's home stays:\n%s", v)
	}
	e.press("s", "enter")
	if c := e.sched.calls; len(c) != 2 || c[0].Issue != "ENG-537" {
		t.Fatalf("s starts the selected view row: %+v", c)
	}
	e.press("n")
	if e.m.tr.ov != ovNone || !strings.Contains(e.m.status, "to add an Issue") {
		t.Fatalf("a custom view names no team for quick add: overlay %d, status %q", e.m.tr.ov, e.m.status)
	}
	e.press("tab", "tab", "n", "h", "i", "enter")
	if len(e.api.created) != 1 || e.api.created[0] != (linear.NewIssue{TeamID: "t1", Title: "hi"}) {
		t.Fatalf("quick add in ENG Issues files to ENG: %+v", e.api.created)
	}
}

func TestTrackerProjectViewOpensAProjectAndEscReturns(t *testing.T) {
	e := newViewsEnv(t)
	e.rank = []string{"projects:t1"}
	e.open()
	e.press("enter")
	v := e.m.View()
	if !slices.Equal(e.api.projIssues, []string{"p-prog"}) || !strings.Contains(v, "Linear › ENG Projects › Billing model") ||
		!strings.Contains(v, "blocked by ENG-590") {
		t.Fatalf("enter opens the Project's Issues (read %v):\n%s", e.api.projIssues, v)
	}
	e.press("n", "h", "i", "enter")
	if len(e.api.created) != 1 || e.api.created[0] != (linear.NewIssue{TeamID: "t1", Title: "hi", ProjectID: "p-prog"}) {
		t.Fatalf("quick add files to the Project in the view's team: %+v", e.api.created)
	}
	e.press("esc")
	if v := e.m.View(); strings.Contains(v, "Linear ›") || !strings.Contains(v, "In Progress  1") {
		t.Fatalf("esc returns to the Project view:\n%s", v)
	}
	e.press("esc")
	if v := e.m.View(); !strings.Contains(v, "In Progress  1") {
		t.Fatalf("esc at the view's home stays:\n%s", v)
	}
}

func TestTrackerBrowseStillDrillsDown(t *testing.T) {
	e := newViewsEnv(t)
	e.open()
	e.press("shift+tab", "j", "enter", "enter", "enter")
	v := e.m.View()
	if !strings.Contains(v, "Linear › acme › ENG › Billing") || !strings.Contains(v, "blocked by ENG-590") {
		t.Fatalf("Browse reaches a Project's Issues:\n%s", v)
	}
	e.press("esc", "esc", "esc", "esc")
	if e.m.view != viewTracker {
		t.Fatalf("esc at the Browse root must stay in the Linear browser")
	}
}

func TestIssueRowChipsGiveWayToTheTitle(t *testing.T) {
	e := newTrkEnv(t)
	is := &e.api.issues[2] // ENG-590, "Add billing model"
	is.Priority, is.Estimate = 2, 5
	is.PRs = []linear.PR{{Number: 4810, Status: "inReview"}}
	is.Labels = []linear.Label{{Name: "Bug", Color: "#eb5757"}}
	is.Assignee = &linear.Person{Name: "ada.lovelace"}
	is.CreatedAt = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	e.toIssues()
	row := func() string {
		for _, l := range strings.Split(e.m.View(), "\n") {
			if strings.Contains(l, "ENG-590 ") {
				return l
			}
		}
		t.Fatalf("no ENG-590 row:\n%s", e.m.View())
		return ""
	}
	e.m.width = 160
	inOrder(t, row(), "▂▄▆", "ENG-590", "In Progress", "Add billing model", "#4810", "● Bug", "△5", "AL", "Oct 17")
	if strings.Contains(row(), "◇ Billing") {
		t.Fatalf("a Project's own list leaves out the Project chip: %q", row())
	}
	e.m.width = 70
	r := row()
	if strings.Contains(r, "#4810") || strings.Contains(r, "Bug") || !strings.Contains(r, "Add billing model") {
		t.Fatalf("narrow rows drop chips from the front and keep the title: %q", r)
	}
	inOrder(t, r, "△5", "AL", "Oct 17")
}

func TestTrackerWaitsForTheViewsBeforeTheFirstTab(t *testing.T) {
	e := newViewsEnv(t)
	m, cmd := e.m, e.m.Init()
	next, views := m.Update(cmd())
	m = next.(Model)
	if v := m.View(); !strings.Contains(v, "loading your Linear views") {
		t.Fatalf("before the views come back:\n%s", v)
	}
	m, enter := press(m, "enter")
	m = drain(m, enter)
	if _, ok := e.wrote["w1/my"]; ok {
		t.Fatal("enter while the views load must not open My Issues")
	}
	m = drain(m, views)
	if v := m.View(); !strings.Contains(tabLine(m), "[Current+ (Ada)]") || !strings.Contains(v, "Cycle 10") {
		t.Fatalf("the first view opens once the views are back:\n%s", v)
	}
}

func TestTargetDateShowsTheYearOnlyWhenItDiffers(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{"2026-11-19": "Nov 19", "2027-01-28": "Jan 28 2027", "": ""} {
		if got := targetDate(in, now); got != want {
			t.Errorf("targetDate(%q) = %q, want %q", in, got, want)
		}
	}
}
