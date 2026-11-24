package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Migrate rewrites the config file at path from [[repos]] tables and layer
// keys at the top level to roots, [defaults], and [repos."<key>"] tables,
// from trackers lists to tracker keys, and from the moved sections in the
// [board] lists to their keys now. It writes the old file to
// <path>.bak first, and puts the old file back when the new one does not
// load. It returns a line for the user.
func Migrate(path string) (string, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text, err := migrateFile(string(old))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if text == string(old) {
		return fmt.Sprintf("%s has no [[repos]] tables, no layer keys at the top level, no trackers lists, and no ready or queued in [board], so lineup changed nothing.", path), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	// O_EXCL keeps a backup that the user made before.
	bak, err := os.OpenFile(path+".bak", os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%s.bak already exists. Move it away, then run lineup config migrate again", path)
	}
	if err != nil {
		return "", err
	}
	if _, err := bak.Write(old); err != nil {
		bak.Close()
		return "", err
	}
	if err := bak.Close(); err != nil {
		return "", err
	}
	if err := writeAtomic(path, []byte(text), info.Mode().Perm()); err != nil {
		return "", err
	}
	if _, err := Load(path); err != nil {
		if rerr := writeAtomic(path, old, info.Mode().Perm()); rerr != nil {
			return "", errors.Join(err, rerr)
		}
		return "", fmt.Errorf("the migrated file did not load, so lineup put the old file back: %w", err)
	}
	return fmt.Sprintf("Rewrote %s. The old file is %s.bak. Run lineup restart to load the new file.", path, path), nil
}

// migrateFile is the text of the config file after the migration.
func migrateFile(text string) (string, error) {
	var raw map[string]any
	md, err := toml.Decode(text, &raw)
	if err != nil {
		return "", err
	}
	_, oldRepos := raw["repos"].([]map[string]any)
	if oldRepos || slices.ContainsFunc(md.Keys(), func(k toml.Key) bool { return slices.Contains(layerKeys, k[0]) }) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if text, err = migrateText(text, home); err != nil {
			return "", err
		}
	}
	if text, err = migrateTrackers(text); err != nil {
		return "", err
	}
	return migrateBoard(text)
}

// migrateBoard rewrites each [board] list that names a moved section. The
// first moved section takes the key that replaced it, in its place, and each
// key stays in the list once.
func migrateBoard(text string) (string, error) {
	var raw struct {
		Board boardFile `toml:"board"`
	}
	if _, err := toml.Decode(text, &raw); err != nil {
		return "", err
	}
	lists := []struct {
		name string
		keys *[]string
	}{{"order", raw.Board.Order}, {"collapsed", raw.Board.Collapsed}, {"hidden", raw.Board.Hidden}}
	// set holds the new value of each list, by its name.
	set := map[string]string{}
	for _, l := range lists {
		if l.keys == nil || !slices.ContainsFunc(*l.keys, func(k string) bool { return movedSections[k] != "" }) {
			continue
		}
		var quoted []string
		for _, k := range *l.keys {
			if to, ok := movedSections[k]; ok {
				k = to
			}
			if q := strconv.Quote(k); !slices.Contains(quoted, q) {
				quoted = append(quoted, q)
			}
		}
		set[l.name] = "[" + strings.Join(quoted, ", ") + "]"
	}
	if len(set) == 0 {
		return text, nil
	}
	items, err := parseItems(text)
	if err != nil {
		return "", err
	}
	inBoard := false
	for i, it := range items {
		if it.kind == '[' {
			name, _, array, _ := splitHeader(it.lines[it.above])
			head, rest := firstKey(name)
			inBoard = !array && head == "board" && strings.TrimSpace(rest) == ""
		}
		value, ok := set[it.first]
		if !inBoard || it.kind != '=' || !ok {
			continue
		}
		key := it.lines[it.above]
		line := strings.TrimRight(key[:valueStart(key)-1], " \t") + " = " + value + " " + trailingComment(it.lines[len(it.lines)-1])
		items[i].lines = append(slices.Clone(it.lines[:it.above]), strings.TrimRight(line, " "))
		delete(set, it.first)
	}
	for _, l := range lists {
		if value, ok := set[l.name]; ok {
			return "", fmt.Errorf("board.%s: lineup config migrate finds the list outside the [board] header: write %s = %s by hand", l.name, l.name, value)
		}
	}
	return keepLayout(items, text), nil
}

