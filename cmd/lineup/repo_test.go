package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/worktree"
)

func TestDetectDefaultsPutTheLowerLayersFirstThenGitHub(t *testing.T) {
	four, five := 4, 5
	greptile := func(min, cycles int) *reviewBot {
		return &reviewBot{Name: "greptile", MinReviewScore: min, MaxReviewCycles: cycles}
	}
	userBar := landingBar{Checks: "required", Threads: "replied", Approvals: "required"}
	allBar := landingBar{Checks: "all", Threads: "replied", Approvals: "required"}
	for _, c := range []struct {
		name  string
		in    config.Inherited
		facts *github.RepoFacts
		want  detected
	}{
		{"GitHub finds each default", config.Inherited{}, &github.RepoFacts{HasIssues: true, ReviewBots: []string{"greptile"}},
			detected{Tracker: "github", ReviewBot: greptile(5, 3), LandingBar: allBar,
				Init: []string{"tracker=github", "review_bot.name=greptile", "landing_bar.checks=all"}}},
		{"no Issues, no bot, and required checks keep the defaults", config.Inherited{}, &github.RepoFacts{RequiredChecks: []string{"test"}},
			detected{LandingBar: userBar, Init: []string{}}},
		{"the lower layers beat GitHub and need no init",
			config.Inherited{Tracker: "linear", ReviewBot: config.InheritedBot{Name: "greptile", MaxReviewCycles: &five}, LandingBar: config.InheritedBar{Checks: "required", Threads: "resolved"}},
			&github.RepoFacts{HasIssues: true, ReviewBots: []string{"greptile"}},
			detected{Tracker: "linear", ReviewBot: greptile(5, 5), LandingBar: landingBar{Checks: "required", Threads: "resolved", Approvals: "required"}, Init: []string{}}},
		{"an inherited score applies to the detected bot", config.Inherited{ReviewBot: config.InheritedBot{MinReviewScore: &four}},
			&github.RepoFacts{RequiredChecks: []string{"test"}, ReviewBots: []string{"greptile"}},
			detected{ReviewBot: greptile(4, 3), LandingBar: userBar, Init: []string{"review_bot.name=greptile"}}},
		{"a Repo with no GitHub origin detects nothing", config.Inherited{}, nil,
			detected{LandingBar: userBar, Init: []string{}}},
	} {
		if got := detectDefaults(c.in, c.facts); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestReadyHooksKeepThePreStartAndCopyHooks(t *testing.T) {
	got := readyHooks([]worktree.Hook{
		{Type: "pre-switch", Source: "user", Name: "fresh-base", Template: "git pull"},
		{Type: "pre-start", Source: "user", Name: "direnv", Template: "direnv allow"},
		{Type: "post-start", Source: "project", Name: "copy", Template: "wt step copy-ignored"},
	}, nil)
	want := worktreeHooks{SetUp: true, Hooks: []wtHook{{"pre-start", "user", "direnv"}, {"post-start", "project", "copy"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := readyHooks([]worktree.Hook{{Type: "post-switch", Name: "x"}}, nil); got.SetUp || len(got.Hooks) != 0 {
		t.Errorf("no setup hook: %+v", got)
	}
}

const repoConfig = `roots = ["~/repos"]

[repos."acme/*"]
[repos."acme/*".review_bot]
max_review_cycles = 5

[repos."acme/data"]
tracker = "linear"
teams = ["PD"]
`

// repoHome makes a home folder with the checkout repos/acme/web, and a
// config file. calls records the Repo that each GitHub read names.
func repoHome(t *testing.T) (env repoEnv, home string, calls *[]string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	gitCheckout(t, filepath.Join(home, "repos", "acme", "web"), "git@github.com:acme/web.git")
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(repoConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	calls = &[]string{}
	env = repoEnv{
		configPath: path,
		trackers:   func() (trackersOn, error) { return trackersOn{linear: true, github: true}, nil },
		facts: func(_ context.Context, repo string) (github.RepoFacts, error) {
			*calls = append(*calls, "facts "+repo)
			return github.RepoFacts{HasIssues: true, DefaultBranch: "main", ReviewBots: []string{"greptile"}}, nil
		},
		hooks: func(_ context.Context, checkout string) ([]worktree.Hook, error) {
			*calls = append(*calls, "hooks "+checkout)
			return []worktree.Hook{{Type: "pre-start", Source: "user", Name: "direnv", Template: "direnv allow"}}, nil
		},
		teams: func(context.Context) ([]linearTeam, error) {
			return []linearTeam{{Workspace: "acme", Key: "PD", Name: "Platform Data"}, {Workspace: "acme", Key: "WEB", Name: "Web"}}, nil
		},
		restart: func() error { *calls = append(*calls, "restart"); return nil },
	}
	return env, home, calls
}

func TestRepoDetectPrintsTheSetupAsJSON(t *testing.T) {
	env, home, calls := repoHome(t)
	var out bytes.Buffer
	if err := cmdRepo(context.Background(), []string{"detect", "acme/web", "--json"}, &out, env); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(home, "repos", "acme", "web")
	want := `{"repo":"acme/web","checkout":` + quote(web) + `,"trackers_on":["linear","github"],"tracker":"github",
		"review_bot":{"name":"greptile","min_review_score":5,"max_review_cycles":5},
		"landing_bar":{"checks":"all","threads":"replied","approvals":"required"},
		"prefix":"",
		"linear_teams":[{"workspace":"acme","key":"PD","name":"Platform Data","repos":["acme/data"]},{"workspace":"acme","key":"WEB","name":"Web"}],
		"worktree":{"hooks":[{"type":"pre-start","source":"user","name":"direnv"}],"set_up":true},
		"init":["tracker=github","review_bot.name=greptile","landing_bar.checks=all"]}`
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(want)); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != compact.String() {
		t.Errorf("got  %s\nwant %s", got, compact.String())
	}
	if want := []string{"facts acme/web", "hooks " + web}; !slices.Equal(*calls, want) {
		t.Errorf("calls %q, want %q", *calls, want)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestRepoDetectReportsAWorktrunkFailureInTheOutput(t *testing.T) {
	env, _, _ := repoHome(t)
	env.hooks = func(context.Context, string) ([]worktree.Hook, error) { return nil, worktree.ErrMissing }
	var out bytes.Buffer
	if err := cmdRepo(context.Background(), []string{"detect", "acme/web", "--json"}, &out, env); err != nil {
		t.Fatal(err)
	}
	var d detected
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if want := (worktreeHooks{Hooks: []wtHook{}, Error: worktree.ErrMissing.Error()}); !reflect.DeepEqual(d.Worktree, want) {
		t.Errorf("worktree %+v, want %+v", d.Worktree, want)
	}
}

func TestRepoDetectAsksGitHubOnlyForAGitHubRepo(t *testing.T) {
	env, home, calls := repoHome(t)
	dir := filepath.Join(home, "repos", "notes")
	os.MkdirAll(dir, 0o755)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	env.trackers = func() (trackersOn, error) { return trackersOn{local: true}, nil }
	var out bytes.Buffer
	if err := cmdRepo(context.Background(), []string{"detect", "local/notes", "--json"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(*calls, func(c string) bool { return strings.HasPrefix(c, "facts") }) {
		t.Errorf("calls %q: a local Repo asked GitHub", *calls)
	}
	var d detected
	json.Unmarshal(out.Bytes(), &d)
	if d.Tracker != "" || d.ReviewBot != nil || d.LandingBar.Checks != "required" || d.Teams != nil || !slices.Equal(d.TrackersOn, []string{"local"}) {
		t.Errorf("detected %+v, want no detected value and no Linear teams", d)
	}
	if d.Prefix != "not" || !slices.Equal(d.Init, []string{"prefix=not"}) {
		t.Errorf("prefix %q, init %q, want the suggested prefix not in both", d.Prefix, d.Init)
	}
}

func TestRepoDetectNeedsACheckout(t *testing.T) {
	env, _, _ := repoHome(t)
	err := cmdRepo(context.Background(), []string{"detect", "acme/api", "--json"}, &bytes.Buffer{}, env)
	if err == nil || err.Error() != "no checkout of acme/api below the roots" {
		t.Errorf("err = %v", err)
	}
	if err := cmdRepo(context.Background(), []string{"detect", "acme/web"}, &bytes.Buffer{}, env); !errors.Is(err, errUsage) {
		t.Errorf("no --json: err = %v, want the usage", err)
	}
}

func TestRepoInitDefaultsWritesTheDetectedValuesAndRestarts(t *testing.T) {
	env, home, calls := repoHome(t)
	var out bytes.Buffer
	if err := cmdRepo(context.Background(), []string{"init", "acme/web", "--defaults"}, &out, env); err != nil {
		t.Fatal(err)
	}
	table := "[repos.\"acme/web\"]\ntracker = \"github\"\n[repos.\"acme/web\".review_bot]\nname = \"greptile\"\n[repos.\"acme/web\".landing_bar]\nchecks = \"all\"\n"
	if want := "Added to " + env.configPath + ":\n\n" + table + "\nThe Scheduler restarted.\n"; out.String() != want {
		t.Errorf("out:\n%s\nwant:\n%s", out.String(), want)
	}
	if (*calls)[len(*calls)-1] != "restart" {
		t.Errorf("calls %q, want a restart last", *calls)
	}
	g, err := config.Load(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.SetUp(filepath.Join(home, "repos", "acme", "web")); !ok {
		t.Error("acme/web is not set up after init")
	}
}

func TestRepoInitWritesTheGivenKeysAndRestartsOnlyAfterAWrite(t *testing.T) {
	env, _, calls := repoHome(t)
	var out bytes.Buffer
	if err := cmdRepo(context.Background(), []string{"init", "acme/web"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[repos.\"acme/web\"]\n\nThe Scheduler restarted.") || !slices.Equal(*calls, []string{"restart"}) {
		t.Errorf("an empty table: out %q, calls %q", out.String(), *calls)
	}
	*calls = nil
	for _, args := range [][]string{
		{"init", "acme/web"},
		{"init", "acme/api", "landing_bar.checks=bogus"},
		{"init", "acme/api", "--defaults", "tracker=github"},
	} {
		if err := cmdRepo(context.Background(), args, &bytes.Buffer{}, env); err == nil {
			t.Errorf("%q: no error", args)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls %q, want no restart and no detect after a refusal", *calls)
	}
}
