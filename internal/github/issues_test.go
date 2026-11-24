package github

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeIssueGH answers issue view from recorded output. issue edit
// fails with gh's "not found" until label create has run, as for a
// repository that lacks the label.
func fakeIssueGH(t *testing.T, view string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "gh"), filepath.Join(dir, "calls.log")
	made := filepath.Join(dir, "label-made")
	script := `#!/bin/sh
printf '%s\n' "$(IFS='|'; echo "$*")" >> "` + log + `"
case "$1 $2" in
"issue view") cat "` + view + `" ;;
"label create") touch "` + made + `" ;;
"issue edit") if [ ! -f "` + made + `" ]; then echo "'in progress' not found" >&2; exit 1; fi ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestARecordedIssueViewDecodes(t *testing.T) {
	bin, _ := fakeIssueGH(t, filepath.Join("testdata", "issue_view.json"))
	c := &Client{Bin: bin}
	ctx := context.Background()
	is, err := c.Issue(ctx, "octo/sandbox", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Issue{Repo: "octo/sandbox", Number: 2, Title: "Add a GREETING.md that says hello from lineup",
		Body: "Live check for lineup's GitHub Tracker. Add GREETING.md with one line: hello from lineup.",
		URL:  "https://github.com/octo/sandbox/issues/2", State: "OPEN", Labels: []string{"bug"}}
	if !reflect.DeepEqual(is, want) {
		t.Fatalf("view\n got %#v\nwant %#v", is, want)
	}
}

var lifecycleLabels = map[string]string{"started": "in progress", "in_review": "in review"}

func TestLabelChangeKeepsOneLifecycleLabel(t *testing.T) {
	add, remove, ok := LabelChange(lifecycleLabels, "started")
	if !ok || add != "in progress" || !reflect.DeepEqual(remove, []string{"in review"}) {
		t.Fatalf("started: %q %q %v", add, remove, ok)
	}
	if _, _, ok := LabelChange(map[string]string{"started": "in progress"}, "in_review"); ok {
		t.Fatal("an unmapped event changed labels")
	}
	// A QA pass is a lifecycle event like started (PROD-TRACK-012).
	if _, _, ok := LabelChange(lifecycleLabels, "qa_passed"); ok {
		t.Fatal("an unmapped qa_passed changed labels")
	}
	qa := map[string]string{"started": "in progress", "in_review": "in review", "qa_passed": "verified"}
	if add, remove, ok := LabelChange(qa, "qa_passed"); !ok || add != "verified" || !reflect.DeepEqual(remove, []string{"in progress", "in review"}) {
		t.Fatalf("qa_passed: %q %q %v", add, remove, ok)
	}
	for _, end := range []string{"merged", "closed"} {
		add, remove, ok := LabelChange(lifecycleLabels, end)
		if !ok || add != "" || !reflect.DeepEqual(remove, []string{"in progress", "in review"}) {
			t.Fatalf("%s: %q %q %v, want every lifecycle label removed", end, add, remove, ok)
		}
	}
}

func TestTheMergeRemovesTheLifecycleLabel(t *testing.T) {
	view := filepath.Join(t.TempDir(), "view.json")
	os.WriteFile(view, []byte(`{"number":2,"title":"t","body":"","url":"u","state":"CLOSED","labels":[{"name":"in progress"},{"name":"bug"}]}`), 0o644)
	bin, log := fakeIssueGH(t, view)
	os.WriteFile(filepath.Join(filepath.Dir(log), "label-made"), nil, 0o644)
	if err := (&Client{Bin: bin}).SetLifecycleLabel(context.Background(), "o/r", 2, lifecycleLabels, "merged"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); got[len(got)-1] != "issue|edit|2|--repo|o/r|--remove-label|in progress" {
		t.Fatalf("calls %q", got)
	}
	bin, log = fakeIssueGH(t, filepath.Join("testdata", "issue_view.json"))
	if err := (&Client{Bin: bin}).SetLifecycleLabel(context.Background(), "o/r", 2, lifecycleLabels, "closed"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); len(got) != 1 {
		t.Fatalf("an Issue with no lifecycle label must not be edited: %q", got)
	}
}

func TestSetLifecycleLabelCreatesAMissingLabelAndRemovesTheOther(t *testing.T) {
	view := filepath.Join(t.TempDir(), "view.json")
	os.WriteFile(view, []byte(`{"number":2,"title":"t","body":"","url":"u","state":"OPEN","labels":[{"name":"in review"},{"name":"bug"}]}`), 0o644)
	bin, log := fakeIssueGH(t, view)
	if err := (&Client{Bin: bin}).SetLifecycleLabel(context.Background(), "o/r", 2, lifecycleLabels, "started"); err != nil {
		t.Fatal(err)
	}
	edit := "issue|edit|2|--repo|o/r|--add-label|in progress|--remove-label|in review"
	want := []string{
		"issue|view|2|--repo|o/r|--json|number,title,body,url,state,labels",
		edit,
		"label|create|in progress|--repo|o/r|--description|Set by lineup",
		edit,
	}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSetLifecycleLabelRemovesOnlyLabelsTheIssueHas(t *testing.T) {
	view := filepath.Join(t.TempDir(), "view.json")
	os.WriteFile(view, []byte(`{"number":2,"title":"t","body":"","url":"u","state":"OPEN","labels":[{"name":"bug"}]}`), 0o644)
	bin, log := fakeIssueGH(t, view)
	os.WriteFile(filepath.Join(filepath.Dir(log), "label-made"), nil, 0o644)
	if err := (&Client{Bin: bin}).SetLifecycleLabel(context.Background(), "o/r", 2, lifecycleLabels, "started"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); got[len(got)-1] != "issue|edit|2|--repo|o/r|--add-label|in progress" {
		t.Fatalf("calls %q", got)
	}
}

func TestAnUnmappedEventWritesNothing(t *testing.T) {
	bin, log := fakeIssueGH(t, filepath.Join("testdata", "issue_view.json"))
	if err := (&Client{Bin: bin}).SetLifecycleLabel(context.Background(), "o/r", 2, map[string]string{"started": "in progress"}, "in_review"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatalf("gh ran: %q", calls(t, log))
	}
}

// A fix Issue opens in the build Issue's repository, assigned to the user
// (PROD-QA-011). gh prints the new URL.
func TestCreateIssueAssignsTheUser(t *testing.T) {
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "gh"), filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
printf '%s\n' "$(IFS='|'; echo "$*")" >> "` + log + `"
echo https://github.com/o/r/issues/31
`
	os.WriteFile(bin, []byte(script), 0o755)
	is, err := (&Client{Bin: bin}).CreateIssue(context.Background(), "o/r", "Fix QA of o/r#7: t", "findings")
	if err != nil {
		t.Fatal(err)
	}
	want := Issue{Repo: "o/r", Number: 31, Title: "Fix QA of o/r#7: t", Body: "findings", URL: "https://github.com/o/r/issues/31", State: "OPEN"}
	if !reflect.DeepEqual(is, want) {
		t.Fatalf("issue\n got %#v\nwant %#v", is, want)
	}
	if got := calls(t, log); !reflect.DeepEqual(got, []string{"issue|create|--repo|o/r|--title|Fix QA of o/r#7: t|--body|findings|--assignee|@me"}) {
		t.Fatalf("calls %q", got)
	}
}

