package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

func write(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeBin(t *testing.T, dir, name string) string {
	t.Helper()
	return write(t, filepath.Join(dir, name), "#!/bin/sh\n")
}

func load(t *testing.T, text string) *Global {
	t.Helper()
	g, err := Load(write(t, filepath.Join(t.TempDir(), "config.toml"), text))
	if err != nil {
		t.Fatal(err)
	}
	g.home = "/home/u"
	bin := fakeBin(t, t.TempDir(), "claude")
	g.lookPath = func(string) (string, error) { return bin, nil }
	return g
}

func TestMissingFileMeansDefaults(t *testing.T) {
	g, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeBin(t, t.TempDir(), "claude")
	g.lookPath = func(string) (string, error) { return bin, nil }
	r, err := g.Resolve(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.WIPCap != 5 || r.PollInterval != 30*time.Second {
		t.Errorf("defaults wrong: %+v", r)
	}
	if r.Budgets != (lifecycle.Budgets{CIDebug: 3, Responding: 3, Restarts: 2}) {
		t.Errorf("budgets = %+v", r.Budgets)
	}
	want := StageConfig{Model: "claude-opus-5-5", Effort: "high"}
	for _, st := range []lifecycle.Stage{lifecycle.Working, lifecycle.CIDebug, lifecycle.Responding, lifecycle.Reviewing} {
		if r.Stages[st] != want {
			t.Errorf("%s = %+v", st, r.Stages[st])
		}
	}
	if len(r.Stages) != 4 || r.Claude != bin {
		t.Errorf("stages %d claude %v", len(r.Stages), r.Claude)
	}
}

func TestUnknownKeysAreAnError(t *testing.T) {
	for name, text := range map[string]string{
		"top":     "wip_capp = 3\n",
		"nested":  "[budgets]\nci_debugg = 1\n",
		"repo":    "[[repos]]\nmatch = \"/a\"\nbogus = 1\n",
		"stage":   "[stage.default]\nmodle = \"x\"\n",
		"removed": "auto_merge = true\n",
	} {
		_, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), text))
		if err == nil || !strings.Contains(err.Error(), "unknown keys") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestStartSkills(t *testing.T) {
	if got := load(t, "wip_cap = 3\n").StartSkills(); len(got) != 0 {
		t.Errorf("default = %v", got)
	}
	got := load(t, "start_skills = [\"kit:tdd\", \"diagnosing-bugs\"]\n").StartSkills()
	if !slices.Equal(got, []string{"kit:tdd", "diagnosing-bugs"}) {
		t.Errorf("start_skills = %v", got)
	}
	if _, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), "start_skills = [\"\"]\n")); err == nil {
		t.Error("an empty skill name must fail at load")
	}
}

func TestAssistDir(t *testing.T) {
	if got := load(t, "wip_cap = 3\n").AssistDir(); got != "/home/u" {
		t.Errorf("default = %q, want the home folder", got)
	}
	if got := load(t, "assist_dir = \"~/repos\"\n").AssistDir(); got != "/home/u/repos" {
		t.Errorf("assist_dir = %q, want /home/u/repos", got)
	}
}

func TestAdHocSession(t *testing.T) {
	if got := load(t, "wip_cap = 3\n").AdHocSession(); got != "ad hoc" {
		t.Errorf("default = %q", got)
	}
	if got := load(t, "ad_hoc_session = \"scratch\"\n").AdHocSession(); got != "scratch" {
		t.Errorf("ad_hoc_session = %q", got)
	}
}

func TestLinearClientID(t *testing.T) {
	if got := load(t, "wip_cap = 3\n").LinearClientID("acme"); got != "" {
		t.Errorf("default = %q", got)
	}
	g := load(t, "linear_client_id = \"abc123\"\n[linear.acme]\nclient_id = \"work-app\"\n")
	for ws, want := range map[string]string{"acme": "work-app", "home": "abc123", "": "abc123"} {
		if got := g.LinearClientID(ws); got != want {
			t.Errorf("LinearClientID(%q) = %q, want %q", ws, got, want)
		}
	}
}

