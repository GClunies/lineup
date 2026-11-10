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

//go:embed agent/CLAUDE.md
var agentRole []byte

//go:embed agent/linear.md
var linearSection []byte

//go:embed agent/github.md
var githubSection []byte

//go:embed agent/local.md
var localSection []byte

const (
	// hubSession has a space, so no Repo session can take it. A Repo session
	// is named after its folder, and the Scheduler removes it after its last Task.
	hubSession = "lineup hub"
	agentRows  = 25 // percent of the tab
)

// watchPrompt is the Orchestrator's first message each time the Hub starts
// it, because a background command does not survive a restart of Claude.
const watchPrompt = "Start the lineup watch, as CLAUDE.md says under Alerts."

// agentSettings gives the Orchestrator its status line, lets it run lineup
// and edit the Trackers without a permission prompt, and keeps it from
// writing to a PR. Only the user posts a review or merges outside lineup.
const agentSettings = `{
  "statusLine": {"type": "command", "command": "echo Lineup"},
  "permissions": {
    "allow": [
      "Bash(lineup:*)",
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
      "Bash(gh pr view:*)",
      "Bash(gh pr list:*)",
      "Bash(gh search prs:*)",
      "Read(/{{issues}}/**)",
      "Edit(/{{issues}}/**)"
    ],
    "deny": [
      "Bash(linear issue start:*)",
      "Bash(linear issue pull-request:*)",
      "Bash(linear issue pr:*)",
      "Bash(linear issue delete:*)",
      "Bash(gh issue close:*)",
      "Bash(gh issue reopen:*)",
      "Bash(gh issue delete:*)",
      "Bash(gh pr review:*)",
      "Bash(gh pr comment:*)",
      "Bash(gh pr merge:*)",
      "Bash(gh pr close:*)",
      "Bash(gh pr edit:*)",
      "Bash(gh api:*)"
    ],
    "additionalDirectories": ["{{issues}}"]
  },
  "hooks": {
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "'{{lineup}}' trackers"}]}]
  }
}
`

// agentTab is one Hub tab: a Board view on top and a Claude agent below. A
// tab with no agent holds the Board view alone, at full height. A tab with no
// Board and no role holds only a general agent, in the folder that assist_dir
// names.
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

// agentTabs are in Hub tab order.
var agentTabs = []agentTab{
	{"assist", "", "ASSIST", nil, nil, "", "#262626", "", nil},
	{"hub", "BOARD", "LINEUP", []string{"board"}, agentRole, agentSettings, "#1b2733", watchPrompt, composeAgent},
}

// activeTab is the tab that a new Hub opens on.
const activeTab = "hub"

// formerLabels are the labels that a Hub tab had before a rename, by its
// label now.
var formerLabels = map[string]string{"hub": "work"}

// retiredTabs are the labels of tabs that no longer exist. Their windows hold
// a board with no agent, or a former agent, so openHub closes them.
var retiredTabs = []string{"orchestrate", "plan", "review"}

// renamed is the Hub window to relabel as label: the one under the tab's
// former label, when no window has the label yet. The rename keeps the agent
// that runs there. openHub keeps a window that it does not know, so a new tab
// beside the old one would run a second agent.
func renamed(windows map[string]hubWindow, label string) (hubWindow, bool) {
	old, ok := formerLabels[label]
	if _, now := windows[label]; !ok || now {
		return hubWindow{}, false
	}
	w, ok := windows[old]
	return w, ok
}

// retiredAgents are the agent blocks of a Hub from before the one agent.
var retiredAgents = []string{"ORCHESTRATOR", "PLANNER", "REVIEWER"}

