package configpanel

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
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

// wrap breaks the text into lines that fit the width, each in the style. It
// breaks at spaces, so HOD-3 stays whole.
func (m Model) wrap(style lipgloss.Style, text string) []string {
	var out []string
	text = strings.ReplaceAll(text, "-", nbHyphen)
	for _, l := range strings.Split(lipgloss.NewStyle().Width(max(m.width-2, 20)).Render(text), "\n") {
		l = strings.ReplaceAll(strings.TrimRight(l, " "), nbHyphen, "-")
		out = append(out, style.Render(" "+l))
	}
	return out
}

// nbHyphen is a hyphen that a line does not break at.
const nbHyphen = "\u2011"

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
		return " " + head.Render("["+global+"]") + "  " + dim.Render(repo+"   tab switches")
	}
	return " " + dim.Render(global) + "  " + head.Render("["+repo+"]") + dim.Render("   tab switches · r picks the Repo")
}

// table is the SETTING, VALUE, and SET BY columns of the rows in sight,
// with the selected row in sight. A closed group sums up its settings.
func (m Model) table(h int) []string {
	if len(m.rows) == 0 {
		switch {
		case m.tab == tabRepo && m.repo == "":
			return []string{dim.Render(" No checkout below roots. Set roots on the Global tab.")}
		case m.filter.Value() != "":
			return []string{m.filterLine(), dim.Render(" No setting matches the filter.")}
		}
		return nil
	}
	type cells struct{ name, value, by string }
	lines := make([]cells, len(m.rows))
	nameW, byW := len("SETTING"), len("SET BY")
	for i, r := range m.rows {
		var c cells
		marker := "  "
		switch {
		case r.set != nil:
			c.value, c.by = plain(r.set.Value), r.set.Source()
		case m.isOpen(r.node):
			marker = "▾ "
		default:
			marker = "▸ "
			c.value, c.by = summary(r.node)
		}
		c.name = strings.Repeat("  ", r.depth) + marker + r.title
		lines[i] = c
		nameW, byW = max(nameW, ansi.StringWidth(c.name)), max(byW, ansi.StringWidth(c.by))
	}
	valW := len("VALUE")
	for _, c := range lines {
		valW = max(valW, ansi.StringWidth(c.value))
	}
	nameW, byW = min(nameW, max(m.width*2/5, 24)), min(byW, max(m.width/3, 14))
	valW = min(valW, max(m.width-nameW-byW-6, 10))
	cell := func(s string, w int) string { return fmt.Sprintf("%-*s", w, ansi.Truncate(s, w, "…")) }
	out := []string{dim.Render("  " + cell("SETTING", nameW) + "  " + cell("VALUE", valW) + "  SET BY")}
	if l := m.filterLine(); l != "" {
		out = append(out, l)
	}
	rows := make([]string, len(m.rows))
	for i, c := range lines {
		by := ansi.Truncate(c.by, byW, "…")
		line := cell(c.name, nameW) + "  " + cell(c.value, valW) + "  " + by
		r := m.rows[i]
		switch {
		case i == m.sel:
			rows[i] = selRow.Render("▶ " + line)
		case r.set == nil:
			rows[i] = "  " + bold.Render(cell(c.name, nameW)) + "  " + dim.Render(cell(c.value, valW)+"  "+by)
		case r.set.By == nil:
			rows[i] = dim.Render("  " + line)
		default:
			rows[i] = "  " + line
		}
	}
	room := max(h-len(out), 1)
	start := min(max(m.sel-room/2, 0), max(len(rows)-room, 0))
	return append(out, rows[start:min(start+room, len(rows))]...)
}

// summary sums up the settings of a closed group: their values when it holds
// at most three and no subgroup, else how many of them a level sets. by
// names each level whose value applies to one of them.
func summary(n *node) (value, by string) {
	leaves := n.leaves()
	var values, levels []string
	set, small := 0, len(leaves) <= 3
	var seen []config.Where
	for _, s := range leaves {
		values = append(values, plain(s.Value))
		if s.Key.Type == config.TypeQueue {
			small = false
		}
		if s.By != nil {
			set++
			if !slices.Contains(seen, *s.By) {
				seen = append(seen, *s.By)
			}
		}
	}
	for _, k := range n.kids {
		small = small && k.set != nil
	}
	slices.SortStableFunc(seen, func(a, b config.Where) int { return int(a.Level) - int(b.Level) })
	for _, w := range seen {
		levels = append(levels, w.String())
	}
	if small {
		return strings.Join(values, " · "), strings.Join(levels, ", ")
	}
	return fmt.Sprintf("%d of %d set", set, len(leaves)), strings.Join(levels, ", ")
}

