package board

import (
	"slices"
	"strings"
	"testing"
	"testing/quick"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// shipped is a build Task whose PR merged an hour ago.
func shipped(id string) scheduler.TaskView {
	t := tv(id, lifecycle.Done, "data", "Ship the QA build", "#12 merged", time.Hour)
	t.Outcome = "merged"
	return t
}

// shippedBoard is a Board with the one Task shipped(id), before any Issue loads.
func shippedBoard(c Client, id string) Model {
	return newModel(c, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{shipped(id)}})
}

// waitingForQA is a Linear Issue of the user's in the started state "Waiting for QA".
func waitingForQA(id string) linear.Issue {
	is := lissue(id, "Ship the QA build", "Waiting for QA")
	is.State.Type = "started"
	return is
}

func TestADoneTaskShowsInFollowUpUntilItsTrackerClosesTheIssue(t *testing.T) {
	completed := waitingForQA("PD-1")
	completed.State = linear.State{ID: "s9", Name: "Done", Type: "completed"}
	ownIssue := func(n int) github.OwnIssue {
		return github.OwnIssue{Issue: github.Issue{Repo: "octo/sandbox", Number: n, Title: "Ship the QA build", State: "OPEN"}}
	}
	for _, c := range []struct {
		name, id, word, state string
		open, closed          tea.Msg
	}{
		{"Linear, then completed", "PD-1", "QA", "Waiting for QA",
			mineIssuesMsg{ws: acme, issues: []linear.Issue{waitingForQA("PD-1")}, live: true},
			mineIssuesMsg{ws: acme, issues: []linear.Issue{completed}, live: true}},
		{"Linear, then gone from My Issues", "PD-1", "QA", "Waiting for QA",
			mineIssuesMsg{ws: acme, issues: []linear.Issue{waitingForQA("PD-1")}, live: true},
			mineIssuesMsg{ws: acme, live: true}},
		{"GitHub, then gone from the open Issues", "octo/sandbox#12", "open", "open",
			mineGitHubMsg{issues: []github.OwnIssue{ownIssue(12), ownIssue(14)}},
			mineGitHubMsg{issues: []github.OwnIssue{ownIssue(14)}}},
		{"Local, then done", "LOCAL-1", "in review", "in review",
			mineLocalMsg{issues: []localissues.Issue{localIssue(1, localissues.InReview)}},
			mineLocalMsg{issues: []localissues.Issue{localIssue(1, localissues.Done)}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := feed(shippedBoard(&fakeClient{}, c.id), c.open)
			out := screen(m)
			if !strings.Contains(out, "Follow-up 1\n") || hasHeading(out, "Done") {
				t.Fatalf("want %s in an open Follow-up and no Done:\n%s", c.id, out)
			}
			if l := lineOf(t, out, boardID(c.id)); !strings.Contains(l, "  "+c.word+"  ") || strings.Contains(l, "merged") {
				t.Errorf("row %q, want the status %q", l, c.word)
			}
			if f := footerAt(m, 100); !strings.Contains(f, " · "+c.state+"\n") {
				t.Errorf("the strip must name the Tracker state %q:\n%s", c.state, f)
			}
			m = openAll(feed(m, c.closed))
			m.sel = c.id
			out = screen(m)
			if hasHeading(out, "Follow-up") {
				t.Fatalf("Follow-up shows after the Tracker closed the Issue:\n%s", out)
			}
			inOrder(t, out, "Done 1\n", boardID(c.id))
			if l := lineOf(t, out, boardID(c.id)); !strings.Contains(l, "done") {
				t.Errorf("row %q, want the status done", l)
			}
			if f := footerAt(m, 100); !strings.Contains(f, "#12 merged") {
				t.Errorf("the strip must show the note %q:\n%s", "#12 merged", f)
			}
		})
	}
}

