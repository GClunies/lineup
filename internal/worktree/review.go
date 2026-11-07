package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A review Task reads the PR's code in a detached worktree beside the Repo's
// main checkout. Plain git makes and removes it, never worktrunk: worktrunk
// hooks would run the PR's code.

// ReviewPath is <main checkout's parent>/<main checkout's folder>.review-pr-<number>.
func ReviewPath(main string, number int) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+".review-pr-"+strconv.Itoa(number))
}

// AddReview fetches the PR's head and its base branch into the main checkout,
// then adds the review worktree, detached at head, if it is missing.
func AddReview(ctx context.Context, main, path string, number int, base, head string) error {
	if err := fetch(ctx, main, pullRef(number), base); err != nil {
		return err
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	// --force only overrides git's guard for a registered worktree whose
	// folder someone deleted, because the folder is missing here.
	_, err := git(ctx, main, "worktree", "add", "--force", "--detach", path, head)
	return err
}

// RemoveReview fetches the PR's head, then removes the review worktree when
// it holds no uncommitted change and no commit that is on no remote. A commit
// on the PR's head is on the remote. kept is true when the worktree stays.
func RemoveReview(ctx context.Context, main, path string, number int) (kept bool, err error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err := fetch(ctx, main, pullRef(number)); err != nil {
		return true, err
	}
	prHead, err := git(ctx, main, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return true, err
	}
	changes, err := git(ctx, path, "status", "--porcelain")
	if err != nil || changes != "" {
		return true, err
	}
	local, err := git(ctx, path, "rev-list", "HEAD", "--not", "--remotes", prHead)
	if err != nil || local != "" {
		return true, err
	}
	if _, err := git(ctx, main, "worktree", "remove", path); err != nil {
		return true, err
	}
	return false, nil
}

// lockWaits are the pauses before each new try of a fetch that found a ref
// locked. The per-Repo lock in the Scheduler covers only lineup's fetches. The
// user's editor or shell can fetch in the same checkout at the same moment.
var lockWaits = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

// fetch fetches the refs from origin into the main checkout. It tries again
// when another fetch holds a ref lock.
func fetch(ctx context.Context, main string, refs ...string) error {
	for i := 0; ; i++ {
		_, err := git(ctx, main, append([]string{"fetch", "origin"}, refs...)...)
		if err == nil || i == len(lockWaits) || !strings.Contains(err.Error(), "cannot lock ref") {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(lockWaits[i]):
		}
	}
}

func pullRef(number int) string { return "pull/" + strconv.Itoa(number) + "/head" }

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git -C %s %s: %w: %s", dir, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
