package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// layeredConfig sets keys at each level, and each table sets only some keys.
const layeredConfig = `roots = ["~/repos"]

[defaults]
wip_cap = 7
[defaults.budgets]
ci_debug = 1
[defaults.stage.default]
model = "global-model"
effort = "low"
[defaults.stage.working]
model = "working-model"
[defaults.landing_bar]
threads = "resolved"

[repos."acme/*"]
poll_interval = "20s"
tracker = "linear"
[repos."acme/*".stage.default]
effort = "max"
[repos."acme/*".review_bot]
name = "greptile"

[repos."acme/api"]
wip_cap = 2
teams = ["ENG"]
[repos."acme/api".stage.ci_debug]
skill = "diagnosing-bugs"
[repos."acme/api".review_bot]
min_review_score = 4
[repos."acme/api".landing_bar]
checks = "all"
`

// readEdit reads the text at a new path below dir.
func readEdit(t *testing.T, dir, text string) *File {
	t.Helper()
	f, err := ReadFile(write(t, filepath.Join(dir, "config.toml"), text))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func row(t *testing.T, rows []Setting, path string) Setting {
	t.Helper()
	i := slices.IndexFunc(rows, func(s Setting) bool { return s.Path == path })
	if i < 0 {
		t.Fatalf("no row %s", path)
	}
	return rows[i]
}

func ptr(s string) *string { return &s }

func TestSettingsNameTheLevelOfEachValue(t *testing.T) {
	f := readEdit(t, t.TempDir(), layeredConfig)
	for _, c := range []struct{ repo, path, value, source string }{
		{"acme/api", "wip_cap", "2", "acme/api"},
		{"acme/api", "poll_interval", "20s", "acme/*"},
		{"acme/api", "budgets.ci_debug", "1", "[defaults]"},
		{"acme/api", "budgets.responding", "3", "default"},
		{"acme/api", "tracker", "linear", "acme/*"},
		{"acme/api", "teams", `["ENG"]`, "acme/api"},
		{"acme/api", "prefix", "none", "default"},
		{"acme/api", "stage.working.model", "working-model", "[defaults]"},
		{"acme/api", "stage.reviewing.model", "global-model", "[defaults] (stage.default)"},
		{"acme/api", "stage.working.effort", "max", "acme/* (stage.default)"},
		{"acme/api", "review_bot.min_review_score", "4", "acme/api"},
		{"acme/web", "review_bot.min_review_score", "5", "default"},
		{"acme/web", "wip_cap", "7", "[defaults]"},
		{"ACME/API", "wip_cap", "2", "acme/api"},
		{"octo/tools", "review_bot.name", "none: no gate", "default"},
		{"", "wip_cap", "7", "[defaults]"},
		{"", "poll_interval", "30s", "default"},
		{"", "roots", `["~/repos"]`, "top level"},
		{"", "local_tracker", "false", "default"},
	} {
		s := row(t, f.Settings(c.repo), c.path)
		if s.Value != c.value || s.Source() != c.source {
			t.Errorf("%q %s = %s from %s, want %s from %s", c.repo, c.path, s.Value, s.Source(), c.value, c.source)
		}
	}
	global := f.Settings("")
	for _, path := range []string{"tracker", "teams", "prefix"} {
		if slices.ContainsFunc(global, func(s Setting) bool { return s.Path == path }) {
			t.Errorf("the Global tab lists %s, a key of a Repo table", path)
		}
	}
	if slices.ContainsFunc(f.Settings("acme/api"), func(s Setting) bool { return s.Path == "roots" }) {
		t.Error("the Repo tab lists roots, a top-level key")
	}
	if slices.ContainsFunc(global, func(s Setting) bool { return s.Path == "stage.ci_debug.model" }) {
		t.Error("the Global tab lists stage.ci_debug.model, which the loader refuses")
	}
}

// TestSettingsAgreeWithResolve checks each layer key that Resolve gives,
// for each Repo of the fixture.
func TestSettingsAgreeWithResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repos := map[string]string{"acme/api": "repos/acme/api", "acme/web": "repos/acme/web", "octo/tools": "repos/octo/tools"}
	for key, rel := range repos {
		gitCheckout(t, home, rel, "git@github.com:"+key+".git")
	}
	path := write(t, filepath.Join(home, ".config", "lineup", "config.toml"), layeredConfig)
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeBin(t, t.TempDir(), "claude")
	g.lookPath = func(string) (string, error) { return bin, nil }
	f, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	orNone := func(s string) string { return cmpOr(s, "none") }
	for key, rel := range repos {
		r, err := g.Resolve(filepath.Join(home, rel), nil)
		if err != nil {
			t.Fatal(err)
		}
		rows := f.Settings(key)
		value := func(path string) string { return row(t, rows, path).Value }
		interval, err := time.ParseDuration(value("poll_interval"))
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"wip_cap":               strconv.Itoa(r.WIPCap),
			"budgets.ci_debug":      strconv.Itoa(r.Budgets.CIDebug),
			"budgets.responding":    strconv.Itoa(r.Budgets.Responding),
			"landing_bar.checks":    r.Bar.Checks.String(),
			"landing_bar.threads":   r.Bar.Threads.String(),
			"landing_bar.approvals": r.Bar.Approvals.String(),
			"review_bot.name":       "none: no gate",
		}
		if b := r.Bar.Bot; b != nil {
			want["review_bot.name"] = b.Bot
			want["review_bot.min_review_score"] = strconv.Itoa(b.MinScore)
			want["review_bot.max_review_cycles"] = strconv.Itoa(r.Budgets.ReviewCycles)
		}
		for st, sc := range r.Stages {
			want["stage."+st.Key()+".skill"] = orNone(sc.Skill)
			want["stage."+st.Key()+".command"] = orNone(sc.Command)
			if st.StartsWorker() {
				want["stage."+st.Key()+".model"] = sc.Model
				want["stage."+st.Key()+".effort"] = sc.Effort
			}
		}
		if interval != r.PollInterval {
			t.Errorf("%s poll_interval = %s, Resolve gives %s", key, interval, r.PollInterval)
		}
		for path, w := range want {
			if got := value(path); got != w {
				t.Errorf("%s %s = %s, Resolve gives %s", key, path, got, w)
			}
		}
		if len(r.Stages) != len(lifecycle.Stages())-5 {
			t.Fatalf("Resolve gives %d agent Stages", len(r.Stages))
		}
	}
}

