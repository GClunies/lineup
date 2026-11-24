package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/rex"
	"github.com/gclunies/lineup/internal/scheduler"
	"github.com/gclunies/lineup/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// spyTracker records each call it gets, as "<call> <identifier>". desc is
// each Issue's description.
type spyTracker struct {
	name  string
	calls *[]string
	desc  string
}

func (s spyTracker) note(call, id string) { *s.calls = append(*s.calls, s.name+" "+call+" "+id) }

func (s spyTracker) Lookup(_ context.Context, id string) (scheduler.Issue, error) {
	s.note("lookup", id)
	return scheduler.Issue{Identifier: id}, nil
}
func (s spyTracker) Begin(_ context.Context, is scheduler.Issue) error {
	s.note("begin", is.Identifier)
	return nil
}
func (s spyTracker) SetStatus(_ context.Context, is scheduler.Issue, ev string) error {
	s.note("status:"+ev, is.Identifier)
	return nil
}
func (s spyTracker) Description(_ context.Context, is scheduler.Issue) (string, error) {
	s.note("describe", is.Identifier)
	return s.desc, nil
}
func (s spyTracker) Create(_ context.Context, build *scheduler.Issue, repo, title, _ string) (scheduler.Issue, error) {
	of := "none"
	if build != nil {
		of = build.Identifier
	}
	s.note("create beside "+of+" in "+repo, title)
	return scheduler.Issue{Identifier: "NEW-1"}, nil
}

// A fix Issue goes to the Tracker of the build Task's Issue. An ad hoc
// build Task has none, so its fix Issue goes to the Repo Tracker
// (PROD-QA-011).
func TestTheMuxFilesAFixIssueBesideTheBuildIssueOrInTheRepoTracker(t *testing.T) {
	cfg := loadConfig(t, "[repos.\"acme/data\"]\nteams = [\"ENG\"]\ntracker = \"linear\"\n"+
		"[repos.\"acme/web\"]\ntracker = \"github\"\n[repos.\"acme/notes\"]\ntracker = \"local\"\n")
	var calls []string
	mux := trackerMux{kinds: map[string]issueTracker{}, fixTracker: cfg.FixTracker}
	for _, k := range []string{scheduler.KindLinear, scheduler.KindGitHub, scheduler.KindLocal} {
		mux.kinds[k] = spyTracker{name: k, calls: &calls}
	}
	ctx := context.Background()
	for _, c := range []struct {
		build      *scheduler.Issue
		repo, want string
	}{
		{&scheduler.Issue{Kind: scheduler.KindLinear, Identifier: "ENG-1"}, "acme/web", "linear create beside ENG-1 in acme/web"},
		{&scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "o/r#2"}, "acme/data", "github create beside o/r#2 in acme/data"},
		{&scheduler.Issue{Kind: scheduler.KindLocal, Identifier: "LOCAL-3"}, "acme/data", "local create beside LOCAL-3 in acme/data"},
		{nil, "acme/data", "linear create beside none in acme/data"},
		{nil, "acme/web", "github create beside none in acme/web"},
		{nil, "acme/notes", "local create beside none in acme/notes"},
		{nil, "acme/other", "local create beside none in acme/other"},
	} {
		calls = nil
		is, err := mux.CreateFix(ctx, c.build, c.repo, "Fix QA of X: t", "findings")
		kind, _, _ := strings.Cut(c.want, " ")
		if err != nil || is.Kind != kind || !slices.Equal(calls, []string{c.want + " Fix QA of X: t"}) {
			t.Errorf("%v in %s: issue %+v err %v calls %q, want %q", c.build, c.repo, is, err, calls, c.want)
		}
	}
}

