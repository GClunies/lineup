package board

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/scheduler"
)

func (m Model) reviewBody() string {
	r := m.rv
	tb := r.curTab()
	if tb.Tasks && m.detail {
		return m.vp.View()
	}
	names := make([]string, len(r.tabs))
	for i, tb := range r.tabs {
		names[i] = tb.Name
	}
	head := " " + headRow.Render(tb.Name)
	if tb.Tasks {
		head += dim.Render(fmt.Sprintf("  %d", len(m.tasks)))
		return m.tabStrip(names, r.tab) + "\n" + head + "\n" + m.taskBody(max(m.height-5, 1))
	}
	prs, loaded := r.list(tb)
	if loaded {
		head += dim.Render(fmt.Sprintf("  %d", len(prs)))
	}
	if r.loading {
		head += dim.Render("  refreshing…")
	}
	if r.filtering || r.filter.Value() != "" {
		head += "  " + r.filter.View()
	}
	lines := []string{m.tabStrip(names, r.tab), head}
	rows := r.rows()
	switch {
	case !loaded:
		lines = append(lines, dim.Render(" loading…"))
	case len(rows) == 0:
		lines = append(lines, dim.Render(" no PRs"))
	default:
		lines = append(lines, m.prRows(rows)...)
	}
	return strings.Join(lines, "\n")
}

// prColumns are the columns at the right of the title, in display order.
var prColumns = []string{"AUTHOR", "CI", "REVIEW", "TASK", "AGE"}

// prDropOrder is the order in which a narrow screen drops the columns. TASK
// goes last, because it leads to the Worker.
var prDropOrder = []int{0, 4, 2, 1, 3} // AUTHOR, AGE, REVIEW, CI, TASK

// colGap is the space before each column after the first.
const colGap = "   "

// prRows lays out the rows in columns under a header row. On a narrow screen
// the columns drop in prDropOrder, so that the title keeps minTitle cells.
func (m Model) prRows(rows []github.PullRequest) []string {
	join := m.joinPR()
	all := make([][][]seg, len(rows)) // for each row, a cell for each of prColumns
	refW, titleW := len("PR"), len("TITLE")
	widths := make([]int, len(prColumns))
	for i, c := range prColumns {
		widths[i] = len(c)
	}
	for i, pr := range rows {
		task := ""
		if t, ok := join[pr.URL]; ok {
			task = taskText(t)
		}
		all[i] = [][]seg{
			{{pr.Author, dim}},
			prCI(pr),
			prReview(pr),
			{{task, good}},
			{{ago(m.opts.Now().Sub(pr.CreatedAt)), dim}},
		}
		refW = max(refW, lipgloss.Width(prRef(pr)))
		titleW = max(titleW, lipgloss.Width(pr.Title))
		for j, cell := range all[i] {
			widths[j] = max(widths[j], segsWidth(cell))
		}
	}
	widths[0], widths[3] = min(widths[0], 18), min(widths[3], 32)
	// marker 3, then the ref, then a gap before the title and each shown column
	shown := []bool{true, true, true, true, true}
	used := func() int {
		u := 3 + refW + len(colGap)
		for j, w := range widths {
			if shown[j] {
				u += len(colGap) + w
			}
		}
		return u
	}
	for _, j := range prDropOrder {
		if m.width-used() >= minTitle {
			break
		}
		shown[j] = false
	}
	titleW = max(min(titleW, m.width-used()), 1)

	headCells := []string{pad("PR", refW), colGap + pad("TITLE", titleW)}
	for j, c := range prColumns {
		if shown[j] {
			headCells = append(headCells, colGap+pad(c, widths[j]))
		}
	}
	out := []string{ansi.Truncate(colHead.Render("   "+strings.Join(headCells, "")), m.width, "…")}
	sel := m.rv.selected(rows)
	for i, pr := range rows {
		ss := []seg{{pad(prRef(pr), refW), plain}, {colGap + pad(pr.Title, titleW), plain}}
		for j := range prColumns {
			if !shown[j] {
				continue
			}
			ss = append(ss, seg{colGap, plain})
			ss = append(ss, padSegs(truncSegs(all[i][j], widths[j]), widths[j])...)
		}
		out = append(out, m.richRow(i == sel, pr.Draft || !pr.MergedAt.IsZero(), ss))
	}
	return out
}

// prRef is the short repository name and the number, for example data#4945.
func prRef(pr github.PullRequest) string {
	return pr.Repo[strings.LastIndex(pr.Repo, "/")+1:] + fmt.Sprintf("#%d", pr.Number)
}

