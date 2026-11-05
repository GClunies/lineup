package board

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/linear"
)

func (m Model) trackerBody() string {
	t := m.tr
	switch {
	case t.wssLoaded && len(t.wss) == 0 && len(t.sources) == 0:
		return dim.Render(" Sign in with `lineup login`")
	case t.viewsWait > 0 && len(t.wss) > 0:
		return dim.Render(" loading your Linear views…")
	case t.viewsWait > 0:
		return dim.Render(" loading…")
	case t.ov == ovStatus:
		return m.statusPicker()
	case t.ov == ovRepo:
		return m.repoPicker()
	case t.ov == ovSkill:
		return m.skillPicker()
	case t.lvl == lvlDetail:
		return t.vp.View()
	}
	var lines []string
	// A lone Source tab still shows its name, as the list has no crumbs.
	if len(t.tabs) > 1 || t.curSource() != nil {
		names := make([]string, len(t.tabs))
		for i, tb := range t.tabs {
			names[i] = tb.name()
		}
		lines = append(lines, m.tabStrip(names, t.tab))
	}
	if tb := t.curTab(); tb.browse() || t.lvl != tb.home() {
		lines = append(lines, m.crumbs())
	}
	lines[len(lines)-1] += m.listState()
	vis := t.visible()
	switch {
	case t.curSource() != nil:
		lines = append(lines, m.itemRows(vis)...)
	case t.lvl == lvlIssues:
		lines = append(lines, m.issueRows(vis)...)
	case t.lvl == lvlProjects:
		lines = append(lines, m.projectRows(vis)...)
	default:
		labels := t.labels()
		for i, v := range vis {
			lines = append(lines, m.row(i == t.cur, labels[v], false))
		}
	}
	if len(vis) == 0 {
		switch {
		case !t.wssLoaded, !t.loaded():
			lines = append(lines, dim.Render(" loading…"))
		case t.err != "":
		case t.refreshing > 0 || t.lvl == lvlTeams || t.lvl == lvlProjects:
			lines = append(lines, dim.Render(" nothing here yet"))
		default:
			lines = append(lines, dim.Render(" no Issues"))
		}
	}
	if t.err != "" {
		lines = append(lines, bad.Render(" "+t.err))
	}
	return strings.Join(lines, "\n")
}

// tabStrip names the tabs, with the open tab in brackets. When the row is
// too wide, it drops tabs from the left until the open tab fits.
func (m Model) tabStrip(names []string, cur int) string {
	segs := make([]string, len(names))
	for i, name := range names {
		if i == cur {
			segs[i] = headRow.Render("["+name+"]") + " "
		} else {
			segs[i] = dim.Render(" "+name+" ") + " "
		}
	}
	start := 0
	for start < cur && ansi.StringWidth(" "+strings.Join(segs[start:cur+1], "")) > m.width {
		start++
	}
	lead := " "
	if start > 0 {
		lead = "…"
	}
	return ansi.Truncate(lead+strings.Join(segs[start:], ""), m.width, "…")
}

func (m Model) crumbs() string {
	t := m.tr
	parts := []string{"Linear"}
	switch v := t.curTab().view; {
	case v != nil:
		parts = append(parts, v.Name)
		if t.lvl == lvlIssues && t.proj.ID != "" {
			parts = append(parts, t.proj.Name)
		}
	case t.my:
		parts = append(parts, "My Issues")
	case t.lvl > lvlRoot:
		parts = append(parts, t.curWS().Name)
		if t.lvl > lvlTeams {
			parts = append(parts, t.team.Key)
		}
		if t.lvl > lvlProjects {
			parts = append(parts, t.proj.Name)
		}
	}
	return " " + headRow.Render(strings.Join(parts, " › "))
}

// listState says that the list is refreshing, and shows the search.
func (m Model) listState() string {
	t := m.tr
	var s string
	if t.refreshing > 0 && t.lvl == lvlIssues {
		s += dim.Render("  refreshing…")
	}
	if t.filtering || t.filter.Value() != "" {
		s += "  " + t.filter.View()
	}
	return s
}

func (m Model) row(sel bool, text string, faded bool) string {
	marker := "  "
	if sel {
		marker = "▸ "
	}
	row := ansi.Truncate(" "+marker+text, m.width, "…")
	switch {
	case sel:
		return selRow.Render(row)
	case faded:
		return dim.Render(row)
	}
	return row
}

// seg is a piece of a row with its own style.
type seg struct {
	text  string
	style lipgloss.Style
}

func segsWidth(ss []seg) int {
	w := 0
	for _, s := range ss {
		w += lipgloss.Width(s.text)
	}
	return w
}

