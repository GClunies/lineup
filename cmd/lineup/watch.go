package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
	"github.com/gclunies/lineup/internal/scheduler"
)

type watcher interface {
	Watch(f func(scheduler.BoardView) bool) error
}

// cmdWatch prints one line each time a build Task starts to need the user or
// ends. The review tab's board toasts the review Tasks' alerts. With --once,
// it exits after the first Board that has new alerts. The Orchestrator runs
// it that way as a background command, which wakes the Orchestrator only when
// it exits. A Monitor would wake it each time the Monitor expires, to no
// purpose. It reconnects when the Scheduler restarts.
func cmdWatch(c scheduler.Client, args []string, out io.Writer) error {
	var once bool
	if _, err := parse("watch", "[--once]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&once, "once", false, "exit after the first new alerts")
	}); err != nil {
		return err
	}
	release, err := lockOne(stateDir(), "watch.lock", "lineup watch")
	if err != nil {
		return err
	}
	defer release()
	path := filepath.Join(stateDir(), "alerted.json")
	for {
		printed, err := watchOnce(c, path, once, out)
		if err != nil || printed && once {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

// watchOnce follows one connection to the Scheduler. It returns only errors of
// the alerts file, because the caller retries a lost connection. path keeps
// the alerts already printed, so a restart of the watch repeats none of them.
// With no file yet, Tasks that are already Done are old news and print nothing.
// With once, it ends the connection after the first Board that prints. It
// reports whether it printed.
func watchOnce(w watcher, path string, once bool, out io.Writer) (bool, error) {
	seen, err := loadAlerted(path)
	if err != nil {
		return false, err
	}
	var printed bool
	var saveErr error
	w.Watch(func(v scheduler.BoardView) bool {
		v = v.OfKind(lifecycle.KindBuild)
		if seen == nil {
			seen = map[string]string{}
			for _, t := range v.Tasks {
				if t.StageKey == lifecycle.Done.Key() {
					seen[t.ID] = t.Attention()
				}
			}
		}
		for _, t := range scheduler.Alerts(seen, v) {
			fmt.Fprintln(out, t.AlertText())
			printed = true
		}
		if err := saveAlerted(path, seen); err != nil {
			saveErr = err
		}
		return !(once && printed)
	})
	return printed, saveErr
}

func loadAlerted(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	return seen, json.Unmarshal(raw, &seen)
}

func saveAlerted(path string, seen map[string]string) error {
	raw, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
