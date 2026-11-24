package board

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

// fakeGH is a gh that answers the user's own Issues from the output that
// internal/github recorded. While the file fail exists, each search fails.
type fakeGH struct {
	bin, fail string
}

func newFakeGH(t *testing.T) fakeGH {
	t.Helper()
	dir := t.TempDir()
	td, err := filepath.Abs(filepath.Join("..", "github", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	g := fakeGH{bin: filepath.Join(dir, "gh"), fail: filepath.Join(dir, "fail")}
	script := `#!/bin/sh
case "$*" in
"search issues"*)
  if [ -f "` + g.fail + `" ]; then echo "HTTP 401: Bad credentials" >&2; exit 1; fi
  case "$*" in
  *--assignee*) cat "` + filepath.Join(td, "own_assigned.json") + `" ;;
  *) cat "` + filepath.Join(td, "own_authored.json") + `" ;;
  esac ;;
"api graphql"*) cat "` + filepath.Join(td, "own_blockers.json") + `" ;;
*) echo "unexpected: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(g.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g fakeGH) failing(t *testing.T, on bool) {
	t.Helper()
	if !on {
		os.Remove(g.fail)
		return
	}
	if err := os.WriteFile(g.fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// githubEnv is mineEnv with the GitHub Tracker on for octo, one checkout
// with the key octo/sandbox, and a Working Task that holds octo/sandbox#16.
// *on turns the Tracker off.
func githubEnv(t *testing.T) (*workEnv, fakeGH, *bool) {
	t.Helper()
	e, _ := mineEnv(t)
	g, on := newFakeGH(t), true
	e.m.opts.GitHub = GitHub{
		Login:     func() (string, bool, error) { return "octo", on, nil },
		Client:    &github.Client{Bin: g.bin},
		Backlog:   "backlog",
		Checkouts: func() []config.Checkout { return []config.Checkout{{Path: "/repos/octo/sandbox", Key: "octo/sandbox"}} },
	}
	e.m = feed(e.m, boardMsg(scheduler.BoardView{Cap: 5, Tasks: []scheduler.TaskView{
		tv("octo/sandbox#16", lifecycle.Working, "sandbox", "Add a GREETING.md", "", 0),
	}}))
	e.m = openAll(drain(e.m, e.m.loadMine()))
	e.m.width = 200
	return e, g, &on
}

func githubRows(m Model) map[string]issueRow {
	rows := map[string]issueRow{}
	for _, r := range m.issues {
		if r.Kind == scheduler.KindGitHub {
			rows[r.ID] = r
		}
	}
	return rows
}

func TestTheBoardShowsTheUsersOwnGitHubIssues(t *testing.T) {
	e, _, _ := githubEnv(t)
	rows := githubRows(e.m)
	for _, c := range []struct{ name, id string }{
		{"a PR", "octo/sandbox#13"},
		{"opened by someone else, with no assignee", "octo/sandbox#15"},
		{"held by a Task", "octo/sandbox#16"},
		{"in a repo with no checkout", "upstream/tool#3"},
	} {
		t.Run("no row: "+c.name, func(t *testing.T) {
			if _, ok := rows[c.id]; ok {
				t.Fatalf("%s has a row", c.id)
			}
		})
	}
	for _, c := range []struct {
		name, id, group, note string
		level                 int
	}{
		{"assigned, with the backlog label", "octo/sandbox#10", groupBacklog, "", 0},
		{"blocked by an open Issue, its other blocker closed", "octo/sandbox#11", groupUpNext, "blocked by #12", 1},
		{"opened by the user, with no assignee", "octo/sandbox#12", groupUpNext, "", 0},
		{"its only blocker closed", "octo/sandbox#14", groupUpNext, "unblocked", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, ok := rows[c.id]
			if !ok {
				t.Fatalf("%s has no row", c.id)
			}
			if r.Group != c.group || r.note() != c.note || r.Level != c.level || r.Repo != "sandbox" || r.priorityText() != "" {
				t.Errorf("%s: group %q note %q level %d repo %q priority %q, want %q %q %d", c.id, r.Group, r.note(), r.Level, r.Repo, r.priorityText(), c.group, c.note, c.level)
			}
		})
	}
	out := screen(e.m)
	inOrder(t, out, "Backlog 2", "sandbox#10", "Up next 5", "sandbox#12", "sandbox#14", "sandbox#11", "Working 1", "sandbox#16")
	if l := lineOf(t, out, "sandbox#11"); !strings.Contains(l, "Drop old API keys") || !strings.Contains(l, "blocked") {
		t.Errorf("the sandbox#11 row must show its title and blocked: %q", l)
	}
	if l := lineOf(t, out, "sandbox#14"); !strings.Contains(l, "unblocked") {
		t.Errorf("the sandbox#14 row must show unblocked: %q", l)
	}
	e.m.sel = "octo/sandbox#11"
	if f := footerAt(e.m, 200); !strings.Contains(f, "\n sandbox#11 · Up next · sandbox · blocked by #12\n") {
		t.Errorf("the strip must name the blocker as #12:\n%s", f)
	}
}

func TestAGitHubRowNeedsACheckoutWithItsRepoKeyInAnyCase(t *testing.T) {
	e, _, _ := githubEnv(t)
	keys := []string{"OCTO/Sandbox", "Upstream/TOOL"}
	e.m.opts.GitHub.Checkouts = func() []config.Checkout {
		var cs []config.Checkout
		for _, k := range keys {
			cs = append(cs, config.Checkout{Path: "/repos/" + k, Key: k})
		}
		return cs
	}
	e.m = drain(e.m, e.m.loadMine())
	rows := githubRows(e.m)
	for _, id := range []string{"octo/sandbox#10", "upstream/tool#3"} {
		if _, ok := rows[id]; !ok {
			t.Errorf("%s has no row with the checkout keys %q: %v", id, keys, rowIDs(e.m.issues))
		}
	}
	keys = nil
	e.m = drain(e.m, e.m.loadMine())
	if rows := githubRows(e.m); len(rows) != 0 {
		t.Fatalf("GitHub rows with no checkout: %v", rowIDs(e.m.issues))
	}
}

func TestATaskHoldsAGitHubRowOnlyByTheExactID(t *testing.T) {
	e, _, _ := githubEnv(t)
	v := e.m.board
	v.Tasks = append(v.Tasks, tv("Octo/Sandbox#12", lifecycle.Working, "sandbox", "Split billing ingest", "", 0))
	e.m = feed(e.m, boardMsg(v))
	if _, ok := githubRows(e.m)["octo/sandbox#12"]; !ok {
		t.Fatal("a Task with another case of the ID hid octo/sandbox#12")
	}
}

func TestAnEmptyBacklogLabelPutsEveryGitHubIssueInUpNext(t *testing.T) {
	e, _, _ := githubEnv(t)
	e.m.opts.GitHub.Backlog = ""
	e.m.setBoard(e.m.board)
	if g := githubRows(e.m)["octo/sandbox#10"].Group; g != groupUpNext {
		t.Fatalf("#10 in %q", g)
	}
}

func TestSOnAGitHubRowAddsAndStartsTheIssue(t *testing.T) {
	e, _, _ := githubEnv(t)
	e.m.sel = "octo/sandbox#12"
	e.press("s", "enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add"}) {
		t.Fatalf("ops %v, want one add", got)
	}
	if a := lastAdd(t, e); a.Issue != "octo/sandbox#12" || a.Kind != scheduler.KindGitHub {
		t.Fatalf("add %+v", a)
	}
}

func TestFOnAGitHubRowShowsTheRenderedBody(t *testing.T) {
	e, _, _ := githubEnv(t)
	e.m.sel = "octo/sandbox#11"
	e.press("f")
	out := ansi.Strip(e.m.vp.View())
	for _, want := range []string{"octo/sandbox#11", "Drop old API keys", "octo/sandbox · no labels · octo, hubot", "blocked by #12", "Goal", "Drop the old API keys."} {
		if !strings.Contains(out, want) {
			t.Errorf("detail lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## Goal") {
		t.Errorf("the body is not rendered:\n%s", out)
	}
}

func TestOOnAGitHubRowOpensTheIssueAndCDoesNothing(t *testing.T) {
	e, _, _ := githubEnv(t)
	e.m.sel = "octo/sandbox#14"
	e.press("c")
	if e.m.mode != modeNormal {
		t.Fatalf("c opened mode %d", e.m.mode)
	}
	e.press("o")
	if !slices.Equal(e.opened, []string{"https://github.com/octo/sandbox/issues/14"}) {
		t.Fatalf("opened %v", e.opened)
	}
}

func TestGitHubRowsShowOnlyWhileTheTrackerIsOn(t *testing.T) {
	e, _, on := githubEnv(t)
	if len(githubRows(e.m)) == 0 {
		t.Fatal("no GitHub rows while the Tracker is on")
	}
	*on = false
	e.m = drain(e.m, e.m.loadMine())
	if rows := githubRows(e.m); len(rows) != 0 {
		t.Fatalf("GitHub rows while the Tracker is off: %v", rows)
	}
	if !slices.ContainsFunc(e.m.issues, func(r issueRow) bool { return r.ID == "ENG-9" }) {
		t.Fatal("the Linear rows left with the GitHub rows")
	}
}

func TestAFailedGitHubReadShowsAStatusAndKeepsTheRows(t *testing.T) {
	e, g, _ := githubEnv(t)
	g.failing(t, true)
	e.m = drain(e.m, e.m.loadMine())
	if !e.m.statusE || !strings.Contains(e.m.status, "GitHub Issues: ") || !strings.Contains(e.m.status, "Bad credentials") {
		t.Fatalf("status %q", e.m.status)
	}
	if len(githubRows(e.m)) != 4 || !slices.ContainsFunc(e.m.issues, func(r issueRow) bool { return r.ID == "ENG-9" }) {
		t.Fatalf("rows after the failure: %v", rowIDs(e.m.issues))
	}
	g.failing(t, false)
	e.m = drain(e.m, e.m.loadMine())
	if e.m.status != "" {
		t.Fatalf("status %q after a good read", e.m.status)
	}
}

func TestAFailedFirstGitHubReadLeavesTheOtherRows(t *testing.T) {
	e, _ := mineEnv(t)
	g := newFakeGH(t)
	g.failing(t, true)
	e.m.opts.GitHub = GitHub{Login: func() (string, bool, error) { return "octo", true, nil }, Client: &github.Client{Bin: g.bin}}
	e.m = drain(e.m, e.m.loadMine())
	if !strings.Contains(e.m.status, "GitHub Issues: ") || len(githubRows(e.m)) != 0 || len(e.m.issues) == 0 {
		t.Fatalf("status %q rows %v", e.m.status, rowIDs(e.m.issues))
	}
}

// staleGH is a gh whose search still returns octo/sandbox#19 after it
// closes, as GitHub's search index does for a few seconds. #20 is blocked
// by #19. The file closed makes #19 closed in the search hit and in
// GraphQL (PROD-ISSUE-006).
type staleGH struct{ bin, closed string }

func newStaleGH(t *testing.T) staleGH {
	t.Helper()
	dir := t.TempDir()
	g := staleGH{bin: filepath.Join(dir, "gh"), closed: filepath.Join(dir, "closed")}
	hit := func(n int, state, body string) string {
		return fmt.Sprintf(`{"assignees":[{"login":"octo"}],"author":{"login":"octo"},"body":%q,"isPullRequest":false,"labels":[],"number":%d,"state":%q,"repository":{"nameWithOwner":"octo/sandbox"},"title":"Issue %d","url":"https://github.com/octo/sandbox/issues/%d"}`, body, n, state, n, n)
	}
	script := `#!/bin/sh
state=open; gql=OPEN
if [ -f "` + g.closed + `" ]; then state=closed; gql=CLOSED; fi
case "$*" in
"search issues"*--assignee*) echo '[` + hit(20, "open", "Blocked by #19") + `,` + hit(19, "'$state'", "") + `]' ;;
"search issues"*) echo '[]' ;;
"api graphql"*) echo '{"data":{"i0":{"issueOrPullRequest":{"state":"'$gql'"}}}}' ;;
*) echo "unexpected: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(g.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAGitHubRowIsUnblockedAtTheFirstRefreshAfterItsBlockerCloses(t *testing.T) {
	e, _ := mineEnv(t)
	g := newStaleGH(t)
	e.m.opts.GitHub = GitHub{
		Login:     func() (string, bool, error) { return "octo", true, nil },
		Client:    &github.Client{Bin: g.bin},
		Checkouts: func() []config.Checkout { return []config.Checkout{{Path: "/repos/octo/sandbox", Key: "octo/sandbox"}} },
	}
	e.m = drain(e.m, e.m.loadMine())
	if got := githubRows(e.m)["octo/sandbox#20"].status(); got != "blocked" {
		t.Fatalf("#20 is %q while #19 is open, want blocked", got)
	}
	if err := os.WriteFile(g.closed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.m = drain(e.m, e.m.loadMine())
	if got := githubRows(e.m)["octo/sandbox#20"].status(); got != "unblocked" {
		t.Fatalf("#20 is %q after #19 closed, want unblocked", got)
	}
}
