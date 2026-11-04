package scheduler

import (
	"slices"
	"testing"
)

func TestAlerts(t *testing.T) {
	working := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Working", StageKey: "working", Group: "Working"}
	ready := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Ready to land", StageKey: "ready_to_land", Group: "Needs you", Note: "#42"}
	question := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "question", Note: "question"}
	failed := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "setup failed", Note: "setup failed: direnv: loading .envrc\nResolved 149 packages"}
	done := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Done", StageKey: "done", Group: "Done", Outcome: "merged", Note: "#42"}

	seen := map[string]string{}
	steps := []struct {
		task TaskView
		want string // "" is no alert
	}{
		{working, ""},
		{ready, "ENG-1 Ready to land, #42: Add billing model"},
		{ready, ""},
		{working, ""},
		{ready, "ENG-1 Ready to land, #42: Add billing model"},
		{question, "ENG-1 Working, question: Add billing model"},
		{failed, "ENG-1 Working, setup failed: direnv: loading .envrc: Add billing model"},
		{done, "ENG-1 Done, merged: Add billing model"},
		{done, ""},
	}
	for i, s := range steps {
		var got []string
		for _, a := range Alerts(seen, BoardView{Tasks: []TaskView{s.task}}) {
			got = append(got, a.AlertText())
		}
		var want []string
		if s.want != "" {
			want = []string{s.want}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("step %d (%s): alerts %q, want %q", i, s.task.Stage, got, want)
		}
	}
}