// With no build Issue, a GitHub fix Issue goes to the Repo's own repository
// (PROD-QA-011).
func TestTheGitHubTrackerFilesAnAdHocFixIssueInTheRepo(t *testing.T) {
	dir := t.TempDir()
	args, bin := filepath.Join(dir, "args"), filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+args+"\necho https://github.com/acme/web/issues/9\n"), 0o755)
	g := githubTracker{c: &github.Client{Bin: bin}}
	for _, c := range []struct {
		build *scheduler.Issue
		want  string
	}{
		{nil, "acme/web"},
		{&scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "acme/api#4"}, "acme/api"},
	} {
		is, err := g.Create(context.Background(), c.build, "acme/web", "Fix QA of AH-65: Add billing", "The rows are missing.")
		if err != nil {
			t.Fatal(err)
		}
		sent, _ := os.ReadFile(args)
		if !strings.Contains(string(sent), "\n--repo\n"+c.want+"\n") || is.Identifier != c.want+"#9" {
			t.Errorf("build %v: gh got %q and gave %s, want the Issue in %s", c.build, sent, is.Identifier, c.want)
		}
	}
}

// The branch poll keeps the merge of a merged PR for its QA Task
// (ARCH-QA-005).
func TestTheBranchPollKeepsTheMergeCommit(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "github", "testdata", "merged.json"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\ncat "+fixture+"\n"), 0o755)
	key := scheduler.PRKey{Repo: "cli/cli", Branch: "b"}
	prs, err := (&githubPort{c: &github.Client{Bin: bin}}).Poll(context.Background(), []scheduler.PRKey{key})
	if err != nil {
		t.Fatal(err)
	}
	if pr := prs[key]; pr.MergeCommit != "93d24ce9ffefb6cc86bde89b50271457bb0ef9cb" || !pr.MergedAt.Equal(time.Date(2026, 10, 27, 14, 38, 50, 0, time.UTC)) {
		t.Fatalf("snapshot = %+v, want the merge", pr)
	}
}

// A Local fix Issue names the Repo, so lineup add maps it there.
func TestTheLocalTrackerFilesAFixIssueWithItsRepo(t *testing.T) {
	l := localTracker{s: localissues.Store{Dir: t.TempDir()}}
	is, err := l.Create(context.Background(), nil, "acme/data", "Fix QA of AH-65: Add billing", "The rows are missing.")
	if err != nil {
		t.Fatal(err)
	}
	if is.Identifier != "LOCAL-1" || is.RepoHint != "acme/data" || is.Title != "Fix QA of AH-65: Add billing" {
		t.Fatalf("issue %+v", is)
	}
	if d, _ := l.Description(context.Background(), is); d != "The rows are missing." {
		t.Fatalf("description %q", d)
	}
}

