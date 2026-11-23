package main

import (
	"bytes"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

func TestAddThatMustAskListsTheRepos(t *testing.T) {
	ask := &scheduler.RepoAsk{Issue: "PD-12", Repos: []string{"crexi/backend", "crexi/data"}}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"add": {Error: ask.Error(), Ask: ask}})
	err := cmdAdd(c, []string{"PD-12"}, &bytes.Buffer{})
	want := "PD-12 fits more than one Repo, so pick a Repo: crexi/backend, crexi/data. Then run lineup add --repo <repo> PD-12"
	if err == nil || err.Error() != want {
		t.Fatalf("add: err %v, want %q", err, want)
	}
}

func TestStartNamesTheTaskByItsCanonicalID(t *testing.T) {
	for typed, want := range map[string]string{"eng-614": "ENG-614 started\n", "ah-3": "AH-3 started\n", "acme/data#12": "acme/data#12 started\n"} {
		c, _ := fakeScheduler(t, map[string]scheduler.Response{"start": {OK: true}})
		var out bytes.Buffer
		if err := cmdStart(c, []string{typed}, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("start %s printed %q, want %q", typed, out.String(), want)
		}
	}
}
