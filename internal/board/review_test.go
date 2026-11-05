package board

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

type fakeReviewAPI struct {
	mu          sync.Mutex
	lists       map[string][]github.PullRequest
	members     map[string][]string
	searches    [][]github.Search
	memberCalls int
	viewerCalls int
	err         error
}

func (f *fakeReviewAPI) Viewer(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewerCalls++
	return "octocat", nil
}

func (f *fakeReviewAPI) SearchPRs(_ context.Context, viewer string, ss []github.Search) (map[string][]github.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if viewer != "octocat" {
		return nil, errors.New("searched as " + viewer)
	}
	f.searches = append(f.searches, ss)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string][]github.PullRequest{}
	for _, s := range ss {
		out[s.Key] = f.lists[s.Key]
	}
	return out, nil
}

func (f *fakeReviewAPI) TeamMembers(_ context.Context, teams []string) (map[string][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memberCalls++
	out := map[string][]string{}
	for _, t := range teams {
		out[t] = f.members[t]
	}
	return out, nil
}

const (
	urlLogs  = "https://github.com/umbrella/data/pull/4945"
	urlCSV   = "https://github.com/umbrella/data/pull/4919"
	urlDraft = "https://github.com/umbrella/backend/pull/25144"
	urlBase  = "https://github.com/umbrella/data/pull/4955"
)

func reviewFixture() map[string][]github.PullRequest {
	return map[string][]github.PullRequest{
		"mine": {
			{Repo: "umbrella/data", Number: 4945, Title: "Write the usage logs again", URL: urlLogs, Author: "octocat",
				CreatedAt: epoch.Add(-50 * time.Hour), Checks: github.ChecksPending,
				LatestReviews: []github.Review{{Author: "knuth", State: "APPROVED"}}},
			{Repo: "umbrella/data", Number: 4919, Title: "Move the CSV coverage readers", URL: urlCSV, Author: "octocat",
				CreatedAt: epoch.Add(-5 * 24 * time.Hour), Checks: github.ChecksFailed},
			{Repo: "umbrella/backend", Number: 25144, Title: "Stop using file age as a proxy", URL: urlDraft, Author: "octocat",
				CreatedAt: epoch.Add(-3 * time.Hour), Draft: true},
		},
		"requested": {
			{Repo: "umbrella/data", Number: 4955, Title: "Pull the base images through the cache", URL: urlBase, Author: "goldberg",
				CreatedAt: epoch.Add(-time.Hour), Checks: github.ChecksPassed, ReviewDecision: "CHANGES_REQUESTED"},
		},
		"view:My teammates": {},
		"queue:platform":    {},
	}
}

func reviewBoard() scheduler.BoardView {
	logsRow := tv("ENG-614", lifecycle.CIRunning, "data", "Write the usage logs", "", 2*time.Hour)
	logsRow.PRURL = urlLogs
	asked := tv("ENG-590", lifecycle.Working, "data", "Base images", "question", 12*time.Minute)
	asked.Flag, asked.Group, asked.PRURL = "question", "Needs you", urlBase
	return scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{logsRow, asked}}
}

type rvEnv struct {
	m      Model
	sched  *fakeClient
	api    *fakeReviewAPI
	cache  map[string][]github.PullRequest
	wrote  map[string][]github.PullRequest
	rank   []string
	saved  [][]string
	opened []string
	ticks  int
}

