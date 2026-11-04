package linear

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Workspace is one Linear workspace the user has signed in to. Its tokens sit
// in the secret store under account(ID).
type Workspace struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URLKey string `json:"urlKey"`
}

// workspaceIndex is workspaces.json in the state folder: the signed-in
// workspaces. It holds no secrets.
type workspaceIndex struct {
	Workspaces []Workspace `json:"workspaces"`
}

func indexPath() string { return filepath.Join(stateDir(), "workspaces.json") }

var errBrokenIndex = errors.New("unreadable")

func parseIndex() (workspaceIndex, error) {
	var ix workspaceIndex
	data, err := os.ReadFile(indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	} else if err != nil {
		return ix, err
	}
	if err := json.Unmarshal(data, &ix); err != nil {
		return workspaceIndex{}, errBrokenIndex
	}
	return ix, nil
}

// Workspaces lists the workspaces the user has signed in to.
func Workspaces() ([]Workspace, error) {
	ix, err := parseIndex()
	if !errors.Is(err, errBrokenIndex) {
		return ix.Workspaces, err
	}
	unlock, err := lockTokens(context.Background())
	if err != nil {
		return nil, err
	}
	defer unlock()
	ix, err = readIndexLocked()
	return ix.Workspaces, err
}

// readIndexLocked reads the index; the caller holds the token lock. An
// unreadable index moves to workspaces.json.broken. The user signs in again,
// which lists each workspace again.
func readIndexLocked() (workspaceIndex, error) {
	ix, err := parseIndex()
	if !errors.Is(err, errBrokenIndex) {
		return ix, err
	}
	if err := os.Rename(indexPath(), indexPath()+".broken"); err != nil {
		return workspaceIndex{}, fmt.Errorf("%s is unreadable and couldn't be set aside: %w", indexPath(), err)
	}
	return workspaceIndex{}, nil
}

// updateIndexLocked changes the index and writes it whole or not at all.
func updateIndexLocked(change func(*workspaceIndex)) error {
	ix, err := readIndexLocked()
	if err != nil {
		return err
	}
	change(&ix)
	data, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir(), "workspaces-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), indexPath())
}

func (ix *workspaceIndex) add(w Workspace) {
	for i := range ix.Workspaces {
		if ix.Workspaces[i].ID == w.ID {
			ix.Workspaces[i] = w // a renamed workspace keeps its ID
			return
		}
	}
	ix.Workspaces = append(ix.Workspaces, w)
}

func (ix *workspaceIndex) remove(id string) {
	ix.Workspaces = slices.DeleteFunc(ix.Workspaces, func(w Workspace) bool { return w.ID == id })
}

// find matches a workspace as typed on the command line: its URL key, which
// is unique, else its name when only one workspace has it.
func (ix workspaceIndex) find(s string) (*Workspace, error) {
	var named []*Workspace
	for i := range ix.Workspaces {
		w := &ix.Workspaces[i]
		if strings.EqualFold(w.URLKey, s) {
			return w, nil
		}
		if strings.EqualFold(w.Name, s) {
			named = append(named, w)
		}
	}
	switch len(named) {
	case 0:
		return nil, fmt.Errorf("not signed in to a workspace called %q", s)
	case 1:
		return named[0], nil
	}
	keys := make([]string, len(named))
	for i, w := range named {
		keys[i] = w.URLKey
	}
	return nil, fmt.Errorf("more than one workspace is called %q; name it by its URL key: %s", s, strings.Join(keys, ", "))
}
