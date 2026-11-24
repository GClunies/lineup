package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// rexweb-pane is a bash script, so the kernel runs it as the interpreter with
// the script's path as the first argument.
func TestProcArgsReadsTheScriptThatAnInterpreterRuns(t *testing.T) {
	script := filepath.Join(t.TempDir(), "rexweb pane dir", "rexweb-pane")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, "https://github.com/acme/data/pull/7")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	want := []string{"/bin/sh", script, "https://github.com/acme/data/pull/7"}
	if got := procArgs(cmd.Process.Pid); !slices.Equal(got, want) {
		t.Fatalf("procArgs = %q, want %q", got, want)
	}
}

func TestProcArgsOfNoProcessIsNil(t *testing.T) {
	if got := procArgs(0); got != nil {
		t.Fatalf("procArgs(0) = %q, want nil", got)
	}
}
