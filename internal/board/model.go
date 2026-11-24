// Package board is the terminal Board: a bubbletea view of the Scheduler's
// BoardView. It never writes the store. Every socket call runs in a tea.Cmd.
package board

import (
	"cmp"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/markdown"
	"github.com/gclunies/lineup/internal/scheduler"
)

// Client is the part of scheduler.Client that the Board uses.
type Client interface {
	Do(req scheduler.Request, out any) error
	Watch(f func(scheduler.BoardView) bool) error
}

// Options are the hooks that the caller wires in.
type Options struct {
	// StartSkills are the skills that the skill picker offers when s starts a
	// Task from an Issue row. Empty offers only "just go".
	StartSkills []string
	// Zoom zooms the agent pane in this tab, and back, or goes to the tab that
	// has the agent. The Hub provides it.
	Zoom func()
	// Notify shows one line as a desktop toast. It fires when a build Task
	// reaches Done, and for the free slot hint. A build Task in Needs you gets
	// no toast, because Status reports it. A review Task gives no alert.
	Notify func(text string)
	// Status reports how many build Tasks are in Needs you. It fires once at
	// start. It fires again when the set of those Tasks changes: a Task joins
	// or leaves the group, or a Task in it changes its Stage or its flag. Init
	// and Update call it, so the reports keep the order of the views. It must
	// not block. A nil Status reports nothing.
	Status func(n int)
	// Open opens a URL. The default runs `open`.
	Open func(url string) error
	// Client replaces the socket client, for tests.
	Client Client
	Now    func() time.Time
	// Linear holds the calls to Linear. Tests replace them.
	Linear Linear
	// GitHub holds the calls to GitHub for the GitHub Issue rows.
	GitHub GitHub
	// Repos lists the checkouts that the Repo picker offers for an Issue of the
	// Kind: the checkouts whose Tracker is the Kind.
	Repos func(kind string) []string
	// LocalIssues is the Local Tracker folder that the work view reads,
	// whatever local_tracker says. Empty reads no local Issues.
	LocalIssues string
	// Teams are the Linear team keys whose unassigned Issues show in Team.
	// Empty reads none.
	Teams []string
	// TeamRepos lists the Repo keys, owner/repo, whose teams list holds the
	// Linear team key. Nil lists none.
	TeamRepos func(teamKey string) []string
	// LocalPrefixes are the Repo prefixes of the Local Issues, besides local.
	LocalPrefixes []string
	// Destinations is destinations.json in the state folder. It keeps the
	// last Destinations that the user picked. Empty keeps them for this run.
	Destinations string
	// Board is the layout of the sections. The zero value is the default.
	Board config.Board
	// ConfigErr is a problem with the config file. The Board shows it on its
	// own line until it quits, whatever other status shows.
	ConfigErr string
}

type mode int

const (
	modeNormal mode = iota
	modeConfirm
	modeCap
	modeHelp
	modeBox     // the capture box
	modeDests   // the capture box's Destination list
	modePromote // the Destination list that P opens, with no local row
	modeSkill   // the skill picker that s opens on an Issue row
	modeRepo    // the Repo picker, when the Issue has no Repo mapping
	modeStatus  // the status list that c opens on an Issue row
	modeFilter  // typing the work view's filter
)

const (
	previewEvery = 500 * time.Millisecond
	capFull      = "wip cap full"
)

