package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// setUpConfig is a Config whose new Repos have no table of their own.
type setUpConfig struct {
	Config
	new map[string]bool
}

func (c setUpConfig) SetUp(repo string) (string, bool) {
	return "acme/" + filepath.Base(repo), !c.new[repo]
}

func TestAddRefusesABuildTaskInARepoThatIsNotSetUp(t *testing.T) {
	cfg := setUpConfig{Config: trackerConfig{}, new: map[string]bool{"/repos/acme/data": true, "/repos/acme/web": true}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config = cfg })
	for _, c := range []struct {
		req  Request
		want string
	}{
		{Request{Op: "add", Issue: "LOCAL-1", Kind: KindLocal}, "repo not set up: acme/data"},
		// web has no Tracker either, and the setup comes first.
		{Request{Op: "add", Issue: "ENG-3", Repo: "web"}, "repo not set up: acme/web"},
		{Request{Op: "add", Repo: "web", Text: "tidy the README"}, "repo not set up: acme/web"},
	} {
		_, err := h.s.Handle(context.Background(), c.req)
		if err == nil || err.Error() != c.want || !errors.Is(err, ErrNotSetUp) {
			t.Errorf("%+v: err = %v, want %q", c.req, err, c.want)
		}
	}
	if ids := boardIDs(h.do(Request{Op: "ls"}).(BoardView)); len(ids) != 0 {
		t.Fatalf("Tasks = %v, want none", ids)
	}
	if v := h.do(Request{Op: "add", Repo: "api", Text: "tidy the README"}).(TaskView); v.Repo != "api" {
		t.Fatalf("a Repo that is set up: %+v", v)
	}
}

func TestAReviewTaskNeedsNoSetup(t *testing.T) {
	cfg := setUpConfig{Config: fakeConfig{}, new: map[string]bool{"/repos/acme/data": true}}
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil, func(d *Deps) { d.Config = cfg })
	h.gh.setReview(openPR(7, "h1"))
	if res := h.review(prURL7); res.ID != "PR-7" {
		t.Fatalf("review = %+v, want PR-7", res)
	}
}
