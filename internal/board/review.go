package board

import (
	"context"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/scheduler"
)

const (
	reviewEvery  = 2 * time.Minute
	membersEvery = time.Hour
	keyMine      = "mine"
)

// ReviewAPI is the part of github.Client that the review tab uses.
type ReviewAPI interface {
	Viewer(ctx context.Context) (string, error)
	SearchPRs(ctx context.Context, viewer string, searches []github.Search) (map[string][]github.PullRequest, error)
	TeamMembers(ctx context.Context, teams []string) (map[string][]string, error)
}

// ReviewView is one tab of the review tab. Key names its cache file and its
// place in the saved rank.
type ReviewView struct {
	Key, Name string
	Filter    github.Filter
}

// Reviews holds the review tab's outside world. Tests replace it.
type Reviews struct {
	// Views are the configured tabs, which follow My PRs and Requested.
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
	{Key: keyMine, Name: "My PRs", Filter: github.Filter{Base: github.MyPRs}},
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
	err     string

	members   map[string][]string
	membersAt time.Time
	viewer    string // the user's login, read once

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

// rows are the open tab's PRs that match the search, best match first.
func (r *review) rows() []github.PullRequest {
	prs := r.lists[r.curTab().Key]
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

// refreshReviews searches every tab in one GraphQL call. It reads the members
// of the author teams first when they are an hour old. Only the newest
// refresh lands, so a slow answer never replaces a newer one.
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
			q, ok := tb.Filter.Query(members)
			if !ok {
				lists[tb.Key] = []github.PullRequest{}
				continue
			}
			searches = append(searches, github.Search{Key: tb.Key, Name: tb.Name, Query: q})
		}
		found, err := api.SearchPRs(ctx, viewer, searches)
		if err != nil {
			return rvLiveMsg{gen: gen, err: err}
		}
		maps.Copy(lists, found)
		for k, prs := range lists {
			write(k, prs)
		}
		return rvLiveMsg{gen: gen, lists: lists, members: members, membersAt: at, viewer: viewer}
	}
}

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
		r.err = ""
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
	rows := r.rows()
	i := r.selected(rows)
	switch k {
	case "tab", "shift+tab":
		d := 1
		if k == "shift+tab" {
			d = len(r.tabs) - 1
		}
		r.tab = (r.tab + d) % len(r.tabs)
		r.sel = ""
		r.filter.SetValue("")
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
	case "R":
		return m, m.refreshReviews()
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		if t, ok := m.joinPR()[rows[i].URL]; ok {
			return m, m.op("focus", scheduler.Request{Op: "focus", Task: t.ID})
		}
		return m, m.openURL(rows[i].URL)
	case "o":
		if len(rows) > 0 {
			return m, m.openURL(rows[i].URL)
		}
	}
	return m, nil
}

func (m Model) openURL(u string) tea.Cmd {
	open := m.opts.Open
	return func() tea.Msg { return opMsg{label: "open", err: open(u)} }
}

// joinPR maps a PR URL to the Task on the Board that has it. The Board lists
// Tasks most urgent first, so a Needs you Task wins over a Done one.
func (m Model) joinPR() map[string]scheduler.TaskView {
	out := map[string]scheduler.TaskView{}
	for _, t := range m.tasks {
		if _, ok := out[t.PRURL]; !ok && t.PRURL != "" {
			out[t.PRURL] = t
		}
	}
	return out
}