// migrateTrackers rewrites each trackers list as a tracker key, and adds
// tracker = "linear" to a Repo with teams and no Tracker, as its teams map
// Linear Issues to it. A list of more than one Tracker fails, as a Repo
// takes Issues from one Tracker.
func migrateTrackers(text string) (string, error) {
	var raw struct {
		Repos map[string]map[string]any `toml:"repos"`
	}
	if _, err := toml.Decode(text, &raw); err != nil {
		return "", err
	}
	// set holds the tracker that replaces each trackers list.
	set := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(raw.Repos)) {
		list, ok := raw.Repos[key]["trackers"]
		if !ok {
			continue
		}
		items, _ := list.([]any)
		var kind string
		if len(items) == 1 {
			kind, _ = items[0].(string)
		}
		if kind == "" {
			return "", fmt.Errorf("repos.%q: trackers holds %d Trackers, but a Repo takes Issues from one Tracker: write tracker = \"<kind>\" in place of the list by hand", key, len(items))
		}
		set[key] = kind
	}
	// tracked holds the lower-case key of each table with a tracker.
	tracked := map[string]bool{}
	for key, t := range raw.Repos {
		if _, ok := t["tracker"]; ok || set[key] != "" {
			tracked[strings.ToLower(key)] = true
		}
	}
	added := map[string]bool{}
	for key, t := range raw.Repos {
		owner, _, _ := strings.Cut(strings.ToLower(key), "/")
		if _, teams := t["teams"]; teams && !tracked[strings.ToLower(key)] && !tracked[owner+"/*"] {
			added[key] = true
		}
	}
	if len(set) == 0 && len(added) == 0 {
		return text, nil
	}
	items, err := parseItems(text)
	if err != nil {
		return "", err
	}
	var out []item
	table := ""
	for _, it := range items {
		if it.kind == '[' {
			table = repoTableKey(it.lines[it.above])
		}
		if it.kind != '=' {
			out = append(out, it)
			continue
		}
		indent := indentOf(it.lines[it.above])
		switch kind, ok := set[table]; {
		case it.first == "trackers" && ok:
			line := indent + "tracker = " + strconv.Quote(kind) + " " + trailingComment(it.lines[len(it.lines)-1])
			it.lines = append(slices.Clone(it.lines[:it.above]), strings.TrimRight(line, " "))
			delete(set, table)
		case it.first == "teams" && added[table]:
			out = append(out, it)
			it = item{lines: []string{indent + `tracker = "linear"`}, kind: '='}
			delete(added, table)
		}
		out = append(out, it)
	}
	for _, key := range slices.Sorted(maps.Keys(set)) {
		return "", fmt.Errorf("repos.%q: lineup config migrate finds the trackers list outside the table's own header: write tracker = %q by hand", key, set[key])
	}
	for _, key := range slices.Sorted(maps.Keys(added)) {
		return "", fmt.Errorf(`repos.%q: lineup config migrate finds the teams list outside the table's own header: write tracker = "linear" by hand`, key)
	}
	return keepLayout(out, text), nil
}

// keepLayout is the text of the items with each line as it was, blank lines
// included, and the final newline only if text has one.
func keepLayout(items []item, text string) string {
	var b strings.Builder
	for _, it := range items {
		for _, l := range it.lines {
			b.WriteString(l + "\n")
		}
	}
	if !strings.HasSuffix(text, "\n") {
		return strings.TrimSuffix(b.String(), "\n")
	}
	return b.String()
}

