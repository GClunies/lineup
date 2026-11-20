package board

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

var (
	teal    = lipgloss.Color("#5fb3ad")
	pink    = lipgloss.Color("#f3a3b5")
	gray    = lipgloss.Color("#9aa3ad")
	dim     = lipgloss.NewStyle().Foreground(gray)
	bold    = lipgloss.NewStyle().Bold(true)
	good    = lipgloss.NewStyle().Foreground(teal)
	bad     = lipgloss.NewStyle().Foreground(pink)
	selRow  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e3f1ef"))
	headRow = lipgloss.NewStyle().Foreground(teal).Bold(true)
)

func (m Model) View() string {
	if m.mode == modeHelp {
		return boardHelp
	}
	foot := m.footer()
	h := m.bodyHeight()
	var top []string
	var body string
	switch {
	case len(m.menu.items) > 0:
		body = m.menuView()
	case m.mode == modeDests || m.mode == modePromote:
		body = m.destList()
	case m.mode == modeSkill:
		body = m.skillPicker()
	case m.mode == modeRepo:
		body = m.repoPicker()
	case m.mode == modeStatus:
		body = m.statusPicker()
	case m.detail:
		body = m.vp.View()
	default:
		if l := m.filterLine(); l != "" {
			top, h = []string{l}, max(h-1, 1)
		}
		body = m.taskBody(h)
	}
	lines := strings.Split(body, "\n")
	if len(lines) > h && !m.detail {
		lines = m.window(lines, h)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return m.header() + "\n" + dim.Render(strings.Repeat("─", max(m.width, 1))) + "\n" +
		strings.Join(append(top, lines[:h]...), "\n") + "\n" + foot
}

// bodyHeight is the number of lines between the header rule and the footer.
func (m Model) bodyHeight() int { return max(m.height-2-lipgloss.Height(m.footer()), 1) }

// window keeps the selected row on screen.
func (m Model) window(lines []string, h int) []string {
	start := windowStart(lines, h)
	return lines[start : start+h]
}

// windowStart is the first of the h lines that window keeps.
func windowStart(lines []string, h int) int {
	at := 0
	for i, l := range lines {
		if strings.Contains(l, "▸") {
			at = i
		}
	}
	return min(max(at-h/2, 0), len(lines)-h)
}

func (m Model) header() string {
	s := " " + bold.Render("lineup") + fmt.Sprintf("  WIP %d/%d", m.board.Started, m.board.Cap)
	if m.board.OverCap > 0 {
		s += bad.Render(fmt.Sprintf(" +%d over", m.board.OverCap))
	}
	s += fmt.Sprintf("   ●%d need you", needsYou(m.board.OfKind(lifecycle.KindBuild).Tasks))
	if n := needsYou(m.board.OfKind(lifecycle.KindReview).Tasks); n > 0 {
		s += fmt.Sprintf(" (+%d in reviews)", n)
	}
	if m.board.ReviewsPaused {
		s += "   " + bad.Render("reviews paused")
	}
	if !m.online {
		s += "   " + bad.Render("scheduler offline, showing cache: run lineup restart")
	}
	return ansi.Truncate(s, m.width, "…")
}

func needsYou(tasks []scheduler.TaskView) int {
	n := 0
	for _, t := range tasks {
		if inNeedsYou(t) {
			n++
		}
	}
	return n
}

func inNeedsYou(t scheduler.TaskView) bool { return t.Group == lifecycle.GroupNeedsYou.String() }

// footer is the mode's footer. A config problem adds its own line above it.
func (m Model) footer() string {
	f := m.modeFooter()
	if m.opts.ConfigErr == "" {
		return f
	}
	return ansi.Truncate(bad.Render(" "+m.opts.ConfigErr), m.width, "…") + "\n" + f
}

func (m Model) modeFooter() string {
	var s string
	switch m.mode {
	case modeBox:
		lines := strings.Split(m.boxFooter(), "\n")
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, m.width, "…")
		}
		return strings.Join(lines, "\n")
	case modeDests:
		s = dim.Render(" type to filter  up/down move  enter pick  esc back")
	case modeSkill:
		s = dim.Render(" type a skill  up/down move  enter start  esc cancel")
	case modeRepo:
		s = dim.Render(" type to filter  up/down move  enter pick  esc cancel")
	case modeStatus:
		s = dim.Render(" j/k move  enter set the status  esc cancel")
	case modeFilter:
		s = dim.Render(" type to filter by ID and title  up/down move  enter keep  esc clear")
	case modePromote:
		s = " promote " + bold.Render(m.box.promote) + " to Linear" + dim.Render("   type to filter  up/down move  enter promote  esc cancel")
	case modeConfirm:
		s = bad.Render(" " + m.pendingAsk + "? (y/n)")
	case modeCap:
		s = bad.Render(fmt.Sprintf(" wip cap full: start once over the cap (o) or raise the cap to %d (c)?", m.board.Cap+1))
	case modeNormal:
		switch {
		case len(m.menu.items) > 0:
			s = dim.Render(" tap an action, or j/k and enter   esc back")
		case m.status != "" && m.statusE:
			s = bad.Render(" " + m.status)
		case m.status != "":
			s = good.Render(" " + m.status)
		default:
			s = hintBar(m.width, m.boardHints())
		}
	}
	return ansi.Truncate(s, m.width, "…")
}

