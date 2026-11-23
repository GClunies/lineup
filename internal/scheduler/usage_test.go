package scheduler

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// serve answers on a socket, as lineup serve does, and returns its client.
func (h *harness) serve() Client {
	h.t.Helper()
	dir, err := os.MkdirTemp("", "lu") // a socket path must be short
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Serve(ctx, filepath.Join(dir, "s.sock")) }()
	h.t.Cleanup(func() { cancel(); <-done; os.RemoveAll(dir) })
	c := Client{Path: filepath.Join(dir, "s.sock")}
	eventually(h.t, "the socket", func() bool { return c.Do(Request{Op: "ls"}, nil) == nil })
	return c
}

// spent is the usage of ENG-1's conversation. ENG-2 has none.
var spent = Usage{Input: 280, Output: 87911, CacheWrite: 638890, CacheRead: 23340576, AgentSeconds: 1112}

const spentJSON = `{"input_tokens":280,"output_tokens":87911,"cache_write_tokens":638890,"cache_read_tokens":23340576,"agent_seconds":1112}`

// usageBoard holds ENG-1, a Working Task whose transcript the Harness reads,
// and ENG-2, a Queued Task with no conversation.
func usageBoard(t *testing.T) *harness {
	h := newHarness(t, filepath.Join(t.TempDir(), "state.db"), nil)
	h.working()
	h.do(Request{Op: "add", Issue: "ENG-2"})
	var conv string
	h.s.call(func() { conv = h.s.recs["ENG-1"].Conversation })
	u := spent
	h.hn.mu.Lock()
	h.hn.usage, h.hn.usageCalls = map[string]*Usage{conv: &u}, 0
	h.hn.mu.Unlock()
	return h
}

// lsUsage is the usage JSON of each Task that ls prints.
func lsUsage(t *testing.T, c Client, req Request) map[string]string {
	t.Helper()
	var v struct{ Tasks []map[string]json.RawMessage }
	if err := c.Do(req, &v); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, task := range v.Tasks {
		var id string
		if err := json.Unmarshal(task["id"], &id); err != nil {
			t.Fatal(err)
		}
		out[id] = string(task["usage"])
	}
	return out
}

func TestLsReadsNoUsageUnlessAsked(t *testing.T) {
	h := usageBoard(t)
	got := lsUsage(t, h.serve(), Request{Op: "ls"})
	if want := map[string]string{"ENG-1": "null", "ENG-2": "null"}; !maps.Equal(got, want) {
		t.Errorf("usage = %v, want %v", got, want)
	}
	h.hn.mu.Lock()
	defer h.hn.mu.Unlock()
	if h.hn.usageCalls != 0 {
		t.Errorf("ls read the usage %d times", h.hn.usageCalls)
	}
}

func TestLsWithUsageGivesEachTaskItsUsage(t *testing.T) {
	h := usageBoard(t)
	got := lsUsage(t, h.serve(), Request{Op: "ls", Usage: true})
	if want := map[string]string{"ENG-1": spentJSON, "ENG-2": "null"}; !maps.Equal(got, want) {
		t.Errorf("usage = %v, want %v", got, want)
	}
}

func TestShowGivesTheTaskItsUsage(t *testing.T) {
	h := usageBoard(t)
	c := h.serve()
	var d TaskDetail
	if err := c.Do(Request{Op: "show", Task: "ENG-1"}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Usage == nil || *d.Usage != spent {
		t.Errorf("ENG-1 usage = %+v, want %+v", d.Usage, spent)
	}
	d = TaskDetail{}
	if err := c.Do(Request{Op: "show", Task: "ENG-2"}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Usage != nil {
		t.Errorf("ENG-2 usage = %+v, want unknown", *d.Usage)
	}
}

// A slow transcript read does not hold the loop, so the Board's view goes on.
func TestAUsageReadDoesNotHoldTheLoop(t *testing.T) {
	h := usageBoard(t)
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	h.hn.mu.Lock()
	h.hn.usageHold = hold
	h.hn.mu.Unlock()
	shown := make(chan TaskDetail, 1)
	go func() {
		d, _ := h.s.show(context.Background(), "ENG-1")
		shown <- d
	}()
	eventually(t, "a usage read", func() bool {
		h.hn.mu.Lock()
		defer h.hn.mu.Unlock()
		return h.hn.usageCalls == 1
	})
	listed := make(chan error, 1)
	go func() {
		_, err := h.s.Handle(context.Background(), Request{Op: "ls"})
		listed <- err
	}()
	select {
	case err := <-listed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ls waited for the usage read")
	}
	release()
	if d := <-shown; d.Usage == nil || *d.Usage != spent {
		t.Errorf("usage after the read = %+v, want %+v", d.Usage, spent)
	}
}
