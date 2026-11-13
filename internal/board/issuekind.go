package board

import (
	"context"
	"fmt"
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

// This file holds what the work view does with an Issue row of each Kind:
// the keys that the row takes, and the Tracker behind the row.

// issueKind is what an Issue row of one Kind offers.
type issueKind struct {
	// keys are the keys that the row takes, in the order that the footer
	// shows them. boardKey takes no other key on the row.
	keys []hint
	// tracker is the Tracker behind the row.
	tracker func(m Model, r issueRow) rowTracker
}

// issueKinds is the one table of the Issue row Kinds.
var issueKinds = map[string]issueKind{
	scheduler.KindLinear: {
		keys:    []hint{{"s", "start"}, {"f", "details"}, {"c", "status"}, {"o", "open the Issue"}},
		tracker: Model.linearRow,
	},
	scheduler.KindLocal: {
		keys:    []hint{{"s", "start"}, {"f", "details"}, {"c", "status"}, {"o", "edit the Issue"}, {"P", "promote to Linear"}},
		tracker: Model.localRow,
	},
}

// takes says whether the row takes the key.
func (r issueRow) takes(key string) bool {
	return slices.ContainsFunc(issueKinds[r.Kind].keys, func(h hint) bool { return h.key == key })
}

// rowTracker is the Tracker behind one Issue row.
type rowTracker interface {
	// facts is the detail pane's line under the title.
	facts() string
	// description is the Issue's description, as Markdown.
	description(ctx context.Context) (string, error)
	// open shows the Issue: a Linear Issue in the browser, a local Issue's
	// file in $EDITOR.
	open() tea.Cmd
	// rememberRepo keeps the Repo that the user picked for the Issue.
	rememberRepo(repo string) error
	// statuses lists the statuses that the Issue can take, in the order work
	// flows, and the index of the one it has, or -1.
	statuses(ctx context.Context) (names []string, cur int, err error)
	// setStatus moves the Issue to the status of that name.
	setStatus(ctx context.Context, name string) error
}

func (m Model) tracker(r issueRow) rowTracker { return issueKinds[r.Kind].tracker(m, r) }

// linearRow is a Linear Issue row, with the client of its workspace.
type linearRow struct {
	is       linear.Issue
	api      LinearAPI
	openURL  func(string) error
	remember func(projectID, repo string) error
}

func (m Model) linearRow(r issueRow) rowTracker {
	return linearRow{is: *r.lin, api: m.lin.api(m.mine.workspaceOf(*r.lin)), openURL: m.opts.Open, remember: m.lin.RememberRepo}
}

func (l linearRow) facts() string {
	proj, who := "no Project", "unassigned"
	if l.is.Project != nil {
		proj = l.is.Project.Name
	}
	if l.is.Assignee != nil {
		who = l.is.Assignee.Name
	}
	return strings.Join([]string{l.is.State.Name, l.is.Team.Key, proj, who}, " · ")
}

func (l linearRow) description(ctx context.Context) (string, error) {
	full, err := l.api.Issue(ctx, l.is.Identifier)
	return full.Description, err
}

func (l linearRow) open() tea.Cmd {
	id, u, open := l.is.Identifier, l.is.URL, l.openURL
	return func() tea.Msg { return opMsg{label: "open", task: id, err: open(u)} }
}

// rememberRepo keeps the Repo for the Issue's Project. An Issue with no
// Project keeps nothing.
func (l linearRow) rememberRepo(repo string) error {
	if l.is.Project == nil {
		return nil
	}
	return l.remember(l.is.Project.ID, repo)
}

// statuses are the workflow states of the Issue's team.
func (l linearRow) statuses(ctx context.Context) ([]string, int, error) {
	states, err := l.api.TeamStates(ctx, l.is.Team.ID)
	names, cur := make([]string, len(states)), -1
	for i, s := range states {
		names[i] = s.Name
		if s.ID == l.is.State.ID {
			cur = i
		}
	}
	return names, cur, err
}

func (l linearRow) setStatus(ctx context.Context, name string) error {
	return l.api.SetStatusByName(ctx, l.is, name)
}

// localRow is a local Issue row, in the Local Tracker's folder.
type localRow struct {
	is    localissues.Issue
	store localissues.Store
}

func (m Model) localRow(r issueRow) rowTracker {
	return localRow{is: *r.loc, store: localissues.Store{Dir: m.opts.LocalIssues}}
}

func (l localRow) path() string { return filepath.Join(l.store.Dir, l.is.ID()+".md") }

func (l localRow) facts() string {
	return strings.Join([]string{string(l.is.Status), firstNonEmpty(l.is.Repo, "no repo"), repoLabels([]string{l.path()})[0]}, " · ")
}

func (l localRow) description(context.Context) (string, error) {
	is, err := l.store.Get(l.is.ID())
	return is.Body, err
}

// open runs $EDITOR on the file. The Issue rows load again after it.
func (l localRow) open() tea.Cmd {
	id := l.is.ID()
	editor := firstNonEmpty(os.Getenv("EDITOR"), "vi")
	return tea.ExecProcess(exec.Command(editor, l.path()), func(err error) tea.Msg {
		return opMsg{label: "edit", task: id, err: err}
	})
}

// rememberRepo writes the Repo into the Issue's file.
func (l localRow) rememberRepo(repo string) error { return l.store.SetRepo(l.is.ID(), repo) }

func (l localRow) statuses(context.Context) ([]string, int, error) {
	names := make([]string, len(localissues.Statuses))
	for i, s := range localissues.Statuses {
		names[i] = string(s)
	}
	return names, slices.Index(localissues.Statuses, l.is.Status), nil
}

func (l localRow) setStatus(_ context.Context, name string) error {
	return l.store.SetStatus(l.is.ID(), localissues.Status(name))
}

// showIssue opens the detail pane on the Issue row r, then reads the Issue's
// description off the render path.
func (m Model) showIssue(r issueRow) (tea.Model, tea.Cmd) {
	tr := m.tracker(r)
	head := fmt.Sprintf(" %s  %s\n %s\n", bold.Render(r.ID), r.Title, tr.facts())
	if note := r.note(); note != "" {
		head += " " + bad.Render(note) + "\n"
	}
	head += "\n"
	m.detail, m.detailTask = true, ""
	m.detailGen++
	m.vp.SetContent(head + dim.Render(" loading…"))
	m.vp.GotoTop()
	gen, md, width := m.detailGen, m.md, max(m.width-4, 20)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		text, err := tr.description(ctx)
		if err != nil {
			return detailMsg{gen: gen, text: head + bad.Render(" "+err.Error())}
		}
		lines := md.Lines(r.ID, text, width)
		if len(lines) == 0 {
			return detailMsg{gen: gen, text: head + dim.Render(" no description")}
		}
		return detailMsg{gen: gen, text: head + " " + strings.Join(lines, "\n ")}
	}
}