type hint struct{ key, does string }

// hintBar shows the keys that work now. It drops hints from the end to fit
// the width, and it always keeps the last hint, "? all keys".
func hintBar(width int, hints []hint) string {
	render := func(h hint) string { return good.Render(h.key) + dim.Render(" "+h.does) }
	last := render(hints[len(hints)-1])
	s := " "
	for _, h := range hints[:len(hints)-1] {
		next := s + render(h) + "   "
		if lipgloss.Width(next+last) > width {
			break
		}
		s = next
	}
	return s + last
}

// boardHints lists the keys for the selected Task, most useful first.
func (m Model) boardHints() []hint {
	tail := []hint{{"z", "agent"}, {"R", "refresh now"}, {"q", "quit"}, {"?", "all keys"}}
	if m.query() != "" {
		tail = append([]hint{{"esc", "clear the filter"}, {"n", "new Issue"}}, tail...)
	} else {
		tail = append([]hint{{"n", "new Issue"}, {"/", "filter"}}, tail...)
	}
	if m.detail {
		return []hint{{"f", "back"}, {"j/k", "scroll"}, {"?", "all keys"}}
	}
	hs := m.rowActions()
	if len(hs) == 0 {
		return tail
	}
	return append(append(hs, hint{"j/k", "move"}), tail...)
}

// rowActions lists the keys that act on the selected row, most useful first.
// The footer hints and the menu that a tap opens both show them.
func (m Model) rowActions() []hint {
	if g, ok := headingName(m.sel); ok {
		verb := "close"
		if !m.open(g) {
			verb = "open"
		}
		return []hint{{"enter", verb}}
	}
	if r, ok := m.currentIssue(); ok {
		return slices.Clone(issueKinds[r.Kind].keys)
	}
	t, ok := m.current()
	if !ok {
		return nil
	}
	var hs []hint
	if t.StageKey == lifecycle.ReadyToLand.Key() {
		hs = append(hs, hint{"m", "merge"})
	}
	if t.Group == lifecycle.GroupQueued.String() {
		hs = append(hs, hint{"s", "start"})
	}
	switch {
	case t.Live:
		hs = append(hs, hint{"enter", "go to the Worker"}, hint{"p", "preview"})
	case restorable(t):
		hs = append(hs, hint{"enter", "restore the Worker"})
	}
	switch {
	case t.PRURL != "":
		hs = append(hs, hint{"o", "open the PR"})
	case t.IssueURL != "":
		hs = append(hs, hint{"o", "open the Issue"})
	}
	hs = append(hs, hint{"f", "details"})
	if t.Group == lifecycle.GroupDone.String() && !t.Tab {
		return append(hs, hint{"x", "archive"})
	}
	return append(hs, hint{"x", "close"})
}

// taskBody is the Task rows, and the Worker's screen beside them while the
// preview is on. h is the height of the body.
func (m Model) taskBody(h int) string {
	if !m.preview {
		return m.rows(m.width)
	}
	lw := m.width * 55 / 100
	return lipgloss.JoinHorizontal(lipgloss.Top, m.rows(lw), m.previewPane(m.width-lw, h))
}

