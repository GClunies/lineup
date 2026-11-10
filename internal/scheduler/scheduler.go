// Package scheduler is `lineup serve`: it owns all Task state, applies the
// lifecycle, starts Workers, and reads Signals.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/store"
)

type Deps struct {
	Rex          Rex
	GitHub       GitHub
	Worktrees    Worktrees
	Harness      Harness
	Tracker      Tracker
	Config       Config
	Store        *store.Store
	DefaultCap   int
	PollInterval time.Duration
	// AdHocSession is the Rex session for Tasks with no Issue.
	AdHocSession string
	// Queues are the review queues that each full poll fills, in the order
	// that they pick PRs.
	Queues []Queue
	// Slots is how many review Tasks of all queues together can be started at
	// one time. 0 sets no shared cap.
	Slots int
	// StateHome is the XDG state folder. A review Worker gets it as
	// XDG_STATE_HOME, so the Scheduler finds pr-review's record where the
	// Worker writes it.
	StateHome string
	// BoardCache is where the Scheduler writes the latest BoardView, so the
	// Board paints from disk before it connects.
	BoardCache string
	Log        *slog.Logger
	Now        func() time.Time
}

type RexEventKind int

const (
	EvStatus RexEventKind = iota
	EvExited
	EvClosed
)

type RexEvent struct {
	Kind     RexEventKind
	BlockID  string
	ExitCode int
	Status   Status
}

type Scheduler struct {
	d        Deps
	inbox    chan func()
	board    *lifecycle.Board
	recs     map[string]*Record
	lanes    map[string]chan func(context.Context)
	watchers map[chan BoardView]struct{}
	// orphans holds Rex events for blocks the loop does not know yet: a fast
	// program can exit before NewWindow returns its block ID.
	orphans map[string][]orphan
	ctx     context.Context

	locksMu   sync.Mutex
	repoLocks map[string]*sync.Mutex

	filling atomic.Bool
	// paused is `lineup reviews pause`: no fill starts a review.
	paused bool
	// members holds the logins of the queues' author teams, read at membersAt.
	members   map[string][]string
	membersAt time.Time
}

type orphan struct {
	at time.Time
	ev RexEvent
}

func New(d Deps) (*Scheduler, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Scheduler{
		d: d, inbox: make(chan func()), board: lifecycle.NewBoard(d.DefaultCap),
		recs: map[string]*Record{}, lanes: map[string]chan func(context.Context){},
		watchers: map[chan BoardView]struct{}{}, orphans: map[string][]orphan{},
		repoLocks: map[string]*sync.Mutex{},
	}
	s.board.SharedSlots = d.Slots
	for _, q := range d.Queues {
		s.board.Slots[q.Name] = q.Slots
	}
	if v, err := d.Store.Setting("wip_cap"); err != nil {
		return nil, err
	} else if v != "" {
		s.board.Cap, _ = strconv.Atoi(v)
	}
	paused, err := d.Store.Setting(pausedKey)
	if err != nil {
		return nil, err
	}
	s.paused, _ = strconv.ParseBool(paused)
	raw, err := d.Store.Tasks()
	if err != nil {
		return nil, err
	}
	for id, data := range raw {
		r := &Record{}
		if err := json.Unmarshal(data, r); err != nil {
			return nil, fmt.Errorf("task %s: %w", id, err)
		}
		s.recs[id] = r
		s.board.Tasks[id] = &r.Task
	}
	return s, nil
}

// Run recovers lost Workers, then serves until ctx ends.
func (s *Scheduler) Run(ctx context.Context, events <-chan RexEvent) error {
	s.ctx = ctx
	s.recover(ctx)
	s.publish()
	tick := time.NewTicker(s.d.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-s.inbox:
			f()
		case ev, ok := <-events:
			if !ok {
				return errors.New("rex event stream ended")
			}
			s.onRex(ev)
		case <-tick.C:
			go s.poll(ctx, nil)
		}
	}
}

