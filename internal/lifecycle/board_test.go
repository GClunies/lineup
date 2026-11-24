package lifecycle

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var policy = Policy{Budgets: Budgets{CIDebug: 3, Responding: 3}}

func taskIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t, _, _ := b.Add("T", policy, false)
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
		{"working done with draft PR", Working, WorkerDone{ContractMet: true}, CIRunning, nil, ""},
		{"ci failed", CIRunning, failedCI, CIDebug, []Effect{PromptWorker{CIDebug}}, ""},
		{"ci pending", CIRunning, pendingCI, CIRunning, nil, ""},
		{"ci passed needs review", CIRunning, needsReview, InReview, []Effect{TrackerStatus{"in_review"}}, ""},
		{"ci passed approved", CIRunning, approved, ReadyToLand, nil, ""},
		{"ci running conflict", CIRunning, conflict, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"ci debug pushed", CIDebug, Pushed{}, CIRunning, nil, ""},
		{"review approved", InReview, approved, ReadyToLand, nil, ""},
		{"review commented", InReview, ReviewCommented{}, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"review changes requested", InReview, ReviewChangesRequested{}, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"review additional authors", InReview, ReviewAdditionalAuthors{}, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"in review conflict", InReview, conflict, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"responding pushed", Responding, Pushed{}, CIRunning, nil, ""},
		{"user merge", ReadyToLand, PRMerge{}, ReadyToLand, []Effect{Merge{}}, ""},
		{"ready to land commented", ReadyToLand, ReviewCommented{}, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"ready to land changes requested", ReadyToLand, ReviewChangesRequested{}, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"ready to land conflict", ReadyToLand, conflict, Responding, []Effect{PromptWorker{Responding}}, ""},
		{"ready to land with a new check", ReadyToLand, pendingCI, CIRunning, nil, ""},
		{"ready to land with a new review request", ReadyToLand, needsReview, InReview, []Effect{TrackerStatus{"in_review"}}, ""},
		{"merged by another person", Working, PRMerged{}, Done, []Effect{TrackerStatus{"merged"}, StartQA{}}, OutcomeMerged},
		{"merged from ready to land", ReadyToLand, PRMerged{}, Done, []Effect{TrackerStatus{"merged"}, StartQA{}}, OutcomeMerged},
		{"closed", InReview, TaskClosed{}, Done, []Effect{Cleanup{}, TrackerStatus{"closed"}}, OutcomeClosed},
		{"a skip closes a build Task as a close does", InReview, TaskClosed{Skipped: true}, Done, []Effect{Cleanup{}, TrackerStatus{"closed"}}, OutcomeClosed},
		{"rerun ci debug", CIRunning, Rerun{CIDebug}, CIDebug, []Effect{PromptWorker{CIDebug}}, ""},
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

func TestClosingADoneTaskCleansUpOnly(t *testing.T) {
	for _, o := range []Outcome{OutcomeMerged, OutcomeClosed, OutcomeSkipped} {
		b, task := taskIn(Done)
		task.Outcome = o
		effects, err := b.Apply("T", TaskClosed{})
		if err != nil {
			t.Fatalf("%s: Apply: %v", o, err)
		}
		if !reflect.DeepEqual(effects, []Effect{Cleanup{}}) {
			t.Errorf("%s: effects = %#v, want only Cleanup", o, effects)
		}
		if task.Stage != Done || task.Outcome != o {
			t.Errorf("%s: stage %s outcome %q, want Done with the same outcome", o, task.Stage, task.Outcome)
		}
	}
}

