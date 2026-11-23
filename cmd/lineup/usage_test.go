package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

// spent is the usage of ah-64 on 22 Nov 2026.
var spent = scheduler.Usage{Input: 268, Output: 83615, CacheWrite: 634187, CacheRead: 22427257, AgentSeconds: 18752}

func TestLineupShowPrintsTheUsage(t *testing.T) {
	u := spent
	data, err := json.Marshal(scheduler.TaskDetail{TaskView: scheduler.TaskView{ID: "ah-64", Repo: "lineup", Stage: "Working", Title: "Explore the ideas", Usage: &u}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"show": {OK: true, Data: data}})
	var out bytes.Buffer
	if err := cmdShow(c, []string{"ah-64"}, &out); err != nil {
		t.Fatal(err)
	}
	want := "ah-64  Explore the ideas\nWorking in lineup\n" +
		"tokens  output 83.6k · input 268 · cache write 634k · cache read 22.4M\nagent time  5h 12m\n"
	if out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

func TestLineupLsJSONPrintsEachTasksUsage(t *testing.T) {
	u := spent
	board := scheduler.BoardView{Tasks: []scheduler.TaskView{{ID: "ah-64", Usage: &u}, {ID: "ah-65"}}}
	c, got := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, board)})
	var out bytes.Buffer
	if err := cmdLs(c, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || !reqs[0].Usage {
		t.Errorf("requests %+v, want one ls with usage", reqs)
	}
	for _, want := range []string{
		`"id":"ah-64",`, `"usage":{"input_tokens":268,"output_tokens":83615,"cache_write_tokens":634187,"cache_read_tokens":22427257,"agent_seconds":18752}`,
		`"id":"ah-65",`, `"usage":null`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("lineup ls --json printed %s, want %s in it", out.String(), want)
		}
	}
}

// Hooks run lineup ready and lineup decide on each Orchestrator turn, so
// they never ask the Scheduler to read transcripts.
func TestOnlyLineupLsJSONAsksForUsage(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(scheduler.Client, []string, io.Writer) error
		args []string
	}{
		{"ls", cmdLs, nil},
		{"ready", cmdReady, nil},
		{"decide", cmdDecide, nil},
		{"decide --json", cmdDecide, []string{"--json"}},
	} {
		sched, got := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, scheduler.BoardView{})})
		c.run(sched, c.args, io.Discard)
		lists := 0
		for _, r := range got() {
			if r.Op == "ls" {
				lists++
				if r.Usage {
					t.Errorf("lineup %s asked for usage", c.name)
				}
			}
		}
		if lists == 0 {
			t.Errorf("lineup %s sent no ls", c.name)
		}
	}
}
