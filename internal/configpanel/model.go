// Package configpanel is the panel of lineup config: a tree of plain-named
// groups that hold each key of the config file, with the value that applies
// and the level that sets it, and for the selected key its doc and its value
// at each level. It changes one key at a time through config.File, which
// writes the file only when the loader passes the new text (PROD-CONFIG-008).
package configpanel

import (
	"errors"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/config"
)

// Options are what the caller wires in.
type Options struct {
	File *config.File
	// Repos are the Repo keys that the Repo tab offers.
	Repos []string
	// Repo is the Repo that the Repo tab shows first. "" takes the first of
	// Repos.
	Repo string
	// Restart runs lineup with the arguments of a restart, such as restart
	// scheduler, and returns what it printed.
	Restart func(args []string) (string, error)
}

type tab int

const (
	tabGlobal tab = iota
	tabRepo
)

type mode int

const (
	modeList    mode = iota
	modeFilter       // typing the filter of the rows
	modeRepos        // the Repo picker
	modeLevels       // the level picker of l and x
	modePick         // the values of an enum or a bool
	modeInput        // typing a value
	modeSaved        // the offer of the restart that a save needs
	modeRunning      // the restart runs
)

type restartMsg struct {
	restart, out string
	err          error
}

// node is a group of the tree, or one setting in it.
type node struct {
	// id is a group's titles from the outermost, joined by " › ", or a
	// setting's path.
	id    string
	title string
	set   *config.Setting // nil for a group
	kids  []*node
}

// group is the subgroup of n with the id, which it adds when n has none.
func (n *node) group(id, title string) *node {
	for _, k := range n.kids {
		if k.set == nil && k.id == id {
			return k
		}
	}
	k := &node{id: id, title: title}
	n.kids = append(n.kids, k)
	return k
}

// leaves lists the settings below n, in tree order.
func (n *node) leaves() []config.Setting {
	if n.set != nil {
		return []config.Setting{*n.set}
	}
	var out []config.Setting
	for _, k := range n.kids {
		out = append(out, k.leaves()...)
	}
	return out
}

// holds reports whether n or a node below it has the id.
func (n *node) holds(id string) bool {
	return n.id == id || slices.ContainsFunc(n.kids, func(k *node) bool { return k.holds(id) })
}

// row is a node in sight, at its depth below the top of the tree.
type row struct {
	*node
	depth  int
	parent int // the index of the row of the parent group, or -1
}

// defaultsGroup holds the [defaults] keys on the Global tab.
const defaultsGroup = "Defaults for every Repo"

type Model struct {
	opts Options
	file *config.File
	tab  tab
	repo string // the Repo of the Repo tab, or ""
	all  []config.Setting
	rows []row
	// open holds the ids of the groups that the user opened. A filter shows
	// each group that holds a match open.
	open map[string]bool
	sel  int
	mode mode

	filter     textinput.Model
	repoFilter textinput.Model
	repoSel    int

	// The edit in progress: its row, its level, and its value.
	edit    config.Setting
	at      config.Where
	levels  []config.Where
	remove  bool // the level picker is for a removal
	levelAt int
	choices []string
	choice  int
	input   textinput.Model

	restart string   // the restart that the last save needs
	waiting []string // the restarts that the user skipped
	status  string
	bad     bool // the status is an error

	width, height int
}

func newInput(prompt string) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.Cursor.SetMode(cursor.CursorStatic)
	return in
}

