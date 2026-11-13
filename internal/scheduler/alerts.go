package scheduler

import (
	"fmt"
	"strings"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// Attention is what the user must know about the Task now: why it needs them,
// or how it ended. It is "" while the Task needs nothing from the user.
func (t TaskView) Attention() string {
	switch {
	case t.Group == lifecycle.GroupNeedsYou.String():
		return t.Stage + "|" + t.Flag
	case t.StageKey == lifecycle.Done.Key():
		return t.Stage + "|" + t.Outcome
	}
	return ""
}

// AlertText is the one line that tells the user about the Task's Attention.
// A flag detail can hold many lines of stderr, so only its first line shows.
func (t TaskView) AlertText() string {
	note, _, _ := strings.Cut(t.Note, "\n")
	if t.StageKey == lifecycle.Done.Key() && t.Outcome != "" {
		note = t.Outcome
	}
	if note == "" {
		return fmt.Sprintf("%s %s: %s", t.ID, t.Stage, t.Title)
	}
	return fmt.Sprintf("%s %s, %s: %s", t.ID, t.Stage, note, t.Title)
}

// Alerts returns the build Tasks of v whose Attention is new since seen, and
// records each one's Attention in seen. A Task that needs nothing leaves seen,
// so it alerts again the next time it needs the user. A review Task never
// alerts: the user answers it in its own tab.
func Alerts(seen map[string]string, v BoardView) []TaskView {
	var out []TaskView
	for _, t := range v.Tasks {
		if t.Kind == lifecycle.KindReview {
			continue
		}
		a := t.Attention()
		if a == "" {
			delete(seen, t.ID)
			continue
		}
		if seen[t.ID] != a {
			out = append(out, t)
		}
		seen[t.ID] = a
	}
	return out
}
