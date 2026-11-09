package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// feed is a Scheduler connection that sends its views, then ends.
type feed []scheduler.BoardView

func (f feed) Watch(fn func(scheduler.BoardView) bool) error {
	for _, v := range f {
		if !fn(v) {
			return nil
		}
	}
	return nil
}

func view(tasks ...scheduler.TaskView) scheduler.BoardView { return scheduler.BoardView{Tasks: tasks} }

func TestWatchPrintsEachAlertOnceAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerted.json")
	old := scheduler.TaskView{ID: "B-1", Title: "old", Stage: "Done", StageKey: "done", Group: "Done", Outcome: "closed"}
	working := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Working", StageKey: "working", Group: "Working"}
	review := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "question", Note: "question"}
	ended := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Done", StageKey: "done", Group: "Done", Outcome: "closed"}

	var out strings.Builder
	if _, err := watchOnce(feed{view(old, working), view(old, review)}, path, false, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "ENG-1 Working, question: billing\n" {
		t.Fatalf("first run printed %q", got)
	}

	out.Reset()
	if _, err := watchOnce(feed{view(old, review), view(old, ended)}, path, false, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "ENG-1 Done, closed: billing\n" {
		t.Fatalf("after a restart, printed %q", got)
	}
}

// The review tab's board toasts review alerts, so the Orchestrator never
// hears of them.
func TestWatchSkipsReviewTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerted.json")
	reviewing := scheduler.TaskView{ID: "R-1", Kind: lifecycle.KindReview, Title: "data#7", Stage: "Reviewing", StageKey: "reviewing", Group: "Working"}
	ready := scheduler.TaskView{ID: "R-1", Kind: lifecycle.KindReview, Title: "data#7", Stage: "Review ready", StageKey: "review_ready", Group: "Needs you"}
	working := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Working", StageKey: "working", Group: "Working"}
	asked := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "question", Note: "question"}

	var out strings.Builder
	if _, err := watchOnce(feed{view(reviewing, working), view(ready, asked)}, path, false, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "ENG-1 Working, question: billing\n" {
		t.Fatalf("printed %q, want the build alert alone", got)
	}
}

// The Orchestrator waits on --once in the background, so each run must end at
// the first alerts and leave the rest for the next run.
func TestWatchOnceEndsAtTheFirstAlertsAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerted.json")
	working := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Working", StageKey: "working", Group: "Working"}
	asked := scheduler.TaskView{ID: "ENG-1", Title: "billing", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "question", Note: "question"}
	other := scheduler.TaskView{ID: "ENG-2", Title: "search", Stage: "Working", StageKey: "working", Group: "Working"}
	stopped := scheduler.TaskView{ID: "ENG-2", Title: "search", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "stopped", Note: "stopped"}

	var out strings.Builder
	printed, err := watchOnce(feed{view(working, other), view(asked, other), view(asked, stopped)}, path, true, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !printed || got != "ENG-1 Working, question: billing\n" {
		t.Fatalf("first run printed %q (printed=%v), want ENG-1 alone", got, printed)
	}

	out.Reset()
	printed, err = watchOnce(feed{view(asked, stopped)}, path, true, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !printed || got != "ENG-2 Working, stopped: search\n" {
		t.Fatalf("second run printed %q (printed=%v), want ENG-2 alone", got, printed)
	}

	out.Reset()
	printed, err = watchOnce(feed{view(asked, stopped)}, path, true, &out)
	if err != nil || printed || out.Len() != 0 {
		t.Fatalf("third run printed %q (printed=%v, err=%v), want nothing", out.String(), printed, err)
	}
}
