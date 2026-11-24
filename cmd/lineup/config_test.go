package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/config"
)

func TestUsageListsTheConfigCommands(t *testing.T) {
	for _, want := range []string{
		"\n  lineup config                open the config panel",
		"\n  lineup config keys [--json] [--repo <owner>/<repo>]\n",
		"\n  lineup config migrate        rewrite the config file",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("lineup --help does not list %q", strings.TrimSpace(want))
		}
	}
}

func TestConfigWithNoArgumentOpensThePanel(t *testing.T) {
	t.Setenv("LINEUP_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	opened := 0
	panel := func() error { opened++; return nil }
	var out bytes.Buffer
	if err := cmdConfig(nil, &out, panel); err != nil || opened != 1 {
		t.Fatalf("lineup config: %v, opened the panel %d times, want once", err, opened)
	}
	for _, args := range [][]string{{"frob"}, {"migrate", "now"}, {"keys", "extra"}, {"keys", "--frob"}} {
		if err := cmdConfig(args, &out, panel); !errors.Is(err, errUsage) {
			t.Errorf("lineup config %q: %v, want a usage error", args, err)
		}
	}
	if opened != 1 {
		t.Errorf("a sub-command opened the panel")
	}
}

// configKeysOf runs lineup config keys with args on the config text.
func configKeysOf(t *testing.T, text string, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if text != "" {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LINEUP_CONFIG", path)
	var out bytes.Buffer
	if err := cmdConfigKeys(args, &out); err != nil {
		t.Fatalf("lineup config keys %q: %v", args, err)
	}
	return out.String()
}

// keysJSON is what the Orchestrator reads from lineup config keys --json.
type keysJSON struct {
	Keys      []keyJSON       `json:"keys"`
	Repo      string          `json:"repo"`
	Settings  []configSetting `json:"settings"`
	LoadError string          `json:"load_error"`
}

type keyJSON struct {
	Path    string   `json:"path"`
	Levels  []string `json:"levels"`
	Restart string   `json:"restart"`
}

func decodeKeys(t *testing.T, out string) keysJSON {
	t.Helper()
	var v keysJSON
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	return v
}

func TestConfigKeysListsEveryKey(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(configKeysOf(t, ""), "\n"), "\n")
	if len(lines) != len(config.Catalog)+1 || !strings.HasPrefix(lines[0], "KEY ") || !strings.Contains(lines[0], "APPLIES AFTER") {
		t.Fatalf("the table has %d lines, want a header and %d keys:\n%s", len(lines), len(config.Catalog), strings.Join(lines, "\n"))
	}
	for i, k := range config.Catalog {
		if !strings.HasPrefix(lines[i+1], k.Path+" ") || !strings.HasSuffix(lines[i+1], k.Doc) || !strings.Contains(lines[i+1], k.Restart) {
			t.Errorf("line %d = %q, want %s with its restart and doc", i+1, lines[i+1], k.Path)
		}
	}

	v := decodeKeys(t, configKeysOf(t, "", "--json"))
	if len(v.Keys) != len(config.Catalog) || v.Settings != nil || v.LoadError != "" {
		t.Fatalf("--json = %+v, want each key of the catalog and no settings", v)
	}
	for i, k := range config.Catalog {
		var levels []string
		for _, l := range k.Levels {
			levels = append(levels, l.String())
		}
		if got := v.Keys[i]; got.Path != k.Path || !slices.Equal(got.Levels, levels) || got.Restart != k.Restart {
			t.Errorf("key %d = %+v, want %s at %q", i, got, k.Path, levels)
		}
	}
	if i := slices.IndexFunc(v.Keys, func(k keyJSON) bool { return k.Path == "wip_cap" }); i < 0 || !slices.Equal(v.Keys[i].Levels, []string{"[defaults]", "owner/*", "owner/repo"}) {
		t.Errorf("wip_cap does not name its levels as the file writes them")
	}
}

const keysConfig = `[defaults]
wip_cap = 4

[repos."acme/*"]
poll_interval = "20s"
tracker = "linear"

[repos."acme/api"]
wip_cap = 2
teams = ["ENG"]
`

func TestConfigKeysNamesTheValueAndTheLevelForARepo(t *testing.T) {
	v := decodeKeys(t, configKeysOf(t, keysConfig, "--json", "--repo", "acme/api"))
	got := map[string]configSetting{}
	for _, s := range v.Settings {
		got[s.Path] = s
	}
	for _, want := range []configSetting{
		{"wip_cap", "2", "acme/api"},
		{"teams", `["ENG"]`, "acme/api"},
		{"poll_interval", "20s", "acme/*"},
		{"tracker", "linear", "acme/*"},
		{"budgets.ci_debug", "3", "default"},
	} {
		if got[want.Path] != want {
			t.Errorf("%s = %+v, want %+v", want.Path, got[want.Path], want)
		}
	}
	if v.Repo != "acme/api" || len(v.Keys) != len(config.Catalog) {
		t.Errorf("repo %q with %d keys, want acme/api with the catalog", v.Repo, len(v.Keys))
	}

	table := configKeysOf(t, keysConfig, "--repo", "acme/api")
	if !strings.HasPrefix(table, "KEY ") || !slices.ContainsFunc(strings.Split(table, "\n"), func(l string) bool {
		return slices.Equal(strings.Fields(l), []string{"poll_interval", "20s", "acme/*"})
	}) {
		t.Errorf("the table does not show poll_interval 20s from acme/*:\n%s", table)
	}

	t.Setenv("LINEUP_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	if err := cmdConfigKeys([]string{"--repo", "acme"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not a Repo key") {
		t.Errorf("--repo acme: %v, want the error of a bad Repo key", err)
	}
}

func TestConfigKeysWorksOnAFileThatDoesNotLoad(t *testing.T) {
	bad := "[defaults]\nwip_cap = \"four\"\n"
	v := decodeKeys(t, configKeysOf(t, bad, "--json", "--repo", "acme/api"))
	if !strings.Contains(v.LoadError, "wip_cap") || len(v.Keys) != len(config.Catalog) {
		t.Fatalf("load_error %q with %d keys, want the error and the catalog", v.LoadError, len(v.Keys))
	}
	if i := slices.IndexFunc(v.Settings, func(s configSetting) bool { return s.Path == "wip_cap" }); i < 0 || v.Settings[i] != (configSetting{"wip_cap", "four", "[defaults]"}) {
		t.Errorf("settings %+v, want wip_cap four from [defaults]", v.Settings)
	}
	if v := decodeKeys(t, configKeysOf(t, bad, "--json")); v.LoadError == "" {
		t.Error("--json with no --repo does not name the load error")
	}
	if out := configKeysOf(t, bad); !strings.HasPrefix(out, "The file does not load: ") {
		t.Errorf("the table does not start with the load error:\n%s", out)
	}
}

func TestPanelReposNamesEachRepoOnceAndTheRepoHere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("[repos.\"acme/web\"]\nwip_cap = 2\n"), 0o600)
	f, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checkouts := []config.Checkout{
		{Path: "/src/web", Key: "acme/web"},
		{Path: "/src/api", Key: "acme/api"},
		{Path: "/src/api-copy", Key: "ACME/API"},
		{Path: "/src/zed/tools", Key: "beta/tools"},
		{Path: "/src/zed", Key: "Beta/zed"},
	}
	for _, c := range []struct{ dir, here string }{
		{"/src/api/cmd", "acme/api"},
		{"/src/api-copy", "acme/api"},
		{"/src/zed/tools/x", "beta/tools"},
		{"/src/zed", "Beta/zed"},
		{"/elsewhere", "acme/web"},
	} {
		repos, here := panelRepos(f, checkouts, c.dir)
		if want := []string{"acme/api", "acme/web", "beta/tools", "Beta/zed"}; !slices.Equal(repos, want) {
			t.Errorf("repos = %q, want %q", repos, want)
		}
		if here != c.here {
			t.Errorf("in %s the Repo tab shows %q first, want %q", c.dir, here, c.here)
		}
	}

	empty, err := config.ReadFile(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, here := panelRepos(empty, checkouts, "/elsewhere"); here != "" {
		t.Errorf("with no Repo table the Repo tab shows %q first, want none", here)
	}
}
