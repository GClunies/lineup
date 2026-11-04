// Package worktree wraps worktrunk (wt), which creates and removes the git
// worktree of a Task.
package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrMissing = errors.New("worktrunk is required: brew install worktrunk (https://worktrunk.dev)")

// Require finds the wt binary. In zsh, wt is a shell function, so only the
// binary on PATH can run without a shell.
func Require() (string, error) {
	bin, err := exec.LookPath("wt")
	if err != nil {
		return "", ErrMissing
	}
	return filepath.Abs(bin)
}

// createScript picks plain switch when the branch exists (a restart), because
// wt switch --create fails on an existing branch.
const createScript = `if git -C "$2" show-ref --verify --quiet "refs/heads/$3"; then
exec "$1" -C "$2" -y switch --no-cd "$3"
else
exec "$1" -C "$2" -y switch --create --no-cd "$3"
fi`

// CreateArgv is the command that the Scheduler runs in the Task tab. Its exit
// code is the setup result. wt runs the copy steps and pre-start hooks.
func CreateArgv(bin, repo, branch string) []string {
	return []string{"sh", "-c", createScript, "sh", bin, repo, branch}
}

type listOutput struct {
	Items []struct {
		Branch   string `json:"branch"`
		Worktree *struct {
			Path string `json:"path"`
		} `json:"worktree"`
	} `json:"items"`
}

// Path returns the worktree path of the branch, or "" with no error if the
// branch has no worktree.
func Path(ctx context.Context, bin, repo, branch string) (string, error) {
	out, err := run(ctx, bin, "-C", repo, "--config-set", "list.json-schema=2", "list", "--format=json")
	if err != nil {
		return "", err
	}
	var l listOutput
	if err := json.Unmarshal(out, &l); err != nil {
		return "", fmt.Errorf("parse wt list output: %w", err)
	}
	for _, it := range l.Items {
		if it.Branch == branch && it.Worktree != nil {
			return it.Worktree.Path, nil
		}
	}
	return "", nil
}

// Remove deletes the worktree of the branch. A branch with no worktree is not an error.
func Remove(ctx context.Context, bin, repo, branch string) error {
	p, err := Path(ctx, bin, repo, branch)
	if err != nil || p == "" {
		return err
	}
	_, err = run(ctx, bin, "-C", repo, "-y", "remove", "--foreground", branch)
	return err
}

func run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wt %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
