package board

import (
	"fmt"
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
	colHead = lipgloss.NewStyle().Foreground(gray).Bold(true)
)

var groupMark = map[string]string{"Needs you": "●", "Working": "◐", "Waiting": "◷"}

func (m Model) View() string {
	if m.mode == modeHelp {
		switch m.view {
		case viewTracker:
			return trackerHelp
		case viewReview:
			if m.rv.curTab().Tasks {
				return reviewTasksHelp
			}
			return reviewHelp
		}
		return boardHelp
	}
	var body string
	switch {
	case m.view == viewTracker:
		body = m.trackerBody()
	case m.view == viewReview:
		body = m.reviewBody()
	case m.detail:
		body = m.vp.View()
	default:
		body = m.taskBody(max(m.height-3, 1))
	}
	lines := strings.Split(body, "\n")
	h := max(m.height-3, 1)
	if len(lines) > h && !m.detail && !(m.view == viewTracker && m.tr.lvl == lvlDetail) {
		lines = m.window(lines, h)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return m.header() + "\n" + dim.Render(strings.Repeat("─", max(m.width, 1))) + "\n" +
		strings.Join(lines[:h], "\n") + "\n" + m.footer()
}

// window keeps the selected row on screen.
func (m Model) window(lines []string, h int) []string {
	at := 0
	for i, l := range lines {
		if strings.Contains(l, "▸") {
			at = i
		}
	}
	start := min(max(at-h/2, 0), len(lines)-h)
	return lines[start : start+h]
}

func (m Model) header() string {
	count := map[string]int{}
	for _, t := range m.tasks {
		count[t.Group]++
	}
	s := " " + bold.Render("lineup") + "  " + m.viewNames()
	if m.kind == lifecycle.KindBuild {
		s += fmt.Sprintf("  WIP %d/%d", m.board.Started, m.board.Cap)
		if m.board.OverCap > 0 {
			s += bad.Render(fmt.Sprintf(" +%d over", m.board.OverCap))
		}
	}
	s += fmt.Sprintf("   ●%d need you   ◐%d working   ◷%d waiting   %d queued",
		count["Needs you"], count["Working"], count["Waiting"], count["Queued"])
	if m.kind == lifecycle.KindReview && m.board.ReviewsPaused {
		s += "   " + bad.Render("reviews paused")
	}
	if !m.online {
		s += "   " + bad.Render("scheduler offline, showing cache: run lineup restart")
	}
	return ansi.Truncate(s, m.width, "…")
}

// viewNames lists the three views with the one on screen bright.
func (m Model) viewNames() string {
	names := [...]struct {
		id   viewID
		name string
	}{{viewTracker, "1 plan"}, {viewBoard, "2 work"}, {viewReview, "3 review"}}
	var parts []string
	for _, n := range names {
		if n.id == m.view {
			parts = append(parts, headRow.Render(n.name))
		} else {
			parts = append(parts, dim.Render(n.name))
		}
	}
	return strings.Join(parts, "  ")
}

func (m Model) footer() string {
	var s string
	if m.view == viewTracker && m.mode == modeNormal {
		return ansi.Truncate(m.trackerFooter(), m.width, "…")
	}
	if m.view == viewReview && m.mode == modeNormal {
		return ansi.Truncate(m.reviewFooter(), m.width, "…")
	}
	switch m.mode {
	case modeConfirm:
		s = bad.Render(" " + m.pendingOp + " " + m.pendingTask + "? (y/n)")
	case modeCap:
		s = bad.Render(fmt.Sprintf(" wip cap full: start once over the cap (o) or raise the cap to %d (c)?", m.board.Cap+1))
	case modeNormal:
		switch {
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
	if m.view == viewReview {
		tail = append([]hint{{"tab", "next view"}, {"< >", "move the tab"}}, tail...)
	}
	if m.detail {
		return []hint{{"f", "back"}, {"j/k", "scroll"}, {"?", "all keys"}}
	}
	t, ok := m.current()
	if !ok && m.kind == lifecycle.KindReview {
		return append([]hint{{"tab", "find a PR to review"}}, tail...)
	}
	if !ok {
		return append([]hint{{"1", "plan: start an Issue"}}, tail...)
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
	case reopens(t):
		hs = append(hs, hint{"enter", "reopen the Worker"})
	}
	switch {
	case t.PRURL != "":
		hs = append(hs, hint{"o", "open the PR"})
	case t.IssueURL != "":
		hs = append(hs, hint{"o", "open the Issue"})
	}
	hs = append(hs, hint{"f", "details"})
	if t.Group == lifecycle.GroupDone.String() {
		hs = append(hs, hint{"x", "archive"})
	} else {
		hs = append(hs, hint{"x", "close"})
	}
	hs = append(hs, hint{"j/k", "move"})
	return append(hs, tail...)
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
	idW, stageW, repoW, noteW := 4, 5, 4, 0
	for _, t := range m.tasks {
		idW = max(idW, lipgloss.Width(t.ID))
		stageW = max(stageW, lipgloss.Width(stageText(t)))
		repoW = max(repoW, lipgloss.Width(t.Repo))
		noteW = max(noteW, min(lipgloss.Width(t.Note), 36))
	}
	// marker 2, gaps 2 each between five columns, age 5
	titleW := max(w-(2+idW+stageW+repoW+noteW+5+14), 12)
	var b strings.Builder
	last := ""
	for _, t := range m.tasks {
		if t.Group != last {
			last = t.Group
			b.WriteString(headRow.Render(" "+strings.ToUpper(t.Group)) + "\n")
		}
		marker := "  "
		if t.ID == m.sel {
			marker = "▸ "
		}
		row := fmt.Sprintf(" %s%s  %s  %s  %s  %s  %s",
			marker, pad(t.ID, idW), pad(stageText(t), stageW), pad(t.Repo, repoW),
			pad(t.Title, titleW), pad(t.Note, noteW), m.age(t))
		row = ansi.Truncate(row, w, "…")
		switch {
		case t.ID == m.sel:
			row = selRow.Render(row)
		case t.Group == "Done" || t.Group == "Queued":
			row = dim.Render(row)
		}
		b.WriteString(row + "\n")
	}
	if len(m.tasks) == 0 {
		b.WriteString(dim.Render(" no Tasks") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func stageText(t scheduler.TaskView) string {
	if t.Group == "Queued" {
		return ""
	}
	if t.Group == "Done" && t.Outcome != "" {
		return t.Outcome
	}
	s := t.Stage
	if t.Flag != "" {
		s += " ⚑"
	}
	if t.Skill != "" {
		s += " · " + t.Skill[strings.LastIndex(t.Skill, ":")+1:]
	}
	return s
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
   enter    focus the Task's tab in Rex
   p        live preview of the Worker's screen
   f        detail pane at full screen, and back
   m        merge the PR            (Ready to land)
   x        close the Task, or archive a Done Task, after y/n
   s        start a Queued Task
   o        open the PR or the Issue
   R        refresh now: poll GitHub for every started Task
   z        zoom the agent pane
   1 2 3    show the plan, work, or review view
   ?        this help      q  quit

 press any key to go back
`

const trackerHelp = `
 Linear browser keys

   The tabs are your favorite Linear views, then Browse. Star a view in
   Linear to add a tab. Browse is a tree: workspace › team › Project › Issues.
   enter goes down one level. esc goes back up, but never above a view.
   To change the Project, press esc on the Issue list.

   tab      next tab        shift+tab  previous tab
   < >      move this view's tab left or right; lineup keeps the order
   j k      move the selection
   enter    open the row
   esc      go back up one level
   /        fuzzy search in this list
   s        start the Issue as a Task; pick or type a skill, or just go
   c        change the status
   A        assign the Issue to you
   n        add an Issue to this Project or team view
   o        open the Issue in the browser
   R        refresh what is on screen now
   z        zoom the agent pane
   1 2 3    show the plan, work, or review view
   ?        this help      q  quit

 press any key to go back
`