// New builds the panel on the Global tab.
func New(opts Options) Model {
	m := Model{opts: opts, file: opts.File, width: 100, height: 30, open: map[string]bool{},
		filter: newInput("/ "), repoFilter: newInput("Repo: "), input: newInput("")}
	m.repo = opts.Repo
	if m.repo == "" && len(opts.Repos) > 0 {
		m.repo = opts.Repos[0]
	}
	m.refresh()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// readOnly reports whether the file does not load, so the panel edits nothing.
func (m Model) readOnly() bool { return m.file.LoadErr != nil }

// refresh reads the settings of the tab again, builds the tree, and keeps
// the selected row in sight: it opens the groups above it.
func (m *Model) refresh() {
	keep := ""
	if r, ok := m.selectedRow(); ok {
		keep = r.id
	}
	switch {
	case m.tab == tabGlobal:
		m.all = m.file.Settings("")
	case m.repo != "":
		m.all = m.file.Settings(m.repo)
	default:
		m.all = nil
	}
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	top := &node{}
	for i := range m.all {
		s := &m.all[i]
		crumbs := m.crumbs(*s)
		text := strings.ToLower(strings.Join(append(slices.Clone(crumbs), s.Label, s.Path), " "))
		if !strings.Contains(text, q) {
			continue
		}
		at := top
		for d, title := range crumbs {
			at = at.group(strings.Join(crumbs[:d+1], " › "), title)
		}
		at.kids = append(at.kids, &node{id: s.Path, title: s.Label, set: s})
	}
	for _, g := range top.kids {
		m.reveal(g, keep)
	}
	m.rows = nil
	m.flatten(top.kids, 0, -1)
	m.sel = max(slices.IndexFunc(m.rows, func(r row) bool { return r.id == keep }), 0)
}

// reveal opens each group above the node with the id.
func (m *Model) reveal(n *node, id string) bool {
	if n.set != nil || n.id == id {
		return n.id == id
	}
	for _, k := range n.kids {
		if m.reveal(k, id) {
			m.open[n.id] = true
			return true
		}
	}
	return false
}

func (m *Model) flatten(nodes []*node, depth, parent int) {
	for _, n := range nodes {
		m.rows = append(m.rows, row{node: n, depth: depth, parent: parent})
		if m.isOpen(n) {
			m.flatten(n.kids, depth+1, len(m.rows)-1)
		}
	}
}

// crumbs are the titles of the groups that hold the setting on the tab. The
// Global tab puts the [defaults] keys in a group of their own.
func (m Model) crumbs(s config.Setting) []string {
	if m.tab == tabGlobal && !slices.Contains(s.Key.Levels, config.LevelTop) {
		return append([]string{defaultsGroup}, s.Group...)
	}
	return s.Group
}

func (m Model) filtering() bool { return strings.TrimSpace(m.filter.Value()) != "" }

// isOpen reports whether the node is a group that shows its rows.
func (m Model) isOpen(n *node) bool { return n.set == nil && (m.open[n.id] || m.filtering()) }

func (m Model) selectedRow() (row, bool) {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.sel], true
}

// selected is the setting of the selected row. A group has none.
func (m Model) selected() (config.Setting, bool) {
	if r, ok := m.selectedRow(); ok && r.set != nil {
		return *r.set, true
	}
	return config.Setting{}, false
}

// openGroup opens or closes the selected group.
func (m Model) openGroup(open bool) Model {
	if r, ok := m.selectedRow(); ok && r.set == nil {
		m.open[r.id] = open
		m.refresh()
	}
	return m
}

func (m Model) say(text string, bad bool) Model {
	m.status, m.bad = text, bad
	return m
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case restartMsg:
		return m.restarted(msg), nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeFilter:
			return m.filterKey(msg)
		case modeRepos:
			return m.repoKey(msg)
		case modeLevels:
			return m.levelKey(msg)
		case modePick:
			return m.pickKey(msg)
		case modeInput:
			return m.inputKey(msg)
		case modeSaved:
			return m.savedKey(msg)
		case modeRunning:
			return m, nil
		}
		return m.listKey(msg)
	}
	return m, nil
}

func (m Model) listKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "tab", "shift+tab":
		// The other tab keeps the selected key when it has it.
		m.tab = 1 - m.tab
		m.refresh()
	case "j", "down":
		m.sel = min(m.sel+1, max(len(m.rows)-1, 0))
	case "k", "up":
		m.sel = max(m.sel-1, 0)
	case "g", "home":
		m.sel = 0
	case "G", "end":
		m.sel = max(len(m.rows)-1, 0)
	case "/":
		m.mode = modeFilter
		return m, m.filter.Focus()
	case "right":
		// → opens a group, and on an open group it selects the first row.
		r, ok := m.selectedRow()
		switch {
		case ok && r.set == nil && !m.isOpen(r.node):
			return m.openGroup(true), nil
		case ok && r.set == nil:
			m.sel = min(m.sel+1, len(m.rows)-1)
		}
	case "left":
		// ← closes an open group, and on any other row it selects the group.
		r, ok := m.selectedRow()
		switch {
		case ok && r.set == nil && m.isOpen(r.node) && !m.filtering():
			return m.openGroup(false), nil
		case ok && r.parent >= 0:
			m.sel = r.parent
		}
	case "esc":
		m.filter.SetValue("")
		m.refresh()
	case "r":
		if m.tab == tabRepo && len(m.opts.Repos) > 0 {
			m.mode, m.repoSel = modeRepos, max(slices.Index(m.opts.Repos, m.repo), 0)
			m.repoFilter.SetValue("")
			return m, m.repoFilter.Focus()
		}
	case "enter":
		if r, ok := m.selectedRow(); ok && r.set == nil {
			return m.openGroup(!m.open[r.id]), nil
		}
		return m.startEdit(false, false)
	case "l":
		return m.startEdit(true, false)
	case "x":
		return m.startEdit(true, true)
	}
	return m, nil
}

