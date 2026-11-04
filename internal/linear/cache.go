package linear

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
)

// The cache lets the Board paint from disk at once and refresh in the
// background. A key is "<workspaceID>/<view>", for example "w1/my". Each key
// is one file, so two writers never lose each other's save. The files hold
// titles, identifiers, and statuses, readable by the user alone.

func cachePath(key string) string {
	return filepath.Join(stateDir(), "cache", url.PathEscape(key)+".json")
}

// ReadCached returns the Issues last stored under key. ok is false when
// nothing usable is stored.
func ReadCached(key string) (issues []Issue, ok bool) {
	data, err := os.ReadFile(cachePath(key))
	if err != nil || json.Unmarshal(data, &issues) != nil {
		return nil, false
	}
	return issues, true
}

// WriteCache stores issues under key. The write is best-effort: a cache that
// cannot be written costs the next start a slower first paint.
func WriteCache(key string, issues []Issue) {
	if issues == nil {
		issues = []Issue{} // an empty list that loaded is not a list never loaded
	}
	data, err := json.Marshal(issues)
	path := cachePath(key)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "w-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(tmp.Name(), path)
	}
}

// DropCache forgets every key of one workspace, on sign-out.
func DropCache(workspaceID string) {
	files, _ := filepath.Glob(filepath.Join(stateDir(), "cache", url.PathEscape(workspaceID+"/")+"*.json"))
	for _, f := range files {
		_ = os.Remove(f)
	}
}
