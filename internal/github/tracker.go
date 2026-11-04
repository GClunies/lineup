package github

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// trackerFile records that the user made GitHub a Tracker. lineup always
// needs gh for PRs, so a signed-in gh alone does not.
func trackerFile(stateDir string) string { return filepath.Join(stateDir, "github.json") }

type trackerRecord struct {
	Login string `json:"login"`
}

// SaveTracker makes GitHub a Tracker for the account login.
func SaveTracker(stateDir, login string) error {
	data, err := json.Marshal(trackerRecord{Login: login})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	tmp := trackerFile(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, trackerFile(stateDir))
}

// TrackerLogin reports the account when GitHub is a Tracker.
func TrackerLogin(stateDir string) (login string, on bool, err error) {
	data, err := os.ReadFile(trackerFile(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var r trackerRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return "", false, err
	}
	return r.Login, true, nil
}

// ForgetTracker stops GitHub being a Tracker. It reports false when it was not one.
func ForgetTracker(stateDir string) (bool, error) {
	err := os.Remove(trackerFile(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
