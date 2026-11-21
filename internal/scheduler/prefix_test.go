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
// prefix web. Every other Repo has none.
type prefixConfig struct{ fakeConfig }

func (prefixConfig) RepoPrefix(repo string) string {
	return map[string]string{"/repos/acme/hod": "hod", "/repos/acme/web": "web"}[repo]
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
	if v.ID != "hod-3" {
		t.Fatalf("ID = %q, want hod-3, one more than the Local Issue hod-2", v.ID)
	}
	if d, _ := h.s.show("hod-3"); d.Branch != "hod-3-block-linear-writes" {
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
	if s, ok := sessions["hod-3 block linear writes"]; !ok || s != "session:hod" {
		t.Errorf("tabs = %v, want the tab hod-3 block linear writes in session:hod", sessions)
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
	h := prefixHarness(t, fakeLocal{"hod": 3, "local": 4}, seedIDs("hod-7", "web-1", "ah-61"))
	for _, c := range []struct{ repo, want string }{
		{"hod", "hod-8"}, // the archived hod-7 is higher than the Local Issue hod-3
		{"web", "web-2"},
		{"hod", "hod-9"},
		{"data", "ah-1"}, // no prefix: the ah- counter, which ah-61 does not move
	} {
		if v := h.do(Request{Op: "add", Repo: c.repo, Text: "work"}).(TaskView); v.ID != c.want {
			t.Errorf("add --repo %s = %s, want %s", c.repo, v.ID, c.want)
		}
	}
	for repo, want := range map[string]string{"hod": "hod-10", "web": "web-3", "data": "local-5", "": "local-5"} {
		if got := h.do(Request{Op: "next-id", Repo: repo}).(string); got != want {
			t.Errorf("next-id %q = %s, want %s", repo, got, want)
		}
	}
}

func TestALocalIssueCannotTakeTheIDOfAnAdHocTask(t *testing.T) {
	h := prefixHarness(t, fakeLocal{}, nil)
	h.do(Request{Op: "add", Repo: "hod", Text: "block linear writes"})
	_, err := h.s.Handle(context.Background(), Request{Op: "add", Issue: "hod-1", Kind: KindLocal, Repo: "hod"})
	if err == nil || !strings.Contains(err.Error(), "hod-1 is already the ID of another Task") {
		t.Fatalf("err = %v, want a refusal that names hod-1", err)
	}
	if v := h.task("hod-1"); v.Title != "block linear writes" {
		t.Fatalf("hod-1 = %+v, want the ad hoc Task", v)
	}
}
