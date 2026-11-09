package github

import (
	"slices"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// Signals turns two consecutive snapshots of one PR into Signals: the edges
// between them, in the order the reducer should see them, then one pr-seen
// with the PR's current facts. A nil prev means the PR was not seen before.
// viewer is the user's own login, whose reviews and commits are not news.
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
	if prev == nil {
		out = append(out, lifecycle.PROpen{})
	} else {
		if prev.HeadSHA != cur.HeadSHA {
			out = append(out, lifecycle.Pushed{})
		}
		out = append(out, newReviews(prev, cur, viewer)...)
		if hasNewAuthor(prev, cur, viewer) {
			out = append(out, lifecycle.ReviewAdditionalAuthors{})
		}
	}
	return append(out, lifecycle.PRSeen{Facts: cur.Facts(viewer)})
}

// botScore is the review bot's score for the head commit, or -1 when no bot
// gates the PR or the bot has not reviewed the head.
func botScore(pr *PR) int {
	if pr.Bot == "" || pr.BotSummary == nil || pr.BotSummary.Commit != pr.HeadSHA {
		return -1
	}
	return pr.BotSummary.Score
}

// newReviews returns one Signal per kind, in the order the reviews arrived.
// The gate owns a review bot's reviews, so they are not news here.
func newReviews(prev, cur *PR, viewer string) []lifecycle.Signal {
	bot := ReviewBots[cur.Bot].Logins
	var seen time.Time
	for _, r := range prev.LatestReviews {
		if r.SubmittedAt.After(seen) {
			seen = r.SubmittedAt
		}
	}
	var fresh []Review
	for _, r := range cur.LatestReviews {
		if r.SubmittedAt.After(seen) && r.Author != viewer && !slices.Contains(bot, r.Author) {
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