// A QA Worker's prompt quotes the build Task's Issue and names the merge and
// the QA worktree (PROD-QA-004).
func TestAQAWorkerGetsTheBuildIssueAndTheMerge(t *testing.T) {
	cfg := loadConfig(t, "[defaults.stage.verifying]\nskill = \"qa-check\"\n")
	var calls []string
	mux := trackerMux{kinds: map[string]issueTracker{scheduler.KindLinear: spyTracker{name: "linear", calls: &calls, desc: "The nightly load fills the column."}}}
	w := scheduler.WorkerSpec{Task: "QA-ENG-1", Kind: lifecycle.KindQA, Stage: lifecycle.Verifying, Repo: t.TempDir(),
		Worktree: "/repos/data.qa-pr-7", Issue: &scheduler.Issue{Kind: scheduler.KindLinear, Identifier: "ENG-1", Title: "Add billing"},
		PRURL: "https://github.com/acme/data/pull/7", MergeCommit: "93d24ce", MergedAt: time.Date(2026, 10, 27, 14, 38, 50, 0, time.UTC)}
	p := harnessPort{cfg: cfg, tracker: mux}
	prompt, err := p.Prompt(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/qa-check ", "> The nightly load fills the column.", "Merge commit: 93d24ce\n", "Merged at: 2026-10-27T14:38:50Z\n", "QA worktree: /repos/data.qa-pr-7\n"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	w.Recheck = true
	if again, _ := p.Prompt(context.Background(), w); strings.Contains(again, "nightly load") {
		t.Errorf("a later check repeats the Issue:\n%s", again)
	}
}

func TestTheMuxSendsEachCallOnlyToItsIssuesTracker(t *testing.T) {
	var calls []string
	mux := trackerMux{kinds: map[string]issueTracker{}, prefixes: []string{"hod"}}
	for _, k := range []string{scheduler.KindLinear, scheduler.KindGitHub, scheduler.KindLocal} {
		mux.kinds[k] = spyTracker{name: k, calls: &calls}
	}
	ctx := context.Background()
	for _, add := range []struct{ kind, id string }{
		{"", "ENG-614"}, {"", "o/r#12"}, {"", "LOCAL-7"}, {"", "HOD-3"},
		{scheduler.KindLinear, "LOCAL-7"}, // a Linear team keyed LOCAL has Issues such as LOCAL-7
	} {
		is, err := mux.Lookup(ctx, add.kind, add.id)
		if err != nil {
			t.Fatal(err)
		}
		mux.Begin(ctx, is)
		mux.SetStatus(ctx, is, "started")
		mux.Description(ctx, is)
	}
	var old scheduler.Issue // a Record from before Kind
	old.Identifier = "ENG-1"
	mux.SetStatus(ctx, old, "merged")
	want := []string{
		"linear lookup ENG-614", "linear begin ENG-614", "linear status:started ENG-614", "linear describe ENG-614",
		"github lookup o/r#12", "github begin o/r#12", "github status:started o/r#12", "github describe o/r#12",
		"local lookup LOCAL-7", "local begin LOCAL-7", "local status:started LOCAL-7", "local describe LOCAL-7",
		"local lookup HOD-3", "local begin HOD-3", "local status:started HOD-3", "local describe HOD-3",
		"linear lookup LOCAL-7", "linear begin LOCAL-7", "linear status:started LOCAL-7", "linear describe LOCAL-7",
		"linear status:merged ENG-1",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls\n%q\nwant\n%q", calls, want)
	}
}

func TestTheLocalTrackerWritesEachEventsStatus(t *testing.T) {
	l := localTracker{s: localissues.Store{Dir: t.TempDir()}}
	made, err := l.s.Create("Retry failed loads", "data", "Body.")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	is, err := l.Lookup(ctx, "LOCAL-1")
	if err != nil || is.Identifier != "LOCAL-1" || is.BranchName != made.Branch() || is.RepoHint != "data" {
		t.Fatalf("lookup %+v %v", is, err)
	}
	for _, step := range []struct {
		event string
		want  localissues.Status
	}{{"started", localissues.Started}, {"in_review", localissues.InReview}, {"closed", localissues.InReview}, {"merged", localissues.Done},
		{"qa_passed", localissues.Done}} {
		if err := l.SetStatus(ctx, is, step.event); err != nil {
			t.Fatal(err)
		}
		if got, _ := l.s.Get("LOCAL-1"); got.Status != step.want {
			t.Fatalf("after %s: status %q, want %q", step.event, got.Status, step.want)
		}
	}
	if d, _ := l.Description(ctx, is); d != "Body." {
		t.Fatalf("description %q", d)
	}
}

func TestAGitHubIssueMapsToTheCheckoutWhoseOriginIsItsRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	checkout := gitCheckout(t, filepath.Join(home, "code", "octo", "sandbox"), "git@github.com:octo/sandbox.git")
	gitCheckout(t, filepath.Join(home, "repos", "octo", "unlisted"), "git@github.com:octo/unlisted.git")
	p := configPort{cfg: loadConfig(t, "roots = [\"~/code\"]\n")}
	if _, err := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/unlisted#2"}); err == nil {
		t.Fatal("a checkout below no root mapped to a Repo")
	}
	if repo, err := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/sandbox#2"}); err != nil || repo != checkout {
		t.Fatalf("repo %q %v, want %s", repo, err, checkout)
	}
	if repo, _ := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "Octo/Sandbox#2"}); repo != checkout {
		t.Fatalf("another spelling of the repository gave %q, want the folder as it is on disk, %s", repo, checkout)
	}
	if _, err := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/elsewhere#2"}); !errors.Is(err, scheduler.ErrNoRepo) {
		t.Fatalf("an Issue with no checkout: err %v, want ErrNoRepo", err)
	}
	clone := gitCheckout(t, filepath.Join(home, "code", "old", "sandbox"), "https://github.com/octo/sandbox")
	want := "no Repo mapping for octo/sandbox#2: 2 checkouts have the key octo/sandbox: " + checkout + " and " + clone
	if _, err := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/sandbox#2"}); !errors.Is(err, scheduler.ErrNoRepo) || err.Error() != want {
		t.Fatalf("two clones of the repository: err %v, want %q", err, want)
	}
}