// startEdit opens the edit of the selected row: the level picker first for
// l and x, else the editor at the row's own level.
func (m Model) startEdit(pickLevel, remove bool) (tea.Model, tea.Cmd) {
	s, ok := m.selected()
	switch {
	case !ok:
		return m, nil
	case m.readOnly():
		return m.say("The file does not load, so the panel only shows it. Fix the file, then open the panel again.", true), nil
	case s.Key.Type == config.TypeQueue:
		return m.say("Edit the review queues in the file: "+m.file.Path, true), nil
	}
	m.edit, m.remove = s, remove
	if remove {
		m.levels = s.SetAt()
		if len(m.levels) == 0 {
			why := s.Label + " is not set at any level here."
			if s.From != "" {
				why += " Its value comes from " + s.From + "."
			}
			return m.say(why, true), nil
		}
	} else {
		m.levels = s.Levels()
	}
	if !pickLevel {
		return m.openEditor(m.levels[len(m.levels)-1])
	}
	m.mode, m.levelAt = modeLevels, len(m.levels)-1
	return m, nil
}

func (m Model) levelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
	case "j", "down", "right", "tab":
		m.levelAt = min(m.levelAt+1, len(m.levels)-1)
	case "k", "up", "left", "shift+tab":
		m.levelAt = max(m.levelAt-1, 0)
	case "enter":
		at := m.levels[m.levelAt]
		if m.remove {
			m.at = at
			return m.save(nil)
		}
		return m.openEditor(at)
	}
	return m, nil
}

// openEditor opens the input that fits the key's type: a pick list for an
// enum or a bool, and a text input for the rest.
func (m Model) openEditor(at config.Where) (tea.Model, tea.Cmd) {
	m.at = at
	s := m.edit
	current := ""
	if s.Raw != nil {
		current = s.Value
	}
	switch s.Key.Type {
	case config.TypeEnum, config.TypeBool:
		m.choices = s.Key.Values
		if s.Key.Type == config.TypeBool {
			m.choices = []string{"true", "false"}
		}
		m.choice = max(slices.Index(m.choices, current), 0)
		m.mode = modePick
		return m, nil
	case config.TypeList:
		var items []string
		if list, ok := s.Raw.([]any); ok {
			for _, it := range list {
				if str, ok := it.(string); ok {
					items = append(items, str)
				}
			}
		}
		current = strings.Join(items, ", ")
	}
	m.input.SetValue(current)
	m.input.CursorEnd()
	m.mode = modeInput
	return m, m.input.Focus()
}

func (m Model) pickKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
	case "right", "l", "j", "down", "tab":
		m.choice = (m.choice + 1) % len(m.choices)
	case "left", "h", "k", "up", "shift+tab":
		m.choice = (m.choice + len(m.choices) - 1) % len(m.choices)
	case "enter":
		v, err := m.edit.Key.Encode(m.choices[m.choice])
		if err != nil {
			return m.say(err.Error(), true), nil
		}
		return m.save(&v)
	}
	return m, nil
}

func (m Model) inputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.status = ""
		return m, nil
	case "enter":
		v, err := m.edit.Key.Encode(m.input.Value())
		if err != nil {
			return m.say(err.Error(), true), nil
		}
		return m.save(&v)
	}
	var cmd tea.Cmd
	m.input, cmd = typeInto(m.input, msg)
	return m, cmd
}