func TestInvalidValuesFailAtLoad(t *testing.T) {
	for name, text := range map[string]string{
		"cap":        "wip_cap = 0\n",
		"interval":   "poll_interval = \"soon\"\n",
		"budget":     "[budgets]\nrestarts = -1\n",
		"stage":      "[stage.queued]\nmodel = \"x\"\n",
		"user stage": "[stage.review_ready]\nmodel = \"x\"\n",
		"event":      "[linear.w.status.ENG]\nbogus = \"x\"\n",
		"match":      "[[repos]]\nmatch = \"[\"\n",
	} {
		if _, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), text)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPrecedence(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".lineup.toml"), `
[stage.ci_debug]
skill = "diagnosing-bugs"
[stage.default]
effort = "max"
`)
	g := load(t, `
wip_cap = 7
poll_interval = "10s"
[budgets]
ci_debug = 1
[stage.default]
model = "global-model"
effort = "low"
[stage.responding]
model = "responding-model"
[[repos]]
match = "`+repo+`"
teams = ["APP"]
wip_cap = 2
[[repos]]
match = "`+filepath.Dir(repo)+`/*"
teams = ["X"]
wip_cap = 3
poll_interval = "20s"
`)
	r, err := g.Resolve(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.WIPCap != 2 {
		t.Errorf("most specific glob must win: cap %d", r.WIPCap)
	}
	if r.PollInterval != 20*time.Second || r.Budgets.CIDebug != 1 || r.Budgets.Responding != 3 {
		t.Errorf("merge of layers wrong: %+v", r)
	}
	if got := r.Stages[lifecycle.Working]; got.Model != "global-model" || got.Effort != "max" {
		t.Errorf("working = %+v", got)
	}
	if got := r.Stages[lifecycle.Responding]; got.Model != "responding-model" || got.Effort != "max" {
		t.Errorf("a stage section must beat any default, responding = %+v", got)
	}
	if got := r.Stages[lifecycle.CIDebug]; got.Skill != "diagnosing-bugs" {
		t.Errorf("ci_debug = %+v", got)
	}
}

func TestOverridesApplyLast(t *testing.T) {
	g := load(t, "[stage.working]\nmodel = \"m\"\n")
	r, err := g.Resolve(t.TempDir(), map[string]string{"model": "x"})
	if err != nil {
		t.Fatal(err)
	}
	for st, sc := range r.Stages {
		if sc.Model != "x" {
			t.Errorf("%s = %+v", st, sc)
		}
	}
	if !r.PR {
		t.Errorf("%+v", r)
	}
	if r, err := g.Resolve(t.TempDir(), map[string]string{"pr": "false"}); err != nil || r.PR {
		t.Errorf("pr=false: PR %v, err %v", r.PR, err)
	}
	if _, err := g.Resolve(t.TempDir(), map[string]string{"pr": "no"}); err == nil {
		t.Error("pr=no accepted")
	}
	if _, err := g.Resolve(t.TempDir(), map[string]string{"nope": "1"}); err == nil {
		t.Error("unknown override accepted")
	}
}

func TestHarnessKeyIsRejected(t *testing.T) {
	const msg = "harness is no longer a setting, lineup runs claude"
	for name, text := range map[string]string{
		"table":     "[harness.claude]\npath = \"/bin/claude\"\n",
		"stage key": "[stage.default]\nharness = \"pi\"\n",
		"repos key": "[[repos]]\nmatch = \"/r\"\n[repos.stage.working]\nharness = \"pi\"\n",
	} {
		global := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
		if _, err := Load(global); err == nil || err.Error() != global+": "+msg {
			t.Errorf("global %s: err = %v", name, err)
		}
	}
	for name, text := range map[string]string{
		"table":     "[harness.claude]\npath = \"/bin/claude\"\n",
		"stage key": "[stage.working]\nharness = \"pi\"\n",
	} {
		repo := t.TempDir()
		write(t, filepath.Join(repo, ".lineup.toml"), text)
		_, err := load(t, "").Resolve(repo, nil)
		if want := filepath.Join(repo, ".lineup.toml") + ": " + msg; err == nil || err.Error() != want {
			t.Errorf("repo %s: err = %v", name, err)
		}
	}
	if _, err := load(t, "").Resolve(t.TempDir(), map[string]string{"harness": "pi"}); err == nil || err.Error() != msg {
		t.Errorf("override: err = %v", err)
	}
}

func TestMissingClaudeIsAnError(t *testing.T) {
	g := load(t, "")
	g.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := g.Resolve(t.TempDir(), nil); err == nil {
		t.Fatal("no error")
	}
}

func TestRepoFileUnknownKeyIsAnError(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".lineup.toml"), "repos = []\n")
	if _, err := load(t, "").Resolve(repo, nil); err == nil {
		t.Fatal("no error")
	}
}