func indentOf(line string) string { return line[:len(line)-len(strings.TrimLeft(line, " \t"))] }

// repoTableKey is the key of a [repos."<key>"] header, or "" for any other
// header, a sub-table of a Repo's table included.
func repoTableKey(header string) string {
	name, _, array, _ := splitHeader(header)
	head, rest := firstKey(name)
	rest = strings.TrimLeft(rest, " \t")
	if array || head != "repos" || !strings.HasPrefix(rest, ".") {
		return ""
	}
	key, after := firstKey(rest[1:])
	if strings.TrimSpace(after) != "" {
		return ""
	}
	return key
}

// trailingComment is the comment at the end of a line, from its #, or "".
func trailingComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		switch {
		case quote != 0:
			if quote == '"' && line[i] == '\\' {
				i++
			} else if line[i] == quote {
				quote = 0
			}
		case line[i] == '"' || line[i] == '\'':
			quote = line[i]
		case line[i] == '#':
			return line[i:]
		}
	}
	return ""
}

// oldRepo is a [[repos]] table.
type oldRepo struct {
	match string
	// only is true for a table with no key but match.
	only bool
	// key is the [repos."<key>"] table that replaces it, or "" when only.
	key string
	// found are the checkouts that match finds.
	found []string
}

// migrateText is the text of the config file after the migration.
func migrateText(text, home string) (string, error) {
	var raw map[string]any
	if _, err := toml.Decode(text, &raw); err != nil {
		return "", err
	}
	entries, _ := raw["repos"].([]map[string]any)
	repos, err := oldRepos(entries, home)
	if err != nil {
		return "", err
	}
	roots, err := migrateRoots(repos, home)
	if err != nil {
		return "", err
	}
	items, err := parseItems(text)
	if err != nil {
		return "", err
	}
	return rewrite(items, repos, roots)
}

func oldRepos(entries []map[string]any, home string) ([]oldRepo, error) {
	var repos []oldRepo
	for i, e := range entries {
		match, _ := e["match"].(string)
		if match == "" {
			return nil, fmt.Errorf("repos[%d]: match is required", i)
		}
		r := oldRepo{match: match, only: len(e) == 1, found: globCheckouts(home, match)}
		if !r.only {
			key, err := repoKeyOf(r, home)
			if err != nil {
				return nil, err
			}
			r.key = key
		}
		for _, o := range repos {
			if o.key != "" && strings.EqualFold(o.key, r.key) {
				return nil, fmt.Errorf("the [[repos]] matches %q and %q both become [repos.%q]: merge them by hand", o.match, r.match, r.key)
			}
		}
		repos = append(repos, r)
	}
	return repos, nil
}

// repoKeyOf is the key of the [repos."<key>"] table that replaces a
// [[repos]] table with keys besides match.
func repoKeyOf(r oldRepo, home string) (string, error) {
	if !strings.ContainsAny(r.match, "*?[") {
		path := filepath.Clean(expand(home, r.match))
		if !isMainCheckout(path) {
			return "", fmt.Errorf("the [[repos]] match %q is not a main git checkout, so it has no owner/repo key", r.match)
		}
		return checkoutKey(path), nil
	}
	dir, base := filepath.Split(r.match)
	if base != "*" || strings.ContainsAny(dir, "*?[") {
		return "", fmt.Errorf("the [[repos]] glob %q is not <folder>/*, so no [repos.\"<owner>/*\"] table can replace it", r.match)
	}
	var owners []string
	for _, c := range r.found {
		owner, _, _ := strings.Cut(checkoutKey(c), "/")
		if !slices.ContainsFunc(owners, func(o string) bool { return strings.EqualFold(o, owner) }) {
			owners = append(owners, owner)
		}
	}
	switch len(owners) {
	case 0:
		return "", fmt.Errorf("the [[repos]] glob %q matches no checkout, so it has no owner", r.match)
	case 1:
		return owners[0] + "/*", nil
	}
	return "", fmt.Errorf("the checkouts that the [[repos]] glob %q matches have more than one owner (%s), so no [repos.\"<owner>/*\"] table can replace it", r.match, strings.Join(owners, ", "))
}

