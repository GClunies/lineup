package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

// The Scheduler reaches every outside system through these ports. cmd/lineup
// wires them to the real adapters, and the tests wire them to fakes.

type Window struct{ SessionID, WindowID, BlockID string }

type Status struct{ State, Kind, Msg string }

// Pane is one block of a tab: its label, and the name and argv of the
// program in its foreground, such as hunk. A script's interpreter is the
// program, and the script is its first argument.
type Pane struct {
	BlockID, Label, Program string
	Args                    []string
}

type Rex interface {
	EnsureSession(ctx context.Context, label string) (string, error)
	// RenameSession gives the session the label, unless another session has
	// it. A missing session is no error.
	RenameSession(ctx context.Context, session, label string) error
	// NewWindow opens a tab that runs argv. With keep, the tab stays after argv exits.
	NewWindow(ctx context.Context, session, label, cwd string, env map[string]string, argv []string, keep bool) (Window, error)
	// Split opens argv beside block, in block's tab, and keeps the focus on
	// block. It returns the new block.
	Split(ctx context.Context, session, block, cwd string, argv []string) (string, error)
	// NewBlock opens argv in a new block of block's tab and returns the new block.
	NewBlock(ctx context.Context, session, block, cwd string, env map[string]string, argv []string) (string, error)
	// CloseBlock closes one block. Its tab stays while it holds another block.
	CloseBlock(ctx context.Context, session, block string) error
	CloseWindow(ctx context.Context, session, window string) error
	DestroySession(ctx context.Context, session string) error
	// Blocks returns the IDs of the live blocks in a session. A missing session has none.
	Blocks(ctx context.Context, session string) (map[string]bool, error)
	// Panes returns the blocks of a tab. A block whose process Rex cannot
	// read has no Program.
	Panes(ctx context.Context, session, window string) ([]Pane, error)
	// IdleShell reports whether a block holds only a plain tab's shell, with
	// nothing in its foreground. Rex restores every block that way after a
	// restart, so lineup's program in it is gone.
	IdleShell(ctx context.Context, session, block string) (bool, error)
	// LastExit returns the exit code of block's program in a tab that Rex
	// kept after the exit. exited is false while the program runs.
	LastExit(ctx context.Context, session, block string) (code int, exited bool, err error)
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
	Number int    `json:"number"`
	URL    string `json:"url"`
	// ClosedAt is when the PR merged or closed, or zero while it is open.
	ClosedAt time.Time       `json:"closed_at,omitzero"`
	Raw      json.RawMessage `json:"raw"`
}

// ReviewKey names the PR that a review Task reviews: owner/name and its number.
type ReviewKey struct {
	Repo   string
	Number int
}

// URL is the PR's GitHub URL. A skip compares it without case.
func (k ReviewKey) URL() string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", k.Repo, k.Number)
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

// ErrUncommitted is the error of a worktree removal that kept the worktree,
// because the worktree holds uncommitted changes.
var ErrUncommitted = errors.New("the worktree has uncommitted changes")

// ErrNoWorktree is the error of a read in a worktree whose folder is gone.
var ErrNoWorktree = errors.New("the worktree is gone")

type Worktrees interface {
	// CreateArgv is the command that creates and sets up the worktree in the Task tab.
	CreateArgv(repo, branch string) []string
	Path(ctx context.Context, repo, branch string) (string, error)
	// Remove removes the worktree at path, whatever branch it holds now, or
	// the branch's worktree when no worktree is at path. The error wraps
	// ErrUncommitted when the worktree holds uncommitted changes, and the
	// worktree stays.
	Remove(ctx context.Context, repo, branch, path string) error
	// DiffBase is the merge-base of the worktree's HEAD with the default
	// branch. The error wraps ErrNoWorktree when the worktree's folder is gone.
	DiffBase(ctx context.Context, worktree string) (string, error)
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
	Task string
	// Kind is the Task's Kind. Done is a Stage of both Kinds, so the Stage
	// alone does not tell it.
	Kind      lifecycle.Kind
	Stage     lifecycle.Stage
	Repo      string
	Worktree  string
	Branch    string
	Issue     *Issue
	Text      string
	PRURL     string
	Overrides map[string]string
	// Conversation is the ID of the Task's Claude conversation. The Task's
	// first Worker starts it, and a Worker in a new tab resumes it.
	Conversation string
	// Name is the Worker's session name, the same as its Rex tab label.
	Name string
	// ReviewCycle is the review bot's score that started a Responding round,
	// or nil.
	ReviewCycle *lifecycle.ReviewCycle
	// Bar is the Task's Landing bar, and Standing is its PR at the latest poll.
	Bar      lifecycle.LandingBar
	Standing lifecycle.Standing
	// Restart is true when the Stage lost a Worker since it began. Steers
	// are the user's steers since the Stage began. Only a Worker in a new
	// conversation gets them, because a resumed one has them already.
	Restart bool
	Steers  []string
	// ReadOnly is true for the Worker of a Done build Task whose worktree
	// lineup removed. It runs in the Repo's main checkout with no tool that
	// writes files.
	ReadOnly bool
}