// call runs f on the loop goroutine and waits. Only lanes and API handlers
// call it, never the loop itself.
func (s *Scheduler) call(f func()) {
	done := make(chan struct{})
	s.inbox <- func() { f(); close(done) }
	<-done
}

func (s *Scheduler) now() time.Time { return s.d.Now() }

// apply runs one Signal through the reducer, records the change, and queues
// the Effects on the Task's lane.
func (s *Scheduler) apply(r *Record, sig lifecycle.Signal, detail string) error {
	from, flag, flagDetail, outcome := r.Stage, r.Flag, r.FlagDetail, r.Outcome
	effects, err := s.board.Apply(r.ID, sig)
	if err != nil {
		return err
	}
	if r.Stage == from && r.Flag == flag && r.FlagDetail == flagDetail && r.Outcome == outcome && len(effects) == 0 {
		return nil
	}
	if r.Stage != from {
		r.StageSince = s.now()
	}
	if detail == "" {
		detail = string(r.Flag)
		if r.FlagDetail != "" {
			detail += ": " + r.FlagDetail
		}
	}
	s.d.Log.Info("transition", "task", r.ID, "signal", sig.Name(), "from", from.String(), "to", r.Stage.String(), "detail", detail)
	s.save(r, store.Event{TaskID: r.ID, At: s.now(), Signal: sig.Name(), From: from.String(), To: r.Stage.String(), Detail: detail})
	if len(effects) > 0 {
		s.enqueue(r.ID, func(ctx context.Context) {
			for _, e := range effects {
				s.exec(ctx, r.ID, e)
			}
		})
	}
	if r.Stage != from && r.Stage == lifecycle.CIRunning {
		s.replayPR(r)
	}
	return nil
}

// replayPR applies the stored PR's pr-seen, so a Task that enters CI running
// moves on now and not at the next poll.
func (s *Scheduler) replayPR(r *Record) {
	if r.PR == nil {
		return
	}
	for _, sig := range s.d.GitHub.Signals(nil, r.PR) {
		if _, open := sig.(lifecycle.PROpen); !open {
			s.apply(r, sig, "")
		}
	}
}

func (s *Scheduler) save(r *Record, events ...store.Event) {
	data, err := json.Marshal(r)
	if err == nil {
		err = s.d.Store.Save(r.ID, data, events)
	}
	if err != nil {
		s.d.Log.Error("save", "task", r.ID, "err", err)
	}
	s.publish()
}

// enqueue runs f on the Task's lane. Each Task's Effects run in order, and
// no lane blocks the loop.
func (s *Scheduler) enqueue(id string, f func(context.Context)) {
	lane, ok := s.lanes[id]
	if !ok {
		lane = make(chan func(context.Context), 1024)
		s.lanes[id] = lane
		go func() {
			for f := range lane {
				f(s.ctx)
			}
		}()
	}
	lane <- f
}

// snapshot returns a copy of the Record for a lane to read.
func (s *Scheduler) snapshot(id string) (Record, bool) {
	var r Record
	var ok bool
	s.call(func() {
		var p *Record
		if p, ok = s.recs[id]; ok {
			r = *p
		}
	})
	return r, ok
}

// update runs f on the live Record in the loop and saves it.
func (s *Scheduler) update(id string, f func(r *Record)) {
	s.call(func() {
		if r, ok := s.recs[id]; ok {
			f(r)
			s.save(r)
		}
	})
}

// fail flags the Task with an error from an Effect or a check.
func (s *Scheduler) fail(id string, what string, err error) {
	s.d.Log.Error(what, "task", id, "err", err)
	s.call(func() {
		if r, ok := s.recs[id]; ok {
			s.apply(r, lifecycle.WorkerStatus{State: "error", Detail: what + ": " + err.Error()}, "")
		}
	})
}

