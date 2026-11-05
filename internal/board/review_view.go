package board

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/scheduler"
)

func (m Model) reviewBody() string {
	r := m.rv
	names := make([]string, len(r.tabs))
	for i, tb := range r.tabs {
		names[i] = tb.Name
	}
	tb := r.curTab()
	prs, loaded := r.list(tb)
	head := " " + headRow.Render(tb.Name)
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
		lines = append(lines, m.prRows(rows, tb.Key != keyMine)...)
	}
	return strings.Join(lines, "\n")
}

// prRows lays out the rows in columns. On a narrow screen the fields drop
// from the right, so that the title keeps minTitle cells.
func (m Model) prRows(rows []github.PullRequest, withAuthor bool) []string {
	join := m.joinPR()
	type cells struct {
		ref, author, task, age string
		status                 []seg
	}
	all := make([]cells, len(rows))
	refW := 0
	widths := make([]int, 4) // author, status, task, age
	for i, pr := range rows {
		c := cells{ref: prRef(pr), status: prStatus(pr), age: ago(m.opts.Now().Sub(pr.CreatedAt))}
		if withAuthor {
			c.author = pr.Author
		}
		if t, ok := join[pr.URL]; ok {
			c.task = taskText(t)
		}
		all[i] = c
		refW = max(refW, lipgloss.Width(c.ref))
		for j, w := range []int{min(lipgloss.Width(c.author), 18), segsWidth(c.status), min(lipgloss.Width(c.task), 32), lipgloss.Width(c.age)} {
			widths[j] = max(widths[j], w)
		}
	}
	// marker 3, then two cells of gap before each column after the ref
	used := func(n int) int {
		u := 3 + refW + 2
		for _, w := range widths[:n] {
			if w > 0 {
				u += w + 2
			}
		}
		return u
	}
	keep := len(widths)
	for keep > 0 && m.width-used(keep) < minTitle {
		keep--
	}
	titleW := max(m.width-used(keep), 1)

	sel := m.rv.selected(rows)
	var out []string
	for i, pr := range rows {
		c := all[i]
		ss := []seg{{pad(c.ref, refW), plain}, {"  " + pad(pr.Title, titleW), plain}}
		cols := [][]seg{{{pad(c.author, widths[0]), dim}}, padSegs(c.status, widths[1]), {{pad(c.task, widths[2]), good}}, {{pad(c.age, widths[3]), dim}}}
		for j := range keep {
			if widths[j] > 0 {
				ss = append(ss, seg{"  ", plain})
				ss = append(ss, cols[j]...)
			}
		}
		out = append(out, m.richRow(i == sel, pr.Draft, ss))
	}
	return out
}

// prRef is the short repository name and the number, for example data#4945.
func prRef(pr github.PullRequest) string {
	return pr.Repo[strings.LastIndex(pr.Repo, "/")+1:] + fmt.Sprintf("#%d", pr.Number)
}

// prStatus is the CI state and the review decision, or draft.
func prStatus(pr github.PullRequest) []seg {
	if pr.Draft {
		return []seg{{"draft", dim}}
	}
	var ss []seg
	switch pr.Checks {
	case github.ChecksPassed:
		ss = append(ss, seg{"✓ CI", good})
	case github.ChecksFailed:
		ss = append(ss, seg{"✗ CI", bad})
	case github.ChecksPending:
		ss = append(ss, seg{"◷ CI", yellow})
	}
	approvals := 0
	for _, rv := range pr.LatestReviews {
		if rv.State == "APPROVED" {
			approvals++
		}
	}
	var decision seg
	switch {
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		decision = seg{"✗ changes", bad}
	case approvals > 0:
		decision = seg{fmt.Sprintf("✓ %d", approvals), good}
	default:
		return ss
	}
	if len(ss) > 0 {
		ss = append(ss, seg{"  ", plain})
	}
	return append(ss, decision)
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
	case m.rv.err != "":
		return bad.Render(" " + m.rv.err)
	case m.rv.filtering:
		return hintBar(m.width, []hint{{"enter", "keep the search"}, {"esc", "clear"}, {"?", "all keys"}})
	}
	hs := []hint{{"tab", "next view"}}
	if pr, ok := m.rv.current(); ok {
		if _, joined := m.joinPR()[pr.URL]; joined {
			hs = append(hs, hint{"enter", "go to the Task"})
		} else {
			hs = append(hs, hint{"enter", "open the PR"})
		}
		hs = append(hs, hint{"o", "open the PR"}, hint{"r", "review now"})
	}
	hs = append(hs, hint{"/", "search"}, hint{"R", "refresh"}, hint{"< >", "move the tab"}, hint{"j/k", "move"}, hint{"q", "quit"}, hint{"?", "all keys"})
	return hintBar(m.width, hs)
}

const reviewHelp = `
 lineup review keys

   The tabs are My PRs, Requested, then each [[review.views]] and
   [[review.queues]] entry of the config. A row shows the Task that has
   its PR, if one is on the Board. A Q: tab lists the PRs that wait for
   the queue, first to start first.

   tab      next view       shift+tab  previous view
   < >      move this view's tab left or right; lineup keeps the order
   j k      move the selection
   /        fuzzy search in this list
   enter    go to the Task's tab, or open the PR when no Task has it
   o        open the PR in the browser
   r        start a review Task for this PR now, outside every queue's slots
   R        refresh now; lineup also refreshes every 2 minutes
   ?        this help      q  quit

 press any key to go back
`
