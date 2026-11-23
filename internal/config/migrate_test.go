package config

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fixtureCheckouts are the main checkouts below the fixture's ~/repos, by
// path from the home folder, with their origin URL. "" is a checkout with no
// origin. Two checkouts have the folder name de-interview-exercise.
var fixtureCheckouts = map[string]string{
	"repos/crexi/data":                      "git@github.com:crexi/data.git",
	"repos/crexi/web":                       "https://github.com/crexi/web.git",
	"repos/crexi/de-interview-exercise":     "git@github.com:crexi/de-interview-exercise.git",
	"repos/crexi-dev/de-interview-exercise": "git@github.com:crexi-dev/de-interview-exercise.git",
	"repos/gclunies/hod":                    "git@github.com:GClunies/hod.git",
	"repos/gclunies/lineup":                 "git@github.com:GClunies/lineup.git",
	"repos/gclunies/brain":                  "",
}

// fixtureHome makes a home folder with the fixture checkouts and the linked
// worktree repos/crexi/data.review-pr-1 of crexi/data, sets HOME to it, and
// returns it.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for rel, url := range fixtureCheckouts {
		dir := filepath.Join(home, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "init", "-q")
		if url != "" {
			gitRun(t, dir, "remote", "add", "origin", url)
		}
	}
	gitRun(t, filepath.Join(home, "repos/crexi/data"), "worktree", "add", "-q", "--orphan", "-b", "review-pr-1", filepath.Join(home, "repos/crexi/data.review-pr-1"))
	return home
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// settings is what a load resolves for one checkout.
type settings struct {
	Resolved Resolved
	Tracker  string
	Prefix   string
}

// settingsDump resolves each fixture checkout, and the Repo of team PD, as
// paths from the home folder.
type settingsDump struct {
	Checkouts map[string]settings
	TeamPD    string
}

func dumpSettings(t *testing.T, g *Global, home string) settingsDump {
	t.Helper()
	bin := fakeBin(t, t.TempDir(), "claude")
	g.lookPath = func(string) (string, error) { return bin, nil }
	d := settingsDump{Checkouts: map[string]settings{}}
	for rel := range fixtureCheckouts {
		path := filepath.Join(home, rel)
		r, err := g.Resolve(path, nil)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", rel, err)
		}
		r.Claude = ""
		_, tracker := g.RepoTracker(path)
		d.Checkouts[rel] = settings{Resolved: r, Tracker: tracker, Prefix: g.RepoPrefix(path)}
	}
	if keys := g.ReposForTeam("PD"); len(keys) == 1 {
		if repo, ok := g.CheckoutFor(keys[0]); ok {
			d.TeamPD, _ = filepath.Rel(home, repo)
		}
	}
	return d
}

