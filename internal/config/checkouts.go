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

// isMainCheckout reports whether dir is a main git checkout, whose .git is a
// folder. A linked worktree has a .git file and is never a Repo.
func isMainCheckout(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && fi.IsDir()
}

// findCheckouts lists the main git checkouts at each root, its children, and
// its grandchildren, sorted by path. The walk does not go into a folder with
// a .git folder or file, or into a folder whose name starts with a dot.
func findCheckouts(home string, roots []string) []string {
	found := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if fi.IsDir() {
				found[dir] = true
			}
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

// Checkouts lists the main git checkouts below the roots, sorted by path. It
// walks the roots on each call, so a new clone shows up at once.
func (g *Global) Checkouts() []Checkout {
	var out []Checkout
	for _, p := range findCheckouts(g.home, g.file.Roots) {
		out = append(out, Checkout{Path: p, Key: g.keyOf(p)})
	}
	return out
}

// checkoutsOf lists the checkouts of the Repo key, which ignores case.
func (g *Global) checkoutsOf(key string) []string {
	var out []string
	for _, c := range g.Checkouts() {
		if strings.EqualFold(c.Key, key) {
			out = append(out, c.Path)
		}
	}
	return out
}

// clash says that several checkouts have the Repo key.
func clash(key string, paths []string) string {
	return fmt.Sprintf("%d checkouts have the key %s: %s", len(paths), key, strings.Join(paths, " and "))
}

// KeyClash says which checkouts have the Repo key, when more than one has
// it. It is "" when one or none has it.
func (g *Global) KeyClash(key string) string {
	if all := g.checkoutsOf(key); len(all) > 1 {
		return clash(key, all)
	}
	return ""
}

// KeyClashes describes each Repo key that more than one checkout has.
// lineup picks no checkout for such a key.
func (g *Global) KeyClashes() []string {
	byKey := map[string][]Checkout{}
	for _, c := range g.Checkouts() {
		k := strings.ToLower(c.Key)
		byKey[k] = append(byKey[k], c)
	}
	var out []string
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		if cs := byKey[k]; len(cs) > 1 {
			var paths []string
			for _, c := range cs {
				paths = append(paths, c.Path)
			}
			out = append(out, clash(cs[0].Key, paths))
		}
	}
	return out
}

// CheckoutFor is the one checkout of the Repo key, for example the checkout
// of a GitHub Issue's repository.
func (g *Global) CheckoutFor(key string) (string, bool) {
	if all := g.checkoutsOf(key); len(all) == 1 {
		return all[0], true
	}
	return "", false
}

// FindCheckout turns the --repo of lineup add, or the repo of a Local Issue,
// into a checkout. The name is a path, an owner/repo key, or the folder name
// of one checkout below the roots.
func (g *Global) FindCheckout(name string) (string, error) {
	isPath := strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~/") || strings.HasPrefix(name, ".")
	if !isPath {
		if _, repo, ok := splitKey(name); ok && repo != "*" {
			switch all := g.checkoutsOf(name); len(all) {
			case 0:
			case 1:
				return all[0], nil
			default:
				return "", fmt.Errorf("%s: pass a path", clash(name, all))
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
	if !isMainCheckout(abs) {
		return "", fmt.Errorf("%s is not a main git checkout", abs)
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
