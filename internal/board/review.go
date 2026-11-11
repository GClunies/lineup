package board

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

const (
	reviewEvery  = 2 * time.Minute
	membersEvery = time.Hour
	keyMine      = "mine"
	// mergedFor is how long a merged PR stays on a tab that lists merged PRs.
	// Auto-merge can close a PR minutes after it opens.
	mergedFor = 24 * time.Hour
)

// ReviewAPI is the part of github.Client that the review tab uses.
type ReviewAPI interface {
	Viewer(ctx context.Context) (string, error)
	SearchPRs(ctx context.Context, viewer string, searches []github.Search) (map[string][]github.PullRequest, error)
	TeamMembers(ctx context.Context, teams []string) (map[string][]string, error)
}

// ReviewView is one tab of the review tab. Key names its cache file and its
// place in the saved rank. A Queue tab lists only the PRs that the queue
// reviews, in the order that its fills start them. It leaves out the PRs that
// an earlier queue in the config owns. The Tasks tab lists the review Tasks
// on the Board instead of PRs, so it has no Filter.
type ReviewView struct {
	Key, Name string
	Filter    github.Filter
	Queue     bool
	Tasks     bool
	// Merged lists the user's PRs that merged in the last mergedFor after
	// the open PRs, newest merge first.
	Merged bool
}

// Reviews holds the review tab's outside world. Tests replace it.
type Reviews struct {
	// Views are the configured tabs, which follow Reviews, My PRs, and
	// Requested.
	Views      []ReviewView
	API        ReviewAPI
	ReadCache  func(key string) ([]github.PullRequest, bool)
	WriteCache func(key string, prs []github.PullRequest)
	// Order reads the user's rank of the tabs, as keys.
	Order     func() ([]string, error)
	SaveOrder func(keys []string) error
	// Tick is tea.Tick. Tests stop the 2-minute refresh with it.
	Tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
}

var builtinReviewViews = []ReviewView{
	{Key: "tasks", Name: "Reviews", Tasks: true},
	{Key: keyMine, Name: "My PRs", Filter: github.Filter{Base: github.MyPRs}, Merged: true},
	{Key: "requested", Name: "Requested", Filter: github.Filter{Base: github.RequestedPRs}},
}

type review struct {
	deps Reviews
	tabs []ReviewView // in the user's rank
	tab  int
	rank []string

	lists   map[string][]github.PullRequest
	fresh   map[string]bool // keys with a live list, which a cached list never replaces
	gen     int
	loading bool
	err     string            // why the whole refresh failed
	failed  map[string]string // why each tab's search failed, by Key

	members   map[string][]string
	membersAt time.Time
	viewer    string // the user's login, read once
	// reviews are the review Tasks on the Board, which keep PRs out of a queue.
	reviews []github.ReviewTask

	sel       string // the selected PR's URL
	filter    textinput.Model
	filtering bool
}

func newReview(deps Reviews) *review {
	if deps.API == nil {
		deps.API = &github.Client{}
	}
	if deps.ReadCache == nil {
		deps.ReadCache = func(string) ([]github.PullRequest, bool) { return nil, false }
	}
	if deps.WriteCache == nil {
		deps.WriteCache = func(string, []github.PullRequest) {}
	}
	if deps.Order == nil {
		deps.Order = func() ([]string, error) { return nil, nil }
	}
	if deps.SaveOrder == nil {
		deps.SaveOrder = func([]string) error { return nil }
	}
	if deps.Tick == nil {
		deps.Tick = tea.Tick
	}
	f := textinput.New()
	f.Prompt = "/ "
	f.Cursor.SetMode(cursor.CursorStatic)
	return &review{deps: deps, tabs: slices.Concat(builtinReviewViews, deps.Views),
		lists: map[string][]github.PullRequest{}, fresh: map[string]bool{}, filter: f}
}

type (
	rvCacheMsg struct {
		lists map[string][]github.PullRequest
	}
	rvLiveMsg struct {
		gen       int
		lists     map[string][]github.PullRequest
		failed    map[string]string
		members   map[string][]string
		membersAt time.Time
		viewer    string
		err       error
	}
	rvRankMsg struct {
		keys []string
		err  error
	}
	rvRankSavedMsg struct{ err error }
	rvTickMsg      struct{}
)

func (r *review) curTab() ReviewView { return r.tabs[r.tab] }

