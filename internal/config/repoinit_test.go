package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/lifecycle"
)

const initBefore = `roots = ["~/repos"]

# Every Repo starts here.
[defaults]
wip_cap = 5

# crexi Repos take Linear Issues.
[repos."crexi/*"]
tracker = "linear"
[repos."crexi/*".review_bot]
name = "greptile"

[repos."crexi/data"]
teams = ["PD"] # the data team
prefix = "dat"
[repos."crexi/data".landing_bar]
threads = "resolved"

# GitHub labels follow.
[github.labels]
started = "in progress"
`

// The new table goes after the last Repo table, before the comment of the
// next table, and every other byte stays.
const initAfter = `roots = ["~/repos"]

# Every Repo starts here.
[defaults]
wip_cap = 5

# crexi Repos take Linear Issues.
[repos."crexi/*"]
tracker = "linear"
[repos."crexi/*".review_bot]
name = "greptile"

[repos."crexi/data"]
teams = ["PD"] # the data team
prefix = "dat"
[repos."crexi/data".landing_bar]
threads = "resolved"

[repos."crexi/web"]
teams = ["WEB", "APP"]
prefix = "cw"
[repos."crexi/web".review_bot]
min_review_score = 4
[repos."crexi/web".landing_bar]
checks = "all"

# GitHub labels follow.
[github.labels]
started = "in progress"
`

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func loadPath(t *testing.T, path string) *Global {
	t.Helper()
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeBin(t, t.TempDir(), "claude")
	g.lookPath = func(string) (string, error) { return bin, nil }
	return g
}

