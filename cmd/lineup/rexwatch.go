package main

import (
	"context"
	"errors"
	"time"
)

var errRexRestarted = errors.New("rex server restarted; exiting so that recovery runs on the next start")

// watchRexServer returns errRexRestarted when the Rex server's instance id
// differs from start. A failed read is transient and is passed to warn.
// Exiting lets the service manager start the Scheduler again. Startup recovery
// then flags each lost Worker, and the user restores it.
func watchRexServer(ctx context.Context, every time.Duration, start string, read func(context.Context) (string, error), warn func(string, ...any)) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		id, err := read(ctx)
		if err != nil {
			warn("rex server status failed", "err", err)
			continue
		}
		if id != start {
			return errRexRestarted
		}
	}
}