// globCheckouts are the main git checkouts that a [[repos]] match finds.
func globCheckouts(home, match string) []string {
	dirs, _ := filepath.Glob(filepath.Clean(expand(home, match)))
	var out []string
	for _, d := range dirs {
		if isMainCheckout(d) {
			out = append(out, d)
		}
	}
	return out
}

// migrateRoots are the roots that find every checkout that a [[repos]]
// match found. The tables with only match go first.
func migrateRoots(repos []oldRepo, home string) ([]string, error) {
	var roots []string
	order := slices.Concat(
		slices.DeleteFunc(slices.Clone(repos), func(r oldRepo) bool { return !r.only }),
		slices.DeleteFunc(slices.Clone(repos), func(r oldRepo) bool { return r.only }))
	for _, r := range order {
		have := findCheckouts(home, roots)
		if slices.ContainsFunc(r.found, func(c string) bool { return !slices.Contains(have, c) }) {
			if root := rootOf(r.match); !slices.Contains(roots, root) {
				roots = append(roots, root)
			}
		}
	}
	have := findCheckouts(home, roots)
	for _, r := range repos {
		for _, c := range r.found {
			if !slices.Contains(have, c) {
				return nil, fmt.Errorf("the [[repos]] match %q finds %s, which is more than two folders below the roots %s: hand-edit the file", r.match, c, strings.Join(roots, ", "))
			}
		}
	}
	return roots, nil
}

// rootOf is the folder of a match: the folders before the first wildcard
// of a glob, or the parent folder of a path.
func rootOf(match string) string {
	if !strings.ContainsAny(match, "*?[") {
		return filepath.Dir(match)
	}
	parts := strings.Split(match, "/")
	i := slices.IndexFunc(parts, func(p string) bool { return strings.ContainsAny(p, "*?[") })
	return strings.Join(parts[:i], "/")
}

// item is one part of a TOML file, as the migration reads it.
type item struct {
	// lines are the comment block directly above a key or a header, then
	// the item's own lines.
	lines []string
	// above is how many of lines are that comment block.
	above int
	// kind is ' ' for a blank line, '#' for a comment block, '=' for a key
	// and its value, and '[' for a table header.
	kind byte
	// first is the first part of the key or of the table name.
	first string
	// array is true for an [[array]] table header.
	array bool
	// line is the line number of the key or the header.
	line int
	// multi is true for a value with a multi-line string.
	multi bool
}

func parseItems(text string) ([]item, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	var items []item
	var above []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
			if above != nil {
				items = append(items, item{lines: above, kind: '#'})
				above = nil
			}
			items = append(items, item{lines: lines[i : i+1], kind: ' '})
		case t[0] == '#':
			above = append(above, lines[i])
		case t[0] == '[':
			name, _, array, ok := splitHeader(lines[i])
			if !ok {
				return nil, fmt.Errorf("line %d: the table header has no closing ]", i+1)
			}
			first, _ := firstKey(name)
			items = append(items, item{lines: append(above, lines[i]), above: len(above), kind: '[', first: first, array: array, line: i + 1})
			above = nil
		default:
			start := i
			eq := valueStart(lines[i])
			if eq < 0 {
				return nil, fmt.Errorf("line %d: the key has no =", i+1)
			}
			var v valueScan
			done := v.line(lines[i][eq:])
			for !done && i+1 < len(lines) {
				i++
				done = v.line(lines[i])
			}
			if !done {
				return nil, fmt.Errorf("line %d: the value has no end", start+1)
			}
			first, _ := firstKey(lines[start])
			items = append(items, item{lines: append(above, lines[start:i+1]...), above: len(above), kind: '=', first: first, line: start + 1, multi: v.multi})
			above = nil
		}
	}
	if above != nil {
		items = append(items, item{lines: above, kind: '#'})
	}
	return items, nil
}