func readDump(t *testing.T, path string) settingsDump {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d settingsDump
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMigrateKeepsWhatEachCheckoutResolves(t *testing.T) {
	home := fixtureHome(t)
	old, err := os.ReadFile("testdata/migrate/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	path := write(t, filepath.Join(home, ".config", "lineup", "config.toml"), string(old))
	msg, err := Migrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Rewrote " + path + ". The old file is " + path + ".bak."; !strings.HasPrefix(msg, want) {
		t.Errorf("message %q, want it to start with %q", msg, want)
	}
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// JSON gives the dump the types that readDump gives.
	data, err := json.Marshal(dumpSettings(t, g, home))
	if err != nil {
		t.Fatal(err)
	}
	var after settingsDump
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if before := readDump(t, "testdata/migrate/before.json"); !reflect.DeepEqual(after, before) {
		t.Errorf("a checkout resolves other settings after the migration:\nafter  %+v\nbefore %+v", after, before)
	}
	if !slices.Equal(g.file.Roots, []string{"~/repos"}) {
		t.Errorf("roots = %q, want [~/repos]", g.file.Roots)
	}
	wantBoard := Board{
		Order:     []string{"Done", "Needs you", "Up next", "Working", "Waiting", "Backlog", "Follow-up"},
		Collapsed: []string{"Backlog", "Up next"},
		Hidden:    []string{"Done"},
	}
	if b, err := g.Board(); err != nil || !reflect.DeepEqual(b, wantBoard) {
		t.Errorf("board %+v, %v, want %+v", b, err, wantBoard)
	}
	if bak, _ := os.ReadFile(path + ".bak"); !bytes.Equal(bak, old) {
		t.Errorf("the .bak file is not the old file:\n%s", bak)
	}
	text, _ := os.ReadFile(path)
	for _, c := range []string{"# Personal repos (GClunies/*) track Issues in GitHub.", "# Crexi only allows apps registered in Crexi", "# Personal: hod tracks its Issues in the Local Tracker only.\n[repos.\"GClunies/hod\"]",
		"# d on the board shows or hides Done.\n[board]\norder = [\"done\", \"needs_you\", \"up_next\", \"working\", \"waiting\", \"backlog\"]\ncollapsed = [\"backlog\", \"up_next\"] # start closed\n"} {
		if !strings.Contains(string(text), c) {
			t.Errorf("the migrated file lost %q:\n%s", c, text)
		}
	}
	msg, err = Migrate(path)
	if again, _ := os.ReadFile(path); err != nil || !bytes.Equal(again, text) || !strings.Contains(msg, "changed nothing") {
		t.Errorf("a second migration: %q %v, and the file changed: %v", msg, err, !bytes.Equal(again, text))
	}
}

func TestMigrateMovesTheLayerKeysAndKeepsTheComments(t *testing.T) {
	home := fixtureHome(t)
	path := write(t, filepath.Join(home, ".config", "lineup", "config.toml"), `# lineup config

# How many Tasks run at once.
wip_cap = 4
start_skills = ["a"]
stage.default = { model = "m" }
budgets.ci_debug = 2

# Every checkout.
[[repos]]
match = "~/repos/*/*"

[review_bot]
name = "greptile"

[[repos]] # Crexi
# All of Crexi.
match = "~/repos/crexi/*"
[repos.stage.working]
skill = "s"

[[repos]]
match = "~/repos/gclunies/brain"
wip_cap = 1
`)
	if _, err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	want := `# lineup config

# Every checkout.
roots = ["~/repos"]
start_skills = ["a"]

[defaults]
# How many Tasks run at once.
wip_cap = 4
stage.default = { model = "m" }
budgets.ci_debug = 2

[defaults.review_bot]
name = "greptile"

[repos."crexi/*"] # Crexi
# All of Crexi.
[repos."crexi/*".stage.working]
skill = "s"

[repos."local/brain"]
wip_cap = 1
`
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("migrated file:\n%s\nwant\n%s", got, want)
	}
}