func TestTildeExpansion(t *testing.T) {
	g := load(t, `
[[repos]]
match = "~/repos/acme/*"
wip_cap = 3
[[repos]]
match = "~/repos/acme/data"
teams = ["APP", "ENG"]
wip_cap = 4
`)
	if repo, ok := g.RepoForTeam("ENG"); !ok || repo != "/home/u/repos/acme/data" {
		t.Errorf("RepoForTeam = %q %v", repo, ok)
	}
	if _, ok := g.RepoForTeam("NOPE"); ok {
		t.Error("unknown team found")
	}
	dir := t.TempDir()
	g.home = dir
	r, err := g.Resolve("~/repos/acme/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.WIPCap != 4 {
		t.Errorf("a ~ match must apply its layer: cap %d", r.WIPCap)
	}
}

func TestLinearStatus(t *testing.T) {
	g := load(t, `
[linear.acme.status.ENG]
started = "In Progress"
[linear.acme.status.APP]
started = "In Progress"
merged = "Done"
`)
	if s, ok := g.LinearStatus("acme", "APP", "merged"); !ok || s != "Done" {
		t.Errorf("%q %v", s, ok)
	}
	if _, ok := g.LinearStatus("acme", "ENG", "merged"); ok {
		t.Error("ENG merged must write nothing")
	}
	if _, ok := g.LinearStatus("nope", "ENG", "started"); ok {
		t.Error("unknown workspace")
	}
}

func TestRepoChoices(t *testing.T) {
	c := NewRepoChoices(filepath.Join(t.TempDir(), "state", "repos.json"))
	if _, ok, err := c.Get("p1"); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if err := c.Set("p1", "/r/a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("p2", "/r/b"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := NewRepoChoices(c.path).Get("p1"); !ok || v != "/r/a" {
		t.Errorf("%q %v", v, ok)
	}
}

func TestViewOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "views.json")
	if ids, err := NewViewOrder(path).Get(); ids != nil || err != nil {
		t.Fatalf("nothing saved: %v %v", ids, err)
	}
	if err := NewViewOrder(path).Set([]string{"cv1", "activeIssues:t1"}); err != nil {
		t.Fatal(err)
	}
	if err := NewViewOrder(path).Set([]string{"activeIssues:t1", "cv1"}); err != nil {
		t.Fatal(err)
	}
	ids, err := NewViewOrder(path).Get()
	if err != nil || strings.Join(ids, ",") != "activeIssues:t1,cv1" {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestTrackerKeys(t *testing.T) {
	g := load(t, "local_tracker = true\nlocal_issues_dir = \"~/plans\"\n[github.labels]\nstarted = \"in progress\"\n")
	if g.LocalIssuesDir() != "/home/u/plans" || g.GitHubLabels()["started"] != "in progress" {
		t.Fatalf("dir %q labels %v", g.LocalIssuesDir(), g.GitHubLabels())
	}
	if d := load(t, "wip_cap = 3\n").LocalIssuesDir(); d != "/home/u/.local/share/lineup/issues" {
		t.Errorf("default dir %q", d)
	}
	if _, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), "[github.labels]\nshipped = \"x\"\n")); err == nil {
		t.Error("an unknown event in github.labels loaded")
	}
}

func TestLocalTrackerOn(t *testing.T) {
	for _, c := range []struct {
		forced, linear, github, want bool
	}{
		{false, false, false, true},
		{false, true, false, false},
		{false, false, true, false},
		{false, true, true, false},
		{true, true, true, true},
		{true, false, false, true},
	} {
		text := fmt.Sprintf("local_tracker = %v\n", c.forced)
		if got := load(t, text).LocalTrackerOn(c.linear, c.github); got != c.want {
			t.Errorf("local_tracker=%v linear=%v github=%v: on=%v, want %v", c.forced, c.linear, c.github, got, c.want)
		}
	}
}

func TestCheckoutsAreTheGitFoldersThatReposMatch(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"code/acme/app/.git", "code/acme/notes", "code/globex/app/.git", "work/api/.git", "other/web/.git"} {
		os.MkdirAll(filepath.Join(root, dir), 0o755)
	}
	g := load(t, `
[[repos]]
match = "`+root+`/code/*/*"
[[repos]]
match = "`+root+`/work/api"
[[repos]]
match = "`+root+`/code/acme/app"
`)
	want := []string{root + "/code/acme/app", root + "/code/globex/app", root + "/work/api"}
	if got := g.Checkouts(); !slices.Equal(got, want) {
		t.Fatalf("Checkouts = %q, want %q", got, want)
	}
	if got := load(t, "wip_cap = 3\n").Checkouts(); len(got) != 0 {
		t.Fatalf("no [[repos]] gave %q", got)
	}
}

