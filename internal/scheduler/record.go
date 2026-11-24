package scheduler

import (
	"cmp"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gclunies/lineup/internal/github"
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
	// Session is the label of the Rex session: the Repo key for a build Task,
	// reviewSession for a review Task.
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
	// Conversation is the ID of the Task's Claude conversation. The Task's
	// first Worker starts it, and every later Worker resumes it.
	Conversation string `json:"conversation,omitempty"`

	SessionID string `json:"session_id,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	Job       Job    `json:"job,omitempty"`
	// SideBlockID is the side pane beside the Task's Worker. No event of it
	// reaches the Task. The JSON key keeps its old name, because a stored
	// record holds it.
	SideBlockID string `json:"hunk_block_id,omitempty"`
	// Side is what the side pane runs. A Record from before it has none.
	Side *sidePane `json:"side,omitempty"`
	// WorkerStage is the Stage of the prompt that the Task's Worker got last.
	// It is nil until a Worker opens.
	WorkerStage *lifecycle.Stage `json:"worker_stage,omitempty"`
	// Pending is the Stage whose prompt the Worker has not received yet. It is
	// nil or the Task's Stage.
	Pending      *lifecycle.Stage `json:"pending,omitempty"`
	PendingSince time.Time        `json:"pending_since,omitzero"`
	Status       string           `json:"status,omitempty"`
	// WorkerState is the Worker's last program status: working, blocked,
	// done, idle, or error. It is "" before the Worker's first status, and
	// "prompted" from a typed prompt until the Worker's next status.
	WorkerState string `json:"worker_state,omitempty"`

	PR *PRSnapshot `json:"pr,omitempty"`
	// LatestHead is a review Task's PR head at the latest poll, and
	// ReviewedHead is the head that its findings file names.
	LatestHead   string `json:"latest_head,omitempty"`
	ReviewedHead string `json:"reviewed_head,omitempty"`
	// WorktreeKept is true when lineup kept a worktree that it meant to
	// remove: a review worktree that holds work that is on no remote, or a
	// build worktree with uncommitted changes or a failed removal.
	WorktreeKept bool `json:"worktree_kept,omitempty"`
	// WorktreeError is why a build Task's worktree stays, when the reason is
	// not uncommitted changes, for example an error of wt remove.
	WorktreeError string `json:"worktree_error,omitempty"`
	// CloseAtIdle is true from a build Task's merge, with close_tab on for
	// the Task, until lineup closes its tab.
	CloseAtIdle bool `json:"close_at_idle,omitempty"`
	// KeepUntil is when lineup removes the worktree of a Done build Task that
	// has no tab. Zero means no removal is due.
	KeepUntil time.Time `json:"keep_until,omitzero"`
	// Archived hides a Done Task from the Board. Its history stays.
	Archived bool `json:"archived,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	StageSince time.Time `json:"stage_since"`
}

// tabLabelWidth is the most characters that a tab label holds, "…" included.
const tabLabelWidth = 32

var (
	repoPrefix = regexp.MustCompile(`^\S+#\d+ `)
	issueKey   = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+:\s*`)
	commitType = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!?:\s*`)
)

// tabLabel names the Task's tab and its Worker's conversation. It is the
// Task's ID and its title, cut at a word to tabLabelWidth characters with a
// closing "…". A GitHub ID loses its owner, because the Repo session names it
// (PROD-TABS-003). A review's title loses its repo#n prefix, its issue key,
// and its commit type, because the ID already names the PR.
func (r *Record) tabLabel() string {
	id := r.ID
	if KindOf(id, nil) == KindGitHub {
		_, id, _ = strings.Cut(id, "/")
	}
	title := r.Title
	if r.Review != nil {
		title = repoPrefix.ReplaceAllString(title, "")
		title = issueKey.ReplaceAllString(title, "")
		title = commitType.ReplaceAllString(title, "")
	}
	label := strings.TrimSpace(id + " " + title)
	rs := []rune(label)
	if len(rs) <= tabLabelWidth {
		return label
	}
	head := rs[:tabLabelWidth-1]
	if !unicode.IsSpace(rs[tabLabelWidth-1]) {
		for i := len(head) - 1; i > len([]rune(id)); i-- {
			if unicode.IsSpace(head[i]) {
				head = head[:i]
				break
			}
		}
	}
	return strings.TrimRightFunc(string(head), func(c rune) bool { return unicode.IsSpace(c) || unicode.IsPunct(c) }) + "…"
}