func TestInitRepoAddsATableThatTheNextLoadReads(t *testing.T) {
	home := fixtureHome(t)
	path := write(t, filepath.Join(home, ".config", "lineup", "config.toml"), initBefore)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	table, err := InitRepo(path, "crexi/web", []string{"landing_bar.checks=all", "prefix=cw", "review_bot.min_review_score=4", "teams=WEB, APP"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[repos.\"crexi/web\"]\nteams = [\"WEB\", \"APP\"]\nprefix = \"cw\"\n[repos.\"crexi/web\".review_bot]\nmin_review_score = 4\n[repos.\"crexi/web\".landing_bar]\nchecks = \"all\"\n"; table != want {
		t.Errorf("table:\n%s\nwant:\n%s", table, want)
	}
	if got := readFile(t, path); got != initAfter {
		t.Errorf("file:\n%s\nwant:\n%s", got, initAfter)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v %v, want the old file's 0600", fi.Mode(), err)
	}

	g := loadPath(t, path)
	web := filepath.Join(home, "repos/crexi/web")
	if key, ok := g.SetUp(web); key != "crexi/web" || !ok {
		t.Errorf("SetUp(web) = %q %v, want crexi/web set up", key, ok)
	}
	r, err := g.Resolve(web, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := lifecycle.LandingBar{Checks: lifecycle.AllChecks, Threads: lifecycle.RepliedThreads, Approvals: lifecycle.RequiredApprovals,
		Bot: &lifecycle.ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: 4, TopScore: 5}}
	if !reflect.DeepEqual(r.Bar, want) {
		t.Errorf("bar %+v, want %+v", r.Bar, want)
	}
	if got := g.ReposForTeam("APP"); !reflect.DeepEqual(got, []string{"crexi/web"}) {
		t.Errorf("team APP maps to %q, want crexi/web", got)
	}
	if got := g.RepoPrefix(web); got != "cw" {
		t.Errorf("RepoPrefix(web) = %q, want cw", got)
	}
}

func TestAnEmptyTableSetsTheRepoUp(t *testing.T) {
	home := fixtureHome(t)
	before := "roots = [\"~/repos\"]\n\n[defaults]\nwip_cap = 5\n"
	path := write(t, filepath.Join(home, "config.toml"), before)
	lineup := filepath.Join(home, "repos/gclunies/lineup")
	if _, ok := loadPath(t, path).SetUp(lineup); ok {
		t.Fatal("GClunies/lineup is set up before its table")
	}
	table, err := InitRepo(path, "GClunies/lineup", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[repos.\"GClunies/lineup\"]\n"; table != want {
		t.Errorf("table %q, want %q", table, want)
	}
	if got, want := readFile(t, path), before+"\n"+table; got != want {
		t.Errorf("file %q, want %q: with no Repo table, the table goes at the end", got, want)
	}
	if key, ok := loadPath(t, path).SetUp(lineup); key != "GClunies/lineup" || !ok {
		t.Errorf("SetUp = %q %v, want GClunies/lineup set up", key, ok)
	}
}

func TestInitRepoMakesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lineup", "config.toml")
	if _, err := InitRepo(path, "GClunies/lineup", []string{"tracker=github"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path), "[repos.\"GClunies/lineup\"]\ntracker = \"github\"\n"; got != want {
		t.Errorf("file %q, want %q", got, want)
	}
}

func TestInitRepoRefusesABadTableAndKeepsTheFile(t *testing.T) {
	for _, c := range []struct {
		key  string
		args []string
		want string
	}{
		{"crexi/web", []string{"landing_bar.checks=bogus"}, "landing_bar.checks"},
		{"crexi/web", []string{"review_bot.name=nope"}, "not a known review bot"},
		{"GClunies/lineup", []string{"teams=PD"}, "teams maps Linear teams"},
		{"crexi/web", []string{"review_bot.min_review_score=high"}, "review_bot.min_review_score"},
		{"crexi/web", []string{"tracker=linear", "tracker=github"}, "tracker is set twice"},
		{"crexi/web", []string{"tracker"}, "key=value"},
		{"crexi/web", []string{"teams=PD,"}, "teams"},
		{"crexi/web", []string{"prefix=local"}, `repos."crexi/web": prefix "LOCAL" is reserved: lineup starts its own IDs with LOCAL, AH, PR, QA`},
		{"crexi/web", []string{"prefix=ah"}, `repos."crexi/web": prefix "AH" is reserved`},
		{"crexi/web", []string{"prefix=PR"}, `repos."crexi/web": prefix "PR" is reserved`},
		{"crexi/web", []string{"prefix=qa-web"}, `repos."crexi/web": prefix "QA-WEB" starts with QA-`},
		{"crexi/web", []string{"prefix=c_w"}, `repos."crexi/web": prefix "C_W" must hold only letters, digits, and dashes`},
		{"crexi/web", []string{"prefix=DAT"}, `repos."crexi/web": prefix "DAT" is also the prefix of repos."crexi/data"`},
		{"crexi/web", []string{"prefix=pd"}, `repos."crexi/web": prefix "PD" equals a Linear team key`},
		{"crexi/web", []string{"wip_cap=3"}, "tracker, teams, prefix, review_bot.name, review_bot.min_review_score, review_bot.max_review_cycles, landing_bar.checks, landing_bar.threads, landing_bar.approvals"},
		{"crexi/data", nil, "crexi/data is already set up in "},
		{"Crexi/Data", nil, "Crexi/Data is already set up in "},
		{"crexi/*", nil, "owner/repo"},
		{"crexi", nil, "owner/repo"},
	} {
		dir := t.TempDir()
		path := write(t, filepath.Join(dir, "config.toml"), initBefore)
		_, err := InitRepo(path, c.key, c.args, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %q: err = %v, want %q", c.key, c.args, err, c.want)
		}
		if got := readFile(t, path); got != initBefore {
			t.Errorf("%s %q: the file changed:\n%s", c.key, c.args, got)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s %q: the folder holds %d files, want only the config", c.key, c.args, len(entries))
		}
	}
}

func TestAnOwnerTableSetsNoRepoUp(t *testing.T) {
	g, home := loadFixture(t, "roots = [\"~/repos\"]\n[repos.\"crexi/*\"]\ntracker = \"linear\"\n[repos.\"gclunies/HOD\"]\n")
	for rel, want := range map[string]bool{"repos/crexi/web": false, "repos/gclunies/hod": true, "repos/gclunies/lineup": false} {
		if _, ok := g.SetUp(filepath.Join(home, rel)); ok != want {
			t.Errorf("%s: set up %v, want %v", rel, ok, want)
		}
	}
}

func TestInheritedIsWhatDefaultsAndTheOwnerTableSet(t *testing.T) {
	g := load(t, `
[defaults.review_bot]
max_review_cycles = 2
[defaults.landing_bar]
approvals = "requested"

[repos."crexi/*"]
tracker = "linear"
[repos."crexi/*".review_bot]
name = "greptile"
[repos."crexi/*".landing_bar]
checks = "all"

[repos."crexi/data"]
[repos."crexi/data".review_bot]
min_review_score = 4
`)
	two := 2
	crexi := Inherited{
		Tracker:    "linear",
		ReviewBot:  InheritedBot{Name: "greptile", MaxReviewCycles: &two},
		LandingBar: InheritedBar{Checks: "all", Approvals: "requested"},
	}
	for key, want := range map[string]Inherited{
		"crexi/data": crexi,
		"Crexi/Web":  crexi,
		"GClunies/lineup": {
			ReviewBot:  InheritedBot{MaxReviewCycles: &two},
			LandingBar: InheritedBar{Approvals: "requested"},
		},
	} {
		if got := g.Inherited(key); !reflect.DeepEqual(got, want) {
			t.Errorf("Inherited(%s) = %+v, want %+v", key, got, want)
		}
	}
}

func TestSuggestPrefixTakesTheFirstStartOfTheNameThatLoadAccepts(t *testing.T) {
	g := load(t, `
[repos."acme/data"]
tracker = "linear"
teams = ["KIN"]
[repos."acme/web"]
prefix = "lin"
[repos."acme/notes"]
prefix = "not"
`)
	for key, want := range map[string]string{
		"GClunies/Kinesis-Advantage-360-Pro-ZMK": "kines", // KIN is a team key, and KINE is a team key below.
		"GClunies/lineup":                        "line",  // acme/web has LIN.
		"acme/notes":                             "not",   // the Repo's own prefix is no clash.
		"GClunies/HOD":                           "hod",
		"GClunies/qa-tools":                      "qat", // QA- is reserved, so the dash goes.
		"GClunies/ah":                            "",    // AH is reserved, and no longer start exists.
		"GClunies/go":                            "go",
		"GClunies/.github":                       "git",
		"GClunies/---":                           "",
	} {
		if got := g.SuggestPrefix(key, []string{"KINE"}); got != want {
			t.Errorf("SuggestPrefix(%s) = %q, want %q", key, got, want)
		}
	}
}

func TestInitRepoRefusesAPrefixThatEqualsALiveTeamKey(t *testing.T) {
	path := write(t, filepath.Join(t.TempDir(), "config.toml"), initBefore)
	_, err := InitRepo(path, "crexi/web", []string{"prefix=kin"}, func() []string { return []string{"ENG", "KIN"} })
	if want := `repos."crexi/web": prefix "KIN" equals a Linear team key`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}
	if got := readFile(t, path); got != initBefore {
		t.Errorf("the file changed:\n%s", got)
	}
	asked := false
	if _, err := InitRepo(path, "crexi/web", []string{"tracker=github"}, func() []string { asked = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if asked {
		t.Error("a table with no prefix asked for the team keys")
	}
}
