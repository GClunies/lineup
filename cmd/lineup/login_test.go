package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/linear"
)

func fakeGHUser(t *testing.T, script string) *github.Client {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &github.Client{Bin: bin}
}

func TestLoginGitHubRecordsTheTracker(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := loginGitHub(context.Background(), fakeGHUser(t, "echo octo"), dir, &out); err != nil {
		t.Fatal(err)
	}
	if login, on, _ := github.TrackerLogin(dir); !on || login != "octo" {
		t.Fatalf("on=%v login=%q", on, login)
	}
	err := loginGitHub(context.Background(), fakeGHUser(t, "echo 'not logged in' >&2; exit 1"), t.TempDir(), &out)
	if err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginNamesTheTrackerForms(t *testing.T) {
	err := login([]string{"acme"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "lineup login linear [workspace]") {
		t.Fatalf("err = %v", err)
	}
	var out bytes.Buffer
	err = login(nil, strings.NewReader("gitlab\n"), &out)
	if !strings.Contains(out.String(), "linear or github?") || err == nil || !strings.Contains(err.Error(), `"gitlab"`) {
		t.Fatalf("out %q err %v", out.String(), err)
	}
}

func TestLooksLikeIssueTakesEveryTrackersID(t *testing.T) {
	for s, want := range map[string]bool{
		"ENG-614": true, "owner/repo#12": true, "local-7": true, "LOCAL-7": true,
		"fix the flaky test": false, "local-x": false, "lineup#12": false, "o/r#0": false,
	} {
		if got := looksLikeIssue(s); got != want {
			t.Errorf("looksLikeIssue(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLogoutLinearPassesTheWorkspaceAndLocal(t *testing.T) {
	type call struct {
		which string
		local bool
	}
	var got []call
	fake := func(_ context.Context, which string, local bool) ([]string, error) {
		got = append(got, call{which, local})
		if which == "gone" {
			return nil, linear.ErrNotSignedIn
		}
		return []string{"Acme"}, nil
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"linear"}, {"linear", "acme", "--local"}, {"linear", "gone"}} {
		if err := logout(args, &out, fake); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if want := []call{{"", false}, {"acme", true}, {"gone", false}}; !slices.Equal(got, want) {
		t.Fatalf("calls %+v, want %+v", got, want)
	}
	if !strings.Contains(out.String(), "Signed out of Linear workspace Acme") || !strings.Contains(out.String(), "Not signed in to Linear") {
		t.Fatalf("out %q", out.String())
	}
	for _, bad := range [][]string{nil, {"linear", "a", "b"}, {"linear", "--all"}, {"gitlab"}, {"github", "x"}} {
		if err := logout(bad, &out, fake); err == nil || !strings.Contains(err.Error(), "lineup logout linear") {
			t.Errorf("%v: err %v", bad, err)
		}
	}
}
