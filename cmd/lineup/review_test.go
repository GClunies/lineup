package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
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

// The CI debug Worker reads the Standing, so it leaves the optional checks alone.
func TestACIDebugWorkerSeesWhichChecksGate(t *testing.T) {
	claudeOnPath(t)
	p := harnessPort{cfg: loadConfig(t, "")}
	spec := scheduler.WorkerSpec{
		Task: "ENG-1", Stage: lifecycle.CIDebug, Repo: t.TempDir(), Branch: "ada/eng-1", Name: "ENG-1 Fix the tooltip",
		PRURL: "https://github.com/umbrella/data/pull/7", Standing: lifecycle.Standing{Failed: []string{"test"}, Optional: []string{"lint"}},
	}
	prompt, err := p.Prompt(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "keep it from landing: test.") || !strings.Contains(prompt, "optional checks failed too: lint.") || !strings.Contains(prompt, "`lineup ask ENG-1 ") {
		t.Fatalf("prompt = %s", prompt)
	}
}

func TestABuildWorkerKeepsTheModelOfWorking(t *testing.T) {
	claudeOnPath(t)
	p := harnessPort{cfg: loadConfig(t, "[stage.default]\nmodel = \"d\"\n[stage.working]\nmodel = \"w\"\neffort = \"max\"\n[stage.ci_debug]\nskill = \"diagnosing-bugs\"\n")}
	spec := scheduler.WorkerSpec{Task: "ENG-1", Stage: lifecycle.CIDebug, Repo: t.TempDir(), Branch: "ada/eng-1",
		Name: "ENG-1 Fix the tooltip", Conversation: "0b6f8f0e-3c1a-4b8e-9d2f-5a7c1e4b9d20"}
	prompt, err := p.Prompt(context.Background(), spec)
	if err != nil || !strings.HasPrefix(prompt, "/diagnosing-bugs ") {
		t.Fatalf("CI debug prompt %q, err %v, want the CI debug skill", prompt, err)
	}
	argv, _, err := p.Command(context.Background(), spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(argv, "--model"); i < 0 || argv[i+1] != "w" || argv[i+3] != "max" {
		t.Fatalf("argv = %q, want the model and effort of Working", argv)
	}
	if i := slices.Index(argv, "--resume"); i < 0 || argv[i+1] != spec.Conversation {
		t.Fatalf("argv = %q, want --resume %s", argv, spec.Conversation)
	}
}

func TestAReviewWorkerReadsTheReviewWorktreeFromTheMainCheckout(t *testing.T) {
	claudeBin := claudeOnPath(t)
	cfg := loadConfig(t, "[stage.reviewing]\neffort = \"max\"\n")
	p := harnessPort{cfg: cfg}
	spec := scheduler.WorkerSpec{
		Stage: lifecycle.Reviewing, Repo: t.TempDir(), Worktree: "/repos/umbrella/data.review-pr-4955",
		PRURL: "https://github.com/umbrella/data/pull/4955", Overrides: map[string]string{"pr": "true"},
		Conversation: "0b6f8f0e-3c1a-4b8e-9d2f-5a7c1e4b9d20", Name: "R-1 data#4955 Fix the tooltip",
	}
	prompt, err := p.Prompt(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	argv, _, err := p.Command(context.Background(), spec, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "--add-dir=/repos/umbrella/data.review-pr-4955") || !slices.Contains(argv, "max") {
		t.Fatalf("argv = %q", argv)
	}
	if i := slices.Index(argv, "--session-id"); i < 0 || argv[i+1] != spec.Conversation {
		t.Fatalf("argv = %q, want --session-id %s", argv, spec.Conversation)
	}
	if argv[len(argv)-1] != prompt || !strings.HasPrefix(prompt, "/pr-review https://github.com/umbrella/data/pull/4955\n") || !strings.Contains(prompt, "At Step 5, wait for the user's decisions") {
		t.Fatalf("prompt = %s", prompt)
	}

	// A reopened Worker resumes the conversation with the Reviewing settings and no prompt.
	model := argv[slices.Index(argv, "--model")+1]
	spec.Stage = lifecycle.ReviewReady
	if prompt, err := p.Prompt(context.Background(), spec); err != nil || prompt != "" {
		t.Fatalf("Review ready prompt %q, err %v, want none", prompt, err)
	}
	argv, _, err = p.Command(context.Background(), spec, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{claudeBin, "--model", model, "--effort", "max", "--resume", spec.Conversation, "--name", spec.Name, "--remote-control", spec.Name, "--allowedTools=Bash(lineup ask:*)", "--add-dir=/repos/umbrella/data.review-pr-4955"}
	if !slices.Equal(argv, want) {
		t.Fatalf("reopen argv = %q, want %q", argv, want)
	}
	spec.Conversation = ""
	if _, _, err := p.Command(context.Background(), spec, ""); err == nil {
		t.Fatal("a reopen with no conversation got a command")
	}
}

// fakeScheduler answers each request with the reply for its op. The returned
// function lists the requests so far.
func fakeScheduler(t *testing.T, replies map[string]scheduler.Response) (scheduler.Client, func() []scheduler.Request) {
	t.Helper()
	dir, _ := os.MkdirTemp("", "lu")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var got []scheduler.Request
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req scheduler.Request
			if json.NewDecoder(bufio.NewReader(conn)).Decode(&req) == nil {
				mu.Lock()
				got = append(got, req)
				mu.Unlock()
				json.NewEncoder(conn).Encode(replies[req.Op])
			}
			conn.Close()
		}
	}()
	return scheduler.Client{Path: sock}, func() []scheduler.Request {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

func reviewReply(t *testing.T, action scheduler.ReviewAction) scheduler.Response {
	data, err := json.Marshal(scheduler.ReviewResult{TaskView: scheduler.TaskView{ID: "R-1", Repo: "data", Stage: "Reviewing", Title: "data#4955 Pull the base images"}, Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler.Response{OK: true, Data: data}
}

func TestLineupReviewStartsANewReview(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"review": reviewReply(t, scheduler.ReviewAdded)})
	var out bytes.Buffer
	if err := cmdReview(c, []string{"https://github.com/umbrella/data/pull/4955"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "review" || reqs[0].URL != "https://github.com/umbrella/data/pull/4955" {
		t.Fatalf("requests = %+v", reqs)
	}
	if out.String() != "R-1 reviewing in data: data#4955 Pull the base images\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestLineupReviewOfAnOpenReviewFocusesIt(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"review": reviewReply(t, scheduler.ReviewOpen), "focus": {OK: true}})
	var out bytes.Buffer
	if err := cmdReview(c, []string{"umbrella/data#4955"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 2 || reqs[1].Op != "focus" || reqs[1].Task != "R-1" {
		t.Fatalf("requests = %+v, want review then focus R-1", reqs)
	}
	if !strings.HasPrefix(out.String(), "R-1 is already open (Reviewing)") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestLineupReviewOfAStoppedReviewRestartsItWithoutFocus(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"review": reviewReply(t, scheduler.ReviewRestarted), "focus": {OK: true}})
	var out bytes.Buffer
	if err := cmdReview(c, []string{"umbrella/data#4955"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "review" {
		t.Fatalf("requests = %+v, want review alone", reqs)
	}
	if want := "R-1 restarted: data#4955 Pull the base images\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

// A reopened Worker opens a new tab later, so the CLI reports it and focuses nothing.
func TestLineupReviewOfAClosedWorkerReopensItWithoutFocus(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"review": reviewReply(t, scheduler.ReviewReopened), "focus": {OK: true}})
	var out bytes.Buffer
	if err := cmdReview(c, []string{"umbrella/data#4955"}, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "review" {
		t.Fatalf("requests = %+v, want review alone", reqs)
	}
	if want := "R-1 reopened: data#4955 Pull the base images\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

// A review Task has no branch, so lineup show prints no empty branch.
func TestLineupShowOfAReviewPrintsNoBranch(t *testing.T) {
	data, err := json.Marshal(scheduler.TaskDetail{TaskView: scheduler.TaskView{ID: "R-1", Repo: "data", Stage: "Reviewing", Title: "data#4955 Pull the base images"}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"show": {OK: true, Data: data}})
	var out bytes.Buffer
	if err := cmdShow(c, []string{"R-1"}, &out); err != nil {
		t.Fatal(err)
	}
	if want := "R-1  data#4955 Pull the base images\nReviewing in data\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

func TestLineupShowPrintsTheLandingBar(t *testing.T) {
	data, err := json.Marshal(scheduler.TaskDetail{TaskView: scheduler.TaskView{ID: "ENG-590", Repo: "data", Stage: "Ready to land", Title: "Add billing model"},
		Branch: "ada/eng-590-billing", Bar: []string{"✓ required checks passed", "! optional checks failed: lint"}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := fakeScheduler(t, map[string]scheduler.Response{"show": {OK: true, Data: data}})
	var out bytes.Buffer
	if err := cmdShow(c, []string{"ENG-590"}, &out); err != nil {
		t.Fatal(err)
	}
	want := "ENG-590  Add billing model\nReady to land in data (ada/eng-590-billing)\nLanding bar\n  ✓ required checks passed\n  ! optional checks failed: lint\n"
	if out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

func reviewsReply(t *testing.T, v scheduler.ReviewsView) scheduler.Response {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler.Response{OK: true, Data: data}
}

func TestLineupReviewsListsEachQueue(t *testing.T) {
	view := scheduler.ReviewsView{
		Paused: true, Slots: 5, Started: 1,
		Queues: []scheduler.QueueView{{
			Name: "platform", Slots: 3,
			Started: []scheduler.TaskView{{ID: "R-12", Stage: "Review ready", Title: "data#4955 Pull the base images"}},
			Ranked: []github.Candidate{
				{PR: github.PullRequest{Repo: "umbrella/data", Number: 4960, Title: "Write the usage logs", Author: "hopper"}, Rereview: true},
				{PR: github.PullRequest{Repo: "umbrella/data", Number: 4919, Title: "Move the CSV readers", Author: "noether"}},
			},
		}},
		Skips:    []github.Skip{{URL: "https://github.com/umbrella/data/pull/4945", Title: "Write the usage logs again", At: time.Date(2026, 11, 9, 17, 0, 0, 0, time.UTC)}},
		Unmapped: []string{"no Repo mapping for umbrella/infra: no [[repos]] checkout has it as its origin"},
	}
	c, got := fakeScheduler(t, map[string]scheduler.Response{"reviews": reviewsReply(t, view)})
	var out bytes.Buffer
	if err := cmdReviews(c, nil, &out); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 || reqs[0].Op != "reviews" {
		t.Fatalf("requests = %+v", reqs)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	want := [][]string{
		{"Reviews paused"},
		{"Reviews: 1/5 started"},
		{"Q: platform", "1/3 started"},
		{"R-12", "Review ready", "data#4955 Pull the base images"},
		{"1", "re-review", "data#4960 Write the usage logs", "hopper"},
		{"2", "data#4919 Move the CSV readers", "noether"},
		{"Skips: 1", "lineup reviews unskip <PR>"},
		{"umbrella/data#4945", "Write the usage logs again", "since 2026-11-09"},
		{"Unmapped", "umbrella/infra"},
	}
	if len(lines) != len(want) {
		t.Fatalf("out =\n%s", out.String())
	}
	for i, w := range want {
		for _, s := range w {
			if !strings.Contains(lines[i], s) {
				t.Errorf("line %d %q lacks %q", i+1, lines[i], s)
			}
		}
	}
	if strings.Contains(lines[5], "re-review") {
		t.Errorf("line 5 %q calls a first review a re-review", lines[4])
	}

	out.Reset()
	if err := cmdReviews(c, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var back scheduler.ReviewsView
	if err := json.Unmarshal(out.Bytes(), &back); err != nil || !reflect.DeepEqual(back, view) {
		t.Fatalf("--json = %s, %v", out.String(), err)
	}
}

func TestLineupReviewsPauseAndResume(t *testing.T) {
	c, got := fakeScheduler(t, map[string]scheduler.Response{"reviews-pause": {OK: true}, "reviews-resume": {OK: true}})
	var out bytes.Buffer
	for _, a := range []string{"pause", "resume"} {
		if err := cmdReviews(c, []string{a}, &out); err != nil {
			t.Fatal(err)
		}
	}
	if reqs := got(); len(reqs) != 2 || reqs[0].Op != "reviews-pause" || reqs[1].Op != "reviews-resume" {
		t.Fatalf("requests = %+v", reqs)
	}
	if !strings.HasPrefix(out.String(), "Review queues paused.") || !strings.Contains(out.String(), "\nReview queues resumed.\n") {
		t.Fatalf("out = %q", out.String())
	}
	for _, args := range [][]string{{"stop"}, {"--json", "pause"}, {"pause", "now"}} {
		if err := cmdReviews(c, args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("lineup reviews %v: %v, want the usage", args, err)
		}
	}
	if n := len(got()); n != 2 {
		t.Fatalf("a usage error sent a request: %d requests", n)
	}
}

func skipReply(t *testing.T, res scheduler.SkipResult) scheduler.Response {
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler.Response{OK: true, Data: data}
}

func TestLineupReviewsSkipAndUnskip(t *testing.T) {
	skipped := github.Skip{URL: "https://github.com/umbrella/data/pull/4945", Title: "Write the usage logs again"}
	cases := []struct {
		args  []string
		reply scheduler.SkipResult
		want  string
	}{
		{[]string{"skip", "umbrella/data#4945"}, scheduler.SkipResult{Skip: skipped, Changed: true},
			"Skipped umbrella/data#4945: Write the usage logs again. No queue picks it until you run lineup reviews unskip umbrella/data#4945.\n"},
		{[]string{"skip", "umbrella/data#4945"}, scheduler.SkipResult{Skip: skipped, Changed: true, Closed: "pr-4945"},
			"Skipped umbrella/data#4945: Write the usage logs again. No queue picks it until you run lineup reviews unskip umbrella/data#4945.\nClosed pr-4945.\n"},
		{[]string{"skip", "umbrella/data#4945"}, scheduler.SkipResult{Skip: skipped}, "umbrella/data#4945 is already skipped.\n"},
		{[]string{"skip", "https://github.com/Umbrella/Data/pull/4945/files"}, scheduler.SkipResult{Skip: skipped, Changed: true},
			"Skipped umbrella/data#4945: Write the usage logs again. No queue picks it until you run lineup reviews unskip umbrella/data#4945.\n"},
		{[]string{"unskip", "umbrella/data#4945"}, scheduler.SkipResult{Skip: skipped, Changed: true}, "Unskipped umbrella/data#4945. The queues can pick it again.\n"},
		{[]string{"unskip", "umbrella/data#4945"}, scheduler.SkipResult{}, "umbrella/data#4945 is not skipped.\n"},
	}
	for _, c := range cases {
		c1, got := fakeScheduler(t, map[string]scheduler.Response{"reviews-" + c.args[0]: skipReply(t, c.reply)})
		var out bytes.Buffer
		if err := cmdReviews(c1, c.args, &out); err != nil {
			t.Fatal(err)
		}
		if reqs := got(); len(reqs) != 1 || reqs[0].Op != "reviews-"+c.args[0] || reqs[0].URL != c.args[1] {
			t.Fatalf("requests = %+v", reqs)
		}
		if out.String() != c.want {
			t.Errorf("%v printed %q, want %q", c.args, out.String(), c.want)
		}
	}
	c, got := fakeScheduler(t, map[string]scheduler.Response{})
	for _, args := range [][]string{{"skip"}, {"skip", "a", "b"}, {"--json", "skip", "a"}, {"unskip"}} {
		if err := cmdReviews(c, args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("lineup reviews %v: %v, want the usage", args, err)
		}
	}
	if n := len(got()); n != 0 {
		t.Fatalf("a usage error sent %d requests", n)
	}
}
