package lifecycle

import (
	"fmt"
	"slices"
	"strings"
)

// LandingBar is what a build Task's PR must meet for the Task to be in Ready
// to land. A Task keeps the bar that it had at Intake.
//
// The zero LandingBar is the bar from before the Landing bar: every check
// gates, threads never gate, and every requested reviewer must approve. A Task
// stored before the Landing bar decodes to it and keeps those rules.
type LandingBar struct {
	Checks    CheckRule    `json:"checks"`
	Threads   ThreadRule   `json:"threads"`
	Approvals ApprovalRule `json:"approvals"`
	// Bot is the review bot gate, or nil for a Repo with no [review_bot] table.
	Bot *ReviewGate `json:"bot,omitempty"`
}

type CheckRule uint8

const (
	AllChecks      CheckRule = iota // every check gates
	RequiredChecks                  // the base branch's required checks gate
)

type ThreadRule uint8

const (
	IgnoredThreads  ThreadRule = iota // threads never gate
	ResolvedThreads                   // each thread is resolved
	RepliedThreads                    // each thread is resolved, and the user replied in each thread that someone else opened
)

type ApprovalRule uint8

const (
	RequestedApprovals ApprovalRule = iota // the required reviews and every open review request
	RequiredApprovals                      // the reviews that the base branch requires
)

// One list per rule gives both its text and its parse, so they cannot drift.
var (
	checkRuleNames    = []string{AllChecks: "all", RequiredChecks: "required"}
	threadRuleNames   = []string{IgnoredThreads: "ignored", ResolvedThreads: "resolved", RepliedThreads: "replied"}
	approvalRuleNames = []string{RequestedApprovals: "requested", RequiredApprovals: "required"}
)

func (r CheckRule) String() string    { return checkRuleNames[r] }
func (r ThreadRule) String() string   { return threadRuleNames[r] }
func (r ApprovalRule) String() string { return approvalRuleNames[r] }

func (r CheckRule) MarshalText() ([]byte, error)    { return []byte(r.String()), nil }
func (r ThreadRule) MarshalText() ([]byte, error)   { return []byte(r.String()), nil }
func (r ApprovalRule) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

func (r *CheckRule) UnmarshalText(b []byte) error    { return parseRule(r, checkRuleNames, b) }
func (r *ThreadRule) UnmarshalText(b []byte) error   { return parseRule(r, threadRuleNames, b) }
func (r *ApprovalRule) UnmarshalText(b []byte) error { return parseRule(r, approvalRuleNames, b) }

func parseRule[R ~uint8](r *R, names []string, b []byte) error {
	i := slices.Index(names, string(b))
	if i < 0 {
		return fmt.Errorf("%q is not one of %s", b, strings.Join(names, ", "))
	}
	*r = R(i)
	return nil
}

// PRFacts is what one poll saw on a build Task's PR. The GitHub adapter builds
// it. Only LandingBar.Measure reads it.
type PRFacts struct {
	Head     string
	Draft    bool
	Conflict bool
	// Checks holds each check on Head. It also holds each required check that
	// GitHub has not made on Head yet, as pending.
	Checks []Check
	// ChecksDue is true when Head has no checks and the base branch has some.
	// GitHub makes a push's checks a few seconds after the push.
	ChecksDue bool
	// Score is the review bot's score for Head. It is nil until the bot scores
	// Head, and for a PR that no bot gates.
	Score   *int
	Threads []Thread
	// Decision is GitHub's reviewDecision. It counts only the reviews that
	// the base branch requires.
	Decision Decision
	// Requested counts the open review requests, required or not.
	Requested int
}

type Check struct {
	Name     string
	State    CheckState
	Required bool // the base branch's rules require it
}

type CheckState uint8

const (
	CheckPending CheckState = iota
	CheckPassed
	CheckFailed
)

type Thread struct {
	Bot      bool // a bot account opened it
	Mine     bool // the user opened it. Workers post as the user.
	Resolved bool
	Replied  bool // the user wrote a comment after the first one
}

