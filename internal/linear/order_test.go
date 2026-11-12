package linear

import (
	"strings"
	"testing"
	"time"
)

// iss makes an Issue whose ID is its identifier. Each blocker is an open
// "blocks" relation, so it may name an Issue outside the list.
func iss(id string, blockers ...string) Issue {
	is := Issue{ID: id, Identifier: id}
	for _, b := range blockers {
		is.Blockers = append(is.Blockers, Ref{ID: b, Identifier: b})
	}
	return is
}

func ids(is []Issue) string {
	var out []string
	for _, x := range is {
		out = append(out, x.Identifier)
	}
	return strings.Join(out, " ")
}

func TestOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []Issue
		want string
	}{
		{"no blockers sorts by natural ID",
			[]Issue{iss("ENG-100"), iss("ENG-10"), iss("ENG-9")}, "ENG-9 ENG-10 ENG-100"},
		{"a blocker comes before what it blocks, whatever the IDs",
			[]Issue{iss("ENG-1", "ENG-9"), iss("ENG-9")}, "ENG-9 ENG-1"},
		{"level is one above the highest blocker",
			[]Issue{iss("ENG-4", "ENG-2", "ENG-3"), iss("ENG-3", "ENG-1"), iss("ENG-2"), iss("ENG-1")},
			"ENG-1 ENG-2 ENG-3 ENG-4"},
		{"a chain in reverse ID order",
			[]Issue{iss("ENG-1", "ENG-2"), iss("ENG-2", "ENG-3"), iss("ENG-3")}, "ENG-3 ENG-2 ENG-1"},
		{"same level orders by ID",
			[]Issue{iss("ENG-20", "ENG-5"), iss("ENG-6", "ENG-5"), iss("ENG-5")}, "ENG-5 ENG-6 ENG-20"},
		{"a blocker outside the list counts as level 0",
			[]Issue{iss("ENG-2", "WEB-7"), iss("ENG-1")}, "ENG-1 ENG-2"},
		{"a blocker from another project sits at the level it has in the list",
			[]Issue{iss("ENG-8", "APP-1"), iss("APP-1", "APP-0"), iss("APP-0"), iss("ENG-9")},
			"APP-0 ENG-9 APP-1 ENG-8"},
		{"teams order by key at one level",
			[]Issue{iss("WEB-1"), iss("APP-3"), iss("APP-20")}, "APP-3 APP-20 WEB-1"},
		{"a two-issue cycle is cut and every issue appears once",
			[]Issue{iss("ENG-1", "ENG-2"), iss("ENG-2", "ENG-1")}, "ENG-1 ENG-2"},
		{"a cycle keeps the levels of the edges outside it",
			[]Issue{iss("ENG-3", "ENG-2"), iss("ENG-2", "ENG-1"), iss("ENG-1", "ENG-3"), iss("ENG-4", "ENG-3")},
			"ENG-3 ENG-1 ENG-4 ENG-2"},
		{"a self-block counts for nothing",
			[]Issue{iss("ENG-2", "ENG-2"), iss("ENG-1")}, "ENG-1 ENG-2"},
		{"empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]Issue(nil), tc.in...)
			got := ids(Order(tc.in))
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if ids(in) != ids(tc.in) {
				t.Error("Order changed its input")
			}
		})
	}
}

func TestLevelsGivesEachIssueItsDependencyLevel(t *testing.T) {
	got := Levels([]Issue{iss("ENG-4", "ENG-2", "ENG-3"), iss("ENG-3", "ENG-1"), iss("ENG-2"), iss("ENG-1"), iss("ENG-5", "WEB-9")})
	want := map[string]int{"ENG-1": 0, "ENG-2": 0, "ENG-3": 1, "ENG-4": 2, "ENG-5": 0}
	if len(got) != len(want) {
		t.Fatalf("levels %v, want %v", got, want)
	}
	for id, l := range want {
		if got[id] != l {
			t.Errorf("level of %s = %d, want %d", id, got[id], l)
		}
	}
}