// retired reports whether w still runs a retired agent. openHub closes such a
// window and opens the tab again, because it adds no block to a window that
// exists.
func retired(w hubWindow) bool {
	return slices.ContainsFunc(w.Blocks, func(b string) bool { return slices.Contains(retiredAgents, b) })
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

// prepareAgent writes the agent's folder and trusts it for Claude. An agent
// with no role works in assist_dir and writes nothing there. openHub calls it
// on every run, so a new lineup binary or prompt reaches the files at once.
// Claude reads CLAUDE.md and the settings when a session starts, so a running
// agent picks them up at its next start.
func prepareAgent(t agentTab) (string, error) {
	if t.role == nil {
		cfg, err := config.Load(configPath())
		if err != nil {
			return "", err
		}
		dir := cfg.AssistDir()
		return dir, claude.Trust(os.Getenv("CLAUDE_CONFIG_DIR"), dir)
	}
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
	if err := writeReplacing(filepath.Join(dir, "CLAUDE.md"), role); err != nil {
		return "", err
	}
	if err := writeReplacing(filepath.Join(dir, ".claude", "settings.json"), []byte(settings)); err != nil {
		return "", err
	}
	return dir, claude.Trust(os.Getenv("CLAUDE_CONFIG_DIR"), dir)
}

// writeReplacing swaps the file in by rename. Claude Code watches its
// settings file, and a write in place would show it an empty file.
func writeReplacing(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// trackersLine is what lineup trackers prints. The Orchestrator's hook runs it
// on each prompt, as Claude Code reads CLAUDE.md only when the session starts,
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

// composeAgent adds the Tracker sections, and fills in the Local Tracker's
// folder and the path of lineup.
func composeAgent(role []byte, settings string) ([]byte, string, error) {
	cfg, err := config.Load(configPath())
	if err != nil {
		return nil, "", err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	return agentFiles(role, settings, cfg.LocalIssuesDir(), self)
}

func agentFiles(role []byte, settings, issuesDir, self string) ([]byte, string, error) {
	out := slices.Concat(role, linearSection, githubSection, localSection)
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
	Blocks []string // block labels
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
					Label string `json:"label"`
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
				hw.Blocks = append(hw.Blocks, b.Label)
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

	for _, label := range retiredTabs {
		if w, ok := windows[label]; ok {
			if _, err := rexRun("window", "close", "-s", hubSession, w.ID); err != nil {
				return err
			}
			delete(windows, label)
		}
	}
	for i, t := range agentTabs {
		// The files are written on every run, as the agent reads them only
		// when it starts.
		var dir string
		if t.role != nil {
			if dir, err = prepareAgent(t); err != nil {
				return err
			}
		}
		if w, ok := renamed(windows, t.window); ok {
			if _, err := rexRun("window", "rename", "-s", hubSession, w.ID, t.window); err != nil {
				return err
			}
			windows[t.window] = w
		}
		if w, ok := windows[t.window]; ok && retired(w) {
			if _, err := rexRun("window", "close", "-s", hubSession, w.ID); err != nil {
				return err
			}
			delete(windows, t.window)
		}
		if _, ok := windows[t.window]; !ok {
			if err := openAgentTab(t, dir, self, claudeBin, t.window == activeTab); err != nil {
				return err
			}
		}
		// Each run moves every tab to its place, so a Hub from before a new
		// tab order takes the new order too.
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

// openAgentTab opens the tab. dir is the agent's folder when openHub has
// prepared it, and empty for an agent with no role.
func openAgentTab(t agentTab, dir, self, claudeBin string, focus bool) error {
	var cmd []string
	if t.agent != "" {
		if dir == "" {
			var err error
			if dir, err = prepareAgent(t); err != nil {
				return err
			}
		}
		cmd = agentArgv(os.Getenv("CLAUDE_CONFIG_DIR"), claudeBin, "lineup-"+t.window)
		// The assist folder holds the user's own conversations, so the assist
		// agent never continues one.
		if t.role != nil && hasConversation(os.Getenv("CLAUDE_CONFIG_DIR"), dir) {
			cmd = append(cmd, "--continue")
		}
		if t.prompt != "" {
			cmd = append(cmd, t.prompt)
		}
	}
	newWindow := []string{"window", "new", "-s", hubSession, t.window, "--focus=" + strconv.FormatBool(focus), "--json"}
	var agent string
	if t.boardArgs == nil {
		out, err := rexRun(slices.Concat(newWindow, []string{"-c", dir, "--"}, cmd)...)
		if err != nil {
			return err
		}
		if agent, err = firstBlock(out); err != nil {
			return err
		}
	} else {
		out, err := rexRun(slices.Concat(newWindow, []string{"--", self}, t.boardArgs)...)
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
		if t.agent == "" {
			return nil
		}
		split, err := rexRun(slices.Concat([]string{"split", "-s", hubSession, "-b", top, "--split", "below",
			"--ratio", fmt.Sprint(agentRows), "--focus=false", "--json", "-c", dir, "--"}, cmd)...)
		if err != nil {
			return err
		}
		if agent, err = firstBlock(split); err != nil {
			return err
		}
	}
	if _, err := rexRun("block", "rename", "-s", hubSession, agent, t.agent); err != nil {
		return err
	}
	theme, _ := json.Marshal(map[string]string{"background": t.bg, "scheme": "dark"})
	_, err := rexRun("block", "call", "-s", hubSession, "-b", agent, "set_theme", string(theme))
	return err
}

// agentArgv runs Claude on the profile of the shell that opened the Hub. An
// empty CLAUDE_CONFIG_DIR is not the default profile: Claude reads it as the
// working folder, so the default profile unsets the variable.
func agentArgv(configDir, claudeBin, name string) []string {
	env := []string{"env", "CLAUDE_CONFIG_DIR=" + configDir}
	if configDir == "" {
		env = []string{"env", "-u", "CLAUDE_CONFIG_DIR"}
	}
	return slices.Concat(env, []string{claudeBin}, claude.SessionFlags(name))
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
	if len(args) > 0 {
		return fmt.Errorf("usage: lineup board")
	}
	opts := board.Options{Zoom: func() { rexRun("zoom", "-s", hubSession, "LINEUP") }}
	opts.Reviews = reviewStores(stateDir())
	if g, err := config.Load(configPath()); err == nil { // a bad file only means defaults here
		opts.StartSkills = g.StartSkills()
		opts.Tracker.Repos = g.Checkouts
		opts.Tracker.Sources = boardSources(g, stateDir())
		opts.Reviews.Views = reviewViews(g)
	}
	opts.Notify = func(text string) { board.Toast(os.Stdout, text) }
	return board.Run(socketPath(), filepath.Join(stateDir(), "board.json"), opts)
}
