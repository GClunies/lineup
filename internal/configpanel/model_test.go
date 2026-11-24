package configpanel

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/testenv"
)

// The package imports config, which runs git.
func TestMain(m *testing.M) { testenv.Main(m) }

const fixture = `roots = ["~/repos"]

[defaults]
wip_cap = 7
[defaults.budgets]
ci_debug = 1

[repos."acme/*"]
poll_interval = "20s"
tracker = "linear"

# The bot gates only acme/api.
[repos."acme/api"]
wip_cap = 2
[repos."acme/api".review_bot]
name = "greptile"
`

type panel struct {
	m        Model
	path     string
	restarts [][]string
}

func newPanel(t *testing.T, text string) *panel {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := &panel{path: path}
	p.m = New(Options{File: f, Repos: []string{"acme/api", "acme/web"}, Restart: func(args []string) (string, error) {
		p.restarts = append(p.restarts, args)
		return "Restarted the Scheduler.\n", nil
	}})
	// The width fits the loader's errors, which name the file.
	p.m.width, p.m.height = 300, 60
	return p
}

// press sends keys, and runs each command as the tea runtime does.
func (p *panel) press(keys ...string) {
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		case "ctrl+u":
			msg = tea.KeyMsg{Type: tea.KeyCtrlU}
		}
		next, cmd := p.m.Update(msg)
		p.m = next.(Model)
		if cmd == nil {
			continue
		}
		if out, ok := cmd().(restartMsg); ok {
			next, _ = p.m.Update(out)
			p.m = next.(Model)
		}
	}
}

// to selects the row of the path with j and k.
func (p *panel) to(t *testing.T, path string) {
	t.Helper()
	i := slices.IndexFunc(p.m.rows, func(s config.Setting) bool { return s.Path == path })
	if i < 0 {
		t.Fatalf("no row %s on the tab", path)
	}
	for range len(p.m.rows) {
		switch {
		case p.m.sel < i:
			p.press("j")
		case p.m.sel > i:
			p.press("k")
		default:
			return
		}
	}
	t.Fatalf("j and k do not reach %s in mode %d", path, p.m.mode)
}

func (p *panel) view() string { return ansi.Strip(p.m.View()) }

