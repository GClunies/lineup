package board

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/localissues"
)

// This file holds the capture box that n opens on the work view, the
// Destination list that tab opens from it, and the Promote that P starts
// from the same list.

const (
	maxRecent = 5
	// teamReads is how many teams of one workspace list their Projects at once.
	teamReads = 4
)

// destination is where a new Issue goes. A zero Workspace means the Local Tracker.
type destination struct {
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
	TeamID        string `json:"teamId"`
	TeamKey       string `json:"teamKey"`
	ProjectID     string `json:"projectId,omitempty"`
	ProjectName   string `json:"projectName,omitempty"`
}

func (d destination) local() bool { return d.Workspace == "" }

func (d destination) label() string {
	if d.local() {
		return "local"
	}
	s := d.WorkspaceName + " / " + d.TeamKey
	if d.ProjectID != "" {
		s += " / " + d.ProjectName
	}
	return s
}

// key names the Destination across runs, whatever its names become.
func (d destination) key() string { return d.Workspace + "/" + d.TeamID + "/" + d.ProjectID }

// pushRecent puts d first and keeps the newest maxRecent Destinations, each once.
func pushRecent(recent []destination, d destination) []destination {
	out := []destination{d}
	for _, x := range recent {
		if x.key() != d.key() && len(out) < maxRecent {
			out = append(out, x)
		}
	}
	return out
}

// destinations is what the Destination list shows. The Linear part loads
// once, on the first tab of the Board's life.
type destinations struct {
	asked   bool
	waiting int // the lists still to come back: the workspaces, then each workspace's teams and Projects
	wss     []linear.Workspace
	linear  map[string][]destination // by workspace ID
	errs    map[string]string        // by workspace ID; "" holds the workspace list's error
	recent  []destination
}

type destRow struct {
	to  destination
	tag string
}

// rows lists local when withLocal is set, then the recent Destinations, then
// every other Linear Destination by workspace, team, and Project.
func (ds destinations) rows(withLocal bool) []destRow {
	var rows []destRow
	if withLocal {
		rows = append(rows, destRow{tag: "just me"})
	}
	seen := map[string]bool{}
	for _, d := range ds.recent {
		rows = append(rows, destRow{to: d, tag: "recent"})
		seen[d.key()] = true
	}
	for _, w := range ds.wss {
		for _, d := range ds.linear[w.ID] {
			if !seen[d.key()] {
				rows = append(rows, destRow{to: d})
			}
		}
	}
	return rows
}

// failures lists the error lines: the workspace list's, then each workspace's in order.
func (ds destinations) failures() []string {
	var out []string
	if e := ds.errs[""]; e != "" {
		out = append(out, e)
	}
	for _, w := range ds.wss {
		if e := ds.errs[w.ID]; e != "" {
			out = append(out, e)
		}
	}
	return out
}

// captureBox is the capture box and the filter of its Destination list.
type captureBox struct {
	title   textinput.Model
	to      destination
	filter  textinput.Model
	cur     int
	promote string // the local Issue that P promotes
}

func newCaptureBox() captureBox {
	title := textinput.New()
	title.Prompt = "new issue: "
	title.CharLimit = 250
	filter := textinput.New()
	filter.Prompt = "> "
	for _, x := range []*textinput.Model{&title, &filter} {
		x.Cursor.SetMode(cursor.CursorStatic)
	}
	return captureBox{title: title, filter: filter}
}

type (
	destWorkspacesMsg struct {
		wss []linear.Workspace
		err error
	}
	destTeamsMsg struct {
		ws    linear.Workspace
		dests []destination
		err   error
	}
	destRecentMsg struct {
		recent []destination
		err    error
	}
	addedMsg struct {
		id  string
		to  destination
		err error
	}
	promotedMsg struct {
		local, id string
		to        destination
		createErr error // the Linear Issue does not exist
		writeErr  error // the Linear Issue exists, and the local file did not change
	}
)

// openBox opens the capture box. The Destination is local each time.
func (m Model) openBox() (tea.Model, tea.Cmd) {
	m.mode = modeBox
	m.box.to = destination{}
	m.box.title.SetValue("")
	return m, tea.Batch(m.box.title.Focus(), m.readRecent())
}

func (m Model) boxKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeNormal
		m.box.title.Blur()
		return m, nil
	case "tab":
		return m.openDests(modeDests)
	case "enter":
		title := strings.TrimSpace(m.box.title.Value())
		m.mode = modeNormal
		m.box.title.Blur()
		if title == "" {
			return m, nil
		}
		return m, m.addIssue(title, m.box.to)
	}
	var cmd tea.Cmd
	m.box.title, cmd = m.box.title.Update(msg)
	return m, cmd
}

