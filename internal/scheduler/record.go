package scheduler

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// Job is what runs in the Task tab now.
type Job string

const (
	JobNone   Job = ""
	JobSetup  Job = "setup"
	JobWorker Job = "worker"
	// JobExited is a tab whose program has exited but that Rex keeps open.
	JobExited Job = "exited"
)

// Record is the Scheduler's whole knowledge of one Task. Only the loop goroutine
// touches a Record. Effect lanes work on copies.
type Record struct {
	lifecycle.Task

	Title    string `json:"title"`
	Text     string `json:"text,omitempty"`
	Repo     string `json:"repo"`
	RepoSlug string `json:"repo_slug"`
	// Session is the Rex session: the Repo's label, or the ad hoc session.
	Session   string `json:"session"`
	RepoLabel string `json:"repo_label,omitempty"`
	// Branch is "" for a review Task: lineup makes no branch for it.
	Branch    string            `json:"branch"`
	Issue     *Issue            `json:"issue,omitempty"`
	Overrides map[string]string `json:"overrides,omitempty"`
	// Worktree is the Task's worktree. A build Task's Worker works in it. A
	// review Task's Worker reads the PR's code in it and works in the Repo.
	Worktree string `json:"worktree,omitempty"`
	// Review is the PR that a review Task reviews, fixed when it is added.
	// Only a review Task has one.
	Review *ReviewPR `json:"review,omitempty"`
	// Conversation is the ID of the review Worker's Claude conversation. Each
	// Reviewing Worker starts a new one, and a reopen resumes it.
	Conversation string `json:"conversation,omitempty"`

	SessionID   string          `json:"session_id,omitempty"`
	WindowID    string          `json:"window_id,omitempty"`
	BlockID     string          `json:"block_id,omitempty"`
	Job         Job             `json:"job,omitempty"`
	WorkerStage lifecycle.Stage `json:"worker_stage"`
	// Pending is the Worker to start once setup finishes.
	Pending *lifecycle.Stage `json:"pending,omitempty"`
	Status  string           `json:"status,omitempty"`
	// WorkerState is the Worker's last program status: working, blocked,
	// done, idle, or error. It is "" before the Worker's first status.
	WorkerState string `json:"worker_state,omitempty"`

	PR *PRSnapshot `json:"pr,omitempty"`
	// LatestHead is a review Task's PR head at the latest poll, and
	// ReviewedHead is the head that its findings file names.
	LatestHead   string `json:"latest_head,omitempty"`
	ReviewedHead string `json:"reviewed_head,omitempty"`
	// WorktreeKept is true when cleanup kept a review worktree that holds
	// work that is on no remote.
	WorktreeKept bool `json:"worktree_kept,omitempty"`
	// Archived hides a Done Task from the Board. Its history stays.
	Archived bool `json:"archived,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	StageSince time.Time `json:"stage_since"`
}

func (r *Record) tabLabel() string {
	if r.Issue != nil {
		return r.Issue.Identifier + " " + r.Title
	}
	return r.ID + " " + r.Title
}

// sessionLabel is the Repo's folder name, with its parent folder added when
// another Repo on the Board has the same folder name.
func sessionLabel(repo string, others []string) string {
	base := filepath.Base(repo)
	for _, o := range others {
		if o != repo && filepath.Base(o) == base {
			return filepath.Base(filepath.Dir(repo)) + "-" + base
		}
	}
	return base
}

// TaskView is a Task as the Board and the CLI show it.
type TaskView struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Repo       string    `json:"repo"`
	Stage      string    `json:"stage"`
	StageKey   string    `json:"stage_key"`
	Group      string    `json:"group"`
	Flag       string    `json:"flag,omitempty"`
	FlagDetail string    `json:"flag_detail,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Note       string    `json:"note,omitempty"`
	OverCap    bool      `json:"over_cap,omitempty"`
	Skill      string    `json:"skill,omitempty"`
	PRNumber   int       `json:"pr_number,omitempty"`
	PRURL      string    `json:"pr_url,omitempty"`
	ReviewHead string    `json:"review_head,omitempty"` // the head that a review Task was added at
	IssueURL   string    `json:"issue_url,omitempty"`
	Worktree   string    `json:"worktree,omitempty"`
	Since      time.Time `json:"since"`
	Live       bool      `json:"live"`
}

type BoardView struct {
	Cap     int `json:"cap"`
	Started int `json:"started"`
	OverCap int `json:"over_cap"`
	// ReviewsPaused is true while `lineup reviews pause` holds every queue fill.
	ReviewsPaused bool       `json:"reviews_paused,omitempty"`
	Tasks         []TaskView `json:"tasks"`
}

func (r *Record) view() TaskView {
	v := TaskView{
		ID: r.ID, Title: r.Title, Repo: r.RepoLabel,
		Stage: r.Stage.String(), StageKey: r.Stage.Key(), Group: r.Group().String(),
		Flag: string(r.Flag), FlagDetail: r.FlagDetail, Outcome: string(r.Outcome),
		OverCap: r.OverCap, Worktree: r.Worktree, Since: r.StageSince,
		Live: r.Job == JobWorker, Skill: r.Overrides["skill"],
	}
	if v.Repo == "" { // a Record from before RepoLabel, when the session was the Repo's label
		v.Repo = r.Session
	}
	if r.Issue != nil {
		v.IssueURL = r.Issue.URL
	}
	if r.PR != nil {
		v.PRNumber, v.PRURL = r.PR.Number, r.PR.URL
	}
	if r.Review != nil {
		v.PRNumber, v.PRURL, v.ReviewHead = r.Review.Number, r.Review.URL, r.Review.Head
	}
	b := r.Policy.Budgets
	switch r.Stage {
	case lifecycle.CIDebug:
		v.Note = fmt.Sprintf("attempt %d/%d", b.CIDebug-r.Left.CIDebug, b.CIDebug)
	case lifecycle.Responding:
		v.Note = fmt.Sprintf("round %d/%d", b.Responding-r.Left.Responding, b.Responding)
	case lifecycle.InReview, lifecycle.ReadyToLand, lifecycle.CIRunning, lifecycle.Done:
		if v.PRNumber != 0 {
			v.Note = fmt.Sprintf("#%d", v.PRNumber)
		}
	case lifecycle.ReviewReady:
		if r.ReviewedHead != "" && r.LatestHead != "" && r.LatestHead != r.ReviewedHead {
			v.Note = "head moved"
		}
	}
	if r.WorktreeKept {
		v.Note = "worktree kept: " + r.Worktree
	}
	if r.Flag != "" {
		v.Note = string(r.Flag)
		if r.FlagDetail != "" {
			v.Note += ": " + r.FlagDetail
		}
	}
	return v
}
