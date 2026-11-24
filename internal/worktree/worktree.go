// Package worktree wraps worktrunk (wt), which creates and removes the git
// worktree of a Task.
package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrMissing = errors.New("worktrunk is required: brew install worktrunk (https://worktrunk.dev)")

// ErrUncommitted refuses to remove a worktree that holds uncommitted changes,
// untracked files included, as wt remove does without -f.
var ErrUncommitted = errors.New("the worktree has uncommitted changes")

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

// worktrees maps the path of each worktree of the Repo to its branch.
func worktrees(ctx context.Context, bin, repo string) (map[string]string, error) {
	out, err := run(ctx, bin, "-C", repo, "--config-set", "list.json-schema=2", "list", "--format=json")
	if err != nil {
		return nil, err
	}
	var l listOutput
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("parse wt list output: %w", err)
	}
	wts := map[string]string{}
	for _, it := range l.Items {
		if it.Worktree != nil {
			wts[it.Worktree.Path] = it.Branch
		}
	}
	return wts, nil
}

// Path returns the worktree path of the branch, or "" with no error if the
// branch has no worktree.
func Path(ctx context.Context, bin, repo, branch string) (string, error) {
	wts, err := worktrees(ctx, bin, repo)
	return pathOf(wts, branch), err
}

// pathOf is the path of the branch's worktree in wts, or "".
func pathOf(wts map[string]string, branch string) string {
	for p, b := range wts {
		if b == branch {
			return p
		}
	}
	return ""
}

// DiffBase is the merge-base of HEAD in the worktree dir with origin's default
// branch, or with origin/main when the clone names no default branch. A rebase
// or a merge of the default branch moves it. A missing dir gives the error of
// os.Stat, which wraps fs.ErrNotExist.
func DiffBase(ctx context.Context, dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	branch, err := git(ctx, dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD")
	if err != nil || branch == "" {
		branch = "origin/main"
	}
	return git(ctx, dir, "merge-base", "HEAD", branch)
}

// Remove deletes the worktree at path, whatever branch it holds now, or else
// the worktree of the branch. With neither, it does nothing. A worktree with
// uncommitted changes stays, and the error wraps ErrUncommitted. git reads
// the changes, because wt's message is for people. wt prunes a worktree whose
// folder is gone.
func Remove(ctx context.Context, bin, repo, branch, path string) error {
	wts, err := worktrees(ctx, bin, repo)
	if err != nil {
		return err
	}
	if _, ok := wts[path]; !ok {
		if path = pathOf(wts, branch); path == "" {
			return nil
		}
	}
	if _, err := os.Stat(path); err == nil {
		changes, err := git(ctx, path, "status", "--porcelain")
		if err != nil {
			return err
		}
		if changes != "" {
			return fmt.Errorf("%w: %s", ErrUncommitted, path)
		}
	}
	_, err = run(ctx, bin, "-C", repo, "-y", "remove", "--foreground", path)
	return err
}

// Hook is one command that worktrunk runs at a hook, from the user's config
// or from the Repo's .config/wt.toml.
type Hook struct {
	Type     string `json:"type"`   // such as pre-start
	Source   string `json:"source"` // user or project
	Name     string `json:"name"`
	Template string `json:"template"`
}

// Hooks lists the hook commands that wt runs for the Repo.
func Hooks(ctx context.Context, bin, repo string) ([]Hook, error) {
	out, err := run(ctx, bin, "-C", repo, "hook", "show", "--format", "json")
	if err != nil {
		return nil, err
	}
	var hooks []Hook
	if err := json.Unmarshal(out, &hooks); err != nil {
		return nil, fmt.Errorf("parse wt hook show output: %w", err)
	}
	return hooks, nil
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