// richRow renders a row from segments. A selected or faded row lends its
// style to each segment, which keeps its own color.
func (m Model) richRow(sel, faded bool, ss []seg) string {
	base, marker := lipgloss.NewStyle(), "  "
	switch {
	case sel:
		base, marker = selRow, "▸ "
	case faded:
		base = dim
	}
	out := base.Render(" " + marker)
	for _, s := range ss {
		out += s.style.Inherit(base).Render(s.text)
	}
	return ansi.Truncate(out, m.width, "…")
}

var (
	plain  = lipgloss.NewStyle()
	orange = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2994a"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2c94c"))
	purple = lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa"))
)

// priorityGlyph draws Linear's priority in three cells, by priority number.
var priorityGlyph = map[int]seg{
	0: {"···", dim},
	1: {" ! ", orange},
	2: {"▂▄▆", plain},
	3: {"▂▄ ", plain},
	4: {"▂  ", plain},
}

func priority(p int) seg {
	if g, ok := priorityGlyph[p]; ok {
		return g
	}
	return priorityGlyph[0]
}

// prStyle colors a PR chip by Linear's PR status. Any other status is teal.
var prStyle = map[string]lipgloss.Style{"merged": purple, "closed": bad, "draft": dim}

// healthText names a Project's health in Linear's words and colors.
var healthText = map[string]seg{"onTrack": {"On track", good}, "atRisk": {"At risk", yellow}, "offTrack": {"Off track", bad}}

// minTitle is the narrowest title. Chips give way first.
const minTitle = 24

// issueChips are the chips at the right of an Issue row, in display order.
// The row drops them from the front when it is narrow.
func (m Model) issueChips(is linear.Issue) [][]seg {
	var chips [][]seg
	if n := len(is.PRs); n > 0 {
		text, st := fmt.Sprintf("#%d", is.PRs[0].Number), prStatusStyle(is.PRs[0].Status)
		if n > 1 {
			text = fmt.Sprintf("%d PRs", n)
			for _, pr := range is.PRs[1:] {
				if pr.Status != is.PRs[0].Status {
					st = good
				}
			}
		}
		chips = append(chips, []seg{{text, st}})
	}
	for _, l := range is.Labels {
		chips = append(chips, []seg{{"●", lipgloss.NewStyle().Foreground(lipgloss.Color(l.Color))}, {" " + l.Name, dim}})
	}
	if t := m.tr; is.Project != nil && (t.my || is.Project.ID != t.proj.ID) {
		chips = append(chips, []seg{{"◇ " + is.Project.Name, dim}})
	}
	if is.Estimate > 0 {
		chips = append(chips, []seg{{"△" + strconv.FormatFloat(is.Estimate, 'f', -1, 64), dim}})
	}
	if is.Assignee != nil {
		chips = append(chips, []seg{{initials(firstNonEmpty(is.Assignee.FullName, is.Assignee.Name)), dim}})
	}
	if !is.CreatedAt.IsZero() {
		chips = append(chips, []seg{{is.CreatedAt.Local().Format("Jan 2"), dim}})
	}
	return chips
}

func prStatusStyle(status string) lipgloss.Style {
	if st, ok := prStyle[status]; ok {
		return st
	}
	return good
}

// initials turns "ada.lovelace" or "Ada Lovelace" into "AL".
func initials(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []rune
	for _, w := range words {
		if len(out) == 2 {
			break
		}
		out = append(out, unicode.ToUpper([]rune(w)[0]))
	}
	return string(out)
}

// issueRow is one Issue: priority, identifier, the state when stW is not 0,
// title, the blocked note, then the chips that fit.
func (m Model) issueRow(is linear.Issue, sel bool, idW, stW int) string {
	ss := []seg{priority(is.Priority), {" " + pad(is.Identifier, idW) + "  ", plain}}
	if stW > 0 {
		ss = append(ss, seg{pad(is.State.Name, stW) + "  ", plain})
	}
	note := blockedNote(is)
	var tail []seg
	if note != "" {
		tail = append(tail, seg{"  " + note, plain})
	}
	chips := m.issueChips(is)
	chipW := func() int {
		w := 0
		for _, c := range chips {
			w += 2 + segsWidth(c)
		}
		return w
	}
	room := m.width - 3 - segsWidth(ss) - segsWidth(tail)
	for len(chips) > 0 && room-chipW() < minTitle {
		chips = chips[1:]
	}
	ss = append(ss, seg{pad(is.Title, max(room-chipW(), 8)), plain})
	ss = append(ss, tail...)
	for _, c := range chips {
		ss = append(ss, seg{"  ", plain})
		ss = append(ss, c...)
	}
	return m.richRow(sel, note != "", ss)
}

