package lifecycle

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var policy = Policy{Budgets: Budgets{CIDebug: 3, Responding: 3, Restarts: 2}}

func taskIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t := b.Add("T", policy)
	t.Stage = stage
	return b, t
}

// Polls of a PR that meets the zero bar except for one gap.
var (
	approved    = PRSeen{PRFacts{Head: "h", Decision: DecisionApproved}}
	needsReview = PRSeen{PRFacts{Head: "h", Decision: DecisionRequired}}
	failedCI    = PRSeen{PRFacts{Head: "h", Checks: []Check{{Name: "test", State: CheckFailed}}}}
	pendingCI   = PRSeen{PRFacts{Head: "h", Checks: []Check{{Name: "test", State: CheckPending}}}}
	conflict    = PRSeen{PRFacts{Head: "h", Conflict: true}}
)

func TestTransitions(t *testing.T) {
	cases := []struct {
		name    string
		from    Stage
		signal  Signal
		to      Stage
		effects []Effect
		outcome Outcome
	}{
		{"start", Queued, Start{}, Working, []Effect{TrackerStatus{"started"}, StartWorker{Working}}, ""},
		{"working done with draft PR", Working, WorkerDone{ContractMet: true}, CIRunning, nil, ""},
		{"ci failed", CIRunning, failedCI, CIDebug, []Effect{StartWorker{CIDebug}}, ""},
		{"ci pending", CIRunning, pendingCI, CIRunning, nil, ""},
		{"ci passed needs review", CIRunning, needsReview, InReview, []Effect{TrackerStatus{"in_review"}}, ""},
		{"ci passed approved", CIRunning, approved, ReadyToLand, nil, ""},
		{"ci running conflict", CIRunning, conflict, Responding, []Effect{StartWorker{Responding}}, ""},
		{"ci debug pushed", CIDebug, Pushed{}, CIRunning, nil, ""},
		{"review approved", InReview, approved, ReadyToLand, nil, ""},
		{"review commented", InReview, ReviewCommented{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"review changes requested", InReview, ReviewChangesRequested{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"review additional authors", InReview, ReviewAdditionalAuthors{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"in review conflict", InReview, conflict, Responding, []Effect{StartWorker{Responding}}, ""},
		{"responding pushed", Responding, Pushed{}, CIRunning, nil, ""},
		{"user merge", ReadyToLand, PRMerge{}, ReadyToLand, []Effect{Merge{}}, ""},
		{"ready to land commented", ReadyToLand, ReviewCommented{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"ready to land changes requested", ReadyToLand, ReviewChangesRequested{}, Responding, []Effect{StartWorker{Responding}}, ""},
		{"ready to land conflict", ReadyToLand, conflict, Responding, []Effect{StartWorker{Responding}}, ""},
		{"ready to land with a new check", ReadyToLand, pendingCI, CIRunning, nil, ""},
		{"ready to land with a new review request", ReadyToLand, needsReview, InReview, []Effect{TrackerStatus{"in_review"}}, ""},
		{"merged by another person", Working, PRMerged{}, Done, []Effect{Cleanup{}, TrackerStatus{"merged"}}, OutcomeMerged},
		{"merged from ready to land", ReadyToLand, PRMerged{}, Done, []Effect{Cleanup{}, TrackerStatus{"merged"}}, OutcomeMerged},
		{"closed", InReview, TaskClosed{}, Done, []Effect{Cleanup{}, TrackerStatus{"closed"}}, OutcomeClosed},
		{"closed while queued", Queued, TaskClosed{}, Done, []Effect{Cleanup{}, TrackerStatus{"closed"}}, OutcomeClosed},
		{"rerun ci debug", CIRunning, Rerun{CIDebug}, CIDebug, []Effect{StartWorker{CIDebug}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := taskIn(c.from)
			effects, err := b.Apply("T", c.signal)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if task.Stage != c.to {
				t.Errorf("stage = %s, want %s", task.Stage, c.to)
			}
			if !reflect.DeepEqual(effects, c.effects) {
				t.Errorf("effects = %#v, want %#v", effects, c.effects)
			}
			if task.Outcome != c.outcome {
				t.Errorf("outcome = %q, want %q", task.Outcome, c.outcome)
			}
		})
	}
}

func TestATaskSavesItsStageByKey(t *testing.T) {
	_, task := taskIn(CIDebug)
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"stage":"ci_debug"`) {
		t.Fatalf("saved %s, want the stage key ci_debug", raw)
	}
	var back Task
	if err := json.Unmarshal(raw, &back); err != nil || back.Stage != CIDebug {
		t.Fatalf("loaded stage %s, err %v, want CI debug", back.Stage, err)
	}
	if err := json.Unmarshal([]byte(`{"stage":"plan_review"}`), &back); err == nil {
		t.Fatal("loaded an unknown stage key without an error")
	}
}

// A Task from before the Landing bar kept its review bot gate in policy.gate.
// It loads with the gate and the zero bar, so its old rules stay.
func TestAnOldTaskKeepsItsGateAndTheOldRules(t *testing.T) {
	var old Task
	raw := `{"id":"ENG-1","stage":"ci_running","policy":{"budgets":{"ci_debug":3},"gate":{"bot":"greptile","title":"Greptile","min_score":4,"top_score":5}}}`
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		t.Fatal(err)
	}
	want := LandingBar{Bot: &ReviewGate{Bot: "greptile", Title: "Greptile", MinScore: 4, TopScore: 5}}
	if !reflect.DeepEqual(old.Policy.Bar, want) || old.Policy.Budgets.CIDebug != 3 {
		t.Fatalf("policy %+v, want budgets kept and bar %+v", old.Policy, want)
	}
	saved, _ := json.Marshal(old)
	var back Task
	if err := json.Unmarshal(saved, &back); err != nil || !reflect.DeepEqual(back.Policy, old.Policy) {
		t.Fatalf("saved %s, loaded %+v, %v", saved, back.Policy, err)
	}
}

func TestWIPCap(t *testing.T) {
	b := NewBoard(1)
	b.Add("A", policy)
	second := b.Add("B", policy)
	if _, err := b.Apply("A", Start{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Apply("B", Start{}); !errors.Is(err, ErrWIPCapFull) {
		t.Fatalf("start over cap: err = %v, want ErrWIPCapFull", err)
	}
	if second.Stage != Queued {
		t.Fatalf("refused start moved the Task to %s", second.Stage)
	}
	if _, err := b.Apply("B", Start{OverCap: true}); err != nil {
		t.Fatal(err)
	}
	if !second.OverCap || b.OverCap() != 1 {
		t.Fatalf("over-cap start not recorded: task %v, board %d", second.OverCap, b.OverCap())
	}
}

func TestStartUnderCapIsNotRecordedAsOverCap(t *testing.T) {
	b := NewBoard(2)
	task := b.Add("A", policy)
	if _, err := b.Apply("A", Start{OverCap: true}); err != nil {
		t.Fatal(err)
	}
	if task.OverCap {
		t.Fatal("a start with room under the cap was recorded as over the cap")
	}
}

func TestUserSignalsOutsideTheirStageFail(t *testing.T) {
	cases := []struct {
		stage  Stage
		signal Signal
	}{
		{InReview, PRMerge{}},
		{Working, Start{}},
		{Done, TaskClosed{}},
		{Queued, Rerun{Working}},
		{Working, Rerun{InReview}},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		_, err := b.Apply("T", c.signal)
		var na *NotAllowedError
		if !errors.As(err, &na) {
			t.Errorf("%s in %s: err = %v, want NotAllowedError", c.signal.Name(), c.stage, err)
		}
		if task.Stage != c.stage {
			t.Errorf("%s in %s moved the Task to %s", c.signal.Name(), c.stage, task.Stage)
		}
	}
}

func TestObservedSignalsOutsideTheirStageChangeNothing(t *testing.T) {
	cases := []struct {
		stage  Stage
		signal Signal
	}{
		{Queued, failedCI},
		{Working, approved},
		{Working, Pushed{}},
		{CIDebug, failedCI},
		{Responding, approved},
		{InReview, Pushed{}},
		{CIRunning, ReviewCommented{}},
		{ReadyToLand, ReviewAdditionalAuthors{}},
		{Done, PRMerged{}},
		{ReadyToLand, WorkerDone{ContractMet: true}},
		{CIRunning, WorkerLost{}},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		effects, err := b.Apply("T", c.signal)
		if err != nil || effects != nil || task.Stage != c.stage || task.Flag != "" {
			t.Errorf("%s in %s: stage %s, flag %q, effects %v, err %v", c.signal.Name(), c.stage, task.Stage, task.Flag, effects, err)
		}
	}
}

func TestNeedsYouReasons(t *testing.T) {
	cases := []struct {
		name   string
		stage  Stage
		signal Signal
		reason Reason
	}{
		{"permission", Working, WorkerStatus{State: "blocked", Kind: "permission", Detail: "Bash(make deploy)"}, ReasonPermission},
		{"question", Working, WorkerStatus{State: "blocked", Kind: "question"}, ReasonQuestion},
		{"auth", Responding, WorkerStatus{State: "blocked", Kind: "auth"}, ReasonAuth},
		{"unknown kind", Working, WorkerStatus{State: "blocked", Kind: "telepathy"}, ReasonQuestion},
		{"status error", CIDebug, WorkerStatus{State: "error"}, ReasonError},
		{"exit code", Working, WorkerExited{Code: 1}, ReasonError},
		{"setup failed", Working, SetupFailed{"pre-start hook"}, ReasonSetupFailed},
		{"stopped", Working, WorkerDone{ContractMet: false}, ReasonStopped},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		if _, err := b.Apply("T", c.signal); err != nil {
			t.Fatal(err)
		}
		if task.Flag != c.reason || task.Group() != GroupNeedsYou {
			t.Errorf("%s: flag %q group %s, want %q in Needs you", c.name, task.Flag, task.Group(), c.reason)
		}
		if task.Stage != c.stage {
			t.Errorf("%s: stage moved to %s", c.name, task.Stage)
		}
	}
}

func TestWorkingStatusClearsOnlyWorkerFlags(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerStatus{State: "blocked", Kind: "permission"})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" {
		t.Fatalf("working did not clear %q", task.Flag)
	}
	b.Apply("T", WorkerDone{ContractMet: false})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" {
		t.Fatalf("the user's reply did not clear %q", task.Flag)
	}
	b.Apply("T", SetupFailed{"pre-start hook"})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != ReasonSetupFailed {
		t.Fatalf("working cleared %q, want setup failed kept", task.Flag)
	}
}

// A Worker that asked the user a question and then stopped still asks it.
func TestAStopKeepsTheQuestion(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerStatus{State: "blocked", Kind: "question", Detail: "dual write first?"})
	b.Apply("T", WorkerDone{ContractMet: false})
	if task.Flag != ReasonQuestion || task.FlagDetail != "dual write first?" {
		t.Fatalf("flag %q detail %q, want the question kept", task.Flag, task.FlagDetail)
	}
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" || task.FlagDetail != "" {
		t.Fatalf("the user's reply left flag %q detail %q", task.Flag, task.FlagDetail)
	}
}

func TestExitZeroIsNotAnError(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerExited{Code: 0})
	if task.Flag != "" {
		t.Fatalf("exit 0 set flag %q", task.Flag)
	}
}

func TestPROpenAfterUnmetContractMovesOn(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", PROpen{})
	if task.Stage != Working {
		t.Fatalf("pr-open before done moved the Task to %s", task.Stage)
	}
	b.Apply("T", WorkerDone{ContractMet: false})
	b.Apply("T", PROpen{})
	if task.Stage != CIRunning || task.Flag != "" {
		t.Fatalf("stage %s flag %q, want CI running with no flag", task.Stage, task.Flag)
	}
}

// lineup never marks a PR ready, so a passing draft waits for the user to.
func TestADraftWaitsForTheUserToMarkItReady(t *testing.T) {
	b, task := taskIn(CIRunning)
	effects, _ := b.Apply("T", PRSeen{PRFacts{Head: "h", Draft: true, Decision: DecisionRequired}})
	if effects != nil || task.Stage != CIRunning || task.Flag != ReasonDraft || task.Group() != GroupNeedsYou {
		t.Fatalf("draft: stage %s flag %q effects %v, want CI running flagged draft and no effect", task.Stage, task.Flag, effects)
	}
	b.Apply("T", needsReview)
	if task.Stage != InReview || task.Flag != "" {
		t.Fatalf("marked ready: stage %s flag %q, want In review with no flag", task.Stage, task.Flag)
	}
}

func TestCIDebugBudget(t *testing.T) {
	b, task := taskIn(CIRunning)
	for i := range 3 {
		effects, _ := b.Apply("T", failedCI)
		if task.Stage != CIDebug || len(effects) != 1 {
			t.Fatalf("attempt %d: stage %s effects %v", i+1, task.Stage, effects)
		}
		b.Apply("T", Pushed{})
	}
	effects, _ := b.Apply("T", failedCI)
	if effects != nil || task.Flag != ReasonBudgetSpent || task.Stage != CIDebug || task.FlagDetail != "test failed after 3 attempts" {
		t.Fatalf("fourth failure: stage %s flag %q %q effects %v", task.Stage, task.Flag, task.FlagDetail, effects)
	}
	b.Apply("T", approved)
	if task.Stage != ReadyToLand || task.Flag != "" {
		t.Fatalf("checks fixed by hand: stage %s flag %q, want Ready to land", task.Stage, task.Flag)
	}
}

func TestRespondingBudgetIsPerReview(t *testing.T) {
	b, task := taskIn(InReview)
	for range 3 {
		b.Apply("T", ReviewCommented{})
		b.Apply("T", Pushed{})
		b.Apply("T", needsReview)
	}
	b.Apply("T", ReviewCommented{})
	if task.Flag != ReasonBudgetSpent || task.FlagDetail != "Responding" {
		t.Fatalf("fourth round: flag %q %q, want budget spent", task.Flag, task.FlagDetail)
	}

	b, task = taskIn(InReview)
	for range 3 {
		b.Apply("T", ReviewCommented{})
		b.Apply("T", Pushed{})
		b.Apply("T", needsReview)
	}
	b.Apply("T", approved)
	b.Apply("T", ReviewCommented{})
	if task.Stage != Responding || task.Flag != "" {
		t.Fatalf("a new review after approval: stage %s flag %q, want Responding with budget", task.Stage, task.Flag)
	}
}

// A review edge leaves no gap in the Standing, so no poll can clear its flag.
func TestABudgetSpentOnAReviewEdgeStaysUntilTheUserActs(t *testing.T) {
	b, task := taskIn(InReview)
	task.Left.Responding = 0
	b.Apply("T", ReviewCommented{})
	b.Apply("T", approved)
	if task.Stage != Responding || task.Flag != ReasonBudgetSpent {
		t.Fatalf("stage %s flag %q, want Responding with budget spent", task.Stage, task.Flag)
	}
}

var userPolicy = Policy{Budgets: Budgets{CIDebug: 3, Responding: 3, Restarts: 2, ReviewCycles: 1}, Bar: userBar}

func userTaskIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t := b.Add("T", userPolicy)
	t.Stage = stage
	return b, t
}

// threads is an approved PR with passing checks and the given threads.
func threads(score *int, ts ...Thread) PRSeen {
	return PRSeen{PRFacts{Head: "h", Score: score, Decision: DecisionApproved, Threads: ts}}
}

func TestAnOpenThreadOnAnApprovedPRStartsResponding(t *testing.T) {
	for _, from := range []Stage{CIRunning, InReview, ReadyToLand} {
		b, task := userTaskIn(from)
		effects, _ := b.Apply("T", threads(nil, Thread{Resolved: true}))
		if task.Stage != Responding || task.Flag != "" || !reflect.DeepEqual(effects, []Effect{StartWorker{Responding}}) {
			t.Errorf("from %s: stage %s flag %q effects %v, want a Responding round", from, task.Stage, task.Flag, effects)
		}
		if task.Left.Responding != 2 || task.ReviewCycle() != nil {
			t.Errorf("from %s: left %+v cycle %+v, want one Responding round spent", from, task.Left, task.ReviewCycle())
		}
	}
}

func TestASpentRespondingBudgetNamesTheOpenThreads(t *testing.T) {
	b, task := userTaskIn(CIRunning)
	open := threads(nil, Thread{Bot: true}, Thread{})
	for i := range 3 {
		if effects, _ := b.Apply("T", open); task.Stage != Responding || len(effects) != 1 {
			t.Fatalf("round %d: stage %s effects %v", i+1, task.Stage, effects)
		}
		b.Apply("T", Pushed{})
	}
	effects, _ := b.Apply("T", open)
	if effects != nil || task.Stage != Responding || task.Flag != ReasonBudgetSpent || task.FlagDetail != "2 open threads (1 bot) after 3 rounds" {
		t.Fatalf("fourth round: stage %s flag %q %q effects %v", task.Stage, task.Flag, task.FlagDetail, effects)
	}
	b.Apply("T", threads(nil, Thread{Bot: true}))
	if task.FlagDetail != "1 open thread (1 bot) after 3 rounds" {
		t.Fatalf("one thread resolved by hand: flag %q %q", task.Flag, task.FlagDetail)
	}
	b.Apply("T", threads(nil, Thread{Bot: true, Resolved: true, Replied: true}))
	if task.Stage != ReadyToLand || task.Flag != "" {
		t.Fatalf("every thread done: stage %s flag %q, want Ready to land", task.Stage, task.Flag)
	}
}

// ADR 0013 keeps the bot's rounds apart from people's rounds, so open bot
// threads on a score that passes spend a review cycle.
func TestBotThreadsOnAPassingScoreSpendAReviewCycle(t *testing.T) {
	five := 5
	p := userPolicy
	p.Bar.Bot = greptile(4)
	b := NewBoard(5)
	task := b.Add("T", p)
	task.Stage = CIRunning
	b.Apply("T", threads(&five, Thread{Bot: true}))
	if task.Stage != Responding || task.Left.ReviewCycles != 0 || task.Left.Responding != 3 || task.BotScore == nil {
		t.Fatalf("stage %s left %+v, want one review cycle spent", task.Stage, task.Left)
	}
	b.Apply("T", Pushed{})
	b.Apply("T", threads(&five, Thread{Bot: true}))
	if task.Flag != ReasonBudgetSpent || task.FlagDetail != "1 open thread (1 bot) after 1 cycle" {
		t.Fatalf("second round: flag %q %q", task.Flag, task.FlagDetail)
	}
	b.Apply("T", threads(&five, Thread{Bot: true}, Thread{}))
	if task.Stage != Responding || task.Flag != "" || task.Left.Responding != 2 {
		t.Fatalf("a person's thread: stage %s flag %q left %+v, want a Responding round", task.Stage, task.Flag, task.Left)
	}
}

// Unread threads can be people's, so they start a Responding round, also on a
// passing score.
func TestUnreadThreadsStartARespondingRound(t *testing.T) {
	five := 5
	p := userPolicy
	p.Bar.Bot = greptile(4)
	b := NewBoard(5)
	task := b.Add("T", p)
	task.Stage = CIRunning
	over := threads(&five, Thread{Bot: true})
	over.Facts.ThreadsOver = true
	b.Apply("T", over)
	if task.Stage != Responding || task.Left.Responding != 2 || task.ReviewCycle() != nil {
		t.Fatalf("stage %s left %+v cycle %+v, want one Responding round spent", task.Stage, task.Left, task.ReviewCycle())
	}
}

// A Worker can answer each thread with no code change, for example "won't fix".
func TestARoundThatAnswersEveryThreadNeedsNoPush(t *testing.T) {
	b, task := userTaskIn(CIRunning)
	b.Apply("T", threads(nil, Thread{}))
	b.Apply("T", threads(nil, Thread{Resolved: true, Replied: true}))
	b.Apply("T", WorkerDone{ContractMet: false})
	if task.Stage != CIRunning || task.Flag != "" {
		t.Fatalf("every thread answered: stage %s flag %q, want CI running", task.Stage, task.Flag)
	}

	b, task = userTaskIn(CIRunning)
	b.Apply("T", threads(nil, Thread{}))
	b.Apply("T", WorkerDone{ContractMet: false})
	if task.Stage != Responding || task.Flag != ReasonStopped {
		t.Fatalf("a thread still open: stage %s flag %q, want stopped", task.Stage, task.Flag)
	}

	b, task = taskIn(InReview)
	b.Apply("T", ReviewCommented{})
	b.Apply("T", approved)
	b.Apply("T", WorkerDone{ContractMet: false})
	if task.Flag != ReasonStopped {
		t.Fatalf("the zero bar: flag %q, want stopped", task.Flag)
	}
}

func TestWorkerRestartBudgetIsPerStage(t *testing.T) {
	b, task := taskIn(Working)
	task.Left.Restarts = 2
	for i := range 2 {
		effects, _ := b.Apply("T", WorkerLost{})
		if !reflect.DeepEqual(effects, []Effect{StartWorker{Working}}) {
			t.Fatalf("restart %d: effects %v", i+1, effects)
		}
	}
	effects, _ := b.Apply("T", WorkerLost{})
	if effects != nil || task.Flag != ReasonWorkerLost {
		t.Fatalf("third loss: flag %q effects %v", task.Flag, effects)
	}
	b.Apply("T", PROpen{})
	b.Apply("T", Rerun{Working})
	if effects, _ := b.Apply("T", WorkerLost{}); len(effects) != 1 {
		t.Fatalf("a new Stage did not refill the restart budget: %v", effects)
	}
}

var gated = Policy{
	Budgets: Budgets{CIDebug: 3, Responding: 3, Restarts: 2, ReviewCycles: 2},
	Bar:     LandingBar{Bot: greptile(5)},
}

func gatedIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t := b.Add("T", gated)
	t.Stage = stage
	return b, t
}

// scored is a poll of a PR at head with passing checks. The bot scored head
// n, or has not scored it when n is negative.
func scored(head string, n int, d Decision) PRSeen {
	f := PRFacts{Head: head, Decision: d}
	if n >= 0 {
		f.Score = &n
	}
	return PRSeen{f}
}

func TestReviewGate(t *testing.T) {
	four := 4
	cases := []struct {
		name    string
		signal  Signal
		to      Stage
		effects []Effect
		flag    Reason
	}{
		{"top score goes to review", scored("abc", 5, DecisionRequired), InReview, []Effect{TrackerStatus{"in_review"}}, ""},
		{"top score with no reviewer is ready", scored("abc", 5, DecisionNone), ReadyToLand, nil, ""},
		{"low score starts a review cycle", scored("abc", 4, DecisionRequired), Responding, []Effect{StartWorker{Responding}}, ""},
		{"no score yet waits", scored("abc", -1, DecisionRequired), CIRunning, nil, ""},
		{"pending checks wait first", PRSeen{PRFacts{Head: "abc", Score: &four, Checks: []Check{{Name: "test"}}}}, CIRunning, nil, ""},
		{"a draft waits for the user first", PRSeen{PRFacts{Head: "abc", Draft: true, Score: &four}}, CIRunning, nil, ReasonDraft},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := gatedIn(CIRunning)
			effects, err := b.Apply("T", c.signal)
			if err != nil {
				t.Fatal(err)
			}
			if task.Stage != c.to || task.Flag != c.flag || !reflect.DeepEqual(effects, c.effects) {
				t.Errorf("stage %s flag %q effects %#v, want %s %q %#v", task.Stage, task.Flag, effects, c.to, c.flag, c.effects)
			}
		})
	}
}

func TestADraftMarkedReadyDropsItsFlagWhileTheBotReviews(t *testing.T) {
	b, task := gatedIn(CIRunning)
	b.Apply("T", PRSeen{PRFacts{Head: "abc", Draft: true}})
	b.Apply("T", scored("abc", -1, DecisionNone))
	if task.Stage != CIRunning || task.Flag != "" || task.Policy.Bar.Explain(task.Standing) != "waiting for Greptile" {
		t.Fatalf("stage %s flag %q standing %+v, want CI running with no flag, waiting for Greptile", task.Stage, task.Flag, task.Standing)
	}
}

func TestReviewCyclesSpendTheirOwnBudget(t *testing.T) {
	b, task := gatedIn(CIRunning)
	for i := range 2 {
		effects, _ := b.Apply("T", scored("abc", 4, DecisionNone))
		if !reflect.DeepEqual(effects, []Effect{StartWorker{Responding}}) || task.Stage != Responding {
			t.Fatalf("round %d: stage %s effects %v", i+1, task.Stage, effects)
		}
		if r := task.ReviewCycle(); r == nil || r.Score != 4 || r.MinScore != 5 {
			t.Fatalf("round %d: review cycle %+v, want Greptile 4 of minimum 5", i+1, r)
		}
		b.Apply("T", Pushed{})
	}
	if task.Left.Responding != 3 {
		t.Fatalf("review cycles spent the Responding budget: %d left of 3", task.Left.Responding)
	}
	effects, _ := b.Apply("T", scored("abc", 4, DecisionNone))
	if effects != nil || task.Stage != Responding || task.Flag != ReasonBudgetSpent || task.FlagDetail != "Greptile 4/5 after 2 cycles" {
		t.Fatalf("third low score: stage %s flag %q %q effects %v", task.Stage, task.Flag, task.FlagDetail, effects)
	}
}

func TestAPeopleRoundIsNotAReviewCycle(t *testing.T) {
	b, task := gatedIn(CIRunning)
	b.Apply("T", scored("abc", 4, DecisionNone))
	b.Apply("T", Pushed{})
	b.Apply("T", scored("def", 5, DecisionRequired))
	b.Apply("T", ReviewCommented{})
	if task.Stage != Responding || task.ReviewCycle() != nil {
		t.Fatalf("stage %s review cycle %+v, want a Responding round that people started", task.Stage, task.ReviewCycle())
	}
	if task.Left.Responding != 2 || task.Left.ReviewCycles != 1 {
		t.Fatalf("left %+v, want one Responding round and one bot cycle spent", task.Left)
	}
}

// A gated Task must not wait in CI running forever for a bot that never
// reviews, for example in a Repo where the bot is not installed.
func TestALateBotFlagsTheTask(t *testing.T) {
	b, task := gatedIn(CIRunning)
	b.Apply("T", ReviewBotLate{})
	if task.Flag != "" {
		t.Fatalf("late before CI passed: flag %q", task.Flag)
	}
	const head = "3591eba3593b066b117ac68de1d41f1a16f527dd"
	b.Apply("T", scored(head, -1, DecisionNone))
	b.Apply("T", ReviewBotLate{})
	if task.Stage != CIRunning || task.Flag != ReasonNoBotReview || task.FlagDetail != "Greptile did not score 3591eba" {
		t.Fatalf("late: stage %s flag %q %q", task.Stage, task.Flag, task.FlagDetail)
	}
	b.Apply("T", scored(head, -1, DecisionNone))
	if task.Flag != ReasonNoBotReview {
		t.Fatalf("the next poll with no score: flag %q, want no bot review kept", task.Flag)
	}
	b.Apply("T", scored(head, 5, DecisionNone))
	if task.Stage != ReadyToLand || task.Flag != "" {
		t.Fatalf("score after the flag: stage %s flag %q, want Ready to land", task.Stage, task.Flag)
	}

	b, task = taskIn(CIRunning)
	b.Apply("T", needsReview)
	b.Apply("T", ReviewBotLate{})
	if task.Stage != InReview || task.Flag != "" {
		t.Fatalf("a Task with no gate: stage %s flag %q", task.Stage, task.Flag)
	}
}
