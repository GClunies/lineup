package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrWIPCapFull  = errors.New("wip cap full")
	ErrSlotsFull   = errors.New("review slots full")
	ErrUnknownTask = errors.New("unknown task")
)

// NotAllowedError is a user signal that does not fit the Task's Stage.
type NotAllowedError struct {
	Signal string
	Stage  Stage
}

func (e *NotAllowedError) Error() string {
	return fmt.Sprintf("%s is not allowed in %s", e.Signal, e.Stage)
}

type Budgets struct {
	CIDebug    int `json:"ci_debug"`
	Responding int `json:"responding"`
	Restarts   int `json:"restarts"`
	// ReviewCycles is how many Responding rounds a low review bot score can
	// start. It is apart from Responding, which people's reviews spend.
	ReviewCycles int `json:"review_cycles"`
}

// Policy is the resolved configuration that a Task keeps from its start.
type Policy struct {
	Budgets Budgets    `json:"budgets"`
	Bar     LandingBar `json:"bar"`
}

// UnmarshalJSON also reads a Policy from before the Landing bar, which kept
// the review bot gate in gate. It gets the zero LandingBar with that gate.
func (p *Policy) UnmarshalJSON(b []byte) error {
	type policy Policy
	var old struct {
		policy
		Gate *ReviewGate `json:"gate"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	*p = Policy(old.policy)
	if p.Bar.Bot == nil {
		p.Bar.Bot = old.Gate
	}
	return nil
}

// ReviewGate keeps a PR in CI running until a review bot scores its head
// commit. A score below MinScore starts a Responding round.
type ReviewGate struct {
	Bot      string `json:"bot"`   // the bot's name in the configuration, for example greptile
	Title    string `json:"title"` // the bot's name for people, for example Greptile
	MinScore int    `json:"min_score"`
	TopScore int    `json:"top_score"`
}

// ScoreText names a score of the bot, for example "Greptile 4/5".
func (g ReviewGate) ScoreText(n int) string { return fmt.Sprintf("%s %d/%d", g.Title, n, g.TopScore) }

// ReviewCycle is a Responding round that the review bot started: a low score,
// or open bot threads on a score that passes.
type ReviewCycle struct {
	ReviewGate
	Score int
}

type Task struct {
	ID string `json:"id"`
	// Kind is build in a Task from before review Tasks.
	Kind Kind `json:"kind,omitempty"`
	// Queue is the review queue that added a review Task, or "".
	Queue      string  `json:"queue,omitempty"`
	Stage      Stage   `json:"stage"`
	Flag       Reason  `json:"flag,omitempty"`
	FlagDetail string  `json:"flag_detail,omitempty"`
	Outcome    Outcome `json:"outcome,omitempty"`
	OverCap    bool    `json:"over_cap,omitempty"`
	Policy     Policy  `json:"policy"`
	// Left is what remains of each Loop budget.
	Left Budgets `json:"left"`
	// BotScore is the review bot's score when a review cycle started this
	// Responding round. It is nil for a round that people started.
	BotScore *int `json:"bot_score,omitempty"`
	// Standing is the Task's PR measured against its Landing bar at the
	// latest poll. enter keeps it, so the next Worker's prompt can read it.
	Standing Standing `json:"standing,omitzero"`
	// BarFlag is true when the Standing set the budget spent flag. The next
	// pr-seen clears such a flag once its gap closes. A budget spent on a
	// review edge stays until the user acts.
	BarFlag bool `json:"bar_flag,omitempty"`
}

// ReviewCycle is the review cycle that the Task is in, or nil.
func (t *Task) ReviewCycle() *ReviewCycle {
	if t.BotScore == nil || t.Policy.Bar.Bot == nil {
		return nil
	}
	return &ReviewCycle{*t.Policy.Bar.Bot, *t.BotScore}
}

func (t *Task) Group() Group {
	if t.Stage == Done {
		return GroupDone
	}
	if t.Flag != "" {
		return GroupNeedsYou
	}
	return stages[t.Stage].group
}

type Board struct {
	Cap int
	// Slots is how many review Tasks of each queue can be started at one
	// time. A queue that is missing has no slot.
	Slots map[string]int
	// SharedSlots is how many review Tasks of all queues together can be
	// started at one time. 0 sets no shared cap.
	SharedSlots int
	Tasks       map[string]*Task
}

func NewBoard(cap int) *Board {
	return &Board{Cap: cap, Slots: map[string]int{}, Tasks: map[string]*Task{}}
}

// Add puts a Queued build Task on the Board.
func (b *Board) Add(id string, p Policy) *Task {
	t := &Task{ID: id, Stage: Queued, Policy: p, Left: p.Budgets}
	b.Tasks[id] = t
	return t
}

// AddReview puts a Queued review Task on the Board. queue is "" for a review
// that the user asked for.
func (b *Board) AddReview(id, queue string, p Policy) *Task {
	t := b.Add(id, p)
	t.Kind, t.Queue = KindReview, queue
	return t
}

// Started counts the started build Tasks. Review Tasks never count toward the WIP cap.
func (b *Board) Started() int {
	return b.count(func(t *Task) bool { return t.Kind == KindBuild })
}

// OverCap is how many started build Tasks went over the cap with the user's yes.
func (b *Board) OverCap() int {
	return b.count(func(t *Task) bool { return t.Kind == KindBuild && t.OverCap })
}

// QueueStarted counts the started review Tasks of the queue.
func (b *Board) QueueStarted(queue string) int {
	return b.count(func(t *Task) bool { return t.Kind == KindReview && t.Queue == queue })
}

// QueuesStarted counts the started review Tasks that any queue added.
func (b *Board) QueuesStarted() int {
	return b.count(func(t *Task) bool { return t.Kind == KindReview && t.Queue != "" })
}

func (b *Board) count(match func(*Task) bool) int {
	n := 0
	for _, t := range b.Tasks {
		if t.Stage.Started() && match(t) {
			n++
		}
	}
	return n
}

// Apply moves the Task by one Signal and returns the Effects to execute. A
// Worker or GitHub Signal that does not fit the Stage changes nothing.
func (b *Board) Apply(id string, s Signal) ([]Effect, error) {
	t, ok := b.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, id)
	}
	notAllowed := &NotAllowedError{Signal: s.Name(), Stage: t.Stage}

	switch s := s.(type) {
	case Start:
		if t.Stage != Queued {
			return nil, notAllowed
		}
		if t.Kind == KindReview {
			if t.Queue != "" && (b.QueueStarted(t.Queue) >= b.Slots[t.Queue] ||
				b.SharedSlots > 0 && b.QueuesStarted() >= b.SharedSlots) {
				return nil, ErrSlotsFull
			}
			return t.startAgent(Reviewing), nil
		}
		if b.Started() >= b.Cap && !s.OverCap {
			return nil, ErrWIPCapFull
		}
		t.OverCap = s.OverCap && b.Started() >= b.Cap
		t.enter(Working)
		return []Effect{TrackerStatus{"started"}, StartWorker{Working}}, nil

	case PRMerge:
		if t.Stage != ReadyToLand {
			return nil, notAllowed
		}
		return []Effect{Merge{}}, nil

	case TaskClosed:
		if t.Stage == Done {
			return []Effect{Cleanup{}}, nil
		}
		return append([]Effect{Cleanup{}}, t.finish(OutcomeClosed)...), nil

	case TabClosed:
		if t.Stage == Done {
			return []Effect{Cleanup{}}, nil
		}
		return nil, nil

	case Rerun:
		if !s.Stage.Agent() || !t.Kind.Has(s.Stage) || t.Stage == Queued || t.Stage == Done {
			return nil, notAllowed
		}
		return t.startAgent(s.Stage), nil

	case Reopen:
		if !t.Stage.Resumable() {
			return nil, notAllowed
		}
		t.flag("", "")
		return []Effect{StartWorker{t.Stage}}, nil

	case PRMerged:
		if t.Stage == Done || t.Stage == Queued {
			return nil, nil
		}
		return t.finish(OutcomeMerged), nil
	}

	if !t.Stage.Started() {
		return nil, nil
	}
	return t.observe(s), nil
}

// observe applies a Worker or GitHub Signal to a started Task.
func (t *Task) observe(s Signal) []Effect {
	switch s := s.(type) {
	case WorkerStatus:
		switch s.State {
		case "working":
			if t.Flag.Transient() {
				t.flag("", "")
			}
		case "blocked":
			reason := Reason(s.Kind)
			if !reason.Transient() || reason == ReasonError {
				reason = ReasonQuestion
			}
			t.flag(reason, s.Detail)
		case "error":
			t.flag(ReasonError, s.Detail)
		}

	case WorkerExited:
		if s.Code != 0 && t.Stage.Agent() {
			t.flag(ReasonError, fmt.Sprintf("exit code %d", s.Code))
		}

	case SetupFailed:
		t.flag(ReasonSetupFailed, s.Detail)

	case WorkerLost:
		if !t.Stage.Agent() {
			return nil
		}
		if t.Left.Restarts == 0 {
			t.flag(ReasonWorkerLost, "")
			return nil
		}
		t.Left.Restarts--
		t.flag("", "")
		return []Effect{StartWorker{t.Stage}}

	case WorkerDone:
		if !t.Stage.Agent() {
			return nil
		}
		if !s.ContractMet {
			if t.answered() {
				t.enter(CIRunning)
				return nil
			}
			if t.Flag != ReasonQuestion {
				t.flag(ReasonStopped, s.Detail)
			}
			return nil
		}
		if next, ok := afterContract[t.Stage]; ok {
			t.enter(next)
		}

	case PROpen:
		if t.Stage == Working && t.Flag == ReasonStopped {
			t.enter(CIRunning)
		}

	case Pushed:
		if t.Stage == CIDebug || t.Stage == Responding {
			t.enter(CIRunning)
		}

	case PRSeen:
		if t.Kind != KindBuild {
			return nil
		}
		t.Standing = t.Policy.Bar.Measure(s.Facts)
		if t.settles() {
			return t.settle()
		}

	case ReviewAdditionalAuthors:
		if t.Stage == InReview {
			return t.spend(respondingLoop, "")
		}

	case ReviewCommented, ReviewChangesRequested:
		if t.Stage == InReview || t.Stage == ReadyToLand {
			return t.spend(respondingLoop, "")
		}

	case ReviewBotLate:
		if head := t.Standing.Head; t.Stage == CIRunning && t.Standing.Next() == GapScoreDue {
			t.flag(ReasonNoBotReview, fmt.Sprintf("%s did not score %s", t.Policy.Bar.Bot.Title, head[:min(7, len(head))]))
		}

	case PRReviewed:
		if t.Kind == KindReview {
			return t.finish(OutcomeReviewed)
		}

	case PRClosed:
		if t.Kind == KindReview {
			return t.finish(OutcomeClosed)
		}
	}
	return nil
}

// afterContract is the Stage that a met Stage contract leads to. The other
// agent Stages move on when the poll sees their push.
var afterContract = map[Stage]Stage{
	Working:   CIRunning,
	Reviewing: ReviewReady,
}

// settles reports whether the Standing decides the Task's Stage. It does in
// the build Stages that wait on the PR, and in an agent Stage where the
// Standing spent a budget and so started no Worker.
func (t *Task) settles() bool {
	switch t.Stage {
	case CIRunning, InReview, ReadyToLand:
		return true
	}
	return t.Stage.Agent() && t.Flag == ReasonBudgetSpent && t.BarFlag
}

// settle moves the Task to where the Standing's first gap leads. A Task that
// is there already does not change, so a repeated pr-seen changes nothing.
func (t *Task) settle() []Effect { return routes[t.Standing.Next()](t) }

var routes = [...]func(*Task) []Effect{
	NoGap:       (*Task).land,
	GapConflict: func(t *Task) []Effect { return t.spend(respondingLoop, t.why()) },
	GapFailed:   func(t *Task) []Effect { return t.spend(ciDebugLoop, t.why()) },
	GapPending:  func(t *Task) []Effect { return t.hold("") },
	GapDraft:    func(t *Task) []Effect { return t.hold(ReasonDraft) },
	GapScoreDue: func(t *Task) []Effect { return t.hold(ReasonNoBotReview) },
	GapScoreLow: (*Task).reviewCycle,
	GapThreads:  (*Task).answer,
	GapApproval: (*Task).await,
}

func (t *Task) why() string { return t.Policy.Bar.Explain(t.Standing) }

// hold keeps the Task in CI running, or moves it there. keep is the PR flag
// that the gap supports. A draft gets its flag. A head that the bot has not
// scored keeps the flag that review-bot-late set. hold clears a PR flag that
// the gap does not support.
func (t *Task) hold(keep Reason) []Effect {
	if t.Stage != CIRunning {
		t.enter(CIRunning)
	}
	switch {
	case keep == ReasonDraft:
		t.flag(ReasonDraft, "")
	case t.Flag == keep:
	case t.Flag == ReasonDraft || t.Flag == ReasonNoBotReview:
		t.flag("", "")
	}
	return nil
}

// answer starts a Responding round for the open review threads. A round for
// bot threads alone, on a score that passes, is a review cycle. Unread
// threads can be people's, so they start a Responding round.
func (t *Task) answer() []Effect {
	if o := t.Standing.Threads; o.People == 0 && !o.Unread && t.Standing.Score != nil {
		return t.reviewCycle()
	}
	return t.spend(respondingLoop, t.why())
}

func (t *Task) await() []Effect {
	if t.Stage == InReview {
		return nil
	}
	t.enter(InReview)
	return []Effect{TrackerStatus{"in_review"}}
}

func (t *Task) land() []Effect {
	if t.Stage == ReadyToLand {
		return nil
	}
	return t.ready()
}

// answered reports whether a Responding round that pushed nothing did its
// work: a poll saw the PR, the bar holds threads, and no thread, conflict, or
// low score is open. The zero bar ignores threads, so its Tasks keep the
// stopped flag.
func (t *Task) answered() bool {
	s := t.Standing
	return t.Stage == Responding && s.Head != "" && t.Policy.Bar.Threads != IgnoredThreads &&
		!s.Threads.Open() && !s.Conflict && !s.ScoreLow
}

// loop is a Loop budget and the agent Stage that spends it.
type loop struct {
	stage Stage
	left  func(*Budgets) *int
	unit  string // one turn of the loop, in a flag detail
}

var (
	ciDebugLoop     = loop{CIDebug, func(b *Budgets) *int { return &b.CIDebug }, "attempt"}
	respondingLoop  = loop{Responding, func(b *Budgets) *int { return &b.Responding }, "round"}
	reviewCycleLoop = loop{Responding, func(b *Budgets) *int { return &b.ReviewCycles }, "cycle"}
)

// spend enters the loop's agent Stage. With budget left it starts a Worker.
// With none left it flags the Task and starts nothing. why is the gap that
// spends the budget, or "" for a review edge. It goes in the flag detail, for
// example "2 open threads (1 bot) after 3 rounds".
func (t *Task) spend(l loop, why string) []Effect {
	left := l.left(&t.Left)
	if *left > 0 {
		*left--
		return t.startAgent(l.stage)
	}
	t.enter(l.stage)
	detail := l.stage.String()
	if why != "" {
		detail = why + " after " + count(*l.left(&t.Policy.Budgets), l.unit)
	}
	t.flag(ReasonBudgetSpent, detail)
	t.BarFlag = why != ""
	return nil
}

// reviewCycle starts a Responding round that spends the review cycle budget,
// not the Responding budget: for a low review bot score, or for open bot
// threads on a score that passes.
func (t *Task) reviewCycle() []Effect {
	effects := t.spend(reviewCycleLoop, t.why())
	score := *t.Standing.Score
	t.BotScore = &score
	return effects
}

func (t *Task) startAgent(stage Stage) []Effect {
	t.enter(stage)
	return []Effect{StartWorker{stage}}
}

// ready enters Ready to land. The Responding budget is per review, so a fresh
// review gets it back.
func (t *Task) ready() []Effect {
	t.enter(ReadyToLand)
	t.Left.Responding = t.Policy.Budgets.Responding
	return nil
}

// finish ends the Task. A review Task has no Tracker to tell.
func (t *Task) finish(o Outcome) []Effect {
	t.enter(Done)
	t.Outcome = o
	if t.Kind == KindReview {
		return nil
	}
	return []Effect{TrackerStatus{string(o)}}
}

// enter changes the Stage, clears the flag and the review cycle's score, and
// refills the per-Stage budget.
func (t *Task) enter(s Stage) {
	t.Stage = s
	t.flag("", "")
	t.BotScore = nil
	t.Left.Restarts = t.Policy.Budgets.Restarts
}

func (t *Task) flag(r Reason, detail string) {
	t.Flag, t.FlagDetail, t.BarFlag = r, detail, false
}
