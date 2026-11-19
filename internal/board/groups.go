package board

import (
	"maps"
	"slices"
	"strings"

	"github.com/gclunies/lineup/internal/config"
)

// This file holds the group headings of the Board, and the layout of its
// sections.

const (
	groupNeedsYou = "Needs you"
	groupDone     = "Done"
	// headingPrefix starts the selection key of a heading. No Task or Issue ID has it.
	headingPrefix = "group:"
)

// headingKey is the selection key of the heading of group g.
func headingKey(g string) string { return headingPrefix + g }

// headingName returns the group that a heading's selection key names.
func headingName(id string) (string, bool) { return strings.CutPrefix(id, headingPrefix) }

// sections is the order of the sections, and which ones are closed or out of
// sight. It starts as the [board] table says. A key press changes it for this
// run only.
type sections struct {
	order  []string
	closed map[string]bool
	hidden map[string]bool
}

func newSections(b config.Board) sections {
	if len(b.Order) == 0 {
		b = config.DefaultBoard()
	}
	set := func(groups []string) map[string]bool {
		s := map[string]bool{}
		for _, g := range groups {
			s[g] = true
		}
		return s
	}
	return sections{order: b.Order, closed: set(b.Collapsed), hidden: set(b.Hidden)}
}

// rank is the place of group g in the order.
func (l sections) rank(g string) int { return slices.Index(l.order, g) }

// withClosed returns l with group g closed or open. l stays as it was.
func (l sections) withClosed(g string, closed bool) sections {
	l.closed = maps.Clone(l.closed)
	l.closed[g] = closed
	return l
}

// withHidden returns l with group g hidden or shown. l stays as it was.
func (l sections) withHidden(g string, hidden bool) sections {
	l.hidden = maps.Clone(l.hidden)
	l.hidden[g] = hidden
	return l
}