func (s *Scheduler) exec(ctx context.Context, id string, e lifecycle.Effect) {
	r, ok := s.snapshot(id)
	if !ok {
		return
	}
	switch e := e.(type) {
	case lifecycle.StartWorker:
		s.startWorker(ctx, id, e.Stage)
	case lifecycle.Merge:
		if r.PR == nil {
			return
		}
		if err := s.d.GitHub.Merge(ctx, r.RepoSlug, r.PR.Number); err != nil {
			s.fail(id, "merge", err)
			return
		}
		s.poll(ctx, []string{id})
	case lifecycle.Cleanup:
		s.cleanup(ctx, r)
	case lifecycle.TrackerStatus:
		if r.Issue == nil || s.d.Tracker == nil {
			return
		}
		if e.Event == "started" {
			if err := s.d.Tracker.Begin(ctx, *r.Issue); err != nil {
				s.d.Log.Error("assign issue", "task", id, "err", err)
			}
		}
		if err := s.d.Tracker.SetStatus(ctx, *r.Issue, e.Event); err != nil {
			s.d.Log.Error("tracker status", "task", id, "event", e.Event, "err", err)
		}
	}
}

func (s *Scheduler) setup(ctx context.Context, r Record) {
	if r.RepoSlug == "" {
		slug, err := s.d.GitHub.RepoSlug(ctx, r.Repo)
		if err != nil {
			s.setupFailed(r.ID, "repo slug: "+err.Error())
			return
		}
		s.update(r.ID, func(r *Record) { r.RepoSlug = slug })
	}
	session, err := s.d.Rex.EnsureSession(ctx, r.Session)
	if err != nil {
		s.setupFailed(r.ID, "rex session: "+err.Error())
		return
	}
	w, err := s.d.Rex.NewWindow(ctx, session, r.tabLabel(), r.Repo, nil, s.d.Worktrees.CreateArgv(r.Repo, r.Branch))
	if err != nil {
		s.setupFailed(r.ID, "rex tab: "+err.Error())
		return
	}
	s.attach(r.ID, w, JobSetup)
}

// attach records the Task's new tab and replays any events that beat it here.
func (s *Scheduler) attach(id string, w Window, job Job) {
	s.call(func() {
		r, ok := s.recs[id]
		if !ok {
			return
		}
		r.SessionID, r.WindowID, r.BlockID, r.Job = w.SessionID, w.WindowID, w.BlockID, job
		r.Status, r.WorkerState = "", ""
		s.save(r)
		for _, o := range s.orphans[w.BlockID] {
			s.onRex(o.ev)
		}
		delete(s.orphans, w.BlockID)
	})
}

func (s *Scheduler) setupFailed(id, detail string) {
	s.call(func() {
		if r, ok := s.recs[id]; ok {
			s.apply(r, lifecycle.SetupFailed{Detail: detail}, "")
		}
	})
}

// finishSetup runs after worktrunk exits with 0 in the setup tab.
func (s *Scheduler) finishSetup(ctx context.Context, id string) {
	r, ok := s.snapshot(id)
	if !ok {
		return
	}
	path, err := s.d.Worktrees.Path(ctx, r.Repo, r.Branch)
	if err == nil {
		err = s.d.Harness.Prepare(ctx, r.Repo, path)
	}
	if err != nil {
		s.setupFailed(id, err.Error())
		return
	}
	var pending *lifecycle.Stage
	s.update(id, func(r *Record) {
		r.Worktree = path
		pending, r.Pending = r.Pending, nil
	})
	if pending != nil {
		s.startWorker(ctx, id, *pending)
	}
}

