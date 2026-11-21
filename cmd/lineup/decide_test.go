package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

func needing(id string, flag lifecycle.Reason) scheduler.TaskView {
	return scheduler.TaskView{ID: id, Kind: lifecycle.KindBuild, Title: id + " title", Stage: "Working", StageKey: "working", Group: "Needs you", Flag: string(flag), Note: string(flag)}
}

func dataReply(t *testing.T, x any) scheduler.Response {
	t.Helper()
	data, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler.Response{OK: true, Data: data}
}

func decideBoard() scheduler.BoardView {
	landing := needing("ENG-1", "")
	landing.Stage, landing.StageKey, landing.Note = "Ready to land", "ready_to_land", "#41 ✓ approved · ready to land"
	spent := needing("ENG-5", lifecycle.ReasonBudgetSpent)
	spent.Stage, spent.StageKey = "CI debug", "ci_debug"
	working := needing("ENG-10", "")
	working.Group = "Working"
	done := needing("ENG-11", "")
	done.Stage, done.StageKey, done.Group = "Done", "done", "Done"
	review := needing("R-1", "")
	review.Kind, review.Stage, review.StageKey, review.Note = lifecycle.KindReview, "Review ready", "review_ready", "#7 review ready"
	asked := needing("R-2", lifecycle.ReasonQuestion)
	asked.Kind, asked.Stage, asked.StageKey, asked.Live = lifecycle.KindReview, "Review ready", "review_ready", true
	lost := needing("R-3", lifecycle.ReasonTabClosed)
	lost.Kind, lost.Stage, lost.StageKey = lifecycle.KindReview, "Reviewing", "reviewing"
	reviewing := needing("R-4", "")
	reviewing.Kind, reviewing.Stage, reviewing.StageKey, reviewing.Group = lifecycle.KindReview, "Reviewing", "reviewing", "Working"
	question := needing("ENG-3", lifecycle.ReasonQuestion)
	question.Live = true
	return scheduler.BoardView{Tasks: []scheduler.TaskView{
		landing,
		needing("ENG-2", lifecycle.ReasonTabClosed),
		question,
		needing("ENG-12", lifecycle.ReasonQuestion),
		needing("ENG-4", lifecycle.ReasonStopped),
		spent,
		needing("ENG-6", lifecycle.ReasonPRClosed),
		needing("ENG-7", lifecycle.ReasonSetupFailed),
		needing("ENG-8", lifecycle.ReasonPermission),
		needing("ENG-9", lifecycle.ReasonDrafts),
		working, done, review, asked, lost, reviewing,
	}}
}

type listed struct {
	Tasks []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Note    string `json:"note"`
		Choices []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			TakesText bool   `json:"takes_text"`
		} `json:"choices"`
	} `json:"tasks"`
}

func listDecisions(t *testing.T, v scheduler.BoardView) listed {
	t.Helper()
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, v)})
	var out bytes.Buffer
	if err := cmdDecide(c, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got listed
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v in %s", err, out.String())
	}
	return got
}

