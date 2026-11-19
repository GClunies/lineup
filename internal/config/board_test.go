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
		if got, err := g.Board(); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: board %+v, %v, want %+v", name, got, err, want)
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
	if got, err := g.Board(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("board %+v, %v, want %+v", got, err, want)
	}
}

func TestBoardEmptyCollapsedMeansNoneAndOrderKeepsTheRest(t *testing.T) {
	g := load(t, "[board]\norder = [\"waiting\", \"ready\"]\ncollapsed = []\n")
	b, _ := g.Board()
	if got := b.Collapsed; len(got) != 0 {
		t.Fatalf("collapsed %v, want none", got)
	}
	want := []string{"Waiting", "Ready", "Needs you", "Backlog", "Queued", "Working", "Done"}
	if got := b.Order; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
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
		got, berr := g.Board()
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
