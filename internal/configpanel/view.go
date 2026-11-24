package configpanel

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/config"
)

var (
	teal   = lipgloss.Color("#5fb3ad")
	pink   = lipgloss.Color("#f3a3b5")
	gray   = lipgloss.Color("#9aa3ad")
	dim    = lipgloss.NewStyle().Foreground(gray)
	bold   = lipgloss.NewStyle().Bold(true)
	good   = lipgloss.NewStyle().Foreground(teal)
	bad    = lipgloss.NewStyle().Foreground(pink)
	head   = lipgloss.NewStyle().Foreground(teal).Bold(true)
	selRow = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e3f1ef")).Background(lipgloss.Color("#23403d"))
)

func (m Model) View() string {
	rule := dim.Render(strings.Repeat("─", max(m.width, 1)))
	top := []string{bold.Render(" lineup config") + dim.Render("  "+m.file.Path), m.tabLine()}
	if m.readOnly() {
		top = append(top, m.wrap(bad, "config: "+m.file.LoadErr.Error())...)
		top = append(top, bad.Render(" Read-only: fix the file, then open the panel again."))
	} else if m.tab == tabRepo && m.repo != "" && !m.file.SetUp(m.repo) {
		top = append(top, dim.Render(" "+m.repo+" is not set up. A save at "+m.repo+" writes its table."))
	}
	bottom := stack(m.detail(), rule, m.statusLines(), m.footer())
	h := max(m.height-len(top)-2-len(bottom), 3)
	var body []string
	switch m.mode {
	case modeRepos:
		body = m.repoList(h)
	case modeLevels:
		body = m.levelList()
	default:
		body = m.table(h)
	}
	for len(body) < h {
		body = append(body, "")
	}
	lines := stack(top, rule, body[:h], rule, bottom)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	return strings.Join(lines, "\n")
}

// wrap breaks the text into lines that fit the width, each in the style.
func (m Model) wrap(style lipgloss.Style, text string) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(max(m.width-2, 20)).Render(text), "\n") {
		out = append(out, style.Render(" "+strings.TrimRight(l, " ")))
	}
	return out
}

// stack joins lines and lists of lines.
func stack(parts ...any) []string {
	var out []string
	for _, p := range parts {
		switch p := p.(type) {
		case string:
			out = append(out, p)
		case []string:
			out = append(out, p...)
		}
	}
	return out
}

func (m Model) tabLine() string {
	global, repo := " Global ", " Repo: "+cmpOr(m.repo, "none")+" ▾ "
	if m.tab == tabGlobal {
		return " " + head.Render("["+global+"]") + "  " + dim.Render(repo)
	}
	return " " + dim.Render(global) + "  " + head.Render("["+repo+"]")
}

// table is the KEY, VALUE, and SET BY columns, with the selected row in
// sight.
func (m Model) table(h int) []string {
	if len(m.rows) == 0 {
		switch {
		case m.tab == tabRepo && m.repo == "":
			return []string{dim.Render(" No checkout below roots. Set roots on the Global tab.")}
		case m.filter.Value() != "":
			return []string{m.filterLine(), dim.Render(" No key matches the filter.")}
		}
		return nil
	}
	keyW, valW := len("KEY"), len("VALUE")
	for _, s := range m.rows {
		keyW, valW = max(keyW, len(s.Path)), max(valW, ansi.StringWidth(s.Value))
	}
	keyW, valW = min(keyW, 38), min(valW, max(m.width-keyW-24, 12))
	cell := func(s string, w int) string { return fmt.Sprintf("%-*s", w, ansi.Truncate(s, w, "…")) }
	out := []string{dim.Render("   " + cell("KEY", keyW) + "  " + cell("VALUE", valW) + "  SET BY")}
	if l := m.filterLine(); l != "" {
		out = append(out, l)
	}
	rows := make([]string, len(m.rows))
	for i, s := range m.rows {
		line := cell(s.Path, keyW) + "  " + cell(s.Value, valW) + "  " + s.Source()
		switch {
		case i == m.sel:
			rows[i] = selRow.Render("▶  " + line)
		case s.By == nil:
			rows[i] = dim.Render("   " + line)
		default:
			rows[i] = "   " + line
		}
	}
	room := max(h-len(out), 1)
	start := min(max(m.sel-room/2, 0), max(len(rows)-room, 0))
	return append(out, rows[start:min(start+room, len(rows))]...)
}