func TestDecideListsTheTasksThatNeedYou(t *testing.T) {
	want := map[string][]string{
		"ENG-1":  {"merge Merge the PR", "open Open the tab"},
		"ENG-2":  {"open Restore the Worker", "close Close the Task"},
		"ENG-3":  {"answer Answer here (text)", "open Open the tab"},
		"ENG-12": {"open Open the tab"},
		"R-1":    {"open Open the tab", "close Close the Task"},
		"R-2":    {"answer Answer here (text)", "open Open the tab"},
		"R-3":    {"open Restore the Worker", "close Close the Task"},
		"ENG-4":  {"open Open the tab", "close Close the Task"},
		"ENG-5":  {"rerun Run the Stage again", "open Open the tab", "close Close the Task"},
		"ENG-6":  {"close Close the Task", "open Open the tab"},
		"ENG-7":  {"open Open the tab", "close Close the Task"},
		"ENG-8":  {"open Open the tab"},
		"ENG-9":  {"open Open the tab"},
	}
	got := listDecisions(t, decideBoard())
	var ids []string
	for _, task := range got.Tasks {
		ids = append(ids, task.ID)
		var cs []string
		for _, ch := range task.Choices {
			s := ch.ID + " " + ch.Title
			if ch.TakesText {
				s += " (text)"
			}
			cs = append(cs, s)
		}
		if !slices.Equal(cs, want[task.ID]) {
			t.Errorf("%s: choices %q, want %q", task.ID, cs, want[task.ID])
		}
		if task.Title != task.ID+" title" || task.Note == "" {
			t.Errorf("%s: title %q, note %q", task.ID, task.Title, task.Note)
		}
	}
	if wantIDs := []string{"ENG-1", "ENG-2", "ENG-3", "ENG-12", "ENG-4", "ENG-5", "ENG-6", "ENG-7", "ENG-8", "ENG-9", "R-1", "R-2", "R-3"}; !slices.Equal(ids, wantIDs) {
		t.Fatalf("listed %q, want %q", ids, wantIDs)
	}
}

func TestEveryReasonHasAChoice(t *testing.T) {
	v := scheduler.BoardView{}
	for r := range choicesFor {
		v.Tasks = append(v.Tasks, needing("T-"+string(r), r))
	}
	v.Tasks = append(v.Tasks, needing("T-unknown", "a reason added later"))
	for _, task := range listDecisions(t, v).Tasks {
		if len(task.Choices) == 0 {
			t.Errorf("%s has no choice", task.ID)
		}
	}
}

func TestReadyToLandIsTheOnlyBuildStageInNeedsYou(t *testing.T) {
	var got []lifecycle.Stage
	for _, s := range lifecycle.Stages() {
		if task := (lifecycle.Task{Stage: s}); lifecycle.KindBuild.Has(s) && task.Group() == lifecycle.GroupNeedsYou {
			got = append(got, s)
		}
	}
	if !slices.Equal(got, []lifecycle.Stage{lifecycle.ReadyToLand}) {
		t.Fatalf("build Stages in Needs you with no flag: %v, want only Ready to land, which choicesFor[readyToLand] merges", got)
	}
}

func TestReviewReadyIsTheOnlyReviewStageInNeedsYou(t *testing.T) {
	var got []lifecycle.Stage
	for _, s := range lifecycle.Stages() {
		if task := (lifecycle.Task{Kind: lifecycle.KindReview, Stage: s}); lifecycle.KindReview.Has(s) && task.Group() == lifecycle.GroupNeedsYou {
			got = append(got, s)
		}
	}
	if !slices.Equal(got, []lifecycle.Stage{lifecycle.ReviewReady}) {
		t.Fatalf("review Stages in Needs you with no flag: %v, want only Review ready, which reviewReady opens or closes", got)
	}
}

func TestDecideEmptyListIsAnEmptyArray(t *testing.T) {
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, scheduler.BoardView{})})
	var out bytes.Buffer
	if err := cmdDecide(c, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != `{"tasks":[]}` {
		t.Fatalf("out = %s", got)
	}
}

