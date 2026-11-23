package config

import (
	"fmt"
	"slices"
)

// section is one group of the board: the key that [board] names it by, and the
// heading that the board shows. The table is in the default order.
type section struct{ key, heading string }

var sections = []section{
	{"needs_you", "Needs you"},
	{"follow_up", "Follow-up"},
	{"backlog", "Backlog"},
	{"ready", "Ready"},
	{"working", "Working"},
	{"waiting", "Waiting"},
	{"done", "Done"},
}

const sectionNeedsYou = "needs_you"

// goneSection is the key of the Queued section, which lineup no longer has. A
// [board] list that names it still loads, without it, and with a warning.
const goneSection = "queued"

// boardFile is the [board] table. A nil list takes its default, and an empty
// list means none.
type boardFile struct {
	Order     *[]string `toml:"order"`
	Collapsed *[]string `toml:"collapsed"`
	Hidden    *[]string `toml:"hidden"`
}

// Board is the layout of the board's sections, as group headings. Order holds
// every section. Collapsed sections start closed, and Hidden ones start out
// of sight.
type Board struct {
	Order, Collapsed, Hidden []string
}

// DefaultBoard is the layout when [board] is absent.
func DefaultBoard() Board {
	b, _, _ := resolveBoard(boardFile{})
	return b
}

// Board is the board's section layout. A bad [board] table does not fail the
// load: the layout is then the default, and the error says what is wrong. A
// list that names a gone section keeps the rest of its keys, and each warning
// names the list.
func (g *Global) Board() (Board, []string, error) { return g.board, g.boardWarnings, g.boardErr }

func headingOf(key string) (string, bool) {
	i := slices.IndexFunc(sections, func(s section) bool { return s.key == key })
	if i < 0 {
		return "", false
	}
	return sections[i].heading, true
}

// headings turns the section keys of the list called name into headings. It
// fails on an unknown key, on a duplicate, and on the key banned. It drops the
// gone section, and the warning says so.
func headings(name string, keys []string, banned string) (out []string, warning string, err error) {
	out = make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		if k == goneSection {
			warning = fmt.Sprintf("board.%s: section %q is gone, lineup add starts each Task (ignored)", name, k)
			continue
		}
		h, ok := headingOf(k)
		switch {
		case !ok:
			return nil, "", fmt.Errorf("board.%s: unknown section %q", name, k)
		case seen[k]:
			return nil, "", fmt.Errorf("board.%s: duplicate section %q", name, k)
		case k == banned:
			return nil, "", fmt.Errorf("board.%s: section %q cannot be in this list", name, k)
		}
		seen[k] = true
		out = append(out, h)
	}
	return out, warning, nil
}

func resolveBoard(f boardFile) (Board, []string, error) {
	collapsed := []string{"backlog", "done"}
	if f.Collapsed != nil {
		collapsed = *f.Collapsed
	}
	var order, hidden []string
	if f.Order != nil {
		order = *f.Order
	}
	if f.Hidden != nil {
		hidden = *f.Hidden
	}
	var warnings []string
	list := func(name string, keys []string, banned string) ([]string, error) {
		h, warning, err := headings(name, keys, banned)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		return h, err
	}
	var b Board
	var err error
	if b.Order, err = list("order", order, ""); err != nil {
		return Board{}, nil, err
	}
	for _, s := range sections {
		if !slices.Contains(b.Order, s.heading) {
			b.Order = append(b.Order, s.heading)
		}
	}
	if b.Collapsed, err = list("collapsed", collapsed, sectionNeedsYou); err != nil {
		return Board{}, nil, err
	}
	if b.Hidden, err = list("hidden", hidden, sectionNeedsYou); err != nil {
		return Board{}, nil, err
	}
	return b, warnings, nil
}