// tabErr is why the open tab has no fresh list, or "".
func (r *review) tabErr() string { return cmp.Or(r.err, r.failed[r.curTab().Key]) }

// list is the tab's PRs, and false before the first list arrives.
func (r *review) list(tb ReviewView) ([]github.PullRequest, bool) {
	prs, ok := r.lists[tb.Key]
	if !tb.Queue || !ok {
		return prs, ok
	}
	owned := map[string]bool{}
	for _, v := range r.deps.Views {
		if v.Key == tb.Key {
			break
		}
		if v.Queue {
			for _, c := range github.Candidates(r.lists[v.Key], r.reviews) {
				owned[strings.ToLower(c.PR.URL)] = true
			}
		}
	}
	var out []github.PullRequest
	for _, c := range github.Candidates(prs, r.reviews) {
		if !owned[strings.ToLower(c.PR.URL)] {
			out = append(out, c.PR)
		}
	}
	return out, true
}

// rows are the open tab's PRs that match the search, best match first.
func (r *review) rows() []github.PullRequest {
	prs, _ := r.list(r.curTab())
	labels := make([]string, len(prs))
	for i, pr := range prs {
		labels[i] = prRef(pr) + " " + pr.Title + " " + pr.Author
	}
	var out []github.PullRequest
	for _, i := range rank(r.filter.Value(), labels) {
		out = append(out, prs[i])
	}
	return out
}

// selected returns the selected row's index, and the first row when the
// selected PR is not on the list.
func (r *review) selected(rows []github.PullRequest) int {
	for i, pr := range rows {
		if pr.URL == r.sel {
			return i
		}
	}
	return 0
}

func (r *review) current() (github.PullRequest, bool) {
	rows := r.rows()
	if len(rows) == 0 {
		return github.PullRequest{}, false
	}
	return rows[r.selected(rows)], true
}

// loadReviews paints every tab from the cache, reads the saved rank, searches
// GitHub, and starts the 2-minute refresh.
func (m Model) loadReviews() tea.Cmd {
	r := m.rv
	tabs := slices.Clone(r.tabs)
	read, order := r.deps.ReadCache, r.deps.Order
	return tea.Batch(
		func() tea.Msg {
			lists := map[string][]github.PullRequest{}
			for _, tb := range tabs {
				if tb.Tasks {
					continue
				}
				if prs, ok := read(tb.Key); ok {
					lists[tb.Key] = prs
				}
			}
			return rvCacheMsg{lists: lists}
		},
		func() tea.Msg {
			keys, err := order()
			return rvRankMsg{keys: keys, err: err}
		},
		m.refreshReviews(),
		m.reviewTick(),
	)
}

func (m Model) reviewTick() tea.Cmd {
	return m.rv.deps.Tick(reviewEvery, func(time.Time) tea.Msg { return rvTickMsg{} })
}

// refreshReviews searches every tab in one SearchPRs pass. It reads the members
// of the author teams first when they are an hour old. Only the newest
// refresh lands, so a slow answer never replaces a newer one. A tab whose
// search fails keeps its last list.
func (m Model) refreshReviews() tea.Cmd {
	r := m.rv
	r.gen++
	r.loading = true
	gen, tabs, api, write := r.gen, slices.Clone(r.tabs), r.deps.API, r.deps.WriteCache
	members, at, now, viewer := r.members, r.membersAt, m.opts.Now(), r.viewer
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if viewer == "" {
			var err error
			if viewer, err = api.Viewer(ctx); err != nil {
				return rvLiveMsg{gen: gen, err: err}
			}
		}
		var teams []string
		for _, tb := range tabs {
			for _, t := range tb.Filter.AuthorTeams {
				if !slices.Contains(teams, t) {
					teams = append(teams, t)
				}
			}
		}
		if len(teams) > 0 && now.Sub(at) >= membersEvery {
			got, err := api.TeamMembers(ctx, teams)
			if err != nil {
				return rvLiveMsg{gen: gen, err: err}
			}
			members, at = got, now
		}
		lists := map[string][]github.PullRequest{}
		var searches []github.Search
		for _, tb := range tabs {
			if tb.Tasks {
				continue
			}
			q, ok := tb.Filter.Query(members)
			if !ok {
				lists[tb.Key] = []github.PullRequest{}
				continue
			}
			searches = append(searches, github.Search{Key: tb.Key, Name: tb.Name, Query: q})
			if tb.Merged {
				searches = append(searches, github.Search{Key: mergedKey(tb), Name: tb.Name, Query: github.MyMergedPRs(now.Add(-mergedFor))})
			}
		}
		found, err := api.SearchPRs(ctx, viewer, searches)
		var errs github.SearchErrors
		if err != nil && !errors.As(err, &errs) {
			return rvLiveMsg{gen: gen, err: err}
		}
		failed := map[string]string{}
		for _, tb := range tabs {
			if e := cmp.Or(errs[tb.Key], errs[mergedKey(tb)]); e != nil {
				failed[tb.Key] = errText(e)
				delete(found, tb.Key)
				delete(found, mergedKey(tb))
				continue
			}
			if merged, ok := found[mergedKey(tb)]; ok {
				delete(found, mergedKey(tb))
				slices.SortStableFunc(merged, func(a, b github.PullRequest) int { return b.MergedAt.Compare(a.MergedAt) })
				found[tb.Key] = append(found[tb.Key], merged...)
			}
		}
		maps.Copy(lists, found)
		for k, prs := range lists {
			write(k, prs)
		}
		return rvLiveMsg{gen: gen, lists: lists, failed: failed, members: members, membersAt: at, viewer: viewer}
	}
}

