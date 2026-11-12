package github

import (
	"cmp"
	"slices"
	"strings"
)

// ReviewTask is a review Task as the queue rules see it: the PR that it
// reviews, the head commit that it was added at, and whether it is Done.
type ReviewTask struct {
	URL  string `json:"url"`
	Head string `json:"head"`
	Done bool   `json:"done"`
}

// Candidate is a PR that a queue reviews, with why it wants the user.
type Candidate struct {
	PR PullRequest `json:"pr"`
	// Rereview is true when the user reviewed the PR before and it requests
	// the user again.
	Rereview bool `json:"rereview,omitempty"`
	ByName   bool `json:"by_name,omitempty"`
}

// Candidates are the PRs of a queue's search that the queue reviews, in rank
// order: re-reviews first, then the PRs that request the user by name, then
// the oldest. tasks holds every review Task, Done or not.
//
// A PR with an open review Task never enters, because that Task's Worker
// reviews each new head itself. A Done review Task keeps its PR out until the
// author pushes a new head.
func Candidates(prs []PullRequest, tasks []ReviewTask) []Candidate {
	open, seen := map[string]bool{}, map[string]bool{}
	for _, t := range tasks {
		u := strings.ToLower(t.URL)
		open[u] = open[u] || !t.Done
		seen[u+" "+t.Head] = true
	}
	var out []Candidate
	for _, pr := range prs {
		u := strings.ToLower(pr.URL)
		if open[u] || seen[u+" "+pr.Head] || !pr.wantsReview() {
			continue
		}
		out = append(out, Candidate{PR: pr, Rereview: pr.Viewer.Reviewed && pr.Viewer.Requested, ByName: pr.Viewer.ByName})
	}
	slices.SortStableFunc(out, func(a, b Candidate) int {
		return cmp.Or(first(a.Rereview, b.Rereview), first(a.ByName, b.ByName), a.PR.CreatedAt.Compare(b.PR.CreatedAt))
	})
	return out
}

// wantsReview applies the queue rules that need no review Task. pr-review
// refuses a fork, because the fork's code would run with the user's
// credentials. It also stops on a PR with another person's open review,
// because a second review can contradict it.
func (pr PullRequest) wantsReview() bool {
	switch {
	case pr.Draft, pr.Fork, pr.Bot, pr.Viewer.Author, pr.hasOpenReview():
		return false
	case pr.Viewer.Reviewed:
		return pr.Viewer.Requested
	}
	return !slices.ContainsFunc(pr.LatestReviews, func(r Review) bool { return r.opinion() == "APPROVED" })
}

// hasOpenReview is true when a person other than the user and the author
// requested changes, or commented on the head without approving. The author
// answers a comment with a new head.
func (pr PullRequest) hasOpenReview() bool {
	return slices.ContainsFunc(pr.LatestReviews, func(r Review) bool {
		switch {
		case r.Bot, r.Viewer, r.Author == pr.Author:
			return false
		case r.opinion() != "":
			return r.opinion() == "CHANGES_REQUESTED"
		}
		return r.State == "COMMENTED" && r.Commit == pr.Head
	})
}

// opinion is the review author's last APPROVED or CHANGES_REQUESTED, or "".
func (r Review) opinion() string {
	if r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" {
		return r.State
	}
	return r.Opinion
}

// first orders true before false.
func first(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}
	return 1
}
