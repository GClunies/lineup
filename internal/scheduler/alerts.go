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

// ReadyToLand reports whether the Task's PR waits only for the user's merge.
// A flag means that the Task needs the user for another reason.
func (t TaskView) ReadyToLand() bool {
	return t.Kind == lifecycle.KindBuild && t.StageKey == lifecycle.ReadyToLand.Key() && t.Flag == ""
}

// AlertText is the one line that tells the user about the Task's Attention:
// the ID, the first line of the note, and the title, in that order. The note
// already names the Stage's state or how the Task ended. It comes before the
// title because a desktop toast cuts a long line at its end, and a title can
// run past 100 characters. A flag detail can hold many lines of
// stderr, so only the first line shows. A Task with no note shows its outcome,
// or else its Stage.
func (t TaskView) AlertText() string {
	note, _, _ := strings.Cut(t.Note, "\n")
	if note == "" {
		note = t.Outcome
	}
	if note == "" {
		note = t.Stage
	}
	return fmt.Sprintf("%s %s | %s", t.ID, note, t.Title)
}

// Alerts returns the build Tasks of v whose Attention is new since seen, and
// records each one's Attention in seen. A Task that needs nothing leaves seen,
// so it alerts again the next time it needs the user. A review Task or a QA
// Task never alerts (PROD-BOARD-019).
func Alerts(seen map[string]string, v BoardView) []TaskView {
	var out []TaskView
	for _, t := range v.Tasks {
		if t.Kind != lifecycle.KindBuild {
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