func TestReviewViewsAndQueues(t *testing.T) {
	g := load(t, `
[[review.views]]
name = "My teammates"
repos = ["umbrella/data"]
author_teams = ["umbrella/platform"]

[[review.views]]
name = "Data team"
requested_teams = ["umbrella/data"]
labels = ["needs-review", "breaking change"]

[[review.queues]]
name = "platform"
repos = ["umbrella/data"]
authors = ["hopper", "noether"]

[[review.queues]]
name = "one at a time"
authors = ["octo"]
slots = 1
`)
	wantViews := []ReviewView{
		{Name: "My teammates", PRFilter: PRFilter{Repos: []string{"umbrella/data"}, AuthorTeams: []string{"umbrella/platform"}}},
		{Name: "Data team", PRFilter: PRFilter{RequestedTeams: []string{"umbrella/data"}, Labels: []string{"needs-review", "breaking change"}}},
	}
	if got := g.ReviewViews(); !reflect.DeepEqual(got, wantViews) {
		t.Errorf("ReviewViews = %+v\nwant %+v", got, wantViews)
	}
	wantQueues := []ReviewQueue{
		{Name: "platform", PRFilter: PRFilter{Repos: []string{"umbrella/data"}, Authors: []string{"hopper", "noether"}}, Slots: 3},
		{Name: "one at a time", PRFilter: PRFilter{Authors: []string{"octo"}}, Slots: 1},
	}
	if got := g.ReviewQueues(); !reflect.DeepEqual(got, wantQueues) {
		t.Errorf("ReviewQueues = %+v\nwant %+v", got, wantQueues)
	}
	if got := load(t, "wip_cap = 3\n"); len(got.ReviewViews())+len(got.ReviewQueues()) != 0 {
		t.Errorf("no review config gave %+v %+v", got.ReviewViews(), got.ReviewQueues())
	}
}

func TestReviewSlotsAreTheDefaultOfEveryQueue(t *testing.T) {
	g := load(t, `
[review]
slots = 5

[[review.queues]]
name = "first"
authors = ["a"]

[[review.queues]]
name = "second"
repos = ["o/r"]

[[review.queues]]
name = "own"
repos = ["o/s"]
slots = 2
`)
	if g.ReviewSlots() != 5 {
		t.Errorf("ReviewSlots = %d, want 5", g.ReviewSlots())
	}
	var got []int
	for _, q := range g.ReviewQueues() {
		got = append(got, q.Slots)
	}
	if !slices.Equal(got, []int{5, 5, 2}) {
		t.Errorf("queue slots = %v, want [5 5 2]", got)
	}
	if got := load(t, "wip_cap = 3\n"); got.ReviewSlots() != 0 {
		t.Errorf("unset review.slots = %d, want 0", got.ReviewSlots())
	}
}