// statusList is the list that c opens on the Model's pick.
type statusList struct {
	names []string
	cur   int
}

type statusesMsg struct {
	row   issueRow
	names []string
	cur   int
	err   error
}

// loadStatuses reads the statuses of the Issue row r off the render path.
func (m Model) loadStatuses(r issueRow) tea.Cmd {
	tr := m.tracker(r)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		names, cur, err := tr.statuses(ctx)
		return statusesMsg{row: r, names: names, cur: cur, err: err}
	}
}

// openStatuses opens the status list when its statuses come back. A list
// that comes back after the user went on to another prompt or view opens
// nothing.
func (m Model) openStatuses(msg statusesMsg) Model {
	switch {
	case msg.err != nil:
		m.setStatus("status "+msg.row.ID+": "+msg.err.Error(), true)
	case len(msg.names) == 0:
		m.setStatus("status "+msg.row.ID+": no statuses to pick", true)
	case m.mode == modeNormal:
		m.mode, m.pick = modeStatus, msg.row
		m.sl = statusList{names: msg.names, cur: max(msg.cur, 0)}
	}
	return m
}

func (m Model) statusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sl := &m.sl
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeNormal
	case "j", "down", "ctrl+n":
		sl.cur = min(sl.cur+1, len(sl.names)-1)
	case "k", "up", "ctrl+p":
		sl.cur = max(sl.cur-1, 0)
	case "enter":
		m.mode = modeNormal
		r, name, tr := m.pick, sl.names[sl.cur], m.tracker(m.pick)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return opMsg{label: "status", task: r.ID, note: name, err: tr.setStatus(ctx, name)}
		}
	}
	return m, nil
}

func (m Model) statusPicker() string {
	lines := []string{" " + headRow.Render("Status of "+m.pick.ID)}
	for i, s := range m.sl.names {
		lines = append(lines, m.row(i == m.sl.cur, s))
	}
	return strings.Join(lines, "\n")
}
