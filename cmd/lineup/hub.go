package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/gclunies/lineup/internal/board"
	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/harness/claude"
	"github.com/gclunies/lineup/internal/linear"
)

//go:embed orchestrator/CLAUDE.md
var orchestratorRole []byte

//go:embed planner/CLAUDE.md
var plannerRole []byte

//go:embed planner/linear.md
var plannerLinear []byte

//go:embed planner/github.md
var plannerGitHub []byte

//go:embed planner/local.md
var plannerLocal []byte

const (
	// hubSession has a space, so no Repo session can take it. A Repo session
	// is named after its folder, and the Scheduler removes it after its last Task.
	hubSession = "lineup hub"
	agentRows  = 25 // percent of the tab
)

// watchPrompt is the Orchestrator's first message each time the Hub starts
// it, because a Monitor does not survive a restart of Claude.
const watchPrompt = "Start the lineup watch Monitor, as CLAUDE.md says under Alerts."

// orchestratorSettings gives the Orchestrator its status line and lets it run
// the lineup CLI without a permission prompt.
const orchestratorSettings = `{
  "statusLine": {"type": "command", "command": "echo Orchestrator"},
  "permissions": {"allow": ["Bash(lineup:*)"]}
}
`

// plannerSettings gives the Planner its status line, lets it edit Linear, and
// keeps it from starting Tasks.
const plannerSettings = `{
  "statusLine": {"type": "command", "command": "echo Planner"},
  "permissions": {
    "allow": [
      "Bash(linear issue view:*)",
      "Bash(linear issue query:*)",
      "Bash(linear issue mine:*)",
      "Bash(linear issue list:*)",
      "Bash(linear issue create:*)",
      "Bash(linear issue update:*)",
      "Bash(linear issue comment:*)",
      "Bash(linear issue relation:*)",
      "Bash(linear team:*)",
      "Bash(linear project:*)",
      "Bash(linear milestone:*)",
      "Bash(linear label:*)",
      "Bash(linear cycle:*)",
      "Bash(linear document:*)",
      "Bash(gh issue view:*)",
      "Bash(gh issue list:*)",
      "Bash(gh issue create:*)",
      "Bash(gh issue edit:*)",
      "Bash(gh issue comment:*)",
      "Bash(gh search issues:*)",
      "Read(/{{issues}}/**)",
      "Edit(/{{issues}}/**)",
      "Bash(lineup ls:*)",
      "Bash(lineup show:*)"
    ],
    "deny": [
      "Bash(lineup add:*)",
      "Bash(lineup start:*)",
      "Bash(linear issue start:*)",
      "Bash(linear issue pull-request:*)",
      "Bash(linear issue pr:*)",
      "Bash(linear issue delete:*)",
      "Bash(gh issue close:*)",
      "Bash(gh issue reopen:*)",
      "Bash(gh issue delete:*)",
      "Bash(gh pr:*)"
    ],
    "additionalDirectories": ["{{issues}}"]
  },
  "hooks": {
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "'{{lineup}}' trackers"}]}]
  }
}
`

// agentTab is one Hub tab: a Board view on top and a Claude agent below.
type agentTab struct {
	window, view, agent string // Rex window label, top block label, agent block label
	boardArgs           []string
	role                []byte
	settings            string
	bg                  string // agent pane background
	prompt              string // the agent's first message, if any
	// compose, if set, fills in role and settings when the Hub writes them.
	compose func(role []byte, settings string) ([]byte, string, error)
}

// agentTabs are in Hub tab order. The first is the active tab of a new Hub.
var agentTabs = []agentTab{
	{"orchestrate", "BOARD", "ORCHESTRATOR", []string{"board"}, orchestratorRole, orchestratorSettings, "#1b2733", watchPrompt, nil},
	{"plan", "LINEAR", "PLANNER", []string{"board", "--linear"}, plannerRole, plannerSettings, "#2a2233", "", composePlanner},
}