func (m Model) issueRows(vis []int) []string {
	t := m.tr
	grouped := t.grouped() && t.filter.Value() == ""
	idW, stW := 4, 0
	for _, is := range t.issues {
		idW = max(idW, len(is.Identifier))
		if !t.grouped() {
			stW = max(stW, 5, min(len([]rune(is.State.Name)), 14))
		}
	}
	cycles, states := map[string]int{}, map[string]int{}
	if grouped {
		for _, v := range vis {
			is := t.issues[v]
			cycles[cycleKey(is)]++
			states[stateKey(is)]++
		}
	}
	var out []string
	lastCycle, lastState := "\x00", "\x00"
	for i, v := range vis {
		is := t.issues[v]
		if grouped {
			if c := cycleKey(is); c != lastCycle {
				out = append(out, cycleHeader(is.Cycle, cycles[c]))
				lastCycle, lastState = c, "\x00"
			}
			if s := stateKey(is); s != lastState {
				out = append(out, "  "+bold.Render(is.State.Name)+dim.Render(fmt.Sprintf("  %d", states[s])))
				lastState = s
			}
		}
		out = append(out, m.issueRow(is, i == t.cur, idW, stW))
	}
	return out
}

func cycleKey(is linear.Issue) string {
	if is.Cycle == nil {
		return ""
	}
	return is.Cycle.Name + "|" + is.Cycle.StartsAt.String()
}

// stateKey groups as linear.ViewOrder sorts: by cycle, then state.
func stateKey(is linear.Issue) string {
	return cycleKey(is) + "|" + is.State.Type + "|" + is.State.Name + "|" + strconv.FormatFloat(is.State.Position, 'g', -1, 64)
}

// cycleHeader reads like Linear's: "Cycle 10  Oct 28 → Nov 11  7 Issues".
func cycleHeader(c *linear.Cycle, n int) string {
	count := fmt.Sprintf("%d Issues", n)
	if n == 1 {
		count = "1 Issue"
	}
	if c == nil {
		return " " + headRow.Render("No cycle") + dim.Render("  "+count)
	}
	name := firstNonEmpty(c.Name, "Cycle")
	dates := c.StartsAt.Local().Format("Jan 2") + " → " + c.EndsAt.Local().Format("Jan 2")
	return " " + headRow.Render(name) + dim.Render("  "+dates+"  "+count)
}

// projectRows lists Projects grouped by status, as Linear's Project views do.
func (m Model) projectRows(vis []int) []string {
	t := m.tr
	ps := t.teamProjects()
	grouped := t.filter.Value() == ""
	leadW, dateW, statuses := 0, 0, map[string]int{}
	for _, v := range vis {
		leadW = max(leadW, min(lipgloss.Width(ps[v].Lead), 16))
		dateW = max(dateW, len(targetDate(ps[v].TargetDate, m.opts.Now())))
		statuses[ps[v].StatusType+"|"+ps[v].Status]++
	}
	nameW := max(m.width-3-4-2-9-2-leadW-2-dateW-2-4, 16)
	var out []string
	last := "\x00"
	for i, v := range vis {
		p := ps[v]
		if k := p.StatusType + "|" + p.Status; grouped && k != last {
			out = append(out, " "+headRow.Render(firstNonEmpty(p.Status, "No status"))+dim.Render(fmt.Sprintf("  %d", statuses[k])))
			last = k
		}
		health := healthText[p.Health]
		health.text = pad(health.text, 9)
		ss := []seg{priority(p.Priority), {" " + pad(p.Name, nameW) + "  ", plain}, health,
			{"  " + pad(p.Lead, leadW) + "  " + pad(targetDate(p.TargetDate, m.opts.Now()), dateW) + "  ", dim},
			{fmt.Sprintf("%3d%%", int(p.Progress*100+0.5)), dim}}
		out = append(out, m.richRow(i == t.cur, false, ss))
	}
	return out
}

// targetDate shows "2026-11-15" as "Nov 15", with the year when it is not now's year.
func targetDate(d string, now time.Time) string {
	at, err := time.Parse(time.DateOnly, d)
	switch {
	case err != nil:
		return ""
	case at.Year() != now.Year():
		return at.Format("Jan 2 2006")
	}
	return at.Format("Jan 2")
}

func blockedNote(is linear.Issue) string {
	if b := linear.BlockedBy(is); len(b) > 0 {
		return "blocked by " + strings.Join(b, ", ")
	}
	return ""
}

func (m Model) detailText() string {
	t := m.tr
	is := t.detail
	var b strings.Builder
	fmt.Fprintf(&b, " %s  %s\n", bold.Render(is.Identifier), is.Title)
	proj := "no Project"
	if is.Project != nil {
		proj = is.Project.Name
	}
	who := "unassigned"
	if is.Assignee != nil {
		who = is.Assignee.Name
	}
	fmt.Fprintf(&b, " %s · %s · %s · %s\n", is.State.Name, is.Team.Key, proj, who)
	if note := blockedNote(is); note != "" {
		b.WriteString(" " + bad.Render(note) + "\n")
	}
	b.WriteString("\n")
	switch {
	case t.detailLines != nil:
		for _, l := range t.detailLines {
			b.WriteString(" " + l + "\n")
		}
	case t.err != "":
		b.WriteString(bad.Render(" "+t.err) + "\n")
	default:
		b.WriteString(dim.Render(" loading…") + "\n")
	}
	return b.String()
}

