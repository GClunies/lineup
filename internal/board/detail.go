package board

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/scheduler"
)

// loadDetail reads the detail pane's content off the render path.
func (m Model) loadDetail() tea.Cmd {
	t, ok := m.current()
	if m.detailTask != "" {
		for _, x := range m.tasks {
			if x.ID == m.detailTask {
				t, ok = x, true
			}
		}
	}
	if !ok {
		return nil
	}
	gen, c := m.detailGen, m.client
	return func() tea.Msg {
		var d scheduler.TaskDetail
		if err := c.Do(scheduler.Request{Op: "show", Task: t.ID}, &d); err != nil {
			return detailMsg{gen: gen, err: err}
		}
		return detailMsg{gen: gen, text: formatDetail(d)}
	}
}

func formatDetail(d scheduler.TaskDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n%s  %s\n", d.ID, d.Title, d.Stage, d.Branch)
	if d.Flag != "" {
		fmt.Fprintf(&b, "⚑ %s %s\n", d.Flag, d.FlagDetail)
	}
	if len(d.Bar) > 0 {
		b.WriteString("Landing bar\n")
		for _, line := range d.Bar {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n")
	for _, e := range d.Events {
		fmt.Fprintf(&b, "%s  %-16s %s → %s  %s\n", e.At.Local().Format(time.TimeOnly), e.Signal, e.From, e.To, e.Detail)
	}
	return b.String()
}
