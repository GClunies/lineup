package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

func git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The developer's shell can hold a GIT_DIR and a URL rewrite in the global
// config.
func TestIsolateCutsGitOffFromTheDevelopersMachine(t *testing.T) {
	root := t.TempDir()
	decoy := filepath.Join(root, "decoy")
	git(t, "init", "-q", decoy)
	global := filepath.Join(root, "gitconfig")
	os.WriteFile(global, []byte("[url \"git@github.com:\"]\n\tinsteadOf = https://github.com/\n"), 0o644)
	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	if err := isolate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	git(t, "init", "-q", repo)
	git(t, "-C", repo, "remote", "add", "origin", "https://github.com/o/r.git")
	if got := git(t, "-C", repo, "remote", "get-url", "origin"); got != "https://github.com/o/r.git" {
		t.Errorf("the developer's URL rewrite applied: %s", got)
	}
	if got := git(t, "-C", repo, "config", "maintenance.auto"); got != "false" {
		t.Errorf("maintenance.auto = %q, so a push can start maintenance in the background", got)
	}
	if got := git(t, "--git-dir", filepath.Join(decoy, ".git"), "remote"); got != "" {
		t.Errorf("git wrote to the repo in the developer's GIT_DIR: remotes %q", got)
	}
}

// pkg is one directory of Go files in this module.
type pkg struct {
	imports   []string // directories of the module packages it imports
	runsGit   bool     // a file starts git or wt
	callsMain bool     // a test file calls testenv.Main
}

// A package whose tests run git, through its own code or a module package it
// imports, gets the isolation only from testenv.Main.
func TestEachPackageThatRunsGitCallsMain(t *testing.T) {
	root, module := moduleRoot(t)
	pkgs := map[string]*pkg{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		p := pkgs[filepath.Dir(path)]
		if p == nil {
			p = &pkg{}
			pkgs[filepath.Dir(path)] = p
		}
		for _, imp := range f.Imports {
			v, _ := strconv.Unquote(imp.Path.Value)
			if rest, ok := strings.CutPrefix(v, module+"/"); ok {
				p.imports = append(p.imports, filepath.Join(root, rest))
			}
		}
		test := strings.HasSuffix(path, "_test.go")
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				x, _ := fun.X.(*ast.Ident)
				switch {
				case x == nil:
				case x.Name == "exec" && (fun.Sel.Name == "Command" || fun.Sel.Name == "CommandContext" || fun.Sel.Name == "LookPath"):
					for _, a := range call.Args {
						if lit, ok := a.(*ast.BasicLit); ok && (lit.Value == `"git"` || lit.Value == `"wt"`) {
							p.runsGit = true
						}
					}
				case x.Name == "testenv" && fun.Sel.Name == "Main":
					p.callsMain = p.callsMain || test
				}
			case *ast.Ident:
				p.callsMain = p.callsMain || test && fun.Name == "Main" && f.Name.Name == "testenv"
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var runs func(dir string, seen map[string]bool) bool
	runs = func(dir string, seen map[string]bool) bool {
		p := pkgs[dir]
		if p == nil || seen[dir] {
			return false
		}
		seen[dir] = true
		if p.runsGit {
			return true
		}
		for _, imp := range p.imports {
			if runs(imp, seen) {
				return true
			}
		}
		return false
	}
	if !runs(filepath.Join(root, "internal", "worktree"), map[string]bool{}) {
		t.Fatal("the scan found no git call in internal/worktree")
	}
	for dir, p := range pkgs {
		if runs(dir, map[string]bool{}) && !p.callsMain {
			rel, _ := filepath.Rel(root, dir)
			t.Errorf("%s runs git, but no TestMain in it calls testenv.Main", rel)
		}
	}
}

func moduleRoot(t *testing.T) (root, module string) {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			first, _, _ := strings.Cut(string(data), "\n")
			return dir, strings.TrimSpace(strings.TrimPrefix(first, "module"))
		}
		if parent := filepath.Dir(dir); parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = filepath.Dir(dir)
	}
}
