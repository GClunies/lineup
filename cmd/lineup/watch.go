package main

import (
	"encoding/json"
	"errors"
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
	Watch(f func(scheduler.BoardView)) error
}

// cmdWatch prints one line each time a build Task starts to need the user or
// ends. The review tab's board toasts the review Tasks' alerts.
// The Orchestrator runs it under Claude's Monitor tool, which wakes the
// Orchestrator for each line. It reconnects when the Scheduler restarts.
func cmdWatch(c scheduler.Client, args []string, out io.Writer) error {
	if _, err := parse("watch", "", args, nil); err != nil {
		return err
	}
	release, err := lockOne(stateDir(), "watch.lock", "lineup watch")
	if err != nil {
		return err
	}
	defer release()
	path := filepath.Join(stateDir(), "alerted.json")
	for {
		if err := watchOnce(c, path, out); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

// watchOnce follows one connection to the Scheduler. It returns only errors of
// the alerts file, because the caller retries a lost connection. path keeps
// the alerts already printed, so a restart of the watch repeats none of them.
// With no file yet, Tasks that are already Done are old news and print nothing.
func watchOnce(w watcher, path string, out io.Writer) error {
	seen, err := loadAlerted(path)
	if err != nil {
		return err
	}
	var saveErr error
	w.Watch(func(v scheduler.BoardView) {
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
		}
		if err := saveAlerted(path, seen); err != nil {
			saveErr = err
		}
	})
	return saveErr
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
