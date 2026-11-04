package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sh(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
	return string(out)
}

// setup makes a temp repo and a wrapper that runs the real wt with an isolated user config.
func setup(t *testing.T) (bin, repo, marker string) {
	t.Helper()
	real, err := Require()
	if err != nil {
		t.Skip(err)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo = filepath.Join(root, "repo")
	marker = filepath.Join(root, "prestart.txt")
	sh(t, root, "git", "init", "-q", "-b", "main", repo)
	sh(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	cfg := filepath.Join(root, "wt.toml")
	os.WriteFile(cfg, []byte("[list]\njson-schema = 1\n\n[pre-start]\nmark = \"echo ran >> "+marker+"\"\n"), 0o644)
	bin = filepath.Join(root, "wt")
	os.WriteFile(bin, []byte("#!/bin/sh\nexec "+real+" --config "+cfg+" \"$@\"\n"), 0o755)
	return bin, repo, marker
}

func TestCreatePathRemove(t *testing.T) {
	bin, repo, marker := setup(t)
	ctx := context.Background()

	if p, err := Path(ctx, bin, repo, "feat/x"); err != nil || p != "" {
		t.Fatalf("before create: %q %v", p, err)
	}
	argv := CreateArgv(bin, repo, "feat/x")
	sh(t, t.TempDir(), argv...)
	wt, err := Path(ctx, bin, repo, "feat/x")
	if err != nil || wt == "" {
		t.Fatalf("Path = %q %v", wt, err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(marker); strings.Count(string(data), "ran") != 1 {
		t.Errorf("pre-start hook runs = %q", data)
	}

	sh(t, t.TempDir(), argv...) // restart: the branch and worktree exist

	sh(t, repo, "git", "branch", "feat/old")
	sh(t, t.TempDir(), CreateArgv(bin, repo, "feat/old")...) // branch exists, no worktree

	for range 2 {
		if err := Remove(ctx, bin, repo, "feat/x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
}

func TestCreateFailsWhenHookFails(t *testing.T) {
	bin, repo, _ := setup(t)
	os.WriteFile(filepath.Join(filepath.Dir(repo), "wt.toml"), []byte("[pre-start]\nbad = \"exit 7\"\n"), 0o644)
	argv := CreateArgv(bin, repo, "feat/y")
	if err := exec.Command(argv[0], argv[1:]...).Run(); err == nil {
		t.Fatal("a failing pre-start hook must fail setup")
	}
}
