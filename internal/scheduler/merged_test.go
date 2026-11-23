package scheduler

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// seedKept stores a merged build Task with no tab whose worktree lineup keeps
// until until.
func seedKept(id string, until time.Time) func(*store.Store) {
	return func(st *store.Store) {
		r := Record{Task: lifecycle.Task{ID: id, Stage: lifecycle.Done, Outcome: lifecycle.OutcomeMerged, Policy: testPolicy},
			Title: id, Session: "data", SessionID: "session:data", Repo: "/repos/acme/data", Branch: "b-" + id,
			Worktree: "/worktrees/b-" + id, Conversation: "conversation-" + id, KeepUntil: until, StageSince: time.Now()}
		data, _ := json.Marshal(r)
		st.Save(r.ID, data, nil)
	}
}

func TestAStoppedSchedulerRemovesAWorktreeWhoseDaysEndedAtItsStart(t *testing.T) {
	seed := func(st *store.Store) {
		seedKept("ENG-due", time.Now().Add(-time.Hour))(st)
		seedKept("ENG-kept", time.Now().Add(24*time.Hour))(st)
	}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), seed, func(d *Deps) { d.PollInterval = time.Hour })
	eventually(t, "the due worktree removed", func() bool { return h.task("ENG-due").Worktree == "" })
	time.Sleep(100 * time.Millisecond)
	if got := h.worktreesRemoved(); !slices.Equal(got, []string{"b-ENG-due"}) {
		t.Fatalf("removed %v, want only the worktree whose days ended", got)
	}
	if v := h.task("ENG-kept"); v.Worktree == "" || !strings.Contains(v.Note, "worktree until ") {
		t.Fatalf("kept task = %+v, want its worktree and the date it goes", v)
	}
}

// The user's close removes the worktree, but not while it holds uncommitted
// changes. The note says why it stays, and each tick tries again.
func TestAUsersCloseKeepsADirtyWorktreeUntilItIsClean(t *testing.T) {
	h, worker := doneWithATab(t)
	h.wt.setDirty(true)
	h.rex.userCloses(worker)
	path := filepath.Join(h.wt.root, "eng-1-billing")
	eventually(t, "the note to say why the worktree stays", func() bool {
		return h.task("ENG-1").Note == "#42 merged · worktree kept: uncommitted changes in "+path
	})
	tries := len(h.worktreesRemoved())
	time.Sleep(100 * time.Millisecond)
	if got := len(h.worktreesRemoved()); got <= tries {
		t.Fatalf("%d tries after 5 ticks, want more than %d", got, tries)
	}
	if v := h.task("ENG-1"); v.Worktree != path {
		t.Fatalf("task = %+v, want the dirty worktree kept", v)
	}
	h.wt.setDirty(false)
	eventually(t, "the clean worktree removed", func() bool { return h.task("ENG-1").Worktree == "" })
	if v := h.task("ENG-1"); v.Note != "#42 merged" {
		t.Fatalf("note = %q, want the merge alone", v.Note)
	}
}
