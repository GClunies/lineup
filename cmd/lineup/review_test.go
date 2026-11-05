package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// replayGH is a gh stand-in that prints a recorded GraphQL response.
func replayGH(t *testing.T, fixture string) *github.Client {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "internal", "github", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\ncat "+abs+"\n"), 0o755)
	return &github.Client{Bin: bin}
}

// The fixture is a recorded poll of four umbrella/data PRs. Each level follows
// the transition table for a review Task added at the given time.
func TestTheRecordedReviewPollGivesTheTablesSignals(t *testing.T) {
	p := &githubPort{c: replayGH(t, "review_poll.json"), viewer: "octocat"}
	keys := []scheduler.ReviewKey{{Repo: "umbrella/data", Number: 4916}, {Repo: "umbrella/data", Number: 4915}, {Repo: "umbrella/data", Number: 4911}, {Repo: "umbrella/data", Number: 4959}}
	prs, err := p.ReviewPRs(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	day := func(d string) time.Time { v, _ := time.Parse(time.RFC3339, d+"T00:00:00Z"); return v }
	cases := []struct {
		number int
		added  time.Time
		want   lifecycle.Signal
	}{
		{4916, day("2026-11-04"), lifecycle.PRReviewed{}}, // COMMENTED on 11-04
		{4916, day("2026-11-05"), nil},                    // that review came before the Task
		{4915, day("2026-11-05"), lifecycle.PRMerged{}},
		{4911, day("2026-11-05"), lifecycle.PRClosed{}},
		{4959, day("2026-11-05"), lifecycle.PRReviewed{}},
	}
	for _, c := range cases {
		pr, ok := prs[scheduler.ReviewKey{Repo: "umbrella/data", Number: c.number}]
		if !ok {
			t.Fatalf("#%d missing", c.number)
		}
		if got := pr.Level(c.added); got != c.want {
			t.Errorf("#%d added %s: %v, want %v", c.number, c.added.Format(time.DateOnly), got, c.want)
		}
	}

	pending := &githubPort{c: replayGH(t, "review_poll_pending.json"), viewer: "octocat"}
	prs, err = pending.ReviewPRs(context.Background(), keys[:1])
	if err != nil {
		t.Fatal(err)
	}
	if got := prs[keys[0]].Level(day("2026-11-01")); got != nil {
		t.Fatalf("a pending review gave %v", got)
	}
}

func TestAReviewedPRMapsToTheCheckoutWhoseOriginIsItsRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	checkout := gitCheckout(t, filepath.Join(home, "code", "umbrella", "data"), "git@github.com:umbrella/data.git")
	p := configPort{cfg: loadConfig(t, "[[repos]]\nmatch = \"~/code/*/*\"\n")}
	if repo, ok := p.RepoForGitHub("umbrella/data"); !ok || repo != checkout {
		t.Fatalf("repo %q %v, want %s", repo, ok, checkout)
	}
	if _, ok := p.RepoForGitHub("umbrella/backend"); ok {
		t.Fatal("a repository with no checkout mapped to a Repo")
	}
}

func TestAReviewWorkerReadsTheReviewWorktreeFromTheMainCheckout(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin") // no direnv: the default Claude profile
	claudeBin := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(claudeBin, []byte("#!/bin/sh\n"), 0o755)
	cfg := loadConfig(t, "[harness.claude]\npath = \""+claudeBin+"\"\n[stage.reviewing]\neffort = \"max\"\n")
	p := harnessPort{cfg: cfg}
	argv, _, err := p.Command(context.Background(), scheduler.WorkerSpec{
		Stage: lifecycle.Reviewing, Repo: t.TempDir(), Worktree: "/repos/umbrella/data.review-pr-4955",
		PRURL: "https://github.com/umbrella/data/pull/4955", Overrides: map[string]string{"pr": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "--add-dir=/repos/umbrella/data.review-pr-4955") || !slices.Contains(argv, "max") {
		t.Fatalf("argv = %q", argv)
	}
	prompt := argv[len(argv)-1]
	if !strings.HasPrefix(prompt, "/pr-review https://github.com/umbrella/data/pull/4955\n") || !strings.Contains(prompt, "Stop at Step 5") {
		t.Fatalf("prompt = %s", prompt)
	}
}
