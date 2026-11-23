package board

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/github"
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
		case !m.detail:
			return m.strip()
		case m.status != "":
			s = m.message()
		default:
			s = hintBar(m.width, m.boardHints())
		}
	}
	return ansi.Truncate(s, m.width, "…")
}

// message is the Board message, in the bad style when it is an error.
func (m Model) message() string {
	if m.statusE {
		return bad.Render(" " + m.status)
	}
	return good.Render(" " + m.status)
}

// strip is the footer under the rows: a rule, the title and the facts of the
// selection, the Board message while it is set, and the key hints. The title
// and the facts wrap, so that they show in full.
func (m Model) strip() string {
	w := max(m.width-1, 1)
	lines := []string{dim.Render(" " + strings.Repeat("─", w))}
	wrap := func(s string, style lipgloss.Style) {
		if s == "" {
			return
		}
		for _, l := range strings.Split(ansi.Wrap(s, w, ""), "\n") {
			lines = append(lines, style.Render(" "+l))
		}
	}
	title, facts := m.selection()
	wrap(title, bold)
	wrap(facts, lipgloss.NewStyle())
	if m.status != "" {
		lines = append(lines, ansi.Truncate(m.message(), m.width, "…"))
	}
	lines = append(lines, ansi.Truncate(hintBar(m.width, m.boardHints()), m.width, "…"))
	return strings.Join(lines, "\n")
}

