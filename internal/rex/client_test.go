package rex

import (
	"context"
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
