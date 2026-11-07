package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prRepo makes a GitHub stand-in: a bare origin with a develop branch and the
// PR's head under refs/pull/7/head, and a main checkout cloned from it.
func prRepo(t *testing.T) (origin, main, head string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	origin, main = filepath.Join(root, "origin.git"), filepath.Join(root, "data")
	author := filepath.Join(root, "author")
	sh(t, root, "git", "init", "-q", "--bare", "-b", "develop", origin)
	sh(t, root, "git", "clone", "-q", origin, author)
	commit(t, author, "init")
	sh(t, author, "git", "push", "-q", "origin", "HEAD:develop")
	sh(t, root, "git", "clone", "-q", origin, main)
	commit(t, author, "the PR's change")
	sh(t, author, "git", "push", "-q", "origin", "HEAD:refs/pull/7/head")
	return origin, main, strings.TrimSpace(sh(t, author, "git", "rev-parse", "HEAD"))
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	sh(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", msg)
}

func TestReviewPathSitsBesideTheMainCheckout(t *testing.T) {
	if got := ReviewPath("/repos/umbrella/data", 4955); got != "/repos/umbrella/data.review-pr-4955" {
		t.Fatalf("ReviewPath = %s", got)
	}
}

func TestAddReviewChecksOutTheHeadDetachedAndRepeats(t *testing.T) {
	_, main, head := prRepo(t)
	path := ReviewPath(main, 7)
	for range 2 {
		if err := AddReview(context.Background(), main, path, 7, "develop", head); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.TrimSpace(sh(t, path, "git", "rev-parse", "HEAD")); got != head {
		t.Fatalf("review worktree at %s, want %s", got, head)
	}
	if err := exec.Command("git", "-C", path, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
		t.Fatal("the review worktree is on a branch, want detached")
	}
}

// Another fetch in the main checkout, for example the user's editor, can hold
// the lock on origin/develop for a moment. AddReview waits it out.
func TestAddReviewRetriesARefLockThatAnotherFetchHolds(t *testing.T) {
	origin, main, head := prRepo(t)
	other := filepath.Join(filepath.Dir(origin), "other")
	sh(t, filepath.Dir(origin), "git", "clone", "-q", origin, other)
	commit(t, other, "a new develop commit")
	sh(t, other, "git", "push", "-q", "origin", "HEAD:develop")

	lock := filepath.Join(main, ".git", "refs", "remotes", "origin", "develop.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(1500*time.Millisecond, func() { os.Remove(lock) })
	if err := AddReview(context.Background(), main, ReviewPath(main, 7), 7, "develop", head); err != nil {
		t.Fatalf("AddReview gave up on a ref lock: %v", err)
	}
}

func TestAddReviewFailsAtOnceOnAnotherFetchError(t *testing.T) {
	_, main, head := prRepo(t)
	start := time.Now()
	if err := AddReview(context.Background(), main, ReviewPath(main, 8), 8, "develop", head); err == nil {
		t.Fatal("AddReview of a PR with no head ref succeeded")
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("AddReview retried an error that is not a ref lock, for %v", d)
	}
}

// A user can delete the folder of a kept worktree by hand. git still lists
// it, and a new review of the same PR must not fail on that.
func TestAddReviewReplacesAWorktreeWhoseFolderIsGone(t *testing.T) {
	_, main, head := prRepo(t)
	path := ReviewPath(main, 7)
	if err := AddReview(context.Background(), main, path, 7, "develop", head); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(path)
	if err := AddReview(context.Background(), main, path, 7, "develop", head); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("worktree not made again: %v", err)
	}
}

func TestRemoveReview(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, origin, path string)
		kept   bool
	}{
		{"clean", func(*testing.T, string, string) {}, false},
		{"unpushed commit", func(t *testing.T, _, path string) { commit(t, path, "a local fix") }, true},
		{"uncommitted change", func(t *testing.T, _, path string) {
			os.WriteFile(filepath.Join(path, "notes.txt"), []byte("x"), 0o644)
		}, true},
		{"commit pushed to the PR", func(t *testing.T, _, path string) {
			commit(t, path, "a suggestion")
			sh(t, path, "git", "push", "-q", "origin", "HEAD:refs/pull/7/head")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			origin, main, head := prRepo(t)
			path := ReviewPath(main, 7)
			if err := AddReview(context.Background(), main, path, 7, "develop", head); err != nil {
				t.Fatal(err)
			}
			c.change(t, origin, path)
			kept, err := RemoveReview(context.Background(), main, path, 7)
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(path)
			if kept != c.kept || (statErr == nil) != c.kept {
				t.Fatalf("kept %v, folder there %v, want %v", kept, statErr == nil, c.kept)
			}
			if listed := strings.Contains(sh(t, main, "git", "worktree", "list"), path); listed != c.kept {
				t.Fatalf("git worktree list has it: %v, want %v", listed, c.kept)
			}
		})
	}
}

func TestRemoveReviewOfAMissingWorktreeIsDone(t *testing.T) {
	_, main, _ := prRepo(t)
	if kept, err := RemoveReview(context.Background(), main, ReviewPath(main, 7), 7); kept || err != nil {
		t.Fatalf("kept %v err %v", kept, err)
	}
}
