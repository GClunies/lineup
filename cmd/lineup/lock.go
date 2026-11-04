package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lockServe takes the single-Scheduler lock in dir.
func lockServe(dir string) (release func(), err error) {
	return lockOne(dir, "serve.lock", "Scheduler")
}

// lockOne takes the lock file name in dir, so that only one process of the
// kind who runs. The kernel drops the lock when the process dies, so a crash
// leaves no stale lock.
func lockOne(dir, name, who string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		defer f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s lock: %w", name, err)
		}
		buf := make([]byte, 32)
		n, _ := f.Read(buf)
		return nil, fmt.Errorf("another %s is running (pid %s)", who, strings.TrimSpace(string(buf[:n])))
	}
	f.Truncate(0)
	f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
