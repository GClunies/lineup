package linear

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// stateDir is a var so tests can point it at a temp folder.
var stateDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "lineup", "linear")
}

// lockWait bounds how long a process waits for another's token change.
var lockWait = 15 * time.Second

// lockTokens takes the lock that every change to the stored tokens goes
// through (refresh, sign-in, sign-out), shared by all lineup processes. It
// gives up after lockWait or when ctx ends. A lock that cannot be taken is an
// error, never a silent unlocked change.
func lockTokens(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return nil, fmt.Errorf("token lock: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(stateDir(), "token.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("token lock: %w", err)
	}
	deadline := time.Now().Add(lockWait)
	for {
		// A cancelled command must not change tokens because the lock was free.
		if ctx.Err() != nil {
			f.Close()
			return nil, ctx.Err()
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("token lock: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("another lineup is busy updating your Linear sign-in; try again in a moment")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