func (s *Scheduler) startWorker(ctx context.Context, id string, stage lifecycle.Stage) {
	r, ok := s.snapshot(id)
	if !ok || r.Stage != stage {
		return
	}
	switch {
	case r.Review != nil:
		if !s.setupReview(ctx, r) {
			return
		}
		if r, ok = s.snapshot(id); !ok || r.Stage != stage {
			return
		}
	case r.Job == JobSetup || r.Worktree == "":
		s.update(id, func(r *Record) { r.Pending = &stage })
		if r.Job != JobSetup {
			s.setup(ctx, r)
		}
		return
	}
	if r.Review != nil && stage == lifecycle.Reviewing {
		// A restart or a rerun reviews afresh, so it never resumes the last review.
		r.Conversation = uuid.NewV4().String()
		s.update(id, func(rec *Record) { rec.Conversation = r.Conversation })
	}
	restart, steers := s.replay(id, r.StageSince)
	spec := WorkerSpec{
		Restart: restart, Steers: steers,
		Task: r.ID, Stage: stage, Repo: r.Repo, Worktree: r.Worktree, Branch: r.Branch,
		Issue: r.Issue, Text: r.Text, PRURL: prURL(r.PR), Overrides: r.Overrides,
		Conversation: r.Conversation, Name: r.tabLabel(), ReviewCycle: r.ReviewCycle(),
		Bar: r.Policy.Bar, Standing: r.Standing,
	}
	folder := r.Worktree
	if r.Review != nil {
		// The PR's code never becomes the Worker's folder: its .claude
		// settings and hooks would run.
		spec.PRURL, folder = r.Review.URL, r.Repo
	}
	argv, env, err := s.d.Harness.Command(ctx, spec)
	if err != nil {
		s.fail(id, "worker command", err)
		return
	}
	if r.Review != nil {
		if env == nil {
			env = map[string]string{}
		}
		env["XDG_STATE_HOME"] = s.d.StateHome
	}
	s.detach(ctx, r)
	session, err := s.d.Rex.EnsureSession(ctx, r.Session)
	if err != nil {
		s.fail(id, "rex session", err)
		return
	}
	w, err := s.d.Rex.NewWindow(ctx, session, r.tabLabel(), folder, env, argv)
	if err != nil {
		s.fail(id, "rex tab", err)
		return
	}
	s.update(id, func(r *Record) { r.WorkerStage = stage })
	s.attach(id, w, JobWorker)
	if r.Review != nil {
		s.openHunk(ctx, id, w.SessionID, w.BlockID, r.Worktree, r.Review.URL)
	}
}

// steerSignal names the event that records one `lineup steer`.
const steerSignal = "steer"

// replay reports whether the Stage lost a Worker since it began, and the
// steers the user sent since. A new Stage has neither.
func (s *Scheduler) replay(id string, since time.Time) (restart bool, steers []string) {
	events, err := s.d.Store.Events(id)
	if err != nil {
		s.d.Log.Warn("replay", "task", id, "err", err)
		return false, nil
	}
	for _, e := range events {
		if e.At.Before(since) {
			continue
		}
		switch e.Signal {
		case lifecycle.WorkerLost{}.Name():
			restart = true
		case steerSignal:
			steers = append(steers, e.Detail)
		}
	}
	return restart, steers
}

// openHunk shows the PR in hunk beside a review Worker. A failed split leaves
// the Worker running.
func (s *Scheduler) openHunk(ctx context.Context, id, session, block, worktree, url string) {
	hunk, err := s.d.Rex.Split(ctx, session, block, worktree, hunkArgv(url))
	if err != nil {
		s.d.Log.Warn("hunk pane", "task", id, "err", err)
		return
	}
	s.update(id, func(r *Record) {
		if r.BlockID == block {
			r.HunkBlockID = hunk
		}
	})
}

// sessionEmpty reports whether a session holds no tabs. The user, or another
// Scheduler, can open tabs in a session that lineup made.
func (s *Scheduler) sessionEmpty(ctx context.Context, session string) bool {
	blocks, err := s.d.Rex.Blocks(ctx, session)
	if err != nil {
		s.d.Log.Warn("list session blocks", "session", session, "err", err)
		return false
	}
	return len(blocks) == 0
}

// detach forgets the Task's tab before closing it, so its exit is not read as a lost Worker.
func (s *Scheduler) detach(ctx context.Context, r Record) {
	if r.WindowID == "" {
		return
	}
	s.update(r.ID, func(r *Record) { r.WindowID, r.BlockID, r.HunkBlockID, r.Job = "", "", "", JobNone })
	if err := s.d.Rex.CloseWindow(ctx, r.SessionID, r.WindowID); err != nil {
		s.d.Log.Warn("close tab", "task", r.ID, "err", err)
	}
}

