package scheduler

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

func TestAnAddStartsTheTask(t *testing.T) {
	for _, c := range []struct {
		name    string
		req     Request
		id      string
		setup   string
		tracker bool
	}{
		{"a Linear Issue", Request{Op: "add", Issue: "ENG-1"}, "ENG-1", "wt ada/eng-1-billing", true},
		{"a GitHub Issue", Request{Op: "add", Issue: "acme/data#12", Kind: KindGitHub}, "acme/data#12", "wt ada/eng-1-billing", true},
		{"an ad hoc Task", Request{Op: "add", Repo: "web", Text: "Fix map tooltip"}, "ah-1", "wt lineup/ah-1-fix-map-tooltip", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
			if v := h.do(c.req).(TaskView); v.ID != c.id || v.Stage != "Working" || v.OverCap {
				t.Fatalf("add = %+v, want %s in Working", v, c.id)
			}
			h.rex.exits(h.waitTab(c.setup), 0)
			h.waitTab("claude working")
			if spec := h.waitSpecs(1); spec.Task != c.id || spec.Stage != lifecycle.Working {
				t.Fatalf("first prompt = %s in %s, want %s in Working", spec.Task, spec.Stage, c.id)
			}
			d, err := h.s.show(c.id)
			if err != nil {
				t.Fatal(err)
			}
			if e := d.Events[0]; e.Signal != "add" || e.From != "" || e.To != "Working" {
				t.Fatalf("first event = %+v, want add to Working", e)
			}
			if c.tracker {
				eventually(t, "the tracker told", func() bool {
					h.tr.mu.Lock()
					defer h.tr.mu.Unlock()
					return slices.Equal(h.tr.status, []string{"started"})
				})
			}
		})
	}
}

func TestAnAddGivesItsOverridesToTheFirstPrompt(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	set := map[string]string{"skill": "pr-explain", "pr": "false"}
	v := h.do(Request{Op: "add", Repo: "data", Text: "Explain PR 12", Set: set}).(TaskView)
	if v.Skill != "pr-explain" {
		t.Fatalf("add = %+v, want the skill pr-explain", v)
	}
	h.rex.exits(h.waitTab("wt lineup/ah-1-explain-pr-12"), 0)
	h.waitTab("claude working")
	if spec := h.waitSpecs(1); !maps.Equal(spec.Overrides, set) {
		t.Fatalf("first prompt overrides = %v, want %v", spec.Overrides, set)
	}
	d, err := h.s.show(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e := d.Events[0]; e.Signal != "add" || e.Detail != "pr=false skill=pr-explain" {
		t.Fatalf("add event = %+v, want the overrides in its detail", e)
	}
}

// legacy is the stored JSON of r with keys set as lineup stored them before
// lineup add started each Task.
func legacy(t *testing.T, r Record, keys map[string]string) []byte {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range keys {
		m[k] = v
	}
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func storedTask(id string, stage lifecycle.Stage) Record {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	return Record{
		Task:  lifecycle.Task{ID: id, Stage: stage, Policy: testPolicy, Left: testPolicy.Budgets},
		Title: "Add billing model", Repo: "/repos/acme/data", RepoSlug: "acme/data", Session: "data", Branch: "ada/" + id,
		Issue: &Issue{Kind: KindLinear, Identifier: id, Title: "Add billing model"}, CreatedAt: at, StageSince: at,
	}
}

func TestAStoredQueuedTaskLoadsAsARemovedTask(t *testing.T) {
	seed := func(st *store.Store) {
		st.Save("ENG-5", legacy(t, storedTask("ENG-5", lifecycle.Working), map[string]string{"stage": "queued", "worker_stage": "queued"}), nil)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed)
	h.s.call(func() { h.s.recs["ENG-5"].StageSince = time.Now().Add(-time.Hour) })
	v := h.do(Request{Op: "ls"}).(BoardView)
	if ids := boardIDs(v); slices.Contains(ids, "ENG-5") {
		t.Fatalf("board = %v, want ENG-5 archived", ids)
	}
	// A Task that never started did no work, so the header counts it as no Task done.
	if len(v.Done) != 0 {
		t.Fatalf("done times = %v, want none for a Task that never started", v.Done)
	}
	if v := h.task("ENG-5"); v.Stage != "Done" || v.Outcome != "removed" || v.Note != "removed: never started" {
		t.Fatalf("task = %+v, want Done, removed: never started", v)
	}
	removes := func() []store.Event {
		events, err := h.st.Events("ENG-5")
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(events, func(e store.Event) bool { return e.Signal != "remove" })
	}
	if got := removes(); len(got) != 1 || got[0].From != "Queued" || got[0].To != "Done" || got[0].Detail != "never started" {
		t.Fatalf("remove events = %+v, want one from Queued to Done, never started", got)
	}
	// The store holds the migrated Task, so a second load records nothing more.
	if _, err := New(Deps{Store: h.st, DefaultCap: 5}); err != nil {
		t.Fatal(err)
	}
	if got := removes(); len(got) != 1 {
		t.Fatalf("remove events after a second load = %+v, want one", got)
	}
}

func TestAStoredWorkerStageOfQueuedLoadsAsNoWorker(t *testing.T) {
	for _, c := range []struct {
		stored    string
		hadWorker bool
	}{{"queued", false}, {"working", true}} {
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		st.Save("ENG-7", legacy(t, storedTask("ENG-7", lifecycle.Working), map[string]string{"worker_stage": c.stored}), nil)
		s, err := New(Deps{Store: st, DefaultCap: 5})
		if err != nil {
			t.Fatal(err)
		}
		r := s.recs["ENG-7"]
		if r.Stage != lifecycle.Working || r.hadWorker() != c.hadWorker {
			t.Errorf("worker_stage %s: stage %s, had a Worker %v, want Working, %v", c.stored, r.Stage, r.hadWorker(), c.hadWorker)
		}
		if events, _ := st.Events("ENG-7"); len(events) != 0 {
			t.Errorf("worker_stage %s: events %+v, want none", c.stored, events)
		}
	}
}