func TestDecideRunsTheChosenCommand(t *testing.T) {
	opened := dataReply(t, scheduler.OpenResult{TaskView: scheduler.TaskView{ID: "ENG-2", Title: "ENG-2 title"}, Action: scheduler.OpenRestored})
	for _, c := range []struct {
		args    []string
		want    scheduler.Request
		wantOut string
	}{
		{[]string{"ENG-1", "merge"}, scheduler.Request{Op: "merge", Task: "ENG-1"}, ""},
		{[]string{"ENG-2", "open"}, scheduler.Request{Op: "open", Task: "ENG-2"}, "ENG-2 restored: ENG-2 title\n"},
		{[]string{"ENG-2", "close"}, scheduler.Request{Op: "close", Task: "ENG-2"}, ""},
		{[]string{"ENG-3", "answer", "Use the fast path."}, scheduler.Request{Op: "steer", Task: "ENG-3", Text: "Use the fast path."}, ""},
		{[]string{"ENG-5", "rerun"}, scheduler.Request{Op: "rerun", Task: "ENG-5", Stage: "ci_debug"}, ""},
	} {
		sched, got := fakeScheduler(t, map[string]scheduler.Response{
			"ls": dataReply(t, decideBoard()), "open": opened,
			"merge": {OK: true}, "close": {OK: true}, "steer": {OK: true}, "rerun": {OK: true},
		})
		var out bytes.Buffer
		if err := cmdDecide(sched, c.args, &out); err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if reqs := got(); len(reqs) != 2 || reqs[0].Op != "ls" || reqs[1] != c.want {
			t.Errorf("%q: requests %+v, want ls then %+v", c.args, reqs, c.want)
		}
		if out.String() != c.wantOut {
			t.Errorf("%q: out %q, want %q", c.args, out.String(), c.wantOut)
		}
	}
}

func TestDecideRefusesWhatTheTaskCannotDo(t *testing.T) {
	for _, c := range []struct {
		args     []string
		usage    bool
		message  string
		requests int
	}{
		{[]string{"ENG-10", "open"}, false, "ENG-10 does not need you", 1},
		{[]string{"R-4", "open"}, false, "R-4 does not need you", 1},
		{[]string{"ENG-2", "merge"}, false, `ENG-2 has no choice "merge". Its choices are open, close`, 1},
		{[]string{"ENG-3", "answer"}, true, "", 1},
		{[]string{"ENG-3", "answer", "  "}, true, "", 1},
		{[]string{"ENG-2", "close", "now"}, true, "", 1},
		{[]string{"ENG-2"}, true, "", 0},
		{[]string{"--json", "ENG-2", "open"}, true, "", 0},
	} {
		sched, got := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, decideBoard())})
		var out bytes.Buffer
		err := cmdDecide(sched, c.args, &out)
		switch {
		case err == nil:
			t.Errorf("%q: no error", c.args)
		case errors.Is(err, errUsage) != c.usage:
			t.Errorf("%q: error %v, usage %v", c.args, err, c.usage)
		case c.message != "" && err.Error() != c.message:
			t.Errorf("%q: error %q, want %q", c.args, err, c.message)
		}
		if reqs := got(); len(reqs) != c.requests {
			t.Errorf("%q: requests %+v, want %d", c.args, reqs, c.requests)
		}
	}
}

// lineup ready names each build Task whose PR waits only for the merge, with
// its PR, so the Orchestrator's prompt hook tells it on each turn.
func TestReadyNamesEachPRThatIsReadyToLand(t *testing.T) {
	ready := scheduler.TaskView{ID: "ENG-571", Kind: lifecycle.KindBuild, Title: "Rate limit login", StageKey: lifecycle.ReadyToLand.Key(),
		PRNumber: 4636, PRURL: "https://github.com/acme/data/pull/4636"}
	flagged := ready
	flagged.ID, flagged.Flag = "ENG-572", "pr closed"
	working := ready
	working.ID, working.StageKey = "ENG-573", lifecycle.Responding.Key()
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, scheduler.BoardView{Tasks: []scheduler.TaskView{ready, flagged, working}})})
	var out bytes.Buffer
	if err := cmdReady(c, nil, &out); err != nil {
		t.Fatal(err)
	}
	if want := "Ready to land: ENG-571, PR #4636 https://github.com/acme/data/pull/4636, Rate limit login\n"; out.String() != want {
		t.Errorf("lineup ready printed %q, want %q", out.String(), want)
	}
	c, _ = fakeScheduler(t, map[string]scheduler.Response{"ls": dataReply(t, scheduler.BoardView{Tasks: []scheduler.TaskView{working}})})
	out.Reset()
	if err := cmdReady(c, nil, &out); err != nil || out.Len() != 0 {
		t.Errorf("with no PR ready to land: %q, %v, want nothing", out.String(), err)
	}
}