func (m Model) previewPane(w, h int) string {
	lines := strings.Split(strings.TrimRight(m.previewTxt, "\n"), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, max(w-3, 1), "")
	}
	return lipgloss.NewStyle().Width(max(w-1, 1)).Height(h).BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).BorderForeground(teal).
		Render(strings.Join(lines, "\n"))
}

func (m Model) rows(w int) string {
	var texts []string
	for _, l := range m.rowLines(w) {
		texts = append(texts, l.text)
	}
	return strings.Join(texts, "\n")
}

// rowLine is one line of the Task rows: a heading, a row, or a note. id is
// the heading's key or the row's ID, and empty for a note.
type rowLine struct{ id, text string }

// column is one column of a row. A max of 0 is no cap, and a drop of 0 never
// drops. A narrow row drops the lowest rank first.
type column struct {
	min, max int
	drop     int
	task     func(Model, scheduler.TaskView) string
	issue    func(issueRow) string
}

const (
	colID = iota
	colTitle
	colNote
	colTail
	colRepo
	colSkill
	numCols
)

var columns = [numCols]column{
	colID:    {task: func(_ Model, t scheduler.TaskView) string { return t.ID }, issue: func(r issueRow) string { return r.ID }},
	colTitle: {min: 12, task: func(_ Model, t scheduler.TaskView) string { return t.Title }, issue: func(r issueRow) string { return r.Title }},
	colNote:  {min: 12, max: 48, task: func(_ Model, t scheduler.TaskView) string { return t.Note }, issue: issueRow.note},
	colTail:  {drop: 3, task: func(m Model, t scheduler.TaskView) string { return m.age(t) }, issue: issueRow.priorityText},
	colRepo:  {drop: 2, task: func(_ Model, t scheduler.TaskView) string { return t.Repo }, issue: func(r issueRow) string { return r.Repo }},
	colSkill: {drop: 1, task: func(_ Model, t scheduler.TaskView) string { return t.Skill[strings.LastIndex(t.Skill, ":")+1:] }, issue: func(issueRow) string { return "" }},
}

const (
	// titleWant is the title width that the optional columns must leave before
	// they show.
	titleWant = 25
	// noteKeep is the note width that a narrow row keeps, enough for
	// "#25867 ✓ approved · ready to land".
	noteKeep = 33
)

// columnWidths drops columns until the title gets titleWant cells beside the
// note, or beside noteKeep cells of it when the note is longer. The note then
// takes what the title leaves, but never less than noteKeep. A narrow row cuts the
// title down to its minimum first.
func (m Model) columnWidths(w int) [numCols]int {
	var ws [numCols]int
	for _, t := range m.tasks {
		for i, c := range columns {
			ws[i] = max(ws[i], lipgloss.Width(c.task(m, t)))
		}
	}
	for _, r := range m.issues {
		for i, c := range columns {
			ws[i] = max(ws[i], lipgloss.Width(c.issue(r)))
		}
	}
	for i, c := range columns {
		if ws[i] > 0 {
			ws[i] = max(ws[i], c.min)
			if c.max > 0 {
				ws[i] = min(ws[i], c.max)
			}
		}
	}
	// The marker takes 3 cells, and 2 cells sit between columns.
	share := func() int {
		n, rest := 0, 0
		for i, cw := range ws {
			if cw > 0 {
				n++
				if i != colTitle && i != colNote {
					rest += cw
				}
			}
		}
		return w - 3 - rest - 2*(n-1)
	}
	for rank := 1; rank < numCols && share() < titleWant+min(ws[colNote], noteKeep); rank++ {
		for i, c := range columns {
			if c.drop == rank {
				ws[i] = 0
			}
		}
	}
	if ws[colNote] > 0 {
		ws[colNote] = min(ws[colNote], max(share()-titleWant, min(share()-columns[colTitle].min, noteKeep), columns[colNote].min))
	}
	ws[colTitle] = max(share()-ws[colNote], columns[colTitle].min)
	return ws
}

