package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// The QA Worker reports each check with lineup qa: a wait, a pass, or a fail
// (PROD-QA-005).
func TestLineupQASendsTheVerdict(t *testing.T) {
	for _, c := range []struct {
		args []string
		want scheduler.Request
	}{
		{[]string{"QA-ENG-12", "wait", "03:00", "the DAG has not run"},
			scheduler.Request{Op: "qa", Task: "QA-ENG-12", Key: "wait", Value: "03:00", Text: "the DAG has not run"}},
		{[]string{"QA-ENG-12", "pass", "core.dim_land has one row for each property"},
			scheduler.Request{Op: "qa", Task: "QA-ENG-12", Key: "pass", Text: "core.dim_land has one row for each property"}},
		{[]string{"QA-ENG-12", "fail", "12 properties have two rows"},
			scheduler.Request{Op: "qa", Task: "QA-ENG-12", Key: "fail", Text: "12 properties have two rows"}},
	} {
		sched, got := fakeScheduler(t, map[string]scheduler.Response{"qa": {OK: true}})
		var out bytes.Buffer
		if err := clientCommands["qa"](sched, c.args, &out); err != nil {
			t.Fatalf("lineup qa %q: %v", c.args, err)
		}
		if reqs := got(); len(reqs) != 1 || !reflect.DeepEqual(reqs[0], c.want) {
			t.Errorf("lineup qa %q sent %+v, want %+v", c.args, reqs, c.want)
		}
	}
}

func TestLineupQARefusesAnotherShape(t *testing.T) {
	sched, got := fakeScheduler(t, nil)
	for _, args := range [][]string{
		nil, {"QA-ENG-12"}, {"QA-ENG-12", "pass"}, {"QA-ENG-12", "wait", "2h"}, {"QA-ENG-12", "pass", "ok", "more"},
		{"QA-ENG-12", "wait", "2h", "why", "more"}, {"QA-ENG-12", "skip", "why"}, {"QA-ENG-12", "pass", "2h", "why"},
	} {
		if err := cmdQA(sched, args, &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("lineup qa %q: err = %v, want usage", args, err)
		}
	}
	if reqs := got(); len(reqs) != 0 {
		t.Fatalf("requests = %+v, want none", reqs)
	}
}

func TestLineupQAReportsTheSchedulersRefusal(t *testing.T) {
	sched, _ := fakeScheduler(t, map[string]scheduler.Response{"qa": {Error: "03:00 is not in the future"}})
	if err := cmdQA(sched, []string{"QA-ENG-12", "wait", "03:00", "the DAG has not run"}, &bytes.Buffer{}); err == nil || err.Error() != "03:00 is not in the future" {
		t.Fatalf("err = %v", err)
	}
}

// waitingQA is a QA Task in QA waiting, as the Scheduler lists it.
func waitingQA() scheduler.TaskView {
	return scheduler.TaskView{ID: "QA-ENG-12", Kind: lifecycle.KindQA, Title: "Add billing model", Repo: "data",
		Stage: lifecycle.QAWaiting.String(), StageKey: lifecycle.QAWaiting.Key(), Group: lifecycle.GroupWaiting.String(),
		Note: "next check Nov 25 03:00: the DAG has not run", PRNumber: 7, PRURL: "https://github.com/acme/data/pull/7",
		IssueURL: "https://linear.app/acme/issue/ENG-12", Worktree: "/repos/data.qa-pr-7",
		NextCheck: time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC), Deadline: time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)}
}

// lineup ls lists a QA Task with its group, its Stage, and its next check,
// and ls --json gives its next_check and its deadline (PROD-QA-012).
func TestLineupLsListsAQATask(t *testing.T) {
	build := scheduler.TaskView{ID: "ENG-12", Kind: lifecycle.KindBuild, Title: "Add billing model", Repo: "data",
		Stage: "Done", StageKey: "done", Group: "Done", Note: "#7 merged"}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{build, waitingQA()}})})
	var out bytes.Buffer
	if err := cmdLs(c, nil, &out); err != nil {
		t.Fatal(err)
	}
	want := "WIP 0/5\n" +
		"ENG-12     Done     Done        data  Add billing model  #7 merged\n" +
		"QA-ENG-12  Waiting  QA waiting  data  Add billing model  next check Nov 25 03:00: the DAG has not run\n"
	if out.String() != want {
		t.Errorf("lineup ls printed\n%s\nwant\n%s", out.String(), want)
	}
	out.Reset()
	if err := cmdLs(c, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"kind":"qa"`, `"next_check":"2026-11-25T11:00:00Z"`, `"deadline":"2026-11-26T18:00:00Z"`} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("lineup ls --json lacks %s:\n%s", field, out.String())
		}
	}
}

// lineup show names a QA Task's Stage, its note, its Issue, its PR, and its
// worktree (PROD-QA-012).
func TestLineupShowShowsAQATask(t *testing.T) {
	data, err := json.Marshal(scheduler.TaskDetail{TaskView: waitingQA()})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"show": {OK: true, Data: data}})
	var out bytes.Buffer
	if err := cmdShow(c, []string{"QA-ENG-12"}, &out); err != nil {
		t.Fatal(err)
	}
	want := "QA-ENG-12  Add billing model\nQA waiting in data\nnext check Nov 25 03:00: the DAG has not run\n" +
		"https://linear.app/acme/issue/ENG-12\nhttps://github.com/acme/data/pull/7\n/repos/data.qa-pr-7\nusage  unknown\n"
	if out.String() != want {
		t.Errorf("lineup show printed\n%s\nwant\n%s", out.String(), want)
	}
}
