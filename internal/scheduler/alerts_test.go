package scheduler

import (
	"slices"
	"testing"
)

func TestAlerts(t *testing.T) {
	working := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Working", StageKey: "working", Group: "Working"}
	ready := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Ready to land", StageKey: "ready_to_land", Group: "Needs you", Note: "#42 ✓ approved · ready to land"}
	question := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "question", Note: "question"}
	failed := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: "setup failed", Note: "setup failed: direnv: loading .envrc\nResolved 149 packages"}
	done := TaskView{ID: "ENG-1", Title: "Add billing model", Stage: "Done", StageKey: "done", Group: "Done", Outcome: "merged", Note: "#42 merged"}

	seen := map[string]string{}
	steps := []struct {
		task TaskView
		want string // "" is no alert
	}{
		{working, ""},
		{ready, "ENG-1 Add billing model · #42 ✓ approved · ready to land"},
		{ready, ""},
		{working, ""},
		{ready, "ENG-1 Add billing model · #42 ✓ approved · ready to land"},
		{question, "ENG-1 Add billing model · question"},
		{failed, "ENG-1 Add billing model · setup failed: direnv: loading .envrc"},
		{done, "ENG-1 Add billing model · #42 merged"},
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

func TestAlertText(t *testing.T) {
	base := TaskView{ID: "ENG-1", Title: "Add billing model"}
	with := func(f func(*TaskView)) TaskView { t := base; f(&t); return t }
	cases := []struct {
		name string
		task TaskView
		want string
	}{
		{"ready to land", with(func(v *TaskView) { v.Stage, v.Note = "Ready to land", "#42 ✓ approved · ready to land" }), "ENG-1 Add billing model · #42 ✓ approved · ready to land"},
		{"review ready", with(func(v *TaskView) { v.Stage, v.Note = "Review ready", "#9 · review ready · head moved" }), "ENG-1 Add billing model · #9 · review ready · head moved"},
		{"drafts with detail", with(func(v *TaskView) { v.Stage, v.Flag, v.Note = "Responding", "drafts", "drafts: 2 replies to approve" }), "ENG-1 Add billing model · drafts: 2 replies to approve"},
		{"tab closed", with(func(v *TaskView) { v.Stage, v.Flag, v.Note = "Working", "tab closed", "tab closed" }), "ENG-1 Add billing model · tab closed"},
		{"flag detail keeps the first line", with(func(v *TaskView) {
			v.Stage, v.Note = "Working", "setup failed: direnv: loading .envrc\nResolved 149 packages"
		}), "ENG-1 Add billing model · setup failed: direnv: loading .envrc"},
		{"done merged", with(func(v *TaskView) { v.Stage, v.Outcome, v.Note = "Done", "merged", "#42 merged" }), "ENG-1 Add billing model · #42 merged"},
		{"done closed", with(func(v *TaskView) { v.Stage, v.Outcome, v.Note = "Done", "closed", "#42 closed" }), "ENG-1 Add billing model · #42 closed"},
		{"done skipped", with(func(v *TaskView) { v.Stage, v.Outcome, v.Note = "Done", "skipped", "#9 skipped" }), "ENG-1 Add billing model · #9 skipped"},
		{"done reviewed", with(func(v *TaskView) { v.Stage, v.Outcome, v.Note = "Done", "reviewed", "#9 reviewed" }), "ENG-1 Add billing model · #9 reviewed"},
		{"done with no PR", with(func(v *TaskView) { v.Stage, v.Outcome, v.Note = "Done", "closed", "closed" }), "ENG-1 Add billing model · closed"},
		{"no note falls back to the outcome", with(func(v *TaskView) { v.Stage, v.Outcome = "Done", "merged" }), "ENG-1 Add billing model · merged"},
		{"no note or outcome falls back to the stage", with(func(v *TaskView) { v.Stage = "Ready to land" }), "ENG-1 Add billing model · Ready to land"},
	}
	for _, c := range cases {
		if got := c.task.AlertText(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
