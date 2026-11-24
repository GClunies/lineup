package worktree

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

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

func TestHooksListsTheUsersAndTheReposHooks(t *testing.T) {
	bin, repo, _ := setup(t)
	os.MkdirAll(filepath.Join(repo, ".config"), 0o755)
	os.WriteFile(filepath.Join(repo, ".config", "wt.toml"), []byte("[post-start]\ncopy = \"wt step copy-ignored\"\n"), 0o644)
	got, err := Hooks(context.Background(), bin, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []Hook{
		{Type: "pre-start", Source: "user", Name: "mark", Template: "echo ran >> " + filepath.Join(filepath.Dir(repo), "prestart.txt")},
		{Type: "post-start", Source: "project", Name: "copy", Template: "wt step copy-ignored"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hooks %+v, want %+v", got, want)
	}
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
		if err := Remove(ctx, bin, repo, "feat/x", wt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
}

func TestDiffBaseFollowsTheDefaultBranch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	sh(t, root, "git", "init", "-q", "-b", "trunk", origin)
	commit(t, origin, "first")
	first := strings.TrimSpace(sh(t, origin, "git", "rev-parse", "HEAD"))
	clone := filepath.Join(root, "clone")
	sh(t, root, "git", "clone", "-q", origin, clone)
	sh(t, clone, "git", "switch", "-q", "-c", "feat")
	commit(t, clone, "feat")
	if base, err := DiffBase(ctx, clone); err != nil || base != first {
		t.Fatalf("DiffBase = %q, %v, want the branch point %s on origin/trunk", base, err, first)
	}

	commit(t, origin, "second")
	second := strings.TrimSpace(sh(t, origin, "git", "rev-parse", "HEAD"))
	sh(t, clone, "git", "fetch", "-q")
	sh(t, clone, "git", "-c", "user.name=t", "-c", "user.email=t@t", "rebase", "-q", "origin/trunk")
	if base, err := DiffBase(ctx, clone); err != nil || base != second {
		t.Fatalf("after a rebase DiffBase = %q, %v, want the new head of origin/trunk %s", base, err, second)
	}

	// With no origin/HEAD, the base is the merge-base with origin/main.
	sh(t, clone, "git", "remote", "set-head", "origin", "-d")
	sh(t, clone, "git", "update-ref", "refs/remotes/origin/main", first)
	if base, err := DiffBase(ctx, clone); err != nil || base != first {
		t.Fatalf("with no origin/HEAD DiffBase = %q, %v, want the merge-base with origin/main %s", base, err, first)
	}

	if _, err := DiffBase(ctx, filepath.Join(root, "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("DiffBase of a missing folder: err = %v, want fs.ErrNotExist", err)
	}
	if _, err := DiffBase(ctx, root); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("DiffBase of a folder that is no worktree: err = %v, want a git error", err)
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

// A Worker can switch the Task's worktree to another branch, for example for a
// follow-up PR. Remove still finds the worktree by the path that setup
// recorded, keeps it while it holds uncommitted changes, and a new setup of the
// branch then works (ARCH-TREE-006).
func TestRemoveFindsAWorktreeThatMovedToAnotherBranch(t *testing.T) {
	bin, repo, _ := setup(t)
	ctx := context.Background()
	argv := CreateArgv(bin, repo, "feat/x")
	sh(t, t.TempDir(), argv...)
	wt, err := Path(ctx, bin, repo, "feat/x")
	if err != nil || wt == "" {
		t.Fatalf("Path = %q %v", wt, err)
	}
	sh(t, wt, "git", "switch", "-q", "-c", "feat/x-followup")
	notes := filepath.Join(wt, "notes.txt")
	if err := os.WriteFile(notes, []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, bin, repo, "feat/x", wt); !errors.Is(err, ErrUncommitted) {
		t.Fatalf("Remove of the moved dirty worktree: err = %v, want ErrUncommitted", err)
	}
	if _, err := os.Stat(notes); err != nil {
		t.Fatalf("the dirty worktree is gone: %v", err)
	}
	os.Remove(notes)
	if err := Remove(ctx, bin, repo, "feat/x", wt); err != nil {
		t.Fatalf("Remove of the moved clean worktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the moved worktree is still there: %v", err)
	}
	sh(t, t.TempDir(), argv...) // a new setup of feat/x
}

// wt remove fails on a dirty worktree, so Remove refuses it before wt runs,
// with an error that callers can tell apart.
func TestRemoveKeepsAWorktreeWithUncommittedChanges(t *testing.T) {
	bin, repo, _ := setup(t)
	ctx := context.Background()
	sh(t, t.TempDir(), CreateArgv(bin, repo, "feat/dirty")...)
	wt, err := Path(ctx, bin, repo, "feat/dirty")
	if err != nil || wt == "" {
		t.Fatalf("Path = %q %v", wt, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "notes.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, bin, repo, "feat/dirty", ""); !errors.Is(err, ErrUncommitted) {
		t.Fatalf("Remove of a dirty worktree: err = %v, want ErrUncommitted", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "notes.txt")); err != nil {
		t.Fatalf("the dirty worktree is gone: %v", err)
	}
	os.Remove(filepath.Join(wt, "notes.txt"))
	if err := Remove(ctx, bin, repo, "feat/dirty", ""); err != nil {
		t.Fatalf("Remove of the cleaned worktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the cleaned worktree is still there: %v", err)
	}
}

// The user can delete a worktree's folder by hand. Remove of its path prunes
// what git still records of it.
func TestRemoveOfAWorktreeWhoseFolderIsGone(t *testing.T) {
	bin, repo, _ := setup(t)
	ctx := context.Background()
	sh(t, t.TempDir(), CreateArgv(bin, repo, "feat/gone")...)
	wt, err := Path(ctx, bin, repo, "feat/gone")
	if err != nil || wt == "" {
		t.Fatalf("Path = %q %v", wt, err)
	}
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, bin, repo, "feat/gone", wt); err != nil {
		t.Fatalf("Remove of a worktree whose folder is gone: %v", err)
	}
	if list := sh(t, repo, "git", "worktree", "list"); strings.Contains(list, wt) {
		t.Fatalf("git still lists the worktree:\n%s", list)
	}
}
