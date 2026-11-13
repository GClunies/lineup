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
	"github.com/charmbracelet/lipgloss"
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
	fail        map[string]error // a failed search by Key
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
	out, failed := map[string][]github.PullRequest{}, github.SearchErrors{}
	for _, s := range ss {
		if err := f.fail[s.Key]; err != nil {
			failed[s.Key] = err
		} else {
			out[s.Key] = f.lists[s.Key]
		}
	}
	if len(failed) > 0 {
		return out, failed
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

// reviewBoard has a review Task on two PRs, and a build Task on a third.
func reviewBoard() scheduler.BoardView {
	logsRow := rtv("R-14", lifecycle.Reviewing, "data#4945 Write the usage logs again", urlLogs, 2*time.Hour)
	asked := rtv("R-10", lifecycle.Reviewing, "data#4955 Pull the base images", urlBase, 12*time.Minute)
	asked.Flag, asked.Group, asked.Note = "question", "Needs you", "question"
	build := tv("ENG-622", lifecycle.CIRunning, "data", "Move the CSV coverage readers", "", time.Hour)
	build.PRURL = urlCSV
	return scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{logsRow, asked, build}}
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
		Client: e.sched,
		Now:    func() time.Time { return epoch },
		Open:   func(u string) error { e.opened = append(e.opened, u); return nil },
		Reviews: Reviews{
			Views: []ReviewView{
				{Key: "view:My teammates", Name: "My teammates", Filter: github.Filter{Base: github.ViewPRs, Repo: []string{"umbrella/data"}, AuthorTeam: []string{"umbrella/platform"}}},
				{Key: "queue:platform", Name: "Q: platform", Filter: github.Filter{Base: github.QueuePRs, Author: []string{"hopper"}}, Queue: true},
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

func (e *rvEnv) open() {
	var cmd tea.Cmd
	e.m, cmd = press(e.m, "2")
	e.m = drain(e.m, cmd)
}

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
	e.press("tab")
	v := e.m.View()
	if l := line(v, "[My PRs]"); !strings.Contains(l, "Reviews") || !strings.Contains(l, "Requested") || !strings.Contains(l, "My teammates") || !strings.Contains(l, "Q: platform") {
		t.Fatalf("tab row = %q", l)
	}
	logsRow := line(v, "data#4945")
	for _, want := range []string{"▸", "Write the usage logs again", "◷ CI", "✓ 1", "R-14 Reviewing", "2d"} {
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
	for _, c := range []struct{ head, cell string }{{"AUTHOR", "octocat"}, {"CI", "◷ CI"}, {"REVIEW", "✓ 1"}, {"TASK", "R-14"}, {"AGE", "2d"}} {
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
	for _, want := range []string{"goldberg", "✗ changes", "R-10 ⚑ question"} {
		if !strings.Contains(base, want) {
			t.Errorf("Requested row lacks %q: %q", want, base)
		}
	}
}

// Auto-merge can close a PR minutes after it opens, so My PRs keeps the
// user's PRs that merged in the last day, after the open ones.
func TestMyPRsListsTheMergedPRsAfterTheOpenOnes(t *testing.T) {
	e := newRvEnv(t)
	e.api.lists["mine:merged"] = []github.PullRequest{
		{Repo: "umbrella/lineup", Number: 12, Title: "Gate Ready to land", URL: "https://github.com/umbrella/lineup/pull/12", Author: "octocat",
			CreatedAt: epoch.Add(-3 * time.Hour), MergedAt: epoch.Add(-2 * time.Hour), Checks: github.ChecksPassed},
		{Repo: "umbrella/lineup", Number: 13, Title: "Drop two fail files", URL: "https://github.com/umbrella/lineup/pull/13", Author: "octocat",
			CreatedAt: epoch.Add(-time.Hour), MergedAt: epoch.Add(-30 * time.Minute), Checks: github.ChecksPassed},
	}
	e.open()
	e.press("tab")
	var refs []string
	for _, pr := range e.m.rv.rows() {
		refs = append(refs, prRef(pr))
	}
	if want := []string{"data#4945", "data#4919", "backend#25144", "lineup#13", "lineup#12"}; !slices.Equal(refs, want) {
		t.Fatalf("rows = %v, want %v", refs, want)
	}
	if row := line(e.m.View(), "lineup#13"); !strings.Contains(row, "merged") || strings.Contains(row, "✓ CI") {
		t.Errorf("merged row = %q, want merged in place of the CI state", row)
	}
	if _, ok := e.wrote["mine:merged"]; ok || len(e.wrote["mine"]) != 5 {
		t.Errorf("cache writes = %v, want the merged PRs in the mine file", e.wrote)
	}
}

// The CSV PR has a build Task, which belongs to the work board, so the row
// joins no Task.
func TestReviewEnterFocusesTheJoinedTaskOrOpensThePR(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.press("tab", "enter")
	if n := len(e.sched.calls); n != 1 || e.sched.calls[0].Op != "focus" || e.sched.calls[0].Task != "R-14" {
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

func TestReviewEnterOpensThePRWhenTheTaskHasEnded(t *testing.T) {
	e := newRvEnv(t)
	b := reviewBoard()
	b.Tasks[0].Live = false
	b.Tasks[0].Stage, b.Tasks[0].StageKey, b.Tasks[0].Group = lifecycle.Done.String(), lifecycle.Done.Key(), lifecycle.GroupDone.String()
	e.m.setBoard(b)
	e.open()
	e.press("tab")
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
	e.press("z", "tab")
	if !strings.Contains(e.m.View(), "go to the Worker") {
		t.Error("the footer does not offer the joined Task's Worker")
	}
	e.press("z")
	if zoomed != 2 {
		t.Fatalf("z on the Reviews tab and My PRs zoomed %d times, want 2", zoomed)
	}
}

func TestReviewTabsSwitch(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	if v := e.m.View(); !strings.Contains(v, "[Reviews]") {
		t.Fatalf("the review tab does not open on Reviews:\n%s", v)
	}
	for _, c := range []struct{ key, open string }{
		{"tab", "[My PRs]"}, {"tab", "[Requested]"}, {"tab", "[My teammates]"}, {"shift+tab", "[Requested]"},
		{"shift+tab", "[My PRs]"}, {"shift+tab", "[Reviews]"}, {"shift+tab", "[Q: platform]"}, {"tab", "[Reviews]"},
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
	e.press("tab")
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
	var first tea.Cmd
	e.m, first = press(e.m, "2")
	batch, ok := first().(tea.BatchMsg)
	if !ok {
		t.Fatal("the first visit is not a batch")
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
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
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
	if len(queries) != 5 || !strings.Contains(queries["mine"], "author:@me") || !strings.Contains(queries["requested"], "review-requested:@me") {
		t.Errorf("queries = %v", queries)
	}
	if q := queries["mine:merged"]; !strings.Contains(q, "is:merged") || !strings.Contains(q, "author:@me merged:>="+epoch.Add(-24*time.Hour).UTC().Format(time.RFC3339)) {
		t.Errorf("the merged query = %q, want the user's PRs merged in the last day", q)
	}
	if len(e.wrote) != 4 || len(e.wrote["mine"]) != 3 {
		t.Errorf("cache writes = %v", e.wrote)
	}
	e.press("R")
	if len(e.api.searches) != 2 || e.api.memberCalls != 1 || e.api.viewerCalls != 1 {
		t.Errorf("R: %d searches, %d member reads, %d login reads; want 2, 1, and 1", len(e.api.searches), e.api.memberCalls, e.api.viewerCalls)
	}
	e.press("tab", "R")
	if len(e.api.searches) != 3 || !slices.Equal(e.sched.ops(), []string{"poll", "poll"}) {
		t.Errorf("R on the Reviews tab and My PRs: %d searches, Scheduler ops %v; want 3 and two polls", len(e.api.searches), e.sched.ops())
	}
}

func TestAViewAndAQueueApplyTheTitleRegexes(t *testing.T) {
	e := newRvEnv(t)
	for i := range e.m.rv.tabs {
		e.m.rv.tabs[i].Filter.ExcludeRegex = "HLESS-"
	}
	pr := func(n int, title string) github.PullRequest {
		return github.PullRequest{Repo: "umbrella/data", Number: n, Title: title, Author: "hopper",
			URL: fmt.Sprintf("https://github.com/umbrella/data/pull/%d", n), CreatedAt: epoch.Add(-time.Hour)}
	}
	e.api.lists["view:My teammates"] = []github.PullRequest{pr(1, "HLESS-12 Move the loaders"), pr(2, "Write the usage logs")}
	e.api.lists["queue:platform"] = []github.PullRequest{pr(3, "Read the HLESS- table"), pr(4, "hless-14 lower case")}
	e.open()
	for key, want := range map[string][]int{"view:My teammates": {2}, "queue:platform": {4}} {
		var got []int
		for _, p := range e.wrote[key] {
			got = append(got, p.Number)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s cached %v, want %v", key, got, want)
		}
	}
	if len(e.wrote["mine"]) != 3 {
		t.Errorf("My PRs cached %d PRs, want its 3: it has no exclusions", len(e.wrote["mine"]))
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
	e.press("tab", "R")
	v := e.m.View()
	if !strings.Contains(v, "data#4945") {
		t.Fatalf("a failed search dropped the list:\n%s", v)
	}
	if !strings.Contains(line(v, "HTTP 502"), "My PRs, Requested") {
		t.Fatalf("the status line does not name the views:\n%s", v)
	}
}

func TestAFailedSearchFailsOnlyItsTab(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.api.lists["requested"] = append(e.api.lists["requested"], github.PullRequest{Repo: "umbrella/data", Number: 4960,
		Title: "Read the new feed", URL: "https://github.com/umbrella/data/pull/4960", Author: "goldberg", CreatedAt: epoch})
	e.api.lists["mine"] = nil
	e.api.fail = map[string]error{"mine": errors.New("search My PRs: HTTP 502")}
	clear(e.wrote)
	e.press("tab", "R")
	v := e.m.View()
	if !strings.Contains(v, "data#4945") || !strings.Contains(v, "HTTP 502") {
		t.Fatalf("My PRs lost its last list or its error:\n%s", v)
	}
	if _, ok := e.wrote["mine"]; ok {
		t.Errorf("the failed search replaced the cache: %v", e.wrote["mine"])
	}
	e.press("tab")
	v = e.m.View()
	if !strings.Contains(v, "data#4960") {
		t.Fatalf("Requested did not get its new list:\n%s", v)
	}
	if strings.Contains(v, "HTTP 502") {
		t.Fatalf("Requested shows the error of My PRs:\n%s", v)
	}
	e.api.fail = nil
	e.press("shift+tab", "R")
	if v = e.m.View(); strings.Contains(v, "HTTP 502") || strings.Contains(v, "data#4945") {
		t.Fatalf("a passed search left the old error or list:\n%s", v)
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
	want := []string{"tasks", "queue:platform", "mine", "requested", "view:My teammates", "gone"}
	if len(e.saved) != 1 || !slices.Equal(e.saved[0], want) {
		t.Fatalf("saved %v, want %v", e.saved, want)
	}
	if l := line(e.m.View(), "[Q: platform]"); !strings.Contains(l, "Reviews") || strings.Index(l, "Reviews") > strings.Index(l, "[Q: platform]") {
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
	e.press("tab")
	e.m.width = 70
	v := e.m.View()
	logsRow := line(v, "data#4945")
	if !strings.Contains(logsRow, "Write the usage logs aga") {
		t.Fatalf("the title lost its 24 cells: %q", logsRow)
	}
	if !strings.Contains(logsRow, "R-14") {
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

	e.press("tab", "tab", "tab")
	e.sched.review = scheduler.ReviewResult{TaskView: scheduler.TaskView{ID: "R-3", Stage: "Reviewing"}, Action: scheduler.ReviewAdded}
	e.press("r")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "review" || c[0].URL != urlBase {
		t.Fatalf("r sent %+v, want review %s", c, urlBase)
	}
	if l := line(e.m.View(), "review R-3"); !strings.Contains(l, "Reviewing") {
		t.Fatalf("status line = %q", l)
	}

	e.sched.review.Action = scheduler.ReviewOpen
	e.press("r")
	if c := e.sched.calls[1:]; len(c) != 2 || c[0].Op != "review" || c[1].Op != "focus" || c[1].Task != "R-3" {
		t.Fatalf("r on a PR with an open review sent %+v, want review then focus R-3", c)
	}
	if l := line(e.m.View(), "review R-3"); !strings.Contains(l, "already open") {
		t.Fatalf("status line = %q", l)
	}

	e.sched.review.Action = scheduler.ReviewRestarted
	e.press("r")
	if c := e.sched.calls[3:]; len(c) != 1 || c[0].Op != "review" {
		t.Fatalf("r on a PR whose review had stopped sent %+v, want review alone", c)
	}
	if l := line(e.m.View(), "review R-3"); !strings.Contains(l, "restarted") {
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
	open.Kind, open.PRURL, open.ReviewHead = lifecycle.KindReview, reviewing.URL, reviewing.Head
	done := tv("R-6", lifecycle.Done, "data", "data#5005 Change 5005", "", time.Hour)
	done.Kind, done.PRURL, done.ReviewHead = lifecycle.KindReview, reviewed.URL, reviewed.Head
	older := tv("R-5", lifecycle.Done, "data", "data#5006 Change 5006", "", time.Hour)
	older.Kind, older.PRURL, older.ReviewHead = lifecycle.KindReview, pushed.URL, "h5006-before-the-push"
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

func TestTheQueueTabHoldsAPRWhoseDoneReviewKeepsItsTab(t *testing.T) {
	pushed := github.PullRequest{Repo: "umbrella/data", Number: 5006, Title: "Change 5006", Author: "hopper",
		URL: "https://github.com/umbrella/data/pull/5006", Head: "h5006", CreatedAt: epoch.Add(-2 * time.Hour)}
	for _, tab := range []bool{true, false} {
		e := newRvEnv(t)
		e.api.lists["queue:platform"] = []github.PullRequest{pushed}
		b := reviewBoard()
		done := tv("R-5", lifecycle.Done, "data", "data#5006 Change 5006", "", time.Hour)
		done.Kind, done.PRURL, done.ReviewHead, done.Tab = lifecycle.KindReview, pushed.URL, "h5006-before-the-push", tab
		b.Tasks = append(b.Tasks, done)
		e.m.setBoard(b)
		e.open()
		e.press("shift+tab")
		listed := strings.Contains(ansi.Strip(e.m.View()), "data#5006")
		if listed == tab {
			t.Errorf("a Done review with tab=%v: the queue lists PR 5006 = %v, want %v", tab, listed, !tab)
		}
	}
}

// A review Task whose Worker closed in Review ready waits for the user's
// decisions, so enter reopens its Worker.
func TestReviewEnterReopensAClosedReviewWorker(t *testing.T) {
	e := newRvEnv(t)
	b := reviewBoard()
	ready := rtv("R-12", lifecycle.ReviewReady, "data#4955 Pull the base images", urlBase, time.Hour)
	ready.Live = false
	b.Tasks[1] = ready
	e.m.setBoard(b)
	e.open()
	e.press("tab", "tab")
	if !strings.Contains(e.m.View(), "restore the Worker") {
		t.Fatalf("the footer does not offer the restore:\n%s", e.m.View())
	}
	e.sched.open = scheduler.OpenResult{TaskView: ready, Action: scheduler.OpenRestored}
	e.press("enter")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "open" || c[0].Task != "R-12" {
		t.Fatalf("enter sent %+v, want open R-12 alone", c)
	}
	if len(e.opened) != 0 {
		t.Fatalf("enter opened %v", e.opened)
	}
	if l := line(e.m.View(), "open R-12"); !strings.Contains(l, "restored") {
		t.Fatalf("status line = %q", l)
	}
}

func TestTheReviewsTabListsOnlyTheReviewTasks(t *testing.T) {
	e := newRvEnv(t)
	e.sched.capture = "reviewer screen"
	e.open()
	v := e.m.View()
	if !strings.Contains(v, "R-14") || !strings.Contains(v, "R-10") || strings.Contains(v, "ENG-622") {
		t.Fatalf("the Reviews tab does not list just the review Tasks:\n%s", v)
	}
	if l := line(v, "Reviews  "); !strings.HasSuffix(strings.TrimSpace(l), "2") {
		t.Fatalf("Reviews head = %q, want 2 Tasks", l)
	}
	if f := line(v, "all keys"); !strings.Contains(f, "x close") || !strings.Contains(f, "S skip") {
		t.Fatalf("the footer lacks the Board's keys: %q", f)
	}
	e.press("enter")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "open" || c[0].Task != "R-10" {
		t.Fatalf("enter sent %+v, want open R-10", c)
	}
	e.press("x", "y")
	if c := e.sched.calls[1:]; len(c) != 1 || c[0].Op != "close" || c[0].Task != "R-10" {
		t.Fatalf("x then y sent %+v, want close R-10", c)
	}
	e.press("j", "j", "f")
	if c := e.sched.calls[2:]; len(c) != 1 || c[0].Op != "show" || c[0].Task != "R-14" || !strings.Contains(e.m.View(), "R-14  ") {
		t.Fatalf("f sent %+v, want show R-14 at full screen:\n%s", c, e.m.View())
	}
	e.press("f")
	var cmd tea.Cmd
	e.m, cmd = press(e.m, "p")
	e.m = run(e.m, cmd)
	if !strings.Contains(e.m.View(), "reviewer screen") {
		t.Fatalf("p shows no preview:\n%s", e.m.View())
	}
	e.press("?")
	if v := e.m.View(); !strings.Contains(v, "the Reviews tab") {
		t.Fatalf("? on the Reviews tab:\n%s", v)
	}
	gen := e.m.previewGen
	e.press("esc", "tab")
	if _, cmd := e.m.Update(captureTick(gen)); cmd != nil {
		t.Fatal("the preview still captures on a PR tab")
	}
}

// A review Task whose Worker closed in Review ready waits for the user's
// decisions, so enter on its row reopens its Worker.
func TestTheReviewsTabReopensAClosedReviewWorker(t *testing.T) {
	e := newRvEnv(t)
	ready := rtv("R-12", lifecycle.ReviewReady, "data#4955 Pull the base images", urlBase, time.Hour)
	ready.Live = false
	e.m.setBoard(scheduler.BoardView{Tasks: []scheduler.TaskView{ready}})
	e.open()
	if !strings.Contains(e.m.View(), "restore the Worker") {
		t.Fatalf("the hints do not offer the restore:\n%s", e.m.View())
	}
	e.sched.open = scheduler.OpenResult{TaskView: ready, Action: scheduler.OpenRestored}
	e.press("enter")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "open" || c[0].Task != "R-12" {
		t.Fatalf("enter sent %+v, want open R-12 alone", c)
	}
	if l := line(e.m.View(), "open R-12"); !strings.Contains(l, "restored") {
		t.Fatalf("status line = %q", l)
	}
}

func TestTheReviewViewToastsBothKinds(t *testing.T) {
	var toasts []string
	m, _ := press(New(reviewBoard(), Options{Client: &fakeClient{}, Notify: func(s string) { toasts = append(toasts, s) }}), "2")
	v := reviewBoard()
	v.Tasks[0] = rtv("R-14", lifecycle.ReviewReady, "data#4945 Write the usage logs again", urlLogs, 0)
	v.Tasks[2] = tv("ENG-622", lifecycle.ReadyToLand, "data", "Move the CSV coverage readers", "#4919", 0)
	if _, cmd := m.Update(boardMsg(v)); cmd != nil {
		cmd()
	}
	if want := []string{"R-14 Review ready: data#4945 Write the usage logs again", "ENG-622 Ready to land, #4919: Move the CSV coverage readers"}; !slices.Equal(toasts, want) {
		t.Fatalf("toasts %q, want %q", toasts, want)
	}
}

func TestAQueueTabLeavesOutThePRsOfAnEarlierQueueInTheConfig(t *testing.T) {
	first := ReviewView{Key: "queue:first", Name: "Q: first", Queue: true}
	second := ReviewView{Key: "queue:second", Name: "Q: second", Queue: true}
	r := newReview(Reviews{Views: []ReviewView{first, second}})
	pr := func(n int, age time.Duration) github.PullRequest {
		return github.PullRequest{Repo: "umbrella/data", Number: n, Author: "hopper", CreatedAt: epoch.Add(-age),
			URL: fmt.Sprintf("https://github.com/umbrella/data/pull/%d", n), Head: fmt.Sprintf("h%d", n)}
	}
	shared, only, older := pr(1, time.Hour), pr(2, 2*time.Hour), pr(3, 90*time.Hour)
	r.lists = map[string][]github.PullRequest{"queue:first": {shared}, "queue:second": {shared, only, older}}
	r.tabs = []ReviewView{second, first}
	got, _ := r.list(second)
	if len(got) != 2 || got[0].Number != 3 || got[1].Number != 2 {
		t.Errorf("second queue lists %v, want #3 then #2", got)
	}
	if got, _ := r.list(first); len(got) != 1 || got[0].Number != 1 {
		t.Errorf("first queue lists %v, want #1", got)
	}
}

func TestReviewEnterRestoresAStartedTaskWithNoWorker(t *testing.T) {
	e := newRvEnv(t)
	b := reviewBoard()
	b.Tasks[0].Live = false
	e.m.setBoard(b)
	e.open()
	e.press("tab")
	if !strings.Contains(e.m.View(), "restore the Worker") {
		t.Errorf("the footer does not offer the restore:\n%s", e.m.View())
	}
	e.press("enter")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "open" || c[0].Task != b.Tasks[0].ID || len(e.opened) != 0 {
		t.Fatalf("enter on a started Task with no Worker: opened %v, calls %+v, want open %s", e.opened, c, b.Tasks[0].ID)
	}
}

func TestReviewTasksMarkTheOnesThatASkipEnded(t *testing.T) {
	skipped := tv("R-1", lifecycle.Done, "data", "data#1 One", "", time.Hour)
	skipped.Kind, skipped.PRURL, skipped.ReviewHead, skipped.Outcome = lifecycle.KindReview, "https://github.com/umbrella/data/pull/1", "h1", "skipped"
	closed := skipped
	closed.PRURL, closed.Outcome = "https://github.com/umbrella/data/pull/2", "closed"
	got := reviewTasks([]scheduler.TaskView{skipped, closed})
	want := []github.ReviewTask{
		{URL: skipped.PRURL, Head: "h1", Done: true, Skipped: true},
		{URL: closed.PRURL, Head: "h1", Done: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("reviewTasks = %+v, want %+v", got, want)
	}
}

func skipPR(n int, age time.Duration) github.PullRequest {
	return github.PullRequest{Repo: "umbrella/data", Number: n, Title: fmt.Sprintf("Change %d", n), Author: "hopper",
		URL: fmt.Sprintf("https://github.com/umbrella/data/pull/%d", n), Head: fmt.Sprintf("h%d", n), CreatedAt: epoch.Add(-age)}
}

func skipOf(pr github.PullRequest) github.Skip { return github.Skip{URL: pr.URL, Title: pr.Title} }

// withSkips is the Board of the review fixture with the PRs skipped.
func withSkips(prs ...github.PullRequest) scheduler.BoardView {
	b := reviewBoard()
	for _, pr := range prs {
		b.Skips = append(b.Skips, skipOf(pr))
	}
	return b
}

func TestTheQueueTabListsASkippedPRLastAndDim(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
	e := newRvEnv(t)
	skipped, waits, later := skipPR(5001, 72*time.Hour), skipPR(5002, 10*time.Hour), skipPR(5003, time.Hour)
	e.api.lists["queue:platform"] = []github.PullRequest{skipped, waits, later}
	e.m.setBoard(withSkips(skipped))
	e.open()
	e.press("shift+tab")

	v := e.m.View()
	if l := line(v, "Q: platform  "); !strings.Contains(l, "2 · 1 skipped") {
		t.Fatalf("queue head = %q, want 2 PRs that wait and 1 skipped", l)
	}
	at := func(s string) int { return strings.Index(ansi.Strip(v), s) }
	if !(at("data#5002") < at("data#5003") && at("data#5003") < at("data#5001")) || at("data#5002") < 0 {
		t.Fatalf("want #5002, #5003, then the skipped #5001, though #5001 is the oldest:\n%s", v)
	}
	if l := line(v, "data#5001"); !strings.Contains(l, "skipped") {
		t.Errorf("the skipped row = %q, want skipped in TASK", l)
	}
	if l := line(v, "data#5003"); strings.Contains(l, "skipped") {
		t.Errorf("the row that waits = %q", l)
	}
	faded := strings.SplitN(dim.Render("x"), "x", 2)[0]
	raw := func(s string) string {
		for _, l := range strings.Split(v, "\n") {
			if strings.Contains(ansi.Strip(l), s) {
				return l
			}
		}
		return ""
	}
	if l := raw("data#5001"); !strings.HasPrefix(l, faded) {
		t.Errorf("the skipped row is not dim: %q", l)
	}
	if l := raw("data#5003"); strings.HasPrefix(l, faded) {
		t.Errorf("the row that waits is dim: %q", l)
	}
	e.press("down", "down")
	if f := line(e.m.View(), "all keys"); !strings.Contains(f, "S unskip") {
		t.Fatalf("footer on the skipped row = %q, want S unskip", f)
	}
	e.press("k")
	if f := line(e.m.View(), "all keys"); !strings.Contains(f, "S skip") || strings.Contains(f, "unskip") {
		t.Fatalf("footer on the row that waits = %q, want S skip", f)
	}
}

// A skip ends the review Task of the PR. The row must not leave the Q: tab
// with the Task, or the user could not unskip it. An unskip brings it back to
// the PRs that wait, at the same head. A close still holds the PR at its head.
func TestAPRWhoseReviewASkipEndedStaysOnTheQueueTab(t *testing.T) {
	pr := skipPR(5001, 72*time.Hour)
	ended := func(outcome string) scheduler.TaskView {
		done := rtv("pr-5001", lifecycle.Done, "data#5001 Change 5001", pr.URL, time.Hour)
		done.ReviewHead, done.Outcome = pr.Head, outcome
		return done
	}
	cases := []struct {
		name      string
		outcome   string
		skipped   bool
		wantHead  string
		wantTask  string
		wantListd bool
	}{
		{"the skip just ended the review", "skipped", true, "0 · 1 skipped", "pr-5001 skipped", true},
		{"after the unskip", "skipped", false, "1", "pr-5001 skipped", true},
		{"a close at the same head", "closed", false, "", "", false},
	}
	for _, c := range cases {
		e := newRvEnv(t)
		e.api.lists["queue:platform"] = []github.PullRequest{pr}
		b := reviewBoard()
		b.Tasks = append(b.Tasks, ended(c.outcome))
		if c.skipped {
			b.Skips = []github.Skip{skipOf(pr)}
		}
		e.m.setBoard(b)
		e.open()
		e.press("shift+tab")
		v := e.m.View()
		l := line(v, "data#5001")
		if (l != "") != c.wantListd {
			t.Errorf("%s: the Q: tab lists the PR = %v, want %v:\n%s", c.name, l != "", c.wantListd, v)
			continue
		}
		if !c.wantListd {
			continue
		}
		if h := line(v, "Q: platform  "); !strings.HasSuffix(strings.TrimSpace(h), c.wantHead) {
			t.Errorf("%s: head = %q, want it to end with %q", c.name, h, c.wantHead)
		}
		if !strings.Contains(l, c.wantTask) || strings.Count(l, "skipped") > 1 {
			t.Errorf("%s: row = %q, want %q once", c.name, l, c.wantTask)
		}
	}
}

func TestSKeyTogglesTheSelectedPRsSkip(t *testing.T) {
	e := newRvEnv(t)
	skipped, waits := skipPR(5001, 72*time.Hour), skipPR(5002, time.Hour)
	e.api.lists["queue:platform"] = []github.PullRequest{skipped, waits}
	e.m.setBoard(withSkips(skipped))
	e.open()
	e.press("shift+tab")

	e.press("S")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "reviews-skip" || c[0].URL != waits.URL {
		t.Fatalf("S on a PR that waits sent %+v, want reviews-skip %s at once", c, waits.URL)
	}
	if l := line(e.m.View(), "skip data#5002"); !strings.Contains(l, "ok") {
		t.Fatalf("status line = %q", l)
	}

	e.press("down", "S")
	if c := e.sched.calls[1:]; len(c) != 1 || c[0].Op != "reviews-unskip" || c[0].URL != skipped.URL {
		t.Fatalf("S on a skipped PR sent %+v, want reviews-unskip %s at once", c, skipped.URL)
	}
	if l := line(e.m.View(), "unskip data#5001"); !strings.Contains(l, "ok") {
		t.Fatalf("status line = %q", l)
	}
	e.sched.errs["reviews-skip"] = errors.New("umbrella/data#5002 is merged")
	e.press("k", "S")
	if l := line(e.m.View(), "skip data#5002"); !strings.Contains(l, "is merged") {
		t.Fatalf("status line = %q, want the refusal", l)
	}
}

func TestSKeyAsksBeforeItClosesAnOpenReview(t *testing.T) {
	e := newRvEnv(t)
	b := reviewBoard()
	b.Tasks[0].ID = "pr-4945"
	e.m.setBoard(b)
	e.open()
	e.press("tab")
	e.press("S")
	if len(e.sched.calls) != 0 {
		t.Fatalf("S on a PR with an open review sent %+v before the answer", e.sched.calls)
	}
	if f := line(e.m.View(), "(y/n)"); !strings.Contains(f, "skip data#4945 and close pr-4945?") {
		t.Fatalf("prompt = %q", f)
	}
	e.press("n")
	if len(e.sched.calls) != 0 {
		t.Fatalf("n sent %+v", e.sched.calls)
	}
	if l := line(e.m.View(), "cancelled"); !strings.Contains(l, "skip cancelled") {
		t.Fatalf("status line = %q", l)
	}
	e.press("S", "y")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "reviews-skip" || c[0].URL != urlLogs {
		t.Fatalf("y sent %+v, want reviews-skip %s", c, urlLogs)
	}
}

func TestSKeyOnTheReviewsTabSkipsTheTasksPR(t *testing.T) {
	e := newRvEnv(t)
	e.open()
	e.press("S")
	if f := line(e.m.View(), "(y/n)"); !strings.Contains(f, "skip data#4955 and close R-10?") {
		t.Fatalf("prompt = %q", f)
	}
	e.press("y")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "reviews-skip" || c[0].URL != urlBase {
		t.Fatalf("y sent %+v, want reviews-skip %s", c, urlBase)
	}

	e = newRvEnv(t)
	b := reviewBoard()
	b.Skips = []github.Skip{{URL: urlBase}}
	e.m.setBoard(b)
	e.open()
	if f := line(e.m.View(), "all keys"); !strings.Contains(f, "S unskip") {
		t.Fatalf("footer = %q, want S unskip on the Task of a skipped PR", f)
	}
	e.press("S")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "reviews-unskip" || c[0].URL != urlBase {
		t.Fatalf("S on the Task of a skipped PR sent %+v, want reviews-unskip at once", c)
	}
}

func TestEveryPRTabMarksASkippedPRAndKeepsItsRow(t *testing.T) {
	e := newRvEnv(t)
	b := reviewBoard()
	b.Skips = []github.Skip{{URL: urlBase}}
	e.m.setBoard(b)
	e.open()
	e.press("tab", "tab")
	l := line(e.m.View(), "data#4955")
	if !strings.Contains(l, "skipped") {
		t.Fatalf("the Requested row of a skipped PR = %q, want skipped in TASK", l)
	}
	if strings.Contains(e.m.View(), "1 skipped") {
		t.Fatalf("a tab that applies no queue rule counts skips:\n%s", e.m.View())
	}
	e.sched.review = scheduler.ReviewResult{TaskView: scheduler.TaskView{ID: "pr-4955", Stage: "Reviewing"}, Action: scheduler.ReviewAdded}
	e.press("r")
	if c := e.sched.calls; len(c) != 1 || c[0].Op != "review" || c[0].URL != urlBase {
		t.Fatalf("r on a skipped PR sent %+v, want review %s", c, urlBase)
	}
}