func (s *Scheduler) cleanup(ctx context.Context, r Record) {
	s.detach(ctx, r)
	switch {
	case r.Review != nil:
		s.removeReviewWorktree(ctx, r)
	// A Task gets a SessionID when setup opens its tab. Setup can fail after
	// worktrunk made the worktree but before the Record holds its path.
	case r.Worktree != "" || r.SessionID != "":
		if err := s.d.Worktrees.Remove(ctx, r.Repo, r.Branch); err != nil {
			s.d.Log.Error("remove worktree", "task", r.ID, "err", err)
		}
	}
	var last bool
	s.call(func() {
		last = !slices.ContainsFunc(s.list(), func(o *Record) bool {
			return o.ID != r.ID && o.Session == r.Session && o.Stage.Started()
		})
	})
	if last && r.SessionID != "" && s.sessionEmpty(ctx, r.SessionID) {
		if err := s.d.Rex.DestroySession(ctx, r.SessionID); err != nil {
			s.d.Log.Warn("remove session", "session", r.Session, "err", err)
		}
	}
}

func (s *Scheduler) byBlock(block string) *Record {
	for _, r := range s.recs {
		if r.BlockID == block && block != "" {
			return r
		}
	}
	return nil
}

func (s *Scheduler) onRex(ev RexEvent) {
	r := s.byBlock(ev.BlockID)
	if r == nil {
		s.keepOrphan(ev)
		return
	}
	switch ev.Kind {
	case EvStatus:
		if r.Job != JobWorker {
			return
		}
		was := r.WorkerState
		r.Status, r.WorkerState = ev.Status.Msg, ev.Status.State
		if ev.Status.State == "done" {
			s.checkContract(r, false)
			s.save(r)
			return
		}
		// A working Worker reports each new step as working again. Only a
		// return to working clears a flag, such as the question it asked.
		if ev.Status.State != "working" || was != "working" {
			s.apply(r, lifecycle.WorkerStatus{State: ev.Status.State, Kind: ev.Status.Kind, Detail: ev.Status.Msg}, "")
		}
		if r.asksForDecisions(ev.Status) {
			s.checkContract(r, true)
		}
		s.publish()

	case EvExited:
		job := r.Job
		r.Job = JobExited
		s.save(r)
		switch {
		case job == JobSetup && ev.ExitCode != 0:
			s.apply(r, lifecycle.SetupFailed{Detail: fmt.Sprintf("worktrunk exited with %d", ev.ExitCode)}, "")
		case job == JobSetup:
			id := r.ID
			s.enqueue(id, func(ctx context.Context) { s.finishSetup(ctx, id) })
		case job == JobWorker && ev.ExitCode != 0:
			s.apply(r, lifecycle.WorkerExited{Code: ev.ExitCode}, "")
		case job == JobWorker:
			s.checkContract(r, false)
		}

	case EvClosed:
		job := r.Job
		s.forgetTab(r)
		s.save(r)
		switch {
		case job == JobSetup:
			s.apply(r, lifecycle.SetupFailed{Detail: "setup tab closed"}, "")
		case job == JobWorker:
			s.apply(r, lifecycle.WorkerLost{}, "")
		}
	}
}

// forgetTab drops the Task's tab after its block is gone. The hunk pane
// holds a review tab open, so the Scheduler closes that tab.
func (s *Scheduler) forgetTab(r *Record) {
	id, session, window := r.ID, r.SessionID, r.WindowID
	r.WindowID, r.BlockID, r.HunkBlockID, r.Job = "", "", "", JobNone
	if r.Review == nil || window == "" {
		return
	}
	s.enqueue(id, func(ctx context.Context) {
		if err := s.d.Rex.CloseWindow(ctx, session, window); err != nil {
			s.d.Log.Warn("close tab", "task", id, "err", err)
		}
	})
}

