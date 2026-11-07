package github

import (
	"regexp"
	"slices"
	"strconv"
)

// ReviewBot is a code review bot that keeps one summary comment on a PR and
// edits it after each review. The summary names its score and the commit it
// reviewed.
type ReviewBot struct {
	Title    string
	Logins   []string // as GraphQL spells them, with no [bot] suffix
	TopScore int
	score    *regexp.Regexp
	commit   *regexp.Regexp
}

// ReviewBots holds each review bot that a Task can gate on, by its name in
// the configuration.
var ReviewBots = map[string]ReviewBot{
	"greptile": {
		Title:    "Greptile",
		Logins:   []string{"greptile-apps", "greptile-apps-staging"},
		TopScore: 5,
		score:    regexp.MustCompile(`<!-- greptile_confidence_score:(\d+) -->`),
		commit:   regexp.MustCompile(`Last reviewed commit: .*?/commit/([0-9a-f]{40})`),
	},
}

// BotSummary is what a review bot's summary comment says.
type BotSummary struct {
	Score  int
	Commit string
}

// summary reads the first comment that the bot wrote with a score and a
// reviewed commit. Only the bot's own logins count, so no other author can
// write a score.
func (b ReviewBot) summary(comments []gqlComment) *BotSummary {
	for _, c := range comments {
		if c.Author == nil || !slices.Contains(b.Logins, c.Author.Login) {
			continue
		}
		score, commit := b.score.FindStringSubmatch(c.Body), b.commit.FindStringSubmatch(c.Body)
		if score == nil || commit == nil {
			continue
		}
		n, err := strconv.Atoi(score[1])
		if err != nil {
			continue
		}
		return &BotSummary{Score: n, Commit: commit[1]}
	}
	return nil
}
