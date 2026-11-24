package board

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// statusLog records the counts that the Board sends to Options.Status.
type statusLog struct{ counts []int }

func (l *statusLog) record(n int) { l.counts = append(l.counts, n) }

// take returns the counts recorded since the last call.
func (l *statusLog) take() []int {
	got := l.counts
	l.counts = nil
	return got
}

// show gives the Board a view, as the Scheduler's watch does, and runs the
// command that Update returns.
func show(m Model, v scheduler.BoardView) Model {
	t, cmd := m.Update(boardMsg(v))
	if cmd != nil {
		cmd()
	}
	return t.(Model)
}

// needing is a view with n build Tasks that need the user.
func needing(n int) scheduler.BoardView {
	v := scheduler.BoardView{Cap: 5}
	for i := 1; i <= n; i++ {
		t := tv(fmt.Sprintf("ENG-%d", 700+i), lifecycle.Working, "data", "Backfill events", "question", time.Minute)
		t.Flag, t.Group = "question", "Needs you"
		v.Tasks = append(v.Tasks, t)
	}
	return v
}

func TestStatusWritesABlockedRecordThatNamesTheCount(t *testing.T) {
	const head, tail = "\x1b]7501;state=blocked:kind=question:app=lineup:msg=", "\x1b\\"
	for _, c := range []struct {
		n   int
		msg string
	}{
		{1, "1 Task needs you"},
		{2, "2 Tasks need you"},
		{14, "14 Tasks need you"},
	} {
		var out bytes.Buffer
		if err := Status(&out, c.n); err != nil {
			t.Fatalf("Status(%d): %v", c.n, err)
		}
		body, hasHead := strings.CutPrefix(out.String(), head)
		body, hasTail := strings.CutSuffix(body, tail)
		msg, err := base64.StdEncoding.DecodeString(body)
		if !hasHead || !hasTail || err != nil || string(msg) != c.msg {
			t.Errorf("Status(%d) wrote %q, want %q, then the base64 of %q, then %q", c.n, out.String(), head, c.msg, tail)
		}
	}
}

func TestStatusClearsTheRecordWhenNoTaskNeedsYou(t *testing.T) {
	var out bytes.Buffer
	if err := Status(&out, 0); err != nil {
		t.Fatalf("Status(0): %v", err)
	}
	if got, want := out.String(), "\x1b]7501;state=clear\x1b\\"; got != want {
		t.Errorf("Status(0) wrote %q, want %q", got, want)
	}
}

func TestTheBoardReportsTheBuildTasksThatNeedYouAtStart(t *testing.T) {
	for _, c := range []struct {
		name string
		v    scheduler.BoardView
		want int
	}{
		{"two build Tasks need the user", fixture(), 2},
		{"no Task needs the user", scheduler.BoardView{Cap: 5}, 0},
	} {
		log := &statusLog{}
		m := New(c.v, Options{Client: &fakeClient{}, Status: log.record})
		if got := headingCount(screen(m), "Needs you"); got != c.want {
			t.Fatalf("%s: the Needs you heading counts %d, want the count %d that the Board reports", c.name, got, c.want)
		}
		if got := log.take(); len(got) != 0 {
			t.Fatalf("%s: New reported %v, want the report to wait for Init", c.name, got)
		}
		m.Init()
		if got := log.take(); !slices.Equal(got, []int{c.want}) {
			t.Errorf("%s: Init reported %v, want [%d]", c.name, got, c.want)
		}
	}
}

func TestTheBoardSendsNoReportWhileTheTasksThatNeedYouStayTheSame(t *testing.T) {
	log := &statusLog{}
	m := New(fixture(), Options{Client: &fakeClient{}, Status: log.record})
	m.Init()
	if got := log.take(); !slices.Equal(got, []int{2}) {
		t.Fatalf("Init reported %v, want [2]", got)
	}

	busy := fixture()
	busy.Tasks[0] = tv("ENG-601", lifecycle.Working, "data", "Drop legacy views", "", 0)
	busy.Tasks[2] = tv("ENG-571", lifecycle.CIRunning, "data", "Rate limit login", "#4636 ○ CI running", 0)
	busy.Tasks[5].Note = "question: which index?"
	for _, c := range []struct {
		name string
		v    scheduler.BoardView
	}{
		{"the view that the Board started on", fixture()},
		{"Tasks that need nothing move on, and a note changes", busy},
	} {
		m = show(m, c.v)
		if got := log.take(); len(got) != 0 {
			t.Errorf("%s: the Board reported %v, want no report", c.name, got)
		}
	}
}

