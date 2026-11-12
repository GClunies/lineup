// Package board is the terminal Board: a bubbletea view of the Scheduler's
// BoardView. It never writes the store. Every socket call runs in a tea.Cmd.
package board

import (
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/scheduler"
)

// Client is the part of scheduler.Client that the Board uses.
type Client interface {
	Do(req scheduler.Request, out any) error
	Watch(f func(scheduler.BoardView) bool) error
}

// Options are the hooks that the caller wires in.
type Options struct {
	// StartSkills are the skills that the Linear browser offers when it starts
	// a Task. Empty means it starts at once with no skill.
	StartSkills []string
	// Zoom zooms the agent pane in this tab, and back, or goes to the tab that
	// has the agent. The Hub provides it.
	Zoom func()
	// Reviews holds the PR views of the review view. Its Reviews tab lists
	// the review Tasks, and its other tabs list PRs.
	Reviews Reviews
	// Notify shows one alert line as a desktop toast. It fires for every
	// Kind, whichever view is up.
	Notify func(text string)
	// Open opens a URL. The default runs `open`.
	Open func(url string) error
	// Client replaces the socket client, for tests.
	Client Client
	Now    func() time.Time
	// Tracker holds the calls of the tracker browser. Tests replace the
	// Linear calls. Sources lists the GitHub and Local tabs.
	Tracker Tracker
	// LocalIssues is the Local Tracker folder that the work view reads,
	// whatever local_tracker says. Empty reads no local Issues.
	LocalIssues string
	// Destinations is destinations.json in the state folder. It keeps the
	// last Destinations that the user picked. Empty keeps them for this run.
	Destinations string
}

type viewID int

const (
	viewBoard viewID = iota
	viewTracker
	viewReview
)

type mode int

const (
	modeNormal mode = iota
	modeConfirm
	modeCap
	modeHelp
	modeBox   // the capture box
	modeDests // the capture box's Destination list
)

const (
	previewEvery = 500 * time.Millisecond
	capFull      = "wip cap full"
)

var groupOrder = []string{"Needs you", "Working", "Waiting", "Queued", groupReady, "Done", groupBacklog}

type (
	boardMsg  scheduler.BoardView
	onlineMsg bool
	opMsg     struct {
		label, task string
		note        string // follows "ok" in the status line
		err         error
	}
	captureMsg struct {
		gen  int
		text string
		err  error
	}
	captureTick int
	detailMsg   struct {
		gen  int
		text string
		err  error
	}
)

type Model struct {
	opts   Options
	client Client

	board   scheduler.BoardView
	kind    lifecycle.Kind       // the Kind of the Tasks that this board shows
	tasks   []scheduler.TaskView // the Tasks of kind, in group order
	issues  []issueRow           // the Issue rows of the work view, in group order
	sel     string               // the ID of the selected Task or Issue row
	online  bool
	status  string
	statusE bool

	view          viewID
	loaded        [3]bool // the views whose first load ran
	mode          mode
	pendingTask   string // Task the current prompt is about
	pendingOp     string // close, archive, or reviews-skip, for the y/n prompt
	pendingURL    string // the PR that a skip prompt is about
	pendingRef    string // that PR's short name
	pendingAsk    string
	width, height int

	preview    bool
	previewTxt string
	previewGen int

	detail     bool
	detailGen  int
	detailTask string
	vp         viewport.Model

	tr    *tracker
	rv    *review
	mine  mine
	box   captureBox
	dests destinations

	alerted map[string]string // Task ID to the Attention that last toasted
}

// New builds a Model that paints from the given view.
func New(v scheduler.BoardView, opts Options) Model {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Open == nil {
		opts.Open = func(u string) error { return exec.Command("open", u).Start() }
	}
	m := Model{opts: opts, client: opts.Client, width: 100, height: 30, vp: viewport.New(100, 28)}
	m.tr = newTracker(opts.Tracker)
	m.rv = newReview(opts.Reviews)
	m.mine = mine{lin: map[string][]linear.Issue{}, fresh: map[string]bool{}}
	m.box = newCaptureBox()
	m.dests = destinations{linear: map[string][]destination{}, errs: map[string]string{}}
	m.setBoard(v)
	m.alerted = map[string]string{}
	scheduler.Alerts(m.alerted, v) // what is on the Board now is not news
	return m
}

func (m *Model) setBoard(v scheduler.BoardView) {
	m.board = v
	m.rv.holds = github.Holds{Tasks: reviewTasks(v.Tasks), Skips: v.Skips}
	m.tasks = m.tasks[:0]
	own := v.OfKind(m.kind).Tasks
	for _, g := range groupOrder {
		for _, t := range own {
			if t.Group == g {
				m.tasks = append(m.tasks, t)
			}
		}
	}
	m.issues = nil
	if m.kind == lifecycle.KindBuild {
		m.issues = issueRows(m.mine.linearIssues(), m.mine.loc, v.Tasks)
	}
	if ids := m.rowIDs(); !slices.Contains(ids, m.sel) && len(ids) > 0 {
		m.sel = ids[0]
	}
}

