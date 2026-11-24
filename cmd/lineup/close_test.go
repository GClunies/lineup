package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

// lineup close prints why lineup kept the worktree, and nothing when lineup
// removed it (PROD-TABS-020).
func TestLineupCloseSaysWhyItKeptTheWorktree(t *testing.T) {
	for _, why := range []string{"worktree kept: uncommitted changes in /wt/eng-1", ""} {
		data, _ := json.Marshal(why)
		c, got := fakeScheduler(t, map[string]scheduler.Response{"close": {OK: true, Data: data}})
		var out bytes.Buffer
		if err := cmdClose(c, []string{"ENG-1"}, &out); err != nil {
			t.Fatal(err)
		}
		if reqs := got(); len(reqs) != 1 || reqs[0].Op != "close" || reqs[0].Task != "ENG-1" {
			t.Fatalf("requests = %+v, want close ENG-1 alone", reqs)
		}
		want := why
		if why != "" {
			want += "\n"
		}
		if out.String() != want {
			t.Fatalf("out = %q, want %q", out.String(), want)
		}
	}
}
