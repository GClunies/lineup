package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// Usage is what one conversation spent: the tokens of its API messages, its
// subagents' included, and its agent time.
type Usage struct {
	Input, Output, CacheWrite, CacheRead int64
	// AgentTime is the time that the Worker worked on prompts. It leaves out
	// the time that the Worker waited for the user.
	AgentTime time.Duration
}

type tokens struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
}

type entry struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	UUID       string `json:"uuid"`
	DurationMs int64  `json:"durationMs"`
	Message    struct {
		ID    string  `json:"id"`
		Usage *tokens `json:"usage"`
	} `json:"message"`
}

var (
	usageKey = []byte(`"usage"`)
	turnKey  = []byte(`"turn_duration"`)
)

// ReadUsage adds up the usage of the conversation id. Claude writes one entry
// for each content block of a message, each with the whole message's usage,
// so each message counts once. Claude ends each turn with an entry that holds
// the turn's duration less the time that it waited for the user, and the
// agent time is the sum of those durations. A subagent works inside a turn,
// so only the conversation's own files add time. A resume from another folder
// can copy entries into a file there, so each message and each turn counts
// once across every file. Usage is nil when no file of the conversation is
// left.
func ReadUsage(configDir, id string) (*Usage, error) {
	mains, subagents, err := conversationFiles(configDir, id)
	if err != nil || len(mains) == 0 {
		return nil, err
	}
	t := tally{messages: map[string]tokens{}, turns: map[string]time.Duration{}}
	for _, f := range mains {
		if err := t.read(f, true); err != nil {
			return nil, err
		}
	}
	for _, f := range subagents {
		if err := t.read(f, false); err != nil {
			return nil, err
		}
	}
	var u Usage
	for _, m := range t.messages {
		u.Input += m.Input
		u.Output += m.Output
		u.CacheWrite += m.CacheWrite
		u.CacheRead += m.CacheRead
	}
	for _, d := range t.turns {
		u.AgentTime += d
	}
	return &u, nil
}

type tally struct {
	messages map[string]tokens
	turns    map[string]time.Duration
}

func (t tally) read(path string, main bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// A line that holds an image can be megabytes long, past a Scanner's limit.
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		t.add(line, main)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (t tally) add(line []byte, main bool) {
	turn := main && bytes.Contains(line, turnKey)
	if !turn && !bytes.Contains(line, usageKey) {
		return
	}
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return // Claude can still be writing the last line.
	}
	if e.Message.ID != "" && e.Message.Usage != nil {
		t.messages[e.Message.ID] = *e.Message.Usage
	}
	if turn && e.Type == "system" && e.Subtype == "turn_duration" {
		t.turns[e.UUID] = time.Duration(e.DurationMs) * time.Millisecond
	}
}
