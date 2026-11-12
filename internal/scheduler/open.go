package scheduler

import (
	"context"
	"fmt"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

type OpenAction string

const (
	OpenFocused  OpenAction = "focused"  // the Task's tab was live, and it came to the front
	OpenRestored OpenAction = "restored" // a new tab resumed the Task's conversation
	OpenStarted  OpenAction = "started"  // the conversation was gone, and a fresh Worker began again
)

type OpenResult struct {
	TaskView
	Action OpenAction `json:"action"`
}

// open is the user's ask for the Task's Worker. It runs on the Task's lane, so
// it never overlaps a prompt that the lane delivers.
func (s *Scheduler) open(id string) (OpenResult, error) {
	var res OpenResult
	var err error
	done := make(chan struct{})
	s.call(func() {
		if _, ok := s.recs[id]; !ok {
			err = fmt.Errorf("%w: %s", lifecycle.ErrUnknownTask, id)
			close(done)
			return
		}
		s.enqueue(id, func(ctx context.Context) {
			defer close(done)
			res, err = s.restore(ctx, id, true)
		})
	})
	<-done
	return res, err
}

// restoreLater restores the Task's Worker on its lane, without a focus. Only the
// loop calls it.
func (s *Scheduler) restoreLater(id string) {
	s.enqueue(id, func(ctx context.Context) {
		if _, err := s.restore(ctx, id, false); err != nil {
			s.d.Log.Warn("restore the Worker", "task", id, "err", err)
		}
	})
}

// restore gives the Task a Worker in a tab, and focuses it when focus is true.
// A tab that Rex kept after its program exited closes first.
func (s *Scheduler) restore(ctx context.Context, id string, focus bool) (OpenResult, error) {
	r, ok := s.snapshot(id)
	if !ok {
		return OpenResult{}, fmt.Errorf("%w: %s", lifecycle.ErrUnknownTask, id)
	}
	res := OpenResult{TaskView: r.view(), Action: OpenFocused}
	switch {
	case r.Stage == lifecycle.Queued:
		return res, fmt.Errorf("%s has not started", id)
	case r.Stage == lifecycle.Done && r.WindowID == "":
		return res, fmt.Errorf("%s is Done and its worktree is gone", id)
	case r.WindowID != "" && (r.Job != JobExited || r.Stage == lifecycle.Done):
		if !focus {
			return res, nil
		}
		return res, s.d.Rex.Focus(ctx, r.SessionID, r.WindowID, r.BlockID)
	case r.Review == nil && r.Worktree == "":
		return res, fmt.Errorf("%s has no worktree yet", id)
	}
	gone := r.Conversation != "" && !s.d.Harness.HasConversation(ctx, WorkerSpec{Repo: r.Repo, Conversation: r.Conversation})
	fresh := r.Conversation == "" || gone
	if fresh && r.Stage != lifecycle.ReviewReady && !r.Stage.Agent() {
		return res, fmt.Errorf("%s has no conversation to restore in %s", id, r.Stage)
	}
	s.closeTab(ctx, r)
	if fresh {
		s.update(id, func(rec *Record) {
			rec.Conversation = ""
			s.save(rec, store.Event{TaskID: id, At: s.now(), Signal: replacedSignal, From: rec.Stage.String(), To: rec.Stage.String(), Detail: r.Conversation})
		})
		if r.Stage == lifecycle.ReviewReady {
			// The review's findings are on disk, but nothing resumes their
			// conversation, so the Task reviews again.
			if err := s.signal(id, lifecycle.Rerun{Stage: lifecycle.Reviewing}); err != nil {
				return res, err
			}
		}
	}
	r, ok = s.snapshot(id)
	if !ok {
		return res, fmt.Errorf("%w: %s", lifecycle.ErrUnknownTask, id)
	}
	if !s.openWorker(ctx, r, r.Stage, fresh) {
		after, _ := s.snapshot(id)
		return OpenResult{TaskView: after.view()}, fmt.Errorf("%s did not open: %s", id, after.view().Note)
	}
	if err := s.signal(id, lifecycle.TabOpened{}); err != nil {
		return res, err
	}
	res.Action = OpenRestored
	if fresh {
		res.Action = OpenStarted
	}
	if focus {
		if now, ok := s.snapshot(id); ok && now.WindowID != "" {
			if err := s.d.Rex.Focus(ctx, now.SessionID, now.WindowID, now.BlockID); err != nil {
				s.d.Log.Warn("focus restored tab", "task", id, "err", err)
			}
		}
	}
	return s.opened(res, id), nil
}

func (s *Scheduler) opened(res OpenResult, id string) OpenResult {
	if r, ok := s.snapshot(id); ok {
		res.TaskView = r.view()
	}
	return res
}
