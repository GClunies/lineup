package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

func openReply(t *testing.T, action scheduler.OpenAction) scheduler.Response {
	data, err := json.Marshal(scheduler.OpenResult{TaskView: scheduler.TaskView{ID: "adhoc-1", Title: "Add billing model"}, Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler.Response{OK: true, Data: data}
}

func TestLineupOpenSendsOpen(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"open": openReply(t, scheduler.OpenRestored)})
	var out bytes.Buffer
	if err := cmdOpen(c, []string{"adhoc-1"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "open" || reqs[0].Task != "adhoc-1" {
		t.Fatalf("requests = %+v, want open adhoc-1 alone", reqs)
	}
	if want := "adhoc-1 restored: Add billing model\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

func TestLineupOpenNeedsOneTask(t *testing.T) {
	c, got := fakeScheduler(t, nil)
	for _, args := range [][]string{nil, {"a", "b"}} {
		if err := cmdOpen(c, args, &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("open %v: err = %v, want usage", args, err)
		}
	}
	if reqs := got(); len(reqs) != 0 {
		t.Fatalf("requests = %+v, want none", reqs)
	}
}

func TestLineupOpenReportsTheSchedulersRefusal(t *testing.T) {
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"open": {Error: "adhoc-1 is Done and its worktree is gone"}})
	err := cmdOpen(c, []string{"adhoc-1"}, &bytes.Buffer{})
	if err == nil || err.Error() != "adhoc-1 is Done and its worktree is gone" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenIsAClientCommand(t *testing.T) {
	if _, ok := clientCommands["open"]; !ok {
		t.Fatal("lineup open is not a client command")
	}
}
