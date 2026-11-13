package board

import (
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// This file holds the work view's filter, which / opens. While it holds
// text, only the Task and Issue rows whose ID and title match show, every
// group with a match is open, and j and k skip the headings. The filter
// lives for this run only.

func newFilter() textinput.Model {
	f := textinput.New()
	f.Prompt = "/ "
	f.Cursor.SetMode(cursor.CursorStatic)
	return f
}

// query is the filter's text, or "" for no filter.
func (m Model) query() string { return strings.TrimSpace(m.filter.Value()) }

// matching keeps the rows whose label matches q, in their order.
func matching[T any](q string, rows []T, label func(T) string) []T {
	labels := make([]string, len(rows))
	for i, r := range rows {
		labels[i] = label(r)
	}
	hit := rank(q, labels)
	var out []T
	for i, r := range rows {
		if slices.Contains(hit, i) {
			out = append(out, r)
		}
	}
	return out
}

// startFilter opens the filter, and keeps the selection that esc gives back.
func (m Model) startFilter() (tea.Model, tea.Cmd) {
	if m.filter.Value() == "" {
		m.unfiltered = m.sel
	}
	m.mode = modeFilter
	return m, m.filter.Focus()
}

// clearFilter empties the filter and gives back the rows and the selection
// from before it.
func (m Model) clearFilter() Model {
	m.mode = modeNormal
	m.filter.SetValue("")
	m.filter.Blur()
	m.setBoard(m.board)
	if slices.Contains(m.rowIDs(), m.unfiltered) {
		m.sel = m.unfiltered
	}
	return m
}

func (m Model) filterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes {
		return m.typeRunes(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.clearFilter(), nil
	case "enter":
		if m.query() == "" {
			return m.clearFilter(), nil
		}
		m.mode = modeNormal
		m.filter.Blur()
		return m, nil
	case "down", "ctrl+n":
		return m.move(1), nil
	case "up", "ctrl+p":
		return m.move(-1), nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.setBoard(m.board)
	return m, cmd
}

// typeRunes adds typed text to the filter. A terminal can send fast input as
// one run of runes, and the text input reads a run that spells a key name,
// such as "up", as that key. So the input gets one rune at a time.
func (m Model) typeRunes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, r := range msg.Runes {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt, Paste: msg.Paste})
		cmds = append(cmds, cmd)
	}
	m.setBoard(m.board)
	return m, tea.Batch(cmds...)
}

// filterLine is the line above the rows while the filter is open or holds
// text. It is "" when it does not show.
func (m Model) filterLine() string {
	if m.mode != modeFilter && m.query() == "" {
		return ""
	}
	return " " + m.filter.View()
}
