package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

// The Board shows a plain title, and ls --json keeps the stored one.
func TestLsJSONPrintsTheStoredTitle(t *testing.T) {
	const stored = "Start a sessions to explore the ideas and UI here:\n- https:/"
	v := scheduler.BoardView{Tasks: []scheduler.TaskView{{ID: "ah-64", Title: stored}}}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, v)})
	var out bytes.Buffer
	if err := cmdLs(c, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got scheduler.BoardView
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("ls --json printed %q: %v", out.String(), err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Title != stored {
		t.Errorf("ls --json printed %q, want the title %q", out.String(), stored)
	}
}