type Harness interface {
	// Prepare readies a new worktree for the Harness, for example by trusting it.
	Prepare(ctx context.Context, repo, worktree string) error
	// Prompt is the message that gives the Worker w.Stage. It is "" for a
	// Stage with no prompt, such as Review ready.
	Prompt(ctx context.Context, w WorkerSpec) (string, error)
	// Command starts a Worker in a new tab. A prompt starts the conversation
	// w.Conversation with it. No prompt resumes w.Conversation.
	Command(ctx context.Context, w WorkerSpec, prompt string) (argv []string, env map[string]string, err error)
	// HasConversation reports whether the Harness still holds w.Conversation, so a
	// resume finds it. A resume of a missing conversation exits after a few seconds,
	// and Rex then closes the tab with its error.
	HasConversation(ctx context.Context, w WorkerSpec) bool
	// Usage is what the conversation of each spec spent, in the order of ws.
	// An entry is nil when the usage is unknown: the Task has no
	// conversation, or the Harness holds no transcript of it that it can read.
	Usage(ctx context.Context, ws []WorkerSpec) []*Usage
}

// The kinds of Tracker that an Issue can come from.
const (
	KindLinear = "linear"
	KindGitHub = "github"
	KindLocal  = "local"
)

// KindOf reads the Tracker from a typed Issue ID: LOCAL-7, and <PREFIX>-<n>
// for each Repo prefix, such as HOD-3, are local, owner/repo#12 is GitHub,
// and anything else, such as ENG-614, is Linear. The checks ignore case, and
// prefixes are in lower case.
func KindOf(identifier string, prefixes []string) string {
	id := strings.ToLower(identifier)
	switch {
	case strings.HasPrefix(id, "local-"):
		return KindLocal
	case strings.Contains(id, "/") && strings.Contains(id, "#"):
		return KindGitHub
	}
	if p, _, ok := SplitID(id); ok && slices.Contains(prefixes, p) {
		return KindLocal
	}
	return KindLinear
}

// CanonicalID is the one form of an ID: owner/repo#12 as it is, and every
// other ID in upper case, such as ENG-614, LOCAL-7, HOD-3, AH-65, or PR-12.
func CanonicalID(id string) string {
	if KindOf(id, nil) == KindGitHub {
		return id
	}
	return strings.ToUpper(id)
}

var prefixedID = regexp.MustCompile(`^(.+)-([1-9][0-9]*)$`)

// SplitID reads the prefix, in lower case, and n from the ID <prefix>-<n>.
// ok is false for an ID of another form.
func SplitID(id string) (prefix string, n int, ok bool) {
	m := prefixedID.FindStringSubmatch(strings.ToLower(id))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	return m[1], n, err == nil
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
	// RepoForIssue picks the Repo that the Issue maps to. The error wraps
	// ErrNoRepo when no mapping exists, and says why. It is a *RepoAsk when
	// the Issue fits more than one Repo and nothing in it picks one.
	RepoForIssue(ctx context.Context, is Issue) (repo string, err error)
	// PickedRepo records the Repo that the user passed for the Issue, so
	// that the next add finds it in the Issue.
	PickedRepo(ctx context.Context, is Issue, repo string) error
	// ResolveRepo turns a name such as "data", an owner/repo key, or a path
	// into a Repo's main checkout.
	ResolveRepo(name string) (string, error)
	// RepoTracker is the Kind of Tracker whose Issues the Repo takes, or ""
	// for a Repo that takes none, and the Repo's owner/repo key.
	RepoTracker(repo string) (key, tracker string)
	// RepoForGitHub picks the checkout below the roots whose origin is the
	// GitHub repository owner/name. ok is false when none is.
	RepoForGitHub(slug string) (repo string, ok bool)
	// RepoPrefix is the prefix of the Repo's Local Issue and ad hoc Task IDs,
	// in lower case, such as hod, or "" for LOCAL-<n> and AH-<n>.
	RepoPrefix(repo string) string
	// SetUp reports whether the Repo has its own [repos."owner/repo"]
	// table, which a build Task needs, and the Repo's owner/repo key.
	SetUp(repo string) (key string, ok bool)
	// Merged is what lineup does after the PR of a build Task in the Repo
	// merges, from the Repo's [merged] tables and the Task's overrides. An
	// error comes with a value that the caller can still use: the value of
	// the tables, without the bad override.
	Merged(repo string, overrides map[string]string) (Merged, error)
}

// Merged is what lineup does with a build Task's tab and worktree after its
// PR merges.
type Merged struct {
	// CloseTab closes the tab once the Worker is not busy.
	CloseTab bool
	// KeepWorktree is how long lineup keeps the worktree after the tab's close.
	KeepWorktree time.Duration
}

// LocalIssues is the Local Tracker's folder, as the ad hoc Task IDs need it.
type LocalIssues interface {
	// Highest is the highest n of the Local Issues <prefix>-<n>, or 0.
	Highest(prefix string) (int, error)
}
