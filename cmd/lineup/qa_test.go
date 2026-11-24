package main

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

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
