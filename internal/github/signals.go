package github

import (
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// ci collapses the check state to what the lifecycle needs. A repo with no
// checks counts as passed.
func ci(c CheckState) CheckState {
	if c == ChecksNone {
		return ChecksPassed
	}
	return c
}

// Signals turns two consecutive snapshots of one PR into the Signals for the
// edges between them, in the order the reducer should see them. A nil prev
// means the PR was not seen before. viewer is the user's own login, whose
// reviews and commits are not news.
func Signals(prev, cur *PR, viewer string) []lifecycle.Signal {
	if cur == nil {
		return nil
	}
	if cur.State != "OPEN" {
		if cur.State == "MERGED" && (prev == nil || prev.State != "MERGED") {
			return []lifecycle.Signal{lifecycle.PRMerged{}}
		}
		return nil
	}

	var out []lifecycle.Signal
	headChanged := prev != nil && prev.HeadSHA != cur.HeadSHA
	if prev == nil {
		out = append(out, lifecycle.PROpen{})
	}
	if headChanged {
		out = append(out, lifecycle.Pushed{})
	}

	was := ChecksPending
	if prev != nil {
		was = ci(prev.Checks)
	}
	now := ci(cur.Checks)
	switch {
	case now == ChecksFailed && (was != ChecksFailed || headChanged):
		out = append(out, lifecycle.CIFailed{})
	case now == ChecksPassed && (was != ChecksPassed || headChanged || (prev != nil && prev.IsDraft != cur.IsDraft)):
		out = append(out, lifecycle.CIPassed{Draft: cur.IsDraft, NeedsReview: needsReview(cur)})
	}

	if prev == nil {
		return out
	}

	if cur.ReviewDecision == "APPROVED" && prev.ReviewDecision != "APPROVED" {
		out = append(out, lifecycle.ReviewApproved{})
	}
	out = append(out, newReviews(prev, cur, viewer)...)
	if hasNewAuthor(prev, cur, viewer) {
		out = append(out, lifecycle.ReviewAdditionalAuthors{})
	}
	if cur.MergeStateStatus == "DIRTY" && prev.MergeStateStatus != "DIRTY" {
		out = append(out, lifecycle.PRConflict{})
	}
	return out
}

func needsReview(pr *PR) bool {
	if pr.ReviewDecision == "APPROVED" {
		return false
	}
	return pr.ReviewRequests > 0 || pr.ReviewDecision == "REVIEW_REQUIRED" || pr.ReviewDecision == "CHANGES_REQUESTED"
}

// newReviews returns one Signal per kind, in the order the reviews arrived.
func newReviews(prev, cur *PR, viewer string) []lifecycle.Signal {
	var seen time.Time
	for _, r := range prev.LatestReviews {
		if r.SubmittedAt.After(seen) {
			seen = r.SubmittedAt
		}
	}
	var fresh []Review
	for _, r := range cur.LatestReviews {
		if r.SubmittedAt.After(seen) && r.Author != viewer {
			fresh = append(fresh, r)
		}
	}
	for i := 1; i < len(fresh); i++ {
		for j := i; j > 0 && fresh[j].SubmittedAt.Before(fresh[j-1].SubmittedAt); j-- {
			fresh[j], fresh[j-1] = fresh[j-1], fresh[j]
		}
	}
	var out []lifecycle.Signal
	var changes, comments bool
	for _, r := range fresh {
		switch {
		case r.State == "CHANGES_REQUESTED" && !changes:
			changes = true
			out = append(out, lifecycle.ReviewChangesRequested{})
		case r.State == "COMMENTED" && !comments:
			comments = true
			out = append(out, lifecycle.ReviewCommented{})
		}
	}
	return out
}

// hasNewAuthor reports whether a commit that the last snapshot lacked has an
// author who is neither the viewer nor unknown.
func hasNewAuthor(prev, cur *PR, viewer string) bool {
	added := cur.CommitCount - prev.CommitCount
	if added <= 0 {
		return false
	}
	authors := cur.CommitAuthors
	if added < len(authors) {
		authors = authors[len(authors)-added:]
	}
	for _, a := range authors {
		if a != "" && a != viewer {
			return true
		}
	}
	return false
}