// rowIDs lists the IDs of the Task and Issue rows in screen order.
func (m Model) rowIDs() []string {
	var ids []string
	for _, g := range groupOrder {
		for _, t := range m.tasks {
			if t.Group == g {
				ids = append(ids, t.ID)
			}
		}
		for _, r := range m.issues {
			if r.Group == g {
				ids = append(ids, r.ID)
			}
		}
	}
	return ids
}

func (m Model) current() (scheduler.TaskView, bool) {
	for _, t := range m.tasks {
		if t.ID == m.sel {
			return t, true
		}
	}
	return scheduler.TaskView{}, false
}

// Init loads the Issue rows and fetches them again every minute.
func (m Model) Init() tea.Cmd { return tea.Batch(m.loadMine(), tickMine()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case boardMsg:
		m.online = true
		m.setBoard(scheduler.BoardView(msg))
		return m, m.toasts(scheduler.BoardView(msg))
	case onlineMsg:
		m.online = bool(msg)
	case opMsg:
		return m.afterOp(msg)
	case captureMsg:
		if msg.gen == m.previewGen && m.preview {
			if msg.err != nil {
				m.previewTxt = msg.err.Error()
			} else {
				m.previewTxt = msg.text
			}
			return m, m.tickCapture()
		}
	case captureTick:
		if int(msg) == m.previewGen && m.preview {
			return m, m.capture()
		}
	case detailMsg:
		return m.afterDetail(msg)
	case tea.KeyMsg:
		return m.key(msg)
	default:
		if m2, cmd, ok := m.tracker(msg); ok {
			return m2, cmd
		}
		if m2, cmd, ok := m.reviewMsg(msg); ok {
			return m2, cmd
		}
		if m2, cmd, ok := m.mineMsg(msg); ok {
			return m2, cmd
		}
		if m2, cmd, ok := m.boxMsg(msg); ok {
			return m2, cmd
		}
	}
	return m, nil
}

func (m *Model) layout() {
	m.vp.Width, m.vp.Height = m.width, max(m.height-3, 1)
	m.tr.vp.Width, m.tr.vp.Height = m.width, max(m.height-3, 1)
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch m.mode {
	case modeHelp:
		m.mode = modeNormal
		return m, nil
	case modeConfirm:
		m.mode = modeNormal
		if k == "y" && m.pendingOp == opSkip {
			return m, m.skipOp(opSkip, m.pendingURL, m.pendingRef)
		}
		if k == "y" {
			return m, m.op(m.pendingOp, scheduler.Request{Op: m.pendingOp, Task: m.pendingTask})
		}
		m.setStatus(strings.TrimPrefix(m.pendingOp, "reviews-")+" cancelled", false)
		return m, nil
	case modeCap:
		return m.capKey(k)
	case modeBox:
		return m.boxKey(msg)
	case modeDests:
		return m.destKey(msg)
	}

	if (m.view == viewTracker && m.tr.capturing()) || m.picking() {
		return m.trackerKey(msg)
	}
	if m.view == viewReview && m.rv.filtering {
		return m.reviewKey(msg)
	}
	// A status message stays until the next key, then the key hints come back.
	m.status = ""
	if k == "ctrl+c" || k == "q" {
		return m, tea.Quit
	}
	switch k {
	case "?":
		m.mode = modeHelp
		return m, nil
	case "1":
		return m.show(viewTracker, m.kind)
	case "2":
		return m.show(viewBoard, lifecycle.KindBuild)
	case "3":
		return m.show(viewReview, lifecycle.KindReview)
	}
	switch m.view {
	case viewTracker:
		return m.trackerKey(msg)
	case viewReview:
		return m.reviewKey(msg)
	}
	return m.boardKey(msg)
}

// show switches to a view. The Linear browser and the PR search load on the
// first visit, so a Hub that never opens them makes no call.
func (m Model) show(v viewID, kind lifecycle.Kind) (tea.Model, tea.Cmd) {
	m.view, m.kind = v, kind
	m.detail, m.preview = false, false
	m.previewGen++
	m.setBoard(m.board)
	if m.loaded[v] {
		return m, nil
	}
	m.loaded[v] = true
	switch v {
	case viewTracker:
		return m, m.loadWorkspaces()
	case viewReview:
		return m, m.loadReviews()
	}
	return m, nil
}

