package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

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

// loadFixture loads the config text from a home folder that fixtureHome
// makes, and returns the home folder.
func loadFixture(t *testing.T, text string) (*Global, string) {
	t.Helper()
	home := fixtureHome(t)
	g, err := Load(write(t, filepath.Join(home, ".config", "lineup", "config.toml"), text))
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeBin(t, t.TempDir(), "claude")
	g.lookPath = func(string) (string, error) { return bin, nil }
	return g, home
}

func loadErr(t *testing.T, text string) error {
	t.Helper()
	_, err := Load(write(t, filepath.Join(t.TempDir(), "c.toml"), text))
	return err
}

// gitCheckout makes a git checkout at home/rel with the origin, or with no
// origin when origin is "".
func gitCheckout(t *testing.T, home, rel, origin string) {
	t.Helper()
	dir := filepath.Join(home, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q")
	if origin != "" {
		gitRun(t, dir, "remote", "add", "origin", origin)
	}
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
	if r.Budgets != (lifecycle.Budgets{CIDebug: 3, Responding: 3}) {
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
		"top":      "wip_capp = 3\n",
		"defaults": "[defaults]\nwip_capp = 3\n",
		"nested":   "[defaults.budgets]\nci_debugg = 1\n",
		"repo":     "[repos.\"o/r\"]\nbogus = 1\n",
		"stage":    "[defaults.stage.default]\nmodle = \"x\"\n",
		"removed":  "auto_merge = true\n",
	} {
		if err := loadErr(t, text); err == nil || !strings.Contains(err.Error(), "unknown keys") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestStartSkills(t *testing.T) {
	if got := load(t, "").StartSkills(); len(got) != 0 {
		t.Errorf("default = %v", got)
	}
	got := load(t, "start_skills = [\"kit:tdd\", \"diagnosing-bugs\"]\n").StartSkills()
	if !slices.Equal(got, []string{"kit:tdd", "diagnosing-bugs"}) {
		t.Errorf("start_skills = %v", got)
	}
	if err := loadErr(t, "start_skills = [\"\"]\n"); err == nil {
		t.Error("an empty skill name must fail at load")
	}
}

func TestAnAdHocSessionIsAnUnknownKey(t *testing.T) {
	err := loadErr(t, "ad_hoc_session = \"scratch\"\n")
	if err == nil || !strings.Contains(err.Error(), "unknown keys: ad_hoc_session. An ad hoc Task opens in its Repo's session") {
		t.Fatalf("err = %v, want unknown keys: ad_hoc_session with the hint", err)
	}
}

func TestLinearClientID(t *testing.T) {
	if got := load(t, "").LinearClientID("acme"); got != "" {
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
		"cap":        "[defaults]\nwip_cap = 0\n",
		"interval":   "[defaults]\npoll_interval = \"soon\"\n",
		"budget":     "[defaults.budgets]\nci_debug = -1\n",
		"stage":      "[defaults.stage.queued]\nmodel = \"x\"\n",
		"user stage": "[defaults.stage.review_ready]\nmodel = \"x\"\n",
		"repo cap":   "[repos.\"o/r\"]\nwip_cap = 0\n",
		"event":      "[linear.w.status.ENG]\nbogus = \"x\"\n",
	} {
		if err := loadErr(t, text); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := loadErr(t, "roots = [\"~/repos\", \" \"]\n"); err == nil || !strings.HasSuffix(err.Error(), "roots[1] is empty") {
		t.Errorf("an empty root: err = %v", err)
	}
}

func TestARestartsBudgetIsAnUnknownKey(t *testing.T) {
	err := loadErr(t, "[defaults.budgets]\nrestarts = 2\n")
	if err == nil || !strings.Contains(err.Error(), "unknown keys: defaults.budgets.restarts") {
		t.Fatalf("err = %v, want unknown keys: defaults.budgets.restarts", err)
	}
}

// precedenceConfig sets keys at each level: [defaults], crexi/*, and
// crexi/data. Each table sets only some of its keys.
const precedenceConfig = `
roots = ["~/repos"]

[defaults]
wip_cap = 7
poll_interval = "10s"
[defaults.budgets]
ci_debug = 1
[defaults.stage.default]
model = "global-model"
effort = "low"
[defaults.stage.working]
model = "working-model"

[repos."crexi/*"]
wip_cap = 3
poll_interval = "20s"
[repos."crexi/*".stage.default]
effort = "max"
[repos."crexi/*".review_bot]
name = "greptile"

[repos."crexi/data"]
wip_cap = 2
teams = ["APP"]
[repos."crexi/data".stage.ci_debug]
skill = "diagnosing-bugs"
[repos."crexi/data".review_bot]
min_review_score = 4
`

func TestPrecedence(t *testing.T) {
	g, home := loadFixture(t, precedenceConfig)
	resolve := func(rel string, overrides map[string]string) Resolved {
		t.Helper()
		r, err := g.Resolve(filepath.Join(home, rel), overrides)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := resolve("repos/crexi/data", nil)
	if r.WIPCap != 2 {
		t.Errorf("the owner/repo table must beat the owner/* table: cap %d", r.WIPCap)
	}
	if r.PollInterval != 20*time.Second || r.Budgets.CIDebug != 1 || r.Budgets.Responding != 3 {
		t.Errorf("each key must layer on its own: %+v", r)
	}
	if got := r.Stages[lifecycle.Working]; got.Model != "working-model" || got.Effort != "max" {
		t.Errorf("a stage section must beat any default, working = %+v", got)
	}
	if got := r.Stages[lifecycle.Responding]; got.Model != "global-model" || got.Effort != "max" {
		t.Errorf("responding = %+v", got)
	}
	if got := r.Stages[lifecycle.CIDebug]; got.Skill != "diagnosing-bugs" {
		t.Errorf("ci_debug = %+v", got)
	}
	if b := r.Bar.Bot; b == nil || b.Bot != "greptile" || b.MinScore != 4 {
		t.Errorf("the review_bot keys of two levels must merge: %+v", b)
	}

	r = resolve("repos/crexi/web", nil)
	if r.WIPCap != 3 || r.PollInterval != 20*time.Second || r.Stages[lifecycle.CIDebug].Skill != "" {
		t.Errorf("crexi/web takes the owner/* table only: %+v", r)
	}
	if b := r.Bar.Bot; b == nil || b.MinScore != 5 {
		t.Errorf("crexi/web bot = %+v, want greptile at its top score", b)
	}

	r = resolve("repos/gclunies/lineup", nil)
	if r.WIPCap != 7 || r.PollInterval != 10*time.Second || r.Stages[lifecycle.Responding].Effort != "low" || r.Bar.Bot != nil {
		t.Errorf("GClunies/lineup takes [defaults] only: %+v", r)
	}

	r = resolve("repos/crexi/data", map[string]string{"model": "task-model"})
	for st, sc := range r.Stages {
		if sc.Model != "task-model" {
			t.Errorf("a Task override must beat each table: %s = %+v", st, sc)
		}
	}
}

func TestARepoKeyIgnoresCase(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\"]\n[repos.\"gclunies/HOD\"]\nwip_cap = 2\n[repos.\"CREXI/*\"]\nwip_cap = 3\n")
	for rel, want := range map[string]int{"repos/gclunies/hod": 2, "repos/crexi/web": 3, "repos/gclunies/lineup": 5} {
		if r, err := g.Resolve(filepath.Join(home, rel), nil); err != nil || r.WIPCap != want {
			t.Errorf("%s: cap %d %v, want %d", rel, r.WIPCap, err, want)
		}
	}
	err := loadErr(t, "[repos.\"crexi/data\"]\nwip_cap = 2\n[repos.\"Crexi/Data\"]\nwip_cap = 3\n")
	if want := `repos."crexi/data": repos."Crexi/Data" names the same Repo`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("two keys that differ in case: err = %v, want %q", err, want)
	}
}

func TestACheckoutWithNoGitHubOriginIsLocal(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\"]\n[repos.\"local/brain\"]\nwip_cap = 9\n[repos.\"local/*\"]\npoll_interval = \"5s\"\n")
	r, err := g.Resolve(filepath.Join(home, "repos/gclunies/brain"), nil)
	if err != nil || r.WIPCap != 9 || r.PollInterval != 5*time.Second {
		t.Fatalf("brain: %+v %v, want its local/brain and local/* tables", r, err)
	}
	if r, _ := g.Resolve(filepath.Join(home, "repos/gclunies/hod"), nil); r.WIPCap != 5 || r.PollInterval != 30*time.Second {
		t.Errorf("hod has a GitHub origin, so no local table applies: %+v", r)
	}
}

func TestARepoKeyIsOwnerRepoOrOwnerStar(t *testing.T) {
	for _, key := range []string{"crexi", "*/data", "*/*", "crexi/da ta", "crexi/d*", "crexi/data/x", "/data", "crexi/", "cr?xi/data", "crexi/**"} {
		err := loadErr(t, "[repos."+strconv.Quote(key)+"]\nwip_cap = 2\n")
		if want := "repos." + strconv.Quote(key) + ": a Repo key is owner/repo or owner/*"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", key, err, want)
		}
	}
	load(t, "[repos.\"crexi/*\"]\nwip_cap = 2\n[repos.\"crexi/data.review\"]\nwip_cap = 3\n")
}

func TestTheOldFormFailsAtLoadAndNamesTheMigration(t *testing.T) {
	for text, want := range map[string]string{
		"[[repos]]\nmatch = \"~/repos/*/*\"\n":              `[[repos]] tables are gone. Run lineup config migrate to rewrite the file with roots and [repos."owner/repo"] tables`,
		"wip_cap = 3\n[[repos]]\nmatch = \"~/r/*\"\n":       "[[repos]] tables are gone. Run lineup config migrate",
		"start_skills = [\"x\"]\nwip_cap = 3\n":             "wip_cap belongs in [defaults]. Run lineup config migrate to move it",
		"poll_interval = \"10s\"\nwip_cap = 3\n":            "poll_interval belongs in [defaults]. Run lineup config migrate to move it",
		"[budgets]\nci_debug = 1\n":                         "budgets belongs in [defaults]",
		"[stage.default]\nmodel = \"m\"\n":                  "stage belongs in [defaults]",
		"stage.default = { model = \"m\" }\n":               "stage belongs in [defaults]",
		"[review_bot]\nname = \"greptile\"\n":               "review_bot belongs in [defaults]",
		"[github.labels]\nstarted = \"x\"\n[landing_bar]\n": "landing_bar belongs in [defaults]",
	} {
		path := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
		if _, err := Load(path); err == nil || !strings.HasPrefix(err.Error(), path+": "+want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
}

func TestALaterStageModelIsRejected(t *testing.T) {
	for _, key := range []string{"ci_debug", "responding"} {
		for _, field := range []string{"model", "effort"} {
			text := fmt.Sprintf("[defaults.stage.%s]\n%s = \"x\"\n", key, field)
			want := fmt.Sprintf("stage.%s.%s: a Task keeps the %s of the Stage its Worker starts in", key, field, field)
			if err := loadErr(t, text); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s.%s: err = %v, want %q", key, field, err, want)
			}
		}
	}
	if err := loadErr(t, "[repos.\"o/r\".stage.ci_debug]\nmodel = \"x\"\n"); err == nil || !strings.Contains(err.Error(), `repos."o/r": stage.ci_debug.model`) {
		t.Errorf("a Repo table: err = %v, want the stage.ci_debug.model message", err)
	}
	g := load(t, "[defaults.stage.default]\nmodel = \"d\"\n[defaults.stage.working]\neffort = \"max\"\n[defaults.stage.reviewing]\nmodel = \"r\"\n[defaults.stage.ci_debug]\nskill = \"diagnosing-bugs\"\n")
	r, err := g.Resolve(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Stages[lifecycle.CIDebug]; got.Skill != "diagnosing-bugs" || got.Model != "d" {
		t.Errorf("ci_debug = %+v, want its skill and the default model", got)
	}
}

func TestOverridesApplyLast(t *testing.T) {
	g := load(t, "[defaults.stage.working]\nmodel = \"m\"\n")
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
		"stage key": "[defaults.stage.default]\nharness = \"pi\"\n",
		"repos key": "[repos.\"o/r\".stage.working]\nharness = \"pi\"\n",
	} {
		global := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
		if _, err := Load(global); err == nil || err.Error() != global+": "+msg {
			t.Errorf("global %s: err = %v", name, err)
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

func TestTildeExpansion(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\"]\n[repos.\"crexi/data\"]\nteams = [\"APP\", \"ENG\"]\nwip_cap = 4\n")
	if repo, ok := g.RepoForTeam("ENG"); !ok || repo != filepath.Join(home, "repos/crexi/data") {
		t.Errorf("RepoForTeam = %q %v", repo, ok)
	}
	if _, ok := g.RepoForTeam("NOPE"); ok {
		t.Error("unknown team found")
	}
	r, err := g.Resolve("~/repos/crexi/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.WIPCap != 4 {
		t.Errorf("a ~ path must name its checkout: cap %d", r.WIPCap)
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

func TestTrackerKeys(t *testing.T) {
	g := load(t, "local_tracker = true\nlocal_issues_dir = \"~/plans\"\n[github.labels]\nstarted = \"in progress\"\n")
	if g.LocalIssuesDir() != "/home/u/plans" || g.GitHubLabels()["started"] != "in progress" {
		t.Fatalf("dir %q labels %v", g.LocalIssuesDir(), g.GitHubLabels())
	}
	if d := load(t, "").LocalIssuesDir(); d != "/home/u/.local/share/lineup/issues" {
		t.Errorf("default dir %q", d)
	}
	if err := loadErr(t, "[github.labels]\nshipped = \"x\"\n"); err == nil {
		t.Error("an unknown event in github.labels loaded")
	}
}

func TestGitHubBacklogLabel(t *testing.T) {
	g := load(t, "[github.labels]\nstarted = \"in progress\"\nbacklog = \"later\"\n")
	if g.GitHubBacklogLabel() != "later" {
		t.Errorf("backlog %q", g.GitHubBacklogLabel())
	}
	if got := g.GitHubLabels(); got["started"] != "in progress" {
		t.Errorf("lifecycle labels %v", got)
	}
	if _, ok := g.GitHubLabels()["backlog"]; ok {
		t.Error("backlog is a lifecycle label, so an event would remove it")
	}
	if l := load(t, "").GitHubBacklogLabel(); l != "backlog" {
		t.Errorf("default backlog %q", l)
	}
	if l := load(t, "[github.labels]\nbacklog = \"\"\n").GitHubBacklogLabel(); l != "" {
		t.Errorf("empty backlog %q", l)
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

func TestCheckoutsAreTheMainCheckoutsBelowTheRoots(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\", \"~/solo\"]\n")
	gitRun(t, filepath.Join(home, "repos/gclunies/lineup"), "worktree", "add", "-q", "--orphan", "-b", "wt", filepath.Join(home, "repos/gclunies/lineup.wt"))
	gitCheckout(t, home, "solo", "https://github.com/o/solo")
	gitCheckout(t, home, "repos/acme/app/deep", "")
	gitCheckout(t, home, "repos/.cache/web", "")
	gitCheckout(t, home, "repos/gclunies/lineup/vendor/inner", "")
	var got []string
	for _, c := range g.Checkouts() {
		rel, _ := filepath.Rel(home, c.Path)
		got = append(got, rel+" "+c.Key)
	}
	want := []string{
		"repos/crexi-dev/de-interview-exercise crexi-dev/de-interview-exercise",
		"repos/crexi/data crexi/data",
		"repos/crexi/de-interview-exercise crexi/de-interview-exercise",
		"repos/crexi/web crexi/web",
		"repos/gclunies/brain local/brain",
		"repos/gclunies/hod GClunies/hod",
		"repos/gclunies/lineup GClunies/lineup",
		"solo o/solo",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Checkouts =\n%s\nwant\n%s\n(a root can be a checkout, a linked worktree is none, and the walk skips dot folders, the inside of a checkout, and a third level)",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := load(t, "").Checkouts(); len(got) != 0 {
		t.Fatalf("no roots gave %v", got)
	}
	gitCheckout(t, home, "repos/acme/notes", "")
	if n := len(g.Checkouts()); n != len(want)+1 {
		t.Errorf("a new clone must show up at once: %d checkouts, want %d", n, len(want)+1)
	}
}

func TestLinkedWorktreesAreNoCheckouts(t *testing.T) {
	root := t.TempDir()
	main, clone := filepath.Join(root, "acme/app"), filepath.Join(root, "old/app")
	gitCheckout(t, root, "acme/app", "git@github.com:acme/app.git")
	gitCheckout(t, root, "old/app", "https://github.com/acme/app")
	gitRun(t, main, "worktree", "add", "-q", "--orphan", "-b", "one", filepath.Join(root, "acme/app.one"))
	gitRun(t, main, "worktree", "add", "-q", "--orphan", "-b", "two", filepath.Join(root, "two"))
	gitCheckout(t, root, "two/inner", "git@github.com:acme/inner.git")
	g := load(t, fmt.Sprintf("roots = [%q]\n", root))
	var got []string
	for _, c := range g.Checkouts() {
		got = append(got, c.Path+" "+c.Key)
	}
	if want := []string{main + " acme/app", clone + " acme/app"}; !slices.Equal(got, want) {
		t.Fatalf("Checkouts = %q, want %q (the walk skips each linked worktree and does not go into it)", got, want)
	}
	clash := "2 checkouts have the key acme/app: " + main + " and " + clone
	if got := g.KeyClashes(); !slices.Equal(got, []string{clash}) {
		t.Errorf("KeyClashes = %q, want [%q]", got, clash)
	}
	if _, err := g.FindCheckout("acme/app"); err == nil || err.Error() != clash+": pass a path" {
		t.Errorf("FindCheckout(acme/app): err = %v, want %q", err, clash+": pass a path")
	}
	if path, ok := g.CheckoutFor("acme/app"); ok {
		t.Errorf("CheckoutFor(acme/app) = %q, want none of two checkouts", path)
	}
	if path, err := g.FindCheckout(main); err != nil || path != main {
		t.Errorf("FindCheckout(%s) = %q %v, want the path", main, path, err)
	}
	os.RemoveAll(clone)
	if got := g.KeyClashes(); len(got) != 0 {
		t.Errorf("one checkout left: KeyClashes = %q, want none", got)
	}
	if path, ok := g.CheckoutFor("acme/app"); !ok || path != main {
		t.Errorf("one checkout left: CheckoutFor(acme/app) = %q %v, want %s", path, ok, main)
	}
}

func TestTheKeyOfACheckoutIsItsGitHubOrigin(t *testing.T) {
	home := t.TempDir()
	gitCheckout(t, home, "sandbox", "")
	dir := filepath.Join(home, "sandbox")
	if got := checkoutKey(dir); got != "local/sandbox" {
		t.Errorf("no origin: %q, want local/sandbox", got)
	}
	gitRun(t, dir, "remote", "add", "origin", "git@github.com:o/r.git")
	for url, want := range map[string]string{
		"https://github.com/o/r.git": "o/r", "https://github.com/o/r": "o/r", "git@github.com:O/R.git": "O/R",
		"ssh://git@github.com/o/r.git": "o/r", "https://gitlab.com/o/r": "local/sandbox",
	} {
		gitRun(t, dir, "remote", "set-url", "origin", url)
		if got := checkoutKey(dir); got != want {
			t.Errorf("checkoutKey with origin %s = %q, want %q", url, got, want)
		}
	}
}

func TestCheckoutForIsTheOneCheckoutOfTheKey(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\"]\n")
	gitCheckout(t, home, "repos/acme/one", "git@github.com:acme/app.git")
	gitCheckout(t, home, "repos/acme/two", "git@github.com:acme/app.git")
	for key, want := range map[string]string{
		"crexi/data": "repos/crexi/data", "CREXI/Data": "repos/crexi/data", "gclunies/hod": "repos/gclunies/hod",
		"local/brain": "repos/gclunies/brain", "acme/app": "", "crexi/nope": "",
	} {
		repo, ok := g.CheckoutFor(key)
		if rel, _ := filepath.Rel(home, repo); ok != (want != "") || ok && rel != want {
			t.Errorf("CheckoutFor(%s) = %q %v, want %q", key, repo, ok, want)
		}
	}
}

func TestFindCheckoutTakesAPathAKeyOrAFolderName(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\"]\n")
	gitCheckout(t, home, "repos/acme/one", "git@github.com:acme/app.git")
	gitCheckout(t, home, "repos/acme/two", "git@github.com:acme/app.git")
	t.Chdir(filepath.Join(home, "repos"))
	for name, want := range map[string]string{
		"web":                                  "repos/crexi/web",
		"crexi/data":                           "repos/crexi/data",
		"GClunies/HOD":                         "repos/gclunies/hod",
		"local/brain":                          "repos/gclunies/brain",
		"~/repos/crexi/web":                    "repos/crexi/web",
		filepath.Join(home, "repos/crexi/web"): "repos/crexi/web",
		"./crexi/web":                          "repos/crexi/web",
		"gclunies/brain":                       "repos/gclunies/brain",
	} {
		repo, err := g.FindCheckout(name)
		if rel, _ := filepath.Rel(home, repo); err != nil || rel != want {
			t.Errorf("FindCheckout(%s) = %q %v, want %s", name, repo, err, want)
		}
	}
	for name, want := range map[string]string{
		"de-interview-exercise": "2 checkouts named de-interview-exercise: pass crexi-dev/de-interview-exercise or crexi/de-interview-exercise",
		"nope":                  "no checkout named nope below roots ~/repos: add its parent folder to roots or pass a path",
		"acme/app": "2 checkouts have the key acme/app: " +
			filepath.Join(home, "repos/acme/one") + " and " + filepath.Join(home, "repos/acme/two") + ": pass a path",
		"crexi/nope":                     filepath.Join(home, "repos/crexi/nope") + " is not a main git checkout",
		"~/repos/crexi":                  filepath.Join(home, "repos/crexi") + " is not a main git checkout",
		"~/repos/crexi/data.review-pr-1": filepath.Join(home, "repos/crexi/data.review-pr-1") + " is not a main git checkout",
	} {
		if _, err := g.FindCheckout(name); err == nil || err.Error() != want {
			t.Errorf("FindCheckout(%s): err = %v, want %q", name, err, want)
		}
	}
	gitCheckout(t, home, "repos/old/data", "git@github.com:crexi/data.git")
	want := "2 checkouts named data: pass " + filepath.Join(home, "repos/crexi/data") + " or " + filepath.Join(home, "repos/old/data")
	if _, err := g.FindCheckout("data"); err == nil || err.Error() != want {
		t.Errorf("two checkouts of crexi/data named data: err = %v, want %q", err, want)
	}
}

func TestReviewQueues(t *testing.T) {
	g := load(t, `
[[review.queues]]
name = "platform"
state = "open"
draft = false
repo = ["umbrella/data"]
author = ["hopper", "noether"]
exclude_regex = 'HLESS-|\[skip review\]'

[[review.queues]]
name = "one at a time"
author = ["octo"]
slots = 1
`)
	no := false
	wantQueues := []ReviewQueue{
		{Name: "platform", PRFilter: PRFilter{State: "open", Draft: &no, Repo: []string{"umbrella/data"}, Author: []string{"hopper", "noether"},
			ExcludeRegex: `HLESS-|\[skip review\]`}, Slots: 3},
		{Name: "one at a time", PRFilter: PRFilter{Author: []string{"octo"}}, Slots: 1},
	}
	if got := g.ReviewQueues(); !reflect.DeepEqual(got, wantQueues) {
		t.Errorf("ReviewQueues = %+v\nwant %+v", got, wantQueues)
	}
	if got := load(t, ""); len(got.ReviewQueues()) != 0 {
		t.Errorf("no review config gave %+v", got.ReviewQueues())
	}
}

func TestAViewsTableUnderReviewFailsAtLoad(t *testing.T) {
	const msg = "the Board has no review view, so delete the [[review.views]] tables"
	for name, text := range map[string]string{
		"view":            "[[review.views]]\nname = \"My teammates\"\nrepo = [\"umbrella/data\"]\n",
		"view and queue":  "[[review.views]]\nname = \"a\"\nrepo = [\"o/r\"]\n[[review.queues]]\nname = \"q\"\nrepo = [\"o/s\"]\n",
		"view old filter": "[[review.views]]\nname = \"a\"\nrepos = [\"o/r\"]\n",
	} {
		global := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
		if _, err := Load(global); err == nil || err.Error() != global+": "+msg {
			t.Errorf("%s: err = %v, want %q", name, err, msg)
		}
	}
}

func TestReviewSlotsAreTheDefaultOfEveryQueue(t *testing.T) {
	g := load(t, `
[review]
slots = 5

[[review.queues]]
name = "first"
author = ["a"]

[[review.queues]]
name = "second"
repo = ["o/r"]

[[review.queues]]
name = "own"
repo = ["o/s"]
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
	if got := load(t, ""); got.ReviewSlots() != 0 {
		t.Errorf("unset review.slots = %d, want 0", got.ReviewSlots())
	}
}

func TestBadReviewConfigFailsAtLoad(t *testing.T) {
	queue := "[[review.queues]]\nname = \"q\"\n"
	for name, c := range map[string]struct{ text, want string }{
		"unknown key":       {queue + "repo = [\"o/r\"]\nauthors_x = [\"x\"]\n", "unknown keys"},
		"unknown section":   {"[review.bogus]\nx = 1\n", "unknown keys"},
		"no filter":         {queue, "review.queues[0]: needs at least one"},
		"empty filter":      {queue + "repo = []\n", "review.queues[0]: needs at least one"},
		"no name":           {"[[review.queues]]\nrepo = [\"o/r\"]\n", "review.queues[0]: name is required"},
		"team form":         {queue + "author_team = [\"platform\"]\n", `review.queues[0]: author_team[0] "platform" must be org/slug`},
		"repo form":         {queue + "repo = [\"umbrella\"]\n", `repo[0] "umbrella" must be owner/name`},
		"repo space":        {queue + "repo = [\"umbrella/da ta\"]\n", "repo[0]"},
		"author space":      {queue + "author = [\"a b\"]\n", "author[0]"},
		"label quote":       {queue + "label = [\"a\\\"b\"]\n", "label[0]"},
		"requested space":   {queue + "review_requested = \"a b\"\n", `review_requested "a b" must be @me or a login`},
		"bad state":         {queue + "repo = [\"o/r\"]\nstate = \"draft\"\n", `state "draft" must be one of open, closed, merged, all`},
		"bad include":       {queue + "repo = [\"o/r\"]\ninclude_regex = \"(\"\n", "review.queues[0]: include_regex: error parsing regexp"},
		"bad exclude":       {queue + "repo = [\"o/r\"]\nexclude_regex = \"a[\"\n", "review.queues[0]: exclude_regex: error parsing regexp"},
		"only regexes":      {queue + "include_regex = \"DATA-\"\nexclude_regex = \"WIP\"\n", "review.queues[0]: needs at least one"},
		"only state":        {queue + "state = \"open\"\ndraft = false\n", "review.queues[0]: needs at least one"},
		"queue closed":      {queue + "repo = [\"o/r\"]\nstate = \"closed\"\n", "review.queues[0]: a queue reviews open PRs"},
		"queue all":         {queue + "repo = [\"o/r\"]\nstate = \"all\"\n", "review.queues[0]: a queue reviews open PRs"},
		"queue drafts":      {queue + "repo = [\"o/r\"]\ndraft = true\n", "review.queues[0]: a queue never reviews drafts"},
		"duplicate queue":   {queue + "repo = [\"o/r\"]\n" + queue + "repo = [\"o/s\"]\n", `review.queues[1]: name "q" is used twice`},
		"slots zero":        {queue + "repo = [\"o/r\"]\nslots = 0\n", "review.queues[0]: slots must be at least 1"},
		"shared slots zero": {"[review]\nslots = 0\n", "review.slots: slots must be at least 1"},
		"queue no filter":   {queue + "slots = 2\n", "review.queues[0]: needs at least one"},
		"queue unknown key": {queue + "repo = [\"o/r\"]\nslot = 2\n", "unknown keys"},
		"old repos":         {queue + "repos = [\"o/r\"]\n", "Rename repos to repo"},
		"old authors":       {queue + "authors = [\"x\"]\n", "Rename authors to author"},
		"old author_teams":  {queue + "author_teams = [\"o/t\"]\n", "Rename author_teams to author_team"},
		"old labels":        {queue + "labels = [\"x\"]\n", "Rename labels to label"},
		"old exclude":       {queue + "repo = [\"o/r\"]\nexclude_titles = [\"WIP\"]\n", "Rename exclude_titles to exclude_regex"},
		"old requested":     {queue + "requested_teams = [\"o/t\"]\n", `Rename requested_teams to review_requested = "@me"`},
	} {
		if err := loadErr(t, c.text); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestReviewInARepoTableIsAnError(t *testing.T) {
	if err := loadErr(t, "[[repos.\"o/r\".review.queues]]\nname = \"a\"\nrepo = [\"o/r\"]\n"); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("err = %v", err)
	}
}

func TestTheReviewingStageHasItsOwnSection(t *testing.T) {
	g := load(t, "[defaults.stage.default]\nmodel = \"m\"\n[defaults.stage.reviewing]\neffort = \"max\"\n")
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
	g, home := loadFixture(t, `
roots = ["~/repos"]

[repos."crexi/*".review_bot]
name = "greptile"
min_review_score = 5
max_review_cycles = 5

[repos."crexi/data".review_bot]
min_review_score = 4
`)
	r, err := g.Resolve(filepath.Join(home, "repos/crexi/web"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &lifecycle.ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: 5, TopScore: 5}
	if p := r.Policy(); !reflect.DeepEqual(p.Bar.Bot, want) || p.Budgets.ReviewCycles != 5 {
		t.Errorf("gate %+v cycles %d, want %+v and 5", p.Bar.Bot, p.Budgets.ReviewCycles, want)
	}
	if r, err := g.Resolve(filepath.Join(home, "repos/gclunies/lineup"), nil); err != nil || r.Bar.Bot != nil || r.Budgets.ReviewCycles != 0 {
		t.Errorf("a Repo with no table: gate %+v cycles %d err %v", r.Bar.Bot, r.Budgets.ReviewCycles, err)
	}
	if r, err := g.Resolve(filepath.Join(home, "repos/crexi/data"), nil); err != nil || r.Bar.Bot.MinScore != 4 || r.Bar.Bot.Bot != "greptile" || r.Budgets.ReviewCycles != 5 {
		t.Errorf("the owner/repo table must override one key and keep the rest: %+v %+v %v", r.Bar.Bot, r.Budgets, err)
	}

	r, err = load(t, "[defaults.review_bot]\nname = \"greptile\"\n").Resolve(t.TempDir(), nil)
	if err != nil || r.Bar.Bot.MinScore != 5 || r.Budgets.ReviewCycles != 3 {
		t.Errorf("defaults: gate %+v cycles %d err %v, want the top score and 3 cycles", r.Bar.Bot, r.Budgets.ReviewCycles, err)
	}
}

func TestBadReviewBotFailsAtLoad(t *testing.T) {
	for name, text := range map[string]string{
		"unknown bot":     "[defaults.review_bot]\nname = \"coderabbit\"\n",
		"score below 1":   "[defaults.review_bot]\nname = \"greptile\"\nmin_review_score = 0\n",
		"score above top": "[defaults.review_bot]\nname = \"greptile\"\nmin_review_score = 6\n",
		"negative cycles": "[defaults.review_bot]\nname = \"greptile\"\nmax_review_cycles = -1\n",
		"unknown key":     "[defaults.review_bot]\nname = \"greptile\"\nmin_score = 4\n",
		"in a Repo table": "[repos.\"o/r\".review_bot]\nname = \"greptile\"\nmin_review_score = 9\n",
	} {
		if err := loadErr(t, text); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// One table can leave out a key for a lower layer to set, so the merged
// tables must still name a bot and keep its score in range.
func TestTheMergedReviewBotMustBeWhole(t *testing.T) {
	g, home := loadFixture(t, "[repos.\"GClunies/lineup\".review_bot]\nmin_review_score = 4\n")
	if _, err := g.Resolve(filepath.Join(home, "repos/gclunies/lineup"), nil); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("no bot named: err %v", err)
	}
	g, home = loadFixture(t, "[defaults.review_bot]\nname = \"greptile\"\n[repos.\"GClunies/lineup\".review_bot]\nmin_review_score = 7\n")
	if _, err := g.Resolve(filepath.Join(home, "repos/gclunies/lineup"), nil); err == nil {
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
	g, home := loadFixture(t, `
[defaults.landing_bar]
checks = "all"

[repos."crexi/*".landing_bar]
threads = "resolved"

[repos."crexi/data".landing_bar]
approvals = "requested"
threads = "ignored"
`)
	for rel, want := range map[string]lifecycle.LandingBar{
		"repos/crexi/web":       {Checks: lifecycle.AllChecks, Threads: lifecycle.ResolvedThreads, Approvals: lifecycle.RequiredApprovals},
		"repos/gclunies/lineup": {Checks: lifecycle.AllChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals},
		"repos/crexi/data":      {Checks: lifecycle.AllChecks, Threads: lifecycle.IgnoredThreads, Approvals: lifecycle.RequestedApprovals},
	} {
		if r, err := g.Resolve(filepath.Join(home, rel), nil); err != nil || r.Bar != want {
			t.Errorf("%s: %+v %v, want %+v", rel, r.Bar, err, want)
		}
	}
}

func TestBadLandingBarFailsAtLoad(t *testing.T) {
	for text, want := range map[string]string{
		"[defaults.landing_bar]\nchecks = \"some\"\n":       `defaults: landing_bar.checks: "some" is not one of all, required`,
		"[defaults.landing_bar]\nthreads = \"any\"\n":       `defaults: landing_bar.threads: "any" is not one of ignored, resolved, replied`,
		"[defaults.landing_bar]\napprovals = \"all\"\n":     `defaults: landing_bar.approvals: "all" is not one of requested, required`,
		"[defaults.landing_bar]\nreviews = \"required\"\n":  "unknown keys: defaults.landing_bar.reviews",
		"[repos.\"o/*\".landing_bar]\nthreads = \"open\"\n": `repos."o/*": landing_bar.threads: "open" is not one of`,
		"[repos.\"o/r\".landing_bar]\nchecks = \"x\"\n":     `repos."o/r": landing_bar.checks: "x" is not one of all, required`,
	} {
		if err := loadErr(t, text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
}

func TestTheRestartsHintTellsTheUserToDeleteTheLine(t *testing.T) {
	err := loadErr(t, "[repos.\"o/r\".budgets]\nci_debug = 3\nrestarts = 2\n")
	if err == nil || !strings.Contains(err.Error(), "delete the restarts line from the [budgets] table") {
		t.Fatalf("err = %v, want a hint that names the restarts line", err)
	}
}

func TestTheTrackersKeyLocksARepo(t *testing.T) {
	g, home := loadFixture(t, `
roots = ["~/repos"]
[repos."GClunies/*"]
trackers = ["local", "github"]
[repos."GClunies/hod"]
trackers = ["local"]
[repos."crexi/data"]
teams = ["ENG"]
`)
	for repo, want := range map[string][]string{
		"~/repos/gclunies/hod":                    {"local"},
		filepath.Join(home, "repos/gclunies/hod"): {"local"},
		"~/repos/gclunies/lineup":                 {"local", "github"},
		"~/repos/crexi/data":                      nil,
		"~/repos/gclunies/brain":                  nil,
	} {
		if got := g.RepoTrackers(repo); !slices.Equal(got, want) || (want == nil) != (got == nil) {
			t.Errorf("RepoTrackers(%s) = %q, want %q", repo, got, want)
		}
	}
	want := []LockedRepo{{"GClunies/*", []string{"local", "github"}}, {"GClunies/hod", []string{"local"}}}
	if got := g.LockedRepos(); !reflect.DeepEqual(got, want) {
		t.Errorf("LockedRepos = %+v, want %+v", got, want)
	}
	for kind, left := range map[string][]string{"linear": {"repos/gclunies/hod", "repos/gclunies/lineup"}, "github": {"repos/gclunies/hod"}} {
		got := g.CheckoutsFor(kind)
		for _, rel := range left {
			if slices.Contains(got, filepath.Join(home, rel)) {
				t.Errorf("CheckoutsFor(%s) offers %s", kind, rel)
			}
		}
		if len(got) != len(fixtureCheckouts)-len(left) {
			t.Errorf("CheckoutsFor(%s) = %q, want all but %q", kind, got, left)
		}
	}
	if n := len(g.CheckoutsFor("local")); n != len(fixtureCheckouts) {
		t.Errorf("CheckoutsFor(local) has %d checkouts, want all %d", n, len(fixtureCheckouts))
	}
}

func TestTheTrackersKeyTakesOnlyKnownTrackers(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"[repos.\"GClunies/hod\"]\ntrackers = [\"jira\"]", `repos."GClunies/hod": trackers[0] "jira" is not a Tracker: use linear, github, local`},
		{"[repos.\"GClunies/hod\"]\ntrackers = [\"Local\"]", `trackers[0] "Local" is not a Tracker`},
		{"[repos.\"GClunies/*\"]\ntrackers = []", `repos."GClunies/*": trackers must name at least one of linear, github, local`},
		{"[repos.\"GClunies/hod\"]\ntrackers = [\"local\"]\nteams = [\"ENG\"]", `repos."GClunies/hod": teams maps Linear teams to the Repo, so trackers must hold linear`},
		{"[repos.\"crexi/*\"]\ntrackers = [\"local\"]\n[repos.\"crexi/data\"]\nteams = [\"ENG\"]", `repos."crexi/data": teams maps Linear teams to the Repo, so trackers must hold linear`},
		{"[repos.\"crexi/*\"]\nteams = [\"ENG\"]", `repos."crexi/*": teams maps Linear teams to one Repo, so it needs an owner/repo key`},
	} {
		if err := loadErr(t, c.body+"\n"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.body, err, c.want)
		}
	}
	load(t, "[repos.\"acme/data\"]\nteams = [\"ENG\"]\ntrackers = [\"linear\", \"local\"]\n")
	load(t, "[repos.\"acme/*\"]\ntrackers = [\"local\"]\n[repos.\"acme/data\"]\nteams = [\"ENG\"]\ntrackers = [\"linear\"]\n")
}

func TestThePrefixKeyNamesARepo(t *testing.T) {
	g, home := loadFixture(t, `
roots = ["~/repos"]
[repos."GClunies/hod"]
trackers = ["local"]
prefix = "hod"
[repos."crexi/web"]
prefix = "web-2"
`)
	for repo, want := range map[string]string{
		"~/repos/gclunies/hod":                    "hod",
		filepath.Join(home, "repos/gclunies/hod"): "hod",
		"~/repos/crexi/web":                       "web-2",
		"~/repos/crexi/data":                      "",
	} {
		if got := g.RepoPrefix(repo); got != want {
			t.Errorf("RepoPrefix(%s) = %q, want %q", repo, got, want)
		}
	}
	if repo, ok := g.RepoForPrefix("hod"); !ok || repo != filepath.Join(home, "repos/gclunies/hod") {
		t.Errorf("RepoForPrefix(hod) = %q, %v", repo, ok)
	}
	if _, ok := g.RepoForPrefix("data"); ok {
		t.Error("RepoForPrefix(data) found a Repo with no prefix")
	}
	if got, want := g.PrefixedRepos(), []PrefixedRepo{{"GClunies/hod", "hod"}, {"crexi/web", "web-2"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("PrefixedRepos = %+v, want %+v", got, want)
	}
	if got, want := g.Prefixes(), []string{"hod", "web-2"}; !slices.Equal(got, want) {
		t.Errorf("Prefixes = %q, want %q", got, want)
	}
}

func TestThePrefixKeyRefusesABadPrefix(t *testing.T) {
	hod := "[repos.\"GClunies/hod\"]\n"
	for _, c := range []struct{ body, want string }{
		{hod + `prefix = "Hod"`, `repos."GClunies/hod": prefix "Hod" must hold only lowercase letters, digits, and dashes`},
		{hod + `prefix = "hod_1"`, `prefix "hod_1" must hold only lowercase letters, digits, and dashes`},
		{hod + `prefix = "hod.x"`, `prefix "hod.x" must hold only`},
		{hod + `prefix = "-hod"`, `prefix "-hod" must hold only`},
		{hod + "prefix = \"hod\"\n[repos.\"acme/data\"]\nprefix = \"hod\"", `repos."acme/data": prefix "hod" is also the prefix of repos."GClunies/hod"`},
		{hod + "prefix = \"eng\"\n[repos.\"acme/data\"]\nteams = [\"ENG\"]", `repos."GClunies/hod": prefix "eng" equals a Linear team key`},
		{hod + `prefix = "local"`, `prefix "local" is reserved`},
		{hod + `prefix = "ah"`, `prefix "ah" is reserved`},
		{hod + `prefix = "pr"`, `prefix "pr" is reserved`},
		{"[repos.\"GClunies/*\"]\nprefix = \"gc\"", `repos."GClunies/*": prefix "gc" needs an owner/repo key, the key of one Repo`},
	} {
		if err := loadErr(t, c.body+"\n"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.body, err, c.want)
		}
	}
}

func TestCheckPrefixTeamsRefusesAPrefixThatEqualsATeamKeyInAnyCase(t *testing.T) {
	g := load(t, "[repos.\"GClunies/hod\"]\nprefix = \"hod\"\n")
	if err := g.CheckPrefixTeams([]string{"ENG", "PD"}); err != nil {
		t.Fatalf("other team keys: %v", err)
	}
	err := g.CheckPrefixTeams([]string{"ENG", "HOD"})
	if err == nil || !strings.Contains(err.Error(), `prefix "hod" equals a Linear team key`) {
		t.Fatalf("err = %v, want a refusal that names hod", err)
	}
}

func TestMergedClosesNoTabAndKeepsAWeekByDefault(t *testing.T) {
	g := load(t, "")
	if g.CloseMergedTab() || g.KeepWorktree() != 7*24*time.Hour {
		t.Errorf("defaults: close_tab %v, keep %s, want false and 7 days", g.CloseMergedTab(), g.KeepWorktree())
	}
	g = load(t, "[merged]\nclose_tab = true\nkeep_worktree_days = 0\n")
	if !g.CloseMergedTab() || g.KeepWorktree() != 0 {
		t.Errorf("set: close_tab %v, keep %s, want true and 0", g.CloseMergedTab(), g.KeepWorktree())
	}
	if g := load(t, "[merged]\nkeep_worktree_days = 30\n"); g.KeepWorktree() != 30*24*time.Hour {
		t.Errorf("30 days: keep %s", g.KeepWorktree())
	}
}

func TestBadMergedConfigFailsAtLoad(t *testing.T) {
	for name, c := range map[string]struct{ text, want string }{
		"more than 30": {"[merged]\nkeep_worktree_days = 31\n", "merged.keep_worktree_days: 31 is more than 30"},
		"negative":     {"[merged]\nkeep_worktree_days = -1\n", "merged.keep_worktree_days: -1"},
		"unknown key":  {"[merged]\nclose_tabs = true\n", "unknown keys"},
	} {
		if err := loadErr(t, c.text); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
