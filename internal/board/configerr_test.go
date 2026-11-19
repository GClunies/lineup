package board

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const configLine = `config: board.hidden: unknown section "dun" (default layout)`

func configErrBoard() Model {
	m := New(fixture(), Options{Client: &fakeClient{}, Now: func() time.Time { return epoch }, ConfigErr: configLine})
	m.online = true
	m.width, m.height = 81, 30
	m.layout()
	return m
}

func TestAConfigProblemStaysOnScreenAfterOtherStatusMessages(t *testing.T) {
	m := configErrBoard()
	if out := ansi.Strip(m.View()); !strings.Contains(out, configLine) {
		t.Fatalf("config line missing:\n%s", out)
	}
	m.setStatus("start ENG-601 ok", false)
	m.setStatus("no URL for ENG-601", true)
	out := ansi.Strip(m.View())
	for _, s := range []string{configLine, "no URL for ENG-601"} {
		if !strings.Contains(out, s) {
			t.Fatalf("%q missing:\n%s", s, out)
		}
	}
	m, _ = press(m, "j", "j")
	if out := ansi.Strip(m.View()); !strings.Contains(out, configLine) {
		t.Fatalf("config line gone after a key:\n%s", out)
	}
	if got := lipgloss.Height(m.View()); got != m.height {
		t.Fatalf("screen is %d lines, want %d", got, m.height)
	}
}

func TestNoConfigProblemAddsNoLine(t *testing.T) {
	m := newModel(&fakeClient{}, fixture())
	if out := ansi.Strip(m.View()); strings.Contains(out, "config:") {
		t.Fatalf("unexpected config line:\n%s", out)
	}
}