func (m Model) rowLines(w int) []rowLine {
	ws := m.columnWidths(w)
	row := func(id string, cells [numCols]string, faded bool) string {
		marker := "  "
		if id == m.sel {
			marker = "▸ "
		}
		var parts []string
		for i, c := range cells {
			if ws[i] > 0 {
				parts = append(parts, pad(c, ws[i]))
			}
		}
		s := ansi.Truncate(" "+marker+strings.Join(parts, "  "), w, "…")
		switch {
		case id == m.sel:
			return selRow.Render(s)
		case faded:
			return dim.Render(s)
		}
		return s
	}
	// A heading shows the group's count, and a closed group's mark.
	heading := func(g string, n int) string {
		s := fmt.Sprintf(" %s  %d", strings.ToUpper(g), n)
		if !m.open(g) {
			s += " …"
		}
		if m.sel == headingKey(g) {
			return selRow.Render(ansi.Truncate("▸"+s[1:], w, "…"))
		}
		return headRow.Render(ansi.Truncate(s, w, "…"))
	}
	var out []rowLine
	for _, g := range m.lay.order {
		if !m.shown(g) {
			continue
		}
		var lines []rowLine
		for _, t := range m.tasks {
			if t.Group == g {
				var cells [numCols]string
				for i, c := range columns {
					cells[i] = c.task(m, t)
				}
				lines = append(lines, rowLine{t.ID, row(t.ID, cells, g == groupDone || g == "Queued")})
			}
		}
		for _, r := range m.issues {
			if r.Group == g {
				var cells [numCols]string
				for i, c := range columns {
					cells[i] = c.issue(r)
				}
				lines = append(lines, rowLine{r.ID, row(r.ID, cells, g == groupBacklog || len(r.Blockers) > 0)})
			}
		}
		if len(lines) == 0 {
			continue
		}
		out = append(out, rowLine{headingKey(g), heading(g, len(lines))})
		if m.open(g) {
			out = append(out, lines...)
		}
	}
	switch {
	case len(m.tasks)+len(m.issues) > 0:
	case m.query() != "":
		out = append(out, rowLine{"", dim.Render(" no row matches the filter")})
	default:
		out = append(out, rowLine{"", dim.Render(" no Tasks")})
	}
	return out
}

// row is one line of a picker or a list, with the marker when it is selected.
func (m Model) row(sel bool, text string) string {
	if !sel {
		return ansi.Truncate("   "+text, m.width, "…")
	}
	return selRow.Render(ansi.Truncate(" ▸ "+text, m.width, "…"))
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

func (m Model) age(t scheduler.TaskView) string {
	if t.Since.IsZero() || t.Group == "Queued" {
		return ""
	}
	now := m.opts.Now()
	d := now.Sub(t.Since)
	if t.Group == "Done" && t.Since.Local().YearDay() == now.Local().YearDay() && d < 24*time.Hour {
		return "today"
	}
	return ago(d)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

const boardHelp = `
 lineup board keys

   j k      move the selection
   enter    focus the Task's tab in Rex, or restore its closed Worker
            on a group heading: open or close the group
   d        show or hide Done
   p        live preview of the Worker's screen
   f        detail pane at full screen, and back; on an Issue row,
            the Issue's description
   m        merge the PR            (Ready to land)
   x        close the Task, or its tab and worktree when it is Done,
            or archive a Done Task with no tab, after y/n
   s        start a Queued Task, or start a Task from a Ready or
            Backlog Issue; pick or type a skill, or just go
   c        change the status of an Issue row: pick a Linear state of
            its team, or a local status
   o        open the PR or the Issue; a local Issue opens in $EDITOR
   P        promote a local Issue: pick a Linear team or Project; lineup
            creates the Issue there and cancels the local one
   n        capture a new Issue in the Local Tracker; tab picks a
            Linear team or Project instead
   /        filter the rows by ID and title; enter keeps the filter,
            esc clears it
   R        refresh now: poll GitHub for every started Task
   z        zoom the agent pane
   ?        this help      q  quit

   tap a row       a menu of its actions; tap one to run it
   tap a heading   open or close the group
   wheel           move the selection

 press any key to go back
`
