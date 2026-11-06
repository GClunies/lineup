// Package board is the terminal Board: a bubbletea view of the Scheduler's
// BoardView. It never writes the store. Every socket call runs in a tea.Cmd.
package board

import (
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// Client is the part of scheduler.Client that the Board uses.
type Client interface {
	Do(req scheduler.Request, out any) error
	Watch(f func(scheduler.BoardView)) error
}

// Options are the hooks that the caller wires in.
type Options struct {
	// StartSkills are the skills that the Linear browser offers when it starts
	// a Task. Empty means it starts at once with no skill.
	StartSkills []string
	// Zoom zooms the agent pane in this tab, and back. The Hub provides it.
	Zoom func()
	// Linear opens on the Linear browser instead of the Board.
	Linear bool
	// Review opens on the review tab instead of the Board. Its Reviews tab
	// lists the review Tasks, and its other tabs list PRs.
	Review  bool
	Reviews Reviews
	// Notify shows one alert line as a desktop toast. The Hub sets it for the
	// work and review boards, and each toasts only its own Kind's alerts.
	Notify func(text string)
	// Open opens a URL. The default runs `open`.
	Open func(url string) error
	// Client replaces the socket client, for tests.
	Client Client
	Now    func() time.Time
	// Tracker holds the calls of the tracker browser. Tests replace the
	// Linear calls. Sources lists the GitHub and Local tabs.
	Tracker Tracker
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
)

const (
	previewEvery = 500 * time.Millisecond
	capFull      = "wip cap full"
)

var groupOrder = []string{"Needs you", "Working", "Waiting", "Queued", "Done"}

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
	sel     string               // selected Task ID
	online  bool
	status  string
	statusE bool

	view          viewID
	mode          mode
	pendingTask   string // Task the current prompt is about
	pendingOp     string // close or archive, for the y/n prompt
	width, height int

	preview    bool
	previewTxt string
	previewGen int

	detail     bool
	detailGen  int
	detailTask string
	vp         viewport.Model

	tr *tracker
	rv *review

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
	switch {
	case opts.Linear:
		m.view = viewTracker
	case opts.Review:
		m.view, m.kind = viewReview, lifecycle.KindReview
	}
	m.setBoard(v)
	m.alerted = map[string]string{}
	scheduler.Alerts(m.alerted, v.OfKind(m.kind)) // the view it opens on is not news
	return m
}

func (m *Model) setBoard(v scheduler.BoardView) {
	m.board = v
	m.rv.reviews = reviewTasks(v.Tasks)
	m.tasks = m.tasks[:0]
	own := v.OfKind(m.kind).Tasks
	for _, g := range groupOrder {
		for _, t := range own {
			if t.Group == g {
				m.tasks = append(m.tasks, t)
			}
		}
	}
	if _, ok := m.current(); !ok && len(m.tasks) > 0 {
		m.sel = m.tasks[0].ID
	}
}

func (m Model) index() int {
	for i, t := range m.tasks {
		if t.ID == m.sel {
			return i
		}
	}
	return -1
}

func (m Model) current() (scheduler.TaskView, bool) {
	if i := m.index(); i >= 0 {
		return m.tasks[i], true
	}
	return scheduler.TaskView{}, false
}

func (m Model) Init() tea.Cmd {
	switch {
	case m.opts.Linear:
		return m.loadWorkspaces()
	case m.opts.Review:
		return m.loadReviews()
	}
	return nil
}

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
		if k == "y" {
			return m, m.op(m.pendingOp, scheduler.Request{Op: m.pendingOp, Task: m.pendingTask})
		}
		m.setStatus(m.pendingOp+" cancelled", false)
		return m, nil
	case modeCap:
		return m.capKey(k)
	}

	if m.view == viewTracker && m.tr.capturing() {
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
	}
	switch m.view {
	case viewTracker:
		return m.trackerKey(msg)
	case viewReview:
		return m.reviewKey(msg)
	}
	return m.boardKey(msg)
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
	switch k {
	case "j", "down":
		if i := m.index(); i >= 0 && i+1 < len(m.tasks) {
			m.sel = m.tasks[i+1].ID
		}
	case "k", "up":
		if i := m.index(); i > 0 {
			m.sel = m.tasks[i-1].ID
		}
	case "z":
		if m.opts.Zoom != nil {
			m.opts.Zoom()
		}
	case "R":
		return m, m.op("refresh", scheduler.Request{Op: "poll"})
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
		if ok && reopens(t) {
			return m, m.reviewPR(t.PRURL, t.ID)
		}
		if ok {
			return m, m.op("focus", scheduler.Request{Op: "focus", Task: t.ID})
		}
	case "m":
		if ok && t.StageKey == lifecycle.ReadyToLand.Key() {
			return m, m.op("merge", scheduler.Request{Op: "merge", Task: t.ID})
		}
	case "x":
		if ok {
			m.mode, m.pendingTask, m.pendingOp = modeConfirm, t.ID, "close"
			if t.Group == lifecycle.GroupDone.String() {
				m.pendingOp = "archive"
			}
		}
	case "s":
		if ok && t.Group == "Queued" {
			return m, m.op("start", scheduler.Request{Op: "start", Task: t.ID})
		}
	case "o":
		if ok {
			if u := firstNonEmpty(t.PRURL, t.IssueURL); u != "" {
				open := m.opts.Open
				return m, func() tea.Msg { return opMsg{label: "open", err: open(u)} }
			}
			m.setStatus("no URL for "+t.ID, true)
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
	alerts := scheduler.Alerts(m.alerted, v.OfKind(m.kind))
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
