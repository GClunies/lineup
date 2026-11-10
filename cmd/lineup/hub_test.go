package main

import (
	"encoding/json"
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

func TestAgentTabsHaveOneAgent(t *testing.T) {
	var agents []string
	for _, tab := range agentTabs {
		if tab.agent != "" {
			agents = append(agents, tab.window)
		}
	}
	if !slices.Equal(agents, []string{"assist", "work"}) {
		t.Errorf("tabs with an agent = %v, want assist and work", agents)
	}
	for _, c := range []struct {
		window string
		board  []string
	}{{"plan", []string{"board", "--linear"}}, {"review", []string{"board", "--review"}}} {
		i := slices.IndexFunc(agentTabs, func(a agentTab) bool { return a.window == c.window })
		if i < 0 || !slices.Equal(agentTabs[i].boardArgs, c.board) {
			t.Errorf("the %s tab does not run lineup %v", c.window, c.board)
		}
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

func TestRunBoardRefusesAnUnknownFlag(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"--linear", "--review"}} {
		if err := runBoard(args); err == nil || !strings.Contains(err.Error(), "--review") {
			t.Errorf("runBoard(%q) = %v", args, err)
		}
	}
}

func TestOpenHubRenamesTheOrchestrateTab(t *testing.T) {
	old := hubWindow{ID: "w7", Label: "orchestrate"}
	cases := []struct {
		name    string
		windows map[string]hubWindow
		label   string
		want    string // the ID of the window to rename, or ""
	}{
		{"an old Hub", map[string]hubWindow{"orchestrate": old, "plan": {ID: "w8"}}, "work", "w7"},
		{"a Hub with both", map[string]hubWindow{"orchestrate": old, "work": {ID: "w9"}}, "work", ""},
		{"a new Hub", map[string]hubWindow{}, "work", ""},
		{"a tab that kept its label", map[string]hubWindow{"orchestrate": old}, "plan", ""},
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
	var order []string
	for _, tab := range agentTabs {
		order = append(order, tab.window)
	}
	if !slices.Equal(order, []string{"assist", "plan", "work", "review"}) {
		t.Errorf("tab order = %v, want assist, plan, work, review", order)
	}
	if i := slices.IndexFunc(agentTabs, func(a agentTab) bool { return a.window == activeTab }); i < 0 || agentTabs[i].agent != "LINEUP" {
		t.Errorf("a new Hub opens on %q, want the work tab with LINEUP", activeTab)
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
		{[]string{"ASSIST", "shell"}, false},
	} {
		if got := retired(hubWindow{Blocks: c.blocks}); got != c.want {
			t.Errorf("retired(%v) = %v, want %v", c.blocks, got, c.want)
		}
	}
}
