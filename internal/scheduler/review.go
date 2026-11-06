package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// reviewSession is the Rex session of every review Task.
const reviewSession = "reviews"

// ReviewPR is the PR that a review Task reviews. It never changes after the
// add, so a lane's copy of the Record can share it.
type ReviewPR struct {
	Repo   string `json:"repo"` // owner/name as GitHub spells it
	Number int    `json:"number"`
	URL    string `json:"url"`
	// Head is the PR's head commit when the Task was added.
	Head string `json:"head"`
	Base string `json:"base"`
}

func (p ReviewPR) key() ReviewKey { return ReviewKey{p.Repo, p.Number} }

// is reports whether p is the PR that k names. GitHub names ignore case.
func (p ReviewPR) is(k ReviewKey) bool {
	return p.Number == k.Number && strings.EqualFold(p.Repo, k.Repo)
}

var (
	prURLPattern = regexp.MustCompile(`^(?:https?://)?github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([1-9][0-9]*)(?:[/?#].*)?$`)
	prIDPattern  = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)
)

// ParseReviewKey reads a PR URL, such as https://github.com/owner/repo/pull/12/files,
// or the short form owner/repo#12.
func ParseReviewKey(s string) (ReviewKey, error) {
	s = strings.TrimSpace(s)
	m := prURLPattern.FindStringSubmatch(s)
	if m == nil {
		m = prIDPattern.FindStringSubmatch(s)
	}
	if m == nil {
		return ReviewKey{}, fmt.Errorf("%q is not a GitHub PR URL such as https://github.com/owner/repo/pull/12", s)
	}
	n, _ := strconv.Atoi(m[2])
	return ReviewKey{Repo: m[1], Number: n}, nil
}

// Level is the review Signal that the PR's current state holds for a review
// Task added at since, or nil. A review that the user submitted comes first,
// because it is the user's own end of the review.
func (p ReviewSnapshot) Level(since time.Time) lifecycle.Signal {
	switch {
	case p.ReviewedAt.After(since):
		return lifecycle.PRReviewed{}
	case p.State == "MERGED":
		return lifecycle.PRMerged{}
	case p.State == "CLOSED":
		return lifecycle.PRClosed{}
	}
	return nil
}

// workerWorking reports whether the Worker can still act on the PR. A
// blocked Worker counts, because pr-review asks for a permission after it
// posts the review.
func (r *Record) workerWorking() bool {
	return r.Job == JobWorker && r.WorkerState != "done" && r.WorkerState != "idle"
}

// asksForDecisions reports whether a Reviewing Worker asks the user a
// question. pr-review asks for the decision on each finding that way after it
// saves the findings, so the question meets the contract if the file exists.
func (r *Record) asksForDecisions(st Status) bool {
	return r.Review != nil && r.Stage == lifecycle.Reviewing && st.State == "blocked" && st.Kind == "question"
}

// ReviewAction says what the review op did.
type ReviewAction string

const (
	ReviewAdded     ReviewAction = "added"     // a new review Task, started
	ReviewStarted   ReviewAction = "started"   // the open Task was Queued, now started
	ReviewRestarted ReviewAction = "restarted" // the open Task had stopped, now running again
	ReviewReopened  ReviewAction = "reopened"  // the open Task's Worker had closed, now resumed
	ReviewOpen      ReviewAction = "open"      // the open Task runs, nothing changed
)

// ReviewResult is a review Task that `lineup review` added or found.
type ReviewResult struct {
	TaskView
	Action ReviewAction `json:"action"`
}