// save applies the edit at m.at. A refused edit keeps the editor open with
// the error, and the file stays as it was. A file that changed on disk is
// read again.
func (m Model) save(value *string) (tea.Model, tea.Cmd) {
	err := m.file.Apply(config.Edit{Level: m.at.Level, Repo: m.at.Table, Path: m.edit.Path, Value: value})
	if errors.Is(err, config.ErrFileChanged) {
		if f, rerr := config.ReadFile(m.file.Path); rerr == nil {
			m.file = f
		}
		m.mode = modeList
		m.refresh()
		return m.say(err.Error()+" The panel read the file again.", true), nil
	}
	if err != nil {
		if m.mode == modeLevels {
			m.mode = modeList
		}
		return m.say(err.Error(), true), nil
	}
	m.input.Blur()
	m.refresh()
	m.restart, m.mode = m.edit.Key.Restart, modeSaved
	return m.say("Saved. Applies after: "+m.restart+".", false), nil
}

func (m Model) savedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeRunning
		restart, run := m.restart, m.opts.Restart
		m = m.say("Running "+restart+"…", false)
		return m, func() tea.Msg {
			out, err := run(strings.Fields(restart)[1:])
			return restartMsg{restart, out, err}
		}
	case "esc", "q":
		if !slices.Contains(m.waiting, m.restart) {
			m.waiting = append(m.waiting, m.restart)
		}
		m.mode = modeList
		m.status = ""
	}
	return m, nil
}

func (m Model) restarted(msg restartMsg) Model {
	m.mode = modeList
	if msg.err != nil {
		return m.say(msg.restart+": "+strings.TrimSpace(cmpOr(msg.out, msg.err.Error())), true)
	}
	// lineup restart restarts the Scheduler too.
	m.waiting = slices.DeleteFunc(m.waiting, func(r string) bool {
		return r == msg.restart || msg.restart == config.RestartAll
	})
	return m.say("Ran "+msg.restart+". "+lastLine(msg.out), false)
}

// waitsFor is the restart that the skipped restarts need, or "".
func (m Model) waitsFor() string {
	if slices.Contains(m.waiting, config.RestartAll) {
		return config.RestartAll
	}
	if len(m.waiting) > 0 {
		return m.waiting[0]
	}
	return ""
}

func (m Model) filterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filter.SetValue("")
		fallthrough
	case "enter":
		m.mode = modeList
		m.filter.Blur()
		m.refresh()
		return m, nil
	case "down":
		m.sel = min(m.sel+1, max(len(m.rows)-1, 0))
		return m, nil
	case "up":
		m.sel = max(m.sel-1, 0)
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = typeInto(m.filter, msg)
	m.refresh()
	m.sel = 0
	return m, cmd
}

// repoChoices are the Repo keys that the picker's filter keeps.
func (m Model) repoChoices() []string {
	q := strings.ToLower(strings.TrimSpace(m.repoFilter.Value()))
	return slices.DeleteFunc(slices.Clone(m.opts.Repos), func(r string) bool {
		return q != "" && !strings.Contains(strings.ToLower(r), q)
	})
}

func (m Model) repoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.repoChoices()
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.repoFilter.Blur()
		return m, nil
	case "down", "ctrl+n", "tab":
		m.repoSel = min(m.repoSel+1, max(len(choices)-1, 0))
		return m, nil
	case "up", "ctrl+p", "shift+tab":
		m.repoSel = max(m.repoSel-1, 0)
		return m, nil
	case "enter":
		if m.repoSel < len(choices) {
			m.repo = choices[m.repoSel]
		}
		m.mode = modeList
		m.repoFilter.Blur()
		m.refresh()
		return m, nil
	}
	var cmd tea.Cmd
	m.repoFilter, cmd = typeInto(m.repoFilter, msg)
	m.repoSel = 0
	return m, cmd
}

// typeInto gives a key to a text input. A terminal can send fast input as one
// run of runes, and the text input reads a run that spells a key name, such
// as "up", as that key. So the input gets one rune at a time.
func typeInto(in textinput.Model, msg tea.KeyMsg) (textinput.Model, tea.Cmd) {
	if msg.Type != tea.KeyRunes {
		return in.Update(msg)
	}
	var cmds []tea.Cmd
	for _, r := range msg.Runes {
		var cmd tea.Cmd
		in, cmd = in.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt, Paste: msg.Paste})
		cmds = append(cmds, cmd)
	}
	return in, tea.Batch(cmds...)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func cmpOr(s, or string) string {
	if strings.TrimSpace(s) == "" {
		return or
	}
	return s
}

// Run shows the panel until the user quits.
func Run(opts Options) error {
	_, err := tea.NewProgram(New(opts), tea.WithAltScreen()).Run()
	return err
}
