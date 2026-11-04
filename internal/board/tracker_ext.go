package board

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/scheduler"
)

// This file holds the browser's GitHub and Local tabs. Each tab shows the
// Items of one Source. Linear tabs keep their own code in tracker.go.

type (
	trkItemsMsg struct {
		key   string
		items []Item
		err   error
	}
	trkItemBodyMsg struct {
		id    string
		lines []string
		err   error
	}
)

func (t *tracker) curSource() Source { return t.curTab().src }

// selectedItem is the Item that an action on a Source tab applies to.
func (t *tracker) selectedItem() (Item, bool) {
	if t.lvl == lvlDetail {
		return t.itemDetail, t.itemDetail.ID != ""
	}
	vis := t.visible()
	if t.lvl != lvlIssues || t.cur < 0 || t.cur >= len(vis) {
		return Item{}, false
	}
	return t.items[vis[t.cur]], true
}

// pickID names the Issue that an overlay is about.
func (t *tracker) pickID() string {
	if t.item != nil {
		return t.item.ID
	}
	return t.pick.Identifier
}

func (m Model) loadItems() tea.Cmd {
	t := m.tr
	src := t.curSource()
	t.refreshing = 1
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		items, err := src.List(ctx)
		return trkItemsMsg{key: src.Key(), items: items, err: err}
	}
}

func (m Model) itemsBack(msg trkItemsMsg) Model {
	t := m.tr
	if src := t.curSource(); src == nil || src.Key() != msg.key {
		return m
	}
	keep, _ := t.selectedItem()
	t.refreshing = 0
	t.err = errText(msg.err)
	t.items = msg.items
	for i, v := range t.visible() {
		if t.items[v].ID == keep.ID {
			t.cur = i
		}
	}
	t.clamp()
	return m
}

func (m Model) loadItemBody(it Item) tea.Cmd {
	t := m.tr
	src := t.curSource()
	width := max(m.width-4, 20)
	return func() tea.Msg {
		if it.Broken != "" {
			return trkItemBodyMsg{id: it.ID, lines: []string{bad.Render(it.Broken)}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		body, err := src.Body(ctx, it)
		if err != nil {
			return trkItemBodyMsg{id: it.ID, err: err}
		}
		return trkItemBodyMsg{id: it.ID, lines: t.md.Lines(it.ID, body, width)}
	}
}

// startTask adds the Issue as a Task, sets its skill if any, then starts it.
// An empty repo lets the Scheduler choose. Without a mapping, need comes
// back so that the Repo picker opens.
func (m Model) startTask(id, kind, repo, skill string, need trkNeedRepoMsg) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		var v scheduler.TaskView
		err := c.Do(scheduler.Request{Op: "add", Issue: id, Kind: kind, Repo: repo}, &v)
		if err != nil {
			if repo == "" && strings.Contains(err.Error(), noMapping) {
				return need
			}
			return opMsg{label: "add", task: id, err: err}
		}
		id := firstNonEmpty(v.ID, id)
		if skill != "" {
			if err := c.Do(scheduler.Request{Op: "set", Task: id, Key: "skill", Value: skill}, nil); err != nil {
				return opMsg{label: "set skill", task: id, err: err}
			}
		}
		return opMsg{label: "start", task: id, note: skill, err: c.Do(scheduler.Request{Op: "start", Task: id}, nil)}
	}
}

func (m Model) startItem(it Item, repo, skill string) tea.Cmd {
	return m.startTask(it.ID, it.Kind, repo, skill, trkNeedRepoMsg{item: &it, skill: skill})
}

// editItem opens a local Issue's file in $EDITOR, then reads the list again.
func (m Model) editItem(it Item) tea.Cmd {
	editor := firstNonEmpty(os.Getenv("EDITOR"), "vi")
	return tea.ExecProcess(exec.Command(editor, it.Path), func(err error) tea.Msg {
		return trkWriteMsg{label: "edit " + it.ID, err: err}
	})
}