// titleTail is what a title loses from its end.
const titleTail = " :,;-–—("

// PlainTitle is s as one line with no URL. It is the first line of s that
// keeps a word when its URLs and a trailing titleTail go, with its words
// joined by one space. A title that is only URLs keeps its first line.
func PlainTitle(s string) string {
	first := ""
	for _, line := range strings.Split(s, "\n") {
		if first == "" {
			first = strings.TrimSpace(line)
		}
		words := strings.Fields(line)
		var kept []string
		for i, w := range words {
			if !urlWord(w, i == len(words)-1) {
				kept = append(kept, w)
			}
		}
		if t := strings.TrimRight(strings.Join(kept, " "), titleTail); t != "" {
			return t
		}
	}
	return first
}

// urlWord reports whether the word w is a URL. A cut can leave the last word
// of a line as the start of "https://" or "http://", as in "https:/".
func urlWord(w string, last bool) bool {
	if strings.Contains(w, "://") || strings.HasPrefix(w, "www.") {
		return true
	}
	return last && len(w) >= 4 && (strings.HasPrefix("https://", w) || strings.HasPrefix("http://", w))
}

// textTitleWidth is the most runes that the title of a Task with no Issue holds.
const textTitleWidth = 60

// textTitle is the title of a Task with no Issue: the PlainTitle of its text,
// cut at a word to textTitleWidth runes. A first word that is longer is cut
// inside the word.
func textTitle(text string) string {
	t := []rune(PlainTitle(text))
	if len(t) <= textTitleWidth {
		return string(t)
	}
	head := string(t[:textTitleWidth])
	if i := strings.LastIndexFunc(head, unicode.IsSpace); i > 0 && !unicode.IsSpace(t[textTitleWidth]) {
		head = head[:i]
	}
	return strings.TrimRight(head, titleTail)
}

// HubSession is the Rex session of the Hub. No Repo session takes this name,
// because a Repo key always holds a "/".
const HubSession = "my-lineup"

// repoLabel is the Board's name for the Repo: its folder name, with its parent
// folder added when another Repo on the Board has the same folder name.
func repoLabel(repo string, others []string) string {
	base := filepath.Base(repo)
	if slices.ContainsFunc(others, func(o string) bool { return o != repo && filepath.Base(o) == base }) {
		return filepath.Base(filepath.Dir(repo)) + "-" + base
	}
	return base
}

// TaskView is a Task as the Board and the CLI show it.
type TaskView struct {
	ID         string         `json:"id"`
	Kind       lifecycle.Kind `json:"kind"`
	Title      string         `json:"title"`
	Repo       string         `json:"repo"`
	Stage      string         `json:"stage"`
	StageKey   string         `json:"stage_key"`
	Group      string         `json:"group"`
	Flag       string         `json:"flag,omitempty"`
	FlagDetail string         `json:"flag_detail,omitempty"`
	Outcome    string         `json:"outcome,omitempty"`
	Note       string         `json:"note,omitempty"`
	Optional   []string       `json:"optional,omitempty"` // the optional checks that failed on a build Task's PR
	OverCap    bool           `json:"over_cap,omitempty"`
	Skill      string         `json:"skill,omitempty"`
	PRNumber   int            `json:"pr_number,omitempty"`
	PRURL      string         `json:"pr_url,omitempty"`
	ReviewHead string         `json:"review_head,omitempty"` // the head that a review Task was added at
	IssueURL   string         `json:"issue_url,omitempty"`
	Worktree   string         `json:"worktree,omitempty"`
	Since      time.Time      `json:"since"`
	Live       bool           `json:"live"`
	Tab        bool           `json:"tab,omitempty"`
	// WorktreeNote says why lineup keeps the Task's worktree. The note ends
	// with it too, and a Follow-up row, whose note is the Tracker state, adds it.
	WorktreeNote string `json:"worktree_note,omitempty"`
	// Usage is nil when it is unknown. Only show, and ls with Usage, read it,
	// so the Board's watch stream never holds it.
	Usage *Usage `json:"usage"`
}