// prCI is the CI state, draft, or merged.
func prCI(pr github.PullRequest) []seg {
	switch {
	case !pr.MergedAt.IsZero():
		return []seg{{"merged", dim}}
	case pr.Draft:
		return []seg{{"draft", dim}}
	case pr.Checks == github.ChecksPassed:
		return []seg{{"✓ CI", good}}
	case pr.Checks == github.ChecksFailed:
		return []seg{{"✗ CI", bad}}
	case pr.Checks == github.ChecksPending:
		return []seg{{"◷ CI", yellow}}
	}
	return nil
}

// prReview is the review decision: changes requested, or the approvals.
func prReview(pr github.PullRequest) []seg {
	if pr.Draft {
		return nil
	}
	approvals := 0
	for _, rv := range pr.LatestReviews {
		if rv.State == "APPROVED" {
			approvals++
		}
	}
	switch {
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return []seg{{"✗ changes", bad}}
	case approvals > 0:
		return []seg{{fmt.Sprintf("✓ %d", approvals), good}}
	}
	return nil
}

// truncSegs cuts the segments to w cells.
func truncSegs(ss []seg, w int) []seg {
	var out []seg
	for _, s := range ss {
		if w <= 0 {
			break
		}
		if lipgloss.Width(s.text) > w {
			s.text = ansi.Truncate(s.text, w, "…")
		}
		w -= lipgloss.Width(s.text)
		out = append(out, s)
	}
	return out
}

func padSegs(ss []seg, w int) []seg {
	if gap := w - segsWidth(ss); gap > 0 {
		return append(ss, seg{strings.Repeat(" ", gap), plain})
	}
	return ss
}

// taskText is the joined Task's ID and its Stage, or its Needs you reason.
func taskText(t scheduler.TaskView) string {
	if t.Flag != "" {
		return t.ID + " ⚑ " + t.Flag
	}
	return strings.TrimSpace(t.ID + " " + stageText(t))
}

func (m Model) reviewFooter() string {
	switch {
	case m.status != "" && m.statusE:
		return bad.Render(" " + m.status)
	case m.status != "":
		return good.Render(" " + m.status)
	case m.rv.tabErr() != "":
		return bad.Render(" " + m.rv.tabErr())
	case m.rv.filtering:
		return hintBar(m.width, []hint{{"enter", "keep the search"}, {"esc", "clear"}, {"?", "all keys"}})
	case m.rv.curTab().Tasks:
		return hintBar(m.width, m.boardHints())
	}
	hs := []hint{{"tab", "next view"}}
	if pr, ok := m.rv.current(); ok {
		switch t, joined := m.joinPR()[pr.URL]; {
		case joined && t.Live:
			hs = append(hs, hint{"enter", "go to the Worker"})
		case joined && restorable(t):
			hs = append(hs, hint{"enter", "restore the Worker"})
		}
		hs = append(hs, hint{"o", "open the PR"}, hint{"r", "review now"})
	}
	hs = append(hs, hint{"/", "search"}, hint{"z", "agent"}, hint{"R", "refresh now"}, hint{"< >", "move the tab"}, hint{"j/k", "move"}, hint{"q", "quit"}, hint{"?", "all keys"})
	return hintBar(m.width, hs)
}

const reviewHelp = `
 lineup review keys

   The tabs are Reviews, My PRs, Requested, then each [[review.views]]
   and [[review.queues]] entry of the config. Reviews lists the review
   Tasks. A PR row shows the review Task that has its PR, if one is on
   the Board, in the TASK column. A Q: tab lists the PRs that wait for
   the queue, first to start first.

   tab      next view       shift+tab  previous view
   < >      move this view's tab left or right; lineup keeps the order
   j k      move the selection
   /        fuzzy search in this list
   enter    go to the Worker's tab of the Task in the TASK column,
            restore its closed Worker, or open the PR when the PR has
            no started Task
   o        open the PR in the browser
   r        start a review Task for this PR now, outside every queue's
            slots, or reopen its closed Worker
   R        refresh now: search GitHub again and poll every started
            Task; lineup also refreshes every 2 minutes
   z        zoom the agent pane
   1 2 3    show the plan, work, or review view
   ?        this help      q  quit

 press any key to go back
`

const reviewTasksHelp = `
 lineup review keys: the Reviews tab

   The Reviews tab lists the review Tasks on the Board. The other tabs
   list PRs.

   tab      next view       shift+tab  previous view
   < >      move this view's tab left or right; lineup keeps the order
   j k      move the selection
   enter    focus the Task's tab in Rex, or restore its closed Worker
   p        live preview of the Worker's screen
   f        detail pane at full screen, and back
   x        close the Task, or its tab and worktree when it is Done,
            or archive a Done Task with no tab, after y/n
   s        start a Queued Task
   o        open the PR
   R        refresh now: search GitHub again and poll every started
            Task; lineup also refreshes every 2 minutes
   z        zoom the agent pane
   1 2 3    show the plan, work, or review view
   ?        this help      q  quit

 press any key to go back
`
