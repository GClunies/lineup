package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
)

const logKeep = 1 << 20

// trimLog keeps the last keep bytes of the log, from the first whole line.
// launchd does not rotate the log, so each Scheduler start trims it.
func trimLog(path string, keep int64) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && int64(len(b)) <= keep {
		return nil
	}
	if err != nil {
		return err
	}
	tail := b[int64(len(b))-keep:]
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return os.WriteFile(path, tail, 0o644)
}