// selection is the title and the facts that the strip shows for the selected
// row or heading. Both are empty when nothing is selected.
func (m Model) selection() (title, facts string) {
	join := func(fs ...string) string {
		return strings.Join(slices.DeleteFunc(fs, func(f string) bool { return f == "" }), " · ")
	}
	if g, ok := headingName(m.sel); ok {
		n := len(m.groupIDs(g))
		if n == 1 {
			return g, "1 row"
		}
		return g, fmt.Sprintf("%d rows", n)
	}
	if r, ok := m.currentIssue(); ok {
		return r.Title, join(boardID(r.ID), r.Group, r.Repo, r.note())
	}
	t, ok := m.current()
	if !ok {
		return "", ""
	}
	pr := ""
	if n := fmt.Sprintf("#%d", t.PRNumber); t.PRNumber != 0 && !strings.HasPrefix(t.Note, n) {
		pr = n
	}
	return t.Title, join(boardID(t.ID), t.Stage, t.Repo, pr, t.Note)
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
	if t.StageKey == lifecycle.Done.Key() && !t.Tab {
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

// column is one column of a row. A drop of 0 never drops. A narrow row drops
// the lowest rank first.
type column struct {
	drop  int
	task  func(Model, scheduler.TaskView) string
	issue func(issueRow) string
}

const (
	colID = iota
	colTitle
	colStatus
	colTail
	colRepo
	colSkill
	numCols
)

var columns = [numCols]column{
	colID:     {task: func(_ Model, t scheduler.TaskView) string { return boardID(t.ID) }, issue: func(r issueRow) string { return boardID(r.ID) }},
	colTitle:  {task: func(_ Model, t scheduler.TaskView) string { return t.Title }, issue: func(r issueRow) string { return r.Title }},
	colStatus: {task: func(_ Model, t scheduler.TaskView) string { return taskStatus(t) }, issue: issueRow.status},
	colTail:   {drop: 3, task: func(m Model, t scheduler.TaskView) string { return m.age(t) }, issue: issueRow.priorityText},
	colRepo:   {drop: 2, task: func(_ Model, t scheduler.TaskView) string { return t.Repo }, issue: func(r issueRow) string { return r.Repo }},
	colSkill:  {drop: 1, task: func(_ Model, t scheduler.TaskView) string { return t.Skill[strings.LastIndex(t.Skill, ":")+1:] }, issue: func(issueRow) string { return "" }},
}

const (
	// titleWant is the title width that the optional columns must leave before
	// they show, and titleMin the width that a narrow row cuts the title to.
	titleWant = 25
	titleMin  = 12
)

// stageWord is the status word of a Task row, by its Stage key.
var stageWord = map[string]string{
	lifecycle.Queued.Key():      "queued",
	lifecycle.Working.Key():     "working",
	lifecycle.CIRunning.Key():   "CI running",
	lifecycle.CIDebug.Key():     "CI debug",
	lifecycle.InReview.Key():    "in review",
	lifecycle.Responding.Key():  "responding",
	lifecycle.ReadyToLand.Key(): "ready",
	lifecycle.Done.Key():        "done",
	lifecycle.Reviewing.Key():   "reviewing",
	lifecycle.ReviewReady.Key(): "review ready",
}

// flagWord is the status word of a flag whose Reason does not read as one.
// Any other flag shows its Reason.
var flagWord = map[string]string{
	string(lifecycle.ReasonPRClosed):    "PR closed",
	string(lifecycle.ReasonNoBotReview): "bot late",
}

// taskStatus is the status word of a Task row: the flag of a Task in Needs
// you, and the Stage of any other Task.
func taskStatus(t scheduler.TaskView) string {
	if inNeedsYou(t) && t.Flag != "" {
		return cmp.Or(flagWord[t.Flag], t.Flag)
	}
	return cmp.Or(stageWord[t.StageKey], t.Stage)
}

// boardID is the ID that the ID column shows. A GitHub ID drops its owner,
// because the Repo column names the repository.
func boardID(id string) string {
	if _, _, err := github.ParseIssueID(id); err != nil {
		return id
	}
	_, short, _ := strings.Cut(id, "/")
	return short
}

// cellRow is a Task or Issue row before it is laid out. An alert row shows
// its status in the bad style.
type cellRow struct {
	id           string
	cells        [numCols]string
	faded, alert bool
}

// columnWidths fits the columns to the rows on screen. It drops columns until
// the title gets titleWant cells, and the title then takes the rest. A narrow
// row cuts the title down to titleMin.
func columnWidths(w int, rows []cellRow) [numCols]int {
	var ws [numCols]int
	for _, r := range rows {
		for i, c := range r.cells {
			ws[i] = max(ws[i], lipgloss.Width(c))
		}
	}
	// The marker takes 3 cells, and 2 cells sit between columns.
	share := func() int {
		n, rest := 0, 0
		for i, cw := range ws {
			if cw > 0 {
				n++
				if i != colTitle {
					rest += cw
				}
			}
		}
		return w - 3 - rest - 2*(n-1)
	}
	for rank := 1; rank < numCols && share() < titleWant; rank++ {
		for i, c := range columns {
			if c.drop == rank {
				ws[i] = 0
			}
		}
	}
	ws[colTitle] = max(share(), titleMin)
	return ws
}

func (m Model) rowLines(w int) []rowLine {
	type group struct {
		name string
		rows []cellRow
	}
	var groups []group
	var onScreen []cellRow
	for _, g := range m.lay.order {
		if !m.shown(g) {
			continue
		}
		var rows []cellRow
		for _, t := range m.tasks {
			if t.Group == g {
				r := cellRow{id: t.ID, faded: g == groupDone || g == "Queued", alert: inNeedsYou(t)}
				for i, c := range columns {
					r.cells[i] = c.task(m, t)
				}
				rows = append(rows, r)
			}
		}
		for _, is := range m.issues {
			if is.Group == g {
				r := cellRow{id: is.ID, faded: g == groupBacklog || len(is.Blockers) > 0, alert: len(is.Blockers) > 0}
				for i, c := range columns {
					r.cells[i] = c.issue(is)
				}
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		groups = append(groups, group{g, rows})
		if m.open(g) {
			onScreen = append(onScreen, rows...)
		}
	}
	ws := columnWidths(w, onScreen)
	// Each cell gets its own style, because the reset of an inner style ends an
	// outer one.
	row := func(r cellRow) string {
		style, marker := lipgloss.NewStyle(), "  "
		switch {
		case r.id == m.sel:
			style, marker = selRow, "▸ "
		case r.faded:
			style = dim
		}
		var parts []string
		for i, c := range r.cells {
			if ws[i] == 0 {
				continue
			}
			cs := style
			if i == colStatus && r.alert {
				cs = bad
			}
			parts = append(parts, cs.Render(pad(c, ws[i])))
		}
		return ansi.Truncate(style.Render(" "+marker)+strings.Join(parts, style.Render("  ")), w, "…")
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
	for _, g := range groups {
		out = append(out, rowLine{headingKey(g.name), heading(g.name, len(g.rows))})
		if !m.open(g.name) {
			continue
		}
		for _, r := range g.rows {
			out = append(out, rowLine{r.id, row(r)})
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
	if t.StageKey == lifecycle.Done.Key() && t.Since.Local().YearDay() == now.Local().YearDay() && d < 24*time.Hour {
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
