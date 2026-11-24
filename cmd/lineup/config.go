package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/configpanel"
	"golang.org/x/term"
)

// cmdConfig opens the config panel with no argument, rewrites the config
// file in its current form with migrate, and lists the key catalog with keys.
func cmdConfig(args []string, out io.Writer, panel func() error) error {
	switch {
	case len(args) == 0:
		return panel()
	case args[0] == "keys":
		return cmdConfigKeys(args[1:], out)
	case args[0] == "migrate" && len(args) == 1:
		msg, err := config.Migrate(configPath())
		if err != nil {
			return err
		}
		fmt.Fprintln(out, msg)
		return nil
	}
	return fmt.Errorf("%w: lineup config [migrate | keys [--json] [--repo <owner>/<repo>]]", errUsage)
}

// configKeys is what lineup config keys --json prints. Settings come only
// with --repo.
type configKeys struct {
	Keys      []config.Key    `json:"keys"`
	Repo      string          `json:"repo,omitempty"`
	Settings  []configSetting `json:"settings,omitempty"`
	LoadError string          `json:"load_error,omitempty"`
}

type configSetting struct {
	Path   string `json:"path"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// cmdConfigKeys prints the key catalog, and with --repo the value that
// applies to the Repo and the level that sets it. It works on a file that
// does not load, and names the load error (PROD-CONFIG-011).
func cmdConfigKeys(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("config keys", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "")
	repo := fs.String("repo", "", "")
	usage := fmt.Errorf("%w: lineup config keys [--json] [--repo <owner>/<repo>]", errUsage)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage
	}
	if owner, name, ok := strings.Cut(*repo, "/"); *repo != "" && (!ok || owner == "" || name == "" || name == "*" || strings.Contains(name, "/")) {
		return fmt.Errorf("%q is not a Repo key: use owner/repo", *repo)
	}
	f, err := config.ReadFile(configPath())
	if err != nil {
		return err
	}
	v := configKeys{Keys: config.Catalog, Repo: *repo}
	if f.LoadErr != nil {
		v.LoadError = f.LoadErr.Error()
	}
	if *repo != "" {
		for _, s := range f.Settings(*repo) {
			v.Settings = append(v.Settings, configSetting{Path: s.Path, Value: s.Value, Source: s.Source()})
		}
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	if v.LoadError != "" {
		fmt.Fprintf(out, "The file does not load: %s\n\n", v.LoadError)
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if *repo == "" {
		fmt.Fprintln(w, "KEY\tTYPE\tLEVELS\tDEFAULT\tAPPLIES AFTER\tDOC")
		for _, k := range config.Catalog {
			levels := make([]string, len(k.Levels))
			for i, l := range k.Levels {
				levels[i] = l.String()
			}
			kind := string(k.Type)
			if len(k.Values) > 0 {
				kind += " (" + strings.Join(k.Values, ", ") + ")"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", k.Path, kind, strings.Join(levels, ", "), k.Default, k.Restart, k.Doc)
		}
		return w.Flush()
	}
	fmt.Fprintf(w, "KEY\tVALUE FOR %s\tSET BY\n", *repo)
	for _, s := range v.Settings {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Path, s.Value, s.Source)
	}
	return w.Flush()
}

// runConfigPanel opens the config panel on the config file. A restart that
// the panel offers runs this lineup binary (PROD-CONFIG-010).
func runConfigPanel() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("the config panel needs a terminal. lineup config keys lists the keys without one")
	}
	f, err := config.ReadFile(configPath())
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	repos, here := panelRepos(f, f.Checkouts(), cwd)
	return configpanel.Run(configpanel.Options{File: f, Repos: repos, Repo: here, Restart: func(args []string) (string, error) {
		out, err := exec.Command(self, args...).CombinedOutput()
		return string(out), err
	}})
}

// panelRepos are the Repo keys of the checkouts, each once and sorted in any
// case, and the Repo that the Repo tab shows first: the Repo of the deepest
// checkout that holds dir, else the first Repo with its own table, else none.
func panelRepos(f *config.File, checkouts []config.Checkout, dir string) (repos []string, here string) {
	deepest := ""
	for _, c := range checkouts {
		i := slices.IndexFunc(repos, func(r string) bool { return strings.EqualFold(r, c.Key) })
		if i < 0 {
			i = len(repos)
			repos = append(repos, c.Key)
		}
		if (dir == c.Path || strings.HasPrefix(dir, c.Path+string(filepath.Separator))) && len(c.Path) > len(deepest) {
			here, deepest = repos[i], c.Path
		}
	}
	slices.SortFunc(repos, func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) })
	if i := slices.IndexFunc(repos, f.SetUp); here == "" && i >= 0 {
		here = repos[i]
	}
	return repos, here
}
