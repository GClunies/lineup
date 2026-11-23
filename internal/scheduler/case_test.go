package scheduler

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// An old store holds lineup's own IDs in lower case, such as ah-65 or local-7.
func TestAnOldStoreLoadsEachLowerCaseIDInUpperCase(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	seed := func(st *store.Store) {
		for _, r := range []Record{
			{Task: lifecycle.Task{ID: "ah-65"}, Branch: "ah-65-tidy-logs"},
			{Task: lifecycle.Task{ID: "local-7"}, Branch: "local-7-retry-loads", Issue: &Issue{Kind: KindLocal, Identifier: "local-7"}},
			{Task: lifecycle.Task{ID: "hod-1"}, Branch: "hod-1-block-writes"},
			{Task: lifecycle.Task{ID: "pr-12"}},
		} {
			r.Stage, r.Outcome, r.Policy, r.Title, r.StageSince = lifecycle.Done, lifecycle.OutcomeClosed, testPolicy, r.ID, time.Now()
			data, _ := json.Marshal(r)
			st.Save(r.ID, data, []store.Event{{TaskID: r.ID, At: at, Signal: "close", From: "Working", To: "Done"}})
		}
	}
	want := []string{"AH-65", "HOD-1", "LOCAL-7", "PR-12"}
	h := newHarness(t, db, seed)
	var ids []string
	for _, v := range h.do(Request{Op: "ls"}).(BoardView).Tasks {
		ids = append(ids, v.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, want) {
		t.Fatalf("ls = %q, want %q", ids, want)
	}
	d := h.do(Request{Op: "show", Task: "local-7"}).(TaskDetail)
	if d.ID != "LOCAL-7" || d.Branch != "local-7-retry-loads" || len(d.Events) != 1 || d.Events[0].TaskID != "LOCAL-7" || !d.Events[0].At.Equal(at) {
		t.Fatalf("show local-7 = %s on %s with events %+v, want LOCAL-7 on its old branch with its event", d.ID, d.Branch, d.Events)
	}

	h.do(Request{Op: "archive", Task: "ah-65"})
	rows := func(st *store.Store) []string {
		t.Helper()
		tasks, err := st.Tasks()
		if err != nil {
			t.Fatal(err)
		}
		var r Record
		if err := json.Unmarshal(tasks["LOCAL-7"], &r); err != nil || r.ID != "LOCAL-7" || r.Issue.Identifier != "LOCAL-7" {
			t.Fatalf("the stored LOCAL-7 is %+v, %v, want the ID and Identifier LOCAL-7", r, err)
		}
		return slices.Sorted(maps.Keys(tasks))
	}
	if got := rows(h.st); !slices.Equal(got, want) {
		t.Fatalf("after a save the store holds %q, want one row for each of %q", got, want)
	}

	h.cancel()
	h2 := newHarness(t, db, nil)
	if got := rows(h2.st); !slices.Equal(got, want) {
		t.Fatalf("after a second start the store holds %q, want %q", got, want)
	}
	if got := h2.events("AH-65"); !slices.Equal(got, []string{"close", "archive"}) {
		t.Fatalf("AH-65 events = %q, want close then archive", got)
	}
	if ev, _ := h2.st.Events("ah-65"); len(ev) != 0 {
		t.Fatalf("ah-65 still holds events %+v", ev)
	}
}

// Each op takes an ad hoc ID, a Repo prefix's ID, and a Local Issue's ID in
// any case. A branch stays in lower case.
func TestTheOpsTakeLineupsOwnIDsInAnyCase(t *testing.T) {
	h := prefixHarness(t, fakeLocal{}, nil)
	for _, c := range []struct {
		add                 Request
		typed, want, branch string
	}{
		{Request{Op: "add", Repo: "data", Text: "tidy logs"}, "ah-1", "AH-1", "lineup/ah-1-tidy-logs"},
		{Request{Op: "add", Repo: "hod", Text: "block writes"}, "Hod-1", "HOD-1", "hod-1-block-writes"},
		{Request{Op: "add", Issue: "local-7", Kind: KindLocal, Repo: "data"}, "lOCAL-7", "LOCAL-7", "ada/eng-1-billing"},
	} {
		if v := h.do(c.add).(TaskView); v.ID != c.want {
			t.Fatalf("add %+v = %s, want %s", c.add, v.ID, c.want)
		}
		h.do(Request{Op: "set", Task: c.typed, Key: "skill", Value: "tdd"})
		d := h.do(Request{Op: "show", Task: strings.ToLower(c.want)}).(TaskDetail)
		if d.ID != c.want || d.Skill != "tdd" || d.Branch != c.branch {
			t.Fatalf("show %s = %s on %s with skill %q, want %s on %s with skill tdd", strings.ToLower(c.want), d.ID, d.Branch, d.Skill, c.want, c.branch)
		}
	}
	h.do(Request{Op: "start", Task: "ah-1"})
	var labels []string
	eventually(t, "the setup tab", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		labels = labels[:0]
		for _, tb := range h.rex.tabs {
			labels = append(labels, tb.label)
		}
		return len(labels) == 1
	})
	if labels[0] != "AH-1 tidy logs" {
		t.Errorf("tab label = %q, want AH-1 tidy logs", labels[0])
	}
	h.do(Request{Op: "close", Task: "local-7"})
	h.waitStage("LOCAL-7", lifecycle.Done)
}
