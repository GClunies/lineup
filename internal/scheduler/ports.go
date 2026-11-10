package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

// The Scheduler reaches every outside system through these ports. cmd/lineup
// wires them to the real adapters, and the tests wire them to fakes.

type Window struct{ SessionID, WindowID, BlockID string }

type Status struct{ State, Kind, Msg string }

type Rex interface {
	EnsureSession(ctx context.Context, label string) (string, error)
	NewWindow(ctx context.Context, session, label, cwd string, env map[string]string, argv []string) (Window, error)
	// Split opens argv beside block, in block's tab, and keeps the focus on
	// block. It returns the new block.
	Split(ctx context.Context, session, block, cwd string, argv []string) (string, error)
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

// PRKey names a build Task's PR by its branch. Bot is the review bot that
// gates the Task, or "", so the poll reads the bot's summary only for it.
type PRKey struct{ Repo, Branch, Bot string }

// PRSnapshot is the GitHub adapter's view of one PR, kept opaque here so the
// Scheduler can store it and hand it back for edge detection.
type PRSnapshot struct {
	Number int             `json:"number"`
	URL    string          `json:"url"`
	Raw    json.RawMessage `json:"raw"`
}

// ReviewKey names the PR that a review Task reviews: owner/name and its number.
type ReviewKey struct {
	Repo   string
	Number int
}

// ReviewSnapshot is the state of a reviewed PR that one poll read.
type ReviewSnapshot struct {
	Repo   string // owner/name as GitHub spells it
	Number int
	URL    string
	Title  string
	State  string // OPEN, MERGED, or CLOSED
	Fork   bool
	Head   string
	Base   string
	// ReviewedAt is when the user last submitted a review, or zero. A pending
	// review is not submitted.
	ReviewedAt time.Time
}

type GitHub interface {
	RepoSlug(ctx context.Context, repoPath string) (string, error)
	// ReviewPRs reads each PR by number. A PR that GitHub does not return is absent.
	ReviewPRs(ctx context.Context, keys []ReviewKey) (map[ReviewKey]ReviewSnapshot, error)
	// Poll returns the newest PR for each key. A key with no PR is absent.
	Poll(ctx context.Context, keys []PRKey) (map[PRKey]PRSnapshot, error)
	// Signals compares two snapshots of one PR. prev is nil before the first.
	Signals(prev, cur *PRSnapshot) []lifecycle.Signal
	Merge(ctx context.Context, repo string, number int) error
	// SearchPRs runs every search in one call, as the user, and returns each
	// Key's PRs.
	SearchPRs(ctx context.Context, searches []github.Search) (map[string][]github.PullRequest, error)
	// TeamMembers reads the logins of each team, org/slug.
	TeamMembers(ctx context.Context, teams []string) (map[string][]string, error)
}

type Worktrees interface {
	// CreateArgv is the command that creates and sets up the worktree in the Task tab.
	CreateArgv(repo, branch string) []string
	Path(ctx context.Context, repo, branch string) (string, error)
	Remove(ctx context.Context, repo, branch string) error
	// AddReview fetches the PR into the Repo's main checkout, then adds the
	// review worktree, detached at the PR's head, if it is missing. It
	// returns the worktree's path.
	AddReview(ctx context.Context, repo string, pr ReviewPR) (string, error)
	// RemoveReview fetches, then removes the review worktree unless it holds
	// an uncommitted change or a commit that is on no remote. kept reports
	// that the worktree stays.
	RemoveReview(ctx context.Context, repo, path string, pr ReviewPR) (kept bool, err error)
}

// WorkerSpec is what a Harness needs to start a Worker.
type WorkerSpec struct {
	Task      string
	Stage     lifecycle.Stage
	Repo      string
	Worktree  string
	Branch    string
	Issue     *Issue
	Text      string
	PRURL     string
	Overrides map[string]string
	// Conversation is the ID of a review Worker's Claude conversation. A
	// Reviewing Worker starts it, and a Review ready Worker resumes it.
	Conversation string
	// Name is the Worker's session name, the same as its Rex tab label.
	Name string
	// ReviewCycle is the review bot's score that started a Responding round,
	// or nil.
	ReviewCycle *lifecycle.ReviewCycle
	// Bar is the Task's Landing bar, and Standing is its PR at the latest poll.
	Bar      lifecycle.LandingBar
	Standing lifecycle.Standing
	// Restart is true when a Worker replaces one that the Stage lost, and
	// Steers are the user's steers since the Stage began.
	Restart bool
	Steers  []string
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
	// RepoForGitHub picks the [[repos]] checkout whose origin is the GitHub
	// repository owner/name. ok is false when none is.
	RepoForGitHub(slug string) (repo string, ok bool)
}
