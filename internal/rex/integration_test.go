package rex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestSession(t *testing.T) (*Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	if _, err := exec.LookPath("rex"); err != nil {
		t.Skip("rex not on PATH")
	}
	c := &Client{}
	label := fmt.Sprintf("lineup-test-%d", time.Now().UnixNano())
	id, err := c.EnsureSession(context.Background(), label)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.DestroySession(context.Background(), id); err != nil {
			t.Errorf("destroy session: %v", err)
		}
	})
	return c, id
}

// subscribe starts the event stream and waits until Rex delivers events, so
// that later events are not missed.
func subscribe(t *testing.T, c *Client) (<-chan Event, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ch, err := c.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	return ch, cancel
}

func waitFor(t *testing.T, ch <-chan Event, d time.Duration, match func(Event) bool) Event {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("event stream closed")
			}
			if match(ev) {
				return ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for event")
		}
	}
}

func TestChildExitedThenBlockClosed(t *testing.T) {
	c, sess := newTestSession(t)
	ctx := context.Background()

	view, err := c.View(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Windows) != 0 {
		t.Fatalf("new session has windows %+v, want none", view.Windows)
	}

	ch, _ := subscribe(t, c)
	w, err := c.NewWindow(ctx, sess, "exit3", "", nil, []string{"sh", "-c", "sleep 2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	ev := waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == ChildExited && e.BlockID == w.BlockID })
	if ev.ExitCode != 3 || ev.SessionID != sess {
		t.Fatalf("got %+v, want exit code 3 in %s", ev, sess)
	}
	waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == BlockClosed && e.BlockID == w.BlockID })
	waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == WindowClosed && e.WindowID == w.WindowID })
}

func TestNewWindowEnvCwdAndClose(t *testing.T) {
	c, sess := newTestSession(t)
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ch, _ := subscribe(t, c)
	env := map[string]string{"LINEUP_CODE": "5"}
	w, err := c.NewWindow(ctx, sess, "env", dir, env, []string{"sh", "-c", `test "$(pwd -P)" = "$1" && exit $LINEUP_CODE; exit 9`, "sh", dir})
	if err != nil {
		t.Fatal(err)
	}
	ev := waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == ChildExited && e.BlockID == w.BlockID })
	if ev.ExitCode != 5 {
		t.Fatalf("exit code %d, want 5 from env LINEUP_CODE", ev.ExitCode)
	}

	view, err := c.View(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Windows) != 1 || view.Windows[0].Label != "env" || view.Windows[0].BlockIDs[0] != w.BlockID {
		t.Fatalf("view %+v does not hold window env with block %s", view, w.BlockID)
	}

	if err := c.CloseWindow(ctx, sess, w.WindowID); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWindow(ctx, sess, w.WindowID); err != nil {
		t.Fatalf("second close: %v", err)
	}
	ss, err := c.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range ss {
		found = found || s.ID == sess
	}
	if !found {
		t.Fatal("session missing from Sessions")
	}
}

func TestSplitRunsBesideItsAnchor(t *testing.T) {
	c, sess := newTestSession(t)
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := subscribe(t, c)
	w, err := c.NewWindow(ctx, sess, "anchor", "", nil, []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	split, err := c.Split(ctx, sess, w.BlockID, dir, []string{"sh", "-c", `test "$(pwd -P)" = "$1"`, "sh", dir})
	if err != nil {
		t.Fatal(err)
	}
	ev := waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == ChildExited && e.BlockID == split })
	if ev.ExitCode != 0 {
		t.Fatalf("exit code %d, want 0 from the split's folder", ev.ExitCode)
	}
	waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == BlockClosed && e.BlockID == split })
	view, err := c.View(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Windows) != 1 || len(view.Windows[0].BlockIDs) != 1 || view.Windows[0].BlockIDs[0] != w.BlockID {
		t.Fatalf("view %+v, want the window with its anchor block %s only", view, w.BlockID)
	}
}

// A split keeps its window open after the anchor block closes.
func TestAWindowOutlivesItsAnchorWhileASplitRuns(t *testing.T) {
	c, sess := newTestSession(t)
	ctx := context.Background()
	ch, _ := subscribe(t, c)
	w, err := c.NewWindow(ctx, sess, "anchor", "", nil, []string{"sleep", "2"})
	if err != nil {
		t.Fatal(err)
	}
	split, err := c.Split(ctx, sess, w.BlockID, "", []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, 15*time.Second, func(e Event) bool { return e.Kind == BlockClosed && e.BlockID == w.BlockID })
	view, err := c.View(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Windows) != 1 || !slices.Equal(view.Windows[0].BlockIDs, []string{split}) {
		t.Fatalf("view %+v, want the window with the split %s only", view, split)
	}
}

func TestDestroySessionIsIdempotent(t *testing.T) {
	c, sess := newTestSession(t)
	ctx := context.Background()
	if err := c.DestroySession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := c.DestroySession(ctx, sess); err != nil {
		t.Fatalf("second destroy: %v", err)
	}
}

// TestClaudeProgramStatus runs the real claude CLI, which costs tokens. It
// needs LINEUP_INTEGRATION=1 and LINEUP_TRUSTED_DIR, a folder that the profile in
// CLAUDE_CONFIG_DIR already trusts. A throwaway profile cannot authenticate.
func TestClaudeProgramStatus(t *testing.T) {
	if os.Getenv("LINEUP_INTEGRATION") != "1" {
		t.Skip("set LINEUP_INTEGRATION=1 to run claude")
	}
	dir, profile := os.Getenv("LINEUP_TRUSTED_DIR"), os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" || profile == "" {
		t.Skip("set LINEUP_TRUSTED_DIR and CLAUDE_CONFIG_DIR")
	}
	c, sess := newTestSession(t)
	ctx := context.Background()
	ch, _ := subscribe(t, c)

	claude := os.ExpandEnv("$HOME/.local/bin/claude")
	w, err := c.NewWindow(ctx, sess, "claude", dir,
		map[string]string{"CLAUDE_CONFIG_DIR": profile},
		[]string{claude, "--model", "haiku", "--permission-mode", "default"})
	if err != nil {
		t.Fatal(err)
	}
	isStatus := func(state string) func(Event) bool {
		return func(e Event) bool {
			return e.Kind == ProgramStatusChanged && e.BlockID == w.BlockID && e.Status.State == state
		}
	}
	waitFor(t, ch, 45*time.Second, isStatus("idle"))

	if err := c.Type(ctx, sess, w.BlockID, "reply with the single word ok"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, 45*time.Second, isStatus("working"))
	waitFor(t, ch, 60*time.Second, isStatus("done"))

	if err := c.Type(ctx, sess, w.BlockID, "run the command: touch x"); err != nil {
		t.Fatal(err)
	}
	ev := waitFor(t, ch, 60*time.Second, isStatus("blocked"))
	if ev.Status.Kind != "permission" || !strings.Contains(ev.Status.Msg, "touch x") {
		t.Fatalf("blocked event %+v, want kind permission naming the command", ev.Status)
	}

	rs, err := c.ProgramStatus(ctx, sess, w.BlockID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].State != "blocked" || rs[0].Kind != "permission" {
		t.Fatalf("ProgramStatus %+v, want one blocked/permission record", rs)
	}
	screen, err := c.Capture(ctx, sess, w.BlockID)
	if err != nil || !strings.Contains(screen, "Do you want to proceed?") {
		t.Fatalf("Capture err=%v, screen lacks the permission prompt:\n%s", err, screen)
	}
	if err := c.Focus(ctx, sess, w.BlockID); err != nil {
		t.Fatalf("focus: %v", err)
	}
}