func TestBadReviewConfigFailsAtLoad(t *testing.T) {
	for name, c := range map[string]struct{ text, want string }{
		"unknown key":       {"[[review.views]]\nname = \"a\"\nrepos = [\"o/r\"]\nauthor = [\"x\"]\n", "unknown keys"},
		"unknown section":   {"[review.bogus]\nx = 1\n", "unknown keys"},
		"no filter":         {"[[review.views]]\nname = \"a\"\n", "review.views[0]: needs at least one"},
		"empty filter":      {"[[review.views]]\nname = \"a\"\nrepos = []\n", "review.views[0]: needs at least one"},
		"no name":           {"[[review.views]]\nrepos = [\"o/r\"]\n", "review.views[0]: name is required"},
		"team form":         {"[[review.views]]\nname = \"a\"\nauthor_teams = [\"platform\"]\n", `review.views[0]: author_teams[0] "platform" must be org/slug`},
		"requested form":    {"[[review.views]]\nname = \"a\"\nrequested_teams = [\"umbrella/data/x\"]\n", "requested_teams[0]"},
		"repo form":         {"[[review.views]]\nname = \"a\"\nrepos = [\"umbrella\"]\n", `repos[0] "umbrella" must be owner/name`},
		"repo space":        {"[[review.views]]\nname = \"a\"\nrepos = [\"umbrella/da ta\"]\n", "repos[0]"},
		"author space":      {"[[review.views]]\nname = \"a\"\nauthors = [\"a b\"]\n", "authors[0]"},
		"label quote":       {"[[review.views]]\nname = \"a\"\nlabels = [\"a\\\"b\"]\n", "labels[0]"},
		"duplicate view":    {"[[review.views]]\nname = \"a\"\nrepos = [\"o/r\"]\n[[review.views]]\nname = \"a\"\nrepos = [\"o/s\"]\n", `review.views[1]: name "a" is used twice`},
		"view and queue":    {"[[review.views]]\nname = \"a\"\nrepos = [\"o/r\"]\n[[review.queues]]\nname = \"a\"\nrepos = [\"o/s\"]\n", `review.queues[0]: name "a" is used twice`},
		"built-in name":     {"[[review.views]]\nname = \"My PRs\"\nrepos = [\"o/r\"]\n", "is used twice"},
		"slots zero":        {"[[review.queues]]\nname = \"q\"\nrepos = [\"o/r\"]\nslots = 0\n", "review.queues[0]: slots must be at least 1"},
		"shared slots zero": {"[review]\nslots = 0\n", "review.slots: slots must be at least 1"},
		"queue no filter":   {"[[review.queues]]\nname = \"q\"\nslots = 2\n", "review.queues[0]: needs at least one"},
		"queue unknown key": {"[[review.queues]]\nname = \"q\"\nrepos = [\"o/r\"]\nslot = 2\n", "unknown keys"},
	} {
		_, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), c.text))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestReviewInTheRepoFileIsAnError(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".lineup.toml"), "[[review.views]]\nname = \"a\"\nrepos = [\"o/r\"]\n")
	if _, err := load(t, "").Resolve(repo, nil); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("err = %v", err)
	}
}

func TestTheReviewingStageHasItsOwnSection(t *testing.T) {
	g := load(t, "[stage.default]\nmodel = \"m\"\n[stage.reviewing]\neffort = \"max\"\n")
	r, err := g.Resolve(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Stages[lifecycle.Reviewing]; got.Model != "m" || got.Effort != "max" {
		t.Errorf("reviewing = %+v, want the default model and its own effort", got)
	}
	if got := r.Stages[lifecycle.Working]; got.Effort != "high" {
		t.Errorf("[stage.reviewing] reached working: %+v", got)
	}
}

func TestReviewBotGateIsPerRepo(t *testing.T) {
	crexi, other := t.TempDir(), t.TempDir()
	g := load(t, `
[[repos]]
match = "`+crexi+`"

[repos.review_bot]
name = "greptile"
min_review_score = 5
max_review_cycles = 5
`)
	r, err := g.Resolve(crexi, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &lifecycle.ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: 5, TopScore: 5}
	if p := r.Policy(); !reflect.DeepEqual(p.Bar.Bot, want) || p.Budgets.ReviewCycles != 5 {
		t.Errorf("gate %+v cycles %d, want %+v and 5", p.Bar.Bot, p.Budgets.ReviewCycles, want)
	}
	if r, err := g.Resolve(other, nil); err != nil || r.Bar.Bot != nil || r.Budgets.ReviewCycles != 0 {
		t.Errorf("a Repo with no table: gate %+v cycles %d err %v", r.Bar.Bot, r.Budgets.ReviewCycles, err)
	}

	write(t, filepath.Join(crexi, ".lineup.toml"), "[review_bot]\nmin_review_score = 4\n")
	if r, err := g.Resolve(crexi, nil); err != nil || r.Bar.Bot.MinScore != 4 || r.Bar.Bot.Bot != "greptile" || r.Budgets.ReviewCycles != 5 {
		t.Errorf("the Repo file must override one key and keep the rest: %+v %+v %v", r.Bar.Bot, r.Budgets, err)
	}

	r, err = load(t, "[review_bot]\nname = \"greptile\"\n").Resolve(other, nil)
	if err != nil || r.Bar.Bot.MinScore != 5 || r.Budgets.ReviewCycles != 3 {
		t.Errorf("defaults: gate %+v cycles %d err %v, want the top score and 3 cycles", r.Bar.Bot, r.Budgets.ReviewCycles, err)
	}
}

func TestBadReviewBotFailsAtLoad(t *testing.T) {
	for name, text := range map[string]string{
		"unknown bot":      "[review_bot]\nname = \"coderabbit\"\n",
		"score below 1":    "[review_bot]\nname = \"greptile\"\nmin_review_score = 0\n",
		"score above top":  "[review_bot]\nname = \"greptile\"\nmin_review_score = 6\n",
		"negative cycles":  "[review_bot]\nname = \"greptile\"\nmax_review_cycles = -1\n",
		"unknown key":      "[review_bot]\nname = \"greptile\"\nmin_score = 4\n",
		"in a repos entry": "[[repos]]\nmatch = \"/a\"\n[repos.review_bot]\nname = \"greptile\"\nmin_review_score = 9\n",
	} {
		if _, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), text)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// One table can leave out a key for a lower layer to set, so the merged
// tables must still name a bot and keep its score in range.
func TestTheMergedReviewBotMustBeWhole(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".lineup.toml"), "[review_bot]\nmin_review_score = 4\n")
	if _, err := load(t, "").Resolve(repo, nil); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("no bot named: err %v", err)
	}
	write(t, filepath.Join(repo, ".lineup.toml"), "[review_bot]\nmin_review_score = 7\n")
	if _, err := load(t, "[review_bot]\nname = \"greptile\"\n").Resolve(repo, nil); err == nil {
		t.Error("a score above the bot's top score passed")
	}
}