// Usage is what a Task's Worker spent: the tokens of each API message once,
// its subagents' included, and its agent time. Agent time leaves out the
// time that the Worker waited for the user.
type Usage struct {
	Input        int64 `json:"input_tokens"`
	Output       int64 `json:"output_tokens"`
	CacheWrite   int64 `json:"cache_write_tokens"`
	CacheRead    int64 `json:"cache_read_tokens"`
	AgentSeconds int64 `json:"agent_seconds"`
}

// Lines are the usage lines of the detail pane and of lineup show, for
// example "tokens  output 83.6k · input 268 · cache write 634k · cache read
// 22.4M" and "agent time  5h 12m". A nil Usage is unknown.
func (u *Usage) Lines() []string {
	if u == nil {
		return []string{"usage  unknown"}
	}
	return []string{
		fmt.Sprintf("tokens  output %s · input %s · cache write %s · cache read %s",
			tokenCount(u.Output), tokenCount(u.Input), tokenCount(u.CacheWrite), tokenCount(u.CacheRead)),
		"agent time  " + agentTime(u.AgentSeconds),
	}
}

func tokenCount(n int64) string {
	switch {
	case n < 1_000:
		return strconv.FormatInt(n, 10)
	case n < 9_995:
		return fmt.Sprintf("%.2fk", float64(n)/1e3)
	case n < 99_950:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	case n < 999_500:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n < 9_995_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n < 99_950_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	return fmt.Sprintf("%.0fM", float64(n)/1e6)
}

func agentTime(s int64) string {
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%ds", s)
}

type BoardView struct {
	Cap     int `json:"cap"`
	Started int `json:"started"`
	OverCap int `json:"over_cap"`
	// ReviewsPaused is true while `lineup reviews pause` holds every queue fill.
	ReviewsPaused bool `json:"reviews_paused,omitempty"`
	// Skips are the PRs that the user holds out of every review queue.
	Skips []github.Skip `json:"skips,omitempty"`
	Tasks []TaskView    `json:"tasks"`
	// Done is the time that each Task reached Done in the last 48 hours,
	// archived Tasks included, so that the header counts the Tasks done since
	// yesterday.
	Done []time.Time `json:"done,omitempty"`
}

// OfKind is the view with only the Tasks of Kind k. Each Hub tab shows one Kind.
func (v BoardView) OfKind(k lifecycle.Kind) BoardView {
	out := v
	out.Tasks = nil
	for _, t := range v.Tasks {
		if t.Kind == k {
			out.Tasks = append(out.Tasks, t)
		}
	}
	return out
}

// hadWorker reports whether a Worker ever opened in a tab for the Task. A Task
// that had one and has no tab now waits for the user, so no prompt opens a tab.
func (r *Record) hadWorker() bool { return r.WorkerStage != nil }

// keptSetupTab reports whether the Task's tab is its setup tab, which Rex keeps
// after worktrunk exits.
func (r *Record) keptSetupTab() bool { return r.Job == JobExited && !r.hadWorker() && r.BlockID != "" }

func (r *Record) keepsTab() bool { return r.Stage == lifecycle.Done && r.WindowID != "" }

// keepsWork reports whether a Done Task keeps its tab, or a Done build Task
// its worktree, which the user's close removes.
func (r *Record) keepsWork() bool {
	return r.Stage == lifecycle.Done && (r.WindowID != "" || r.Review == nil && r.Worktree != "")
}

// doneState is where a Done build Task stands with its tab and its worktree.
// Each state derives from the Record's fields.
type doneState int

const (
	// doneOther is any other Task: one that is not Done, a review Task, or a
	// Done build Task that lineup has nothing to do for.
	doneOther doneState = iota
	// closing waits for a Worker that is not busy, then lineup closes the tab.
	closing
	// kept has no tab, and lineup removes the worktree at KeepUntil.
	kept
	// readOnly has no tab and no worktree. open resumes its conversation in
	// the Repo's main checkout, with no tool that writes files.
	readOnly
)

func (r *Record) done() doneState {
	switch {
	case r.Stage != lifecycle.Done || r.Review != nil:
		return doneOther
	case r.WindowID != "" && r.CloseAtIdle:
		return closing
	case r.WindowID == "" && r.Worktree != "" && !r.KeepUntil.IsZero():
		return kept
	case r.WindowID == "" && r.Worktree == "" && r.Conversation != "":
		return readOnly
	}
	return doneOther
}

func (r *Record) usageSpec() WorkerSpec {
	return WorkerSpec{Task: r.ID, Repo: r.Repo, Conversation: r.Conversation}
}

func (r *Record) view() TaskView {
	v := TaskView{
		ID: r.ID, Kind: r.Kind, Title: r.Title, Repo: r.RepoLabel,
		Stage: r.Stage.String(), StageKey: r.Stage.Key(), Group: r.Group().String(),
		Flag: string(r.Flag), FlagDetail: r.FlagDetail, Outcome: string(r.Outcome),
		OverCap: r.OverCap, Worktree: r.Worktree, Since: r.StageSince,
		Live: r.Job == JobWorker, Tab: r.WindowID != "", Skill: r.Overrides["skill"],
		WorktreeNote: r.worktreeNote(),
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
	if r.Stage.Started() {
		v.Optional = r.Standing.Optional
	}
	v.Note = r.note(v.PRNumber)
	if r.Flag != "" {
		v.Note = string(r.Flag)
		if r.FlagDetail != "" {
			v.Note += ": " + r.FlagDetail
		}
	}
	return v
}

// note says what the Task does now. A build Task with a PR also says where the
// PR's review stands, for example "#42 ✓ approved · ready to land". A review
// Task says what it does, or how it ended, after the PR number. A flag
// replaces the note.
func (r *Record) note(pr int) string {
	var lead []string
	if pr != 0 {
		lead = append(lead, fmt.Sprintf("#%d", pr))
	}
	withReview := func() {
		if pr != 0 {
			lead = append(lead, r.Standing.ReviewState())
		}
	}
	var tail []string
	optional := func() {
		if o := r.Standing.Optional; len(o) > 0 {
			tail = append(tail, "optional failed: "+strings.Join(o, ", "))
		}
	}
	switch r.Stage {
	case lifecycle.Working:
		withReview()
	case lifecycle.CIDebug:
		withReview()
		tail = append(tail, "fixing CI")
	case lifecycle.Responding:
		withReview()
		tail = append(tail, "answering review")
	case lifecycle.CIRunning, lifecycle.InReview:
		withReview()
		if why := r.Policy.Bar.Blocker(r.Standing); why != "" {
			tail = append(tail, why)
		}
		optional()
	case lifecycle.ReadyToLand:
		withReview()
		tail = append(tail, "ready to land")
		optional()
	case lifecycle.Reviewing:
		lead = append(lead, "reviewing")
	case lifecycle.ReviewReady:
		lead = append(lead, "review ready")
		if r.ReviewedHead != "" && r.LatestHead != "" && r.LatestHead != r.ReviewedHead {
			tail = append(tail, "head moved")
		}
	case lifecycle.Done:
		switch r.Outcome {
		case lifecycle.OutcomeNone:
		case lifecycle.OutcomeRemoved:
			lead = append(lead, string(r.Outcome)+": never started")
		default:
			lead = append(lead, string(r.Outcome))
		}
	}
	if w := r.worktreeNote(); w != "" {
		tail = append(tail, w)
	}
	parts := tail
	if len(lead) > 0 {
		parts = append([]string{strings.Join(lead, " ")}, tail...)
	}
	return strings.Join(parts, " · ")
}

// worktreeNote says why lineup keeps the Task's worktree, or "" when it keeps none.
func (r *Record) worktreeNote() string {
	switch {
	case r.WorktreeKept && r.Review == nil:
		return "worktree kept: " + cmp.Or(r.WorktreeError, "uncommitted changes in "+r.Worktree)
	case r.WorktreeKept:
		return "worktree kept: " + r.Worktree
	case r.done() == kept:
		return "worktree until " + r.KeepUntil.Format("Jan 2")
	}
	return ""
}

// checklist is the Landing bar's checklist for a started Task whose PR a
// poll has measured, one line for each item, then the optional checks that
// failed.
func (r *Record) checklist() []string {
	if !r.Stage.Started() || r.Standing.Head == "" {
		return nil
	}
	var out []string
	for _, i := range r.Policy.Bar.Checklist(r.Standing) {
		mark := "✓"
		if i.Gap != lifecycle.NoGap {
			mark = "✗"
		}
		out = append(out, mark+" "+i.Text)
	}
	if o := r.Standing.Optional; len(o) > 0 {
		out = append(out, "! optional checks failed: "+strings.Join(o, ", "))
	}
	return out
}
