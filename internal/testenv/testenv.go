// Package testenv runs a package's tests with a git that does not depend on
// the developer's machine.
package testenv

import (
	"os"
	"path/filepath"
	"strings"
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

// isolate drops each GIT_ variable that the shell set, for example a git
// hook's GIT_INDEX_FILE or a GIT_DIR that points at a real repo, and gives
// git a global config of its own. That config turns off auto maintenance,
// which can still write into a repo when t.TempDir removes it.
func isolate(dir string) error {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[maintenance]\n\tauto = false\n"), 0o644); err != nil {
		return err
	}
	return os.Setenv("GIT_CONFIG_GLOBAL", global)
}