// openPromote opens the Destination list for the local Issue id.
func (m Model) openPromote(id string) (tea.Model, tea.Cmd) {
	m.box.promote = id
	m2, cmd := m.openDests(modePromote)
	return m2, tea.Batch(cmd, m.readRecent())
}

// openDests opens the Destination list in mode, modeDests or modePromote.
func (m Model) openDests(mode mode) (Model, tea.Cmd) {
	m.mode = mode
	m.box.filter.SetValue("")
	m.box.cur = 0
	cmd := m.box.filter.Focus()
	if !m.dests.asked {
		m.dests.asked, m.dests.waiting = true, 1
		cmd = tea.Batch(cmd, m.loadDests())
	}
	return m, cmd
}

// visibleDests lists the Destination rows that match the filter, best match first.
func (m Model) visibleDests() []destRow {
	rows := m.dests.rows(m.mode == modeDests)
	labels := make([]string, len(rows))
	for i, r := range rows {
		labels[i] = r.to.label()
	}
	var out []destRow
	for _, i := range rank(m.box.filter.Value(), labels) {
		out = append(out, rows[i])
	}
	return out
}

// destKey handles the keys of the Destination list. The list goes back to
// the capture box, or for a Promote to the work view.
func (m Model) destKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	vis := m.visibleDests()
	back := modeBox
	if m.mode == modePromote {
		back = modeNormal
	}
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = back
		m.box.filter.Blur()
	case "down", "ctrl+n":
		m.box.cur = max(min(m.box.cur+1, len(vis)-1), 0)
	case "up", "ctrl+p":
		m.box.cur = max(m.box.cur-1, 0)
	case "enter":
		if m.box.cur >= len(vis) {
			return m, nil
		}
		to := vis[m.box.cur].to
		promoting := m.mode == modePromote
		m.mode = back
		m.box.filter.Blur()
		if promoting {
			return m, m.promote(m.box.promote, to)
		}
		m.box.to = to
	default:
		var cmd tea.Cmd
		m.box.filter, cmd = m.box.filter.Update(msg)
		m.box.cur = 0
		return m, cmd
	}
	return m, nil
}

func (m Model) loadDests() tea.Cmd {
	wss := m.tr.deps.Workspaces
	return func() tea.Msg {
		w, err := wss()
		return destWorkspacesMsg{wss: w, err: err}
	}
}

// loadTeamDests lists a workspace's teams, and each team's open Projects
// after it. A team whose Projects fail still lists, as a Destination with no Project.
func loadTeamDests(api LinearAPI, w linear.Workspace) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		teams, err := api.Teams(ctx)
		projects := make([][]linear.ProjectInfo, len(teams))
		errs := make([]error, len(teams))
		slots := make(chan struct{}, teamReads)
		var wg sync.WaitGroup
		for i, t := range teams {
			wg.Go(func() {
				slots <- struct{}{}
				defer func() { <-slots }()
				projects[i], errs[i] = api.TeamProjects(ctx, t.ID)
			})
		}
		wg.Wait()
		var dests []destination
		for i, t := range teams {
			d := destination{Workspace: w.ID, WorkspaceName: w.Name, TeamID: t.ID, TeamKey: t.Key}
			dests = append(dests, d)
			for _, p := range linear.ProjectOrder(projects[i]) {
				d.ProjectID, d.ProjectName = p.ID, p.Name
				dests = append(dests, d)
			}
		}
		return destTeamsMsg{ws: w, dests: dests, err: cmp.Or(append([]error{err}, errs...)...)}
	}
}

func (m Model) readRecent() tea.Cmd {
	path := m.opts.Destinations
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		recent, err := readRecent(path)
		return destRecentMsg{recent: recent, err: err}
	}
}

func readRecent(path string) ([]destination, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recent []destination
	return recent, json.Unmarshal(data, &recent)
}

// saveRecent puts d first in the file. It reads the file again, so a pick in
// another Board since this one read it stays.
func (m Model) saveRecent(d destination) tea.Cmd {
	path := m.opts.Destinations
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		old, _ := readRecent(path) // a broken file starts over
		recent := pushRecent(old, d)
		return destRecentMsg{recent: recent, err: config.WriteJSON(path, recent)}
	}
}

