package scheduler

import (
	"context"
	"uuid"

	"github.com/gclunies/lineup/internal/lifecycle"
)

type worker int

const (
	noWorktree worker = iota
	settingUp
	noWorker
	lostWorker
	busyWorker
	readyWorker
)

type delivery int

const (
	wait delivery = iota
	setUp
	open
	flagTab
	typePrompt
)

var deliveries = [...]delivery{
	noWorktree:  setUp,
	settingUp:   wait,
	noWorker:    open,
	lostWorker:  flagTab,
	busyWorker:  wait,
	readyWorker: typePrompt,
}

func (r *Record) worker() worker {
	switch {
	case r.Job == JobWorker && (r.WorkerState == "idle" || r.WorkerState == "done"):
		return readyWorker
	case r.Job == JobWorker:
		return busyWorker
	case r.Job == JobSetup:
		return settingUp
	case r.Review == nil && r.Worktree == "":
		return noWorktree
	case r.Conversation == "":
		return noWorker
	}
	return lostWorker
}

func (s *Scheduler) deliver(ctx context.Context, id string) {
	r, ok := s.snapshot(id)
	if !ok || r.Pending == nil {
		return
	}
	stage := *r.Pending
	if stage != r.Stage {
		s.update(id, func(r *Record) {
			if r.Pending != nil && *r.Pending != r.Stage {
				r.Pending = nil
			}
		})
		return
	}
	switch deliveries[r.worker()] {
	case setUp:
		s.setup(ctx, r)
	case open:
		s.openWorker(ctx, r, stage, true)
	case flagTab:
		s.call(func() {
			if rec, ok := s.recs[r.ID]; ok && rec.Pending != nil && *rec.Pending == stage {
				s.apply(rec, lifecycle.TabClosed{Waits: true}, "")
			}
		})
	case typePrompt:
		s.typePrompt(ctx, r, stage)
	}
}

func (s *Scheduler) spec(r Record, stage lifecycle.Stage) WorkerSpec {
	replaced, steers := s.replay(r.ID)
	spec := WorkerSpec{
		Restart: replaced, Steers: steers,
		Task: r.ID, Stage: stage, Repo: r.Repo, Worktree: r.Worktree, Branch: r.Branch,
		Issue: r.Issue, Text: r.Text, PRURL: prURL(r.PR), Overrides: r.Overrides,
		Conversation: r.Conversation, Name: r.tabLabel(), ReviewCycle: r.ReviewCycle(),
		Bar: r.Policy.Bar, Standing: r.Standing,
	}
	if r.Review != nil {
		spec.PRURL = r.Review.URL
	}
	return spec
}

// openWorker opens a tab for the Task's Worker and reports whether it did.
func (s *Scheduler) openWorker(ctx context.Context, r Record, stage lifecycle.Stage, fresh bool) bool {
	if r.Review != nil {
		if !s.setupReview(ctx, r) {
			return false
		}
		var ok bool
		if r, ok = s.snapshot(r.ID); !ok || r.Stage != stage {
			return false
		}
	}
	spec := s.spec(r, stage)
	var prompt string
	if fresh {
		spec.Conversation = uuid.NewV4().String()
		var err error
		if prompt, err = s.d.Harness.Prompt(ctx, spec); err != nil {
			s.fail(r.ID, "worker prompt", err)
			return false
		}
	}
	folder := r.Worktree
	if r.Review != nil {
		// The PR's code never becomes the Worker's folder: its .claude
		// settings and hooks would run.
		folder = r.Repo
	}
	argv, env, err := s.d.Harness.Command(ctx, spec, prompt)
	if err != nil {
		s.fail(r.ID, "worker command", err)
		return false
	}
	if r.Review != nil {
		if env == nil {
			env = map[string]string{}
		}
		env["XDG_STATE_HOME"] = s.d.StateHome
	}
	session, err := s.d.Rex.EnsureSession(ctx, r.Session)
	if err != nil {
		s.fail(r.ID, "rex session", err)
		return false
	}
	w, err := s.d.Rex.NewWindow(ctx, session, r.tabLabel(), folder, env, argv)
	if err != nil {
		s.fail(r.ID, "rex tab", err)
		return false
	}
	since := r.PendingSince
	s.update(r.ID, func(rec *Record) {
		rec.Conversation, rec.WorkerStage = spec.Conversation, stage
		// A rerun during a slow open set the prompt again, and the new Worker has
		// not got it.
		if fresh && rec.Pending != nil && *rec.Pending == stage && rec.PendingSince.Equal(since) {
			rec.Pending = nil
		}
	})
	s.attach(r.ID, w, JobWorker)
	if r.Review != nil {
		s.openHunk(ctx, r.ID, w.SessionID, w.BlockID, r.Worktree, r.Review.URL)
	}
	return true
}

// promptedState is the WorkerState from a typed prompt until the Worker reports
// working, blocked, or error. A done or idle in it ended the turn before the
// prompt, so onRex drops it. It is not "working", so the first working report
// clears a flag.
const promptedState = "prompted"

// awaitsStatus reports whether a prompt waits for the Worker to report.
func (r *Record) awaitsStatus() bool {
	return r.Job == JobWorker && (r.WorkerState == promptedState || r.WorkerState == "" && r.Pending != nil)
}

// typePrompt claims Pending and marks the Worker prompted before it types, so a
// rerun during the Type sets Pending again and waits for the Worker.
func (s *Scheduler) typePrompt(ctx context.Context, r Record, stage lifecycle.Stage) {
	spec := s.spec(r, stage)
	spec.Restart, spec.Steers = false, nil
	prompt, err := s.d.Harness.Prompt(ctx, spec)
	if err != nil {
		s.fail(r.ID, "worker prompt", err)
		return
	}
	var claimed bool
	var was string
	s.update(r.ID, func(rec *Record) {
		if claimed = rec.Pending != nil && *rec.Pending == stage && rec.BlockID == r.BlockID; claimed {
			rec.Pending, rec.WorkerStage = nil, stage
			if prompt != "" {
				was, rec.WorkerState, rec.PendingSince = rec.WorkerState, promptedState, s.now()
			}
		}
	})
	if !claimed || prompt == "" {
		return
	}
	if err := s.d.Rex.Type(ctx, r.SessionID, r.BlockID, prompt); err != nil {
		var gone bool
		s.update(r.ID, func(rec *Record) {
			if rec.WorkerState == promptedState {
				rec.WorkerState = was
			}
			if rec.Pending == nil && rec.Stage == stage {
				s.pend(rec, stage)
			}
			gone = rec.BlockID != r.BlockID || rec.Job != JobWorker
		})
		// The tab closed during the Type, so the write had no target.
		if gone {
			s.deliver(ctx, r.ID)
			return
		}
		s.fail(r.ID, "type prompt", err)
	}
}
