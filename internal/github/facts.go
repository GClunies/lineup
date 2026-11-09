package github

import (
	"slices"

	"github.com/gclunies/lineup/internal/lifecycle"
)

type Thread struct {
	Resolved bool
	// Opener is the login of the thread's first comment, or "" for a deleted
	// account. Bot is true when a bot account wrote it.
	Opener string
	Bot    bool
	// Commenters are the logins of the thread's last comments.
	Commenters []string
}

// Facts is the PR in the lifecycle's terms. viewer is the user's own login:
// Workers post as the user, so the user's replies are what a thread needs.
func (pr *PR) Facts(viewer string) lifecycle.PRFacts {
	f := lifecycle.PRFacts{
		Head:      pr.HeadSHA,
		Draft:     pr.IsDraft,
		Conflict:  pr.MergeStateStatus == "DIRTY",
		Checks:    pr.CheckRuns,
		ChecksDue: pr.ChecksDue,
		Decision:  decisions[pr.ReviewDecision],
		Requested: pr.ReviewRequests,
	}
	if n := botScore(pr); n >= 0 {
		f.Score = &n
	}
	for _, t := range pr.Threads {
		f.Threads = append(f.Threads, lifecycle.Thread{
			Bot:      t.Bot,
			Mine:     viewer != "" && t.Opener == viewer,
			Resolved: t.Resolved,
			Replied:  viewer != "" && slices.Contains(t.Commenters, viewer),
		})
	}
	f.ThreadsOver = pr.ThreadsOver
	return f
}

var decisions = map[string]lifecycle.Decision{
	"REVIEW_REQUIRED":   lifecycle.DecisionRequired,
	"APPROVED":          lifecycle.DecisionApproved,
	"CHANGES_REQUESTED": lifecycle.DecisionChanges,
}

// requiredChecks is the union of the classic branch protection's required
// contexts and the required checks of each active ruleset on the base branch.
// Both name a check with no PR number, so the first poll of a PR has them.
func (g gqlPR) requiredChecks() []string {
	if g.BaseRef == nil {
		return nil
	}
	var names []string
	if r := g.BaseRef.RefUpdateRule; r != nil {
		names = append(names, r.RequiredStatusCheckContexts...)
	}
	for _, r := range g.BaseRef.Rules.Nodes {
		if r.Type != "REQUIRED_STATUS_CHECKS" || r.RepositoryRuleset != nil && r.RepositoryRuleset.Enforcement != "ACTIVE" {
			continue
		}
		for _, c := range r.Parameters.RequiredStatusChecks {
			names = append(names, c.Context)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// checkRuns marks each head check that the base branch requires, and adds
// each required check that GitHub has not made yet as pending.
func (g gqlPR) checkRuns() []lifecycle.Check {
	required := g.requiredChecks()
	var out []lifecycle.Check
	if n := len(g.Head.Nodes); n > 0 && g.Head.Nodes[n-1].Commit.StatusCheckRollup != nil {
		for _, c := range g.Head.Nodes[n-1].Commit.StatusCheckRollup.Contexts.Nodes {
			name := c.Name
			if c.Typename == "StatusContext" {
				name = c.Context
			}
			out = append(out, lifecycle.Check{Name: name, State: c.state(), Required: slices.Contains(required, name)})
		}
	}
	for _, name := range required {
		if !slices.ContainsFunc(out, func(c lifecycle.Check) bool { return c.Name == name }) {
			out = append(out, lifecycle.Check{Name: name, State: lifecycle.CheckPending, Required: true})
		}
	}
	return out
}

// state maps a CheckRun's status and conclusion, or a StatusContext's state.
func (c gqlContext) state() lifecycle.CheckState {
	if c.Typename == "StatusContext" {
		switch c.State {
		case "SUCCESS":
			return lifecycle.CheckPassed
		case "FAILURE", "ERROR":
			return lifecycle.CheckFailed
		}
		return lifecycle.CheckPending
	}
	if c.Status != "COMPLETED" {
		return lifecycle.CheckPending
	}
	switch c.Conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return lifecycle.CheckPassed
	case "":
		return lifecycle.CheckPending
	}
	return lifecycle.CheckFailed
}

func (g gqlPR) threads() []Thread {
	var out []Thread
	for _, n := range g.ReviewThreads.Nodes {
		t := Thread{Resolved: n.IsResolved}
		if len(n.Opener.Nodes) > 0 && n.Opener.Nodes[0].Author != nil {
			a := n.Opener.Nodes[0].Author
			t.Opener, t.Bot = a.Login, a.Typename == "Bot"
		}
		for _, c := range n.Recent.Nodes {
			if c.Author != nil {
				t.Commenters = append(t.Commenters, c.Author.Login)
			}
		}
		out = append(out, t)
	}
	return out
}
