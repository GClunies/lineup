package board

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/scheduler"
)

// menu lists choices one per line, so that a tap picks one. A tap on a row
// opens the row's actions, and a tapped action that asks y/n or hits the WIP
// cap asks with a menu too, because a phone has no easy y or n. Each item's
// key goes to the Board as if the user typed it. The last item backs out.
type menu struct {
	title string
	items []hint
	cur   int
}

func (m Model) mouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.mouse = true
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	left := msg.Button == tea.MouseButtonLeft
	switch {
	case len(m.menu.items) > 0:
		if i := bodyIndex(strings.Split(m.menuView(), "\n"), msg.Y, 2, m.bodyHeight()) - 1; left && i >= 0 {
			return m.choose(i)
		}
	case m.mode == modeHelp:
		if left {
			m.mode = modeNormal
		}
	case m.mode != modeNormal:
	case m.detail:
		if left {
			return m.boardKey(keyMsg("f"))
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case msg.Button == tea.MouseButtonWheelUp:
		return m.move(-1), nil
	case msg.Button == tea.MouseButtonWheelDown:
		return m.move(1), nil
	case left:
		return m.tapRow(msg.X, msg.Y)
	}
	return m, nil
}

// tapRow selects the row under the tap. A heading opens or closes its group,
// and a Task or Issue row opens its menu.
func (m Model) tapRow(x, y int) (tea.Model, tea.Cmd) {
	id := m.rowAt(x, y)
	if id == "" || !slices.Contains(m.rowIDs(), id) {
		return m, nil
	}
	m.status, m.sel = "", id
	if g, ok := headingName(id); ok {
		return m.toggleGroup(g), nil
	}
	title := id
	if t, ok := m.current(); ok {
		title += "  " + scheduler.PlainTitle(t.Title)
	} else if r, ok := m.currentIssue(); ok {
		title += "  " + scheduler.PlainTitle(r.Title)
	}
	m.menu = menu{title: title, items: append(m.rowActions(), hint{"", "back"})}
	return m, nil
}

// rowAt is the ID of the heading or row on screen line y at column x, or
// empty when none is there.
func (m Model) rowAt(x, y int) string {
	top, h, w := 2, m.bodyHeight(), m.width
	if m.filterLine() != "" {
		top, h = 3, max(h-1, 1)
	}
	if m.preview {
		w = m.width * 55 / 100
	}
	if x >= w {
		return ""
	}
	rows := m.rowLines(w, h)
	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = r.text
	}
	if i := bodyIndex(texts, y, top, h); i >= 0 {
		return rows[i].id
	}
	return ""
}

// bodyIndex is the index in lines of screen line y, when View draws lines
// from screen line top in h lines, or -1.
func bodyIndex(lines []string, y, top, h int) int {
	i := y - top
	if i < 0 || i >= h {
		return -1
	}
	if len(lines) > h {
		i += windowStart(lines, h)
	}
	if i >= len(lines) {
		return -1
	}
	return i
}

func (m Model) menuView() string {
	lines := []string{" " + headRow.Render(ansi.Truncate(m.menu.title, max(m.width-1, 1), "…"))}
	for i, it := range m.menu.items {
		lines = append(lines, m.row(i == m.menu.cur, it.does))
	}
	return strings.Join(lines, "\n")
}

func (m Model) menuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch k {
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.menu.cur = min(m.menu.cur+1, len(m.menu.items)-1)
	case "k", "up":
		m.menu.cur = max(m.menu.cur-1, 0)
	case "enter":
		return m.choose(m.menu.cur)
	case "esc":
		return m.choose(len(m.menu.items) - 1)
	default:
		if i := slices.IndexFunc(m.menu.items, func(h hint) bool { return h.key == k }); i >= 0 {
			return m.choose(i)
		}
	}
	return m, nil
}

// choose closes the menu and sends item i's key to the Board.
func (m Model) choose(i int) (tea.Model, tea.Cmd) {
	if i >= len(m.menu.items) {
		return m, nil
	}
	k := m.menu.items[i].key
	m.menu = menu{}
	if k == "" {
		return m, nil
	}
	t, cmd := m.key(keyMsg(k))
	return t.(Model).withPromptMenu(), cmd
}

// withPromptMenu opens the y/n of a close, or the WIP cap choice, as a menu
// when the user works by mouse.
func (m Model) withPromptMenu() Model {
	if !m.mouse {
		return m
	}
	switch m.mode {
	case modeConfirm:
		m.menu = menu{title: m.pendingAsk + "?", items: []hint{{"y", "yes, " + m.pendingAsk}, {"n", "no, keep it"}}}
	case modeCap:
		m.menu = menu{title: "wip cap full", items: []hint{
			{"o", "start once over the cap"},
			{"c", fmt.Sprintf("raise the cap to %d and start", m.board.Cap+1)},
			{"esc", "cancel"},
		}}
	}
	return m
}

// keyMsg is the key press that k names, as msg.String() spells it.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