func newRvEnv(t *testing.T) *rvEnv {
	t.Helper()
	e := &rvEnv{sched: &fakeClient{errs: map[string]error{}},
		api:   &fakeReviewAPI{lists: reviewFixture(), members: map[string][]string{"umbrella/platform": {"hopper", "kernighan"}}},
		cache: map[string][]github.PullRequest{}, wrote: map[string][]github.PullRequest{}}
	e.m = New(reviewBoard(), Options{
		Review: true,
		Client: e.sched,
		Now:    func() time.Time { return epoch },
		Open:   func(u string) error { e.opened = append(e.opened, u); return nil },
		Reviews: Reviews{
			Views: []ReviewView{
				{Key: "view:My teammates", Name: "My teammates", Filter: github.Filter{Base: github.ViewPRs, Repos: []string{"umbrella/data"}, AuthorTeams: []string{"umbrella/platform"}}},
				{Key: "queue:platform", Name: "Q: platform", Filter: github.Filter{Base: github.QueuePRs, Authors: []string{"hopper"}}, Queue: true},
			},
			API:        e.api,
			ReadCache:  func(k string) ([]github.PullRequest, bool) { prs, ok := e.cache[k]; return prs, ok },
			WriteCache: func(k string, prs []github.PullRequest) { e.wrote[k] = prs },
			Order:      func() ([]string, error) { return e.rank, nil },
			SaveOrder:  func(keys []string) error { e.saved = append(e.saved, keys); return nil },
			Tick: func(d time.Duration, _ func(time.Time) tea.Msg) tea.Cmd {
				if d != 2*time.Minute {
					t.Errorf("refresh every %v, want 2m", d)
				}
				e.ticks++
				return nil
			},
		},
	})
	e.m.online = true
	e.m.width, e.m.height = 120, 30
	return e
}

func (e *rvEnv) open() { e.m = drain(e.m, e.m.Init()) }

func (e *rvEnv) press(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		e.m, cmd = press(e.m, k)
		e.m = drain(e.m, cmd)
	}
	return cmd
}