func TestTheBoardReportsAChangedReasonWithTheSameCount(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(v *scheduler.BoardView)
	}{
		{"a flag changes", func(v *scheduler.BoardView) { v.Tasks[4].Flag = "error" }},
		{"a Stage changes", func(v *scheduler.BoardView) {
			v.Tasks[5].Stage, v.Tasks[5].StageKey = lifecycle.CIDebug.String(), lifecycle.CIDebug.Key()
		}},
		{"one Task leaves and another joins", func(v *scheduler.BoardView) {
			v.Tasks[4].Flag, v.Tasks[4].Group = "", "Working"
			v.Tasks[2] = tv("ENG-571", lifecycle.ReadyToLand, "data", "Rate limit login", "#4636 ✓ approved · ready to land", 0)
		}},
	} {
		log := &statusLog{}
		m := New(fixture(), Options{Client: &fakeClient{}, Status: log.record})
		v := fixture()
		c.change(&v)
		show(m, v)
		if got := log.take(); !slices.Equal(got, []int{2}) {
			t.Errorf("%s: the Board reported %v, want [2], because a new reason is a new report at the same count", c.name, got)
		}
	}
}

func TestTheBoardReportsZeroWhenNoBuildTaskNeedsYou(t *testing.T) {
	log := &statusLog{}
	m := New(fixture(), Options{Client: &fakeClient{}, Status: log.record})
	quiet := fixture()
	for _, i := range []int{4, 5} {
		quiet.Tasks[i].Flag, quiet.Tasks[i].Group = "", "Working"
	}
	m = show(m, quiet)
	m = show(m, quiet)
	show(m, fixture())
	if got, want := log.take(), []int{0, 2}; !slices.Equal(got, want) {
		t.Errorf("the Board reported %v, want %v: 0 once, when the last Task stops needing the user, then 2 when two need the user again", got, want)
	}
}

func TestTheBoardDoesNotReportTheReviewTasksThatNeedYou(t *testing.T) {
	log := &statusLog{}
	asking := rtv("R-2", lifecycle.Reviewing, "data#8 Fix the map", urlBase, time.Hour)
	asking.Flag, asking.Group = "question", "Needs you"
	v := fixture()
	v.Tasks = append(v.Tasks, rtv("R-1", lifecycle.ReviewReady, "data#7 Fix the tooltip", urlBase, 0), asking)
	m := New(v, Options{Client: &fakeClient{}, Status: log.record})
	m.online = true
	if h := ansi.Strip(m.header()); !strings.Contains(h, "+2 in reviews") || headingCount(screen(m), "Needs you") != 2 {
		t.Fatalf("header %q, want 2 review Tasks that need the user, and the Needs you heading 2 build Tasks:\n%s", h, screen(m))
	}
	m.Init()
	if got := log.take(); !slices.Equal(got, []int{2}) {
		t.Errorf("Init reported %v, want [2], because a review Task does not count", got)
	}

	next := fixture()
	next.Tasks = append(next.Tasks, rtv("R-1", lifecycle.Done, "data#7 Fix the tooltip", urlBase, 0), asking,
		rtv("R-3", lifecycle.ReviewReady, "data#9 Fix the legend", urlBase, 0))
	show(m, next)
	if got := log.take(); len(got) != 0 {
		t.Errorf("a review Task joined Needs you and another left it, and the Board reported %v", got)
	}
}

// Bubbletea runs each command on its own goroutine, so a report that waited in
// a command could reach the terminal after a later one, and the record would
// keep the older count. This test never runs a command, so each report must
// come from Update itself.
func TestTheBoardReportsInTheOrderOfTheViews(t *testing.T) {
	log := &statusLog{}
	m := New(scheduler.BoardView{Cap: 5}, Options{Client: &fakeClient{}, Status: log.record})
	for _, n := range []int{1, 3, 0, 2, 2, 5} {
		m = feed(m, boardMsg(needing(n)))
	}
	if got, want := log.take(), []int{1, 3, 0, 2, 5}; !slices.Equal(got, want) {
		t.Errorf("the Board reported %v, want %v", got, want)
	}
}

func TestABoardWithoutAHookSkipsThatReport(t *testing.T) {
	merged := tv("ENG-571", lifecycle.Done, "data", "Rate limit login", "#4636 merged", 0)
	merged.Outcome = "merged"
	both := fixture()
	both.Tasks[2] = merged
	both.Tasks[4].Flag = "error"

	var toasts []string
	noStatus := New(fixture(), Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }})
	noStatus.Init()
	show(noStatus, both)
	if want := []string{"ENG-571 #4636 merged | Rate limit login"}; !slices.Equal(toasts, want) {
		t.Errorf("with no Status, toasts %q, want %q", toasts, want)
	}

	log := &statusLog{}
	noNotify := New(fixture(), Options{Client: &fakeClient{}, Status: log.record})
	show(noNotify, both)
	if got := log.take(); !slices.Equal(got, []int{2}) {
		t.Errorf("with no Notify, the Board reported %v, want [2]", got)
	}
}

// Rex titles a toast's banner with the OSC 777 title, then names the session,
// my-lineup. An empty title keeps lineup to that one name (PROD-BOARD-026).
func TestToastNamesLineupOnce(t *testing.T) {
	var out bytes.Buffer
	text := "1 slot free, next ENG-611: Move the retry job"
	if err := Toast(&out, text); err != nil {
		t.Fatalf("Toast: %v", err)
	}
	if got, want := out.String(), "\x1b]777;notify;;"+text+"\x07"; got != want {
		t.Errorf("Toast wrote %q, want %q", got, want)
	}
}
