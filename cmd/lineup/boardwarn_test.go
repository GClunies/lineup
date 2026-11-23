package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A [board] list that names the gone Queued section keeps its other keys, and
// the Board shows the warning with no "(default layout)".
func TestBoardOptionsShowABoardWarningAndKeepTheLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[board]\norder = [\"queued\", \"done\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := boardOptions(path)
	if want := `config: board.order: section "queued" is gone, lineup add starts each Task (ignored)`; opts.ConfigErr != want {
		t.Errorf("ConfigErr %q, want %q", opts.ConfigErr, want)
	}
	if len(opts.Board.Order) == 0 || opts.Board.Order[0] != "Done" {
		t.Errorf("order %q, want Done first", opts.Board.Order)
	}
}