func TestEachOpenTrackerStateKeepsADoneTaskInFollowUp(t *testing.T) {
	linearOpen := map[string]bool{"triage": false, "backlog": false, "unstarted": true, "started": true, "completed": false, "canceled": false}
	localOpen := map[localissues.Status]bool{localissues.Backlog: true, localissues.Todo: true, localissues.Started: true,
		localissues.InReview: true, localissues.Done: false, localissues.Canceled: false}
	for _, st := range localissues.Statuses {
		if _, ok := localOpen[st]; !ok {
			t.Fatalf("local status %q has no case", st)
		}
	}
	inFollowUp := func(m Model) bool { return hasHeading(screen(m), "Follow-up") }
	for typ, want := range linearOpen {
		is := waitingForQA("PD-1")
		is.State.Type = typ
		if m := feed(shippedBoard(&fakeClient{}, "PD-1"), mineIssuesMsg{ws: acme, issues: []linear.Issue{is}, live: true}); inFollowUp(m) != want {
			t.Errorf("Linear state type %q: in Follow-up %v, want %v:\n%s", typ, !want, want, screen(m))
		}
	}
	for st, want := range localOpen {
		m := feed(shippedBoard(&fakeClient{}, "LOCAL-1"), mineLocalMsg{issues: []localissues.Issue{localIssue(1, st)}})
		if inFollowUp(m) != want {
			t.Errorf("local status %q: in Follow-up %v, want %v:\n%s", st, !want, want, screen(m))
		}
		if want && !strings.Contains(lineOf(t, screen(m), "LOCAL-1"), string(st)) {
			t.Errorf("local status %q: the row lacks the status:\n%s", st, screen(m))
		}
	}
}

func TestAFollowUpRowAddsNothingToTheNeedsYouCount(t *testing.T) {
	log := &statusLog{}
	m := New(fixture(), Options{Client: &fakeClient{}, Status: log.record})
	m.Init()
	if got := log.take(); !slices.Equal(got, []int{2}) {
		t.Fatalf("Init reported %v, want [2]", got)
	}
	v := fixture()
	v.Tasks = append(v.Tasks, shipped("PD-1"))
	m = feed(m, mineIssuesMsg{ws: acme, issues: []linear.Issue{waitingForQA("PD-1")}, live: true})
	m = show(m, v)
	if out := screen(m); !strings.Contains(out, "Follow-up 1\n") {
		t.Fatalf("PD-1 is not in Follow-up:\n%s", out)
	}
	if got := headingCount(screen(m), "Needs you"); got != 2 {
		t.Errorf("the Needs you heading counts %d, want the 2 Tasks in Needs you and not the Follow-up row", got)
	}
	if got := log.take(); len(got) != 0 {
		t.Errorf("the Board reported %v, want no report, because a Follow-up row does not need the user", got)
	}
}

func TestAFollowUpRowKeepsTheAgeAndTheXOfADoneTask(t *testing.T) {
	for _, c := range []struct {
		name          string
		tab           bool
		hint, ask, op string
	}{
		{"no tab", false, "x archive", "archive PD-1? (y/n)", "archive"},
		{"a tab", true, "x close", "close PD-1's tab and worktree? (y/n)", "close"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeClient{}
			done := shipped("PD-1")
			done.Tab = c.tab
			m := feed(newModel(f, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{done}}),
				mineIssuesMsg{ws: acme, issues: []linear.Issue{waitingForQA("PD-1")}, live: true})
			out := screen(m)
			inOrder(t, out, "Follow-up 1\n", "PD-1")
			if l := lineOf(t, out, "PD-1"); !strings.Contains(l, "today") {
				t.Errorf("row %q, want the age %q of a Task that ended today", l, "today")
			}
			if !strings.Contains(m.footer(), c.hint) {
				t.Fatalf("footer %q, want %q", m.footer(), c.hint)
			}
			m, _ = press(m, "x")
			if !strings.Contains(m.footer(), c.ask) {
				t.Fatalf("footer %q, want %q", m.footer(), c.ask)
			}
			_, cmd := press(m, "y")
			cmd()
			if len(f.calls) != 1 || f.calls[0].Op != c.op || f.calls[0].Task != "PD-1" {
				t.Fatalf("calls %+v, want one %s of PD-1", f.calls, c.op)
			}
		})
	}
}

