package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// merge pushes a new commit to origin's base branch, as a PR's merge does,
// and gives its oid.
func merge(t *testing.T, origin, base string) string {
	t.Helper()
	other := filepath.Join(filepath.Dir(origin), "merger")
	sh(t, filepath.Dir(origin), "git", "clone", "-q", origin, other)
	commit(t, other, "Merge pull request #7")
	sh(t, other, "git", "push", "-q", "origin", "HEAD:"+base)
	return strings.TrimSpace(sh(t, other, "git", "rev-parse", "HEAD"))
}

func TestQAPathSitsBesideTheMainCheckout(t *testing.T) {
	if got := QAPath("/repos/umbrella/data", 4955); got != "/repos/umbrella/data.qa-pr-4955" {
		t.Fatalf("QAPath = %s", got)
	}
}

// The main checkout does not have the merge commit yet. AddQA fetches it by
// its SHA, so the worktree can check it out, also after a merge into a base
// branch that is not the default branch (ARCH-QA-005).
func TestAddQAChecksOutTheMergeCommitDetachedAndRepeats(t *testing.T) {
	for _, base := range []string{"develop", "release"} {
		t.Run(base, func(t *testing.T) {
			origin, main, _ := prRepo(t)
			at := merge(t, origin, base)
			path := QAPath(main, 7)
			for range 2 {
				if err := AddQA(context.Background(), main, path, at); err != nil {
					t.Fatal(err)
				}
			}
			if got := strings.TrimSpace(sh(t, path, "git", "rev-parse", "HEAD")); got != at {
				t.Fatalf("QA worktree at %s, want %s", got, at)
			}
			if err := exec.Command("git", "-C", path, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
				t.Fatal("the QA worktree is on a branch, want detached")
			}
		})
	}
}

// A QA Worker changes no code, so an uncommitted change is the only work
// that can be lost (PROD-QA-009).
func TestRemoveQA(t *testing.T) {
	for name, dirty := range map[string]bool{"clean": false, "uncommitted change": true} {
		t.Run(name, func(t *testing.T) {
			origin, main, _ := prRepo(t)
			path := QAPath(main, 7)
			if err := AddQA(context.Background(), main, path, merge(t, origin, "develop")); err != nil {
				t.Fatal(err)
			}
			if dirty {
				os.WriteFile(filepath.Join(path, "notes.txt"), []byte("x"), 0o644)
			}
			kept, err := RemoveQA(context.Background(), main, path)
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(path)
			if kept != dirty || (statErr == nil) != dirty {
				t.Fatalf("kept %v, folder there %v, want %v", kept, statErr == nil, dirty)
			}
			if listed := strings.Contains(sh(t, main, "git", "worktree", "list"), path); listed != dirty {
				t.Fatalf("git worktree list has it: %v, want %v", listed, dirty)
			}
		})
	}
	_, main, _ := prRepo(t)
	if kept, err := RemoveQA(context.Background(), main, QAPath(main, 7)); kept || err != nil {
		t.Fatalf("a missing worktree: kept %v err %v", kept, err)
	}
}
