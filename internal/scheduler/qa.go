package scheduler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

// qaSession is the Rex session of every QA Task (PROD-QA-002).
const qaSession = "qa"

// qaPrefix starts the ID of each QA Task, before its build Task's ID.
const qaPrefix = "QA-"

// QA is the [qa] table of a Repo's layer.
type QA struct {
	// Enabled is true when a merge in the Repo adds a QA Task (PROD-QA-001).
	Enabled bool
	// MaxWait is the longest that a QA Task waits for a verdict (PROD-QA-008).
	MaxWait time.Duration
}

// startQA adds the QA Task of a build Task that merged, when its Repo has QA
// on. It reads the Repo's [qa] table now, so a setting applies to each merge
// after the restart that loads it (ARCH-QA-002). It reads files, so only
// lanes call it.
func (s *Scheduler) startQA(ctx context.Context, build Record) {
	qa, err := s.d.Config.QA(build.Repo)
	if err != nil {
		s.d.Log.Error("qa settings", "task", build.ID, "err", err)
		return
	}
	if !qa.Enabled || build.PR == nil {
		return
	}
	s.call(func() { s.addQA(build, qa) })
}

// addQA puts the QA Task of the build Task on the Board, unless a Task holds
// its ID already. The deadline counts from the merge. Only the loop calls it.
func (s *Scheduler) addQA(build Record, qa QA) {
	id := qaPrefix + build.ID
	if _, ok := s.recs[id]; ok {
		return
	}
	now := s.now()
	merged := build.PR.MergedAt
	if merged.IsZero() {
		merged = now
	}
	pr := *build.PR
	r := &Record{
		Title: build.Title, Repo: build.Repo, RepoSlug: build.RepoSlug, Session: qaSession, RepoLabel: build.RepoLabel,
		PR: &pr, MaxWait: qa.MaxWait, CreatedAt: now, StageSince: now,
	}
	if build.Issue != nil {
		is := *build.Issue
		r.Issue = &is
	} else {
		// An ad hoc build Task's request stands for its Issue.
		r.Text = build.Text
	}
	r.Task = *s.board.AddQA(id, build.Policy, now, merged.Add(qa.MaxWait))
	s.board.Tasks[id] = &r.Task
	s.recs[id] = r
	s.transition(r, store.Event{Signal: "add", Detail: "merge of " + build.ID}, nil)
	s.clockQA()
}

// clockQA is the QA clock (ARCH-QA-003). It sends qa-expired to each QA Task
// in QA waiting whose deadline has come. Then it sends qa-due to each one
// whose next check has come, the earliest first, until the QA slots are full.
// Only the loop calls it.
func (s *Scheduler) clockQA() {
	now := s.now()
	var due []*Record
	for _, r := range s.list() {
		switch {
		case r.Kind != lifecycle.KindQA || r.Stage != lifecycle.QAWaiting:
		case !now.Before(r.Deadline):
			s.apply(r, lifecycle.QAExpired{Detail: "no verdict after " + shortDuration(r.MaxWait)}, "")
		case !now.Before(r.NextCheck):
			due = append(due, r)
		}
	}
	slices.SortStableFunc(due, func(a, b *Record) int { return a.NextCheck.Compare(b.NextCheck) })
	for _, r := range due {
		if err := s.apply(r, lifecycle.QADue{}, ""); errors.Is(err, lifecycle.ErrQASlotsFull) {
			return
		}
	}
}

// shortDuration drops the zero tail of a duration, so 48h0m0s is 48h and
// 1h30m0s is 1h30m.
func shortDuration(d time.Duration) string {
	out := d.String()
	if strings.HasSuffix(out, "m0s") {
		out = out[:len(out)-2]
	}
	if strings.HasSuffix(out, "h0m") {
		out = out[:len(out)-2]
	}
	return out
}

// setupQA readies a QA Worker in the Task's lane: it adds the QA worktree at
// the merge commit if it is missing, and trusts the main checkout, which is
// the Worker's folder (ARCH-QA-005, ARCH-TREE-005). Each step converges, so a
// restart repeats them.
func (s *Scheduler) setupQA(ctx context.Context, r Record) bool {
	if r.PR == nil || r.PR.MergeCommit == "" {
		s.setupFailed(r.ID, "GitHub gave no merge commit for the PR")
		return false
	}
	l := s.repoLock(r.Repo)
	l.Lock()
	path, err := s.d.Worktrees.AddQA(ctx, r.Repo, r.PR.Number, r.PR.MergeCommit)
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

// removeQAWorktree removes the QA worktree, unless it holds an uncommitted
// change (PROD-QA-009).
func (s *Scheduler) removeQAWorktree(ctx context.Context, r Record) {
	if r.Worktree == "" {
		return
	}
	l := s.repoLock(r.Repo)
	l.Lock()
	kept, err := s.d.Worktrees.RemoveQA(ctx, r.Repo, r.Worktree)
	l.Unlock()
	if err != nil {
		s.d.Log.Error("remove QA worktree", "task", r.ID, "path", r.Worktree, "err", err)
	}
	s.update(r.ID, func(r *Record) {
		if r.WorktreeKept = kept; !kept {
			r.Worktree = ""
		}
	})
}

// qaNote says what a QA Task waits for, or how it ended: the next check and
// its reason in QA waiting, the findings in QA failed, and the summary or the
// fix Issue in Done (PROD-QA-006, PROD-QA-009, PROD-QA-010, PROD-QA-011).
func (r *Record) qaNote() string {
	var parts []string
	switch r.Stage {
	case lifecycle.QAWaiting:
		next := "next check " + r.NextCheck.Local().Format("Jan 2 15:04")
		if r.Verdict != "" {
			next += ": " + r.Verdict
		}
		parts = append(parts, next)
	case lifecycle.QAFailed:
		parts = append(parts, r.Verdict)
	case lifecycle.Done:
		word := string(r.Outcome)
		if r.Outcome == lifecycle.OutcomePassed || r.Outcome == lifecycle.OutcomeFixFiled {
			word = cmp.Or(r.Verdict, word)
		}
		parts = append(parts, word)
	}
	if w := r.worktreeNote(); w != "" {
		parts = append(parts, w)
	}
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " · ")
}