func (m Model) statusPicker() string {
	t := m.tr
	lines := []string{" " + headRow.Render("Change status of "+t.pickID())}
	if t.item != nil {
		for i, s := range t.itemStates {
			lines = append(lines, m.row(i == t.stCur, s, false))
		}
		return strings.Join(lines, "\n")
	}
	for i, s := range t.states {
		lines = append(lines, m.row(i == t.stCur, s.Name+"  "+s.Type, false))
	}
	return strings.Join(lines, "\n")
}

func (m Model) repoPicker() string {
	t := m.tr
	lines := []string{" " + headRow.Render("Which Repo for "+t.pickID()+"?") + "  " + t.repoIn.View()}
	labels := repoLabels(t.repos)
	for i, v := range rank(t.repoIn.Value(), labels) {
		lines = append(lines, m.row(i == t.repoCur, labels[v], false))
	}
	if len(t.repos) == 0 {
		lines = append(lines, dim.Render(" no git checkouts: add a [[repos]] match to the config"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) skillPicker() string {
	t := m.tr
	lines := []string{" " + headRow.Render("Which skill for "+t.pickID()+"?") + "  " + t.skillIn.View()}
	for i, s := range skillRows(t.skillIn.Value(), m.opts.StartSkills) {
		if s == "" {
			s = noSkill
		}
		lines = append(lines, m.row(i == t.skCur, s, false))
	}
	return strings.Join(lines, "\n")
}

func (m Model) trackerFooter() string {
	t := m.tr
	switch {
	case t.ov == ovAdd:
		return " " + t.input.View()
	case t.ov == ovStatus:
		return dim.Render(" j/k move  enter set status  esc cancel")
	case t.ov == ovSkill:
		return dim.Render(" type a skill  up/down move  enter start  esc cancel")
	case t.ov == ovRepo:
		return dim.Render(" type to filter  up/down move  enter pick  esc cancel")
	case t.filtering:
		return dim.Render(" type to filter  up/down move  enter keep  esc clear")
	}
	switch {
	case m.status != "" && m.statusE:
		return bad.Render(" " + m.status)
	case m.status != "":
		return good.Render(" " + m.status)
	}
	return hintBar(m.width, m.trackerHints())
}

// trackerHints lists the keys for the current level, most useful first.
func (m Model) trackerHints() []hint {
	t := m.tr
	tb := t.curTab()
	if tb.src != nil {
		hs := m.sourceHints()
		if len(t.tabs) > 1 {
			hs = append(hs, hint{"tab", "next view"}, hint{"< >", "move the tab"})
		}
		return append(hs, hint{"q", "quit"}, hint{"?", "all keys"})
	}
	atHome := tb.view != nil && t.lvl == tb.home()
	var hs []hint
	switch t.lvl {
	case lvlRoot:
		hs = append(hs, hint{"enter", "open"})
	case lvlTeams:
		hs = append(hs, hint{"enter", "open the team"}, hint{"esc", "workspaces"})
	case lvlProjects:
		hs = append(hs, hint{"enter", "open the Project"})
		if !atHome {
			hs = append(hs, hint{"esc", "teams"})
		}
	case lvlIssues:
		hs = append(hs, hint{"s", "start"}, hint{"enter", "read"})
		switch {
		case atHome:
		case t.my:
			hs = append(hs, hint{"esc", "workspaces"})
		default:
			hs = append(hs, hint{"esc", "other Projects"})
		}
		hs = append(hs, hint{"c", "status"}, hint{"A", "assign to me"})
		if !t.my && t.team.ID != "" {
			hs = append(hs, hint{"n", "new Issue"})
		}
		hs = append(hs, hint{"o", "browser"})
	case lvlDetail:
		hs = append(hs, hint{"s", "start"}, hint{"esc", "back to the list"}, hint{"c", "status"},
			hint{"A", "assign to me"}, hint{"o", "browser"}, hint{"j/k", "scroll"})
	}
	if t.lvl != lvlDetail {
		hs = append(hs, hint{"/", "search"}, hint{"j/k", "move"})
	}
	if len(t.tabs) > 1 {
		hs = append(hs, hint{"tab", "next view"})
	}
	if tb.view != nil {
		hs = append(hs, hint{"< >", "move the tab"})
	}
	return append(hs, hint{"q", "quit"}, hint{"?", "all keys"})
}