func cmpOr(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

func TestApplyWritesAnEditThatLoads(t *testing.T) {
	f := readEdit(t, t.TempDir(), layeredConfig)
	if err := f.Apply(Edit{Level: LevelRepo, Repo: "acme/api", Path: "wip_cap", Value: ptr("3")}); err != nil {
		t.Fatal(err)
	}
	disk := readFile(t, f.Path)
	if want := strings.Replace(layeredConfig, "wip_cap = 2", "wip_cap = 3", 1); disk != want || f.Text != want {
		t.Fatalf("the file is\n%s\nwant\n%s", disk, want)
	}
	if _, err := Load(f.Path); err != nil {
		t.Fatal(err)
	}
	if s := row(t, f.Settings("acme/api"), "wip_cap"); s.Value != "3" {
		t.Errorf("after the edit, wip_cap = %s", s.Value)
	}
}

func TestApplyKeepsTheFileWhenTheLoaderRefusesTheEdit(t *testing.T) {
	f := readEdit(t, t.TempDir(), layeredConfig)
	err := f.Apply(Edit{Level: LevelRepo, Repo: "acme/api", Path: "wip_cap", Value: ptr("0")})
	if err == nil || !strings.HasSuffix(err.Error(), `repos."acme/api": wip_cap must be at least 1`) || !strings.HasPrefix(err.Error(), f.Path+": ") {
		t.Fatalf("err = %v, want the loader's error", err)
	}
	if disk := readFile(t, f.Path); disk != layeredConfig || f.Text != layeredConfig {
		t.Fatalf("a refused edit changed the file:\n%s", disk)
	}
}

func TestApplyRefusesALevelThatTheCatalogDoesNotAllow(t *testing.T) {
	f := readEdit(t, t.TempDir(), layeredConfig)
	err := f.Apply(Edit{Level: LevelOwner, Repo: "acme/*", Path: "prefix", Value: ptr(`"ac"`)})
	if err == nil || err.Error() != "prefix cannot be set at owner/*: set it at owner/repo" {
		t.Fatalf("err = %v", err)
	}
	for _, k := range Catalog {
		for l := LevelTop; l <= LevelRepo; l++ {
			if slices.Contains(k.Levels, l) {
				continue
			}
			repo := map[Level]string{LevelOwner: "acme/*", LevelRepo: "acme/api"}[l]
			path := strings.NewReplacer("<stage>", "default", "<event>", "started", "<workspace>", "acme", "<team>", "ENG", "<field>", "name").Replace(k.Path)
			if err := f.Apply(Edit{Level: l, Repo: repo, Path: path, Value: ptr("1")}); err == nil || !strings.Contains(err.Error(), "cannot be set at "+l.String()) {
				t.Errorf("%s at %s: err = %v", path, l, err)
			}
		}
	}
	if disk := readFile(t, f.Path); disk != layeredConfig {
		t.Fatalf("a refused edit changed the file:\n%s", disk)
	}
}

func TestApplyRefusesAFileThatChangedOnDisk(t *testing.T) {
	f := readEdit(t, t.TempDir(), layeredConfig)
	other := layeredConfig + "local_tracker = true\n"
	write(t, f.Path, other)
	err := f.Apply(Edit{Level: LevelDefaults, Path: "wip_cap", Value: ptr("4")})
	if !errors.Is(err, ErrFileChanged) {
		t.Fatalf("err = %v, want ErrFileChanged", err)
	}
	if disk := readFile(t, f.Path); disk != other {
		t.Fatalf("the edit wrote over the other change:\n%s", disk)
	}
}

func TestAFileThatDoesNotLoadIsReadOnly(t *testing.T) {
	text := layeredConfig + "[review_bot]\nname = \"greptile\"\n"
	f := readEdit(t, t.TempDir(), text)
	if f.LoadErr == nil {
		t.Fatal("the fixture loads")
	}
	if s := row(t, f.Settings("acme/api"), "wip_cap"); s.Value != "2" || s.Source() != "acme/api" {
		t.Errorf("a file that does not load shows wip_cap = %s from %s", s.Value, s.Source())
	}
	if err := f.Apply(Edit{Level: LevelDefaults, Path: "wip_cap", Value: ptr("4")}); err == nil || !strings.Contains(err.Error(), f.LoadErr.Error()) {
		t.Fatalf("err = %v", err)
	}
	if disk := readFile(t, f.Path); disk != text {
		t.Fatal("the edit wrote a file that does not load")
	}
	bad := readEdit(t, t.TempDir(), "wip_cap = = 3\n")
	if bad.Raw != nil || bad.LoadErr == nil || len(bad.Settings("")) == 0 {
		t.Errorf("a file that is not TOML: Raw %v, LoadErr %v", bad.Raw, bad.LoadErr)
	}
}

func TestApplyWritesEachEditInPlace(t *testing.T) {
	const text = `roots = ["~/repos"]

[defaults]
wip_cap = 5 # five at once

[repos."acme/api"]
tracker = "linear"
[repos."acme/api".review_bot]
name = "greptile"

# Board sections.
[board]
hidden = []
`
	for _, c := range []struct {
		name string
		edit Edit
		want string
	}{
		{"a value keeps its comment", Edit{Level: LevelDefaults, Path: "wip_cap", Value: ptr("6")},
			strings.Replace(text, "wip_cap = 5 #", "wip_cap = 6 #", 1)},
		{"a new key follows the keys of its table", Edit{Level: LevelDefaults, Path: "poll_interval", Value: ptr(`"1m"`)},
			strings.Replace(text, "five at once\n", "five at once\npoll_interval = \"1m\"\n", 1)},
		{"a new top-level key follows the top-level keys", Edit{Level: LevelTop, Path: "local_tracker", Value: ptr("true")},
			strings.Replace(text, "]\n\n[defaults]", "]\nlocal_tracker = true\n\n[defaults]", 1)},
		{"a new table follows the tables of its Repo", Edit{Level: LevelRepo, Repo: "acme/api", Path: "budgets.ci_debug", Value: ptr("2")},
			strings.Replace(text, "name = \"greptile\"\n", "name = \"greptile\"\n[repos.\"acme/api\".budgets]\nci_debug = 2\n", 1)},
		{"a new Repo gets its table", Edit{Level: LevelRepo, Repo: "acme/web", Path: "review_bot.name", Value: ptr(`"greptile"`)},
			strings.Replace(text, "name = \"greptile\"\n", "name = \"greptile\"\n\n[repos.\"acme/web\"]\n[repos.\"acme/web\".review_bot]\nname = \"greptile\"\n", 1)},
		{"a new table of its own goes at the end", Edit{Level: LevelTop, Path: "layout.agent_size", Value: ptr("40")},
			text + "\n[layout]\nagent_size = 40\n"},
		{"a removal keeps the trailing comment", Edit{Level: LevelDefaults, Path: "wip_cap"},
			strings.Replace(text, "wip_cap = 5 # five at once", "# five at once", 1)},
		{"a removal keeps the header", Edit{Level: LevelTop, Path: "board.hidden"},
			strings.Replace(text, "hidden = []\n", "", 1)},
	} {
		f := readEdit(t, t.TempDir(), text)
		if err := f.Apply(c.edit); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := readFile(t, f.Path); got != c.want {
			t.Errorf("%s: the file is\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

func TestApplyRefusesAKeyThatItCannotPlaceSafely(t *testing.T) {
	for _, c := range []struct{ text, path string }{
		{"[defaults]\nbudgets = { ci_debug = 3 }\n", "budgets.responding"},
		{"[defaults]\nbudgets.ci_debug = 3\n", "budgets.responding"},
		{"[defaults]\nbudgets = { ci_debug = 3 }\n", "budgets.ci_debug"},
	} {
		f := readEdit(t, t.TempDir(), c.text)
		err := f.Apply(Edit{Level: LevelDefaults, Path: c.path, Value: ptr("2")})
		if err == nil || !strings.Contains(err.Error(), "edit it in the file") {
			t.Errorf("%q: set %s: err = %v", c.text, c.path, err)
		}
		if readFile(t, f.Path) != c.text {
			t.Errorf("%q: a refused edit changed the file", c.text)
		}
	}
}

func TestApplyMakesAMissingFile(t *testing.T) {
	f, err := ReadFile(filepath.Join(t.TempDir(), "lineup", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(Edit{Level: LevelTop, Path: "roots", Value: ptr(`["~/repos"]`)}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.Path); got != "roots = [\"~/repos\"]\n" {
		t.Fatalf("the new file is %q", got)
	}
}

// commentsAndBlanks are the comment lines and the blank lines of a text.
func commentsAndBlanks(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "#") {
			out = append(out, l)
		}
	}
	return out
}

// isSubsequence reports whether each of want is in got, in order.
func isSubsequence(want, got []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

// sampleValue is a value of the key that the loader takes at each level of
// the fixture.
func sampleValue(k Key) string {
	switch k.Path {
	case "layout.agent_size":
		return "20"
	case "prefix":
		return `"zz"`
	case "teams":
		return `["ENG2"]`
	}
	switch k.Type {
	case TypeInt:
		return "2"
	case TypeBool:
		return "true"
	case TypeDuration:
		return `"1m"`
	case TypeEnum:
		return strconv.Quote(k.Values[0])
	case TypeList:
		return listText(append(slices.Clone(k.Values), "x")[:1])
	}
	return `"x"`
}

// TestEveryKeySetsAndRemovesAtEveryLevel sets and then removes each
// concrete key at each level that the catalog allows, on a copy of a real
// config file with comments. Each pair of edits starts from the copy, as the
// loader ties some keys together. Each edit must load, show at its level, and
// keep each comment line and blank line of the file in order.
func TestEveryKeySetsAndRemovesAtEveryLevel(t *testing.T) {
	orig, err := os.ReadFile("testdata/edit/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	keep := commentsAndBlanks(string(orig))
	tables := map[Level][]string{
		LevelTop: {""}, LevelDefaults: {""},
		LevelOwner: {"acme/*", "neworg/*"},
		LevelRepo:  {"acme/api", "acme/web", "acme/new"},
	}
	dir := t.TempDir()
	edits := 0
	for _, k := range Catalog {
		if k.Type == TypeQueue {
			continue
		}
		for _, l := range k.Levels {
			for _, repo := range tables[l] {
				tab := repo
				if l == LevelOwner {
					tab = strings.Replace(repo, "*", "any", 1)
				}
				var paths []string
				for _, s := range readEdit(t, dir, string(orig)).Settings(tab) {
					if s.Key.Path == k.Path {
						paths = append(paths, s.Path)
					}
				}
				if len(paths) == 0 {
					t.Errorf("%s has no row on the tab of %q", k.Path, tab)
				}
				for _, path := range paths {
					f := readEdit(t, dir, string(orig))
					w := Where{Level: l, Table: repo}
					for _, value := range []*string{ptr(sampleValue(k)), nil} {
						if err := f.Apply(Edit{Level: l, Repo: repo, Path: path, Value: value}); err != nil {
							t.Fatalf("%s at %s, value %v: %v\n%s", path, w, value != nil, err, f.Text)
						}
						edits++
						if disk := readFile(t, f.Path); disk != f.Text {
							t.Fatalf("%s at %s: the file on disk is not the text", path, w)
						}
						if !isSubsequence(keep, commentsAndBlanks(f.Text)) {
							t.Fatalf("%s at %s lost a comment line or a blank line:\n%s", path, w, f.Text)
						}
						s := row(t, f.Settings(tab), path)
						if slices.Contains(s.SetAt, w) != (value != nil) {
							t.Fatalf("%s at %s, value %v: the row says it is set at %v", path, w, value != nil, s.SetAt)
						}
					}
				}
			}
		}
	}
	if edits < 300 {
		t.Fatalf("the test ran %d edits", edits)
	}
}
