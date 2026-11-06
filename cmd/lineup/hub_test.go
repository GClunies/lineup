package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAgentTabSettings(t *testing.T) {
	for _, tab := range agentTabs {
		var v struct {
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(tab.settings), &v); err != nil {
			t.Fatalf("%s settings are not valid JSON: %v", tab.window, err)
		}
		if tab.window != "plan" {
			continue
		}
		for _, cmd := range []string{"Bash(lineup start:*)", "Bash(lineup add:*)"} {
			if !slices.Contains(v.Permissions.Deny, cmd) {
				t.Errorf("planner settings do not deny %s", cmd)
			}
			if slices.Contains(v.Permissions.Allow, cmd) {
				t.Errorf("planner settings allow %s", cmd)
			}
		}
	}
}

func TestAgentTabsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, tab := range agentTabs {
		for _, k := range []string{tab.window, tab.view, tab.agent} {
			if seen[k] {
				t.Errorf("label %q is used twice", k)
			}
			seen[k] = true
		}
	}
}

func TestAgentArgvUnsetsAnEmptyConfigDir(t *testing.T) {
	if got := strings.Join(agentArgv("", "/bin/claude"), " "); got != "env -u CLAUDE_CONFIG_DIR /bin/claude" {
		t.Errorf("default profile: %s", got)
	}
	if got := strings.Join(agentArgv("/p/work", "/bin/claude"), " "); got != "env CLAUDE_CONFIG_DIR=/p/work /bin/claude" {
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

func TestThePlannerLearnsWhichTrackersAreOnAtEachPrompt(t *testing.T) {
	role, settings, err := plannerFiles(plannerRole, plannerSettings, "/Users/g/issues", "/Users/g/bin/lineup")
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
		t.Errorf("the Planner cannot write local Issues: %+v", v.Permissions)
	}
	for _, cmd := range []string{"Bash(gh issue close:*)", "Bash(gh pr:*)"} {
		if !slices.Contains(v.Permissions.Deny, cmd) {
			t.Errorf("planner settings do not deny %s", cmd)
		}
	}
	if h := v.Hooks["UserPromptSubmit"]; len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != "'/Users/g/bin/lineup' trackers" {
		t.Errorf("no prompt hook runs lineup trackers: %+v", v.Hooks)
	}
	if got := trackersLine(trackersOn{linear: true, local: true}); !strings.HasPrefix(got, "Trackers on now: Linear, Local.") {
		t.Errorf("trackersLine = %q", got)
	}
}

func TestTheReviewTabHasTheReviewer(t *testing.T) {
	i := slices.IndexFunc(agentTabs, func(a agentTab) bool { return a.window == "review" })
	if i < 0 {
		t.Fatal("the Hub has no review tab")
	}
	tab := agentTabs[i]
	if tab.view != "REVIEWS" || !slices.Equal(tab.boardArgs, []string{"board", "--review"}) || tab.agent != "REVIEWER" {
		t.Errorf("review tab = %s %s %s %v", tab.window, tab.view, tab.agent, tab.boardArgs)
	}
	if !strings.Contains(string(tab.role), "# Reviewer") {
		t.Error("the review tab has no Reviewer role")
	}
	var v struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(tab.settings), &v); err != nil {
		t.Fatalf("reviewer settings are not valid JSON: %v", err)
	}
	for _, cmd := range []string{"Bash(lineup review:*)", "Bash(lineup reviews:*)", "Bash(lineup steer:*)"} {
		if !slices.Contains(v.Permissions.Allow, cmd) {
			t.Errorf("reviewer settings do not allow %s", cmd)
		}
	}
	for _, cmd := range []string{"Bash(lineup start:*)", "Bash(lineup merge:*)", "Bash(gh pr review:*)", "Bash(gh pr comment:*)", "Bash(gh api:*)"} {
		if !slices.Contains(v.Permissions.Deny, cmd) || slices.Contains(v.Permissions.Allow, cmd) {
			t.Errorf("reviewer settings do not deny %s", cmd)
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
	if agentTabs[0].window != "work" || agentTabs[0].agent != "ORCHESTRATOR" {
		t.Errorf("the first tab is %s with %s, want work with ORCHESTRATOR", agentTabs[0].window, agentTabs[0].agent)
	}
}

func TestTheOrchestratorLeavesReviewsToTheReviewer(t *testing.T) {
	var v struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(orchestratorSettings), &v); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"Bash(lineup review:*)", "Bash(lineup reviews:*)"} {
		if !slices.Contains(v.Permissions.Deny, cmd) {
			t.Errorf("orchestrator settings do not deny %s", cmd)
		}
	}
}
