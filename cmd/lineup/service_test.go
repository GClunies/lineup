package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) (manager, *[]string) {
	t.Helper()
	dir := t.TempDir()
	var calls []string
	loaded := false
	m := manager{
		svc: service{
			Label:   "test.lineup",
			Program: []string{"/opt/my tools/lineup", "serve"},
			Env:     map[string]string{"PATH": "/a b:/usr/bin&co"},
			Log:     filepath.Join(dir, "state", "scheduler.log"),
		},
		plistDir: filepath.Join(dir, "LaunchAgents"),
		uid:      501,
		sleep:    func(time.Duration) {},
	}
	m.ctl = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "print":
			if !loaded {
				return os.ErrNotExist
			}
		case "bootstrap":
			loaded = true
		case "bootout":
			loaded = false
		}
		return nil
	}
	return m, &calls
}

func TestPlistHoldsProgramEnvAndLog(t *testing.T) {
	m, _ := testManager(t)
	p := string(m.svc.plist())
	for _, want := range []string{"/opt/my tools/lineup", "<string>serve</string>", "/a b:/usr/bin&amp;co", m.svc.Log, "<key>KeepAlive</key>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil missing")
	}
	f := filepath.Join(t.TempDir(), "x.plist")
	os.WriteFile(f, []byte(p), 0o644)
	if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %s", out)
	}
}

func TestEnsureLoadsOnceAndReloadsOnChange(t *testing.T) {
	m, calls := testManager(t)
	if _, err := m.ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.plistPath()); err != nil {
		t.Fatal("plist not written")
	}
	want := []string{"print gui/501/test.lineup", "bootstrap gui/501 " + m.plistPath()}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("first ensure ran %q", *calls)
	}

	*calls = nil
	if _, err := m.ensure(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, "|") != "print gui/501/test.lineup" {
		t.Fatalf("second ensure ran %q", *calls)
	}

	*calls = nil
	m.svc.Env["LINEUP_STATE_DIR"] = "/elsewhere"
	if _, err := m.ensure(); err != nil {
		t.Fatal(err)
	}
	want = []string{"print gui/501/test.lineup", "bootout gui/501/test.lineup", "print gui/501/test.lineup", "bootstrap gui/501 " + m.plistPath()}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("changed ensure ran %q", *calls)
	}
	b, _ := os.ReadFile(m.plistPath())
	if !strings.Contains(string(b), "/elsewhere") {
		t.Fatal("plist not rewritten")
	}
}

// lingering makes launchd keep the agent for n more prints after a bootout, and
// refuse a bootstrap until then, as it does while the old Scheduler exits.
func lingering(m *manager, n int) {
	inner, left := m.ctl, 0
	m.ctl = func(args ...string) error {
		switch {
		case args[0] == "bootout":
			left = n
		case args[0] == "print" && left > 0:
			left--
			return nil
		case args[0] == "bootstrap" && left > 0:
			return errors.New("Bootstrap failed: 5: Input/output error")
		}
		return inner(args...)
	}
}

func TestEnsureWaitsForTheBootoutBeforeBootstrap(t *testing.T) {
	m, calls := testManager(t)
	m.ensure()
	lingering(&m, 3)
	m.svc.Env["PATH"] = "/another/shell"
	if _, err := m.ensure(); err != nil {
		t.Fatal(err)
	}
	if last := (*calls)[len(*calls)-1]; last != "bootstrap gui/501 "+m.plistPath() {
		t.Fatalf("last call %q", last)
	}
}

func TestEnsureGivesUpWhenTheAgentStays(t *testing.T) {
	m, calls := testManager(t)
	m.ensure()
	lingering(&m, 1000)
	m.svc.Env["PATH"] = "/another/shell"
	*calls = nil
	if _, err := m.ensure(); err == nil || !strings.Contains(err.Error(), "still has") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "bootstrap") {
			t.Fatal("bootstrapped while launchd still had the agent")
		}
	}
}

func TestStartLeavesALoadedAgentAlone(t *testing.T) {
	m, calls := testManager(t)
	if err := m.start(); err != nil {
		t.Fatal(err)
	}
	if last := (*calls)[len(*calls)-1]; last != "bootstrap gui/501 "+m.plistPath() {
		t.Fatalf("first start ran %q", *calls)
	}

	*calls = nil
	m.svc.Env["PATH"] = "/another/shell"
	if err := m.start(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, "|"); got != "print gui/501/test.lineup" {
		t.Fatalf("start with a new PATH ran %q", got)
	}
	if b, _ := os.ReadFile(m.plistPath()); strings.Contains(string(b), "/another/shell") {
		t.Fatal("start rewrote the plist of a loaded agent")
	}
}

func TestStopReportsWhetherTheAgentWasLoaded(t *testing.T) {
	m, calls := testManager(t)
	if was, err := m.stop(); was || err != nil {
		t.Fatalf("stop when unloaded: %v %v", was, err)
	}
	m.ensure()
	*calls = nil
	if was, err := m.stop(); !was || err != nil {
		t.Fatalf("stop when loaded: %v %v", was, err)
	}
	if got := strings.Join(*calls, "|"); got != "print gui/501/test.lineup|bootout gui/501/test.lineup" {
		t.Fatalf("stop ran %q", got)
	}
}

func TestRestartKicksALoadedAgent(t *testing.T) {
	m, calls := testManager(t)
	m.ensure()
	*calls = nil
	if err := m.restart(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, "|"); got != "print gui/501/test.lineup|kickstart -k gui/501/test.lineup" {
		t.Fatalf("restart ran %q", got)
	}
}

func TestRestartDoesNotKickAnAgentItJustLoaded(t *testing.T) {
	m, calls := testManager(t)
	if err := m.restart(); err != nil {
		t.Fatal(err)
	}
	m.svc.Env["PATH"] = "/another/shell"
	if err := m.restart(); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "kickstart") {
			t.Fatalf("kicked a Scheduler that bootstrap had just started: %q", *calls)
		}
	}
}

func TestWaitListeningReturnsOnceTheSchedulerListens(t *testing.T) {
	dir, _ := os.MkdirTemp("", "lu")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	if err := waitListening(sock, 200*time.Millisecond); err == nil {
		t.Fatal("no Scheduler listens, yet the wait passed")
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		ln, err := net.Listen("unix", sock)
		if err == nil {
			t.Cleanup(func() { ln.Close() })
		}
	}()
	start := time.Now()
	if err := waitListening(sock, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Fatalf("returned after %v, want just after the listener started at 300ms", d)
	}
}

func TestRestartRefusesABadConfigAndKeepsTheScheduler(t *testing.T) {
	m, calls := testManager(t)
	m.config = filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(m.config, []byte("[[review.queues]]\nname = \"q\"\nrepos = [\"acme/data\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.ensure()
	*calls = nil
	if err := m.restart(); err == nil || !strings.Contains(err.Error(), "repo") {
		t.Fatalf("restart with a bad config: %v, want the load error", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("restart with a bad config ran %q", *calls)
	}
}
