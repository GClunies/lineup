package board

import (
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/localissues"
	"github.com/gclunies/lineup/internal/scheduler"
)

// This file holds the start that s opens on an Issue row: the skill picker,
// then the Repo picker when the Issue has no Repo mapping, then the add and
// the start of the Task.

const (
	// noSkill is the first row of the skill picker.
	noSkill   = "just go"
	noMapping = "no Repo mapping"
)

// starter is the state of the skill picker and the Repo picker. The Model's
// pick is the Issue row that they start.
type starter struct {
	skillIn textinput.Model
	skCur   int
	skill   string // the skill chosen for the start that waits on the Repo picker
	repoIn  textinput.Model
	repoCur int
	repos   []string
}

func newStarter() starter {
	si := textinput.New()
	si.Prompt = "skill: "
	ri := textinput.New()
	ri.Prompt = "repo: "
	for _, x := range []*textinput.Model{&si, &ri} {
		x.Cursor.SetMode(cursor.CursorStatic)
	}
	return starter{skillIn: si, repoIn: ri}
}

type (
	needRepoMsg struct {
		row   issueRow
		skill string
	}
	reposMsg struct{ repos []string }
)

// startRow opens the skill picker for an Issue row.
func (m Model) startRow(r issueRow) (tea.Model, tea.Cmd) {
	m.mode, m.pick = modeSkill, r
	m.st.skCur = 0
	m.st.skillIn.SetValue("")
	return m, m.st.skillIn.Focus()
}

func (m Model) skillKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := &m.st
	rows := skillRows(st.skillIn.Value(), m.opts.StartSkills)
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeNormal
		st.skillIn.Blur()
		m.setStatus("start cancelled", false)
	case "down", "ctrl+n":
		st.skCur = min(st.skCur+1, len(rows)-1)
	case "up", "ctrl+p":
		st.skCur = max(st.skCur-1, 0)
	case "enter":
		m.mode = modeNormal
		st.skillIn.Blur()
		return m, m.startTask(m.pick, "", rows[st.skCur])
	default:
		var cmd tea.Cmd
		st.skillIn, cmd = st.skillIn.Update(msg)
		st.skCur = 0
		return m, cmd
	}
	return m, nil
}

func (m Model) repoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := &m.st
	vis := rank(st.repoIn.Value(), repoLabels(st.repos))
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeNormal
		st.repoIn.Blur()
		m.setStatus("start cancelled", false)
	case "down", "ctrl+n":
		st.repoCur = max(min(st.repoCur+1, len(vis)-1), 0)
	case "up", "ctrl+p":
		st.repoCur = max(st.repoCur-1, 0)
	case "enter":
		if st.repoCur >= len(vis) {
			return m, nil
		}
		r, repo := m.pick, st.repos[vis[st.repoCur]]
		m.mode = modeNormal
		st.repoIn.Blur()
		remember, start := m.rememberRepo(r), m.startTask(r, repo, st.skill)
		return m, func() tea.Msg {
			if err := remember(repo); err != nil {
				return opMsg{label: "remember Repo", task: r.ID, err: err}
			}
			return start()
		}
	default:
		var cmd tea.Cmd
		st.repoIn, cmd = st.repoIn.Update(msg)
		st.repoCur = 0
		return m, cmd
	}
	return m, nil
}

// rememberRepo keeps the Repo that the user picked for r: in a local Issue's
// file, or for a Linear Issue's Project. A Linear Issue with no Project keeps
// nothing.
func (m Model) rememberRepo(r issueRow) func(repo string) error {
	if r.loc != nil {
		store := localissues.Store{Dir: m.opts.LocalIssues}
		return func(repo string) error { return store.SetRepo(r.ID, repo) }
	}
	remember := m.lin.RememberRepo
	return func(repo string) error {
		if r.lin.Project == nil {
			return nil
		}
		return remember(r.lin.Project.ID, repo)
	}
}

// startTask adds the Issue as a Task, sets its skill if any, then starts it.
// An empty repo lets the Scheduler choose. Without a mapping, the Repo picker
// opens.
func (m Model) startTask(r issueRow, repo, skill string) tea.Cmd {
	c, need := m.client, needRepoMsg{row: r, skill: skill}
	return func() tea.Msg {
		var v scheduler.TaskView
		err := c.Do(scheduler.Request{Op: "add", Issue: r.ID, Kind: r.Kind, Repo: repo}, &v)
		if err != nil {
			if repo == "" && strings.Contains(err.Error(), noMapping) {
				return need
			}
			return opMsg{label: "add", task: r.ID, err: err}
		}
		id := firstNonEmpty(v.ID, r.ID)
		if skill != "" {
			if err := c.Do(scheduler.Request{Op: "set", Task: id, Key: "skill", Value: skill}, nil); err != nil {
				return opMsg{label: "set skill", task: id, err: err}
			}
		}
		return opMsg{label: "start", task: id, note: skill, err: c.Do(scheduler.Request{Op: "start", Task: id}, nil)}
	}
}

func (m Model) loadRepos() tea.Cmd {
	f := m.opts.Repos
	return func() tea.Msg { return reposMsg{repos: f()} }
}

// startMsg handles the messages of the start. ok is false for any other message.
func (m Model) startMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case needRepoMsg:
		m.mode, m.pick, m.st.skill = modeRepo, msg.row, msg.skill
		m.st.repoIn.SetValue("")
		m.st.repoCur = 0
		return m, tea.Batch(m.loadRepos(), m.st.repoIn.Focus()), true
	case reposMsg:
		m.st.repos, m.st.repoCur = msg.repos, 0
		return m, nil, true
	}
	return m, nil, false
}

// skillRows lists the skill picker's rows for the typed text. An empty row
// starts with no skill. Typed text filters the listed skills, and a name that
// is not listed becomes its own row.
func skillRows(typed string, listed []string) []string {
	q := strings.TrimPrefix(strings.TrimSpace(typed), "/")
	if q == "" {
		return append([]string{""}, listed...)
	}
	var rows []string
	for _, i := range rank(q, listed) {
		rows = append(rows, listed[i])
	}
	if !slices.Contains(listed, q) {
		rows = append(rows, q)
	}
	return rows
}

func repoLabels(repos []string) []string {
	home, _ := os.UserHomeDir()
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = strings.Replace(r, home, "~", 1)
	}
	return out
}

func (m Model) skillPicker() string {
	st := m.st
	lines := []string{" " + headRow.Render("Which skill for "+m.pick.ID+"?") + "  " + st.skillIn.View()}
	for i, s := range skillRows(st.skillIn.Value(), m.opts.StartSkills) {
		if s == "" {
			s = noSkill
		}
		lines = append(lines, m.row(i == st.skCur, s))
	}
	return strings.Join(lines, "\n")
}

func (m Model) repoPicker() string {
	st := m.st
	lines := []string{" " + headRow.Render("Which Repo for "+m.pick.ID+"?") + "  " + st.repoIn.View()}
	labels := repoLabels(st.repos)
	for i, v := range rank(st.repoIn.Value(), labels) {
		lines = append(lines, m.row(i == st.repoCur, labels[v]))
	}
	if len(st.repos) == 0 {
		lines = append(lines, dim.Render(" no git checkouts: add a [[repos]] match to the config"))
	}
	return strings.Join(lines, "\n")
}
