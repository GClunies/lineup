package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// RepoChoices remembers which Repo the user picked for a Linear Project.
type RepoChoices struct {
	mu   sync.Mutex
	path string
}

func NewRepoChoices(path string) *RepoChoices { return &RepoChoices{path: path} }

func DefaultRepoChoicesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "lineup", "repos.json"), nil
}

func (c *RepoChoices) read() (map[string]string, error) {
	m := map[string]string{}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(data, &m)
}

func (c *RepoChoices) Get(projectID string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.read()
	repo, ok := m[projectID]
	return repo, ok, err
}

func (c *RepoChoices) Set(projectID, repoPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.read()
	if err != nil {
		return err
	}
	m[projectID] = repoPath
	return WriteJSON(c.path, m)
}

// ViewOrder remembers the user's rank of their Linear views, as View IDs.
type ViewOrder struct {
	mu   sync.Mutex
	path string
}

func NewViewOrder(path string) *ViewOrder { return &ViewOrder{path: path} }

func DefaultViewOrderPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "lineup", "views.json"), nil
}

// Get returns the saved View IDs, best first. With nothing saved it returns none.
func (v *ViewOrder) Get() ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	data, err := os.ReadFile(v.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	return ids, json.Unmarshal(data, &ids)
}

func (v *ViewOrder) Set(ids []string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return WriteJSON(v.path, ids)
}

// WriteJSON replaces the file at path in one rename, so a reader never sees half a file.
func WriteJSON(path string, x any) error {
	data, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