// boardKey handles the keys of the Task rows. The work board and the review
// tab's Reviews tab share it.
func (m Model) boardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.detail {
		switch k {
		case "f", "esc":
			m.detail = false
			m.detailGen++
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	t, ok := m.current()
	r, isRow := m.currentIssue()
	switch k {
	case "j", "down":
		ids := m.rowIDs()
		if i := slices.Index(ids, m.sel); i >= 0 && i+1 < len(ids) {
			m.sel = ids[i+1]
		}
	case "k", "up":
		ids := m.rowIDs()
		if i := slices.Index(ids, m.sel); i > 0 {
			m.sel = ids[i-1]
		}
	case "z":
		if m.opts.Zoom != nil {
			m.opts.Zoom()
		}
	case "R":
		return m, m.op("refresh", scheduler.Request{Op: "poll"})
	case "n":
		if m.view == viewBoard {
			return m.openBox()
		}
	case "p":
		m.preview = !m.preview
		m.previewGen++
		m.previewTxt = ""
		if m.preview && ok {
			return m, m.capture()
		}
	case "f":
		if !ok {
			return m, nil
		}
		m.detail, m.detailTask = true, t.ID
		m.detailGen++
		m.vp.SetContent("loading…")
		m.vp.GotoTop()
		return m, m.loadDetail()
	case "enter":
		if ok {
			return m, m.openTask(t.ID)
		}
	case "m":
		if ok && t.StageKey == lifecycle.ReadyToLand.Key() {
			return m, m.op("merge", scheduler.Request{Op: "merge", Task: t.ID})
		}
	case "x":
		if ok {
			m.mode, m.pendingTask, m.pendingOp = modeConfirm, t.ID, "close"
			m.pendingAsk = "close " + t.ID
			if t.Group == lifecycle.GroupDone.String() {
				if t.Tab {
					m.pendingAsk = "close " + t.ID + "'s tab and worktree"
				} else {
					m.pendingOp, m.pendingAsk = "archive", "archive "+t.ID
				}
			}
		}
	case "s":
		if ok && t.Group == "Queued" {
			return m, m.op("start", scheduler.Request{Op: "start", Task: t.ID})
		}
		if isRow {
			return m.startRow(r)
		}
	case "S":
		if ok && t.Kind == lifecycle.KindReview {
			return m.toggleSkip(t.PRURL, urlRef(t.PRURL))
		}
	case "o":
		if ok {
			if u := firstNonEmpty(t.PRURL, t.IssueURL); u != "" {
				open := m.opts.Open
				return m, func() tea.Msg { return opMsg{label: "open", err: open(u)} }
			}
			m.setStatus("no URL for "+t.ID, true)
		}
		if isRow {
			return m, m.openRow(r)
		}
	}
	return m, nil
}

func (m Model) capKey(k string) (tea.Model, tea.Cmd) {
	id := m.pendingTask
	m.mode = modeNormal
	switch k {
	case "o":
		return m, m.op("start", scheduler.Request{Op: "start", Task: id, OverCap: true})
	case "c":
		n := m.board.Cap + 1
		c := m.client
		return m, func() tea.Msg {
			if err := c.Do(scheduler.Request{Op: "cap", N: n}, nil); err != nil {
				return opMsg{label: "cap", task: id, err: err}
			}
			err := c.Do(scheduler.Request{Op: "start", Task: id}, nil)
			return opMsg{label: "start", task: id, err: err}
		}
	}
	m.setStatus("start cancelled", false)
	return m, nil
}

func (m Model) afterOp(msg opMsg) (tea.Model, tea.Cmd) {
	what := msg.label
	if msg.task != "" {
		what += " " + msg.task
	}
	switch {
	case msg.err == nil:
		ok := what + " ok"
		if msg.note != "" {
			ok += " · " + msg.note
		}
		m.setStatus(ok, false)
		if msg.label == "start" {
			return m, m.loadMine()
		}
	case msg.label == "start" && msg.err.Error() == capFull:
		m.mode, m.pendingTask = modeCap, msg.task
	default:
		m.setStatus(what+": "+msg.err.Error(), true)
	}
	return m, nil
}

func (m Model) afterDetail(msg detailMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.detailGen || !m.detail {
		return m, nil
	}
	if msg.err != nil {
		m.vp.SetContent(msg.err.Error())
		return m, nil
	}
	m.vp.SetContent(msg.text)
	return m, nil
}

func (m *Model) setStatus(s string, isErr bool) { m.status, m.statusE = s, isErr }

func (m Model) op(label string, req scheduler.Request) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		return opMsg{label: label, task: req.Task, err: c.Do(req, nil)}
	}
}

func (m Model) openTask(id string) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		var res scheduler.OpenResult
		err := c.Do(scheduler.Request{Op: "open", Task: id}, &res)
		return opMsg{label: "open", task: id, note: string(res.Action), err: err}
	}
}

func (m Model) capture() tea.Cmd {
	t, ok := m.current()
	if !ok {
		return nil
	}
	c, gen := m.client, m.previewGen
	return func() tea.Msg {
		var out string
		err := c.Do(scheduler.Request{Op: "capture", Task: t.ID}, &out)
		return captureMsg{gen: gen, text: out, err: err}
	}
}

func (m Model) tickCapture() tea.Cmd {
	gen := m.previewGen
	return tea.Tick(previewEvery, func(time.Time) tea.Msg { return captureTick(gen) })
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func (m Model) toasts(v scheduler.BoardView) tea.Cmd {
	alerts := scheduler.Alerts(m.alerted, v)
	if m.opts.Notify == nil || len(alerts) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, t := range alerts {
			m.opts.Notify(t.AlertText())
		}
		return nil
	}
}
