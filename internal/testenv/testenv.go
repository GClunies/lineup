// Package testenv runs a package's tests with a git that does not depend on
// the developer's machine.
package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// Main isolates git, runs the tests, and exits. Call it from TestMain in each
// package whose tests run git or wt.
func Main(m *testing.M) {
	dir, err := os.MkdirTemp("", "testenv")
	if err != nil {
		panic(err)
	}
	if err := isolate(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func isolate(dir string) error {
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[maintenance]\n\tauto = false\n"), 0o644); err != nil {
		return err
	}
	return os.Setenv("GIT_CONFIG_GLOBAL", global)
}
