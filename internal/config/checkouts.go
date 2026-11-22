package config

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Checkout is a git checkout below a root.
type Checkout struct {
	Path string // absolute, cleaned
	Key  string // owner/repo from a GitHub origin, else local/<folder name>
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?/?$`)

// checkoutKey is owner/repo from the checkout's GitHub origin, else
// local/<folder name>.
func checkoutKey(path string) string {
	out, err := exec.Command("git", "-C", path, "remote", "get-url", "origin").Output()
	if err == nil {
		if m := githubRemote.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
			return m[1]
		}
	}
	return "local/" + filepath.Base(path)
}

// keyOf is the Repo key of the checkout at path. It runs git once for each
// path.
func (g *Global) keyOf(path string) string {
	path = filepath.Clean(expand(g.home, path))
	g.mu.Lock()
	defer g.mu.Unlock()
	if k, ok := g.keys[path]; ok {
		return k
	}
	g.keys[path] = checkoutKey(path)
	return g.keys[path]
}

// findCheckouts lists the git checkouts at each root, its children, and its
// grandchildren, sorted by path. The walk does not go into a checkout or
// into a folder whose name starts with a dot.
func findCheckouts(home string, roots []string) []string {
	found := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			found[dir] = true
			return
		}
		if depth == 2 {
			return
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			sub := filepath.Join(dir, e.Name())
			if fi, err := os.Stat(sub); err == nil && fi.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				walk(sub, depth+1)
			}
		}
	}
	for _, r := range roots {
		walk(filepath.Clean(expand(home, r)), 0)
	}
	return slices.Sorted(maps.Keys(found))
}

// Checkouts lists the git checkouts below the roots, sorted by path. It
// walks the roots on each call, so a new clone shows up at once.
func (g *Global) Checkouts() []Checkout {
	var out []Checkout
	for _, p := range findCheckouts(g.home, g.file.Roots) {
		out = append(out, Checkout{Path: p, Key: g.keyOf(p)})
	}
	return out
}

// checkoutFor picks the checkout of the Repo key, which ignores case. Of
// several checkouts with the key, it picks the one named like the repo, if
// only one is. all lists every checkout with the key.
func (g *Global) checkoutFor(key string) (path string, all []string) {
	_, repo, _ := strings.Cut(key, "/")
	var named []string
	for _, c := range g.Checkouts() {
		if strings.EqualFold(c.Key, key) {
			all = append(all, c.Path)
			if strings.EqualFold(filepath.Base(c.Path), repo) {
				named = append(named, c.Path)
			}
		}
	}
	switch {
	case len(all) == 1:
		return all[0], all
	case len(named) == 1:
		return named[0], all
	}
	return "", all
}

// CheckoutFor is the checkout of the Repo key, for example the checkout of
// a GitHub Issue's repository.
func (g *Global) CheckoutFor(key string) (string, bool) {
	path, _ := g.checkoutFor(key)
	return path, path != ""
}

// FindCheckout turns the --repo of lineup add, or the repo of a Local Issue,
// into a checkout. The name is a path, an owner/repo key, or the folder name
// of one checkout below the roots.
func (g *Global) FindCheckout(name string) (string, error) {
	isPath := strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~/") || strings.HasPrefix(name, ".")
	if !isPath {
		if _, repo, ok := splitKey(name); ok && repo != "*" {
			path, all := g.checkoutFor(name)
			switch {
			case path != "":
				return path, nil
			case len(all) > 1:
				return "", fmt.Errorf("%d checkouts have the origin %s and none is named %s: pass a path, %s", len(all), name, repo, strings.Join(all, " or "))
			}
		}
		if !strings.Contains(name, "/") {
			return g.checkoutNamed(name)
		}
	}
	abs, err := filepath.Abs(expand(g.home, name))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return "", fmt.Errorf("%s is not a git checkout", abs)
	}
	return abs, nil
}

// checkoutNamed is the one checkout below the roots with the folder name.
func (g *Global) checkoutNamed(name string) (string, error) {
	var paths, keys []string
	for _, c := range g.Checkouts() {
		if filepath.Base(c.Path) == name {
			paths, keys = append(paths, c.Path), append(keys, c.Key)
		}
	}
	switch len(paths) {
	case 0:
		roots := cmp.Or(strings.Join(g.file.Roots, ", "), "(none)")
		return "", fmt.Errorf("no checkout named %s below roots %s: add its parent folder to roots or pass a path", name, roots)
	case 1:
		return paths[0], nil
	}
	slices.Sort(keys)
	// Two checkouts of one Repo have one key, so only their paths tell them apart.
	if len(slices.Compact(slices.Clone(keys))) < len(keys) {
		keys = paths
	}
	return "", fmt.Errorf("%d checkouts named %s: pass %s", len(paths), name, strings.Join(keys, " or "))
}