func rexRun(args ...string) ([]byte, error) {
	var errb bytes.Buffer
	cmd := exec.Command("rex", args...)
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rex %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

// prepareAgent writes the agent's folder and trusts it for Claude.
func prepareAgent(t agentTab) (string, error) {
	dir := filepath.Join(stateDir(), strings.ToLower(t.agent))
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o700); err != nil {
		return "", err
	}
	role, settings := t.role, t.settings
	if t.compose != nil {
		var err error
		if role, settings, err = t.compose(role, settings); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), role, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		return "", err
	}
	return dir, claude.Trust(os.Getenv("CLAUDE_CONFIG_DIR"), dir)
}

// trackersOn is which Trackers are on.
type trackersOn struct{ linear, github, local bool }

func currentTrackers() (trackersOn, error) {
	cfg, err := config.Load(configPath())
	if err != nil {
		return trackersOn{}, err
	}
	wss, _ := linear.Workspaces()
	_, githubOn, _ := github.TrackerLogin(stateDir())
	on := trackersOn{linear: len(wss) > 0, github: githubOn}
	on.local = cfg.LocalTrackerOn(on.linear, on.github)
	return on, nil
}

// trackersLine is what lineup trackers prints. The Planner's hook runs it on
// each prompt, as Claude Code reads CLAUDE.md only when the session starts,
// and a sign-in can come later.
func trackersLine(on trackersOn) string {
	var names []string
	for _, t := range []struct {
		on   bool
		name string
	}{{on.linear, "Linear"}, {on.github, "GitHub"}, {on.local, "Local"}} {
		if t.on {
			names = append(names, t.name)
		}
	}
	return "Trackers on now: " + strings.Join(names, ", ") + ". Work only in these Trackers' sections of your instructions."
}

