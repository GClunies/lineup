package config

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBoardDefaults(t *testing.T) {
	want := Board{
		Order:     []string{"Needs you", "Follow-up", "Backlog", "Ready", "Working", "Waiting", "Done"},
		Collapsed: []string{"Backlog", "Done"},
		Hidden:    []string{},
	}
	missing, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, g := range map[string]*Global{"no file": missing, "no table": load(t, "start_skills = [\"a\"]\n")} {
		if got, warnings, err := g.Board(); err != nil || warnings != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: board %+v, %q, %v, want %+v", name, got, warnings, err, want)
		}
	}
}

func TestBoardKeysTakeTheirValuesAndAbsentKeysTakeDefaults(t *testing.T) {
	g := load(t, `
[board]
order = ["done", "needs_you", "working", "waiting", "ready", "backlog"]
hidden = ["done"]
`)
	want := Board{
		Order:     []string{"Done", "Needs you", "Working", "Waiting", "Ready", "Backlog", "Follow-up"},
		Collapsed: []string{"Backlog", "Done"},
		Hidden:    []string{"Done"},
	}
	if got, warnings, err := g.Board(); err != nil || warnings != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("board %+v, %q, %v, want %+v", got, warnings, err, want)
	}
}

// lineup has no Queued section, so a list that names it loads without it, and
// a warning names each such list.
func TestABoardListThatNamesQueuedLoadsWithoutItAndWarns(t *testing.T) {
	g := load(t, `
[board]
order = ["done", "needs_you", "queued", "working", "waiting", "ready", "backlog"]
collapsed = ["queued", "backlog"]
hidden = ["done"]
`)
	got, warnings, err := g.Board()
	if err != nil {
		t.Fatal(err)
	}
	want := Board{
		Order:     []string{"Done", "Needs you", "Working", "Waiting", "Ready", "Backlog", "Follow-up"},
		Collapsed: []string{"Backlog"},
		Hidden:    []string{"Done"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("board %+v, want %+v", got, want)
	}
	wantWarnings := []string{
		`board.order: section "queued" is gone, lineup add starts each Task (ignored)`,
		`board.collapsed: section "queued" is gone, lineup add starts each Task (ignored)`,
	}
	if !slices.Equal(warnings, wantWarnings) {
		t.Errorf("warnings %q, want %q", warnings, wantWarnings)
	}
}

func TestBoardEmptyCollapsedMeansNoneAndOrderKeepsTheRest(t *testing.T) {
	g := load(t, "[board]\norder = [\"waiting\", \"ready\"]\ncollapsed = []\n")
	b, _, _ := g.Board()
	if got := b.Collapsed; len(got) != 0 {
		t.Fatalf("collapsed %v, want none", got)
	}
	want := []string{"Waiting", "Ready", "Needs you", "Follow-up", "Backlog", "Working", "Done"}
	if got := b.Order; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestBoardTakesFollowUpInEveryList(t *testing.T) {
	g := load(t, "[board]\norder = [\"waiting\", \"follow_up\"]\ncollapsed = [\"follow_up\"]\nhidden = [\"follow_up\", \"done\"]\n")
	want := Board{
		Order:     []string{"Waiting", "Follow-up", "Needs you", "Backlog", "Ready", "Working", "Done"},
		Collapsed: []string{"Follow-up"},
		Hidden:    []string{"Follow-up", "Done"},
	}
	if got, _, err := g.Board(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("board %+v, %v, want %+v", got, err, want)
	}
}

func TestABadBoardTableLoadsWithTheDefaultLayoutAndTheError(t *testing.T) {
	cases := map[string]string{
		`hidden = ["dun"]`:                  `board.hidden: unknown section "dun"`,
		`order = ["Done"]`:                  `board.order: unknown section "Done"`,
		`collapsed = ["nope"]`:              `board.collapsed: unknown section "nope"`,
		`order = ["done", "ready", "done"]`: `board.order: duplicate section "done"`,
		`collapsed = ["ready", "ready"]`:    `board.collapsed: duplicate section "ready"`,
		`hidden = ["done", "done"]`:         `board.hidden: duplicate section "done"`,
		`collapsed = ["needs_you"]`:         `board.collapsed: section "needs_you" cannot be in this list`,
		`hidden = ["backlog", "needs_you"]`: `board.hidden: section "needs_you" cannot be in this list`,
	}
	for body, want := range cases {
		path := write(t, filepath.Join(t.TempDir(), "config.toml"), "start_skills = [\"a\"]\n[board]\n"+body+"\n")
		g, err := Load(path)
		if err != nil {
			t.Errorf("%s: Load failed: %v", body, err)
			continue
		}
		got, _, berr := g.Board()
		if berr == nil || !strings.Contains(berr.Error(), want) {
			t.Errorf("%s: board error %v, want %q", body, berr, want)
		}
		if !reflect.DeepEqual(got, DefaultBoard()) {
			t.Errorf("%s: layout %+v, want the default", body, got)
		}
		if s := g.StartSkills(); len(s) != 1 {
			t.Errorf("%s: the rest of the file was lost: %v", body, s)
		}
	}
}

func TestABadKeyOutsideBoardStillFailsTheLoad(t *testing.T) {
	path := write(t, filepath.Join(t.TempDir(), "config.toml"), "[board]\nhidden = [\"dun\"]\n[github.labels]\nnope = \"x\"\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "github.labels: unknown event") {
		t.Fatalf("error %v", err)
	}
}
