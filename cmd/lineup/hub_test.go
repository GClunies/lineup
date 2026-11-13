package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestAgentTabsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, tab := range agentTabs {
		for _, k := range []string{tab.window, tab.view, tab.agent} {
			if k == "" {
				continue
			}
			if seen[k] {
				t.Errorf("label %q is used twice", k)
			}
			seen[k] = true
		}
	}
}

func TestAgentArgvUnsetsAnEmptyConfigDir(t *testing.T) {
	if got := strings.Join(agentArgv("", "/bin/claude", "lineup-plan"), " "); got != "env -u CLAUDE_CONFIG_DIR /bin/claude --name lineup-plan --remote-control lineup-plan" {
		t.Errorf("default profile: %s", got)
	}
	if got := strings.Join(agentArgv("/p/work", "/bin/claude", "lineup-work"), " "); got != "env CLAUDE_CONFIG_DIR=/p/work /bin/claude --name lineup-work --remote-control lineup-work" {
		t.Errorf("named profile: %s", got)
	}
}

func TestAdHocSessionRefusesTheHub(t *testing.T) {
	if _, err := adHocSession(hubSession); err == nil {
		t.Error("the Hub's session was accepted for ad hoc Tasks")
	}
	if got, err := adHocSession("ad hoc"); err != nil || got != "ad hoc" {
		t.Errorf("ad hoc = %q, %v", got, err)
	}
}

func TestTheAgentLearnsWhichTrackersAreOnAtEachPrompt(t *testing.T) {
	role, settings, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/bin/lineup")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"## Linear", "## GitHub", "## Local"} {
		if !strings.Contains(string(role), "\n"+name+"\n") {
			t.Errorf("the role has no %s section", name)
		}
	}
	if !strings.Contains(string(role), "Read `/Users/g/issues/local-7.md`") || strings.Contains(string(role), "{{") {
		t.Errorf("the Local section does not name the folder:\n%s", role)
	}
	var v struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
			Dirs  []string `json:"additionalDirectories"`
		} `json:"permissions"`
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &v); err != nil {
		t.Fatalf("settings are not valid JSON: %v\n%s", err, settings)
	}
	if !slices.Contains(v.Permissions.Allow, "Edit(//Users/g/issues/**)") || !slices.Equal(v.Permissions.Dirs, []string{"/Users/g/issues"}) {
		t.Errorf("the agent cannot write local Issues: %+v", v.Permissions)
	}
	if h := v.Hooks["UserPromptSubmit"]; len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != "'/Users/g/bin/lineup' trackers" {
		t.Errorf("no prompt hook runs lineup trackers: %+v", v.Hooks)
	}
	if got := trackersLine(trackersOn{linear: true, local: true}); !strings.HasPrefix(got, "Trackers on now: Linear, Local.") {
		t.Errorf("trackersLine = %q", got)
	}
}

func TestAgentRoleAsksWithAskUserQuestion(t *testing.T) {
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/bin/lineup")
	if err != nil {
		t.Fatal(err)
	}
	role := string(raw)
	_, questions, ok := strings.Cut(role, "\n## Questions\n")
	if !ok {
		t.Fatalf("the role has no Questions section:\n%s", role)
	}
	questions, _, _ = strings.Cut(questions, "\n## ")
	for _, want := range []string{
		"`AskUserQuestion`",
		"**Close the tab**", "**Open the tab**", "**Keep it open**",
		"**Restore**", "**Close the Task**",
		"**Answer here**", "`lineup steer <task>",
		"`lineup close <task>`", "`lineup open <task>`",
	} {
		if !strings.Contains(questions, want) {
			t.Errorf("the Questions section has no %s", want)
		}
	}
	for _, plain := range []string{"in chat", "Do not act on an alert yourself"} {
		if strings.Contains(role, plain) {
			t.Errorf("the role still says %q", plain)
		}
	}
	if !strings.Contains(role, "| `lineup open <task>` |") {
		t.Error("the Jobs table has no lineup open row")
	}
}

func TestHubHasOneTabAndItCarriesTheAgent(t *testing.T) {
	var tabs, agents []string
	for _, tab := range agentTabs {
		tabs = append(tabs, tab.window)
		if tab.agent != "" {
			agents = append(agents, tab.window)
		}
	}
	if !slices.Equal(tabs, []string{"hub"}) || !slices.Equal(agents, []string{"hub"}) {
		t.Errorf("tabs %v with agents %v, want hub with an agent", tabs, agents)
	}
	i := slices.IndexFunc(agentTabs, func(a agentTab) bool { return a.window == "hub" })
	if got := agentTabs[i]; got.agent != "LINEUP" || !slices.Equal(got.boardArgs, []string{"board"}) {
		t.Errorf("the hub tab runs %v over %q, want board over LINEUP", got.boardArgs, got.agent)
	}
}

// permits reports whether an allow rule and a deny rule of a Claude Code
// settings file match cmd.
func permits(t *testing.T, settings, cmd string) (allow, deny bool) {
	t.Helper()
	var v struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settings), &v); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	match := func(rule string) bool {
		prefix, ok := strings.CutPrefix(rule, "Bash(")
		prefix = strings.TrimSuffix(strings.TrimSuffix(prefix, ")"), ":*")
		return ok && (cmd == prefix || strings.HasPrefix(cmd, prefix+" "))
	}
	return slices.ContainsFunc(v.Permissions.Allow, match), slices.ContainsFunc(v.Permissions.Deny, match)
}