func (s *Scheduler) keepOrphan(ev RexEvent) {
	cutoff := s.now().Add(-time.Minute)
	for b, os := range s.orphans {
		if os[len(os)-1].at.Before(cutoff) {
			delete(s.orphans, b)
		}
	}
	s.orphans[ev.BlockID] = append(s.orphans[ev.BlockID], orphan{s.now(), ev})
}

// checkContract runs when a Worker reports done, or when a review Worker asks
// for decisions. It observes the Stage contract's output and applies done
// with the result. An unmet contract after a question applies nothing, so the
// Task keeps the question flag.
func (s *Scheduler) checkContract(r *Record, question bool) {
	id, stage, block := r.ID, r.Stage, r.BlockID
	if !stage.Agent() {
		return
	}
	s.enqueue(id, func(ctx context.Context) {
		if stage != lifecycle.Reviewing {
			s.poll(ctx, []string{id})
		}
		s.call(func() {
			r, ok := s.recs[id]
			if !ok || r.Stage != stage || (r.BlockID != block && r.BlockID != "") {
				return
			}
			reviewed, met := s.contractOutput(r)
			if met || !question {
				if met && reviewed != "" {
					r.ReviewedHead = reviewed
				}
				s.apply(r, lifecycle.WorkerDone{ContractMet: met}, "")
			}
		})
	})
}

// contractOutput reports whether the Task's Stage contract output exists, as
// the latest poll saw it. reviewed is the head that a review's findings name.
func (s *Scheduler) contractOutput(r *Record) (reviewed string, met bool) {
	switch r.Stage {
	case lifecycle.Reviewing:
		return s.findings(*r.Review, r.StageSince)
	case lifecycle.Working:
		return "", r.PR != nil
	}
	// CI debug and Responding meet the contract with a push, which the poll
	// turns into pushed. Still being here after it means no push.
	return "", !r.Stage.Agent()
}

// poll reads GitHub for the given Tasks, or for every started Task when ids
// is nil. A full poll then fills the review queues.
func (s *Scheduler) poll(ctx context.Context, ids []string) {
	s.pollTasks(ctx, ids)
	if ids == nil {
		s.fill(ctx)
	}
}

// pollTasks reads the PRs of the Tasks. A build Task's PR comes by its
// branch, and a review Task's PR by its number, so the branch Signals never
// reach a PR that lineup reviews.
func (s *Scheduler) pollTasks(ctx context.Context, ids []string) {
	keys := map[PRKey]string{}
	reviews := map[ReviewKey]string{}
	s.call(func() {
		for _, r := range s.list() {
			switch {
			case !r.Stage.Started() || (ids != nil && !slices.Contains(ids, r.ID)):
			case r.Review != nil:
				reviews[r.Review.key()] = r.ID
			case r.RepoSlug != "":
				key := PRKey{Repo: r.RepoSlug, Branch: r.Branch}
				if g := r.Policy.Bar.Bot; g != nil {
					key.Bot = g.Bot
				}
				keys[key] = r.ID
			}
		}
	})
	s.pollReviews(ctx, reviews)
	if len(keys) == 0 {
		return
	}
	res, err := s.d.GitHub.Poll(ctx, slices.Collect(mapKeys(keys)))
	if err != nil {
		s.d.Log.Error("github poll", "err", err)
		return
	}
	s.call(func() {
		for key, id := range keys {
			cur, ok := res[key]
			r := s.recs[id]
			if !ok || r == nil {
				continue
			}
			sigs := s.d.GitHub.Signals(r.PR, &cur)
			r.PR = &cur
			for _, sig := range sigs {
				s.apply(r, sig, "")
			}
			// pr-seen can change the Standing alone, with no transition to save.
			s.save(r)
			if r.Stage == lifecycle.CIRunning && s.now().Sub(r.StageSince) >= botReviewWait {
				s.apply(r, lifecycle.ReviewBotLate{}, "")
			}
		}
	})
}

