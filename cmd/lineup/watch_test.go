package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

// feed is a Scheduler connection that sends its views, then ends.
type feed []scheduler.BoardView

func (f feed) Watch(fn func(scheduler.BoardView)) error {
	for _, v := range f {
		fn(v)
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
	if err := watchOnce(feed{view(old, working), view(old, review)}, path, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "ENG-1 Working, question: billing\n" {
		t.Fatalf("first run printed %q", got)
	}

	out.Reset()
	if err := watchOnce(feed{view(old, review), view(old, ended)}, path, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "ENG-1 Done, closed: billing\n" {
		t.Fatalf("after a restart, printed %q", got)
	}
}