// review adds and starts a review Task with no queue for the PR at url. If an
// open review Task has the PR, it adds nothing. It starts that Task if it is
// Queued, reopens its Worker if it closed in Review ready, and runs it again
// if it stopped for good.
func (s *Scheduler) review(ctx context.Context, url string) (ReviewResult, error) {
	key, err := ParseReviewKey(url)
	if err != nil {
		return ReviewResult{}, err
	}
	var res ReviewResult
	var found bool
	if err := s.withLoop(func() (err error) { res, found, err = s.startOpenReview(key); return }); found || err != nil {
		return res, err
	}
	prs, err := s.d.GitHub.ReviewPRs(ctx, []ReviewKey{key})
	if err != nil {
		return ReviewResult{}, err
	}
	pr, ok := prs[key]
	switch {
	case !ok:
		return ReviewResult{}, fmt.Errorf("GitHub has no PR %s#%d", key.Repo, key.Number)
	case pr.Fork:
		return ReviewResult{}, fmt.Errorf("%s#%d comes from a fork: pr-review would run the fork's code with your credentials", pr.Repo, pr.Number)
	case pr.State != "OPEN":
		return ReviewResult{}, fmt.Errorf("%s#%d is %s", pr.Repo, pr.Number, strings.ToLower(pr.State))
	}
	repo, policy, err := s.reviewRepo(pr.Repo)
	if err != nil {
		return ReviewResult{}, err
	}
	err = s.withLoop(func() error {
		// A second `lineup review` can add the PR while this one reads GitHub.
		var err error
		if res, found, err = s.startOpenReview(key); found || err != nil {
			return err
		}
		r, err := s.addReview(reviewAdd{
			PR:    ReviewPR{Repo: pr.Repo, Number: pr.Number, URL: pr.URL, Head: pr.Head, Base: pr.Base},
			Title: pr.Title, Repo: repo, Policy: policy,
		})
		res = ReviewResult{TaskView: r.view(), Action: ReviewAdded}
		return err
	})
	return res, err
}

// reviewRepo finds the Repo whose main checkout reviews the PRs of the GitHub
// repository owner/name, and that Repo's policy. It reads files, so only
// lanes and API handlers call it, never the loop.
func (s *Scheduler) reviewRepo(slug string) (string, lifecycle.Policy, error) {
	repo, ok := s.d.Config.RepoForGitHub(slug)
	if !ok {
		return "", lifecycle.Policy{}, fmt.Errorf("%w for %s: no [[repos]] checkout has it as its origin", ErrNoRepo, slug)
	}
	policy, err := s.d.Config.Policy(repo, nil)
	return repo, policy, err
}

// reviewAdd is a review Task to put on the Board. Queue is "" for a review
// that the user asked for.
type reviewAdd struct {
	PR     ReviewPR
	Title  string // the PR's title
	Repo   string
	Policy lifecycle.Policy
	Queue  string
}

// addReview puts a review Task on the Board and starts it. Only the loop
// calls it.
func (s *Scheduler) addReview(a reviewAdd) (*Record, error) {
	_, name, _ := strings.Cut(a.PR.Repo, "/")
	r := &Record{
		Title: fmt.Sprintf("%s#%d %s", name, a.PR.Number, a.Title), Repo: a.Repo, RepoSlug: a.PR.Repo, Session: reviewSession,
		Review: &a.PR, CreatedAt: s.now(), StageSince: s.now(),
	}
	r.ID = s.nextID("R")
	r.RepoLabel = sessionLabel(r.Repo, s.repos())
	r.Task = *s.board.AddReview(r.ID, a.Queue, a.Policy)
	s.board.Tasks[r.ID] = &r.Task
	s.recs[r.ID] = r
	detail := a.PR.URL
	if a.Queue != "" {
		detail = "queue " + a.Queue + ": " + detail
	}
	s.save(r, store.Event{TaskID: r.ID, At: s.now(), Signal: "add", To: lifecycle.Queued.String(), Detail: detail})
	return r, s.apply(r, lifecycle.Start{}, "")
}

// reviewTask is the Record as the queue rules see it. ok is false for a
// build Task.
func (r *Record) reviewTask() (t github.ReviewTask, ok bool) {
	if r.Review == nil {
		return t, false
	}
	return github.ReviewTask{URL: r.Review.URL, Head: r.Review.Head, Done: r.Stage == lifecycle.Done}, true
}

// startOpenReview finds the open review Task of the PR. It starts the Task if
// it is Queued, and reopens its Worker if the Worker closed in Review ready. It
// runs the Task again if no Worker will, because its flag never clears by
// itself. Only the loop calls it.
func (s *Scheduler) startOpenReview(key ReviewKey) (ReviewResult, bool, error) {
	for _, r := range s.list() {
		if r.Review == nil || r.Stage == lifecycle.Done || !r.Review.is(key) {
			continue
		}
		var err error
		action := ReviewOpen
		switch {
		case r.Stage == lifecycle.Queued:
			action, err = ReviewStarted, s.apply(r, lifecycle.Start{}, "")
		case r.Stage.Resumable() && r.Job != JobWorker && r.Conversation == "":
			// A Task from before conversation IDs has no conversation to resume.
			action, err = ReviewRestarted, s.apply(r, lifecycle.Rerun{Stage: lifecycle.Reviewing}, "")
		case r.Stage.Resumable() && r.Job != JobWorker:
			action, err = ReviewReopened, s.apply(r, lifecycle.Reopen{}, "")
		case r.Flag != "" && !r.Flag.Transient():
			action, err = ReviewRestarted, s.apply(r, lifecycle.Rerun{Stage: lifecycle.Reviewing}, "")
		}
		return ReviewResult{TaskView: r.view(), Action: action}, true, err
	}
	return ReviewResult{}, false, nil
}