// mergedKey names a tab's search for merged PRs. Its PRs join the tab's list,
// so it has no cache file.
func mergedKey(tb ReviewView) string { return tb.Key + ":merged" }

// rankTabs puts the tabs in the saved rank first, in that order, then the
// rest in the default order.
func (r *review) rankTabs() {
	at := map[string]int{}
	for i, k := range r.rank {
		if _, ok := at[k]; !ok {
			at[k] = i
		}
	}
	sort.SliceStable(r.tabs, func(a, b int) bool {
		ra, oka := at[r.tabs[a].Key]
		rb, okb := at[r.tabs[b].Key]
		if oka != okb {
			return oka
		}
		return oka && ra < rb
	})
}

// saveReviewRank stores the tab order. A saved key that is not a tab now
// keeps its place after the tabs.
func (m Model) saveReviewRank() tea.Cmd {
	r := m.rv
	var keys []string
	for _, tb := range r.tabs {
		keys = append(keys, tb.Key)
	}
	for _, k := range r.rank {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	r.rank = keys
	save := r.deps.SaveOrder
	return func() tea.Msg { return rvRankSavedMsg{err: save(keys)} }
}

func (m Model) reviewMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	r := m.rv
	switch msg := msg.(type) {
	case rvCacheMsg:
		for k, prs := range msg.lists {
			if !r.fresh[k] {
				r.lists[k] = prs
			}
		}
	case rvLiveMsg:
		if msg.gen != r.gen {
			return m, nil, true
		}
		r.loading = false
		if msg.err != nil {
			r.err = errText(msg.err)
			return m, nil, true
		}
		r.err, r.failed = "", msg.failed
		r.members, r.membersAt, r.viewer = msg.members, msg.membersAt, msg.viewer
		for k, prs := range msg.lists {
			r.lists[k], r.fresh[k] = prs, true
		}
	case rvRankMsg:
		if msg.err != nil {
			m.setStatus("read the tab order: "+msg.err.Error(), true)
			return m, nil, true
		}
		r.rank = msg.keys
		r.rankTabs()
		r.tab, r.sel = 0, ""
	case rvRankSavedMsg:
		if msg.err != nil {
			m.setStatus("save the tab order: "+msg.err.Error(), true)
		}
	case rvTickMsg:
		return m, tea.Batch(m.refreshReviews(), m.reviewTick()), true
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) reviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.rv
	k := msg.String()
	if r.filtering {
		switch k {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			r.filtering = false
			r.filter.SetValue("")
			r.filter.Blur()
		case "enter":
			r.filtering = false
			r.filter.Blur()
		default:
			var cmd tea.Cmd
			r.filter, cmd = r.filter.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	switch k {
	case "tab", "shift+tab":
		d := 1
		if k == "shift+tab" {
			d = len(r.tabs) - 1
		}
		r.tab = (r.tab + d) % len(r.tabs)
		r.sel = ""
		r.filter.SetValue("")
		m.detail, m.preview = false, false
		m.previewGen++
		return m, nil
	case "<", ">":
		j := r.tab - 1
		if k == ">" {
			j = r.tab + 1
		}
		if j < 0 || j >= len(r.tabs) {
			return m, nil
		}
		r.tabs[r.tab], r.tabs[j] = r.tabs[j], r.tabs[r.tab]
		r.tab = j
		return m, m.saveReviewRank()
	case "R":
		return m, tea.Batch(m.refreshReviews(), m.pollQuietly())
	}
	if r.curTab().Tasks {
		return m.boardKey(msg)
	}
	rows := r.rows()
	i := r.selected(rows)
	switch k {
	case "j", "down":
		if i+1 < len(rows) {
			r.sel = rows[i+1].URL
		}
	case "k", "up":
		if i > 0 {
			r.sel = rows[i-1].URL
		}
	case "/":
		r.filtering = true
		return m, r.filter.Focus()
	case "esc":
		r.filter.SetValue("")
	case "z":
		if m.opts.Zoom != nil {
			m.opts.Zoom()
		}
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		t, ok := m.joinPR()[rows[i].URL]
		switch {
		case ok && t.Live:
			return m, m.op("focus", scheduler.Request{Op: "focus", Task: t.ID})
		case ok && reopens(t):
			return m, m.reviewPR(rows[i].URL, prRef(rows[i]))
		}
		return m, m.openURL(rows[i].URL)
	case "o":
		if len(rows) > 0 {
			return m, m.openURL(rows[i].URL)
		}
	case "r":
		if len(rows) > 0 {
			return m, m.reviewPR(rows[i].URL, prRef(rows[i]))
		}
	}
	return m, nil
}

// pollQuietly asks the Scheduler to poll now. Only an error reaches the
// status line, so that it never hides an error of the PR search.
func (m Model) pollQuietly() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		if err := c.Do(scheduler.Request{Op: "poll"}, nil); err != nil {
			return opMsg{label: "refresh", err: err}
		}
		return nil
	}
}