func TestTheLandingBarDefaultsToTheUsersBar(t *testing.T) {
	r, err := load(t, "").Resolve(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := lifecycle.LandingBar{Checks: lifecycle.RequiredChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals}
	if r.Bar != want || r.Policy().Bar != want {
		t.Errorf("bar %+v, policy bar %+v, want %+v", r.Bar, r.Policy().Bar, want)
	}
}

func TestALandingBarLayerSetsOneKeyAndKeepsTheRest(t *testing.T) {
	oss, other := t.TempDir(), t.TempDir()
	g := load(t, `
[landing_bar]
checks = "all"

[[repos]]
match = "`+oss+`"

[repos.landing_bar]
threads = "resolved"
`)
	r, err := g.Resolve(oss, nil)
	if want := (lifecycle.LandingBar{Checks: lifecycle.AllChecks, Threads: lifecycle.ResolvedThreads, Approvals: lifecycle.RequiredApprovals}); err != nil || r.Bar != want {
		t.Errorf("the repos entry: %+v %v, want %+v", r.Bar, err, want)
	}
	if r, err := g.Resolve(other, nil); err != nil || r.Bar.Threads != lifecycle.RepliedThreads || r.Bar.Checks != lifecycle.AllChecks {
		t.Errorf("a Repo that the entry does not match: %+v %v", r.Bar, err)
	}

	write(t, filepath.Join(oss, ".lineup.toml"), "[landing_bar]\napprovals = \"requested\"\nthreads = \"ignored\"\n")
	r, err = g.Resolve(oss, nil)
	if want := (lifecycle.LandingBar{Checks: lifecycle.AllChecks, Threads: lifecycle.IgnoredThreads, Approvals: lifecycle.RequestedApprovals}); err != nil || r.Bar != want {
		t.Errorf("the Repo file: %+v %v, want %+v", r.Bar, err, want)
	}
}

func TestBadLandingBarFailsAtLoad(t *testing.T) {
	for text, want := range map[string]string{
		"[landing_bar]\nchecks = \"some\"\n":                               `global: landing_bar.checks: "some" is not one of all, required`,
		"[landing_bar]\nthreads = \"any\"\n":                               `global: landing_bar.threads: "any" is not one of ignored, resolved, replied`,
		"[landing_bar]\napprovals = \"all\"\n":                             `global: landing_bar.approvals: "all" is not one of requested, required`,
		"[landing_bar]\nreviews = \"required\"\n":                          "unknown keys: landing_bar.reviews",
		"[[repos]]\nmatch = \"/a\"\n[repos.landing_bar]\nchecks = \"x\"\n": `repos[0]: landing_bar.checks: "x" is not one of all, required`,
	} {
		_, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), text))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".lineup.toml"), "[landing_bar]\nthreads = \"open\"\n")
	if _, err := load(t, "").Resolve(repo, nil); err == nil || !strings.Contains(err.Error(), `.lineup.toml: landing_bar.threads: "open" is not one of`) {
		t.Errorf("the Repo file: err = %v", err)
	}
}
