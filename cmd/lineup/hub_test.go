package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/scheduler"
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
	if got := strings.Join(agentArgv("", "/bin/claude", "lineup-plan"), " "); got != "env -u CLAUDE_CONFIG_DIR /bin/claude --name lineup-plan" {
		t.Errorf("default profile: %s", got)
	}
	if got := strings.Join(agentArgv("/p/work", "/bin/claude", "lineup-work"), " "); got != "env CLAUDE_CONFIG_DIR=/p/work /bin/claude --name lineup-work" {
		t.Errorf("named profile: %s", got)
	}
}

func TestTheAgentLearnsWhichTrackersAreOnAtEachPrompt(t *testing.T) {
	role, settings, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/.config/lineup/config.toml", "/Users/g/bin/lineup", nil, nil)
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
	if !slices.Contains(v.Permissions.Allow, "Edit(//Users/g/issues/**)") || !slices.Equal(v.Permissions.Dirs, []string{"/Users/g/issues", "/Users/g/.config/lineup"}) {
		t.Errorf("the agent cannot write local Issues: %+v", v.Permissions)
	}
	if !slices.Contains(v.Permissions.Allow, "Edit(//Users/g/.config/lineup/config.toml)") || slices.Contains(v.Permissions.Allow, "Edit(//Users/g/.config/lineup/**)") {
		t.Errorf("the agent cannot write the config file alone: %+v", v.Permissions)
	}
	if !strings.Contains(string(role), "`/Users/g/.config/lineup/config.toml`") || !strings.Contains(string(role), "`lineup restart`") {
		t.Errorf("the role does not say how to change the review queues:\n%s", role)
	}
	if h := v.Hooks["UserPromptSubmit"]; len(h) != 1 || len(h[0].Hooks) != 2 || h[0].Hooks[0].Command != "'/Users/g/bin/lineup' trackers" || h[0].Hooks[1].Command != "'/Users/g/bin/lineup' ready" {
		t.Errorf("no prompt hook runs lineup trackers and lineup ready: %+v", v.Hooks)
	}
	if got := trackersLine(trackersOn{linear: true, local: true}); !strings.HasPrefix(got, "Trackers on now: Linear, Local.") {
		t.Errorf("trackersLine = %q", got)
	}
}

