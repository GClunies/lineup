package board

import (
	"cmp"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/lifecycle"
)

// This file holds the group headings of the work and review views, and the
// closed groups that the Board keeps between runs.

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

// folds lists the closed groups of each Kind, in group order. Needs you
// never closes, whatever the list holds.
type folds map[lifecycle.Kind][]string

func defaultFolds() folds {
	return folds{
		lifecycle.KindBuild:  {groupBacklog, groupDone},
		lifecycle.KindReview: {groupDone},
	}
}

// readFolds reads groups.json. A missing or unreadable file gives the defaults,
// and so does a Kind that the file leaves out.
func readFolds(path string) folds {
	f := defaultFolds()
	if path == "" {
		return f
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	var saved folds
	if json.Unmarshal(data, &saved) != nil {
		return f
	}
	maps.Copy(f, saved)
	return f
}

// writeFolds saves f to path. An empty path keeps f for this run only.
func writeFolds(path string, f folds) error {
	if path == "" {
		return nil
	}
	return config.WriteJSON(path, f)
}

func (f folds) closed(k lifecycle.Kind, g string) bool {
	return g != groupNeedsYou && slices.Contains(f[k], g)
}

// with returns f with group g of Kind k closed or open. f stays as it was.
func (f folds) with(k lifecycle.Kind, g string, closed bool) folds {
	gs := []string{}
	for _, x := range f[k] {
		if x != g {
			gs = append(gs, x)
		}
	}
	if closed {
		gs = append(gs, g)
	}
	slices.SortFunc(gs, func(a, b string) int { return cmp.Compare(slices.Index(groupOrder, a), slices.Index(groupOrder, b)) })
	out := maps.Clone(f)
	out[k] = gs
	return out
}