func (p *panel) file(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// rowLine is the line of the view that shows the key.
func (p *panel) rowLine(t *testing.T, path string) string {
	t.Helper()
	for _, l := range strings.Split(p.view(), "\n") {
		if f := strings.Fields(strings.TrimPrefix(l, "▶")); len(f) > 0 && f[0] == path {
			return strings.Join(f, " ")
		}
	}
	t.Fatalf("no line for %s in\n%s", path, p.view())
	return ""
}

func TestTheGlobalTabShowsTheTopKeysAndTheDefaults(t *testing.T) {
	p := newPanel(t, fixture)
	v := p.view()
	for _, want := range []string{"[ Global ]", "Repo: acme/api ▾", "KEY", "VALUE", "SET BY", "enter edit · l set at level · x remove from level · / filter"} {
		if !strings.Contains(v, want) {
			t.Errorf("the view has no %q:\n%s", want, v)
		}
	}
	for path, want := range map[string]string{
		"roots":            `roots ["~/repos"] top level`,
		"wip_cap":          "wip_cap 7 [defaults]",
		"budgets.ci_debug": "budgets.ci_debug 1 [defaults]",
		"poll_interval":    "poll_interval 30s default",
	} {
		if got := p.rowLine(t, path); got != want {
			t.Errorf("row %q, want %q", got, want)
		}
	}
	p.to(t, "budgets.ci_debug")
	v = p.view()
	for _, want := range []string{"budgets.ci_debug: How many times the CI debug Stage runs", "Type int. Default 3.", "Applies after: lineup restart scheduler"} {
		if !strings.Contains(v, want) {
			t.Errorf("the detail has no %q:\n%s", want, v)
		}
	}
}

func TestTheRepoTabShowsTheLevelOfEachKey(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	if v := p.view(); !strings.Contains(v, "[ Repo: acme/api ▾ ]") || strings.Contains(v, "not set up") {
		t.Fatalf("the Repo tab:\n%s", v)
	}
	for path, want := range map[string]string{
		"wip_cap":            "wip_cap 2 acme/api",
		"poll_interval":      "poll_interval 20s acme/*",
		"budgets.ci_debug":   "budgets.ci_debug 1 [defaults]",
		"budgets.responding": "budgets.responding 3 default",
		"tracker":            "tracker linear acme/*",
	} {
		if got := p.rowLine(t, path); got != want {
			t.Errorf("row %q, want %q", got, want)
		}
	}
	p.press("r", "web", "enter")
	if v := p.view(); !strings.Contains(v, "[ Repo: acme/web ▾ ]") || !strings.Contains(v, "acme/web is not set up") {
		t.Fatalf("after the pick of acme/web:\n%s", v)
	}
	if got := p.rowLine(t, "wip_cap"); got != "wip_cap 7 [defaults]" {
		t.Errorf("acme/web row %q", got)
	}
}

func TestASaveWritesTheFileAndRunsTheRestartOnEnter(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "wip_cap")
	p.press("enter", "backspace", "4", "enter")
	if want := strings.Replace(fixture, "wip_cap = 2", "wip_cap = 4", 1); p.file(t) != want {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if v := p.view(); !strings.Contains(v, "Saved. Applies after: lineup restart scheduler.") || !strings.Contains(v, "enter runs it · esc skips") {
		t.Fatalf("no restart offer:\n%s", v)
	}
	if got := p.rowLine(t, "wip_cap"); got != "wip_cap 4 acme/api" {
		t.Errorf("after the save, row %q", got)
	}
	p.press("enter")
	if !slices.EqualFunc(p.restarts, [][]string{{"restart", "scheduler"}}, slices.Equal) {
		t.Fatalf("restarts %q", p.restarts)
	}
	if v := p.view(); !strings.Contains(v, "Ran lineup restart scheduler. Restarted the Scheduler.") {
		t.Fatalf("after the restart:\n%s", v)
	}
}

func TestEscSkipsTheRestartAndTheChangeWaits(t *testing.T) {
	p := newPanel(t, fixture)
	p.to(t, "roots")
	p.press("enter", "ctrl+u", "~/repos, ~/work", "enter", "esc")
	if !strings.Contains(p.file(t), `roots = ["~/repos", "~/work"]`) {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if len(p.restarts) != 0 {
		t.Fatalf("esc ran %q", p.restarts)
	}
	if v := p.view(); !strings.Contains(v, "The change waits for lineup restart.") {
		t.Fatalf("no line about the restart:\n%s", v)
	}
}

func TestABadValueShowsTheLoaderErrorAndKeepsTheFile(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "wip_cap")
	p.press("enter", "backspace", "0", "enter")
	if p.file(t) != fixture {
		t.Fatalf("a refused edit wrote\n%s", p.file(t))
	}
	if v := p.view(); !strings.Contains(v, `repos."acme/api": wip_cap must be at least 1`) || strings.Contains(v, "Saved") {
		t.Fatalf("no loader error:\n%s", v)
	}
	p.press("esc", "q")
	if len(p.restarts) != 0 {
		t.Fatalf("restarts %q", p.restarts)
	}
}

func TestLSetsTheKeyAtTheLevelThatTheUserPicks(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "budgets.ci_debug")
	p.press("l")
	v := p.view()
	for _, want := range []string{"Set budgets.ci_debug at which level?", "[defaults]  replaces its value", "acme/*", "acme/api"} {
		if !strings.Contains(v, want) {
			t.Errorf("the level list has no %q:\n%s", want, v)
		}
	}
	p.press("k", "enter", "backspace", "2", "enter")
	if want := "tracker = \"linear\"\n[repos.\"acme/*\".budgets]\nci_debug = 2\n"; !strings.Contains(p.file(t), want) {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if got := p.rowLine(t, "budgets.ci_debug"); got != "budgets.ci_debug 2 acme/*" {
		t.Errorf("row %q", got)
	}
}

func TestPrefixOffersOnlyTheRepoTable(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "prefix")
	p.press("l")
	if v := p.view(); strings.Contains(v, "acme/*") || strings.Contains(v, "[defaults]") {
		t.Fatalf("the level list offers a level that prefix cannot take:\n%s", v)
	}
}

