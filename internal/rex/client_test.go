package rex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRex is a rex that keeps its sessions in a file. session new pauses, so
// two callers that both miss the session would both create one.
func fakeRex(t *testing.T) (Client, string) {
	dir := t.TempDir()
	state := filepath.Join(dir, "sessions")
	os.WriteFile(state, nil, 0o644)
	script := `#!/bin/sh
state="` + state + `"
case "$1 $2" in
"session ls")
	printf '{"sessions":['; sep=""
	while read -r l; do printf '%s{"session_id":"id-%s","label":"%s"}' "$sep" "$l" "$l"; sep=","; done < "$state"
	printf ']}\n' ;;
"session new")
	sleep 0.2; echo "$3" >> "$state"
	printf '{"session_id":"id-%s","initial_windows":[]}\n' "$3" ;;
esac
`
	bin := filepath.Join(dir, "rex")
	os.WriteFile(bin, []byte(script), 0o755)
	return Client{Bin: bin}, state
}

func TestEnsureSessionCreatesOneSessionForConcurrentCallers(t *testing.T) {
	c, state := fakeRex(t)
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := c.EnsureSession(context.Background(), "data")
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	b, _ := os.ReadFile(state)
	if n := strings.Count(string(b), "data\n"); n != 1 || ids[0] != ids[1] {
		t.Fatalf("created %d data sessions, ids %q", n, ids)
	}
}

func TestIsNotFoundReadsOnlyARexTargetThatIsMissing(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"missing session", errors.New(`rex session inspect --json session:1: exit status 3: Error: session target "session:1" not found`), true},
		{"missing window", errors.New(`rex window close -s session:1 window:2: exit status 3: Error: window target "window:2" not found`), true},
		{"missing block in a missing session", errors.New(`rex session inspect: exit status 3: Error: block target "block:3" not found: session scope: session "session:1" not found`), true},
		{"missing binary", errors.New(`rex session inspect --json session:1: exec: "rex": executable file not found in $PATH: `), false},
		{"server down", errors.New(`rex session inspect: exit status 1: Error: server socket not found`), false},
		{"no error", nil, false},
	}
	for _, c := range cases {
		if got := IsNotFound(c.err); got != c.want {
			t.Errorf("%s: IsNotFound = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAMissingBinaryIsNotAMissingSession(t *testing.T) {
	c := Client{Bin: "lineup-no-such-rex"}
	if err := c.DestroySession(context.Background(), "session:1"); err == nil {
		t.Fatal("DestroySession with no rex binary returned nil, want the error")
	}
	if err := c.CloseWindow(context.Background(), "session:1", "window:1"); err == nil {
		t.Fatal("CloseWindow with no rex binary returned nil, want the error")
	}
}
