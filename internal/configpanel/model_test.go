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

// to opens the groups above the setting with → and selects it with j and k.
func (p *panel) to(t *testing.T, path string) {
	t.Helper()
	for range 20 {
		i := slices.IndexFunc(p.m.rows, func(r row) bool { return r.set != nil && r.set.Path == path })
		if i < 0 {
			i = slices.IndexFunc(p.m.rows, func(r row) bool { return r.set == nil && !p.m.isOpen(r.node) && r.holds(path) })
		}
		if i < 0 {
			t.Fatalf("no row holds %s on the tab:\n%s", path, p.view())
		}
		p.move(t, i)
		if p.m.rows[i].set != nil {
			return
		}
		p.press("right")
	}
	t.Fatalf("→ does not reach %s", path)
}

// move selects the row with j and k.
func (p *panel) move(t *testing.T, i int) {
	t.Helper()
	for range len(p.m.rows) + 1 {
		switch {
		case p.m.sel < i:
			p.press("j")
		case p.m.sel > i:
			p.press("k")
		default:
			return
		}
	}
	t.Fatalf("j and k do not reach row %d in mode %d", i, p.m.mode)
}

// toGroup selects the row of the group with the title.
func (p *panel) toGroup(t *testing.T, title string) {
	t.Helper()
	i := slices.IndexFunc(p.m.rows, func(r row) bool { return r.set == nil && r.title == title })
	if i < 0 {
		t.Fatalf("no group %s in sight:\n%s", title, p.view())
	}
	p.move(t, i)
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

// rowLine selects the setting and gives its line of the view, with one space
// between its words.
func (p *panel) rowLine(t *testing.T, path string) string {
	t.Helper()
	p.to(t, path)
	return p.selLine(t)
}

// selLine is the line of the selected row, with one space between its words.
func (p *panel) selLine(t *testing.T) string {
	t.Helper()
	for _, l := range strings.Split(p.view(), "\n") {
		if strings.HasPrefix(l, "▶") {
			return strings.Join(strings.Fields(strings.TrimPrefix(l, "▶")), " ")
		}
	}
	t.Fatalf("no selected row in\n%s", p.view())
	return ""
}

// has reports whether a line of the view, with one space between its words,
// is the text.
func (p *panel) has(text string) bool {
	for _, l := range strings.Split(p.view(), "\n") {
		if strings.Join(strings.Fields(strings.TrimPrefix(l, "▶")), " ") == text {
			return true
		}
	}
	return false
}

// levelChoices are the lines of the level list below its prompt.
func (p *panel) levelChoices(t *testing.T, prompt string) []string {
	t.Helper()
	lines := strings.Split(p.view(), "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == prompt })
	if i < 0 {
		t.Fatalf("no prompt %q in\n%s", prompt, p.view())
	}
	var out []string
	for _, l := range lines[i+1:] {
		l = strings.Join(strings.Fields(strings.TrimPrefix(l, "▶")), " ")
		if l == "" || strings.HasPrefix(l, "─") {
			break
		}
		out = append(out, l)
	}
	return out
}

func TestTheGlobalTabShowsTheTopKeysAndTheDefaults(t *testing.T) {
	p := newPanel(t, fixture)
	v := p.view()
	for _, want := range []string{"[ Global ]", "Repo: acme/api ▾", "SETTING", "VALUE", "SET BY", "→ or enter open · ← close · / filter · q quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("the view has no %q:\n%s", want, v)
		}
	}
	for path, want := range map[string]string{
		"roots":            "Roots ~/repos top level",
		"wip_cap":          "WIP cap 7 [defaults]",
		"budgets.ci_debug": "CI debug 1 [defaults]",
		"poll_interval":    "Poll interval 30s default",
	} {
		if got := p.rowLine(t, path); got != want {
			t.Errorf("row %q, want %q", got, want)
		}
	}
	p.to(t, "budgets.ci_debug")
	v = p.view()
	for _, want := range []string{"Defaults for every Repo › Loop budgets › CI debug", "How many times the CI debug Stage runs",
		"File key: [defaults.budgets] ci_debug · int", "Applies after: lineup restart scheduler", "enter edit · l set at level · x remove"} {
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
		"wip_cap":            "WIP cap 2 acme/api",
		"poll_interval":      "Poll interval 20s acme/*",
		"budgets.ci_debug":   "CI debug 1 [defaults]",
		"budgets.responding": "Responding 3 default",
		"tracker":            "Tracker linear acme/*",
	} {
		if got := p.rowLine(t, path); got != want {
			t.Errorf("row %q, want %q", got, want)
		}
	}
	p.press("r", "web", "enter")
	if v := p.view(); !strings.Contains(v, "[ Repo: acme/web ▾ ]") || !strings.Contains(v, "acme/web is not set up") {
		t.Fatalf("after the pick of acme/web:\n%s", v)
	}
	if got := p.rowLine(t, "wip_cap"); got != "WIP cap 7 [defaults]" {
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
	if got := p.rowLine(t, "wip_cap"); got != "WIP cap 4 acme/api" {
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
	if got, want := p.levelChoices(t, "Set CI debug at which level?"), []string{"[defaults] replaces its value", "acme/*", "acme/api"}; !slices.Equal(got, want) {
		t.Errorf("the level list offers %q, want %q", got, want)
	}
	p.press("k", "enter", "backspace", "2", "enter")
	if want := "tracker = \"linear\"\n[repos.\"acme/*\".budgets]\nci_debug = 2\n"; !strings.Contains(p.file(t), want) {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if got := p.rowLine(t, "budgets.ci_debug"); got != "CI debug 2 acme/*" {
		t.Errorf("row %q", got)
	}
}

func TestPrefixOffersOnlyTheRepoTable(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "prefix")
	p.press("l")
	if got := p.levelChoices(t, "Set ID prefix at which level?"); !slices.Equal(got, []string{"acme/api"}) {
		t.Fatalf("the level list offers %q, and prefix takes only acme/api", got)
	}
}

func TestXRemovesTheKeyFromALevelThatSetsIt(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.to(t, "wip_cap")
	p.press("x")
	if got := p.levelChoices(t, "Remove WIP cap from which level?"); !slices.Equal(got, []string{"[defaults]", "acme/api"}) {
		t.Fatalf("the level list offers %q, want the levels that set it", got)
	}
	p.press("enter")
	if want := strings.Replace(fixture, "wip_cap = 2\n", "", 1); p.file(t) != want {
		t.Fatalf("the file is\n%s", p.file(t))
	}
	if got := p.rowLine(t, "wip_cap"); got != "WIP cap 7 [defaults]" {
		t.Errorf("row %q", got)
	}
	p.press("esc")
	p.to(t, "budgets.responding")
	p.press("x")
	if v := p.view(); !strings.Contains(v, "Responding is not set at any level here.") {
		t.Fatalf("x on a default:\n%s", v)
	}
}

func TestAnEnumTakesAPickList(t *testing.T) {
	p := newPanel(t, fixture)
	p.to(t, "landing_bar.checks")
	p.press("enter")
	if v := p.view(); !strings.Contains(v, "Set Checks at [defaults]: [all]  required") {
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
	if v := p.view(); !strings.Contains(v, "Set WIP cap at acme/web  not set up: the save writes its table") {
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
	if got := p.rowLine(t, "wip_cap"); got != "WIP cap 7 [defaults]" {
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

func TestTheFilterShowsTheMatchesWithTheirGroupsOpen(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("/", "budgets")
	for _, want := range []string{"▾ Defaults for every Repo", "▾ Loop budgets", "CI debug 1 [defaults]", "Responding 3 default"} {
		if !p.has(want) {
			t.Errorf("the filter does not show %q:\n%s", want, p.view())
		}
	}
	if v := p.view(); strings.Contains(v, "Capacity") || strings.Contains(v, "Folders and skills") {
		t.Errorf("the filter shows a group with no match:\n%s", v)
	}
	// The filter matches the labels and the groups too.
	p.press("esc", "/", "pass score")
	if !p.has("Lowest pass score the bot's top score default") || p.has("Bot none: no gate default") {
		t.Errorf("the filter on a label:\n%s", p.view())
	}
	p.press("enter", "j", "j", "j")
	if got := p.selLine(t); got != "Lowest pass score the bot's top score default" {
		t.Fatalf("selected %q", got)
	}
	// esc clears the filter and keeps the selected setting in sight.
	p.press("esc")
	if got := p.selLine(t); got != "Lowest pass score the bot's top score default" || !p.has("▸ Capacity 7 · 30s [defaults]") {
		t.Errorf("after esc, selected %q in\n%s", got, p.view())
	}
}

func TestTheReviewQueuesShowAndDoNotEdit(t *testing.T) {
	p := newPanel(t, fixture+"\n[[review.queues]]\nname = \"team\"\nrepo = [\"acme/api\"]\n")
	if got := p.rowLine(t, "review.queues[0]"); got != `team repo = ["acme/api"] top level` {
		t.Errorf("row %q", got)
	}
	p.to(t, "review.queues[0]")
	p.press("enter")
	if v := p.view(); !strings.Contains(v, "Edit the review queues in the file") {
		t.Fatalf("enter on a queue:\n%s", v)
	}
}

func TestGroupsStartClosedWithASummaryOfTheirSettings(t *testing.T) {
	p := newPanel(t, fixture)
	for _, want := range []string{
		"▸ Folders and skills ~/repos · none top level",
		"▸ Local Tracker false · ~/.local/share/lineup/issues",
		"▸ GitHub labels 0 of 5 set",
		"▸ Board and Hub layout 0 of 5 set",
		"▸ Defaults for every Repo 2 of 28 set [defaults]",
	} {
		if !p.has(want) {
			t.Errorf("the Global tab has no line %q:\n%s", want, p.view())
		}
	}
	p.press("tab")
	for _, want := range []string{
		"▸ Repo linear · none · none acme/*",
		"▸ Capacity 2 · 20s acme/*, acme/api",
		"▸ Loop budgets 1 · 3 [defaults]",
		"▸ Review bot greptile · 5 · 3 acme/api",
		"▸ Landing bar required · replied · required",
		"▸ Workers 0 of 16 set",
	} {
		if !p.has(want) {
			t.Errorf("the Repo tab has no line %q:\n%s", want, p.view())
		}
	}
	for _, r := range p.m.rows {
		if r.set != nil {
			t.Errorf("the setting %s is in sight before its group opens", r.set.Path)
		}
	}
}

func TestArrowsAndEnterOpenAndCloseAGroup(t *testing.T) {
	p := newPanel(t, fixture)
	p.press("tab")
	p.toGroup(t, "Capacity")
	p.press("right")
	if !p.has("▾ Capacity") || !p.has("WIP cap 2 acme/api") || !p.has("Poll interval 20s acme/*") {
		t.Fatalf("→ does not open Capacity:\n%s", p.view())
	}
	p.press("right")
	if got := p.selLine(t); got != "WIP cap 2 acme/api" {
		t.Errorf("→ on an open group selects %q, want its first setting", got)
	}
	p.press("left")
	if got := p.selLine(t); got != "▾ Capacity" {
		t.Errorf("← on a setting selects %q, want its group", got)
	}
	p.press("left")
	if !p.has("▸ Capacity 2 · 20s acme/*, acme/api") || p.has("WIP cap 2 acme/api") {
		t.Errorf("← does not close Capacity:\n%s", p.view())
	}
	p.press("enter")
	if !p.has("WIP cap 2 acme/api") {
		t.Errorf("enter does not open Capacity:\n%s", p.view())
	}
	p.press("enter", "l", "x")
	if p.has("WIP cap 2 acme/api") || p.m.mode != modeList || p.file(t) != fixture {
		t.Errorf("enter, l, and x on a group:\n%s", p.view())
	}
}

// TestTheDetailShowsTheValueAtEachLevel checks the layering of a key for
// each level that can apply: the default, [defaults], owner/*, owner/repo,
// and the stage.default key of a Stage.
func TestTheDetailShowsTheValueAtEachLevel(t *testing.T) {
	p := newPanel(t, fixture+"[defaults.stage.default]\nmodel = \"m1\"\n")
	p.press("tab")
	for _, c := range []struct {
		path  string
		lines []string
	}{
		{"budgets.responding", []string{"Loop budgets › Responding", "default 3 ◀ applies", "[defaults] ·", "acme/* ·", "acme/api ·"}},
		{"budgets.ci_debug", []string{"default 3", "[defaults] 1 ◀ applies", "acme/* ·", "acme/api ·"}},
		{"poll_interval", []string{"default 30s", "[defaults] ·", "acme/* 20s ◀ applies", "acme/api ·"}},
		{"wip_cap", []string{"Capacity › WIP cap", "How many build Tasks can be started at one time.",
			"default 5", "[defaults] 7", "acme/* ·", "acme/api 2 ◀ applies",
			`File key: [repos."acme/api"] wip_cap · int`, "Applies after: lineup restart scheduler"}},
		{"stage.working.model", []string{"Workers › Stage Working › Model", "default claude-opus-5-5",
			"[defaults] (stage.default) m1 ◀ applies", "[defaults] ·", "acme/* ·", "acme/api ·",
			`File key: [repos."acme/api".stage.working] model · string`}},
		{"review_bot.min_review_score", []string{"default the bot's top score (5) ◀ applies", "acme/api ·"}},
		{"landing_bar.checks", []string{`File key: [repos."acme/api".landing_bar] checks · enum · all, required`}},
	} {
		p.to(t, c.path)
		for _, want := range c.lines {
			if !p.has(want) {
				t.Errorf("%s: the detail has no line %q:\n%s", c.path, want, p.view())
			}
		}
	}
	p.press("tab")
	p.to(t, "roots")
	for _, want := range []string{"Folders and skills › Roots", "default none", "top level ~/repos ◀ applies", "File key: roots · list"} {
		if !p.has(want) {
			t.Errorf("roots: the detail has no line %q:\n%s", want, p.view())
		}
	}
}

// TestThePanelFitsTheTerminal draws each mode at 80 by 24 and at 120 by 30:
// each line fits the width, the view fills the height, and the selected row
// and the footer stay in sight.
func TestThePanelFitsTheTerminal(t *testing.T) {
	text := fixture + "[defaults.stage.default]\nmodel = \"m1\"\n[repos.\"acme/a-very-long-repo-name\"]\nwip_cap = 3\n"
	for _, size := range [][2]int{{80, 24}, {120, 30}} {
		p := newPanel(t, text)
		next, _ := p.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		p.m = next.(Model)
		check := func(step string) {
			t.Helper()
			lines := strings.Split(p.m.View(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d %s: the view has %d lines", size[0], size[1], step, len(lines))
			}
			for _, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d %s: a line is %d wide: %q", size[0], size[1], step, w, ansi.Strip(l))
				}
			}
			if v := p.view(); !strings.Contains(v, "▶") || !strings.Contains(lines[len(lines)-1], "quit") && !strings.Contains(v, "esc") {
				t.Errorf("%dx%d %s: no selected row or no footer:\n%s", size[0], size[1], step, v)
			}
		}
		check("Global")
		p.press("tab")
		check("Repo")
		p.to(t, "stage.working.model")
		check("a stage key")
		p.to(t, "prefix")
		if !strings.Contains(p.view(), "HOD-3.") {
			t.Errorf("%dx%d: the doc of prefix breaks HOD-3:\n%s", size[0], size[1], p.view())
		}
		p.press("l")
		check("the level list")
		p.press("esc", "enter")
		check("the editor")
		p.press("esc", "/", "w")
		check("the filter")
		p.press("esc", "r")
		check("the Repo picker")
	}
}

func TestTabAndTheRepoPickerKeepTheSelectedKey(t *testing.T) {
	p := newPanel(t, fixture)
	p.to(t, "wip_cap")
	p.press("tab")
	if got := p.selLine(t); got != "WIP cap 2 acme/api" {
		t.Errorf("after tab, selected %q, want WIP cap of acme/api", got)
	}
	p.press("r", "web", "enter")
	if got := p.selLine(t); got != "WIP cap 7 [defaults]" {
		t.Errorf("after the pick of acme/web, selected %q", got)
	}
	p.to(t, "tracker")
	p.press("tab")
	if got := p.selLine(t); got != "▸ Folders and skills ~/repos · none top level" {
		t.Errorf("a key that the Global tab lacks selects %q, want the first row", got)
	}
}