// firstKey is the first part of a dotted TOML key, without its quotes, and
// the text after it.
func firstKey(s string) (first, rest string) {
	s = strings.TrimLeft(s, " \t")
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		end := 1
		for end < len(s) && s[end] != s[0] {
			if s[0] == '"' && s[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(s) {
			return s, ""
		}
		return s[1:end], s[end+1:]
	}
	end := strings.IndexAny(s, ". \t=")
	if end < 0 {
		end = len(s)
	}
	return s[:end], s[end:]
}

// splitHeader splits a table header line into the table name and the text
// after the closing bracket.
func splitHeader(line string) (name, after string, array, ok bool) {
	s := strings.TrimLeft(line, " \t")
	open := 1
	if strings.HasPrefix(s, "[[") {
		open, array = 2, true
	}
	var quote byte
	for i := open; i < len(s); i++ {
		switch {
		case quote != 0:
			if quote == '"' && s[i] == '\\' {
				i++
			} else if s[i] == quote {
				quote = 0
			}
		case s[i] == '"' || s[i] == '\'':
			quote = s[i]
		case s[i] == ']':
			if array && !strings.HasPrefix(s[i:], "]]") {
				return "", "", false, false
			}
			return s[open:i], s[i+open:], array, true
		}
	}
	return "", "", false, false
}

// valueStart is the index just after the = of a key line, or -1.
func valueStart(line string) int {
	var quote byte
	for i := 0; i < len(line); i++ {
		switch {
		case quote != 0:
			if quote == '"' && line[i] == '\\' {
				i++
			} else if line[i] == quote {
				quote = 0
			}
		case line[i] == '"' || line[i] == '\'':
			quote = line[i]
		case line[i] == '=':
			return i + 1
		}
	}
	return -1
}

// valueScan follows a TOML value across lines: its strings, and its
// brackets outside strings.
type valueScan struct {
	depth int
	quote string // the delimiter of the open string, or ""
	multi bool   // the value has a multi-line string
}

// line reads one line of the value and says whether the value ends on it.
func (v *valueScan) line(s string) bool {
	for i := 0; i < len(s); i++ {
		switch {
		case v.quote != "":
			if v.quote[0] == '"' && s[i] == '\\' {
				i++
			} else if strings.HasPrefix(s[i:], v.quote) {
				i += len(v.quote) - 1
				v.quote = ""
			}
		case strings.HasPrefix(s[i:], `"""`), strings.HasPrefix(s[i:], `'''`):
			v.quote, v.multi = s[i:i+3], true
			i += 2
		case s[i] == '"' || s[i] == '\'':
			v.quote = s[i : i+1]
		case s[i] == '[' || s[i] == '{':
			v.depth++
		case s[i] == ']' || s[i] == '}':
			v.depth--
		case s[i] == '#':
			i = len(s)
		}
	}
	if len(v.quote) == 1 {
		v.quote = ""
	}
	return v.depth <= 0 && v.quote == ""
}

// rewrite moves the layer keys at the top level into [defaults], puts each
// [[repos]] table under its key, and adds roots.
func rewrite(items []item, repos []oldRepo, roots []string) (string, error) {
	first := slices.IndexFunc(items, func(it item) bool { return it.kind == '[' })
	if first < 0 {
		first = len(items)
	}
	var top, defaults, sections []item
	var rootComments []string
	rootsAt := -1
	for _, it := range items[:first] {
		if it.multi {
			return "", multiErr(it)
		}
		if it.kind == '=' && rootsAt < 0 {
			rootsAt = len(top)
		}
		if it.kind == '=' && slices.Contains(layerKeys, it.first) {
			defaults = append(defaults, it)
		} else {
			top = append(top, it)
		}
	}

	entry := -1
	var section byte // 'r' a [[repos]] table or its sub-table, 'd' a dropped table, 'l' a layer table, 0 another table
	for _, it := range items[first:] {
		if it.kind == '[' {
			name, after, _, _ := splitHeader(it.lines[it.above])
			head, rest := firstKey(name)
			switch {
			case head == "repos" && it.array && strings.TrimSpace(rest) == "":
				entry++
				if entry >= len(repos) {
					return "", fmt.Errorf("line %d: write each repos entry as a [[repos]] table", it.line)
				}
				if repos[entry].only {
					section = 'd'
					rootComments = append(rootComments, it.lines[:it.above]...)
					continue
				}
				section = 'r'
				it.lines = replaceLast(it.lines, "[repos."+strconv.Quote(repos[entry].key)+"]"+after)
			case head == "repos" && len(repos) == 0:
				// A file with no [[repos]] tables has its Repo tables in the current form.
				section = 0
			case head == "repos":
				if entry < 0 {
					return "", fmt.Errorf("line %d: a repos table comes before the first [[repos]] table", it.line)
				}
				section = 'r'
				it.lines = replaceLast(it.lines, headerText(it.array, "repos."+strconv.Quote(repos[entry].key)+rest)+after)
			case slices.Contains(layerKeys, head):
				section = 'l'
				it.lines = replaceLast(it.lines, headerText(it.array, "defaults."+strings.TrimLeft(name, " \t"))+after)
			default:
				section = 0
			}
			sections = append(sections, it)
			continue
		}
		if section != 0 && it.multi {
			return "", multiErr(it)
		}
		switch {
		case section == 'd':
			if it.kind == '#' || it.kind == '=' {
				rootComments = append(rootComments, commentLines(it)...)
			}
		case section == 'r' && it.kind == '=' && it.first == "match":
			if it.above > 0 {
				sections = append(sections, item{lines: it.lines[:it.above], kind: '#'})
			}
		default:
			sections = append(sections, it)
		}
	}
	if entry+1 != len(repos) {
		return "", errors.New("write each repos entry as a [[repos]] table")
	}

	if len(roots) > 0 {
		quoted := make([]string, len(roots))
		for i, r := range roots {
			quoted[i] = strconv.Quote(r)
		}
		rootComments = append(rootComments, "roots = ["+strings.Join(quoted, ", ")+"]")
	}
	if rootComments != nil {
		if rootsAt < 0 {
			rootsAt = len(top)
		}
		top = slices.Insert(top, rootsAt, item{lines: rootComments, kind: '='})
	}
	blank := item{lines: []string{""}, kind: ' '}
	if len(defaults) > 0 {
		defaults = slices.Concat([]item{blank, {lines: []string{"[defaults]"}, kind: '['}}, defaults)
	}
	return joinItems(slices.Concat(top, defaults, []item{blank}, sections)), nil
}

func multiErr(it item) error {
	return fmt.Errorf("line %d: lineup config migrate cannot move a multi-line string: write it on one line, or edit the file by hand", it.line)
}

// commentLines are the comment lines of an item.
func commentLines(it item) []string {
	return slices.DeleteFunc(slices.Clone(it.lines), func(l string) bool { return !strings.HasPrefix(strings.TrimSpace(l), "#") })
}

func headerText(array bool, name string) string {
	if array {
		return "[[" + name + "]]"
	}
	return "[" + name + "]"
}

// replaceLast replaces the last line, the header line of a header item.
func replaceLast(lines []string, last string) []string {
	return append(slices.Clone(lines[:len(lines)-1]), last)
}

// joinItems joins the lines of the items, with one blank line at most
// between two items and none at the start or the end.
func joinItems(items []item) string {
	var b bytes.Buffer
	blank := false
	for _, it := range items {
		if it.kind == ' ' {
			blank = b.Len() > 0
			continue
		}
		if blank {
			b.WriteString("\n")
			blank = false
		}
		for _, l := range it.lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}