// composePlanner fills in the Local Tracker's folder and the path of lineup.
func composePlanner(role []byte, settings string) ([]byte, string, error) {
	cfg, err := config.Load(configPath())
	if err != nil {
		return nil, "", err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	return plannerFiles(role, settings, cfg.LocalIssuesDir(), self)
}

func plannerFiles(role []byte, settings, issuesDir, self string) ([]byte, string, error) {
	out := slices.Concat(role, plannerLinear, plannerGitHub, plannerLocal)
	out = bytes.ReplaceAll(out, []byte("{{issues}}"), []byte(issuesDir))
	// The rules write /{{issues}}, as a rule path that starts with // is
	// absolute in Claude Code settings.
	for k, v := range map[string]string{"{{issues}}": filepath.ToSlash(issuesDir), "{{lineup}}": self} {
		q, err := json.Marshal(v)
		if err != nil {
			return nil, "", err
		}
		settings = strings.ReplaceAll(settings, k, strings.Trim(string(q), `"`))
	}
	return out, settings, nil
}

type hubWindow struct {
	ID     string
	Label  string
	Blocks []string
}

func hubWindows() (map[string]hubWindow, bool, error) {
	out, err := rexRun("session", "inspect", "--json", hubSession)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, false, nil
		}
		return nil, false, err
	}
	var v struct {
		Windows []struct {
			ID     string `json:"window_id"`
			Label  string `json:"label"`
			Layers []struct {
				Blocks []struct {
					ID string `json:"block_id"`
				} `json:"blocks"`
			} `json:"layers"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, false, err
	}
	ws := map[string]hubWindow{}
	for _, w := range v.Windows {
		hw := hubWindow{ID: w.ID, Label: w.Label}
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				hw.Blocks = append(hw.Blocks, b.ID)
			}
		}
		ws[w.Label] = hw
	}
	return ws, true, nil
}

// openHub starts the Scheduler service, then creates the Hub session or adds
// back whichever of its tabs is gone. Each Board retries the socket until it is up.
func openHub() error {
	if _, err := requireTools(); err != nil {
		return err
	}
	if err := startService(); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude is required and is not on PATH")
	}
	windows, exists, err := hubWindows()
	if err != nil {
		return err
	}
	var shell string
	if !exists {
		out, err := rexRun("session", "new", hubSession, "--window", "scratch", "--json")
		if err != nil {
			return err
		}
		var v struct {
			Windows []struct {
				ID string `json:"window_id"`
			} `json:"initial_windows"`
		}
		json.Unmarshal(out, &v)
		if len(v.Windows) > 0 {
			shell = v.Windows[0].ID
		}
	}

	for i, t := range agentTabs {
		if _, ok := windows[t.window]; ok {
			continue
		}
		if err := openAgentTab(t, self, claudeBin, i == 0); err != nil {
			return err
		}
		if _, err := rexRun("window", "move", "-s", hubSession, t.window, "--index", strconv.Itoa(i+1)); err != nil {
			return err
		}
	}
	if shell != "" {
		rexRun("window", "close", "-s", hubSession, shell)
	}
	fmt.Printf("The Hub is open in the Rex session %q.\n", hubSession)
	return nil
}

func openAgentTab(t agentTab, self, claudeBin string, focus bool) error {
	dir, err := prepareAgent(t)
	if err != nil {
		return err
	}
	out, err := rexRun(append([]string{"window", "new", "-s", hubSession, t.window, "--focus=" + strconv.FormatBool(focus), "--json", "--", self}, t.boardArgs...)...)
	if err != nil {
		return err
	}
	top, err := firstBlock(out)
	if err != nil {
		return err
	}
	if _, err := rexRun("block", "rename", "-s", hubSession, top, t.view); err != nil {
		return err
	}
	args := append([]string{"split", "-s", hubSession, "-b", top, "--split", "below",
		"--ratio", fmt.Sprint(agentRows), "--focus=false", "--json", "-c", dir, "--"},
		agentArgv(os.Getenv("CLAUDE_CONFIG_DIR"), claudeBin)...)
	if hasConversation(os.Getenv("CLAUDE_CONFIG_DIR"), dir) {
		args = append(args, "--continue")
	}
	if t.prompt != "" {
		args = append(args, t.prompt)
	}
	split, err := rexRun(args...)
	if err != nil {
		return err
	}
	agent, err := firstBlock(split)
	if err != nil {
		return err
	}
	if _, err := rexRun("block", "rename", "-s", hubSession, agent, t.agent); err != nil {
		return err
	}
	theme, _ := json.Marshal(map[string]string{"background": t.bg, "scheme": "dark"})
	_, err = rexRun("block", "call", "-s", hubSession, "-b", agent, "set_theme", string(theme))
	return err
}

// agentArgv runs Claude on the profile of the shell that opened the Hub. An
// empty CLAUDE_CONFIG_DIR is not the default profile: Claude reads it as the
// working folder, so the default profile unsets the variable.
func agentArgv(configDir, claudeBin string) []string {
	if configDir == "" {
		return []string{"env", "-u", "CLAUDE_CONFIG_DIR", claudeBin}
	}
	return []string{"env", "CLAUDE_CONFIG_DIR=" + configDir, claudeBin}
}

// hasConversation reports whether Claude has a conversation for dir to
// continue. Without one, `claude --continue` exits at once.
func hasConversation(configDir, dir string) bool {
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".claude")
	}
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(dir)
	found, _ := filepath.Glob(filepath.Join(configDir, "projects", slug, "*.jsonl"))
	return len(found) > 0
}

func firstBlock(out []byte) (string, error) {
	var v struct {
		BlockIDs []string `json:"block_ids"`
		BlockID  string   `json:"block_id"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("rex reply %q: %w", out, err)
	}
	if v.BlockID != "" {
		return v.BlockID, nil
	}
	if len(v.BlockIDs) == 0 {
		return "", fmt.Errorf("rex reply %q has no block", out)
	}
	return v.BlockIDs[0], nil
}

func runBoard(args []string) error {
	tracker := len(args) == 1 && args[0] == "--linear"
	if len(args) > 0 && !tracker {
		return fmt.Errorf("usage: lineup board [--linear]")
	}
	target := "ORCHESTRATOR"
	if tracker {
		target = "PLANNER"
	}
	opts := board.Options{
		Linear: tracker,
		Zoom:   func() { rexRun("zoom", "-s", hubSession, target) },
	}
	if g, err := config.Load(configPath()); err == nil { // a bad file only means defaults here
		opts.StartSkills = g.StartSkills()
		opts.Tracker.Repos = g.Checkouts
		opts.Tracker.Sources = boardSources(g, stateDir())
	}
	if !tracker {
		opts.Notify = func(text string) { board.Toast(os.Stdout, text) }
	}
	return board.Run(socketPath(), filepath.Join(stateDir(), "board.json"), opts)
}
