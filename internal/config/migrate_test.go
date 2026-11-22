package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixtureCheckouts are the checkouts below the fixture's ~/repos, by path
// from the home folder, with their origin URL. "" is a checkout with no
// origin. Two checkouts have the origin crexi/data, and two have the folder
// name de-interview-exercise.
var fixtureCheckouts = map[string]string{
	"repos/crexi/data":                      "git@github.com:crexi/data.git",
	"repos/crexi/data.review-pr-1":          "git@github.com:crexi/data.git",
	"repos/crexi/web":                       "https://github.com/crexi/web.git",
	"repos/crexi/de-interview-exercise":     "git@github.com:crexi/de-interview-exercise.git",
	"repos/crexi-dev/de-interview-exercise": "git@github.com:crexi-dev/de-interview-exercise.git",
	"repos/gclunies/hod":                    "git@github.com:GClunies/hod.git",
	"repos/gclunies/lineup":                 "git@github.com:GClunies/lineup.git",
	"repos/gclunies/brain":                  "",
}

// fixtureHome makes a home folder with the fixture checkouts, sets HOME to
// it, and returns it.
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
	Trackers []string
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
		d.Checkouts[rel] = settings{Resolved: r, Trackers: g.RepoTrackers(path), Prefix: g.RepoPrefix(path)}
	}
	if repo, ok := g.RepoForTeam("PD"); ok {
		d.TeamPD, _ = filepath.Rel(home, repo)
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