// plain writes a TOML list of strings as its items with commas between them,
// and an empty list as none. Other text stays as it is.
func plain(text string) string {
	if !strings.HasPrefix(text, "[") {
		return text
	}
	var v struct{ V []string }
	if _, err := toml.Decode("V = "+text, &v); err != nil {
		return text
	}
	if len(v.V) == 0 {
		return "none"
	}
	return strings.Join(v.V, ", ")
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
	verb := "Set " + m.edit.Label + " at which level?"
	if m.remove {
		verb = "Remove " + m.edit.Label + " from which level?"
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
	for _, s := range m.edit.SetAt() {
		if s.Level == w.Level {
			return dim.Render("  replaces its value")
		}
	}
	return ""
}

// detail describes the selected row. A group names the rows that it holds.
// A setting gives its doc, its value at each level with the one that
// applies, the key as the file writes it where enter sets it, and the
// restart that a change needs (PROD-CONFIG-012).
func (m Model) detail() []string {
	r, ok := m.selectedRow()
	if !ok {
		return nil
	}
	// The pane keeps the height of the tallest detail of the tab, so the
	// tree does not jump as the selection moves.
	tallest := 0
	for i := range m.all {
		tallest = max(tallest, len(m.detailOf(&node{set: &m.all[i]})))
	}
	out := m.detailOf(r.node)
	for len(out) < min(tallest, m.height/2) {
		out = append(out, "")
	}
	return out
}

func (m Model) detailOf(r *node) []string {
	if r.set == nil {
		var titles []string
		for _, k := range r.kids {
			titles = append(titles, k.title)
		}
		return stack(bold.Render(" "+r.id), m.wrap(dim, "Holds "+strings.Join(titles, " · ")))
	}
	s := *r.set
	crumbs := slices.Concat(m.crumbs(s), []string{s.Label})
	out := stack(bold.Render(" "+strings.Join(crumbs, " › ")), m.wrap(lipgloss.NewStyle(), s.Key.Doc))

	type level struct{ name, value string }
	def := s.Key.Default
	if s.By == nil && s.Value != def {
		def += " (" + s.Value + ")"
	}
	levels := []level{{"default", def}}
	applies := 0
	for _, l := range s.Layers {
		name, value := l.At.String(), "·"
		if l.Path != s.Path {
			name += " (" + l.Path[:strings.LastIndex(l.Path, ".")] + ")"
		}
		if l.Set {
			value, applies = plain(l.Value), len(levels)
		}
		levels = append(levels, level{name, value})
	}
	nameW, valW := 0, 0
	for _, l := range levels {
		nameW, valW = max(nameW, ansi.StringWidth(l.name)), max(valW, ansi.StringWidth(l.value))
	}
	valW = min(valW, max(m.width-nameW-18, 10))
	for i, l := range levels {
		line := fmt.Sprintf("   %-*s  %-*s", nameW, l.name, valW, ansi.Truncate(l.value, valW, "…"))
		if i == applies {
			out = append(out, good.Render(line+"  ◀ applies"))
		} else {
			out = append(out, dim.Render(strings.TrimRight(line, " ")))
		}
	}

	k := s.Key
	fileKey := "File key: " + s.FileKey(s.Levels()[len(s.Levels())-1]) + " · " + string(k.Type)
	if len(k.Values) > 0 {
		fileKey += " · " + strings.Join(k.Values, ", ")
	}
	return stack(out, m.wrap(dim, fileKey), dim.Render(" Applies after: "+k.Restart))
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
		out = append(out, " Set "+m.edit.Label+" at "+at+": "+strings.Join(items, " "))
	case modeInput:
		hint := ""
		if m.edit.Key.Type == config.TypeList {
			hint = dim.Render("  items with commas between them")
		}
		out = append(out, " Set "+m.edit.Label+" at "+at+":"+hint, " > "+m.input.View())
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

func (m Model) footer() []string {
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
		keys = "enter edit · l set at level · x remove · ← group · / filter · q quit"
		if r, ok := m.selectedRow(); ok && r.set == nil {
			keys = "→ or enter open · ← close · / filter · q quit"
		}
	}
	return m.wrap(dim, keys)
}
