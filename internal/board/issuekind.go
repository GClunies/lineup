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
		keys:    []hint{{"s", "start"}, {"f", "details"}, {"o", "open the Issue"}},
		tracker: Model.linearRow,
	},
	scheduler.KindLocal: {
		keys:    []hint{{"s", "start"}, {"f", "details"}, {"o", "edit the Issue"}, {"P", "promote to Linear"}},
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