// botReviewWait is how long a gated Task can be in CI running before passed
// checks with no review bot score flag it. Greptile's check run keeps CI
// pending while it reviews, and it scored each probed push within 4
// minutes. Passed checks and no score after this long mean the bot is not
// reviewing, for example in a Repo where it is not installed.
const botReviewWait = 15 * time.Minute

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func prURL(p *PRSnapshot) string {
	if p == nil {
		return ""
	}
	return p.URL
}

// recover compares the store with Rex. Each Task in an agent Stage whose tab
// is gone gets a fresh Worker from its restart budget.
func (s *Scheduler) recover(ctx context.Context) {
	live := map[string]map[string]bool{}
	for _, r := range s.list() {
		if !r.Stage.Started() || r.SessionID == "" && r.Job == JobNone && !r.Stage.Agent() {
			continue
		}
		blocks, ok := live[r.SessionID]
		if !ok {
			var err error
			if blocks, err = s.d.Rex.Blocks(ctx, r.SessionID); err != nil {
				s.d.Log.Error("recover", "task", r.ID, "err", err)
				continue
			}
			live[r.SessionID] = blocks
		}
		if r.BlockID != "" && blocks[r.BlockID] {
			if r.Job == JobWorker {
				if st, err := s.d.Rex.ProgramStatus(ctx, r.SessionID, r.BlockID); err == nil && st.State != "" {
					s.onRex(RexEvent{Kind: EvStatus, BlockID: r.BlockID, Status: st})
				}
				if r.Review != nil && !blocks[r.HunkBlockID] {
					id, session, block, worktree, url := r.ID, r.SessionID, r.BlockID, r.Worktree, r.Review.URL
					s.enqueue(id, func(ctx context.Context) { s.openHunk(ctx, id, session, block, worktree, url) })
				}
			}
			continue
		}
		job := r.Job
		s.forgetTab(r)
		s.save(r)
		if r.Flag != "" && !r.Flag.Transient() {
			continue
		}
		switch {
		case r.Review != nil && r.Worktree == "" && r.Stage.Agent():
			id, stage := r.ID, r.Stage
			s.enqueue(id, func(ctx context.Context) { s.startWorker(ctx, id, stage) })
		case job == JobSetup || r.Worktree == "" && r.Stage.Agent():
			stage := r.Stage
			r.Pending = &stage
			rec := *r
			s.enqueue(r.ID, func(ctx context.Context) { s.setup(ctx, rec) })
		case r.Stage.Agent():
			s.d.Log.Info("recover worker", "task", r.ID, "stage", r.Stage.String())
			s.apply(r, lifecycle.WorkerLost{}, "recovered at start")
		}
	}
}

// list returns the Records in a stable order: oldest first.
func (s *Scheduler) list() []*Record {
	out := slices.Collect(func(yield func(*Record) bool) {
		for _, r := range s.recs {
			if !yield(r) {
				return
			}
		}
	})
	slices.SortFunc(out, func(a, b *Record) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmpString(a.ID, b.ID)
	})
	return out
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// doneShownFor is how long a Done Task stays on the Board.
const doneShownFor = 7 * 24 * time.Hour

func (s *Scheduler) view() BoardView {
	v := BoardView{Cap: s.board.Cap, Started: s.board.Started(), OverCap: s.board.OverCap(), ReviewsPaused: s.paused}
	for _, r := range s.list() {
		if r.Stage == lifecycle.Done && (r.Archived || s.now().Sub(r.StageSince) > doneShownFor) {
			continue
		}
		v.Tasks = append(v.Tasks, r.view())
	}
	return v
}

// publish sends the Board to every watcher and to the cache file. A slow
// watcher misses intermediate views but always gets the latest one.
func (s *Scheduler) publish() {
	v := s.view()
	for w := range s.watchers {
		select {
		case <-w:
		default:
		}
		w <- v
	}
	if s.d.BoardCache != "" {
		if data, err := json.Marshal(v); err == nil {
			tmp := s.d.BoardCache + ".tmp"
			if os.WriteFile(tmp, data, 0o600) == nil {
				os.Rename(tmp, s.d.BoardCache)
			}
		}
	}
}
