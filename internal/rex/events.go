package rex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type EventKind int

const (
	ProgramStatusChanged EventKind = iota + 1
	ChildExited
	BlockClosed
	WindowClosed
	SessionDestroyed
)

type Event struct {
	Kind                         EventKind
	SessionID, BlockID, WindowID string
	ExitCode                     int          // ChildExited
	Status                       StatusRecord // ProgramStatusChanged
}

type statusPayload struct {
	ID     string `json:"id"`
	Record struct {
		State string `json:"state"`
		Kind  string `json:"kind"`
		Msg   string `json:"msg"`
		App   string `json:"app"`
	} `json:"record"`
}

// ParseEvent reads one line of `rex events --all --json`. It returns false for
// events that the Scheduler does not use.
func ParseEvent(line []byte) (Event, bool) {
	var top struct {
		Type    string `json:"type"`
		Payload struct {
			SessionID string `json:"session_id"`
			Event     struct {
				Type    string `json:"type"`
				Payload struct {
					BlockID  string          `json:"block_id"`
					WindowID string          `json:"window_id"`
					Name     string          `json:"name"`
					Payload  json.RawMessage `json:"payload"`
				} `json:"payload"`
			} `json:"event"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &top) != nil {
		return Event{}, false
	}
	p := top.Payload
	ev := Event{SessionID: p.SessionID}
	if top.Type == "session_destroyed" {
		ev.Kind = SessionDestroyed
		return ev, true
	}
	if top.Type != "session_event" {
		return Event{}, false
	}
	inner := p.Event.Payload
	ev.BlockID, ev.WindowID = inner.BlockID, inner.WindowID
	switch p.Event.Type {
	case "block_closed":
		ev.Kind = BlockClosed
	case "window_closed":
		ev.Kind = WindowClosed
	case "block_event":
		switch inner.Name {
		case "child_exited":
			var x struct {
				ExitCode int `json:"exit_code"`
			}
			if json.Unmarshal(inner.Payload, &x) != nil {
				return Event{}, false
			}
			ev.Kind, ev.ExitCode = ChildExited, x.ExitCode
		case "program_status_changed":
			var x statusPayload
			if json.Unmarshal(inner.Payload, &x) != nil {
				return Event{}, false
			}
			r := x.Record
			ev.Kind = ProgramStatusChanged
			ev.Status = StatusRecord{State: r.State, Kind: r.Kind, Msg: r.Msg, App: r.App}
		default:
			return Event{}, false
		}
	default:
		return Event{}, false
	}
	return ev, true
}

// Events streams parsed events for the whole server. The channel closes when
// the stream ends or ctx is done.
func (c *Client) Events(ctx context.Context) (<-chan Event, error) {
	cmd := exec.CommandContext(ctx, c.bin(), "events", "--all", "--json")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rex events: %w", err)
	}
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		defer cmd.Wait()
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			ev, ok := ParseEvent(sc.Bytes())
			if !ok {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
