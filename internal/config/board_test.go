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
		Order:     []string{"Needs you", "Backlog", "Ready", "Queued", "Working", "Waiting", "Done"},
		Collapsed: []string{"Backlog", "Done"},
		Hidden:    []string{},
	}
	missing, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, g := range map[string]*Global{"no file": missing, "no table": load(t, "start_skills = [\"a\"]\n")} {
		if got := g.Board(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: board %+v, want %+v", name, got, want)
		}
	}
}

func TestBoardKeysTakeTheirValuesAndAbsentKeysTakeDefaults(t *testing.T) {
	g := load(t, `
[board]
order = ["done", "needs_you", "working", "waiting", "queued", "ready", "backlog"]
hidden = ["done"]
`)
	want := Board{
		Order:     []string{"Done", "Needs you", "Working", "Waiting", "Queued", "Ready", "Backlog"},
		Collapsed: []string{"Backlog", "Done"},
		Hidden:    []string{"Done"},
	}
	if got := g.Board(); !reflect.DeepEqual(got, want) {
		t.Fatalf("board %+v, want %+v", got, want)
	}
}

func TestBoardEmptyCollapsedMeansNoneAndOrderKeepsTheRest(t *testing.T) {
	g := load(t, "[board]\norder = [\"waiting\", \"ready\"]\ncollapsed = []\n")
	if got := g.Board().Collapsed; len(got) != 0 {
		t.Fatalf("collapsed %v, want none", got)
	}
	want := []string{"Waiting", "Ready", "Needs you", "Backlog", "Queued", "Working", "Done"}
	if got := g.Board().Order; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestBoardRejectsABadSection(t *testing.T) {
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
		path := write(t, filepath.Join(t.TempDir(), "config.toml"), "[board]\n"+body+"\n")
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", body, err, want)
		}
	}
}