func TestTabClosedCleansUpADoneTask(t *testing.T) {
	b, task := taskIn(Done)
	task.Outcome = OutcomeMerged
	effects, err := b.Apply("T", TabClosed{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(effects, []Effect{Cleanup{}}) {
		t.Errorf("effects = %#v, want only Cleanup", effects)
	}
	if task.Stage != Done || task.Outcome != OutcomeMerged {
		t.Errorf("stage %s outcome %q, want Done, merged", task.Stage, task.Outcome)
	}
}

func TestTabClosedChangesNothingInAStartedStage(t *testing.T) {
	for _, stage := range []Stage{Working, CIRunning, CIDebug, InReview, Responding, ReadyToLand} {
		b, task := taskIn(stage)
		effects, err := b.Apply("T", TabClosed{})
		if err != nil || len(effects) != 0 || task.Stage != stage {
			t.Errorf("%s: effects %#v, err %v, stage %s, want no change", stage, effects, err, task.Stage)
		}
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

func TestAnAddStartsABuildTaskInWorking(t *testing.T) {
	b := NewBoard(5)
	task, effects, err := b.Add("T", policy, false)
	if err != nil {
		t.Fatal(err)
	}
	if task.Stage != Working || task.Kind != KindBuild || b.Tasks["T"] != task || b.Started() != 1 {
		t.Fatalf("task %+v, %d started, want a started build Task in Working on the Board", task, b.Started())
	}
	if want := []Effect{TrackerStatus{"started"}, PromptWorker{Working}}; !reflect.DeepEqual(effects, want) {
		t.Fatalf("effects = %#v, want %#v", effects, want)
	}
}

func TestWIPCap(t *testing.T) {
	b := NewBoard(1)
	b.Add("A", policy, false)
	if task, effects, err := b.Add("B", policy, false); !errors.Is(err, ErrWIPCapFull) || task != nil || effects != nil {
		t.Fatalf("add over cap: task %v, effects %v, err %v, want ErrWIPCapFull", task, effects, err)
	}
	if _, ok := b.Tasks["B"]; ok || b.Started() != 1 {
		t.Fatalf("the refused add put B on the Board: %d started", b.Started())
	}
	second, _, err := b.Add("B", policy, true)
	if err != nil {
		t.Fatal(err)
	}
	if second.Stage != Working || !second.OverCap || b.OverCap() != 1 {
		t.Fatalf("over-cap add: stage %s, over cap %v, board %d", second.Stage, second.OverCap, b.OverCap())
	}
}

func TestAnAddUnderCapIsNotRecordedAsOverCap(t *testing.T) {
	b := NewBoard(2)
	task, _, err := b.Add("A", policy, true)
	if err != nil {
		t.Fatal(err)
	}
	if task.OverCap {
		t.Fatal("an add with room under the cap was recorded as over the cap")
	}
}

func TestUserSignalsOutsideTheirStageFail(t *testing.T) {
	cases := []struct {
		stage  Stage
		signal Signal
	}{
		{InReview, PRMerge{}},
		{Done, Rerun{Working}},
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
		{Working, approved},
		{Working, Pushed{}},
		{CIDebug, failedCI},
		{Responding, approved},
		{InReview, Pushed{}},
		{CIRunning, ReviewCommented{}},
		{ReadyToLand, ReviewAdditionalAuthors{}},
		{Done, PRMerged{}},
		{ReadyToLand, WorkerDone{ContractMet: true}},
		{CIRunning, TabClosed{}},
		{ReadyToLand, TabClosed{Waits: true}},
		{Working, TabOpened{}},
	}
	for _, c := range cases {
		b, task := taskIn(c.stage)
		effects, err := b.Apply("T", c.signal)
		if err != nil || effects != nil || task.Stage != c.stage || task.Flag != "" {
			t.Errorf("%s in %s: stage %s, flag %q, effects %v, err %v", c.signal.Name(), c.stage, task.Stage, task.Flag, effects, err)
		}
	}
}

func TestAMissingContractLeavesTheTabClosedFlag(t *testing.T) {
	b, task := taskIn(Working)
	for _, s := range []Signal{TabClosed{}, WorkerDone{ContractMet: false}} {
		if _, err := b.Apply("T", s); err != nil {
			t.Fatal(err)
		}
	}
	if task.Flag != ReasonTabClosed {
		t.Fatalf("flag = %q, want %q: the Worker is gone, so it can't have stopped", task.Flag, ReasonTabClosed)
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
	b.Apply("T", WorkerDone{ContractMet: false, Detail: "Done for now."})
	if task.Flag != ReasonQuestion || task.FlagDetail != "dual write first?" {
		t.Fatalf("flag %q detail %q, want the question kept", task.Flag, task.FlagDetail)
	}
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" || task.FlagDetail != "" {
		t.Fatalf("the user's reply left flag %q detail %q", task.Flag, task.FlagDetail)
	}
}

// A Responding Worker that waits for the user to decide its Draft replies
// keeps the Task in Responding and in Needs you until the user answers.
func TestDraftRepliesWaitInRespondingForTheUser(t *testing.T) {
	b, task := taskIn(Responding)
	b.Apply("T", WorkerStatus{State: "blocked", Kind: "drafts", Detail: "2 replies to approve"})
	b.Apply("T", WorkerDone{ContractMet: false, Detail: "Waiting for your decisions."})
	if task.Stage != Responding || task.Flag != ReasonDrafts || task.FlagDetail != "2 replies to approve" || task.Group() != GroupNeedsYou {
		t.Fatalf("stage %s flag %q detail %q group %s, want Responding with the drafts kept", task.Stage, task.Flag, task.FlagDetail, task.Group())
	}
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" || task.FlagDetail != "" {
		t.Fatalf("the user's answer left flag %q detail %q", task.Flag, task.FlagDetail)
	}
	b.Apply("T", Pushed{})
	if task.Stage != CIRunning {
		t.Fatalf("stage %s after the push, want CI running", task.Stage)
	}
}

func TestAStopCarriesTheWorkerLastLine(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerDone{ContractMet: false, Detail: `schema_version: "1.0" or "1.0.0"?`})
	if task.Flag != ReasonStopped || task.FlagDetail != `schema_version: "1.0" or "1.0.0"?` {
		t.Fatalf("flag %q detail %q, want stopped with the last line", task.Flag, task.FlagDetail)
	}
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != "" || task.FlagDetail != "" {
		t.Fatalf("the user's reply left flag %q detail %q", task.Flag, task.FlagDetail)
	}
}

func TestAStopWithNoMessageIsBareStopped(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerDone{ContractMet: false})
	if task.Flag != ReasonStopped || task.FlagDetail != "" {
		t.Fatalf("flag %q detail %q, want a bare stopped", task.Flag, task.FlagDetail)
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

var userPolicy = Policy{Budgets: Budgets{CIDebug: 3, Responding: 3, ReviewCycles: 1}, Bar: userBar}

func userTaskIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t, _, _ := b.Add("T", userPolicy, false)
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
		if task.Stage != Responding || task.Flag != "" || !reflect.DeepEqual(effects, []Effect{PromptWorker{Responding}}) {
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

// Open bot threads on a score that passes spend a review cycle, so the bot's
// rounds stay apart from people's rounds (PROD-LAND-013).
func TestBotThreadsOnAPassingScoreSpendAReviewCycle(t *testing.T) {
	five := 5
	p := userPolicy
	p.Bar.Bot = greptile(4)
	b := NewBoard(5)
	task, _, _ := b.Add("T", p, false)
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
	task, _, _ := b.Add("T", p, false)
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

// comments is an approved PR with passing checks and people's comments
// outside threads, at the given minutes past noon.
func comments(minutes ...int) PRSeen {
	f := PRFacts{Head: "h", Decision: DecisionApproved}
	for _, m := range minutes {
		f.Feedback = append(f.Feedback, time.Date(2026, 11, 21, 12, m, 0, 0, time.UTC))
	}
	return PRSeen{f}
}

// A comment outside a thread starts one Responding round, in each Stage that
// waits on the PR. The round takes the comment, so the next poll lands.
func TestANewCommentStartsOneRespondingRound(t *testing.T) {
	for _, from := range []Stage{CIRunning, InReview, ReadyToLand} {
		b, task := userTaskIn(from)
		effects, _ := b.Apply("T", comments(1))
		if task.Stage != Responding || !reflect.DeepEqual(effects, []Effect{PromptWorker{Responding}}) || task.Left.Responding != 2 {
			t.Fatalf("from %s: stage %s effects %v left %+v, want a Responding round", from, task.Stage, effects, task.Left)
		}
		if task.Take || task.Standing.Comments != 0 {
			t.Fatalf("from %s: take %v comments %d, want the round to take the comment", from, task.Take, task.Standing.Comments)
		}
		b.Apply("T", Pushed{})
		if effects, _ := b.Apply("T", comments(1)); task.Stage != ReadyToLand || effects != nil {
			t.Fatalf("from %s: after the round: stage %s effects %v, want Ready to land", from, task.Stage, effects)
		}
		if effects, _ := b.Apply("T", comments(1, 2)); task.Stage != Responding || len(effects) != 1 || task.Left.Responding != 2 {
			t.Fatalf("from %s: a second comment: stage %s effects %v left %+v, want a new round", from, task.Stage, effects, task.Left)
		}
	}
}

// A review edge starts its round before the poll's pr-seen, so the round
// takes the comments that the same poll's pr-seen holds.
func TestARoundThatAReviewStartedTakesThePollsComments(t *testing.T) {
	b, task := userTaskIn(InReview)
	b.Apply("T", ReviewCommented{})
	if !task.Take {
		t.Fatal("a round started with no take")
	}
	b.Apply("T", comments(1))
	b.Apply("T", Pushed{})
	if effects, _ := b.Apply("T", comments(1)); task.Stage != ReadyToLand || effects != nil {
		t.Fatalf("stage %s effects %v, want Ready to land after one round", task.Stage, effects)
	}
}

// A rerun of Responding is a round for people, so it takes the comments that
// the budget left new. CI debug takes none.
func TestARerunOfRespondingTakesTheNewComments(t *testing.T) {
	b, task := userTaskIn(CIRunning)
	task.Left.Responding = 0
	b.Apply("T", comments(1))
	if task.Flag != ReasonBudgetSpent {
		t.Fatalf("flag %q, want budget spent", task.Flag)
	}
	b.Apply("T", Rerun{Stage: CIDebug})
	if task.Take {
		t.Fatal("a rerun of CI debug takes the comments")
	}
	b.Apply("T", Rerun{Stage: Responding})
	b.Apply("T", comments(1))
	b.Apply("T", Pushed{})
	if effects, _ := b.Apply("T", comments(1)); task.Stage != ReadyToLand || effects != nil {
		t.Fatalf("after the rerun: stage %s effects %v, want Ready to land", task.Stage, effects)
	}
}

// A Worker can answer a comment with no change, for example a thanks needs none.
func TestARoundThatAnswersEveryCommentNeedsNoPush(t *testing.T) {
	b, task := userTaskIn(CIRunning)
	b.Apply("T", comments(1))
	b.Apply("T", comments(1))
	b.Apply("T", WorkerDone{ContractMet: false})
	if task.Stage != CIRunning || task.Flag != "" {
		t.Fatalf("stage %s flag %q, want CI running", task.Stage, task.Flag)
	}
}

// A review cycle's prompt names only the bot's review, so a person's comment
// waits for a round of its own.
func TestAReviewCycleLeavesPeoplesCommentsNew(t *testing.T) {
	four, five := 4, 5
	p := userPolicy
	p.Bar.Bot = greptile(5)
	b := NewBoard(5)
	task, _, _ := b.Add("T", p, false)
	task.Stage = CIRunning
	low := comments(1)
	low.Facts.Score = &four
	b.Apply("T", low)
	if task.ReviewCycle() == nil || task.Standing.Comments != 1 {
		t.Fatalf("cycle %+v comments %d, want a review cycle and the comment still new", task.ReviewCycle(), task.Standing.Comments)
	}
	b.Apply("T", Pushed{})
	passed := comments(1)
	passed.Facts.Score = &five
	if b.Apply("T", passed); task.Stage != Responding || task.ReviewCycle() != nil || task.Left.Responding != 2 {
		t.Fatalf("stage %s cycle %+v left %+v, want a Responding round for the comment", task.Stage, task.ReviewCycle(), task.Left)
	}
}

// Comments on the PR of a Task stored before lineup read them are not news.
// With IgnoredThreads, comments do not gate, as threads do not.
func TestOldCommentsAndIgnoredThreadsStartNoRound(t *testing.T) {
	b, task := userTaskIn(CIRunning)
	task.Taken = nil
	if b.Apply("T", comments(1, 2)); task.Stage != ReadyToLand {
		t.Fatalf("a stored Task: stage %s, want Ready to land", task.Stage)
	}
	if b.Apply("T", comments(1, 2, 3)); task.Stage != Responding {
		t.Fatalf("a stored Task's new comment: stage %s, want Responding", task.Stage)
	}

	b, task = taskIn(CIRunning)
	if b.Apply("T", comments(1)); task.Stage != ReadyToLand {
		t.Fatalf("the zero bar: stage %s, want Ready to land", task.Stage)
	}
}

// A new comment past the Responding budget names itself in the flag, and the
// flag clears when the user answers it by hand.
func TestASpentRespondingBudgetNamesTheNewComments(t *testing.T) {
	b, task := userTaskIn(CIRunning)
	task.Left.Responding = 0
	b.Apply("T", comments(1))
	if task.Flag != ReasonBudgetSpent || task.FlagDetail != "1 new comment after 3 rounds" {
		t.Fatalf("flag %q %q", task.Flag, task.FlagDetail)
	}
}

func TestAClosedTabFlagsOnlyAStageThatNeedsTheWorker(t *testing.T) {
	for _, stage := range []Stage{Working, CIDebug, Responding} {
		b, task := taskIn(stage)
		effects, err := b.Apply("T", TabClosed{})
		if err != nil || effects != nil || task.Stage != stage || task.Flag != ReasonTabClosed || task.FlagDetail != "" {
			t.Errorf("tab closed in %s: stage %s flag %q detail %q effects %v err %v", stage, task.Stage, task.Flag, task.FlagDetail, effects, err)
		}
		if task.Group() != GroupNeedsYou {
			t.Errorf("tab closed in %s: group %s, want Needs you", stage, task.Group())
		}
	}
	for _, stage := range []Stage{CIRunning, InReview, ReadyToLand} {
		b, task := taskIn(stage)
		if effects, err := b.Apply("T", TabClosed{}); err != nil || effects != nil || task.Flag != "" {
			t.Errorf("tab closed in %s: flag %q effects %v err %v", stage, task.Flag, effects, err)
		}
	}
}

func TestAClosedTabNamesTheStageThatWaits(t *testing.T) {
	b, task := taskIn(CIDebug)
	b.Apply("T", TabClosed{})
	b.Apply("T", TabClosed{Waits: true})
	if task.Flag != ReasonTabClosed || task.FlagDetail != "CI debug waits for the Worker" {
		t.Fatalf("flag %q detail %q", task.Flag, task.FlagDetail)
	}
}

func TestAClosedTabReplacesTheFlagOfItsWorkerOnly(t *testing.T) {
	for _, r := range []Reason{ReasonQuestion, ReasonPermission, ReasonAuth, ReasonError, ReasonStopped, ReasonWorkerSilent} {
		b, task := taskIn(Working)
		task.flag(r, "detail")
		b.Apply("T", TabClosed{})
		if task.Flag != ReasonTabClosed || task.FlagDetail != "" {
			t.Errorf("tab closed over %q: flag %q detail %q", r, task.Flag, task.FlagDetail)
		}
	}
	for _, r := range []Reason{ReasonBudgetSpent, ReasonSetupFailed, ReasonDraft, ReasonNoBotReview} {
		b, task := taskIn(Working)
		task.flag(r, "detail")
		b.Apply("T", TabClosed{})
		if task.Flag != r || task.FlagDetail != "detail" {
			t.Errorf("tab closed over %q: flag %q detail %q, want it kept", r, task.Flag, task.FlagDetail)
		}
	}
}

func TestTheTabClosedFlagStaysUntilTheUserOpensTheTab(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", TabClosed{})
	b.Apply("T", WorkerStatus{State: "working"})
	if task.Flag != ReasonTabClosed {
		t.Fatalf("a working status cleared the flag: %q", task.Flag)
	}
	if effects, err := b.Apply("T", TabOpened{}); err != nil || effects != nil || task.Flag != "" {
		t.Fatalf("tab opened: flag %q effects %v err %v", task.Flag, effects, err)
	}
	task.flag(ReasonError, "open: no tab")
	b.Apply("T", TabOpened{})
	if task.Flag != "" {
		t.Fatalf("tab opened left the Worker's %q flag", task.Flag)
	}
	task.flag(ReasonBudgetSpent, "x")
	b.Apply("T", TabOpened{})
	if task.Flag != ReasonBudgetSpent {
		t.Fatalf("tab opened cleared the %q flag", ReasonBudgetSpent)
	}
}

var gated = Policy{
	Budgets: Budgets{CIDebug: 3, Responding: 3, ReviewCycles: 2},
	Bar:     LandingBar{Bot: greptile(5)},
}

func gatedIn(stage Stage) (*Board, *Task) {
	b := NewBoard(5)
	t, _, _ := b.Add("T", gated, false)
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
		{"low score starts a review cycle", scored("abc", 4, DecisionRequired), Responding, []Effect{PromptWorker{Responding}}, ""},
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
		if !reflect.DeepEqual(effects, []Effect{PromptWorker{Responding}}) || task.Stage != Responding {
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

// A Record from before the tab closed flag can carry worker lost.
func TestAStoredWorkerLostFlagGivesWayToTheTabFlags(t *testing.T) {
	b, task := taskIn(Working)
	task.flag("worker lost", "")
	b.Apply("T", TabClosed{})
	if task.Flag != ReasonTabClosed {
		t.Fatalf("tab closed over worker lost: flag %q", task.Flag)
	}
	task.flag("worker lost", "")
	b.Apply("T", TabOpened{})
	if task.Flag != "" {
		t.Fatalf("tab opened left the flag %q", task.Flag)
	}
}

func TestAClosedPRFlagsABuildTaskInEveryStartedStage(t *testing.T) {
	for _, stage := range []Stage{Working, CIRunning, CIDebug, InReview, Responding, ReadyToLand} {
		t.Run(stage.String(), func(t *testing.T) {
			b, task := taskIn(stage)
			effects, err := b.Apply("T", PRClosed{Number: 5149})
			if err != nil || len(effects) != 0 {
				t.Fatalf("effects %v, err %v", effects, err)
			}
			if task.Stage != stage || task.Flag != ReasonPRClosed || task.FlagDetail != "#5149 closed without a merge" {
				t.Fatalf("stage %s, flag %q, detail %q", task.Stage, task.Flag, task.FlagDetail)
			}
			if task.Group() != GroupNeedsYou || task.Outcome != OutcomeNone {
				t.Fatalf("group %s, outcome %q", task.Group(), task.Outcome)
			}
		})
	}
}

func TestARepeatedPRClosedChangesNothing(t *testing.T) {
	b, task := taskIn(InReview)
	b.Apply("T", PRClosed{Number: 7})
	before := *task
	effects, _ := b.Apply("T", PRClosed{Number: 7})
	if len(effects) != 0 || !reflect.DeepEqual(*task, before) {
		t.Fatalf("second pr-closed changed %+v to %+v", before, *task)
	}
}

func TestAClosedPRWaitsForAFlagThatBlocksTheWorker(t *testing.T) {
	b, task := taskIn(Working)
	b.Apply("T", WorkerStatus{State: "blocked", Kind: "question", Detail: "which db?"})
	b.Apply("T", PRClosed{Number: 7})
	if task.Flag != ReasonQuestion {
		t.Fatalf("flag %q, want the question", task.Flag)
	}
	b.Apply("T", WorkerStatus{State: "working"})
	b.Apply("T", PRClosed{Number: 7})
	if task.Flag != ReasonPRClosed {
		t.Fatalf("flag %q, want pr closed once the question cleared", task.Flag)
	}
}

func TestAWorkerStatusKeepsThePRClosedFlag(t *testing.T) {
	b, task := taskIn(Responding)
	b.Apply("T", PRClosed{Number: 7})
	b.Apply("T", WorkerStatus{State: "working"})
	b.Apply("T", TabOpened{})
	if task.Flag != ReasonPRClosed {
		t.Fatalf("flag %q, want pr closed", task.Flag)
	}
}

func TestAReopenedPRClearsTheFlagAndTheTaskGoesWhereItsFactsLead(t *testing.T) {
	cases := []struct {
		name  string
		stage Stage
		seen  PRSeen
		want  Stage
	}{
		{"needs review", CIRunning, needsReview, InReview},
		{"approved", InReview, approved, ReadyToLand},
		{"failed CI", InReview, failedCI, CIDebug},
		{"pending", ReadyToLand, pendingCI, CIRunning},
		{"a Worker Stage stays", Responding, approved, Responding},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, task := taskIn(c.stage)
			b.Apply("T", PRClosed{Number: 7})
			b.Apply("T", c.seen)
			if task.Flag != "" || task.FlagDetail != "" || task.Stage != c.want {
				t.Fatalf("stage %s, flag %q (%q), want %s with no flag", task.Stage, task.Flag, task.FlagDetail, c.want)
			}
		})
	}
}

func TestAClosedPRMergedLaterEndsTheTask(t *testing.T) {
	b, task := taskIn(InReview)
	b.Apply("T", PRClosed{Number: 7})
	b.Apply("T", PRMerged{})
	if task.Stage != Done || task.Outcome != OutcomeMerged {
		t.Fatalf("stage %s, outcome %q", task.Stage, task.Outcome)
	}
}
