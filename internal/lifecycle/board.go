package lifecycle

import (
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
	Budgets Budgets `json:"budgets"`
	// Gate is nil for a Task that no review bot gates.
	Gate *ReviewGate `json:"gate,omitempty"`
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

// ReviewCycle is a Responding round that a low review bot score started.
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
	// BotScore is the review bot's score that started this Responding
	// round. It is nil for a round that people's reviews started.
	BotScore *int `json:"bot_score,omitempty"`
	// BotWait is the head commit that the Task waits in CI running for the
	// review bot to score, or "".
	BotWait string `json:"bot_wait,omitempty"`
}

// ReviewCycle is the review cycle that the Task is in, or nil.
func (t *Task) ReviewCycle() *ReviewCycle {
	if t.BotScore == nil || t.Policy.Gate == nil {
		return nil
	}
	return &ReviewCycle{*t.Policy.Gate, *t.BotScore}
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
	Tasks map[string]*Task
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
			if t.Queue != "" && b.QueueStarted(t.Queue) >= b.Slots[t.Queue] {
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
			return nil, notAllowed
		}
		return t.finish(OutcomeClosed), nil

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
			t.flag(ReasonStopped, "")
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

	case CIFailed:
		if t.Stage != CIRunning {
			return nil
		}
		return t.spend(CIDebug, &t.Left.CIDebug)

	case CIPassed:
		if t.Stage != CIRunning {
			return nil
		}
		if s.Draft {
			t.flag(ReasonDraft, "")
			return nil
		}
		if t.Flag == ReasonDraft {
			t.flag("", "")
		}
		if g := t.Policy.Gate; g != nil {
			switch {
			case s.Bot == nil:
				return nil
			case s.Bot.Score == nil:
				t.BotWait = s.Bot.Head
				return nil
			case *s.Bot.Score < g.MinScore:
				return t.reviewCycle(*s.Bot.Score)
			}
		}
		if s.NeedsReview {
			t.enter(InReview)
			return []Effect{TrackerStatus{"in_review"}}
		}
		return t.ready()

	case ReviewApproved:
		if t.Stage == InReview {
			return t.ready()
		}

	case ReviewAdditionalAuthors:
		if t.Stage == InReview {
			return t.spend(Responding, &t.Left.Responding)
		}

	case ReviewCommented, ReviewChangesRequested, PRConflict:
		if t.Stage == InReview || t.Stage == ReadyToLand {
			return t.spend(Responding, &t.Left.Responding)
		}

	case ReviewBotLate:
		if t.Stage == CIRunning && t.BotWait != "" {
			t.flag(ReasonNoBotReview, fmt.Sprintf("%s did not score %s", t.Policy.Gate.Title, t.BotWait[:min(7, len(t.BotWait))]))
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

// spend enters an agent Stage that a Loop budget limits. With budget left it
// starts a Worker. With none left it flags the Task and starts nothing.
func (t *Task) spend(stage Stage, left *int) []Effect {
	if *left == 0 {
		t.enter(stage)
		t.flag(ReasonBudgetSpent, stage.String())
		return nil
	}
	*left--
	return t.startAgent(stage)
}

// reviewCycle starts a Responding round for a low review bot score. It spends
// the review cycle budget, not the Responding budget.
func (t *Task) reviewCycle(score int) []Effect {
	effects := t.spend(Responding, &t.Left.ReviewCycles)
	t.BotScore = &score
	if effects == nil {
		t.flag(ReasonBudgetSpent, fmt.Sprintf("%s after %d cycles", t.Policy.Gate.ScoreText(score), t.Policy.Budgets.ReviewCycles))
	}
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
		return []Effect{Cleanup{}}
	}
	return []Effect{Cleanup{}, TrackerStatus{string(o)}}
}

// enter changes the Stage, clears the flag and the review bot's state, and
// refills the per-Stage budget.
func (t *Task) enter(s Stage) {
	t.Stage = s
	t.flag("", "")
	t.BotScore, t.BotWait = nil, ""
	t.Left.Restarts = t.Policy.Budgets.Restarts
}

func (t *Task) flag(r Reason, detail string) {
	t.Flag, t.FlagDetail = r, detail
}