// A Follow-up row's strip names the Tracker state, then the note of a worktree
// that lineup keeps, so the Board still shows why the worktree stays.
func TestAFollowUpRowKeepsTheWorktreeNoteInTheStrip(t *testing.T) {
	const kept = "worktree kept: uncommitted changes in /wt/PD-1"
	task := shipped("PD-1")
	task.Note, task.WorktreeNote = "#12 merged · "+kept, kept
	m := newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{task}})
	m = feed(m, mineIssuesMsg{ws: acme, issues: []linear.Issue{waitingForQA("PD-1")}, live: true})
	if l := lineOf(t, screen(m), boardID("PD-1")); !strings.Contains(l, "  QA  ") || strings.Contains(l, "worktree") {
		t.Errorf("row %q, want the status word QA alone", l)
	}
	if f := footerAt(m, 120); !strings.Contains(f, " · Waiting for QA · "+kept) {
		t.Errorf("the strip must name the Tracker state, then %q:\n%s", kept, f)
	}
}

// The open states of the user's Linear workspace each show a status word of
// at most 12 cells on a Follow-up row (PROD-BOARD-027).
func TestAFollowUpRowShowsAShortTrackerState(t *testing.T) {
	for state, want := range map[string]string{
		"Waiting for Code Review": "code review",
		"Waiting for QA":          "QA",
		"In Development":          "development",
		"Ready for Grooming":      "grooming",
		"Ready for Dev":           "dev",
		"Not Ready for Dev":       "not ready f…",
		"Blocked":                 "blocked",
		"Ready for QA":            "ready for QA",
	} {
		is := waitingForQA("PD-1")
		is.State.Name = state
		m := feed(shippedBoard(&fakeClient{}, "PD-1"), mineIssuesMsg{ws: acme, issues: []linear.Issue{is}, live: true})
		if l := lineOf(t, screen(m), "PD-1"); !strings.Contains(l, "  "+want+"  ") {
			t.Errorf("state %q: row %q, want the status word %q", state, l, want)
		}
		if f := footerAt(m, 100); !strings.Contains(f, " · "+state+"\n") {
			t.Errorf("state %q: the strip must name the full Tracker state:\n%s", state, f)
		}
	}
}

func TestATrackerWordNeverPassesTwelveCells(t *testing.T) {
	if err := quick.Check(func(state string) bool { return lipgloss.Width(trackerWord(state)) <= 12 }, nil); err != nil {
		t.Error(err)
	}
}

// At 113 columns the Follow-up rows of Issue 162 give a status column of at
// most 12 cells, and the strip of the selected row names its full Tracker
// state (PROD-BOARD-027, PROD-BOARD-007).
func TestFollowUpRowsKeepTheStatusColumnShortAt113Columns(t *testing.T) {
	review := waitingForQA("PD-714")
	review.State.Name = "Waiting for Code Review"
	m := newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{shipped("PD-714"), shipped("PD-645")}})
	m = feed(m, mineIssuesMsg{ws: acme, live: true, issues: []linear.Issue{review, waitingForQA("PD-645")}})
	m.sel = "PD-714"
	out := viewAt(m, 113)
	for id, word := range map[string]string{"PD-714": "code review", "PD-645": "QA"} {
		l := lineOf(t, out, id)
		status := cellOf(l, "  "+word+"  ") + 2
		if width := cellOf(l, "today") - status - 2; status < 2 || width > 12 {
			t.Errorf("the %s row shows a status column of %d cells, want %q in at most 12: %q\n%s", id, width, word, l, out)
		}
	}
	if f := footerAt(m, 113); !strings.Contains(f, " · Waiting for Code Review\n") {
		t.Errorf("the strip must name the full Tracker state:\n%s", f)
	}
}