func TestIssueIDAndBranch(t *testing.T) {
	repo, n, err := ParseIssueID("octo/sandbox#2")
	if err != nil || repo != "octo/sandbox" || n != 2 {
		t.Fatalf("%q %d %v", repo, n, err)
	}
	for _, bad := range []string{"sandbox#2", "o/r#0", "o/r#", "ENG-12", "o/r/x#2"} {
		if _, _, err := ParseIssueID(bad); err == nil {
			t.Errorf("ParseIssueID(%q) passed", bad)
		}
	}
	if b := (Issue{Number: 2, Title: "Add a GREETING.md that says hello"}).Branch(); b != "gh-2-add-a-greeting-md-that-says-hello" {
		t.Errorf("branch = %q", b)
	}
}

func TestTrackerRecord(t *testing.T) {
	dir := t.TempDir()
	if _, on, err := TrackerLogin(dir); on || err != nil {
		t.Fatalf("on before login: %v %v", on, err)
	}
	if err := SaveTracker(dir, "octo"); err != nil {
		t.Fatal(err)
	}
	if login, on, err := TrackerLogin(dir); !on || login != "octo" || err != nil {
		t.Fatalf("%q %v %v", login, on, err)
	}
	if was, err := ForgetTracker(dir); !was || err != nil {
		t.Fatalf("forget: %v %v", was, err)
	}
	if was, _ := ForgetTracker(dir); was {
		t.Fatal("forgot twice")
	}
	if _, on, _ := TrackerLogin(dir); on {
		t.Fatal("still on after forget")
	}
}