// repoLock serializes the git steps of review Tasks in one Repo, because each
// fetches into the Repo's main checkout. Only lanes take it, never the loop.
func (s *Scheduler) repoLock(repo string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	l, ok := s.repoLocks[repo]
	if !ok {
		l = &sync.Mutex{}
		s.repoLocks[repo] = l
	}
	return l
}

// setupReview readies a review Worker in the Task's lane: it fetches the PR,
// adds the review worktree if it is missing, and trusts the main checkout,
// which is the Worker's folder. Each step converges, so a restart repeats them.
func (s *Scheduler) setupReview(ctx context.Context, r Record) bool {
	l := s.repoLock(r.Repo)
	l.Lock()
	path, err := s.d.Worktrees.AddReview(ctx, r.Repo, *r.Review)
	l.Unlock()
	if err == nil {
		err = s.d.Harness.Prepare(ctx, r.Repo, r.Repo)
	}
	if err != nil {
		s.setupFailed(r.ID, err.Error())
		return false
	}
	s.update(r.ID, func(r *Record) { r.Worktree = path })
	return true
}

// hunkArgv shows the PR's diff in hunk. hunk reads a token from GH_TOKEN for
// a private repository. The shell sets it from gh, so the token is in no argv.
func hunkArgv(url string) []string {
	return []string{"sh", "-c", `GH_TOKEN=$(gh auth token) exec hunk gh pr "$1"`, "hunk", url}
}

// removeReviewWorktree runs at Done, after the tab closed. It never runs
// worktrunk: the review worktree is not worktrunk's.
func (s *Scheduler) removeReviewWorktree(ctx context.Context, r Record) {
	if r.Worktree == "" {
		return
	}
	l := s.repoLock(r.Repo)
	l.Lock()
	kept, err := s.d.Worktrees.RemoveReview(ctx, r.Repo, r.Worktree, *r.Review)
	l.Unlock()
	if err != nil {
		s.d.Log.Error("remove review worktree", "task", r.ID, "path", r.Worktree, "err", err)
	}
	s.update(r.ID, func(r *Record) {
		if r.WorktreeKept = kept; !kept {
			r.Worktree = ""
		}
	})
}

// pollReviews reads each review Task's PR by number and applies its level
// while the Worker does not work. pr-review posts the review in its Step 9,
// and a Done before that step ends would close the tab under it.
func (s *Scheduler) pollReviews(ctx context.Context, keys map[ReviewKey]string) {
	if len(keys) == 0 {
		return
	}
	prs, err := s.d.GitHub.ReviewPRs(ctx, keysOf(keys))
	if err != nil {
		s.d.Log.Error("github review poll", "err", err)
		return
	}
	s.call(func() {
		for key, id := range keys {
			pr, ok := prs[key]
			r := s.recs[id]
			if !ok || r == nil || !r.Stage.Started() {
				continue
			}
			if r.LatestHead != pr.Head {
				r.LatestHead = pr.Head
				s.save(r)
			}
			if r.workerWorking() {
				continue
			}
			if sig := pr.Level(r.CreatedAt); sig != nil {
				s.apply(r, sig, "")
			}
		}
	})
}

func keysOf[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// findings looks in the PR's pr-review record folder for the review contract's
// output: the findings file of the pinned head, which a reused review leaves,
// or any findings file written since the review started. It returns the head
// that the file names.
func (s *Scheduler) findings(pr ReviewPR, since time.Time) (head string, ok bool) {
	owner, name, _ := strings.Cut(pr.Repo, "/")
	dir := filepath.Join(s.d.StateHome, "pr-review", fmt.Sprintf("%s-%s-%d", owner, name, pr.Number))
	if _, err := os.Stat(filepath.Join(dir, "findings-"+pr.Head+".json")); err == nil {
		return pr.Head, true
	}
	files, _ := filepath.Glob(filepath.Join(dir, "findings-*.json"))
	var newest time.Time
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || info.ModTime().Before(since) || info.ModTime().Before(newest) {
			continue
		}
		newest, head = info.ModTime(), strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "findings-"), ".json")
	}
	return head, head != ""
}
