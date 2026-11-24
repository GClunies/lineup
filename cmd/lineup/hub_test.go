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
	"time"

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
	role, settings, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/.config/lineup/config.toml", "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", nil, nil)
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
	if !strings.Contains(string(role), "`/Users/g/.config/lineup/config.toml`") || !strings.Contains(string(role), "`lineup restart scheduler`") {
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
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/.config/lineup/config.toml", "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", nil, nil)
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

func TestAgentRoleAsksForAPrefixInTheRepoSetup(t *testing.T) {
	_, setup, ok := strings.Cut(string(agentRole), "\n## Repo setup\n")
	if !ok {
		t.Fatal("the role has no Repo setup section")
	}
	setup, _, _ = strings.Cut(setup, "\n## ")
	for _, want := range []string{
		"Ask the Tracker, Review bot, Landing bar, and Prefix questions below in one `AskUserQuestion` call.",
		"| Prefix | `prefix` in upper case",
		"Then **No prefix**",
		"| `prefix=<p>`. A typed answer passes `prefix=<the answer>`. **No prefix** passes nothing. |",
		"If it refuses the prefix, show the user its error and ask the Prefix question again.",
	} {
		if !strings.Contains(setup, want) {
			t.Errorf("the Repo setup section has no %q", want)
		}
	}
}

// section is the text of the role's section under heading, up to the next
// heading of the same level or higher.
func section(t *testing.T, role, heading string) string {
	t.Helper()
	_, s, ok := strings.Cut(role, "\n"+heading+"\n")
	if !ok {
		t.Fatalf("the role has no %s section:\n%s", heading, role)
	}
	level := heading[:strings.Index(heading, " ")+1]
	for _, end := range []string{"\n## ", "\n" + level} {
		s, _, _ = strings.Cut(s, end)
	}
	return s
}

func TestTheOrchestratorSearchesForLinksBeforeItWritesAnIssue(t *testing.T) {
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", "/Users/g/.config/lineup/config.toml", "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	role := string(raw)
	if plans := section(t, role, "## Plans"); !strings.Contains(plans, "Before you create or change an Issue, find its links and show them to the user") {
		t.Errorf("the Plans section does not send the agent to the links search:\n%s", plans)
	}
	links := section(t, role, "### Links")
	for _, want := range []string{
		// The search space (PROD-ORCH-013).
		"GitHub Issues that are open", "Linear Issues that are not Done or Canceled",
		"local Issues with status `backlog`, `todo`, `started`, or `in review`",
		"`lineup ls --json` that are not Done, also ad hoc Tasks with no Issue",
		"`gh pr list --repo <owner>/<repo> --state open`, also PRs that no Issue tracks",
		"Search another Repo only when the Issue links to it", "whose API, topic, or table the Issue uses",
		"the same Linear Project or milestone", "the other Issues that the same request creates",
		"Leave out closed Issues, Done Tasks, and merged PRs",
		// The evidence of the files.
		"`gh pr diff <n> --repo <owner>/<repo> --name-only`", "`files` in `lineup show --json <task>`",
		// The table and the approval (PROD-ORCH-015).
		"| Candidate | Kind | Evidence | Record |", "with the count of items that you read there, also a count of 0",
		"`AskUserQuestion`", "Write nothing before the user picks",
	} {
		if !strings.Contains(links, want) {
			t.Errorf("the Links section has no %q", want)
		}
	}
	// The link kinds, each a row with its record (PROD-ORCH-014).
	for kind, record := range map[string]string{
		"Duplicate":      "No new Issue. Offer to edit the other Issue.",
		"Needs":          "`Blocked by` on the Issue that uses it, or `blocks` on the other",
		"Reverse":        "`Blocked by <new Issue>` on the open Issue",
		"Overlaps":       "`Blocked by` on the new Issue, with the overlap as the reason",
		"Same file only": "A `Related:` link, with no block",
	} {
		row := regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta(kind) + ` \|.*\| ` + regexp.QuoteMeta(record) + ` \|$`)
		if !row.MatchString(links) {
			t.Errorf("the Links section has no %s row that records %q", kind, record)
		}
	}
	// Each Tracker says how to record a block both ways and a related Issue (PROD-TRACK-016).
	for heading, wants := range map[string][]string{
		"## GitHub": {"`Blocked by #12`: #12 blocks this Issue", "add `Blocked by #<this Issue>` to the body of #12", "`Related: #12`"},
		"## Linear": {"linear issue relation add ENG-13 blocked-by ENG-12", "linear issue relation add ENG-12 blocks ENG-13", "linear issue relation add ENG-13 related ENG-12"},
		"## Local":  {"`blocked_by: [LOCAL-7]` in `local-8.md` records that LOCAL-7 blocks LOCAL-8", "add `LOCAL-8` to `blocked_by` in `local-9.md`", "`Related: LOCAL-7`"},
	} {
		s := section(t, role, heading)
		for _, want := range wants {
			if !strings.Contains(s, want) {
				t.Errorf("the %s section has no %q", heading, want)
			}
		}
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
	if !slices.Equal(tabs, []string{"my-lineup"}) || !slices.Equal(agents, []string{"my-lineup"}) {
		t.Errorf("tabs %v with agents %v, want my-lineup with an agent", tabs, agents)
	}
	i := slices.IndexFunc(agentTabs, func(a agentTab) bool { return a.window == "my-lineup" })
	if got := agentTabs[i]; got.agent != "LINEUP" || !slices.Equal(got.boardArgs, []string{"board"}) {
		t.Errorf("the my-lineup tab runs %v over %q, want board over LINEUP", got.boardArgs, got.agent)
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
		"gh pr list --repo o/r --state open", "gh pr diff 34 --repo o/r --name-only", "linear issue relation add ENG-13 related ENG-12",
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

func TestBoardOptionsListTheTeamKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[repos.\"crexi/data\"]\ntracker = \"linear\"\nteams = [\"PD\"]\n[repos.\"crexi/web\"]\ntracker = \"linear\"\nteams = [\"WEB\", \"PD\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := boardOptions(path).Teams; !slices.Equal(got, []string{"PD", "WEB"}) {
		t.Fatalf("Teams %q, want PD and WEB", got)
	}
}

func TestOpenHubRenamesTheHubTab(t *testing.T) {
	hub, lineup := hubWindow{ID: "w7", Label: "hub"}, hubWindow{ID: "w6", Label: "lineup"}
	cases := []struct {
		name    string
		windows map[string]hubWindow
		label   string
		want    string // the ID of the window to rename, or ""
	}{
		{"a Hub with a lineup tab", map[string]hubWindow{"lineup": lineup}, "my-lineup", "w6"},
		{"a Hub with a hub tab", map[string]hubWindow{"hub": hub, "plan": {ID: "w8"}}, "my-lineup", "w7"},
		{"a Hub with both old tabs", map[string]hubWindow{"hub": hub, "lineup": lineup}, "my-lineup", "w6"},
		{"a Hub with the tab", map[string]hubWindow{"lineup": lineup, "my-lineup": {ID: "w9"}}, "my-lineup", ""},
		{"a new Hub", map[string]hubWindow{}, "my-lineup", ""},
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
	if activeTab != "my-lineup" {
		t.Errorf("a new Hub opens on %q, want my-lineup", activeTab)
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
{"window_id":"w2","label":"my-lineup","layers":[{"blocks":[{"label":"BOARD"},{"label":"LINEUP"}]}]}]}';;
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

	if err := openHub(hubRepair); err != nil {
		t.Fatal(err)
	}
	role, want, err := composeAgent(dir, hub.role, hub.settings)
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

// A Hub from before a rename keeps its agent and its conversation: lineup
// renames the session and the tab in place and opens no second Hub.
func TestOpenHubRenamesTheOldHubInPlace(t *testing.T) {
	for _, c := range []struct {
		name, session, tab string
		want               []string // rex calls that the rename makes
	}{
		{"a lineup tab", "my-lineup", "lineup", []string{"window rename -s my-lineup w2 my-lineup"}},
		{"the lineup hub session", "lineup hub", "hub", []string{"session rename lineup hub my-lineup", "window rename -s my-lineup w2 my-lineup"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, tmp := t.TempDir(), t.TempDir()
			log, renamedMark := filepath.Join(tmp, "rex.log"), filepath.Join(tmp, "renamed")
			for name, body := range map[string]string{
				"rex": `echo "$@" >> '` + log + `'
case "$1 $2" in
"session inspect")
  if [ "$4" = '` + c.session + `' ] || [ -f '` + renamedMark + `' ]; then
    echo '{"windows":[{"window_id":"w2","label":"` + c.tab + `","layers":[{"blocks":[{"label":"BOARD"},{"label":"LINEUP"}]}]}]}'
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

			if err := openHub(hubRepair); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(log)
			calls := string(raw)
			for _, want := range c.want {
				if !strings.Contains(calls, want) {
					t.Errorf("rex calls lack %q:\n%s", want, calls)
				}
			}
			if strings.Contains(calls, "session new") || strings.Contains(calls, "window new") || strings.Contains(calls, "window close") {
				t.Errorf("lineup rebuilt the Hub:\n%s", calls)
			}
		})
	}
}

// The blocks of the Hub's my-lineup tab in fakeHubRex.
const (
	restoredBoard = `[{"block_id":"b1","label":"BOARD"},{"block_id":"b2","label":"LINEUP"}]`
	livePanes     = `[{"block_id":"b3","label":"BOARD"},{"block_id":"b2","label":"LINEUP"}]`
)

// fakeHubRex is a rex whose Hub has one my-lineup tab, w2, with blocks, a JSON
// list. Block b1 holds an idle shell, as a pane does after a Rex restart, and
// each other block runs a program. An empty blocks means no Hub session. The
// fake logs each call. lineup runs outside the Hub, and a detach fails the test.
func fakeHubRex(t *testing.T, blocks string) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "rex.log")
	idle := `{"child":{"name":"login","path":"/usr/bin/login"},"foreground":{"name":"zsh","path":"/bin/zsh"},"ancestors":[{"name":"login","path":"/usr/bin/login"}]}`
	live := `{"child":{"name":"claude","path":"/bin/claude"},"foreground":{"name":"claude","path":"/bin/claude"}}`
	inspect := `echo '{"windows":[{"window_id":"w2","label":"my-lineup","layers":[{"blocks":` + blocks + `}]}]}'`
	if blocks == "" {
		inspect = `echo "session $4 not found" >&2; exit 1`
	}
	for name, body := range map[string]string{
		"rex": `echo "$@" >> '` + log + `'
case "$1 $2" in
"session inspect") ` + inspect + `;;
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
	t.Setenv("REX_BLOCK", "block:elsewhere")
	detachHub = func(string, []string, string) error {
		t.Error("lineup started a detached copy")
		return nil
	}
	t.Cleanup(func() { detachHub = startDetached })
	return log
}

// claudeSessions is the folder where Claude keeps the Orchestrator's
// sessions: its agent folder, with each / and . as -, in the projects folder
// of the profile.
func claudeSessions() string {
	agent := filepath.Join(stateDir(), "lineup")
	return filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", strings.NewReplacer("/", "-", ".", "-").Replace(agent))
}

// rexCalls reads the fake rex's log.
func rexCalls(t *testing.T, log string) string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(raw)
}

// reopened reports whether lineup closed the tab w2 and then opened it again.
func reopened(calls string) bool {
	closed, opened := strings.Index(calls, "window close -s my-lineup w2"), strings.Index(calls, "window new -s my-lineup my-lineup")
	return closed >= 0 && opened > closed
}

func TestOnlyARepairContinuesTheOrchestratorsConversation(t *testing.T) {
	for _, c := range []struct {
		name         string
		mode         hubMode
		blocks       string
		wantContinue bool
	}{
		{"lineup restart on a live Hub", hubFresh, livePanes, false},
		{"lineup on a Hub that Rex restored", hubRepair, restoredBoard, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeHubRex(t, c.blocks)
			os.MkdirAll(claudeSessions(), 0o700)
			os.WriteFile(filepath.Join(claudeSessions(), "s1.jsonl"), []byte("{}\n"), 0o600)
			if err := openHub(c.mode); err != nil {
				t.Fatal(err)
			}
			calls := rexCalls(t, log)
			if !reopened(calls) {
				t.Fatalf("lineup did not close the tab, then open it again:\n%s", calls)
			}
			split := calls[strings.Index(calls, "split -s"):]
			split, _, _ = strings.Cut(split, "\n")
			if got := strings.HasSuffix(split, " --continue"); got != c.wantContinue {
				t.Errorf("the agent's command continues the conversation: %v, want %v:\n%s", got, c.wantContinue, split)
			}
		})
	}
}

func TestLineupRebuildsAHubTabWithAMissingOrIdlePane(t *testing.T) {
	for _, c := range []struct {
		name, blocks string
		want         bool
	}{
		{"the Board restored as a shell", restoredBoard, true},
		{"only the agent pane", `[{"block_id":"b2","label":"LINEUP"}]`, true},
		{"only the Board pane", `[{"block_id":"b3","label":"BOARD"}]`, true},
		{"an agent from before the one agent", `[{"block_id":"b3","label":"BOARD"},{"block_id":"b4","label":"ORCHESTRATOR"}]`, true},
		{"both panes live", livePanes, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeHubRex(t, c.blocks)
			if err := openHub(hubRepair); err != nil {
				t.Fatal(err)
			}
			calls := rexCalls(t, log)
			if got := reopened(calls); got != c.want {
				t.Errorf("reopened %v, want %v:\n%s", got, c.want, calls)
			}
			if !c.want && (strings.Contains(calls, "window close") || strings.Contains(calls, "window new")) {
				t.Errorf("lineup closed or opened a tab:\n%s", calls)
			}
		})
	}
}

func TestLineupReopensTheTabThatItRunsInFromADetachedCopy(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, blocks, block string
		mode                hubMode
		want                []string
	}{
		{"lineup in the restored tab", restoredBoard, "b1", hubRepair, []string{"hub"}},
		{"lineup restart in the Orchestrator's pane", livePanes, "b2", hubFresh, []string{"hub", "--fresh"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeHubRex(t, c.blocks)
			t.Setenv("REX_BLOCK", c.block)
			var bin, hubLog string
			var args []string
			detachHub = func(b string, a []string, l string) error {
				bin, args, hubLog = b, a, l
				return nil
			}
			if err := openHub(c.mode); err != nil {
				t.Fatal(err)
			}
			if calls := rexCalls(t, log); strings.Contains(calls, "window close") || strings.Contains(calls, "window new") {
				t.Fatalf("lineup closed or opened a tab itself:\n%s", calls)
			}
			if bin != self || !slices.Equal(args, c.want) || hubLog != filepath.Join(stateDir(), "hub.log") {
				t.Errorf("detached %s %q with the log %s, want %s %q with the log hub.log in the state folder", bin, args, hubLog, self, c.want)
			}
		})
	}
}

func TestTheDetachedCopyRunsInItsOwnSessionWithoutTheCallersBlock(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report")
	self := filepath.Join(dir, "lineup")
	script := "#!/bin/sh\n{ echo \"pid $$\"; echo \"pgid $(ps -o pgid= -p $$)\"; env; } > '" + report + ".tmp'\nmv '" + report + ".tmp' '" + report + "'\necho \"ran $*\"\n"
	if err := os.WriteFile(self, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BLOCK", "b2")
	t.Setenv("LINEUP_STATE_DIR", dir)
	log := filepath.Join(dir, "state", "hub.log")
	if err := startDetached(self, []string{"hub", "--fresh"}, log); err != nil {
		t.Fatal(err)
	}
	var out []byte
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(string(out), "ran hub --fresh\n"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the copy wrote no line to its log: %q", out)
		}
		out, _ = os.ReadFile(log)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var pid, pgid string
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "pid "); ok {
			pid = v
		}
		if v, ok := strings.CutPrefix(line, "pgid "); ok {
			pgid = strings.TrimSpace(v)
		}
		if strings.HasPrefix(line, "REX_BLOCK=") {
			t.Errorf("the copy got %s", line)
		}
	}
	if pid == "" || pgid != pid {
		t.Errorf("the copy runs as pid %q in the process group %q, want its own group", pid, pgid)
	}
	if !strings.Contains(string(raw), "\nLINEUP_STATE_DIR="+dir+"\n") {
		t.Errorf("the copy did not get the caller's environment:\n%s", raw)
	}
}

func TestLineupRestartOpensNoHubWhenNoneIsOpen(t *testing.T) {
	log := fakeHubRex(t, "")
	if err := openHub(hubFresh); err != nil {
		t.Fatal(err)
	}
	if calls := rexCalls(t, log); strings.Contains(calls, "session new") || strings.Contains(calls, "window new") {
		t.Errorf("lineup restart opened a Hub:\n%s", calls)
	}
}

func TestTheOrchestratorCanReadItsEarlierSessions(t *testing.T) {
	fakeHubRex(t, livePanes)
	if err := openHub(hubRepair); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(stateDir(), "lineup")
	role, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	_, section, ok := strings.Cut(string(role), "\n## Earlier sessions\n")
	section, _, _ = strings.Cut(section, "\n## ")
	if !ok || !strings.Contains(section, "`"+claudeSessions()+"`") || !strings.Contains(section, "`lineup restart`") {
		t.Errorf("the role does not name the folder of the earlier sessions:\n%s", section)
	}
	settings, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	var v struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal(settings, &v); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(v.Permissions.Allow, "Read(/"+claudeSessions()+"/**)") {
		t.Errorf("the settings do not allow a read of the earlier sessions: %q", v.Permissions.Allow)
	}
	for _, rule := range v.Permissions.Allow {
		if strings.HasPrefix(rule, "Edit(") && strings.Contains(rule, "/projects/") {
			t.Errorf("the settings allow %s", rule)
		}
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
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", cfg.TrackedRepos(), cfg.PrefixedRepos())
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

	none, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", nil, nil)
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
	raw, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", cfg.TrackedRepos(), cfg.PrefixedRepos())
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
	open, _, err := agentFiles(agentRole, agentSettings, "/Users/g/issues", path, "/Users/g/bin/lineup", "/Users/g/.claude/projects/-Users-g-state-lineup", nil, nil)
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
	noPanel := func() error { t.Fatal("lineup config migrate opened the panel"); return nil }
	if err := cmdConfig([]string{"migrate"}, &out, noPanel); err != nil {
		t.Fatal(err)
	}
	if want := "Rewrote " + path + ". The old file is " + path + ".bak."; !strings.HasPrefix(out.String(), want) {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if got, _ := os.ReadFile(path); string(got) != "[defaults.budgets]\nci_debug = 2\n" {
		t.Errorf("config file:\n%s", got)
	}
	if err := cmdConfig([]string{"migrate", "now"}, &out, noPanel); !errors.Is(err, errUsage) {
		t.Errorf("lineup config migrate now: %v, want a usage error", err)
	}
}

// splitCall is the rex split call that opened the agent pane, or "".
func splitCall(calls string) string {
	for _, line := range strings.Split(calls, "\n") {
		if strings.HasPrefix(line, "split -s") {
			return line
		}
	}
	return ""
}

// The [layout] table sets the split of a new Hub tab and of a reopened one
// (PROD-TABS-018).
func TestTheLayoutSetsTheSplitOfTheHubTab(t *testing.T) {
	for _, l := range []struct{ name, config, want string }{
		{"no config file", "", "--split left --ratio 50"},
		{"left", "[layout]\nagent_side = \"left\"\nagent_size = 35\n", "--split left --ratio 35"},
		{"right", "[layout]\nagent_side = \"right\"\nagent_size = 25\n", "--split right --ratio 25"},
		{"top", "[layout]\nagent_side = \"top\"\nagent_size = 60\n", "--split above --ratio 60"},
		{"bottom", "[layout]\nagent_side = \"bottom\"\nagent_size = 90\n", "--split below --ratio 90"},
	} {
		for _, tab := range []struct {
			name, blocks string
			mode         hubMode
		}{
			{"a new Hub", "", hubRepair},
			{"lineup restart", livePanes, hubFresh},
			{"a tab that Rex restored", restoredBoard, hubRepair},
		} {
			t.Run(l.name+" in "+tab.name, func(t *testing.T) {
				log := fakeHubRex(t, tab.blocks)
				if l.config != "" {
					if err := os.WriteFile(os.Getenv("LINEUP_CONFIG"), []byte(l.config), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := openHub(tab.mode); err != nil {
					t.Fatal(err)
				}
				calls := rexCalls(t, log)
				want := "split -s my-lineup -b b9 " + l.want + " --focus=false "
				if got := splitCall(calls); !strings.HasPrefix(got, want) {
					t.Errorf("the agent pane opened with %q, want %q:\n%s", got, want, calls)
				}
			})
		}
	}
}

func TestABadLayoutStopsLineupBeforeItChangesTheHub(t *testing.T) {
	for _, c := range []struct{ name, blocks string }{
		{"no Hub", ""},
		{"a tab that Rex restored", restoredBoard},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeHubRex(t, c.blocks)
			if err := os.WriteFile(os.Getenv("LINEUP_CONFIG"), []byte("[layout]\nagent_side = \"middle\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := openHub(hubRepair); err == nil || !strings.Contains(err.Error(), "layout.agent_side") {
				t.Fatalf("openHub = %v, want an error that names layout.agent_side", err)
			}
			calls := rexCalls(t, log)
			for _, change := range []string{"session new", "window close", "window new", "split"} {
				if strings.Contains(calls, change) {
					t.Errorf("lineup ran rex %s on a bad layout:\n%s", change, calls)
				}
			}
		})
	}
}
