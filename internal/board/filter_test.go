package board

import (
	"os"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"
)

// typeText sends s as one run of runes, as a terminal sends fast input.
func (e *workEnv) typeText(s string) {
	t, cmd := e.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	e.m = drain(t.(Model), cmd)
}

// screenIDs lists the Task and Issue IDs on the screen, top to bottom.
func screenIDs(m Model, known []string) []string {
	var ids []string
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "▸"))
		if len(f) > 0 && slices.Contains(known, f[0]) {
			ids = append(ids, f[0])
		}
	}
	return ids
}

// rowLabels are the work view's rows as the filter matches them, by ID.
func rowLabels(m Model) (ids, labels []string) {
	for _, t := range m.tasks {
		ids, labels = append(ids, t.ID), append(labels, t.ID+"  "+t.Title)
	}
	for _, r := range m.issues {
		ids, labels = append(ids, r.ID), append(labels, r.ID+"  "+r.Title)
	}
	return ids, labels
}

func TestTheFilterShowsAMatchFromAClosedGroupAndEscRestoresTheRows(t *testing.T) {
	e := groupEnv(t)
	before := screen(e.m)
	if !strings.Contains(before, "BACKLOG  1 …") || strings.Contains(before, "ENG-20 ") {
		t.Fatalf("Backlog does not start closed:\n%s", before)
	}
	e.press("/", "eng-20")
	v := screen(e.m)
	if e.m.mode != modeFilter || !strings.Contains(v, "/ eng-20") {
		t.Fatalf("mode %d, want the filter line:\n%s", e.m.mode, v)
	}
	if !strings.Contains(v, "BACKLOG  1\n") || !strings.Contains(v, "ENG-20 ") || strings.Contains(v, "ENG-9 ") || strings.Contains(v, "READY") {
		t.Fatalf("the filter does not show just ENG-20 in an open Backlog:\n%s", v)
	}
	e.press("esc")
	if after := screen(e.m); after != before || e.m.mode != modeNormal {
		t.Fatalf("esc did not restore the rows, mode %d:\n%s\nwant\n%s", e.m.mode, after, before)
	}
}

func TestEveryGroupWithAMatchOpensAndItsHeadingCountsTheMatches(t *testing.T) {
	e := groupEnv(t)
	e.press("/", "rename")
	v := screen(e.m)
	inOrder(t, v, "READY  1\n", "local-1", "DONE  1\n", "ENG-560")
	for _, gone := range []string{"ENG-9 ", "ENG-588", "WORKING", "BACKLOG"} {
		if strings.Contains(v, gone) {
			t.Fatalf("the filter shows %q:\n%s", gone, v)
		}
	}
	if got := e.m.rowIDs(); !slices.Equal(got, []string{"local-1", "ENG-560"}) {
		t.Fatalf("j and k stop at %v, want just the two matches", got)
	}
}

func TestEnterKeepsTheFilterAndTheKeysWorkOnTheMatches(t *testing.T) {
	e := groupEnv(t)
	e.press("/", "eng-20", "enter")
	if e.m.mode != modeNormal || e.m.sel != "ENG-20" {
		t.Fatalf("mode %d sel %q, want the keys back on ENG-20", e.m.mode, e.m.sel)
	}
	v := screen(e.m)
	if !strings.Contains(v, "/ eng-20") || strings.Contains(v, "ENG-9 ") {
		t.Fatalf("enter dropped the filter:\n%s", v)
	}
	if f := footerAt(e.m, 200); !strings.Contains(f, "esc clear the filter") || !strings.Contains(f, "s start") {
		t.Fatalf("footer %q, want the row's keys and esc clear the filter", f)
	}
	e.press("s")
	if e.m.mode != modeSkill || e.m.pick.ID != "ENG-20" {
		t.Fatalf("s under the filter: mode %d pick %q", e.m.mode, e.m.pick.ID)
	}
	e.press("esc", "esc")
	if v := screen(e.m); strings.Contains(v, "/ eng-20") || !strings.Contains(v, "ENG-9 ") {
		t.Fatalf("esc did not clear the filter:\n%s", v)
	}
}

func TestDigitsAndLettersInTheFilterStayText(t *testing.T) {
	e := groupEnv(t)
	e.press("/", "1", "2", "q", "s", "j")
	if e.m.view != viewBoard || e.m.mode != modeFilter || e.m.filter.Value() != "12qsj" {
		t.Fatalf("view %d mode %d filter %q, want the work view and 12qsj", e.m.view, e.m.mode, e.m.filter.Value())
	}
	if len(e.sched.calls) != 0 {
		t.Fatalf("typing ran ops %v", e.sched.ops())
	}
}

// A terminal can send fast input as one run of runes. A run that spells a
// key name, for example "up" or "esc", is still text.
func TestARunOfRunesThatSpellsAKeyNameStaysText(t *testing.T) {
	e := groupEnv(t)
	e.press("/")
	for _, run := range []string{"up", "down", "esc", "enter", "tab"} {
		e.typeText(run)
	}
	if e.m.mode != modeFilter || e.m.filter.Value() != "updownescentertab" {
		t.Fatalf("mode %d filter %q, want the filter open with updownescentertab", e.m.mode, e.m.filter.Value())
	}
}

func TestTheFilterIsNotSaved(t *testing.T) {
	e := groupEnv(t)
	e.press("/", "rename", "enter", "esc")
	if _, err := os.Stat(e.m.opts.Groups); !os.IsNotExist(err) {
		t.Fatalf("the filter wrote groups.json: %v", err)
	}
	fresh := groupEnv(t)
	if fresh.m.filter.Value() != "" {
		t.Fatalf("a new Board starts with the filter %q", fresh.m.filter.Value())
	}
}

func TestTheReviewViewKeepsItsOwnSearch(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.press("/")
	if e.m.mode == modeFilter || e.m.rv.filtering {
		t.Fatalf("/ on the Reviews tab started a filter: mode %d, review search %v", e.m.mode, e.m.rv.filtering)
	}
	e.press("tab", "/", "logs")
	if e.m.mode == modeFilter || !e.m.rv.filtering || e.m.rv.filter.Value() != "logs" {
		t.Fatalf("mode %d, review search %v %q, want the review search", e.m.mode, e.m.rv.filtering, e.m.rv.filter.Value())
	}
}

// The filter shows exactly the rows that match, and esc gives back the
// screen as it was.
func TestTheFilterNeverShowsARowThatDoesNotMatchAndEscRestoresTheScreen(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		e := groupEnv(t)
		q := rapid.StringMatching(`[a-z0-9-][a-z0-9 -]{0,4}`).Draw(rt, "q")
		before := screen(e.m)
		ids, labels := rowLabels(e.m)
		var want []string
		for _, i := range rank(strings.TrimSpace(q), labels) {
			want = append(want, ids[i])
		}
		e.press("/")
		e.typeText(q)
		if rapid.Bool().Draw(rt, "enter first") {
			e.press("enter")
		}
		got := screenIDs(e.m, ids)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			rt.Fatalf("filter %q shows %v, want %v:\n%s", q, got, want, screen(e.m))
		}
		e.press("esc")
		if after := screen(e.m); after != before {
			rt.Fatalf("esc after %q gave\n%s\nwant\n%s", q, after, before)
		}
	})
}
