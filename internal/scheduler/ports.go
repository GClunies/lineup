package scheduler

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// The Scheduler reaches every outside system through these ports. cmd/lineup
// wires them to the real adapters, and the tests wire them to fakes.

type Window struct{ SessionID, WindowID, BlockID string }

type Status struct{ State, Kind, Msg string }

type Rex interface {
	EnsureSession(ctx context.Context, label string) (string, error)
	NewWindow(ctx context.Context, session, label, cwd string, env map[string]string, argv []string) (Window, error)
	CloseWindow(ctx context.Context, session, window string) error
	DestroySession(ctx context.Context, session string) error
	// Blocks returns the IDs of the live blocks in a session. A missing session has none.
	Blocks(ctx context.Context, session string) (map[string]bool, error)
	ProgramStatus(ctx context.Context, session, block string) (Status, error)
	Type(ctx context.Context, session, block, text string) error
	// Focus shows the block in the Rex app.
	Focus(ctx context.Context, session, window, block string) error
	Capture(ctx context.Context, session, block string) (string, error)
}

type PRKey struct{ Repo, Branch string }

// PRSnapshot is the GitHub adapter's view of one PR, kept opaque here so the
// Scheduler can store it and hand it back for edge detection.
type PRSnapshot struct {
	Number int             `json:"number"`
	URL    string          `json:"url"`
	Raw    json.RawMessage `json:"raw"`
}

type GitHub interface {
	RepoSlug(ctx context.Context, repoPath string) (string, error)
	// Poll returns the newest PR for each key. A key with no PR is absent.
	Poll(ctx context.Context, keys []PRKey) (map[PRKey]PRSnapshot, error)
	// Signals compares two snapshots of one PR. prev is nil before the first.
	Signals(prev, cur *PRSnapshot) []lifecycle.Signal
	MarkReady(ctx context.Context, repo string, number int) error
	Merge(ctx context.Context, repo string, number int) error
}

type Worktrees interface {
	// CreateArgv is the command that creates and sets up the worktree in the Task tab.
	CreateArgv(repo, branch string) []string
	Path(ctx context.Context, repo, branch string) (string, error)
	Remove(ctx context.Context, repo, branch string) error
}

// WorkerSpec is what a Harness needs to start a Worker.
type WorkerSpec struct {
	// Task is the Task ID. The Harness names the agent's session after it.
	Task      string
	Stage     lifecycle.Stage
	Repo      string
	Worktree  string
	Branch    string
	Issue     *Issue
	Text      string
	PRURL     string
	Overrides map[string]string
}

type Harness interface {
	// Prepare readies a new worktree for the Harness, for example by trusting it.
	Prepare(ctx context.Context, repo, worktree string) error
	Command(ctx context.Context, w WorkerSpec) (argv []string, env map[string]string, err error)
}

// The kinds of Tracker that an Issue can come from.
const (
	KindLinear = "linear"
	KindGitHub = "github"
	KindLocal  = "local"
)

// KindOf reads the Tracker from a typed Issue ID: local-7 is local,
// owner/repo#12 is GitHub, and anything else, such as ENG-614, is Linear.
func KindOf(identifier string) string {
	switch {
	case strings.HasPrefix(strings.ToLower(identifier), "local-"):
		return KindLocal
	case strings.Contains(identifier, "/") && strings.Contains(identifier, "#"):
		return KindGitHub
	}
	return KindLinear
}

type Issue struct {
	// Kind is the Tracker: KindLinear, KindGitHub, or KindLocal. It is empty
	// in a Record from before Trackers other than Linear.
	Kind       string `json:"kind,omitempty"`
	Workspace  string `json:"workspace"`
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	BranchName string `json:"branch_name"`
	TeamKey    string `json:"team_key"`
	ProjectID  string `json:"project_id,omitempty"`
	// RepoHint is the Repo that a local Issue names in its repo field.
	RepoHint string `json:"repo_hint,omitempty"`
}

// Tracker is the Kind of Tracker the Issue comes from.
func (is Issue) Tracker() string {
	if is.Kind == "" {
		return KindLinear
	}
	return is.Kind
}

type Tracker interface {
	// Lookup finds the Issue in the Tracker of the given Kind, or in the one
	// that KindOf reads from the identifier when kind is empty.
	Lookup(ctx context.Context, kind, identifier string) (Issue, error)
	// Begin assigns the Issue to the user.
	Begin(ctx context.Context, is Issue) error
	// SetStatus writes the mapped status for a lifecycle event, or nothing when the map has none.
	SetStatus(ctx context.Context, is Issue, event string) error
}

type Config interface {
	Policy(repo string, overrides map[string]string) (lifecycle.Policy, error)
	// RepoForIssue picks the Repo that the Issue maps to. ok is false when no mapping exists.
	RepoForIssue(is Issue) (repo string, ok bool)
	// ResolveRepo turns a name such as "data" or a path into a Repo's main checkout.
	ResolveRepo(name string) (string, error)
}
