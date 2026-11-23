package main

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

func TestAddTakesItsFlagsBeforeAndAfterTheIssueOrTheText(t *testing.T) {
	t.Setenv("LINEUP_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	overCap := scheduler.Request{Op: "add", Issue: "ENG-1", OverCap: true}
	for _, c := range []struct {
		args []string
		want scheduler.Request
	}{
		{[]string{"--repo", "data", "text", "--set", "skill=pr-explain", "--set", "pr=false"},
			scheduler.Request{Op: "add", Repo: "data", Text: "text", Set: map[string]string{"skill": "pr-explain", "pr": "false"}}},
		{[]string{"--over-cap", "ENG-1"}, overCap},
		{[]string{"ENG-1", "--over-cap"}, overCap},
		{[]string{"--set", "skill=a=b", "ENG-1"}, scheduler.Request{Op: "add", Issue: "ENG-1", Set: map[string]string{"skill": "a=b"}}},
		{[]string{"ENG-1"}, scheduler.Request{Op: "add", Issue: "ENG-1"}},
	} {
		sched, got := fakeScheduler(t, map[string]scheduler.Response{
			"add": dataReply(t, scheduler.TaskView{ID: "ENG-1", Repo: "data", Title: "Fix it"}),
		})
		var out bytes.Buffer
		if err := cmdAdd(sched, c.args, &out); err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if reqs := got(); len(reqs) != 1 || !reflect.DeepEqual(reqs[0], c.want) {
			t.Errorf("%q: requests %+v, want %+v", c.args, reqs, c.want)
		}
		if want := "ENG-1 started in data: Fix it\n"; out.String() != want {
			t.Errorf("%q: out %q, want %q", c.args, out.String(), want)
		}
	}
}

func TestAddRefusesABadSetAndASecondArgument(t *testing.T) {
	t.Setenv("LINEUP_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	sched, got := fakeScheduler(t, nil)
	for _, args := range [][]string{
		{"--set", "skill", "ENG-1"},
		{"--set", "=x", "ENG-1"},
		{"ENG-1", "--set", "pr"},
		{"ENG-1", "ENG-2"},
		{"--repo", "data", "one", "--over-cap", "two"},
		{"--over-cap"},
		{"ENG-1", "--bogus"},
	} {
		if err := cmdAdd(sched, args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("lineup add %q: %v, want the usage", args, err)
		}
	}
	if n := len(got()); n != 0 {
		t.Fatalf("a usage error sent %d requests", n)
	}
}

func TestAddThatMustAskListsTheRepos(t *testing.T) {
	t.Setenv("LINEUP_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	ask := &scheduler.RepoAsk{Issue: "PD-12", Repos: []string{"crexi/backend", "crexi/data"}}
	sched, _ := fakeScheduler(t, map[string]scheduler.Response{"add": {Error: ask.Error(), Ask: ask}})
	err := cmdAdd(sched, []string{"PD-12"}, io.Discard)
	want := "PD-12 fits more than one Repo, so pick a Repo: crexi/backend, crexi/data. Then run lineup add --repo <repo> PD-12"
	if err == nil || err.Error() != want {
		t.Fatalf("add: err %v, want %q", err, want)
	}
}