type (
	boardMsg  scheduler.BoardView
	onlineMsg bool
	opMsg     struct {
		label, task string
		note        string // follows "ok" in the status line
		err         error
		// add is the add of a start, which the cap prompt sends again.
		add scheduler.Request
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
	tasks   []scheduler.TaskView // the build Tasks, in group order
	issues  []issueRow           // the Issue rows of the work view, in group order
	sel     string               // the ID of the selected Task or Issue row, or a heading's key
	lay     sections             // the order of the sections, and which are closed or hidden
	online  bool
	status  string
	statusE bool

	mode          mode
	pendingTask   string            // Task the current prompt is about
	pendingAdd    scheduler.Request // the add of the start that the cap prompt is about
	pendingOp     string            // close or archive, for the y/n prompt
	pendingAsk    string
	width, height int

	menu  menu // the actions that a tap on a row opens; empty when closed
	mouse bool // the last input was the mouse, so a prompt opens as a menu

	preview    bool
	previewTxt string
	previewGen int

	detail     bool
	detailGen  int
	detailTask string
	vp         viewport.Model
	md         *markdown.Renderer // renders an Issue's description in the detail pane

	lin        linearClients
	mine       mine
	box        captureBox
	dests      destinations
	st         starter
	pick       issueRow // the Issue row that the skill, Repo, or status list is about
	sl         statusList
	filter     textinput.Model // the work view's filter
	unfiltered string          // the selection when the filter opened
	want       string          // the row to select once it loads, such as a new local Issue

	alerted  map[string]string // Task ID to the Attention that last toasted
	reported map[string]string // Task ID to the Attention of each build or QA Task in Needs you that the last Status report counted
}

// New builds a Model that paints from the given view.
func New(v scheduler.BoardView, opts Options) Model {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Open == nil {
		opts.Open = func(u string) error { return exec.Command("open", u).Start() }
	}
	if opts.Repos == nil {
		opts.Repos = func(string) []string { return nil }
	}
	m := Model{opts: opts, client: opts.Client, width: 100, height: 30, vp: viewport.New(100, 28), md: markdown.New(true)}
	m.lin = newLinearClients(opts.Linear)
	m.st = newStarter()
	m.filter = newFilter()
	m.mine = mine{lin: map[string][]linear.Issue{}, team: map[string][]linear.Issue{}, wss: map[string]linear.Workspace{}, fresh: map[string]bool{}}
	m.box = newCaptureBox()
	m.lay = newSections(opts.Board)
	m.dests = destinations{linear: map[string][]destination{}, errs: map[string]string{}}
	m.setBoard(v)
	m.alerted = map[string]string{}
	scheduler.Alerts(m.alerted, v) // what is on the Board now is not news
	m.reported = needsYouNow(v)    // Init reports it
	return m
}

func (m *Model) setBoard(v scheduler.BoardView) {
	m.board = v
	m.tasks = m.tasks[:0]
	own := slices.DeleteFunc(slices.Clone(v.Tasks), func(t scheduler.TaskView) bool { return !onBoard(t) })
	// Follow-up holds only build Task rows: open is keyed by Issue ID, which
	// is a build Task's own ID, and a QA Task's ID adds QA- (PROD-BOARD-002).
	open := m.mine.openStates()
	for i, t := range own {
		if st, ok := open[t.ID]; ok && t.StageKey == lifecycle.Done.Key() {
			own[i].Group, own[i].Note = groupFollowUp, st
		}
	}
	for _, g := range m.lay.order {
		for _, t := range own {
			if t.Group == g {
				m.tasks = append(m.tasks, t)
			}
		}
	}
	m.issues = m.issueRows(v.Tasks)
	slices.SortStableFunc(m.issues, func(a, b issueRow) int { return cmp.Compare(m.lay.rank(a.Group), m.lay.rank(b.Group)) })
	if q := m.query(); q != "" {
		m.tasks = matching(q, m.tasks, func(t scheduler.TaskView) string { return t.ID + "  " + t.Title })
		m.issues = matching(q, m.issues, func(r issueRow) string { return r.ID + "  " + r.Title })
	}
	m.keepSelection()
}

// keepSelection picks a new selection when the old one is not on the screen.
func (m *Model) keepSelection() {
	ids := m.rowIDs()
	if m.want != "" && slices.Contains(ids, m.want) {
		m.sel, m.want = m.want, ""
	}
	if !slices.Contains(ids, m.sel) && len(ids) > 0 {
		m.sel = m.fallback(ids)
	}
}

// fallback picks the selection when the old one is gone. A row that a closed
// group now hides selects that group's heading, and a row that the limit of
// an open group now hides selects its more line (PROD-BOARD-005). Any other
// loss selects the first Task or Issue row, or the first heading when no
// group is open.
func (m Model) fallback(ids []string) string {
	if g := m.groupOf(m.sel); g != "" {
		if m.shown(g) && !m.open(g) {
			return headingKey(g)
		}
		if slices.Contains(ids, moreKey(g)) {
			return moreKey(g)
		}
	}
	for _, id := range ids {
		if m.groupOf(id) != "" {
			return id
		}
	}
	return ids[0]
}

// groupOf names the group of the Task or Issue row id, or "" for no row.
func (m Model) groupOf(id string) string {
	for _, t := range m.tasks {
		if t.ID == id {
			return t.Group
		}
	}
	for _, r := range m.issues {
		if r.ID == id {
			return r.Group
		}
	}
	return ""
}

// open reports whether the group shows its rows. Under a filter every
// group with a match shows them.
func (m Model) open(g string) bool { return m.query() != "" || !m.lay.closed[g] }

// shown reports whether the group shows at all. Under a filter every group
// with a match shows, a hidden one too.
func (m Model) shown(g string) bool { return m.query() != "" || !m.lay.hidden[g] }

// limit is how many of the n rows of open group g show. An open Team shows
// at most teamRows until the user asks for every row, and the filter shows
// every match (PROD-BOARD-024).
func (m Model) limit(g string, n int) int {
	if g == groupTeam && !m.lay.full[g] && m.query() == "" {
		return min(n, teamRows)
	}
	return n
}

// groupIDs lists the IDs of the rows in group g.
func (m Model) groupIDs(g string) []string {
	var ids []string
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
	return ids
}

// rowIDs lists the selection stops in screen order. Each shown group with a
// row has its heading, except Needs you, and then its rows when it is open,
// up to its limit and then its more line (PROD-BOARD-013). Under a filter the
// stops are the matching rows only.
func (m Model) rowIDs() []string {
	var ids []string
	for _, g := range m.lay.order {
		rows := m.groupIDs(g)
		if len(rows) == 0 || !m.shown(g) {
			continue
		}
		if g != groupNeedsYou && m.query() == "" {
			ids = append(ids, headingKey(g))
		}
		if m.open(g) {
			k := m.limit(g, len(rows))
			ids = append(ids, rows[:k]...)
			if k < len(rows) {
				ids = append(ids, moreKey(g))
			}
		}
	}
	return ids
}

// toggleGroup opens the group if it is closed, and closes it if it is open.
func (m Model) toggleGroup(g string) Model {
	m.lay = m.lay.withClosed(g, m.open(g))
	return m
}

// showAll shows every row of group g for this run, and selects the first row
// that its limit hid (PROD-BOARD-024).
func (m Model) showAll(g string) Model {
	ids := m.groupIDs(g)
	m.sel = ids[m.limit(g, len(ids))]
	m.lay = m.lay.withFull(g)
	return m
}

// toggleDone hides Done if it shows, and shows it, open, if it is hidden.
func (m Model) toggleDone() Model {
	hide := !m.lay.hidden[groupDone]
	m.lay = m.lay.withHidden(groupDone, hide)
	if !hide {
		m.lay = m.lay.withClosed(groupDone, false)
	}
	m.keepSelection()
	return m
}

// restorable reports whether open gives the Task a Worker: a started Task
// with no live Worker, or a Done build Task with no tab, which resumes in its
// kept worktree or read-only in the main checkout.
func restorable(t scheduler.TaskView) bool {
	st, err := lifecycle.ParseStage(t.StageKey)
	return err == nil && !t.Live && (st.Started() || st == lifecycle.Done && t.Kind == lifecycle.KindBuild && !t.Tab)
}

func (m Model) current() (scheduler.TaskView, bool) {
	for _, t := range m.tasks {
		if t.ID == m.sel {
			return t, true
		}
	}
	return scheduler.TaskView{}, false
}

// Init reports the build Tasks that need the user, loads the Issue rows, and
// fetches them again every minute.
func (m Model) Init() tea.Cmd {
	m.sendStatus()
	return tea.Batch(m.loadMine(), tickMine())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case boardMsg:
		v := scheduler.BoardView(msg)
		m.online = true
		m.setBoard(v)
		if now := needsYouNow(v); !maps.Equal(now, m.reported) {
			m.reported = now
			m.sendStatus()
		}
		return m, m.toasts(v)
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
	case statusesMsg:
		return m.openStatuses(msg), nil
	case tea.KeyMsg:
		m.mouse = false
		return m.key(msg)
	case tea.MouseMsg:
		return m.mouseMsg(msg)
	default:
		if m2, cmd, ok := m.startMsg(msg); ok {
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
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if len(m.menu.items) > 0 {
		return m.menuKey(msg)
	}
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
	case modeBox:
		return m.boxKey(msg)
	case modeDests, modePromote:
		return m.destKey(msg)
	case modeSkill:
		return m.skillKey(msg)
	case modeRepo:
		return m.repoKey(msg)
	case modeStatus:
		return m.statusKey(msg)
	case modeFilter:
		return m.filterKey(msg)
	}

	// A status message stays until the next key, then the key hints come back.
	m.status = ""
	if k == "ctrl+c" || k == "q" {
		return m, tea.Quit
	}
	if k == "?" {
		m.mode = modeHelp
		return m, nil
	}
	return m.boardKey(msg)
}

// boardKey handles the keys of the Task and Issue rows.
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
	if r, isRow := m.currentIssue(); isRow && r.takes(k) {
		return m.issueKey(r, k)
	}
	t, ok := m.current()
	switch k {
	case "j", "down":
		return m.move(1), nil
	case "k", "up":
		return m.move(-1), nil
	case "d":
		return m.toggleDone(), nil
	case "/":
		return m.startFilter()
	case "esc":
		if m.query() != "" {
			return m.clearFilter(), nil
		}
	case "z":
		if m.opts.Zoom != nil {
			m.opts.Zoom()
		}
	case "R":
		return m, m.op("refresh", scheduler.Request{Op: "poll"})
	case "n":
		return m.openBox()
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
		if g, isHeading := headingName(m.sel); isHeading {
			return m.toggleGroup(g), nil
		}
		if g, isMore := moreName(m.sel); isMore {
			return m.showAll(g), nil
		}
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
			if t.StageKey == lifecycle.Done.Key() {
				if t.Tab {
					m.pendingAsk = "close " + t.ID + "'s tab and worktree"
				} else {
					m.pendingOp, m.pendingAsk = "archive", "archive "+t.ID
				}
			}
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

// move moves the selection d stops down, or up for a negative d, and stops
// at the ends.
func (m Model) move(d int) Model {
	ids := m.rowIDs()
	if i := slices.Index(ids, m.sel); i >= 0 && i+d >= 0 && i+d < len(ids) {
		m.sel = ids[i+d]
	}
	return m
}

// issueKey handles a key that the Issue row r takes, as issueKinds lists it.
func (m Model) issueKey(r issueRow, k string) (tea.Model, tea.Cmd) {
	switch k {
	case "s":
		return m.startRow(r)
	case "f":
		return m.showIssue(r)
	case "c":
		return m, m.loadStatuses(r)
	case "o":
		return m, m.tracker(r).open()
	case "P":
		return m.openPromote(r.ID)
	}
	return m, nil
}

// capKey answers the cap prompt. o sends the start's add again over the cap,
// and c raises the cap by one, then sends the add again.
func (m Model) capKey(k string) (tea.Model, tea.Cmd) {
	add := m.pendingAdd
	m.mode = modeNormal
	switch k {
	case "o":
		add.OverCap = true
		return m, m.add(add)
	case "c":
		n, c, start := m.board.Cap+1, m.client, m.add(add)
		return m, func() tea.Msg {
			if err := c.Do(scheduler.Request{Op: "cap", N: n}, nil); err != nil {
				return opMsg{label: "cap", task: add.Issue, err: err}
			}
			return start()
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
		if msg.label == "start" || msg.label == "edit" || msg.label == "status" {
			return m, m.loadMine()
		}
	case msg.label == "start" && msg.err.Error() == capFull:
		m.mode, m.pendingAdd = modeCap, msg.add
		return m.withPromptMenu(), nil
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

// toasts is the command that toasts v: one line for each build Task that
// reached Ready to land or Done, then the free slot hint. Any other Task in Needs you
// gets no toast, because the status record reports it. The Alerts call still
// records its Attention, so the Task toasts when it reaches Done.
func (m Model) toasts(v scheduler.BoardView) tea.Cmd {
	alerts := slices.DeleteFunc(scheduler.Alerts(m.alerted, v), func(t scheduler.TaskView) bool { return inNeedsYou(t) && !t.ReadyToLand() })
	var texts []string
	for _, t := range alerts {
		texts = append(texts, t.AlertText())
	}
	if hint := m.slotHint(v, alerts); hint != "" {
		texts = append(texts, hint)
	}
	if m.opts.Notify == nil || len(texts) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, s := range texts {
			m.opts.Notify(s)
		}
		return nil
	}
}

// needsYouNow maps the ID of each build Task and QA Task of v in Needs you
// to its Attention, which holds the Stage and the flag (PROD-BOARD-018). A
// Task that changes its reason is a new entry, even when the count stays the
// same.
func needsYouNow(v scheduler.BoardView) map[string]string {
	now := map[string]string{}
	for _, t := range v.Tasks {
		if onBoard(t) && inNeedsYou(t) {
			now[t.ID] = t.Attention()
		}
	}
	return now
}

// sendStatus gives Status the count of the build Tasks and QA Tasks in Needs
// you that the last report named. Init and Update call it themselves, not through a command.
// Bubbletea runs each command on its own goroutine, so two reports in a row can
// reach the terminal in the wrong order, and the record then keeps the older
// count.
func (m Model) sendStatus() {
	if m.opts.Status != nil {
		m.opts.Status(len(m.reported))
	}
}

// slotHint names the first Up next row when a build Task in alerts reached Done
// and v has a free WIP slot. It never starts the Issue. The rows come from
// issueRows, not m.issues, so the filter never hides the hint.
func (m Model) slotHint(v scheduler.BoardView, alerts []scheduler.TaskView) string {
	free := v.Cap - v.Started
	if free <= 0 || !slices.ContainsFunc(alerts, func(t scheduler.TaskView) bool {
		return t.Kind == lifecycle.KindBuild && t.StageKey == lifecycle.Done.Key()
	}) {
		return ""
	}
	for _, r := range m.issueRows(v.Tasks) {
		if r.Group != groupUpNext {
			continue
		}
		slots := "slots"
		if free == 1 {
			slots = "slot"
		}
		return fmt.Sprintf("%d %s free, next %s: %s", free, slots, r.ID, r.Title)
	}
	return ""
}
