package scheduler

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// sidePane is what a Task's side pane runs: its folder and its command. It
// never changes after it is made, so a lane's copy of the Record can share it.
type sidePane struct {
	Dir  string   `json:"dir"`
	Argv []string `json:"argv"`
}

func (p *sidePane) is(o *sidePane) bool {
	return p != nil && o != nil && p.Dir == o.Dir && slices.Equal(p.Argv, o.Argv)
}

// wantSide is the side pane that the Task's Worker needs now, or nil. A
// read-only Worker has no worktree, and a removed worktree has no changes to
// show. A build Task shows its worktree's changes since the merge-base with
// the default branch, so a rebase changes its pane (PROD-WORKER-012). A review
// or QA Task shows its PR, and reads the base only to learn that its worktree
// is there. ok is false when git cannot tell.
func (s *Scheduler) wantSide(ctx context.Context, r Record) (want *sidePane, ok bool) {
	if r.Worktree == "" {
		return nil, true
	}
	base, err := s.d.Worktrees.DiffBase(ctx, r.Worktree)
	switch {
	case errors.Is(err, ErrNoWorktree):
		return nil, true
	case err != nil:
		s.d.Log.Warn("side pane", "task", r.ID, "err", err)
		return nil, false
	case r.Review != nil:
		return &sidePane{Dir: r.Worktree, Argv: reviewSide(r.Review.URL)}, true
	case r.Kind == lifecycle.KindQA:
		return &sidePane{Dir: r.Worktree, Argv: reviewSide(prURL(r.PR))}, true
	}
	return &sidePane{Dir: r.Worktree, Argv: []string{"hunk", "diff", "--watch", base}}, true
}

// reviewSide shows the PR's page in rexweb, or its diff in hunk if RexWeb.app
// is missing. hunk reads a token from GH_TOKEN for a private repository. The
// shell sets it from gh, so the token is in no argv.
func reviewSide(url string) []string {
	return []string{"sh", "-c", `for a in "$HOME/Applications/RexWeb.app" /Applications/RexWeb.app; do p=$a/Contents/Resources/rexweb-pane; [ -x "$p" ] && exec "$p" "$1"; done; GH_TOKEN=$(gh auth token) exec hunk gh pr "$1"`, "side", url}
}

// sidePanes are the blocks of the Worker's tab that show a diff or a PR: each
// block but the Worker's that has the label side, or that runs hunk or
// rexweb-pane. The user can type hunk in a plain split, whose label is shell
// (ARCH-REX-012).
func sidePanes(panes []Pane, worker string) []string {
	var out []string
	for _, p := range panes {
		if p.BlockID != worker && (p.Label == "side" || p.Program == "hunk" || p.Program == "rexweb-pane") {
			out = append(out, p.BlockID)
		}
	}
	return out
}

// liveSide reads the Task and the side pane that its Worker needs. ok is false
// when the Task has no live Worker, or git cannot tell the pane.
func (s *Scheduler) liveSide(ctx context.Context, id string) (Record, *sidePane, bool) {
	r, ok := s.snapshot(id)
	if !ok || r.Job != JobWorker {
		return r, nil, false
	}
	want, ok := s.wantSide(ctx, r)
	return r, want, ok
}

// syncSide converges the Worker's tab on one side pane that runs want, or on
// none when want is nil (PROD-WORKER-015). panes are the side panes that are
// live in the tab. The Record's pane stays when it runs want, and every other
// pane closes. A new pane splits the old pane before the old pane closes, so
// it takes the old pane's place and size. A failed split leaves the Worker
// without a pane.
func (s *Scheduler) syncSide(ctx context.Context, r Record, panes []string, want *sidePane) {
	closeSide := func(block string) {
		if err := s.d.Rex.CloseBlock(ctx, r.SessionID, block); err != nil {
			s.d.Log.Warn("close side pane", "task", r.ID, "err", err)
		}
	}
	named := slices.Contains(panes, r.SideBlockID)
	var keep, old string
	switch {
	case named && want.is(r.Side):
		keep = r.SideBlockID
	case named && want != nil:
		old = r.SideBlockID
	case len(panes) > 0 && want != nil:
		old = panes[0]
	}
	for _, p := range panes {
		if p != keep && p != old {
			closeSide(p)
		}
	}
	if keep != "" {
		return
	}
	var side string
	if want != nil {
		var err error
		side, err = s.d.Rex.Split(ctx, r.SessionID, cmp.Or(old, r.BlockID), want.Dir, want.Argv)
		if err != nil && old != "" {
			side, err = s.d.Rex.Split(ctx, r.SessionID, r.BlockID, want.Dir, want.Argv)
		}
		if err != nil {
			s.d.Log.Warn("side pane", "task", r.ID, "err", err)
			want = nil
		}
	}
	if old != "" {
		closeSide(old)
	}
	s.update(r.ID, func(rec *Record) {
		if rec.BlockID == r.BlockID {
			rec.SideBlockID, rec.Side = side, want
		}
	})
}

// recoverSide keeps one side pane in a live Worker's tab at the Scheduler's
// start. It closes the others, and opens one when the tab has none or the
// kept one runs another command.
func (s *Scheduler) recoverSide(ctx context.Context, id string) {
	r, want, ok := s.liveSide(ctx, id)
	if !ok {
		return
	}
	panes, err := s.d.Rex.Panes(ctx, r.SessionID, r.WindowID)
	if err != nil {
		s.d.Log.Warn("side pane", "task", id, "err", err)
		return
	}
	s.syncSide(ctx, r, sidePanes(panes, r.BlockID), want)
}

// checkSides queues a check of each live Worker's side pane on its lane, one
// at a time for each Task. The check replaces a pane whose command changed and
// closes a pane that the Task no longer needs. It opens no pane where the
// Record names none, so a failed split is not tried again at each poll. Only
// the loop calls it.
func (s *Scheduler) checkSides() {
	for _, r := range s.list() {
		id := r.ID
		if r.Job != JobWorker || r.SideBlockID == "" || s.sideChecks[id] {
			continue
		}
		s.sideChecks[id] = true
		s.enqueue(id, func(ctx context.Context) {
			s.call(func() { delete(s.sideChecks, id) })
			r, want, ok := s.liveSide(ctx, id)
			if ok && r.SideBlockID != "" && !want.is(r.Side) {
				s.syncSide(ctx, r, []string{r.SideBlockID}, want)
			}
		})
	}
}