// line returns the screen line that holds s.
func line(v, s string) string {
	for _, l := range strings.Split(ansi.Strip(v), "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

func TestReviewRowsShowTheJoinedTask(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	v := e.m.View()
	if l := line(v, "[My PRs]"); !strings.Contains(l, "Requested") || !strings.Contains(l, "My teammates") || !strings.Contains(l, "Q: platform") {
		t.Fatalf("tab row = %q", l)
	}
	logsRow := line(v, "data#4945")
	for _, want := range []string{"▸", "Write the usage logs again", "◷ CI", "✓ 1", "ENG-614 CI running", "2d"} {
		if !strings.Contains(logsRow, want) {
			t.Errorf("joined row lacks %q: %q", want, logsRow)
		}
	}
	if !strings.Contains(logsRow, "octocat") {
		t.Errorf("My PRs hides the author: %q", logsRow)
	}
	head := line(v, "TITLE")
	for _, want := range []string{"PR", "TITLE", "AUTHOR", "CI", "REVIEW", "TASK", "AGE"} {
		if !strings.Contains(head, want) {
			t.Errorf("header row lacks %q: %q", want, head)
		}
	}
	// Each heading starts at the column of its cells.
	for _, c := range []struct{ head, cell string }{{"AUTHOR", "octocat"}, {"CI", "◷ CI"}, {"REVIEW", "✓ 1"}, {"TASK", "ENG-614"}, {"AGE", "2d"}} {
		if hi, ci := strings.Index(head, c.head), strings.Index(logsRow, c.cell); ansi.StringWidth(head[:hi]) != ansi.StringWidth(logsRow[:ci]) {
			t.Errorf("%s heading at cell %d, %q at cell %d", c.head, ansi.StringWidth(head[:hi]), c.cell, ansi.StringWidth(logsRow[:ci]))
		}
	}
	if csvRow := line(v, "data#4919"); !strings.Contains(csvRow, "✗ CI") || strings.Contains(csvRow, "ENG-") {
		t.Errorf("unjoined row = %q", csvRow)
	}
	if d := line(v, "backend#25144"); !strings.Contains(d, "draft") {
		t.Errorf("draft row = %q", d)
	}

	e.press("tab")
	base := line(e.m.View(), "data#4955")
	for _, want := range []string{"goldberg", "✗ changes", "ENG-590 ⚑ question"} {
		if !strings.Contains(base, want) {
			t.Errorf("Requested row lacks %q: %q", want, base)
		}
	}
}

func TestReviewEnterFocusesTheJoinedTaskOrOpensThePR(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.press("enter")
	if n := len(e.sched.calls); n != 1 || e.sched.calls[0].Op != "focus" || e.sched.calls[0].Task != "ENG-614" {
		t.Fatalf("enter on a joined row sent %+v", e.sched.calls)
	}
	if len(e.opened) != 0 {
		t.Fatalf("enter on a joined row opened %v", e.opened)
	}
	e.press("j", "enter")
	if !slices.Equal(e.opened, []string{urlCSV}) || len(e.sched.calls) != 1 {
		t.Fatalf("enter on an unjoined row: opened %v, calls %+v", e.opened, e.sched.calls)
	}
	e.press("k", "o")
	if !slices.Equal(e.opened, []string{urlCSV, urlLogs}) || len(e.sched.calls) != 1 {
		t.Fatalf("o on a joined row: opened %v, calls %+v", e.opened, e.sched.calls)
	}
}

func TestReviewEnterOpensThePRWhenTheTaskHasNoWorker(t *testing.T) {
	e := newRvEnv(t)
	b := reviewBoard()
	b.Tasks[0].Live = false
	e.m.board, e.m.tasks = b, b.Tasks
	e.open()
	if strings.Contains(e.m.View(), "go to the Worker") {
		t.Error("the footer offers a Worker that does not run")
	}
	e.press("enter")
	if !slices.Equal(e.opened, []string{urlLogs}) || len(e.sched.calls) != 0 {
		t.Fatalf("enter on a row with no Worker: opened %v, calls %+v", e.opened, e.sched.calls)
	}
}

func TestReviewZZoomsTheReviewer(t *testing.T) {
	e := newRvEnv(t)
	zoomed := 0
	e.m.opts.Zoom = func() { zoomed++ }
	e.open()
	if !strings.Contains(e.m.View(), "go to the Worker") {
		t.Error("the footer does not offer the joined Task's Worker")
	}
	e.press("z")
	if zoomed != 1 {
		t.Fatalf("z zoomed %d times", zoomed)
	}
}

func TestReviewTabsSwitch(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	for _, c := range []struct{ key, open string }{
		{"tab", "[Requested]"}, {"tab", "[My teammates]"}, {"shift+tab", "[Requested]"},
		{"shift+tab", "[My PRs]"}, {"shift+tab", "[Q: platform]"}, {"tab", "[My PRs]"},
	} {
		e.press(c.key)
		if v := e.m.View(); !strings.Contains(v, c.open) {
			t.Fatalf("after %s, want %s open:\n%s", c.key, c.open, v)
		}
	}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestReviewSearchTakesQ(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	var cmd tea.Cmd
	e.m, _ = press(e.m, "/")
	e.m, cmd = press(e.m, "q")
	if isQuit(cmd) {
		t.Fatal("q typed in the search quit the board")
	}
	if got := e.m.rv.filter.Value(); got != "q" {
		t.Fatalf("search = %q, want q", got)
	}
	e.m, _ = press(e.m, "esc")
	e.m, _ = press(e.m, "/", "C", "S", "V", "enter")
	v := e.m.View()
	if !strings.Contains(v, "data#4919") || strings.Contains(v, "data#4945") {
		t.Fatalf("search CSV:\n%s", v)
	}
	if _, cmd = press(e.m, "q"); !isQuit(cmd) {
		t.Fatal("q outside the search did not quit")
	}
}

func TestReviewPaintsFromCacheFirst(t *testing.T) {
	e := newRvEnv(t)
	e.cache["mine"] = []github.PullRequest{{Repo: "umbrella/data", Number: 1, Title: "Cached row", URL: "u1"}}
	batch, ok := e.m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init is not a batch")
	}
	var cached, live tea.Msg
	for _, c := range batch {
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case rvCacheMsg:
			cached = msg
		case rvLiveMsg:
			live = msg
		}
	}
	m, _ := e.m.Update(cached)
	v := m.View()
	if !strings.Contains(v, "Cached row") || !strings.Contains(v, "refreshing") {
		t.Fatalf("cache paint:\n%s", v)
	}
	m, _ = m.Update(live)
	if v = m.View(); strings.Contains(v, "Cached row") || !strings.Contains(v, "data#4945") || strings.Contains(v, "refreshing") {
		t.Fatalf("the live list should replace the cache:\n%s", v)
	}
	m, _ = m.Update(cached)
	if strings.Contains(m.View(), "Cached row") {
		t.Fatal("a late cache answer replaced the live list")
	}
}

func TestReviewSearchesEveryViewInOneCall(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	if len(e.api.searches) != 1 {
		t.Fatalf("searches = %d calls, want 1", len(e.api.searches))
	}
	queries := map[string]string{}
	for _, s := range e.api.searches[0] {
		queries[s.Key] = s.Query
	}
	if !strings.Contains(queries["view:My teammates"], "author:hopper author:kernighan") {
		t.Errorf("the team query lacks the team members: %q", queries["view:My teammates"])
	}
	if !strings.Contains(queries["queue:platform"], "draft:false -author:@me") {
		t.Errorf("the queue query lacks the queue qualifiers: %q", queries["queue:platform"])
	}
	if len(queries) != 4 || !strings.Contains(queries["mine"], "author:@me") || !strings.Contains(queries["requested"], "review-requested:@me") {
		t.Errorf("queries = %v", queries)
	}
	if len(e.wrote) != 4 || len(e.wrote["mine"]) != 3 {
		t.Errorf("cache writes = %v", e.wrote)
	}
	e.press("R")
	if len(e.api.searches) != 2 || e.api.memberCalls != 1 || e.api.viewerCalls != 1 {
		t.Errorf("R: %d searches, %d member reads, %d login reads; want 2, 1, and 1", len(e.api.searches), e.api.memberCalls, e.api.viewerCalls)
	}
}

func TestReviewTickRefreshes(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	if e.ticks != 1 {
		t.Fatalf("open scheduled %d ticks, want 1", e.ticks)
	}
	e.m = drain(e.m, func() tea.Msg { return rvTickMsg{} })
	if len(e.api.searches) != 2 || e.ticks != 2 {
		t.Fatalf("a tick: %d searches, %d ticks; want 2 and 2", len(e.api.searches), e.ticks)
	}
}

func TestReviewErrorKeepsTheLastList(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.api.err = errors.New("search My PRs, Requested: HTTP 502")
	e.press("R")
	v := e.m.View()
	if !strings.Contains(v, "data#4945") {
		t.Fatalf("a failed search dropped the list:\n%s", v)
	}
	if !strings.Contains(line(v, "HTTP 502"), "My PRs, Requested") {
		t.Fatalf("the status line does not name the views:\n%s", v)
	}
}

func TestReviewRankMovesAndSaves(t *testing.T) {
	e := newRvEnv(t)
	e.rank = []string{"queue:platform", "gone"}
	e.open()
	if l := line(e.m.View(), "[Q: platform]"); !strings.HasPrefix(strings.TrimSpace(l), "[Q: platform]") {
		t.Fatalf("the saved rank does not lead: %q", l)
	}
	e.press(">")
	want := []string{"mine", "queue:platform", "requested", "view:My teammates", "gone"}
	if len(e.saved) != 1 || !slices.Equal(e.saved[0], want) {
		t.Fatalf("saved %v, want %v", e.saved, want)
	}
	if l := line(e.m.View(), "[Q: platform]"); !strings.Contains(l, "My PRs") || strings.Index(l, "My PRs") > strings.Index(l, "[Q: platform]") {
		t.Fatalf("the tab did not move right: %q", l)
	}
	e.press("<", "<")
	if len(e.saved) != 2 {
		t.Fatalf("a move past the left end saved again: %v", e.saved)
	}
}

func TestReviewNarrowRowKeepsTheTitleAndTheTask(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.m.width = 70
	v := e.m.View()
	logsRow := line(v, "data#4945")
	if !strings.Contains(logsRow, "Write the usage logs aga") {
		t.Fatalf("the title lost its 24 cells: %q", logsRow)
	}
	if !strings.Contains(logsRow, "ENG-614") {
		t.Fatalf("the TASK column dropped before the others: %q", logsRow)
	}
	if strings.Contains(logsRow, "octocat") || strings.Contains(logsRow, "2d") {
		t.Fatalf("AUTHOR and AGE did not drop first: %q", logsRow)
	}
	if head := line(v, "TITLE"); strings.Contains(head, "AUTHOR") || strings.Contains(head, "AGE") || !strings.Contains(head, "TASK") {
		t.Fatalf("the header row does not match the cells: %q", head)
	}
}

func TestReviewRStartsAReviewOfTheSelectedPR(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.press("shift+tab") // the empty Q: tab
	e.press("r")
	if len(e.sched.calls) != 0 {
		t.Fatalf("r with no row sent %+v", e.sched.calls)
	}

	e.press("tab", "tab")
	e.sched.review = scheduler.ReviewResult{TaskView: scheduler.TaskView{ID: "R-3", Stage: "Reviewing"}}
	e.press("r")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "review" || c[0].URL != urlBase {
		t.Fatalf("r sent %+v, want review %s", c, urlBase)
	}
	if l := line(e.m.View(), "review R-3"); !strings.Contains(l, "Reviewing") {
		t.Fatalf("status line = %q", l)
	}

	e.sched.review.Existing = true
	e.press("r")
	if c := e.sched.calls[1:]; len(c) != 2 || c[0].Op != "review" || c[1].Op != "focus" || c[1].Task != "R-3" {
		t.Fatalf("r on a PR with an open review sent %+v, want review then focus R-3", c)
	}
	if l := line(e.m.View(), "review R-3"); !strings.Contains(l, "already open") {
		t.Fatalf("status line = %q", l)
	}

	e.sched.errs["review"] = errors.New("umbrella/data#4955 comes from a fork")
	e.press("r")
	if l := line(e.m.View(), "review data#4955"); !strings.Contains(l, "comes from a fork") {
		t.Fatalf("status line = %q, want the refusal", l)
	}
}

func TestTheQueueTabListsThePRsThatWaitFirstToStartFirst(t *testing.T) {
	e := newRvEnv(t)
	pr := func(n int, age time.Duration) github.PullRequest {
		return github.PullRequest{Repo: "umbrella/data", Number: n, Title: fmt.Sprintf("Change %d", n), Author: "hopper",
			URL: fmt.Sprintf("https://github.com/umbrella/data/pull/%d", n), Head: fmt.Sprintf("h%d", n), CreatedAt: epoch.Add(-age)}
	}
	oldest, byName, approved, reviewing, reviewed := pr(5001, 72*time.Hour), pr(5002, time.Hour), pr(5003, 90*time.Hour), pr(5004, 80*time.Hour), pr(5005, 85*time.Hour)
	pushed := pr(5006, 2*time.Hour)
	byName.Viewer = github.ViewerState{Requested: true, ByName: true}
	approved.LatestReviews = []github.Review{{Author: "knuth", State: "APPROVED"}}
	e.api.lists["queue:platform"] = []github.PullRequest{oldest, byName, approved, reviewing, reviewed, pushed}

	b := reviewBoard()
	open := tv("R-7", lifecycle.Reviewing, "data", "data#5004 Change 5004", "", time.Hour)
	open.PRURL, open.ReviewHead = reviewing.URL, reviewing.Head
	done := tv("R-6", lifecycle.Done, "data", "data#5005 Change 5005", "", time.Hour)
	done.PRURL, done.ReviewHead = reviewed.URL, reviewed.Head
	older := tv("R-5", lifecycle.Done, "data", "data#5006 Change 5006", "", time.Hour)
	older.PRURL, older.ReviewHead = pushed.URL, "h5006-before-the-push"
	b.Tasks = append(b.Tasks, open, done, older)
	e.m.setBoard(b)
	e.open()
	e.press("shift+tab")

	v := ansi.Strip(e.m.View())
	if l := line(v, "Q: platform  "); !strings.HasSuffix(strings.TrimSpace(l), "3") {
		t.Fatalf("queue head = %q, want 3 PRs", l)
	}
	first, second, third := strings.Index(v, "data#5002"), strings.Index(v, "data#5001"), strings.Index(v, "data#5006")
	if first < 0 || second < 0 || third < 0 || first > second || second > third {
		t.Fatalf("want #5002, which requests the user by name, then the older #5001, then #5006, pushed since its review:\n%s", v)
	}
	for _, n := range []string{"data#5003", "data#5004", "data#5005"} {
		if strings.Contains(v, n) {
			t.Errorf("the queue tab lists %s:\n%s", n, v)
		}
	}
}
