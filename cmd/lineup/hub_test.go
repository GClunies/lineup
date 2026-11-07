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
	if got := strings.Join(agentArgv("", "/bin/claude", "planner"), " "); got != "env -u CLAUDE_CONFIG_DIR /bin/claude --name planner --remote-control planner" {
		t.Errorf("default profile: %s", got)
	}
	if got := strings.Join(agentArgv("/p/work", "/bin/claude", "orchestrator"), " "); got != "env CLAUDE_CONFIG_DIR=/p/work /bin/claude --name orchestrator --remote-control orchestrator" {
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
