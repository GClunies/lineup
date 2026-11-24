package rex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
"session rename")
	echo "$3 $4" >> "$state.renamed" ;;
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

func TestRenameSessionLeavesATakenLabelAndAMissingSession(t *testing.T) {
	c, state := fakeRex(t)
	os.WriteFile(state, []byte("data\nacme/web\n"), 0o644)
	ctx := context.Background()
	for _, call := range [][2]string{{"id-data", "acme/data"}, {"id-web", "acme/web"}, {"id-gone", "acme/gone"}} {
		if err := c.RenameSession(ctx, call[0], call[1]); err != nil {
			t.Fatalf("RenameSession(%s, %s): %v", call[0], call[1], err)
		}
	}
	b, _ := os.ReadFile(state + ".renamed")
	if got, want := string(b), "id-data acme/data\n"; got != want {
		t.Fatalf("renames = %q, want %q", got, want)
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

func TestTypeWritesEachPartThenEnter(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "writes")
	bin := filepath.Join(dir, "rex")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$8\" >> \""+log+"\"\n"), 0o755)
	c := Client{Bin: bin}
	if err := c.Type(context.Background(), "s", "b", "/skill ", "body"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	if want := "{\"data\":\"/skill \"}\n{\"data\":\"body\"}\n{\"data\":\"\\r\"}\n"; string(got) != want {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

func TestAFailedTypeClearsTheInputBox(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "writes")
	bin := filepath.Join(dir, "rex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$8\" >> \"" + log + "\"\ncase \"$8\" in *alpha*) echo 'write failed' >&2; exit 1;; esac\n"
	os.WriteFile(bin, []byte(script), 0o755)
	c := Client{Bin: bin}
	if err := c.Type(context.Background(), "s", "b", "/skill ", "alpha\nbeta"); err == nil {
		t.Fatal("Type returned no error for a failed write")
	}
	got, _ := os.ReadFile(log)
	if want := "{\"data\":\"/skill \"}\n{\"data\":\"alpha\\nbeta\"}\n" + strings.Repeat("{\"data\":\"\\u0015\"}\n", 4); string(got) != want {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

// The records come from a live Rex at API revision 18.
func TestIdleShellReadsOnlyALoginShellWithNothingInItsForeground(t *testing.T) {
	cases := []struct {
		name, out string
		want      bool
	}{
		{"a restored tab", `{"last_exit":null,"child":{"pid":93443,"name":"login","path":"/usr/bin/login"},"foreground":{"pid":93468,"name":"zsh","path":"/bin/zsh","argv0":"-zsh"},"ancestors":[{"pid":93443,"name":"login","path":"/usr/bin/login"}]}`, true},
		{"a shell that runs claude", `{"last_exit":null,"child":{"pid":93510,"name":"login","path":"/usr/bin/login"},"foreground":{"pid":95752,"name":"2.1.311","argv0":"claude"},"ancestors":[{"pid":93616,"name":"zsh","path":"/bin/zsh"},{"pid":93510,"name":"login","path":"/usr/bin/login"}]}`, false},
		{"a Worker that lineup started", `{"last_exit":null,"child":{"pid":51237,"name":"2.1.311","path":"/Users/g/.local/share/claude/versions/2.1.311","argv0":"/Users/g/.local/bin/claude"},"foreground":{"pid":51237,"name":"2.1.311"}}`, false},
		{"the Board", `{"last_exit":null,"child":{"pid":98430,"name":"lineup","path":"/Users/g/go/bin/lineup"},"foreground":{"pid":98430,"name":"lineup"}}`, false},
	}
	for _, c := range cases {
		got, err := idleShell([]byte(c.out))
		if err != nil || got != c.want {
			t.Errorf("%s: idleShell = %v, %v, want %v", c.name, got, err, c.want)
		}
	}
	if _, err := idleShell([]byte("not json")); err == nil {
		t.Error("idleShell read text that is not JSON")
	}
}

// The records come from a live Rex at API revision 18: a side pane that lineup
// split, a plain split where the user typed hunk, a Worker, a rexweb pane,
// and a kept block.
func TestForegroundNamesTheProgramThatABlockRuns(t *testing.T) {
	cases := []struct {
		name, out string
		want      Process
	}{
		{"a side pane", `{"last_exit":null,"child":{"pid":28928,"name":"hunk","path":"/opt/homebrew/Cellar/hunk/0.24.1/libexec/hunk","cwd":"/wt/data.pd-762"},"foreground":{"pid":28928,"name":"hunk","path":"/opt/homebrew/Cellar/hunk/0.24.1/libexec/hunk","cwd":"/wt/data.pd-762"},"home":"/Users/g"}`, Process{"hunk", 28928}},
		{"hunk typed in a shell", `{"last_exit":null,"child":{"pid":11749,"name":"login","path":"/usr/bin/login"},"foreground":{"pid":12015,"name":"hunk","path":"/opt/homebrew/Cellar/hunk/0.24.1/libexec/hunk","cwd":"/wt/data.pd-762"},"ancestors":[{"pid":11753,"name":"zsh","path":"/bin/zsh"},{"pid":11749,"name":"login","path":"/usr/bin/login"}],"home":"/Users/g"}`, Process{"hunk", 12015}},
		{"a Worker", `{"last_exit":null,"child":{"pid":22873,"name":"2.1.323","path":"/Users/g/.local/share/claude/versions/2.1.323"},"foreground":{"pid":22873,"name":"2.1.323","cwd":"/wt/data.pd-762"},"home":"/Users/g"}`, Process{"2.1.323", 22873}},
		{"a rexweb pane", `{"last_exit":null,"child":{"pid":62174,"user_id":504,"user":"g","name":"bash","path":"/bin/bash","invoked_path":"/bin/bash","argv0":"/bin/bash","cwd":"/wt/data.review-pr-5132"},"foreground":{"pid":62174,"user_id":504,"user":"g","name":"bash","path":"/bin/bash","invoked_path":"/bin/bash","argv0":"/bin/bash","cwd":"/wt/data.review-pr-5132"},"home":"/Users/g"}`, Process{"bash", 62174}},
		{"a kept block after exit 0", `{"last_exit":{"exit_code":0},"home":"/Users/g"}`, Process{}},
	}
	for _, c := range cases {
		if got, err := foreground([]byte(c.out)); err != nil || got != c.want {
			t.Errorf("%s: foreground = %+v, %v, want %+v", c.name, got, err, c.want)
		}
	}
	if _, err := foreground([]byte("not json")); err == nil {
		t.Error("foreground read text that is not JSON")
	}
}

// The record comes from a live Rex: a Worker's tab with its side pane and a
// plain split.
func TestViewReadsTheLabelOfEachBlock(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "inspect.json")
	os.WriteFile(record, []byte(`{"revision":3,"session_id":"session:1","label":"data","windows":[{"window_id":"window:1","label":"ENG-1 Add billing model","layers":[{"layer_id":"layer:1","kind":"tiled","blocks":[{"block_id":"block:worker","label":"shell"},{"block_id":"block:side","label":"side"},{"block_id":"block:typed","label":"shell"}]}]}]}`), 0o644)
	bin := filepath.Join(dir, "rex")
	os.WriteFile(bin, []byte("#!/bin/sh\ncat \""+record+"\"\n"), 0o755)
	c := Client{Bin: bin}
	v, err := c.View(context.Background(), "session:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Windows) != 1 || !slices.Equal(v.Windows[0].BlockIDs, []string{"block:worker", "block:side", "block:typed"}) ||
		!slices.Equal(v.Windows[0].Labels, []string{"shell", "side", "shell"}) {
		t.Fatalf("view %+v, want three blocks with their labels", v)
	}
}

// The records come from a live Rex.
func TestLastExitReadsOnlyAKeptBlockWhoseCommandExited(t *testing.T) {
	cases := []struct {
		name, out string
		code      int
		exited    bool
	}{
		{"a kept block after exit 0", `{"last_exit":{"exit_code":0},"home":"/Users/g"}`, 0, true},
		{"a kept block after exit 3", `{"last_exit":{"exit_code":3,"error":"exit status 3"},"home":"/Users/g"}`, 3, true},
		{"a running command", `{"last_exit":null,"child":{"pid":22579,"name":"sleep","path":"/bin/sleep"},"foreground":{"pid":22579,"name":"sleep"}}`, 0, false},
		{"a plain shell", `{"last_exit":null,"child":{"pid":22591,"name":"login","path":"/usr/bin/login"},"foreground":{"pid":22601,"name":"zsh","path":"/bin/zsh"},"ancestors":[{"pid":22591,"name":"login","path":"/usr/bin/login"}]}`, 0, false},
	}
	for _, c := range cases {
		code, exited, err := lastExit([]byte(c.out))
		if err != nil || code != c.code || exited != c.exited {
			t.Errorf("%s: lastExit = %d, %v, %v, want %d, %v", c.name, code, exited, err, c.code, c.exited)
		}
	}
	if _, _, err := lastExit([]byte("not json")); err == nil {
		t.Error("lastExit read text that is not JSON")
	}
}
