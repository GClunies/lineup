package board

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gclunies/lineup/internal/scheduler"
	"github.com/gclunies/lineup/internal/termsafe"
)

// LoadCache reads the BoardView that the Scheduler last wrote.
func LoadCache(path string) (scheduler.BoardView, error) {
	var v scheduler.BoardView
	raw, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v)
}

// Run paints the cached view, then follows the Scheduler until the user quits.
func Run(socketPath, cachePath string, opts Options) error {
	cached, _ := LoadCache(cachePath)
	if opts.Client == nil {
		opts.Client = scheduler.Client{Path: socketPath}
	}
	p := tea.NewProgram(New(cached, opts), tea.WithAltScreen())
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			err := opts.Client.Watch(func(v scheduler.BoardView) { p.Send(boardMsg(v)) })
			_ = err
			p.Send(onlineMsg(false))
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	_, err := p.Run()
	return err
}

// Toast asks the terminal for a desktop notification (OSC 777). Rex shows it
// for this pane, so a click on it brings the user back to the pane.
func Toast(w io.Writer, text string) error {
	_, err := fmt.Fprintf(w, "\x1b]777;notify;lineup;%s\x07", termsafe.Clean(text, false))
	return err
}