func TestMigrateRefusesWhatNoTableCanReplace(t *testing.T) {
	home := fixtureHome(t)
	gitCheckout(t, home, "deep/a/b/c", "")
	gitCheckout(t, home, "clone/data", "git@github.com:crexi/data.git")
	for text, want := range map[string]string{
		"[[repos]]\nmatch = \"~/repos/gclunies/*\"\nwip_cap = 2\n":                                                   `the checkouts that the [[repos]] glob "~/repos/gclunies/*" matches have more than one owner (local, GClunies)`,
		"[[repos]]\nmatch = \"~/repos/*/data\"\nwip_cap = 2\n":                                                       `the [[repos]] glob "~/repos/*/data" is not <folder>/*`,
		"[[repos]]\nmatch = \"~/nothing/*\"\nwip_cap = 2\n":                                                          `the [[repos]] glob "~/nothing/*" matches no checkout`,
		"[[repos]]\nmatch = \"~/repos/crexi\"\nwip_cap = 2\n":                                                        `the [[repos]] match "~/repos/crexi" is not a main git checkout`,
		"[[repos]]\nmatch = \"~/repos/crexi/data\"\nwip_cap = 2\n[[repos]]\nmatch = \"~/clone/data\"\nwip_cap = 3\n": `the [[repos]] matches "~/repos/crexi/data" and "~/clone/data" both become [repos."crexi/data"]`,
		"[[repos]]\nmatch = \"~/repos/crexi/data.review-pr-1\"\nwip_cap = 2\n":                                       `the [[repos]] match "~/repos/crexi/data.review-pr-1" is not a main git checkout`,
		"[[repos]]\nmatch = \"~/deep/*/*/*\"\n":                                                                      "which is more than two folders below the roots ~/deep",
		"[[repos]]\nmatch = \"~/repos/crexi/data\"\nteams = [\"\"\"PD\"\"\"]\n":                                      "line 3: lineup config migrate cannot move a multi-line string",
		"poll_interval = '''10s'''\n":                                                                                "line 1: lineup config migrate cannot move a multi-line string",
		"[repos.\"GClunies/*\"]\ntrackers = [\"local\", \"github\"]\n":                                               `repos."GClunies/*": trackers holds 2 Trackers, but a Repo takes Issues from one Tracker`,
		"[repos.\"GClunies/hod\"]\ntrackers = []\n":                                                                  `repos."GClunies/hod": trackers holds 0 Trackers`,
		"board.order = [\"ready\", \"done\"]\n":                                                                      `board.order: lineup config migrate finds the list outside the [board] header: write order = ["up_next", "done"] by hand`,
		"board = { hidden = [\"queued\"] }\n":                                                                        `board.hidden: lineup config migrate finds the list outside the [board] header: write hidden = ["up_next"] by hand`,
	} {
		path := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
		_, err := Migrate(path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
		if got, _ := os.ReadFile(path); string(got) != text {
			t.Errorf("%q: the failed migration changed the file:\n%s", text, got)
		}
		if _, err := os.Stat(path + ".bak"); err == nil {
			t.Errorf("%q: the failed migration wrote a .bak file", text)
		}
	}
}

func TestMigratePutsTheOldFileBackWhenTheNewOneDoesNotLoad(t *testing.T) {
	home := fixtureHome(t)
	old := "[[repos]]\nmatch = \"~/repos/crexi/data\"\nwip_cap = 0\n"
	path := write(t, filepath.Join(home, ".config", "lineup", "config.toml"), old)
	_, err := Migrate(path)
	if err == nil || !strings.Contains(err.Error(), "put the old file back") || !strings.Contains(err.Error(), `repos."crexi/data": wip_cap must be at least 1`) {
		t.Fatalf("err = %v, want the load error of the new file", err)
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Errorf("the file is not the old file:\n%s", got)
	}
}

func TestMigrateRewritesATrackersListOfOneTrackerAsTracker(t *testing.T) {
	old := `roots = ["~/repos"]

# hod tracks its Issues in the Local Tracker.
[repos."GClunies/hod"]
  trackers = ["local"] # only local
prefix = "hod"


[repos."crexi/data"]
teams = ["PD"]
wip_cap = 2

# the end
`
	path := write(t, filepath.Join(t.TempDir(), "config.toml"), old)
	msg, err := Migrate(path)
	if err != nil || !strings.HasPrefix(msg, "Rewrote "+path) {
		t.Fatalf("Migrate = %q %v", msg, err)
	}
	want := `roots = ["~/repos"]

# hod tracks its Issues in the Local Tracker.
[repos."GClunies/hod"]
  tracker = "local" # only local
prefix = "hod"


[repos."crexi/data"]
teams = ["PD"]
tracker = "linear"
wip_cap = 2

# the end
`
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("migrated file:\n%s\nwant\n%s", got, want)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != old {
		t.Errorf("the .bak file is not the old file:\n%s", bak)
	}
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.TrackedRepos(); !reflect.DeepEqual(got, []TrackedRepo{{"GClunies/hod", "local"}, {"crexi/data", "linear"}}) {
		t.Errorf("TrackedRepos = %+v", got)
	}
	if msg, err := Migrate(path); err != nil || !strings.Contains(msg, "changed nothing") {
		t.Errorf("a second migration: %q %v", msg, err)
	}
}

func TestMigrateLeavesTeamsWithAnOwnerTrackerAlone(t *testing.T) {
	text := "[repos.\"crexi/*\"]\ntracker = \"linear\"\n[repos.\"Crexi/data\"]\nteams = [\"PD\"]\n"
	path := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
	if msg, err := Migrate(path); err != nil || !strings.Contains(msg, "changed nothing") {
		t.Fatalf("Migrate = %q %v", msg, err)
	}
}

func TestMigrateChangesNothingInANewFile(t *testing.T) {
	text := "roots = [\"~/repos\"]\n[defaults]\nwip_cap = 3\n[repos.\"o/r\"]\nwip_cap = 2\n[board]\norder = [\"done\", \"up_next\"]\n"
	path := write(t, filepath.Join(t.TempDir(), "config.toml"), text)
	msg, err := Migrate(path)
	if err != nil || msg != path+" has no [[repos]] tables, no layer keys at the top level, no trackers lists, and no ready or queued in [board], so lineup changed nothing." {
		t.Fatalf("Migrate = %q %v", msg, err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("a migration that changed nothing wrote a .bak file")
	}
}

// The first of ready and queued in a [board] list becomes up_next, and the
// other goes. The rest of the file keeps its layout and its comments.
func TestMigrateRewritesReadyAndQueuedInTheBoardListsAsOneUpNext(t *testing.T) {
	old := `start_skills = ["a"]

# Board sections.
[board]
# The groups, top first.
order = ["needs_you", "ready", "queued", "working"] # mine
collapsed = [
  "queued",
  "backlog",
]
  hidden = ["queued"]

[github.labels]
backlog = "later"
`
	path := write(t, filepath.Join(t.TempDir(), "config.toml"), old)
	msg, err := Migrate(path)
	if err != nil || !strings.HasPrefix(msg, "Rewrote "+path) {
		t.Fatalf("Migrate = %q %v", msg, err)
	}
	want := `start_skills = ["a"]

# Board sections.
[board]
# The groups, top first.
order = ["needs_you", "up_next", "working"] # mine
collapsed = ["up_next", "backlog"]
  hidden = ["up_next"]

[github.labels]
backlog = "later"
`
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("migrated file:\n%s\nwant\n%s", got, want)
	}
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	wantBoard := Board{
		Order:     []string{"Needs you", "Up next", "Working", "Follow-up", "Backlog", "Waiting", "Done"},
		Collapsed: []string{"Up next", "Backlog"},
		Hidden:    []string{"Up next"},
	}
	if b, err := g.Board(); err != nil || !reflect.DeepEqual(b, wantBoard) {
		t.Errorf("board %+v, %v, want %+v", b, err, wantBoard)
	}
	if msg, err := Migrate(path); err != nil || !strings.Contains(msg, "changed nothing") {
		t.Errorf("a second migration: %q %v", msg, err)
	}
}

func TestMigrateKeepsThePlaceOfTheFirstMovedSection(t *testing.T) {
	for old, want := range map[string]string{
		`order = ["needs_you", "ready", "queued", "working"]`: `order = ["needs_you", "up_next", "working"]`,
		`order = ["queued", "done", "ready"]`:                 `order = ["up_next", "done"]`,
		`collapsed = ["backlog", "ready"]`:                    `collapsed = ["backlog", "up_next"]`,
		`hidden = ["queued"]`:                                 `hidden = ["up_next"]`,
		`order = ["up_next", "ready"]`:                        `order = ["up_next"]`,
	} {
		path := write(t, filepath.Join(t.TempDir(), "config.toml"), "[board]\n"+old+"\n")
		if _, err := Migrate(path); err != nil {
			t.Errorf("%s: %v", old, err)
			continue
		}
		if got, _ := os.ReadFile(path); string(got) != "[board]\n"+want+"\n" {
			t.Errorf("%s: migrated to\n%s\nwant %s", old, got, want)
		}
	}
}

func TestMigrateKeepsABackupThatExists(t *testing.T) {
	home := fixtureHome(t)
	old := "[[repos]]\nmatch = \"~/repos/*/*\"\n"
	path := write(t, filepath.Join(home, ".config", "lineup", "config.toml"), old)
	write(t, path+".bak", "an older backup\n")
	_, err := Migrate(path)
	if want := path + ".bak already exists. Move it away, then run lineup config migrate again"; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got, _ := os.ReadFile(path + ".bak"); string(got) != "an older backup\n" {
		t.Errorf("the backup changed:\n%s", got)
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Errorf("the file changed:\n%s", got)
	}
}