// An empty [repos."owner/repo"] table sets the Repo up. An owner/* table
// does not.
func TestARepoIsSetUpByItsOwnTable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := gitCheckout(t, filepath.Join(home, "code", "acme", "app"), "git@github.com:acme/app.git")
	api := gitCheckout(t, filepath.Join(home, "code", "acme", "api"), "git@github.com:acme/api.git")
	p := configPort{cfg: loadConfig(t, "roots = [\"~/code\"]\n\n[repos.\"acme/*\"]\nwip_cap = 2\n\n[repos.\"Acme/App\"]\n")}
	for repo, want := range map[string]bool{app: true, api: false} {
		if key, ok := p.SetUp(repo); ok != want || key != "acme/"+filepath.Base(repo) {
			t.Errorf("SetUp(%s) = %q %v, want %v", repo, key, ok, want)
		}
	}
}

func TestARepoNameIsAFolderNameAKeyOrAPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := gitCheckout(t, filepath.Join(home, "code", "acme", "app"), "git@github.com:acme/app.git")
	gitCheckout(t, filepath.Join(home, "code", "acme", "api"), "git@github.com:acme/api.git")
	globex := gitCheckout(t, filepath.Join(home, "code", "globex", "api"), "git@github.com:globex/api.git")
	gitCheckout(t, filepath.Join(home, "repos", "acme", "web"), "git@github.com:acme/web.git")
	p := configPort{cfg: loadConfig(t, "roots = [\"~/code\"]\n")}
	for name, want := range map[string]string{"app": app, "~/code/acme/app": app, "globex/api": globex} {
		if repo, err := p.ResolveRepo(name); err != nil || repo != want {
			t.Errorf("%s: %q %v, want %s", name, repo, err, want)
		}
	}
	if _, err := p.ResolveRepo("api"); err == nil || err.Error() != "2 checkouts named api: pass acme/api or globex/api" {
		t.Errorf("two checkouts named api: %v", err)
	}
	if _, err := p.ResolveRepo("web"); err == nil || err.Error() != "no checkout named web below roots ~/code: add its parent folder to roots or pass a path" {
		t.Errorf("a checkout below no root: %v", err)
	}
}

func gitCheckout(t *testing.T, dir, origin string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return dir
}

// claudeOnPath puts a fake claude first on PATH, with no direnv behind it, so that the default Claude profile applies.
func claudeOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return bin
}

func loadConfig(t *testing.T, text string) *config.Global {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(text), 0o644)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestReviewQueuesComeFromTheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
[[review.queues]]
name = "My teammates"
state = "open"
draft = false
repo = ["umbrella/data"]
author_team = ["umbrella/platform"]
review_requested = "@me"
include_regex = "DATA-"
exclude_regex = "WIP"
slots = 1

