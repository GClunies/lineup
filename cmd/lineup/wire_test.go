package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/rex"
	"github.com/gclunies/lineup/internal/scheduler"
)

// spyTracker records each call it gets, as "<call> <identifier>".
type spyTracker struct {
	name  string
	calls *[]string
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
	return "", nil
}

func TestTheMuxSendsEachCallOnlyToItsIssuesTracker(t *testing.T) {
	var calls []string
	mux := trackerMux{}
	for _, k := range []string{scheduler.KindLinear, scheduler.KindGitHub, scheduler.KindLocal} {
		mux[k] = spyTracker{name: k, calls: &calls}
	}
	ctx := context.Background()
	for _, add := range []struct{ kind, id string }{
		{"", "ENG-614"}, {"", "o/r#12"}, {"", "local-7"},
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
		"local lookup local-7", "local begin local-7", "local status:started local-7", "local describe local-7",
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
	if err != nil || is.Identifier != "local-1" || is.BranchName != made.Branch() || is.RepoHint != "data" {
		t.Fatalf("lookup %+v %v", is, err)
	}
	for _, step := range []struct {
		event string
		want  localissues.Status
	}{{"started", localissues.Started}, {"in_review", localissues.InReview}, {"closed", localissues.InReview}, {"merged", localissues.Done}} {
		if err := l.SetStatus(ctx, is, step.event); err != nil {
			t.Fatal(err)
		}
		if got, _ := l.s.Get("local-1"); got.Status != step.want {
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
	p := configPort{cfg: loadConfig(t, "[[repos]]\nmatch = \"~/code/*/*\"\n")}
	if _, ok := p.RepoForIssue(scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/unlisted#2"}); ok {
		t.Fatal("a checkout that no [[repos]] match names mapped to a Repo")
	}
	if repo, ok := p.RepoForIssue(scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/sandbox#2"}); !ok || repo != checkout {
		t.Fatalf("repo %q %v, want %s", repo, ok, checkout)
	}
	if repo, _ := p.RepoForIssue(scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "Octo/Sandbox#2"}); repo != checkout {
		t.Fatalf("another spelling of the repository gave %q, want the folder as it is on disk, %s", repo, checkout)
	}
	if _, ok := p.RepoForIssue(scheduler.Issue{Kind: scheduler.KindGitHub, Identifier: "octo/elsewhere#2"}); ok {
		t.Fatal("an Issue with no checkout mapped to a Repo")
	}
	for url, want := range map[string]string{
		"https://github.com/o/r.git": "o/r", "https://github.com/o/r": "o/r", "git@github.com:o/r.git": "o/r", "https://gitlab.com/o/r": "",
	} {
		exec.Command("git", "-C", checkout, "remote", "set-url", "origin", url).Run()
		if got := originSlug(checkout); got != want {
			t.Errorf("originSlug(%s) = %q, want %q", url, got, want)
		}
	}
}

func TestARepoNameIsTheFolderNameOfOneReposCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := gitCheckout(t, filepath.Join(home, "code", "acme", "app"), "git@github.com:acme/app.git")
	gitCheckout(t, filepath.Join(home, "code", "acme", "api"), "git@github.com:acme/api.git")
	gitCheckout(t, filepath.Join(home, "code", "globex", "api"), "git@github.com:globex/api.git")
	gitCheckout(t, filepath.Join(home, "repos", "acme", "web"), "git@github.com:acme/web.git")
	p := configPort{cfg: loadConfig(t, "[[repos]]\nmatch = \"~/code/*/*\"\n")}
	if repo, err := p.ResolveRepo("app"); err != nil || repo != app {
		t.Fatalf("app: %q %v, want %s", repo, err, app)
	}
	if repo, err := p.ResolveRepo("~/code/acme/app"); err != nil || repo != app {
		t.Fatalf("a path: %q %v, want %s", repo, err, app)
	}
	if _, err := p.ResolveRepo("api"); err == nil || !strings.Contains(err.Error(), "2 [[repos]] checkouts") {
		t.Fatalf("two checkouts named api: %v", err)
	}
	if _, err := p.ResolveRepo("web"); err == nil {
		t.Fatal("a checkout that no [[repos]] match names resolved")
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
	no := false
	want := []scheduler.Queue{
		{Name: "My teammates", Slots: 1, Filter: github.Filter{Base: github.QueuePRs, Repo: []string{"umbrella/data"}, AuthorTeam: []string{"umbrella/platform"},
			State: "open", Draft: &no, ReviewRequested: "@me", IncludeRegex: "DATA-", ExcludeRegex: "WIP"}},
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
