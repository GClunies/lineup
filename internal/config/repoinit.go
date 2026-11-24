package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// setupKey is one key that lineup repo init writes, as key=value names it:
// a key of the Repo's table, or of its review_bot or landing_bar sub-table.
type setupKey struct {
	name string
	// value parses the text after = and returns the value as TOML.
	value func(string) (string, error)
}

// setupKeys are the keys that lineup repo init writes, in the order that it
// writes them.
var setupKeys = []setupKey{
	{"tracker", tomlString},
	{"teams", tomlList},
	{"prefix", tomlString},
	{"review_bot.name", tomlString},
	{"review_bot.min_review_score", tomlInt},
	{"review_bot.max_review_cycles", tomlInt},
	{"landing_bar.checks", tomlString},
	{"landing_bar.threads", tomlString},
	{"landing_bar.approvals", tomlString},
}

func tomlString(s string) (string, error) { return strconv.Quote(s), nil }

func tomlInt(s string) (string, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return "", fmt.Errorf("%q is not a whole number", s)
	}
	return strconv.Itoa(n), nil
}

// tomlList takes items with commas between them, as in teams=PD,ENG.
func tomlList(s string) (string, error) {
	var items []string
	for _, it := range strings.Split(s, ",") {
		it = strings.TrimSpace(it)
		if it == "" {
			return "", fmt.Errorf("%q has an empty item", s)
		}
		items = append(items, strconv.Quote(it))
	}
	return "[" + strings.Join(items, ", ") + "]", nil
}

// parseSetup reads the key=value arguments into one TOML value for each
// setup key, by its index in setupKeys.
func parseSetup(args []string) (map[int]string, error) {
	out := map[int]string{}
	for _, a := range args {
		name, raw, ok := strings.Cut(a, "=")
		raw = strings.TrimSpace(raw)
		if !ok || raw == "" {
			return nil, fmt.Errorf("%q must be key=value", a)
		}
		i := slices.IndexFunc(setupKeys, func(k setupKey) bool { return k.name == name })
		if i < 0 {
			names := make([]string, len(setupKeys))
			for j, k := range setupKeys {
				names[j] = k.name
			}
			return nil, fmt.Errorf("unknown key %q: use %s", name, strings.Join(names, ", "))
		}
		if _, ok := out[i]; ok {
			return nil, fmt.Errorf("%s is set twice", name)
		}
		v, err := setupKeys[i].value(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[i] = v
	}
	return out, nil
}

// renderTable writes the Repo's table and each sub-table that has a key, in
// the order of setupKeys. With no values it is only the header, which still
// sets the Repo up.
func renderTable(key string, values map[int]string) string {
	head := "repos." + strconv.Quote(key)
	var b strings.Builder
	b.WriteString("[" + head + "]\n")
	sub := ""
	for i, k := range setupKeys {
		v, ok := values[i]
		if !ok {
			continue
		}
		name := k.name
		if table, rest, dotted := strings.Cut(k.name, "."); dotted {
			if table != sub {
				b.WriteString("[" + head + "." + table + "]\n")
				sub = table
			}
			name = rest
		}
		b.WriteString(name + " = " + v + "\n")
	}
	return b.String()
}

// insertTable puts the table after the last [repos."…"] table and its
// sub-tables, before the blank lines and comments that lead the next table.
// A text with no such table gets it at the end. One blank line sets it
// apart, and every other byte stays.
func insertTable(text, table string) (string, error) {
	items, err := parseItems(text)
	if err != nil {
		return "", err
	}
	// at is the count of lines up to the last header or key of a Repo table.
	at, n, inRepos := -1, 0, false
	for _, it := range items {
		if it.kind == '[' {
			inRepos = it.first == "repos" && !it.array
		}
		n += len(it.lines)
		if inRepos && (it.kind == '[' || it.kind == '=') {
			at = n
		}
	}
	if at < 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if text != "" && !strings.HasSuffix(text, "\n\n") {
			text += "\n"
		}
		return text + table, nil
	}
	lines := strings.SplitAfter(text, "\n")
	head, rest := strings.Join(lines[:at], ""), strings.Join(lines[at:], "")
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	if rest != "" && !strings.HasPrefix(rest, "\n") {
		table += "\n"
	}
	return head + "\n" + table + rest, nil
}

// InitRepo adds the table of the Repo key, owner/repo, to the config file at
// path, with a value for each key=value argument. It refuses a key that
// already has a table, in any case. It checks the new text with the same
// parse as Load, and writes nothing when that fails. A missing file becomes
// a file with only the table. It returns the table.
func InitRepo(path, key string, args []string) (string, error) {
	if _, repo, ok := splitKey(key); !ok || repo == "*" {
		return "", fmt.Errorf("%q is not a Repo key: use owner/repo", key)
	}
	values, err := parseSetup(args)
	if err != nil {
		return "", err
	}
	perm := fs.FileMode(0o644)
	old := ""
	switch data, err := os.ReadFile(path); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		g, err := parseGlobal(path, string(data))
		if err != nil {
			return "", err
		}
		if _, ok := g.table(key); ok {
			return "", fmt.Errorf("%s is already set up in %s. Edit its table there", key, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		old, perm = string(data), info.Mode().Perm()
	}
	table := renderTable(key, values)
	text, err := insertTable(old, table)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if _, err := parseGlobal(path, text); err != nil {
		return "", err
	}
	return table, writeAtomic(path, []byte(text), perm)
}