type Decision uint8

const (
	DecisionNone     Decision = iota // the base branch requires no review
	DecisionRequired                 // REVIEW_REQUIRED
	DecisionApproved                 // APPROVED
	DecisionChanges                  // CHANGES_REQUESTED
)

// Standing is a PR measured against a Landing bar. The reducer keeps the
// latest one on the Task. The zero Standing meets every bar.
type Standing struct {
	Head     string   `json:"head,omitempty"`
	Conflict bool     `json:"conflict,omitempty"`
	Failed   []string `json:"failed,omitempty"` // the gating checks that failed. CI debug fixes these.
	// Pending counts the gating checks that run or that GitHub has not made
	// yet. It is at least 1 when the head's checks are due.
	Pending  int  `json:"pending,omitempty"`
	Draft    bool `json:"draft,omitempty"`
	ScoreDue bool `json:"score_due,omitempty"` // a bot gates the PR and has not scored Head
	// Score is the review bot's score for Head, or nil. ScoreLow is true when
	// it is below the minimum.
	Score    *int        `json:"score,omitempty"`
	ScoreLow bool        `json:"score_low,omitempty"`
	Threads  OpenThreads `json:"threads,omitzero"`
	// Unapproved is true when the reviews that the bar asks for have not
	// approved. Review keeps GitHub's decision for the text.
	Unapproved bool     `json:"unapproved,omitempty"`
	Review     Decision `json:"review,omitempty"`
	// Optional lists the optional checks that failed. People see them, and
	// nothing acts on them.
	Optional []string `json:"optional,omitempty"`
}

// OpenThreads counts the review threads that the bar's rule does not count as
// done, by who opened them.
type OpenThreads struct {
	Bot    int `json:"bot,omitempty"`
	People int `json:"people,omitempty"`
}

func (o OpenThreads) Total() int { return o.Bot + o.People }

// Measure applies the bar to one poll's facts.
func (b LandingBar) Measure(f PRFacts) Standing {
	s := Standing{Head: f.Head, Conflict: f.Conflict, Draft: f.Draft, Review: f.Decision}
	noneRequired := !slices.ContainsFunc(f.Checks, func(c Check) bool { return c.Required })
	for _, c := range f.Checks {
		gates := b.Checks == AllChecks || c.Required || noneRequired
		switch {
		case gates && c.State == CheckFailed:
			s.Failed = addName(s.Failed, c.Name)
		case gates && c.State == CheckPending:
			s.Pending++
		case c.State == CheckFailed:
			s.Optional = addName(s.Optional, c.Name)
		}
	}
	if f.ChecksDue {
		s.Pending = max(s.Pending, 1)
	}
	if g := b.Bot; g != nil {
		s.Score = f.Score
		s.ScoreDue = f.Score == nil
		s.ScoreLow = f.Score != nil && *f.Score < g.MinScore
	}
	for _, t := range f.Threads {
		done := b.Threads == IgnoredThreads || t.Resolved && (b.Threads == ResolvedThreads || t.Mine || t.Replied)
		switch {
		case done:
		case t.Bot:
			s.Threads.Bot++
		default:
			s.Threads.People++
		}
	}
	required := f.Decision == DecisionRequired || f.Decision == DecisionChanges
	s.Unapproved = required || b.Approvals == RequestedApprovals && f.Decision != DecisionApproved && f.Requested > 0
	return s
}

func addName(names []string, name string) []string {
	if slices.Contains(names, name) {
		return names
	}
	return append(names, name)
}

// Gap is one way a PR falls short of its Landing bar.
type Gap uint8

const (
	NoGap Gap = iota
	GapConflict
	GapFailed
	GapPending
	GapDraft
	GapScoreDue
	GapScoreLow
	GapThreads
	GapApproval
)