func TestAgentRoleLeavesNeedsYouToThePalette(t *testing.T) {
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/.config/lineup/config.toml", "/Users/g/bin/lineup", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	role := string(raw)
	_, questions, ok := strings.Cut(role, "\n## Questions\n")
	if !ok {
		t.Fatalf("the role has no Questions section:\n%s", role)
	}
	questions, _, _ = strings.Cut(questions, "\n## ")
	for _, want := range []string{"`AskUserQuestion`", "`wip cap full`", "`--over-cap`", "`no Repo mapping`", "`pick a Repo`"} {
		if !strings.Contains(questions, want) {
			t.Errorf("the Questions section has no %s", want)
		}
	}
	_, needs, ok := strings.Cut(role, "\n## Needs you\n")
	if !ok {
		t.Fatalf("the role has no Needs you section:\n%s", role)
	}
	needs, _, _ = strings.Cut(needs, "\n## ")
	for _, want := range []string{"What Needs Me…", "`lineup decide`", "`lineup decide <task> <choice>`", "Ready to land: ENG-571, PR #4636", "These lines are your only alert."} {
		if !strings.Contains(needs, want) {
			t.Errorf("the Needs you section has no %s", want)
		}
	}
	for _, plain := range []string{"in chat", "Do not act on an alert yourself", "lineup watch", "## Alerts"} {
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
	if !slices.Equal(tabs, []string{"lineup"}) || !slices.Equal(agents, []string{"lineup"}) {
		t.Errorf("tabs %v with agents %v, want lineup with an agent", tabs, agents)
	}
	i := slices.IndexFunc(agentTabs, func(a agentTab) bool { return a.window == "lineup" })
	if got := agentTabs[i]; got.agent != "LINEUP" || !slices.Equal(got.boardArgs, []string{"board"}) {
		t.Errorf("the lineup tab runs %v over %q, want board over LINEUP", got.boardArgs, got.agent)
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
		"lineup add ENG-1", "lineup add --over-cap ENG-1", "lineup merge ENG-1",
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

func TestBoardOptionsCarryTheConfigProblem(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, body, want string }{
		{"good", "start_skills = [\"a\"]\n", ""},
		{"bad board", "[board]\nhidden = [\"dun\"]\n", `config: board.hidden: unknown section "dun" (default layout)`},
		{"old board key", "[board]\norder = [\"queued\", \"done\"]\n", `config: board.order: section "queued" is now up_next. Run lineup config migrate to rewrite it (default layout)`},
		{"bad file", "[github.labels]\nnope = \"x\"\n", "config: "},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name+".toml")
		if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		got := boardOptions(path).ConfigErr
		if (c.want == "") != (got == "") || !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: ConfigErr %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBoardOptionsReadTheGitHubTracker(t *testing.T) {
	state := t.TempDir()
	t.Setenv("LINEUP_STATE_DIR", state)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[github.labels]\nbacklog = \"later\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := boardOptions(path).GitHub
	if gh.Backlog != "later" {
		t.Errorf("backlog %q", gh.Backlog)
	}
	if _, on, err := gh.Login(); on || err != nil {
		t.Fatalf("on %v err %v before the login", on, err)
	}
	if err := github.SaveTracker(state, "octo"); err != nil {
		t.Fatal(err)
	}
	if login, on, err := gh.Login(); !on || login != "octo" || err != nil {
		t.Fatalf("login %q on %v err %v", login, on, err)
	}
}

func TestOpenHubRenamesTheHubTab(t *testing.T) {
	old := hubWindow{ID: "w7", Label: "hub"}
	cases := []struct {
		name    string
		windows map[string]hubWindow
		label   string
		want    string // the ID of the window to rename, or ""
	}{
		{"an old Hub", map[string]hubWindow{"hub": old, "plan": {ID: "w8"}}, "lineup", "w7"},
		{"a Hub with both", map[string]hubWindow{"hub": old, "lineup": {ID: "w9"}}, "lineup", ""},
		{"a new Hub", map[string]hubWindow{}, "lineup", ""},
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
	if activeTab != "lineup" {
		t.Errorf("a new Hub opens on %q, want lineup", activeTab)
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

func TestOpenHubReopensATabThatRexRestoredAsShells(t *testing.T) {
	w := hubWindow{Blocks: []string{"BOARD", "LINEUP", "shell"}, BlockIDs: []string{"block:board", "block:agent", "block:mine"}}
	for _, c := range []struct {
		name string
		idle map[string]bool
		err  error
		want bool
	}{
		{"both panes restored", map[string]bool{"block:board": true, "block:agent": true}, nil, true},
		{"the Board restored", map[string]bool{"block:board": true}, nil, true},
		{"both panes live", map[string]bool{}, nil, false},
		{"only the user's own shell idle", map[string]bool{"block:mine": true}, nil, false},
		{"Rex cannot read the panes", map[string]bool{"block:board": true}, errors.New("rex: cannot connect"), false},
	} {
		idle := func(b string) (bool, error) { return c.idle[b], c.err }
		if got := restored(w, agentTabs[0], idle); got != c.want {
			t.Errorf("%s: restored = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOpenHubRewritesTheAgentFilesWhenTheTabExists(t *testing.T) {
	bin := t.TempDir()
	for name, body := range map[string]string{
		"rex": `case "$1 $2" in
"session inspect") echo '{"windows":[
{"window_id":"w2","label":"lineup","layers":[{"blocks":[{"label":"BOARD"},{"label":"LINEUP"}]}]}]}';;
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

// A Hub from before the rename keeps its agent and its conversation: lineup
// renames the session and the tab in place and opens no second Hub.
func TestOpenHubRenamesTheOldHubInPlace(t *testing.T) {
	bin, tmp := t.TempDir(), t.TempDir()
	log, renamedMark := filepath.Join(tmp, "rex.log"), filepath.Join(tmp, "renamed")
	for name, body := range map[string]string{
		"rex": `echo "$@" >> '` + log + `'
case "$1 $2" in
"session inspect")
  if [ "$4" = "lineup hub" ] || [ -f '` + renamedMark + `' ]; then
    echo '{"windows":[{"window_id":"w2","label":"hub","layers":[{"blocks":[{"label":"BOARD"},{"label":"LINEUP"}]}]}]}'
  else
    echo "session $4 not found" >&2; exit 1
  fi;;
"session rename") touch '` + renamedMark + `';;
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

	if err := openHub(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	calls := string(raw)
	for _, want := range []string{"session rename lineup hub my-lineup", "window rename -s my-lineup w2 lineup"} {
		if !strings.Contains(calls, want) {
			t.Errorf("rex calls lack %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "session new") || strings.Contains(calls, "window new") {
		t.Errorf("lineup opened a second Hub:\n%s", calls)
	}
}

// fakeHubRex is a rex whose Hub has one lineup tab. Rex restored its BOARD
// pane as an idle shell, and its LINEUP pane runs a program. It logs each call.
func fakeHubRex(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "rex.log")
	idle := `{"child":{"name":"login","path":"/usr/bin/login"},"foreground":{"name":"zsh","path":"/bin/zsh"},"ancestors":[{"name":"login","path":"/usr/bin/login"}]}`
	live := `{"child":{"name":"claude","path":"/bin/claude"},"foreground":{"name":"claude","path":"/bin/claude"}}`
	for name, body := range map[string]string{
		"rex": `echo "$@" >> '` + log + `'
case "$1 $2" in
"session inspect") echo '{"windows":[{"window_id":"w2","label":"lineup","layers":[{"blocks":[{"block_id":"b1","label":"BOARD"},{"block_id":"b2","label":"LINEUP"}]}]}]}';;
"block call") if [ "$7" = process ] && [ "$6" = b1 ]; then echo '` + idle + `'; elif [ "$7" = process ]; then echo '` + live + `'; fi;;
"window new") echo '{"window_id":"w9","block_ids":["b9"]}';;
"split -s") echo '{"block_ids":["b10"]}';;
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
	return log
}

func TestOpenHubReopensATabThatARexRestartLeftAsAShell(t *testing.T) {
	log := fakeHubRex(t)
	t.Setenv("REX_BLOCK", "block:elsewhere")
	if err := openHub(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	calls := string(raw)
	closed, opened := strings.Index(calls, "window close -s my-lineup w2"), strings.Index(calls, "window new -s my-lineup lineup")
	if closed < 0 || opened < closed {
		t.Fatalf("lineup did not close the restored tab, then open it again:\n%s", calls)
	}
}

func TestOpenHubRefusesToCloseTheTabThatItRunsIn(t *testing.T) {
	log := fakeHubRex(t)
	t.Setenv("REX_BLOCK", "b1")
	err := openHub()
	if err == nil || !strings.Contains(err.Error(), "run lineup in another tab") {
		t.Fatalf("openHub = %v, want an error that names another tab", err)
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), "window close") || strings.Contains(string(raw), "window new") {
		t.Fatalf("lineup closed or opened a tab:\n%s", raw)
	}
}

// trackerConfig writes a config with three checkouts: data takes Linear
// Issues from its owner's table, hod takes local Issues, and web takes none.
func trackerConfig(t *testing.T) (path, hod, data string) {
	t.Helper()
	root := t.TempDir()
	hod = gitCheckout(t, filepath.Join(root, "hod"), "git@github.com:GClunies/hod.git")
	data = gitCheckout(t, filepath.Join(root, "data"), "git@github.com:crexi/data.git")
	gitCheckout(t, filepath.Join(root, "web"), "git@github.com:acme/web.git")
	path = filepath.Join(root, "config.toml")
	body := "roots = [\"" + root + "\"]\n\n[repos.\"crexi/*\"]\ntracker = \"linear\"\n\n[repos.\"GClunies/hod\"]\ntracker = \"local\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, hod, data
}

func TestTheRepoPickerOffersARepoOnlyForItsTracker(t *testing.T) {
	path, hod, data := trackerConfig(t)
	opts := boardOptions(path)
	if opts.ConfigErr != "" {
		t.Fatal(opts.ConfigErr)
	}
	for kind, want := range map[string][]string{
		scheduler.KindLinear: {data},
		scheduler.KindGitHub: nil,
		scheduler.KindLocal:  {hod},
	} {
		if got := opts.Repos(kind); !slices.Equal(got, want) {
			t.Errorf("Repos(%s) = %q, want %q", kind, got, want)
		}
	}
}

func TestTheRoleNamesEachRepoTableAndItsTracker(t *testing.T) {
	path, _, _ := trackerConfig(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", cfg.TrackedRepos(), cfg.PrefixedRepos())
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(raw), "\n## Trackers\n")
	if !ok {
		t.Fatalf("the role has no Trackers section:\n%s", raw)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, want := range []string{
		"| `GClunies/hod` | `local` |", "| `crexi/*` | `linear` |",
		"`tracker` key of its `[repos.\"<owner>/<repo>\"]` table", "The Repo's own table wins over the owner's table.",
		"Plan the Issues of a Repo only in its Tracker", "A Repo with no `tracker` takes no Issues. It takes only ad hoc Tasks", path,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Trackers section has no %q:\n%s", want, section)
		}
	}
	_, questions, _ := strings.Cut(string(raw), "\n## Questions\n")
	if !strings.Contains(questions, "Leave out each Repo whose Tracker in the Trackers section is not the Issue's Tracker") {
		t.Errorf("the no Repo mapping row offers Repos of other Trackers:\n%s", questions)
	}

	none, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(none), "No table sets a tracker, so no Repo takes Issues.") {
		t.Errorf("a config with no tracker gives no plain Trackers section")
	}
}

func TestTheRoleNamesEachRepoPrefixAndItsFileRule(t *testing.T) {
	path, _, _ := trackerConfig(t)
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(body, "prefix = \"Hod\"\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", cfg.TrackedRepos(), cfg.PrefixedRepos())
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(raw), "\n## Repo prefixes\n")
	if !ok {
		t.Fatalf("the role has no Repo prefixes section:\n%s", raw)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, want := range []string{"| `GClunies/hod` | `HOD` | `HOD-<n>` | `/Users/g/issues/hod-<n>.md` |", "Name a new Local Issue file for a Repo with a prefix `<prefix>-<n>.md`, in lower case", "A `<PREFIX>-<n>` ID is a Local Issue ID", "`prefix` key of its `[repos.\"<owner>/<repo>\"]` table", path} {
		if !strings.Contains(section, want) {
			t.Errorf("the Repo prefixes section has no %q:\n%s", want, section)
		}
	}
	_, local, _ := strings.Cut(string(raw), "\n## Local\n")
	if !strings.Contains(local, "run `lineup next-id <repo>`") {
		t.Errorf("the Local section does not name lineup next-id:\n%s", local)
	}
	open, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(open), "No Repo has a prefix.") {
		t.Errorf("a config with no prefix gives no plain Repo prefixes section")
	}
}

func TestConfigMigrateRewritesTheConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("LINEUP_CONFIG", path)
	if err := os.WriteFile(path, []byte("[budgets]\nci_debug = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cmdConfig([]string{"migrate"}, &out); err != nil {
		t.Fatal(err)
	}
	if want := "Rewrote " + path + ". The old file is " + path + ".bak."; !strings.HasPrefix(out.String(), want) {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if got, _ := os.ReadFile(path); string(got) != "[defaults.budgets]\nci_debug = 2\n" {
		t.Errorf("config file:\n%s", got)
	}
	if err := cmdConfig(nil, &out); !errors.Is(err, errUsage) {
		t.Errorf("lineup config with no sub-command: %v, want a usage error", err)
	}
}