func TestBlockedBy(t *testing.T) {
	if got := BlockedBy(iss("ENG-1", "ENG-590", "ENG-7")); strings.Join(got, ",") != "ENG-590,ENG-7" {
		t.Fatalf("%v", got)
	}
	if got := BlockedBy(iss("ENG-1")); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestNaturalLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"ENG-9", "ENG-10", true}, {"ENG-10", "ENG-9", false}, {"ENG-10", "ENG-100", true},
		{"AB-5", "ENG-1", true}, {"ENG-1", "ENG-1", false}, {"odd", "ENG-1", false}, {"ENG-1", "odd", true},
	} {
		if got := NaturalLess(tc.a, tc.b); got != tc.want {
			t.Errorf("NaturalLess(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}

func viewIssue(id, state, stateType string, pos float64, pri int, pso float64, cycle *Cycle) Issue {
	return Issue{ID: id, Identifier: id, Priority: pri, PrioritySortOrder: pso, Cycle: cycle,
		State: State{Name: state, Type: stateType, Position: pos}}
}

func TestViewOrder(t *testing.T) {
	c9 := &Cycle{Name: "Cycle 9", StartsAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)}
	c10 := &Cycle{Name: "Cycle 10", StartsAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)}
	// Current+ (Ada) as Linear returned it, all in Cycle 10.
	ada := []Issue{
		viewIssue("ENG-603", "Code Review", "started", 4000, 0, -208134, c10),
		viewIssue("ENG-587", "Code Review", "started", 4000, 0, -192237, c10),
		viewIssue("ENG-537", "In QA", "started", 5000, 0, -186249, c10),
		viewIssue("ENG-486", "Ready", "unstarted", 2000, 2, -154547.88, c10),
		viewIssue("ENG-478", "Code Review", "started", 4000, 0, -174336.43, c10),
		viewIssue("ENG-428", "Ready", "unstarted", 2000, 4, -42549.59, c10),
		viewIssue("ENG-34", "Ready", "unstarted", 2000, 4, 3120.53, c10),
	}
	for _, tc := range []struct {
		name string
		in   []Issue
		want string
	}{
		{"Current+ (Ada) matches Linear's view", ada, "ENG-537 ENG-603 ENG-587 ENG-478 ENG-486 ENG-428 ENG-34"},
		{"cycles by start date, no cycle last",
			[]Issue{viewIssue("A-1", "Todo", "unstarted", 1, 0, 0, nil), viewIssue("A-2", "Todo", "unstarted", 1, 0, 0, c10),
				viewIssue("A-3", "Todo", "unstarted", 1, 0, 0, c9)}, "A-3 A-2 A-1"},
		{"state types in Linear's group order",
			[]Issue{viewIssue("A-1", "Canceled", "canceled", 1, 0, 0, nil), viewIssue("A-2", "Triage", "triage", 1, 0, 0, nil),
				viewIssue("A-3", "Backlog", "backlog", 1, 0, 0, nil), viewIssue("A-4", "Done", "completed", 1, 0, 0, nil),
				viewIssue("A-5", "Todo", "unstarted", 1, 0, 0, nil), viewIssue("A-6", "Doing", "started", 1, 0, 0, nil)},
			"A-6 A-5 A-3 A-2 A-4 A-1"},
		{"urgent first, no priority after low",
			[]Issue{viewIssue("A-1", "Todo", "unstarted", 1, 0, -9, nil), viewIssue("A-2", "Todo", "unstarted", 1, 4, 0, nil),
				viewIssue("A-3", "Todo", "unstarted", 1, 1, 9, nil)}, "A-3 A-2 A-1"},
		{"empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := ids(tc.in)
			if got := ids(ViewOrder(tc.in)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if ids(tc.in) != in {
				t.Error("ViewOrder changed its input")
			}
		})
	}
}

func TestProjectOrder(t *testing.T) {
	p := func(name, status, typ string, pos float64, pri int, pso float64) ProjectInfo {
		return ProjectInfo{ID: name, Name: name, Status: status, StatusType: typ, StatusPosition: pos, Priority: pri, PrioritySortOrder: pso}
	}
	names := func(ps []ProjectInfo) string {
		var out []string
		for _, x := range ps {
			out = append(out, x.Name)
		}
		return strings.Join(out, " ")
	}
	for _, tc := range []struct {
		name string
		in   []ProjectInfo
		want string
	}{
		{"statuses as Linear's view groups them", []ProjectInfo{
			p("idea", "Idea", "backlog", 5016.23, 0, 0),
			p("planned", "Planned", "planned", 1, 0, 0),
			p("design", "Shaping", "backlog", 7076.09, 0, 0),
			p("progress", "In Progress", "started", 4000, 0, 0),
			p("discovery", "Discovery", "backlog", 5976.67, 0, 0),
		}, "progress planned design discovery idea"},
		{"priority then manual order inside a status", []ProjectInfo{
			p("none", "In Progress", "started", 4000, 0, -5),
			p("low-b", "In Progress", "started", 4000, 4, 2),
			p("low-a", "In Progress", "started", 4000, 4, 1),
			p("high", "In Progress", "started", 4000, 2, 9),
		}, "high low-a low-b none"},
		{"paused, completed, canceled last", []ProjectInfo{
			p("x", "Canceled", "canceled", 1, 0, 0), p("y", "Done", "completed", 1, 0, 0),
			p("z", "Paused", "paused", 1, 0, 0), p("w", "Backlog", "backlog", 1, 0, 0),
		}, "w z y x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(ProjectOrder(tc.in)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