[[review.queues]]
name = "platform"
author = ["hopper"]
label = ["needs-review"]
exclude_regex = "HLESS-"
`), 0o644)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []scheduler.Queue{
		{Name: "My teammates", Slots: 1, Filter: github.Filter{Base: github.QueuePRs, Repo: []string{"umbrella/data"}, AuthorTeam: []string{"umbrella/platform"},
			ReviewRequested: "@me", IncludeRegex: "DATA-", ExcludeRegex: "WIP"}},
		{Name: "platform", Slots: 3, Filter: github.Filter{Base: github.QueuePRs, Author: []string{"hopper"}, Label: []string{"needs-review"}, ExcludeRegex: "HLESS-"}},
	}
	if got := reviewQueues(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("reviewQueues =\n%+v\nwant\n%+v, the second with the default 3 slots", got, want)
	}
}

func rexThatFails(t *testing.T, stderr string, code int) *rex.Client {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rex")
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &rex.Client{Bin: bin}
}

func TestBlocksReportsAMissingSessionAsNoBlocks(t *testing.T) {
	p := rexPort{rexThatFails(t, `Error: session target "session:1" not found`, 3)}
	live, err := p.Blocks(context.Background(), "session:1")
	if err != nil || len(live) != 0 {
		t.Fatalf("Blocks = %v, %v, want no blocks and no error", live, err)
	}
}

func TestBlocksReportsEveryOtherFailureAsAnError(t *testing.T) {
	missing := rexPort{&rex.Client{Bin: "lineup-no-such-rex"}}
	down := rexPort{rexThatFails(t, "Error: cannot connect to the rex server", 1)}
	for name, p := range map[string]rexPort{"missing binary": missing, "server down": down} {
		if live, err := p.Blocks(context.Background(), "session:1"); err == nil {
			t.Errorf("%s: Blocks = %v, nil, want an error", name, live)
		}
	}
}

func TestAPrefixedLocalIssueMapsToItsPrefixesRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	hod := gitCheckout(t, filepath.Join(home, "repos", "gclunies", "hod"), "git@github.com:gclunies/hod.git")
	cfg := loadConfig(t, "roots = [\"~/repos\"]\n[repos.\"gclunies/hod\"]\ntracker = \"local\"\nprefix = \"HOD\"\n")
	l := localTracker{s: localissues.Store{Dir: t.TempDir(), Prefixes: cfg.Prefixes()}}
	if err := os.WriteFile(filepath.Join(l.s.Dir, "hod-3.md"), []byte("---\ntitle: Block Linear writes\nstatus: todo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := trackerMux{kinds: map[string]issueTracker{scheduler.KindLocal: l}, prefixes: cfg.Prefixes()}
	is, err := mux.Lookup(context.Background(), "", "hod-3")
	if err != nil {
		t.Fatal(err)
	}
	if is.Kind != scheduler.KindLocal || is.Identifier != "HOD-3" || is.BranchName != "hod-3-block-linear-writes" ||
		is.URL != "file://"+filepath.Join(l.s.Dir, "hod-3.md") {
		t.Fatalf("lookup %+v", is)
	}
	p := configPort{cfg: cfg}
	if repo, err := p.RepoForIssue(context.Background(), is); err != nil || repo != hod {
		t.Fatalf("repo %q %v, want %s from the prefix, as the file names no repo", repo, err, hod)
	}
	if got := p.RepoPrefix(hod); got != "hod" {
		t.Fatalf("RepoPrefix = %q", got)
	}
	is.RepoHint = "data"
	if repo, _ := p.RepoForIssue(context.Background(), is); repo != hod {
		t.Fatalf("repo %q, want %s: the prefix names the Repo before the repo field", repo, hod)
	}
	if repo, err := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindLocal, Identifier: "LOCAL-1"}); err == nil {
		t.Fatalf("LOCAL-1 with no repo field mapped to %q", repo)
	}
	if _, err := p.RepoForIssue(context.Background(), scheduler.Issue{Kind: scheduler.KindLocal, Identifier: "LOCAL-1", RepoHint: "nope"}); !errors.Is(err, scheduler.ErrNoRepo) || !strings.Contains(err.Error(), "no checkout named nope") {
		t.Fatalf("LOCAL-1 with repo: nope: err %v, want ErrNoRepo that says why", err)
	}
}

// The Scheduler keeps a dirty worktree and tries again, so the port names
// that error in the Scheduler's terms.
func TestTheWorktreePortNamesUncommittedChanges(t *testing.T) {
	dir := gitCheckout(t, filepath.Join(t.TempDir(), "eng-1"), "git@github.com:umbrella/data.git")
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("draft\n"), 0o644)
	wt := filepath.Join(t.TempDir(), "wt")
	list := `{"items":[{"branch":"eng-1","worktree":{"path":"` + dir + `"}}]}`
	os.WriteFile(wt, []byte("#!/bin/sh\necho '"+list+"'\n"), 0o755)
	if err := (worktreePort{wt}).Remove(context.Background(), t.TempDir(), "eng-1"); !errors.Is(err, scheduler.ErrUncommitted) {
		t.Fatalf("Remove of a dirty worktree: err = %v, want scheduler.ErrUncommitted", err)
	}
}