// reviewPR starts a review Task for the PR at url now, outside every queue's
// slots. If the PR has an open review Task, it starts that Task if it is
// Queued, reopens its closed Worker, runs it again if it stopped for good, and
// otherwise focuses its tab. ref names the PR in an error.
func (m Model) reviewPR(url, ref string) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		var res scheduler.ReviewResult
		if err := c.Do(scheduler.Request{Op: "review", URL: url}, &res); err != nil {
			return opMsg{label: "review", task: ref, err: err}
		}
		switch res.Action {
		case scheduler.ReviewAdded:
			return opMsg{label: "review", task: res.ID, note: res.Stage}
		case scheduler.ReviewStarted, scheduler.ReviewRestarted, scheduler.ReviewReopened:
			return opMsg{label: "review", task: res.ID, note: string(res.Action)}
		}
		note := "already open"
		if err := c.Do(scheduler.Request{Op: "focus", Task: res.ID}, nil); err != nil {
			note += ", not focused: " + err.Error()
		}
		return opMsg{label: "review", task: res.ID, note: note}
	}
}

// reopens reports whether the review op reopens the Task's Worker, because
// the Worker closed in a Stage that resumes it.
func reopens(t scheduler.TaskView) bool {
	st, err := lifecycle.ParseStage(t.StageKey)
	return err == nil && st.Resumable() && !t.Live
}

// reviewTasks are the review Tasks on the Board, as the queue rules see them.
func reviewTasks(tasks []scheduler.TaskView) []github.ReviewTask {
	var out []github.ReviewTask
	for _, t := range tasks {
		if t.Kind == lifecycle.KindReview {
			out = append(out, github.ReviewTask{URL: t.PRURL, Head: t.ReviewHead, Done: t.Group == lifecycle.GroupDone.String() && !t.Tab})
		}
	}
	return out
}

func (m Model) openURL(u string) tea.Cmd {
	open := m.opts.Open
	return func() tea.Msg { return opMsg{label: "open", err: open(u)} }
}

// joinPR maps a PR URL to the review Task that has it. The Board lists Tasks
// most urgent first, so a Needs you Task wins over a Done one.
func (m Model) joinPR() map[string]scheduler.TaskView {
	out := map[string]scheduler.TaskView{}
	for _, t := range m.tasks {
		if _, ok := out[t.PRURL]; !ok && t.PRURL != "" {
			out[t.PRURL] = t
		}
	}
	return out
}