func (m Model) filterLine() string {
	if m.mode != modeFilter && m.filter.Value() == "" {
		return ""
	}
	return " " + m.filter.View()
}

func (m Model) repoList(h int) []string {
	out := []string{" " + m.repoFilter.View()}
	choices := m.repoChoices()
	start := min(max(m.repoSel-(h-1)/2, 0), max(len(choices)-(h-1), 0))
	for i := start; i < len(choices) && len(out) < h; i++ {
		r := choices[i]
		line := r
		if !m.file.SetUp(r) {
			line += dim.Render("  not set up")
		}
		if i == m.repoSel {
			out = append(out, selRow.Render("▶  "+r)+strings.TrimPrefix(line, r))
		} else {
			out = append(out, "   "+line)
		}
	}
	if len(choices) == 0 {
		out = append(out, dim.Render("   No Repo matches."))
	}
	return out
}

func (m Model) levelList() []string {
	verb := "Set " + m.edit.Path + " at which level?"
	if m.remove {
		verb = "Remove " + m.edit.Path + " from which level?"
	}
	out := []string{" " + bold.Render(verb)}
	for i, w := range m.levels {
		line := w.String() + m.levelNote(w)
		if i == m.levelAt {
			out = append(out, selRow.Render("▶  "+line))
		} else {
			out = append(out, "   "+line)
		}
	}
	return out
}

// levelNote says what a save at the level does besides the key.
func (m Model) levelNote(w config.Where) string {
	switch {
	case m.remove:
		return ""
	case w.Level == config.LevelRepo && !m.file.SetUp(w.Table):
		return dim.Render("  not set up: the save writes its table")
	case w.Level == config.LevelOwner && !m.file.SetUp(w.Table):
		return dim.Render("  no table yet: the save writes it")
	}
	for _, s := range m.edit.SetAt {
		if s.Level == w.Level {
			return dim.Render("  replaces its value")
		}
	}
	return ""
}

// detail describes the selected key: its doc, its type and values, its
// default, and the restart that a change needs.
func (m Model) detail() []string {
	s, ok := m.selected()
	if !ok {
		return nil
	}
	k := s.Key
	out := m.wrap(lipgloss.NewStyle(), s.Path+": "+k.Doc)
	kind := "Type " + string(k.Type)
	if len(k.Values) > 0 {
		kind += ": " + strings.Join(k.Values, ", ")
	}
	out = append(out, dim.Render(" "+kind+". Default "+k.Default+"."), dim.Render(" Applies after: "+k.Restart))
	return out
}

// statusLines are the editor, the status, and the restart that waits.
func (m Model) statusLines() []string {
	var out []string
	at := m.at.String() + m.levelNote(m.at)
	switch m.mode {
	case modePick:
		var items []string
		for i, c := range m.choices {
			if i == m.choice {
				items = append(items, head.Render("["+c+"]"))
			} else {
				items = append(items, " "+c+" ")
			}
		}
		out = append(out, " Set "+m.edit.Path+" at "+at+": "+strings.Join(items, " "))
	case modeInput:
		hint := ""
		if m.edit.Key.Type == config.TypeList {
			hint = dim.Render("  items with commas between them")
		}
		out = append(out, " Set "+m.edit.Path+" at "+at+":"+hint, " > "+m.input.View())
	}
	switch {
	case m.status != "" && m.bad:
		out = append(out, m.wrap(bad, m.status)...)
	case m.status != "":
		out = append(out, m.wrap(good, m.status)...)
	}
	if r := m.waitsFor(); r != "" {
		out = append(out, dim.Render(" The change waits for "+r+"."))
	}
	return out
}

func (m Model) footer() string {
	var keys string
	switch m.mode {
	case modeFilter:
		keys = "type to filter · enter keep · esc clear"
	case modeRepos:
		keys = "type to filter · enter pick · esc cancel"
	case modeLevels:
		keys = "enter pick · esc cancel"
	case modePick:
		keys = "←/→ choose · enter save · esc cancel"
	case modeInput:
		keys = "enter save · esc cancel"
	case modeSaved:
		keys = "enter runs it · esc skips"
	case modeRunning:
		keys = "the restart runs"
	default:
		keys = "enter edit · l set at level · x remove from level · / filter · tab Global/Repo"
		if m.tab == tabRepo {
			keys += " · r pick Repo"
		}
		keys += " · q quit"
	}
	return dim.Render(" " + keys)
}
