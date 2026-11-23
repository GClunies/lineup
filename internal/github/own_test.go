package github

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeOwnGH answers the two searches and the blocker read from recorded
// output. blockers is the GraphQL reply; "fail" makes that call fail.
func fakeOwnGH(t *testing.T, blockers string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "gh"), filepath.Join(dir, "calls.log")
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	reply := `cat "` + filepath.Join(td, blockers) + `"`
	if blockers == "fail" {
		reply = `echo "HTTP 502" >&2; exit 1`
	}
	script := `#!/bin/sh
(IFS='|'; printf '%s' "$*") | tr '\n' ' ' >> "` + log + `"
echo >> "` + log + `"
case "$*" in
"search issues"*--assignee*) cat "` + filepath.Join(td, "own_assigned.json") + `" ;;
"search issues"*--author*) cat "` + filepath.Join(td, "own_authored.json") + `" ;;
"api graphql"*) ` + reply + ` ;;
*) echo "unexpected: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func ownByID(own []OwnIssue) map[string]OwnIssue {
	m := map[string]OwnIssue{}
	for _, is := range own {
		m[is.ID()] = is
	}
	return m
}

func TestOwnIssuesKeepsTheUsersOwnOpenIssues(t *testing.T) {
	bin, log := fakeOwnGH(t, "own_blockers.json")
	own, err := (&Client{Bin: bin}).OwnIssues(context.Background(), "octo")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, is := range own {
		ids = append(ids, is.ID())
	}
	want := []string{"octo/sandbox#10", "octo/sandbox#11", "octo/sandbox#14", "upstream/tool#3", "octo/sandbox#12", "octo/sandbox#16"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("IDs %v, want %v", ids, want)
	}
	by := ownByID(own)
	if _, ok := by["octo/sandbox#13"]; ok {
		t.Error("the PR #13 is an Issue")
	}
	if _, ok := by["octo/sandbox#15"]; ok {
		t.Error("#15, which hubot opened with no assignee, is the user's own")
	}
	if got := by["octo/sandbox#11"]; !reflect.DeepEqual(got.Assignees, []string{"octo", "hubot"}) || got.Repo != "octo/sandbox" ||
		got.URL != "https://github.com/octo/sandbox/issues/11" || got.State != "OPEN" || !strings.Contains(got.Body, "Drop the old API keys") {
		t.Fatalf("#11 %#v", got)
	}
	if got := by["octo/sandbox#10"].Labels; !reflect.DeepEqual(got, []string{"Backlog"}) {
		t.Fatalf("#10 labels %v", got)
	}
	blockers := by["octo/sandbox#11"].Blockers
	if len(blockers) != 2 {
		t.Fatalf("#11 blockers %v", blockers)
	}
	if closedInGraphQL := blockers[0]; closedInGraphQL != (Blocker{"octo/sandbox#7", false}) {
		t.Errorf("blocker closed in the GraphQL reply: %v", closedInGraphQL)
	}
	if openFromSearch := blockers[1]; openFromSearch != (Blocker{"octo/sandbox#12", true}) {
		t.Errorf("blocker open in a search: %v", openFromSearch)
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 3 {
		t.Fatalf("calls:\n%s", calls)
	}
	for i, want := range []string{"|--assignee|octo", "|--author|octo|--no-assignee", "p0=7"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("call %d %q lacks %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[0], "search|issues|--state|open|") {
		t.Errorf("search %q is not for open Issues", lines[0])
	}
}

func TestOwnIssuesKeepsTheIssuesWhenTheBlockerReadFails(t *testing.T) {
	bin, _ := fakeOwnGH(t, "fail")
	own, err := (&Client{Bin: bin}).OwnIssues(context.Background(), "octo")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("err %v", err)
	}
	if got := ownByID(own)["octo/sandbox#14"].Blockers; !reflect.DeepEqual(got, []Blocker{{"octo/sandbox#7", true}}) {
		t.Fatalf("#14 blockers %v", got)
	}
}

func TestBlockedByReadsTheOrchestratorsLines(t *testing.T) {
	cases := []struct{ body, want string }{
		{"Blocked by #12", "o/r#12"},
		{"text\nblocked by #3, #4\n- Blocked by other/repo#5", "o/r#3 o/r#4 other/repo#5"},
		{"> **Blocked by** #6", "o/r#6"},
		{"Blocked by #2\nBlocked by #2", "o/r#2"},
		{"Fixes #9\nThis is not blocked by anything.", ""},
		{"## Blocked by #8", ""},
	}
	for _, c := range cases {
		if got := strings.Join(BlockedBy("o/r", c.body), " "); got != c.want {
			t.Errorf("BlockedBy(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}
