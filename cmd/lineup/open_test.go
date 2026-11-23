package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

func openReply(t *testing.T, action scheduler.OpenAction) scheduler.Response {
	data, err := json.Marshal(scheduler.OpenResult{TaskView: scheduler.TaskView{ID: "AH-1", Title: "Add billing model"}, Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler.Response{OK: true, Data: data}
}

func TestLineupOpenSendsOpen(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"open": openReply(t, scheduler.OpenRestored)})
	var out bytes.Buffer
	if err := cmdOpen(c, []string{"AH-1"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "open" || reqs[0].Task != "AH-1" {
		t.Fatalf("requests = %+v, want open AH-1 alone", reqs)
	}
	if want := "AH-1 restored: Add billing model\n"; out.String() != want {
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
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"open": {Error: "AH-1 is Done and its worktree is gone"}})
	err := cmdOpen(c, []string{"AH-1"}, &bytes.Buffer{})
	if err == nil || err.Error() != "AH-1 is Done and its worktree is gone" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenIsAClientCommand(t *testing.T) {
	if _, ok := clientCommands["open"]; !ok {
		t.Fatal("lineup open is not a client command")
	}
}

func TestLineupAskDraftsSendsTheCount(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"ask": {OK: true}})
	if err := cmdAsk(c, []string{"--drafts", "2", "ENG-1"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "ask" || reqs[0].Task != "ENG-1" || reqs[0].N != 2 || reqs[0].Text != "" {
		t.Fatalf("requests = %+v, want ask ENG-1 with 2 drafts", reqs)
	}
	for _, args := range [][]string{{"--drafts", "0", "ENG-1"}, {"--drafts", "2", "ENG-1", "why?"}, {"ENG-1"}} {
		if err := cmdAsk(c, args, &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("ask %v: err = %v, want usage", args, err)
		}
	}
}
