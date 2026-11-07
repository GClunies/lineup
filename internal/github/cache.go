package github

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
)

// PRCache keeps each view's last search in Dir, one file per key, so the
// review tab paints at once and two writers never lose each other's save.
type PRCache struct{ Dir string }

func (c PRCache) path(key string) string {
	return filepath.Join(c.Dir, url.PathEscape(key)+".json")
}

// Read returns the PRs last stored under key. ok is false when nothing usable is stored.
func (c PRCache) Read(key string) (prs []PullRequest, ok bool) {
	data, err := os.ReadFile(c.path(key))
	if err != nil || json.Unmarshal(data, &prs) != nil {
		return nil, false
	}
	return prs, true
}

// Write stores prs under key. The write is best-effort: a cache that cannot
// be written costs the next start a slower first paint.
func (c PRCache) Write(key string, prs []PullRequest) {
	if prs == nil {
		prs = []PullRequest{} // an empty list that loaded is not a list never loaded
	}
	data, err := json.Marshal(prs)
	if err != nil || os.MkdirAll(c.Dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(c.Dir, "w-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(tmp.Name(), c.path(key))
	}
}