// addIssue creates the Issue at the Destination. A Linear Issue is assigned
// to the user and gets its team's default status.
func (m Model) addIssue(title string, to destination) tea.Cmd {
	if to.local() {
		dir := m.opts.LocalIssues
		return func() tea.Msg {
			if dir == "" {
				return addedMsg{to: to, err: errors.New("no Local Tracker folder")}
			}
			is, err := localissues.Store{Dir: dir}.Create(title, "", "")
			return addedMsg{id: is.ID(), to: to, err: err}
		}
	}
	api := m.tr.api(linear.Workspace{ID: to.Workspace, Name: to.WorkspaceName})
	n := linear.NewIssue{TeamID: to.TeamID, ProjectID: to.ProjectID, Title: title, AssignToMe: true}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		is, err := api.Create(ctx, n)
		return addedMsg{id: is.Identifier, to: to, err: err}
	}
}

// promote creates a Linear Issue at the Destination from the local Issue id,
// then cancels the local Issue. It reads the file again, so an edit since
// the last list goes to Linear.
func (m Model) promote(id string, to destination) tea.Cmd {
	store := localissues.Store{Dir: m.opts.LocalIssues}
	api := m.tr.api(linear.Workspace{ID: to.Workspace, Name: to.WorkspaceName})
	return func() tea.Msg {
		is, err := store.Get(id)
		if err != nil {
			return promotedMsg{local: id, to: to, createErr: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n := linear.NewIssue{TeamID: to.TeamID, ProjectID: to.ProjectID, Title: is.Title, Description: is.Body, AssignToMe: true}
		created, err := api.Create(ctx, n)
		if err != nil {
			return promotedMsg{local: id, to: to, createErr: err}
		}
		return promotedMsg{local: id, id: created.Identifier, to: to, writeErr: store.Promote(id, created.Identifier)}
	}
}

// boxMsg handles the messages of the capture box. ok is false for any other message.
func (m Model) boxMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	ds := &m.dests
	switch msg := msg.(type) {
	case destWorkspacesMsg:
		if msg.err != nil {
			ds.waiting = 0
			ds.errs[""] = "Linear workspaces: " + msg.err.Error()
			return m, nil, true
		}
		ds.wss, ds.waiting = msg.wss, len(msg.wss)
		var cmds []tea.Cmd
		for _, w := range msg.wss {
			cmds = append(cmds, loadTeamDests(m.tr.api(w), w))
		}
		return m, tea.Batch(cmds...), true
	case destTeamsMsg:
		ds.waiting--
		ds.linear[msg.ws.ID] = msg.dests
		if msg.err != nil {
			ds.errs[msg.ws.ID] = msg.ws.Name + ": " + msg.err.Error()
		}
	case destRecentMsg:
		if msg.recent != nil {
			ds.recent = msg.recent
		}
		if msg.err != nil {
			m.setStatus("recent Destinations: "+msg.err.Error(), true)
		}
	case addedMsg:
		if msg.err != nil {
			m.setStatus("add Issue at "+msg.to.label()+": "+msg.err.Error(), true)
			return m, nil, true
		}
		m.setStatus("added "+msg.id, false)
		if msg.to.local() {
			return m, m.loadMine(), true
		}
		ds.recent = pushRecent(ds.recent, msg.to)
		return m, tea.Batch(m.loadMine(), m.saveRecent(msg.to)), true
	case promotedMsg:
		switch {
		case msg.createErr != nil:
			m.setStatus("promote "+msg.local+" to "+msg.to.label()+": "+msg.createErr.Error(), true)
			return m, nil, true
		case msg.writeErr != nil:
			m.setStatus("created "+msg.id+", but "+msg.local+" did not change: "+msg.writeErr.Error(), true)
		default:
			m.setStatus("promoted "+msg.local+" to "+msg.id, false)
		}
		ds.recent = pushRecent(ds.recent, msg.to)
		return m, tea.Batch(m.loadMine(), m.saveRecent(msg.to)), true
	default:
		return m, nil, false
	}
	return m, nil, true
}

// boxFooter is the capture box's two lines.
func (m Model) boxFooter() string {
	hints := dim.Render("   tab: change   enter: add   esc: cancel")
	return " " + m.box.title.View() + "\n" + " to: " + bold.Render(m.box.to.label()) + hints
}

func (m Model) destList() string {
	lines := []string{" " + m.box.filter.View()}
	vis := m.visibleDests()
	w := 0
	for _, r := range vis {
		w = max(w, lipgloss.Width(r.to.label()))
	}
	// A long Project name gives way to the tag: the marker and the tag take 13 columns.
	w = min(w, max(m.width-13, 12))
	for i, r := range vis {
		lines = append(lines, m.row(i == m.box.cur, pad(r.to.label(), w)+"  "+r.tag, false))
	}
	if m.dests.waiting > 0 {
		lines = append(lines, dim.Render("   loading…"))
	}
	for _, e := range m.dests.failures() {
		lines = append(lines, bad.Render("   "+e))
	}
	return strings.Join(lines, "\n")
}