func TestAgentSettingsUnionAllowsEveryLineupCommand(t *testing.T) {
	cmds := []string{
		"lineup add ENG-1", "lineup start ENG-1", "lineup merge ENG-1", "lineup watch --once",
		"lineup review https://github.com/o/r/pull/1", "lineup reviews --json", "lineup steer R-1 hi",
		"linear issue update ENG-1", "linear issue relation add ENG-1 blocks ENG-2", "gh issue edit 12",
		"gh pr view 1", "gh search prs --review-requested @me",
	}
	// Every lineup command that the role tells the agent to run.
	for _, m := range regexp.MustCompile("`(lineup [a-z]+)").FindAllStringSubmatch(string(agentRole), -1) {
		cmds = append(cmds, m[1])
	}
	for _, cmd := range cmds {
		if allow, deny := permits(t, agentSettings, cmd); !allow || deny {
			t.Errorf("the agent's settings do not allow %q", cmd)
		}
	}
}

func TestAgentSettingsDenyKeepsGitHubWrites(t *testing.T) {
	for _, cmd := range []string{
		"gh pr review 1 --approve", "gh pr comment 1", "gh pr merge 1", "gh pr close 1", "gh pr edit 1",
		"gh api repos/o/r/pulls/1/reviews", "gh issue delete 12", "linear issue delete ENG-1",
	} {
		if allow, deny := permits(t, agentSettings, cmd); allow || !deny {
			t.Errorf("the agent's settings do not deny %q", cmd)
		}
	}
}

func TestRunBoardRefusesAnArgument(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"--linear"}, {"--review"}} {
		if err := runBoard(args); err == nil || !strings.Contains(err.Error(), "usage: lineup board") {
			t.Errorf("runBoard(%q) = %v", args, err)
		}
	}
}

func TestOpenHubRenamesTheWorkTab(t *testing.T) {
	old := hubWindow{ID: "w7", Label: "work"}
	cases := []struct {
		name    string
		windows map[string]hubWindow
		label   string
		want    string // the ID of the window to rename, or ""
	}{
		{"an old Hub", map[string]hubWindow{"work": old, "plan": {ID: "w8"}}, "hub", "w7"},
		{"a Hub with both", map[string]hubWindow{"work": old, "hub": {ID: "w9"}}, "hub", ""},
		{"a new Hub", map[string]hubWindow{}, "hub", ""},
	}
	for _, c := range cases {
		got := ""
		if w, ok := renamed(c.windows, c.label); ok {
			got = w.ID
		}
		if got != c.want {
			t.Errorf("%s: renamed %q = %q, want %q", c.name, c.label, got, c.want)
		}
	}
	if !slices.Equal(retiredTabs, []string{"orchestrate", "plan", "review"}) {
		t.Errorf("retired tabs = %v, want orchestrate, plan, review", retiredTabs)
	}
	if activeTab != "hub" {
		t.Errorf("a new Hub opens on %q, want hub", activeTab)
	}
}

func TestOpenHubReopensATabWithARetiredAgent(t *testing.T) {
	for _, c := range []struct {
		blocks []string
		want   bool
	}{
		{[]string{"BOARD", "ORCHESTRATOR"}, true},
		{[]string{"LINEAR", "PLANNER"}, true},
		{[]string{"REVIEWS", "REVIEWER"}, true},
		{[]string{"BOARD", "LINEUP"}, false},
		{[]string{"REVIEWS"}, false},
	} {
		if got := retired(hubWindow{Blocks: c.blocks}); got != c.want {
			t.Errorf("retired(%v) = %v, want %v", c.blocks, got, c.want)
		}
	}
}

func TestOpenHubRewritesTheAgentFilesWhenTheTabExists(t *testing.T) {
	bin := t.TempDir()
	for name, body := range map[string]string{
		"rex": `case "$1 $2" in
"session inspect") echo '{"windows":[
{"window_id":"w2","label":"hub","layers":[{"blocks":[{"label":"BOARD"},{"label":"LINEUP"}]}]}]}';;
esac`,
		"launchctl": "",
		"gh":        "",
		"wt":        "",
		"claude":    "",
	} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LINEUP_STATE_DIR", t.TempDir())
	t.Setenv("LINEUP_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	hub := agentTabs[0]
	dir := filepath.Join(stateDir(), strings.ToLower(hub.agent))
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o700)
	settings := filepath.Join(dir, ".claude", "settings.json")
	os.WriteFile(settings, []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"/tmp/lineup-s1 trackers"}]}]}}`), 0o600)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("an old prompt"), 0o600)

	if err := openHub(); err != nil {
		t.Fatal(err)
	}
	role, want, err := composeAgent(hub.role, hub.settings)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(settings)
	if string(got) != want || strings.Contains(string(got), "/tmp/lineup-s1") {
		t.Errorf("settings = %s, want %s", got, want)
	}
	if prompt, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); !bytes.Equal(prompt, role) {
		t.Errorf("CLAUDE.md is not the current prompt")
	}
}
