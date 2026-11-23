package board

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

// exampleBoard is the Board of the example in Issue 123, 95 columns wide.
// ah-63 waits for the user because its Worker stopped, and ah-64 works.
func exampleBoard() Model {
	stopped := tv("ah-63", lifecycle.Working, "lineup", "setup and brainstorming session", "stopped", 24*time.Hour)
	stopped.Flag, stopped.Group = "stopped", "Needs you"
	m := newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Started: 2, Tasks: []scheduler.TaskView{
		stopped,
		tv("ah-64", lifecycle.Working, "lineup", "Start a sessions to explore the ideas and UI here", "", 5*time.Hour),
	}})
	m.width = 95
	return m
}

// blockedBoard is exampleBoard with two Ready Issues. ENG-901 waits on ENG-900.
func blockedBoard() Model {
	return feed(exampleBoard(), mineIssuesMsg{ws: linear.Workspace{ID: "w1", Name: "Crexi"}, live: true,
		issues: []linear.Issue{mineIssue("ENG-900", "unstarted", 2), mineIssue("ENG-901", "unstarted", 2, "ENG-900")}})
}

func TestEachRowShowsItsStatusWordInItsOwnColumn(t *testing.T) {
	out := screen(exampleBoard())
	stopped, working := lineOf(t, out, "ah-63"), lineOf(t, out, "ah-64")
	at := cellOf(stopped, "stopped")
	if at < 0 || cellOf(working, "working") != at {
		t.Fatalf("each row must show its status word, both at one cell:\n%s", out)
	}
	if title := at - cellOf(working, "Start a sessions") - 2; title < 45 {
		t.Errorf("the title gets %d cells at 95 columns, want at least 45:\n%s", title, out)
	}
	for _, l := range []string{stopped, working} {
		if lipgloss.Width(l) > 95 {
			t.Errorf("the row is %d cells wide at 95 columns: %q", lipgloss.Width(l), l)
		}
	}
}

// statusOf is the status word on the row of task, alone on a Board. The Task
// has no age, no Repo, and no skill, so the word ends the row.
func statusOf(t *testing.T, task scheduler.TaskView) string {
	t.Helper()
	l := strings.TrimSpace(lineOf(t, viewAt(newModel(&fakeClient{}, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{task}}), 100), task.ID))
	fields := regexp.MustCompile(`\s{2,}`).Split(l, -1)
	return fields[len(fields)-1]
}

func TestEveryStageAndFlagHasAStatusOfAtMostTwoWords(t *testing.T) {
	stages := map[lifecycle.Stage]string{
		lifecycle.Queued: "queued", lifecycle.Working: "working", lifecycle.CIRunning: "CI running", lifecycle.CIDebug: "CI debug",
		lifecycle.InReview: "in review", lifecycle.Responding: "responding", lifecycle.ReadyToLand: "ready", lifecycle.Done: "done",
		lifecycle.Reviewing: "reviewing", lifecycle.ReviewReady: "review ready",
	}
	flags := map[lifecycle.Reason]string{
		lifecycle.ReasonQuestion: "question", lifecycle.ReasonPermission: "permission", lifecycle.ReasonAuth: "auth",
		lifecycle.ReasonError: "error", lifecycle.ReasonSetupFailed: "setup failed", lifecycle.ReasonStopped: "stopped",
		lifecycle.ReasonBudgetSpent: "budget spent", lifecycle.ReasonTabClosed: "tab closed", lifecycle.ReasonWorkerSilent: "worker silent",
		lifecycle.ReasonDraft: "draft", lifecycle.ReasonNoBotReview: "bot late", lifecycle.ReasonDrafts: "drafts",
		lifecycle.ReasonPRClosed: "PR closed", "worker lost": "worker lost",
	}
	check := func(name string, task scheduler.TaskView, want string) {
		got := statusOf(t, task)
		if got != want {
			t.Errorf("%s: the row shows %q, want %q", name, got, want)
		}
		if n := len(strings.Fields(got)); n > 2 {
			t.Errorf("%s: %q is %d words, want at most 2", name, got, n)
		}
	}
	for _, st := range lifecycle.Stages() {
		want, ok := stages[st]
		if !ok {
			t.Errorf("the test has no status word for the Stage %s", st)
			continue
		}
		check(st.String(), scheduler.TaskView{ID: "ENG-1", Title: "Job", Stage: st.String(), StageKey: st.Key(), Group: groupOf[st]}, want)
	}
	for r, want := range flags {
		check("flag "+string(r), scheduler.TaskView{ID: "ENG-1", Title: "Job", Stage: lifecycle.Working.String(), StageKey: lifecycle.Working.Key(),
			Group: "Needs you", Flag: string(r), Note: string(r) + ": what happened"}, want)
	}
}

func TestANeedsYouRowShowsItsReasonAndNotItsNote(t *testing.T) {
	v := exampleBoard().board
	closed := tv("ah-65", lifecycle.InReview, "lineup", "Retry failed loads", "pr closed: #5226 closed without a merge", time.Hour)
	closed.Flag, closed.Group, closed.PRNumber = "pr closed", "Needs you", 5226
	v.Tasks = append(v.Tasks, closed)
	m := newModel(&fakeClient{}, v)
	m.width, m.sel = 95, "ah-65"
	out := screen(m)
	if l := lineOf(t, out, "ah-65"); !strings.Contains(l, "PR closed") || strings.Contains(l, "closed without") {
		t.Errorf("the row must show PR closed and not the note: %q", l)
	}
}

func TestABlockedIssueRowShowsBlocked(t *testing.T) {
	out := screen(blockedBoard())
	if l := lineOf(t, out, "ENG-901"); !strings.Contains(l, "blocked") || strings.Contains(l, "blocked by") {
		t.Errorf("the blocked row must show blocked and not its blockers: %q", l)
	}
	if l := lineOf(t, out, "ENG-900"); !strings.Contains(l, "ready") {
		t.Errorf("the Ready row must show ready: %q", l)
	}
}

// A status is a word, so it reads with color off, as NO_COLOR sets it. With
// color on, a row that needs the user shows its word in pink.
func TestAStatusIsAWordWithAndWithoutColor(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })
	words := map[string]string{"ah-63": "stopped", "ah-64": "working", "ENG-900": "ready", "ENG-901": "blocked"}
	const pink = "38;2;243;163;181"
	sgr := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	for _, p := range []termenv.Profile{termenv.Ascii, termenv.TrueColor} {
		lipgloss.SetColorProfile(p)
		raw := blockedBoard().View()
		if colored := strings.Contains(raw, "\x1b["); colored != (p != termenv.Ascii) {
			t.Fatalf("profile %v: the screen has escape codes: %v", p, colored)
		}
		for id, word := range words {
			l := lineOf(t, raw, id)
			at := strings.Index(l, word)
			if at < 0 {
				t.Errorf("profile %v: the %s row lacks %q: %q", p, id, word, l)
				continue
			}
			if p == termenv.Ascii {
				continue
			}
			last := ""
			if codes := sgr.FindAllStringSubmatch(l[:at], -1); len(codes) > 0 {
				last = codes[len(codes)-1][1]
			}
			if alert := id == "ah-63" || id == "ENG-901"; strings.Contains(last, pink) != alert {
				t.Errorf("the %s row shows %q in %q, want pink %v", id, word, last, alert)
			}
		}
	}
}
