package scheduler

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// prefixConfig gives /repos/acme/hod the prefix hod and /repos/acme/web the
// prefix web, and each of them the Local Tracker. Every other Repo has no
// prefix.
type prefixConfig struct{ fakeConfig }

func (prefixConfig) RepoPrefix(repo string) string {
	return map[string]string{"/repos/acme/hod": "hod", "/repos/acme/web": "web"}[repo]
}

func (prefixConfig) RepoTracker(repo string) (string, string) {
	return "acme/" + filepath.Base(repo), KindLocal
}

// fakeLocal is the highest n of each prefix among the Local Issue files.
type fakeLocal map[string]int

func (f fakeLocal) Highest(prefix string) (int, error) { return f[prefix], nil }

func prefixHarness(t *testing.T, local fakeLocal, seed func(*store.Store)) *harness {
	return newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) {
		d.Config, d.LocalIssues = prefixConfig{}, local
	})
}

func TestAnAdHocTaskOfAPrefixedRepoTakesThePrefix(t *testing.T) {
	h := prefixHarness(t, fakeLocal{"hod": 2}, nil)
	v := h.do(Request{Op: "add", Repo: "hod", Text: "block linear writes"}).(TaskView)
	if v.ID != "HOD-3" {
		t.Fatalf("ID = %q, want HOD-3, one more than the Local Issue HOD-2", v.ID)
	}
	if d, _ := h.s.show(context.Background(), "hod-3"); d.Branch != "hod-3-block-linear-writes" {
		t.Errorf("branch = %q, want hod-3-block-linear-writes", d.Branch)
	}
	h.do(Request{Op: "start", Task: "hod-3"})
	sessions := map[string]string{}
	eventually(t, "the setup tab", func() bool {
		h.rex.mu.Lock()
		defer h.rex.mu.Unlock()
		for _, tb := range h.rex.tabs {
			sessions[tb.label] = tb.SessionID
		}
		return len(sessions) == 1
	})
	if s, ok := sessions["HOD-3 block linear writes"]; !ok || s != "session:hod" {
		t.Errorf("tabs = %v, want the tab HOD-3 block linear writes in session:hod", sessions)
	}
}

// seedIDs stores a Done, archived Task for each ID.
func seedIDs(ids ...string) func(*store.Store) {
	return func(st *store.Store) {
		for _, id := range ids {
			r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeClosed, Policy: testPolicy}, Title: id, Archived: true}
			data, _ := json.Marshal(r)
			st.Save(r.ID, data, nil)
		}
	}
}

func TestEachPrefixNumbersOnItsOwnAndSkipsEveryStoredID(t *testing.T) {
	h := prefixHarness(t, fakeLocal{"hod": 3, "local": 4}, seedIDs("HOD-7", "WEB-1", "AH-61"))
	for _, c := range []struct{ repo, want string }{
		{"hod", "HOD-8"}, // the archived HOD-7 is higher than the Local Issue HOD-3
		{"web", "WEB-2"},
		{"hod", "HOD-9"},
		{"data", "AH-1"}, // no prefix: the ah- counter, which AH-61 does not move
	} {
		if v := h.do(Request{Op: "add", Repo: c.repo, Text: "work"}).(TaskView); v.ID != c.want {
			t.Errorf("add --repo %s = %s, want %s", c.repo, v.ID, c.want)
		}
	}
	for repo, want := range map[string]string{"hod": "HOD-10", "web": "WEB-3", "data": "LOCAL-5", "": "LOCAL-5"} {
		if got := h.do(Request{Op: "next-id", Repo: repo}).(string); got != want {
			t.Errorf("next-id %q = %s, want %s", repo, got, want)
		}
	}
}

func TestALocalIssueCannotTakeTheIDOfAnAdHocTask(t *testing.T) {
	h := prefixHarness(t, fakeLocal{}, nil)
	h.do(Request{Op: "add", Repo: "hod", Text: "block linear writes"})
	_, err := h.s.Handle(context.Background(), Request{Op: "add", Issue: "HOD-1", Kind: KindLocal, Repo: "hod"})
	if err == nil || !strings.Contains(err.Error(), "HOD-1 is already the ID of another Task") {
		t.Fatalf("err = %v, want a refusal that names HOD-1", err)
	}
	if v := h.task("HOD-1"); v.Title != "block linear writes" {
		t.Fatalf("HOD-1 = %+v, want the ad hoc Task", v)
	}
}