// sourceKey handles the keys that act on a Source tab's Items. ok is false
// for the keys that every tab shares, such as tab and j.
func (m Model) sourceKey(k string) (Model, tea.Cmd, bool) {
	t := m.tr
	src := t.curSource()
	switch k {
	case "esc":
		if t.lvl == lvlDetail {
			t.lvl = lvlIssues
		}
		return m, nil, true
	case "enter":
		if it, ok := t.selectedItem(); ok && t.lvl == lvlIssues {
			t.itemDetail, t.detailLines, t.lvl = it, nil, lvlDetail
			m.fillDetail()
			return m, m.loadItemBody(it), true
		}
		return m, nil, true
	case "s":
		it, ok := t.selectedItem()
		switch {
		case !ok:
		case it.Broken != "":
			m.setStatus(it.Path+" does not parse: "+it.Broken, true)
		default:
			t.ov, t.item, t.skCur = ovSkill, &it, 0
			t.skillIn.SetValue("")
			return m, t.skillIn.Focus(), true
		}
		return m, nil, true
	case "c":
		if it, ok := t.selectedItem(); ok && it.Broken == "" {
			t.ov, t.item, t.itemStates = ovStatus, &it, src.Statuses(it)
			t.stCur = max(slices.Index(t.itemStates, it.Status), 0)
		}
		return m, nil, true
	case "n":
		if !src.CanCreate() {
			m.setStatus("open a GitHub: <repo> tab to add an Issue there", true)
			return m, nil, true
		}
		t.ov = ovAdd
		t.input.SetValue("")
		return m, t.input.Focus(), true
	case "o":
		it, ok := t.selectedItem()
		switch {
		case ok && it.Path != "":
			return m, m.editItem(it), true
		case ok && it.URL != "":
			open := m.opts.Open
			return m, func() tea.Msg { return opMsg{label: "open", task: it.ID, err: open(it.URL)} }, true
		}
		return m, nil, true
	case "A":
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) itemRows(vis []int) []string {
	t := m.tr
	grouped := t.filter.Value() == ""
	idW := 4
	count := map[string]int{}
	for _, it := range t.items {
		idW = max(idW, len(it.ID))
		count[it.Group]++
	}
	var out []string
	last := "\x00"
	for i, v := range vis {
		it := t.items[v]
		if grouped && it.Group != last {
			if it.Group != "" {
				out = append(out, "  "+bold.Render(it.Group)+dim.Render(fmt.Sprintf("  %d", count[it.Group])))
			}
			last = it.Group
		}
		text := fmt.Sprintf("%-*s  %s", idW, it.ID, it.Title)
		if it.Broken != "" {
			text += "  " + it.Broken
		}
		out = append(out, m.row(i == t.cur, text, it.Broken != ""))
	}
	return out
}

func (m Model) itemDetailText() string {
	t := m.tr
	it := t.itemDetail
	var b strings.Builder
	fmt.Fprintf(&b, " %s  %s\n", bold.Render(it.ID), it.Title)
	where := firstNonEmpty(it.URL, it.Path)
	fmt.Fprintf(&b, " %s · %s · %s\n\n", it.Status, firstNonEmpty(it.Repo, "no repo"), where)
	switch {
	case t.detailLines != nil:
		for _, l := range t.detailLines {
			b.WriteString(" " + l + "\n")
		}
	case t.err != "":
		b.WriteString(bad.Render(" "+t.err) + "\n")
	default:
		b.WriteString(dim.Render(" loading…") + "\n")
	}
	return b.String()
}

func (m Model) sourceHints() []hint {
	t := m.tr
	open := "browser"
	if it, ok := t.selectedItem(); ok && it.Path != "" {
		open = "edit"
	}
	if t.lvl == lvlDetail {
		return []hint{{"s", "start"}, {"esc", "back to the list"}, {"c", "status"}, {"o", open}, {"j/k", "scroll"}}
	}
	hs := []hint{{"s", "start"}, {"enter", "read"}, {"c", "status"}}
	if t.curSource().CanCreate() {
		hs = append(hs, hint{"n", "new Issue"})
	}
	return append(hs, hint{"o", open}, hint{"/", "search"}, hint{"j/k", "move"})
}
