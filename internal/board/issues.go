package board

import (
	"cmp"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// This file holds the work view's Issue rows: the user's own Issues that
// have no Task on the Board, in the Ready and Backlog groups.

const (
	groupReady   = "Ready"
	groupBacklog = "Backlog"
	mineEvery    = 60 * time.Second
)

// issueGroup puts an Issue in Ready or Backlog by its Kind and its Linear
// state type or local status. Any other state is in no group.
var issueGroup = map[string]map[string]string{
	scheduler.KindLinear: {"unstarted": groupReady, "backlog": groupBacklog, "triage": groupBacklog},
	scheduler.KindLocal:  {string(localissues.Todo): groupReady, string(localissues.Backlog): groupBacklog},
}

var closedLocal = map[localissues.Status]bool{localissues.Done: true, localissues.Canceled: true}

// issueRow is one Issue row of the work view.
type issueRow struct {
	ID, Kind, Group, Title, Repo, URL string
	Priority, Level                   int
	Blockers                          []string // the open blockers
	Unblocked                         bool
	lin                               *linear.Issue
	loc                               *localissues.Issue
}

func (r issueRow) note() string {
	switch {
	case len(r.Blockers) > 0:
		return "blocked by " + strings.Join(r.Blockers, ", ")
	case r.Unblocked:
		return "unblocked"
	}
	return ""
}

// priorityGlyph draws Linear's priority in three cells, by priority number.
var priorityGlyph = map[int]string{0: "···", 1: " ! ", 2: "▂▄▆", 3: "▂▄ ", 4: "▂  "}

func (r issueRow) priorityText() string {
	if r.lin == nil {
		return ""
	}
	if g, ok := priorityGlyph[r.Priority]; ok {
		return g
	}
	return priorityGlyph[0]
}

// issueRows makes the Issue rows: the Issues in Ready or Backlog that no Task
// on the Board holds. An ID matches a Task's ID exactly, so LOCAL-5 in Linear
// and local-5 in the Local Tracker are two Issues.
func issueRows(lin []linear.Issue, loc []localissues.Issue, tasks []scheduler.TaskView) []issueRow {
	held := map[string]bool{}
	for _, t := range tasks {
		held[t.ID] = true
	}
	status := map[int]localissues.Status{}
	for _, is := range loc {
		status[is.N] = is.Status
	}
	// The level graph holds every Issue, in or out of the groups. A local
	// Issue joins it under its own ID.
	graph := slices.Clone(lin)
	var rows []issueRow
	for i := range lin {
		is := &lin[i]
		rows = append(rows, issueRow{ID: is.Identifier, Kind: scheduler.KindLinear, Group: issueGroup[scheduler.KindLinear][is.State.Type],
			Title: is.Title, URL: is.URL, Priority: is.Priority, Blockers: linear.BlockedBy(*is), Unblocked: is.Unblocked, lin: is})
	}
	for i := range loc {
		is := &loc[i]
		node := linear.Issue{ID: is.ID(), Identifier: is.ID()}
		var open []string
		for _, b := range is.BlockedBy {
			n, err := localissues.ParseID(b)
			if err == nil && closedLocal[status[n]] {
				continue
			}
			open = append(open, b)
			if err == nil {
				node.Blockers = append(node.Blockers, linear.Ref{ID: localissues.Issue{N: n}.ID()})
			}
		}
		graph = append(graph, node)
		repo := ""
		if is.Repo != "" {
			repo = filepath.Base(is.Repo)
		}
		rows = append(rows, issueRow{ID: is.ID(), Kind: scheduler.KindLocal, Group: issueGroup[scheduler.KindLocal][string(is.Status)],
			Title: is.Title, Repo: repo, Blockers: open, Unblocked: len(is.BlockedBy) > 0 && len(open) == 0, loc: is})
	}
	levels := linear.Levels(graph)
	out := rows[:0]
	for _, r := range rows {
		if r.Group == "" || held[r.ID] {
			continue
		}
		key := r.ID
		if r.lin != nil {
			key = r.lin.ID
		}
		r.Level = levels[key]
		out = append(out, r)
	}
	slices.SortStableFunc(out, cmpIssueRows)
	return out
}

// cmpIssueRows orders by group, then rows with no open blocker first, then
// priority, dependency level, and natural ID. The URL only makes the order
// total, for two workspaces whose teams share a key.
func cmpIssueRows(a, b issueRow) int {
	return cmp.Or(
		cmp.Compare(slices.Index(groupOrder, a.Group), slices.Index(groupOrder, b.Group)),
		cmp.Compare(min(len(a.Blockers), 1), min(len(b.Blockers), 1)),
		cmp.Compare(linear.PriorityRank(a.Priority), linear.PriorityRank(b.Priority)),
		cmp.Compare(a.Level, b.Level),
		natural(a.ID, b.ID),
		strings.Compare(a.URL, b.URL),
	)
}

func natural(a, b string) int {
	switch {
	case linear.NaturalLess(a, b):
		return -1
	case linear.NaturalLess(b, a):
		return 1
	}
	return 0
}

// mine holds the Issues behind the Issue rows.
type mine struct {
	lin   map[string][]linear.Issue // My Issues, by workspace ID
	fresh map[string]bool           // the workspaces whose live list came back
	loc   []localissues.Issue
}

// linearIssues lists every workspace's Issues, in workspace ID order.
func (mi mine) linearIssues() []linear.Issue {
	var out []linear.Issue
	for _, id := range slices.Sorted(maps.Keys(mi.lin)) {
		out = append(out, mi.lin[id]...)
	}
	return out
}

type (
	mineWorkspacesMsg struct {
		wss []linear.Workspace
		err error
	}
	mineIssuesMsg struct {
		ws     linear.Workspace
		issues []linear.Issue
		live   bool
		err    error
	}
	mineLocalMsg struct {
		issues []localissues.Issue
		err    error
	}
	mineTick struct{}
)

// loadMine reads the signed-in workspaces and the Local Tracker folder. The
// workspaces come back first, so that Update makes each workspace's client.
func (m Model) loadMine() tea.Cmd {
	wss := m.lin.Workspaces
	cmds := []tea.Cmd{func() tea.Msg {
		w, err := wss()
		return mineWorkspacesMsg{wss: w, err: err}
	}}
	if dir := m.opts.LocalIssues; dir != "" {
		cmds = append(cmds, func() tea.Msg {
			is, _, err := localissues.Store{Dir: dir}.List()
			return mineLocalMsg{issues: is, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func tickMine() tea.Cmd {
	return tea.Tick(mineEvery, func(time.Time) tea.Msg { return mineTick{} })
}

// fetchMine paints a workspace that has no list yet from the cache, and
// fetches every workspace live. A cached list never replaces a live one.
func (m Model) fetchMine(wss []linear.Workspace) tea.Cmd {
	read, write := m.lin.ReadCache, m.lin.WriteCache
	var cmds []tea.Cmd
	for _, w := range wss {
		key, api := w.ID+"/my", m.lin.api(w)
		if _, ok := m.mine.lin[w.ID]; !ok {
			cmds = append(cmds, func() tea.Msg {
				is, ok := read(key)
				if !ok {
					return nil
				}
				return mineIssuesMsg{ws: w, issues: is}
			})
		}
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			is, err := api.MyIssues(ctx)
			if err == nil {
				write(key, is)
			}
			return mineIssuesMsg{ws: w, issues: is, live: true, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// mineMsg handles the messages of the Issue rows. ok is false for any other message.
func (m Model) mineMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case mineTick:
		return m, tea.Batch(m.loadMine(), tickMine()), true
	case mineWorkspacesMsg:
		if msg.err != nil {
			m.setStatus("Linear workspaces: "+msg.err.Error(), true)
			return m, nil, true
		}
		signedIn := map[string]bool{}
		for _, w := range msg.wss {
			signedIn[w.ID] = true
		}
		for id := range m.mine.lin {
			if !signedIn[id] {
				delete(m.mine.lin, id)
			}
		}
		m.setBoard(m.board)
		return m, m.fetchMine(msg.wss), true
	case mineIssuesMsg:
		if msg.live {
			if msg.err != nil {
				m.setStatus("my Issues in "+msg.ws.Name+": "+msg.err.Error(), true)
				if len(msg.issues) == 0 {
					return m, nil, true
				}
			}
			m.mine.fresh[msg.ws.ID] = true
		} else if m.mine.fresh[msg.ws.ID] {
			return m, nil, true
		}
		m.mine.lin[msg.ws.ID] = msg.issues
		m.setBoard(m.board)
	case mineLocalMsg:
		if msg.err != nil {
			m.setStatus("local Issues: "+msg.err.Error(), true)
			return m, nil, true
		}
		m.mine.loc = msg.issues
		m.setBoard(m.board)
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) currentIssue() (issueRow, bool) {
	for _, r := range m.issues {
		if r.ID == m.sel {
			return r, true
		}
	}
	return issueRow{}, false
}

// openRow opens a Linear Issue in the browser. It opens a local Issue's file
// in $EDITOR, then reads the Issue rows again.
func (m Model) openRow(r issueRow) tea.Cmd {
	if r.lin == nil {
		editor := firstNonEmpty(os.Getenv("EDITOR"), "vi")
		path := filepath.Join(m.opts.LocalIssues, r.ID+".md")
		return tea.ExecProcess(exec.Command(editor, path), func(err error) tea.Msg {
			return opMsg{label: "edit", task: r.ID, err: err}
		})
	}
	open, u := m.opts.Open, r.URL
	return func() tea.Msg { return opMsg{label: "open", task: r.ID, err: open(u)} }
}