// gaps lists each Gap in the order in which a Task closes them. The first gap
// that a Standing has decides the Stage of a Task that waits on its PR. An
// optional check that failed is no gap. why is the gap's text for people.
var gaps = []struct {
	gap Gap
	has func(Standing) bool
	why func(LandingBar, Standing) string
}{
	{GapConflict, func(s Standing) bool { return s.Conflict }, func(LandingBar, Standing) string { return "conflict" }},
	{GapFailed, func(s Standing) bool { return len(s.Failed) > 0 }, func(_ LandingBar, s Standing) string { return strings.Join(s.Failed, ", ") + " failed" }},
	{GapPending, func(s Standing) bool { return s.Pending > 0 }, func(_ LandingBar, s Standing) string { return count(s.Pending, "check") + " pending" }},
	{GapDraft, func(s Standing) bool { return s.Draft }, func(LandingBar, Standing) string { return "draft" }},
	{GapScoreDue, func(s Standing) bool { return s.ScoreDue }, func(b LandingBar, _ Standing) string { return "waiting for " + b.Bot.Title }},
	{GapScoreLow, func(s Standing) bool { return s.ScoreLow }, func(b LandingBar, s Standing) string { return b.Bot.ScoreText(*s.Score) }},
	{GapThreads, func(s Standing) bool { return s.Threads.Total() > 0 }, func(_ LandingBar, s Standing) string { return threadsText(s.Threads) }},
	{GapApproval, func(s Standing) bool { return s.Unapproved }, func(_ LandingBar, s Standing) string {
		if s.Review == DecisionChanges {
			return "changes requested"
		}
		return "needs approval"
	}},
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func threadsText(o OpenThreads) string {
	text := count(o.Total(), "open thread")
	if o.Bot > 0 {
		text += fmt.Sprintf(" (%d bot)", o.Bot)
	}
	return text
}

func (s Standing) Has(g Gap) bool {
	for _, r := range gaps {
		if r.gap == g {
			return r.has(s)
		}
	}
	return false
}

// Next is the first gap in the order of gaps, or NoGap when the PR meets the bar.
func (s Standing) Next() Gap {
	for _, r := range gaps {
		if r.has(s) {
			return r.gap
		}
	}
	return NoGap
}

func (s Standing) Met() bool { return s.Next() == NoGap }

// Explain is the text of the first gap, or "" when the PR meets the bar.
func (b LandingBar) Explain(s Standing) string { return b.why(s.Next(), s) }

// Item is one line of the bar's checklist, for people.
type Item struct {
	Gap  Gap // NoGap when the PR meets the item
	Text string
}

// Checklist lists what the bar holds, in the order of gaps. The two check gaps
// share one line. A conflict and a draft show only when the PR has them. The
// list leaves out what the bar does not hold, for example the score with no
// bot, or threads with IgnoredThreads.
func (b LandingBar) Checklist(s Standing) []Item {
	var out []Item
	item := func(met string, gs ...Gap) {
		for _, g := range gs {
			if s.Has(g) {
				out = append(out, Item{g, b.why(g, s)})
				return
			}
		}
		if met != "" {
			out = append(out, Item{NoGap, met})
		}
	}
	item("", GapConflict)
	item(map[CheckRule]string{AllChecks: "checks passed", RequiredChecks: "required checks passed"}[b.Checks], GapFailed, GapPending)
	item("", GapDraft)
	if b.Bot != nil {
		met := ""
		if s.Score != nil {
			met = b.Bot.ScoreText(*s.Score)
		}
		item(met, GapScoreDue, GapScoreLow)
	}
	if b.Threads != IgnoredThreads {
		item("review threads done", GapThreads)
	}
	approved := map[ApprovalRule]string{RequestedApprovals: "reviews approved", RequiredApprovals: "required reviews approved"}[b.Approvals]
	if s.Review == DecisionNone {
		approved = "no review required"
	}
	item(approved, GapApproval)
	return out
}

func (b LandingBar) why(g Gap, s Standing) string {
	for _, r := range gaps {
		if r.gap == g {
			return r.why(b, s)
		}
	}
	return ""
}
