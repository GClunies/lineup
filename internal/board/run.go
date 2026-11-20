package board

import (
	"encoding/base64"
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
	p := tea.NewProgram(New(cached, opts), tea.WithAltScreen(), tea.WithMouseCellMotion())
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			err := opts.Client.Watch(func(v scheduler.BoardView) bool { p.Send(boardMsg(v)); return true })
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

// Status tells the terminal how many build Tasks need the user. It writes the
// root record of the Program Status Protocol (OSC 7501). A count above 0
// writes a blocked record whose message names the count, and Rex marks the tab
// while the record stands. A count of 0 clears the record. Rex drops a blocked
// record when the Board exits.
func Status(w io.Writer, n int) error {
	if n <= 0 {
		_, err := io.WriteString(w, "\x1b]7501;state=clear\x1b\\")
		return err
	}
	msg := fmt.Sprintf("%d Tasks need you", n)
	if n == 1 {
		msg = "1 Task needs you"
	}
	_, err := fmt.Fprintf(w, "\x1b]7501;state=blocked:kind=question:app=lineup:msg=%s\x1b\\",
		base64.StdEncoding.EncodeToString([]byte(msg)))
	return err
}
