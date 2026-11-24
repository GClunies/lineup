package board

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

const qaPR = "https://github.com/acme/data/pull/7"

// qtv is the QA Task of the merge of ENG-12, in the Stage st.
func qtv(st lifecycle.Stage, note string) scheduler.TaskView {
	t := tv("QA-ENG-12", st, "data", "Add billing model", note, time.Hour)
	t.Kind, t.PRNumber, t.PRURL = lifecycle.KindQA, 7, qaPR
	return t
}

// The Board lists a QA Task in the group of its Stage, with its status word
// and its note (PROD-BOARD-001, PROD-BOARD-002, PROD-BOARD-006).
func TestTheBoardListsAQATaskInTheGroupOfItsStage(t *testing.T) {
	for _, c := range []struct {
		st                lifecycle.Stage
		group, word, note string
	}{
		{lifecycle.QAFailed, "Needs you", "QA failed", "12 properties have two rows"},
		{lifecycle.Verifying, "Working", "verifying", ""},
		{lifecycle.QAWaiting, "Waiting", "QA waiting", "next check Nov 25 03:00: the DAG has not run"},
		{lifecycle.Done, "Done", "done", "every property has one row"},
	} {
		task := qtv(c.st, c.note)
		m := openAll(newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{task}}))
		m.sel = task.ID
		if out := screen(m); headingCount(out, c.group) != 1 {
			t.Errorf("%s: want QA-ENG-12 in %s:\n%s", c.st, c.group, out)
		}
		if l := lineOf(t, screen(m), "QA-ENG-12"); !strings.Contains(l, "  "+c.word+"  ") {
			t.Errorf("%s: the row %q lacks the status word %q", c.st, l, c.word)
		}
		if f := footerAt(m, 120); !strings.Contains(f, "QA-ENG-12 · "+c.st.String()+" · data") || !strings.Contains(f, c.note) {
			t.Errorf("%s: the strip must name the Stage and the note %q:\n%s", c.st, c.note, f)
		}
	}
}

// A QA Task shares the Issue of its build Task. When the Tracker still holds
// that Issue open, only the build Task waits in Follow-up (PROD-BOARD-002).
func TestAQATaskNeverGoesToFollowUp(t *testing.T) {
	for _, st := range []lifecycle.Stage{lifecycle.QAWaiting, lifecycle.Done} {
		v := scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{shipped("ENG-12"), qtv(st, "")}}
		m := feed(newModel(&fakeClient{}, v), mineIssuesMsg{ws: acme, issues: []linear.Issue{waitingForQA("ENG-12")}, live: true})
		out := screen(openAll(m))
		if headingCount(out, "Follow-up") != 1 {
			t.Errorf("%s: want ENG-12 alone in Follow-up:\n%s", st, out)
		}
		inOrder(t, out, "Follow-up 1", "ENG-12 ", groupOf[st]+" 1", "QA-ENG-12")
	}
}

// The mark on the my-lineup tab counts the QA Tasks in Needs you, with the
// build Tasks (PROD-BOARD-018).
func TestTheNeedsYouMarkCountsQATasks(t *testing.T) {
	log := &statusLog{}
	v := fixture()
	v.Tasks = append(v.Tasks, qtv(lifecycle.QAFailed, "12 properties have two rows"))
	m := New(v, Options{Client: &fakeClient{}, Status: log.record})
	m.Init()
	if got := log.take(); !slices.Equal(got, []int{3}) {
		t.Fatalf("Init reported %v, want [3]: 2 build Tasks and 1 QA Task", got)
	}
	v = fixture()
	v.Tasks = append(v.Tasks, qtv(lifecycle.Verifying, ""))
	show(m, v)
	if got := log.take(); !slices.Equal(got, []int{2}) {
		t.Errorf("the QA Task left Needs you, and the Board reported %v, want [2]", got)
	}
}

// A QA Task never toasts, also when it ends, and its end names no free slot
// (PROD-BOARD-019, PROD-BOARD-020).
func TestAQATaskNeverToasts(t *testing.T) {
	var toasts []string
	before := fixture()
	before.Started = 3
	before.Tasks = append(before.Tasks, qtv(lifecycle.Verifying, ""))
	m := New(before, Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
	m = feed(m, mineIssuesMsg{ws: acme, live: true, issues: []linear.Issue{mineIssue("ENG-611", "unstarted", 2)}})
	for _, st := range []lifecycle.Stage{lifecycle.QAFailed, lifecycle.Done} {
		v := fixture()
		v.Started = 3
		qa := qtv(st, "")
		qa.Outcome = map[lifecycle.Stage]string{lifecycle.Done: "passed"}[st]
		v.Tasks = append(v.Tasks, qa)
		m = show(m, v)
	}
	if len(toasts) != 0 {
		t.Errorf("a QA Task toasted %q", toasts)
	}
}

// On a QA row, m does nothing, o opens the PR, and enter, p, f, and x work
// as on any Task (PROD-BOARD-012).
func TestTheKeysOnAQARow(t *testing.T) {
	for _, c := range []struct {
		keys []string
		op   string
	}{
		{[]string{"m"}, ""},
		{[]string{"enter"}, "open"},
		{[]string{"p"}, "capture"},
		{[]string{"f"}, "show"},
		{[]string{"x", "y"}, "close"},
	} {
		f := &fakeClient{}
		m := newModel(f, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{qtv(lifecycle.QAFailed, "12 properties have two rows")}})
		m.sel = "QA-ENG-12"
		_, cmd := press(m, c.keys...)
		if cmd != nil {
			cmd()
		}
		if got := f.ops(); c.op == "" && len(got) != 0 || c.op != "" && !slices.Equal(got, []string{c.op}) {
			t.Errorf("%q on a QA row: ops %v, want %q", c.keys, got, c.op)
		}
	}
	var opened []string
	m := New(scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{qtv(lifecycle.QAFailed, "")}},
		Options{Client: &fakeClient{}, Open: func(u string) error { opened = append(opened, u); return nil }})
	m.sel = "QA-ENG-12"
	if _, cmd := press(m, "o"); cmd != nil {
		cmd()
	}
	if !slices.Equal(opened, []string{qaPR}) {
		t.Errorf("o opened %q, want the PR", opened)
	}
	if hints := footerAt(m, 200); strings.Contains(hints, "m merge") || !strings.Contains(hints, "o open the PR") {
		t.Errorf("the hints must offer o and not m:\n%s", hints)
	}
}