func TestXRemovesTheKeyFromALevelThatSetsIt(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "wip_cap")
	p.press("x")
	if v := p.view(); !strings.Contains(v, "Remove wip_cap from which level?") || strings.Contains(v, "acme/*") {
		t.Fatalf("the level list:\n%s", v)
	}
	p.press("enter")
	if want := strings.Replace(fixture, "wip_cap = 2\n", "", 1); p.file(t) != want {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if got := p.rowLine(t, "wip_cap"); got != "wip_cap 7 [defaults]" {
		t.Errorf("row %q", got)
	}
	p.press("esc")
	p.to(t, "budgets.responding")
	p.press("x")
	if v := p.view(); !strings.Contains(v, "budgets.responding is not set at any level here.") {
		t.Fatalf("x on a default:\n%s", v)
	}
}

func TestAnEnumTakesAPickList(t *testing.T) {
	p := newPanel(t, fixture)
	p.to(t, "landing_bar.checks")
	p.press("enter")
	if v := p.view(); !strings.Contains(v, "Set landing_bar.checks at [defaults]: [all]  required") {
		t.Fatalf("no pick list:\n%s", v)
	}
	p.press("right", "enter")
	if !strings.Contains(p.file(t), "[defaults.landing_bar]\nchecks = \"required\"\n") {
		t.Fatalf("the file is\n%s", p.file(t))
	}
}

func TestASetOnARepoWithNoTableWritesItsTable(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab", "r", "acme/web", "enter")
	p.to(t, "wip_cap")
	p.press("enter")
	if v := p.view(); !strings.Contains(v, "Set wip_cap at acme/web  not set up: the save writes its table") {
		t.Fatalf("no offer to write the table:\n%s", v)
	}
	p.press("ctrl+u", "3", "enter")
	if !strings.Contains(p.file(t), "[repos.\"acme/web\"]\nwip_cap = 3\n") {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if v := p.view(); strings.Contains(v, "acme/web is not set up") {
		t.Fatalf("acme/web is still not set up:\n%s", v)
	}
}

func TestAFileThatDoesNotLoadOpensReadOnly(t *testing.T) {
	text := fixture + "[review_bot]\nname = \"greptile\"\n"
	p := newPanel(t, text)
	v := p.view()
	if !strings.Contains(v, "config: "+p.path+":") || !strings.Contains(v, "Read-only") {
		t.Fatalf("no loader error:\n%s", v)
	}
	if got := p.rowLine(t, "wip_cap"); got != "wip_cap 7 [defaults]" {
		t.Errorf("a file that does not load shows row %q", got)
	}
	p.to(t, "wip_cap")
	p.press("enter", "3", "enter", "x", "l")
	if p.file(t) != text {
		t.Fatalf("the panel wrote a file that does not load:\n%s", p.file(t))
	}
	if v := p.view(); !strings.Contains(v, "The file does not load, so the panel only shows it.") {
		t.Fatalf("no read-only note:\n%s", v)
	}
}

func TestTheFilterKeepsTheMatchingKeys(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("/", "budgets")
	var keys []string
	for _, s := range p.m.rows {
		keys = append(keys, s.Path)
	}
	if !slices.Equal(keys, []string{"budgets.ci_debug", "budgets.responding"}) {
		t.Fatalf("the filter keeps %v", keys)
	}
	p.press("esc")
	if len(p.m.rows) < 20 {
		t.Fatalf("esc keeps %d rows", len(p.m.rows))
	}
}

func TestTheReviewQueuesShowAndDoNotEdit(t *testing.T) {
	p := newPanel(t, fixture+"\n[[review.queues]]\nname = \"team\"\nrepo = [\"acme/api\"]\n")
	if got := p.rowLine(t, "review.queues[0]"); got != `review.queues[0] team: repo = ["acme/api"] top level` {
		t.Errorf("row %q", got)
	}
	p.to(t, "review.queues[0]")
	p.press("enter")
	if v := p.view(); !strings.Contains(v, "Edit the review queues in the file") {
		t.Fatalf("enter on a queue:\n%s", v)
	}
}