// ParseWhen reads the <when> of `lineup qa <task> wait`: a Go duration such
// as 2h, a clock time such as 03:00 at its next time in local time, or an
// RFC 3339 time. The time must come after now (PROD-QA-005).
func ParseWhen(when string, now time.Time) (time.Time, error) {
	when = strings.TrimSpace(when)
	var at time.Time
	if d, err := time.ParseDuration(when); err == nil {
		at = now.Add(d)
	} else if clock, err := time.ParseInLocation("15:04", when, time.Local); err == nil {
		today := now.In(time.Local)
		at = time.Date(today.Year(), today.Month(), today.Day(), clock.Hour(), clock.Minute(), 0, 0, time.Local)
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
	} else if at, err = time.Parse(time.RFC3339, when); err != nil {
		return time.Time{}, fmt.Errorf("%q is not a duration such as 2h, a clock time such as 03:00, or an RFC 3339 time", when)
	}
	if !at.After(now) {
		return time.Time{}, fmt.Errorf("%s is not in the future", when)
	}
	return at, nil
}

// verdict applies the QA Worker's `lineup qa` (ARCH-QA-004). Key is wait,
// pass, or fail, Value is the <when> of a wait, and Text is the reason, the
// summary, or the findings.
func (s *Scheduler) verdict(req Request) error {
	text := strings.TrimSpace(req.Text)
	var sig lifecycle.Signal
	var what string
	switch req.Key {
	case "wait":
		until, err := ParseWhen(req.Value, s.now())
		if err != nil {
			return err
		}
		sig, what = lifecycle.QAWait{Until: until, Reason: text}, "a reason"
	case "pass":
		sig, what = lifecycle.QAPass{Summary: text}, "a summary"
	case "fail":
		sig, what = lifecycle.QAFail{Findings: text}, "the findings"
	default:
		return fmt.Errorf("qa takes wait, pass, or fail, not %q", req.Key)
	}
	if text == "" {
		return fmt.Errorf("qa %s needs %s", req.Key, what)
	}
	return s.signal(req.Task, sig)
}

// recheck is the user's Check again: a check now, with a new deadline of the
// Repo's max_wait from now (PROD-QA-010).
func (s *Scheduler) recheck(id string) error {
	var repo string
	if err := s.with(id, func(r *Record) error { repo = r.Repo; return nil }); err != nil {
		return err
	}
	qa, err := s.d.Config.QA(repo)
	if err != nil {
		return err
	}
	return s.with(id, func(r *Record) error {
		if err := s.apply(r, lifecycle.QARecheck{Deadline: s.now().Add(qa.MaxWait)}, ""); err != nil {
			return err
		}
		r.MaxWait = qa.MaxWait
		s.save(r)
		return nil
	})
}

// fileFix is the user's Start a fix Task. It files the fix Issue in the
// Tracker of the build Task's Issue, ends the QA Task, and returns the new
// Issue's ID (PROD-QA-011). The Tracker call runs off the loop, and a second
// call during it files no second Issue.
func (s *Scheduler) fileFix(ctx context.Context, id string) (string, error) {
	var r Record
	err := s.with(id, func(p *Record) error {
		switch {
		case p.Stage != lifecycle.QAFailed:
			return &lifecycle.NotAllowedError{Signal: "qa-fix", Stage: p.Stage}
		case s.fixing[p.ID]:
			return fmt.Errorf("%s files its fix Issue now", p.ID)
		}
		s.fixing[p.ID] = true
		r = *p
		return nil
	})
	if err != nil {
		return "", err
	}
	defer s.call(func() { delete(s.fixing, r.ID) })
	key, _ := s.d.Config.RepoTracker(r.Repo)
	is, err := s.d.Tracker.CreateFix(ctx, r.Issue, key, fixTitle(r), fixBody(r))
	if err != nil {
		return "", fmt.Errorf("file the fix Issue: %w", err)
	}
	return is.Identifier, s.signal(r.ID, lifecycle.QAFixFiled{Issue: is.Identifier})
}

func fixTitle(r Record) string {
	return "Fix QA of " + strings.TrimPrefix(r.ID, qaPrefix) + ": " + r.Title
}

// fixBody holds the findings and the links to the Issue, the PR, and the
// merge commit.
func fixBody(r Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The QA check of %s failed after the merge.\n\n%s\n\n", strings.TrimPrefix(r.ID, qaPrefix), r.Verdict)
	if r.Issue != nil {
		fmt.Fprintf(&b, "- Issue: [%s](%s)\n", r.Issue.Identifier, r.Issue.URL)
	}
	if r.PR != nil {
		fmt.Fprintf(&b, "- Pull request: [#%d](%s)\n", r.PR.Number, r.PR.URL)
		if c := r.PR.MergeCommit; c != "" {
			fmt.Fprintf(&b, "- Merge commit: [%s](https://github.com/%s/commit/%s)\n", c[:min(7, len(c))], r.RepoSlug, c)
		}
	}
	return b.String()
}
