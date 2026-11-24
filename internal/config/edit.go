package config

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gclunies/lineup/internal/github"
	"github.com/gclunies/lineup/internal/lifecycle"
)

// File is the config file as text, for a reader that shows each key and
// changes one key at a time. Raw is the text decoded with no check, so the
// values of a file that does not load still show. Raw is nil when the text is
// not TOML. LoadErr is the error of the loader on the text, or nil.
type File struct {
	Path    string
	Text    string
	Raw     map[string]any
	LoadErr error

	exists bool
	perm   fs.FileMode
}

// ErrFileChanged is the error of an edit that finds a file on disk that
// differs from the text that it read.
var ErrFileChanged = errors.New("the file changed since lineup read it")

// ReadFile reads the config file at path. A missing file is an empty text.
func ReadFile(path string) (*File, error) {
	f := &File{Path: path, perm: 0o644}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		f.Text, f.exists, f.perm = string(data), true, info.Mode().Perm()
	}
	f.decode()
	return f, nil
}

func (f *File) decode() {
	f.Raw = nil
	raw := map[string]any{}
	if _, err := toml.Decode(f.Text, &raw); err == nil {
		f.Raw = raw
	}
	_, f.LoadErr = parseGlobal(f.Path, f.Text)
}

// Where is one level of the file. Table is the key of the [repos."<key>"]
// table, as the file writes it, for LevelOwner and LevelRepo.
type Where struct {
	Level Level
	Table string
}

func (w Where) String() string {
	if w.Table != "" {
		return w.Table
	}
	return w.Level.String()
}

// Setting is one concrete key, such as stage.working.model, and the value
// that applies to it.
type Setting struct {
	Key  Key
	Path string
	// Group and Label are the plain place of the key: the titles of the
	// groups that hold it, outermost first, and its own name.
	Group []string
	Label string
	// Value is the text of the value that applies, or the default.
	Value string
	// Raw is the decoded value that applies, or nil for the default.
	Raw any
	// By is the level whose value applies, or nil for the default.
	By *Where
	// From is the path whose value applies when it is not Path: a
	// [stage.<key>] table with no value takes the one of [stage.default]
	// (ARCH-CONFIG-002).
	From string
	// Off says why the setting does not apply on the tab, or "". The panel
	// shows it in place of the value and does not edit the setting.
	Off string
	// Layers lists each place that the value can come from, lowest first:
	// each level that sets the stage.default key of a stage key, then each
	// level where Path itself can sit. The last one that sets a value is the
	// one that applies.
	Layers []Layer
}

// Layer is one place that the value of a Setting can come from.
type Layer struct {
	At Where
	// Path is the key at the level: the Setting's own Path, or the
	// stage.default key that it falls back to.
	Path string
	Set  bool
	// Value is the text of the value at the level, when Set.
	Value string
}

// Levels lists each level where Path itself can be set on the tab, lowest
// first.
func (s Setting) Levels() []Where { return s.own(false) }

// SetAt lists each level that sets Path itself, lowest first.
func (s Setting) SetAt() []Where { return s.own(true) }

func (s Setting) own(set bool) []Where {
	var out []Where
	for _, l := range s.Layers {
		if l.Path == s.Path && (l.Set || !set) {
			out = append(out, l.At)
		}
	}
	return out
}

// FileKey is the key as the file writes it at the level, such as
// [repos."acme/api".review_bot] min_review_score.
func (s Setting) FileKey(w Where) string {
	if s.Key.Type == TypeQueue {
		return "[[review.queues]]"
	}
	parts := strings.Split(s.Path, ".")
	key := bareOrQuoted(parts[len(parts)-1])
	if table := append(tableOf(w), parts[:len(parts)-1]...); len(table) > 0 {
		return "[" + joinKey(table) + "] " + key
	}
	return key
}

// BuiltIn is the plain name of the default value of a key.
const BuiltIn = "built-in"

// Name is the plain name of the level in the config panel: your file, all
// Repos, all <owner> Repos, or this Repo.
func (w Where) Name() string {
	switch w.Level {
	case LevelTop:
		return "your file"
	case LevelDefaults:
		return "all Repos"
	case LevelOwner:
		return "all " + strings.TrimSuffix(w.Table, "/*") + " Repos"
	}
	return "this Repo"
}

// Place is the plain name of a layer of the setting: the Name of its level,
// and Every Stage when the layer is the [stage.default] key of a Stage key.
func (s Setting) Place(l Layer) string {
	if l.Path != s.Path {
		return l.At.Name() + ", " + stageTitle("default")
	}
	return l.At.Name()
}

// SetIn is the plain name of the place whose value applies, or BuiltIn.
func (s Setting) SetIn() string {
	if s.By == nil {
		return BuiltIn
	}
	return s.Place(Layer{At: *s.By, Path: cmp.Or(s.From, s.Path)})
}

// Source names the level of the value: default, [defaults], or the key of
// a Repo table, and the stage.default table that gives the value.
func (s Setting) Source() string {
	switch {
	case s.By == nil:
		return "default"
	case s.From != "":
		return s.By.String() + " (" + s.From[:strings.LastIndex(s.From, ".")] + ")"
	}
	return s.By.String()
}

// Settings lists the concrete keys of a tab, in catalog order. With no Repo
// key they are the top-level keys and the [defaults] keys. With a Repo key
// they are the keys of a Repo table, and each value layers as Resolve
// layers it: [defaults], then the owner/* table, then the owner/repo table.
// The tables match the Repo key in any case, and a table that the file does
// not have is a level that sets nothing. It reads Raw, so it works on a file
// that does not load. A TypeNote row leads the group of each Stage that keeps
// the model of its Worker.
func (f *File) Settings(repoKey string) []Setting {
	layers := []Where{{Level: LevelDefaults}}
	if repoKey != "" {
		owner, _, _ := strings.Cut(repoKey, "/")
		for _, w := range []Where{{LevelOwner, owner + "/*"}, {LevelRepo, repoKey}} {
			if k, ok := f.repoTable(w.Table); ok {
				w.Table = k
			}
			layers = append(layers, w)
		}
	}
	// The notes come first, so each one leads the group of its Stage.
	out := workerNotes()
	for _, k := range Catalog {
		var at []Where
		switch {
		case repoKey == "" && slices.Contains(k.Levels, LevelTop):
			at = []Where{{Level: LevelTop}}
		case repoKey == "":
			at = layers[:1]
			if !slices.Contains(k.Levels, LevelDefaults) {
				continue
			}
		case slices.Contains(k.Levels, LevelTop):
			continue
		default:
			at = slices.DeleteFunc(slices.Clone(layers), func(w Where) bool { return !slices.Contains(k.Levels, w.Level) })
		}
		if k.Type == TypeQueue {
			out = append(out, f.queues(k)...)
			continue
		}
		for _, path := range f.expand(k) {
			out = append(out, f.setting(k, path, at))
		}
	}
	// The keys of one Stage go together, default first, then in lifecycle
	// order. The stage keys are the last of the catalog.
	slices.SortStableFunc(out, func(a, b Setting) int { return stageRank(a.Path) - stageRank(b.Path) })
	// A key of one Tracker does not apply to a Repo with another Tracker.
	if t := slices.IndexFunc(out, func(s Setting) bool { return s.Path == "tracker" }); t >= 0 {
		for i, s := range out {
			if s.Key.Tracker != "" && out[t].Value != s.Key.Tracker {
				out[i].Off = "only with the " + trackerNames[s.Key.Tracker] + " Tracker"
			}
		}
	}
	// With no min_review_score, the gate takes the top score of the bot.
	name := slices.IndexFunc(out, func(s Setting) bool { return s.Path == "review_bot.name" })
	for i, s := range out {
		if s.Path != "review_bot.min_review_score" || s.By != nil || name < 0 {
			continue
		}
		if bot, ok := github.ReviewBots[fmt.Sprint(out[name].Raw)]; ok {
			out[i].Value = strconv.Itoa(bot.TopScore)
		}
	}
	return out
}

// stageRank is 0 for a key outside [stage], 1 for a stage.default key, and
// one more than the Stage for each other stage key.
func stageRank(path string) int {
	parts := strings.Split(path, ".")
	switch {
	case parts[0] != "stage":
		return 0
	case parts[1] == "default":
		return 1
	}
	st, err := lifecycle.ParseStage(parts[1])
	if err != nil {
		return 0
	}
	return int(st) + 2
}

// setting looks the path up at each level, lowest first. A stage key that
// no level sets takes the stage.default key of the highest level that sets it.
func (f *File) setting(k Key, path string, at []Where) Setting {
	s := Setting{Key: k, Path: path, Value: k.Default}
	s.Group, s.Label = k.place(path)
	paths := []string{path}
	if parts := strings.Split(path, "."); parts[0] == "stage" && parts[1] != "default" {
		paths = []string{"stage.default." + parts[2], path}
	}
	for _, p := range paths {
		from := ""
		if p != path {
			from = p
		}
		for _, w := range at {
			v, ok := f.lookup(w, p)
			if !ok && from != "" {
				// A stage.default key is a layer only where a level sets it.
				continue
			}
			l := Layer{At: w, Path: p, Set: ok}
			if ok {
				l.Value = valueText(v)
				s.By, s.Raw, s.From = &w, v, from
			}
			s.Layers = append(s.Layers, l)
		}
	}
	if s.By != nil {
		s.Value = valueText(s.Raw)
	}
	return s
}

// queues gives one row for each [[review.queues]] entry, with its name as
// the label and its filters as the value.
func (f *File) queues(k Key) []Setting {
	review, _ := f.Raw["review"].(map[string]any)
	list, _ := review["queues"].([]map[string]any)
	var out []Setting
	for i, q := range list {
		name, _ := q["name"].(string)
		var filters []string
		for _, key := range slices.Sorted(maps.Keys(q)) {
			if key != "name" {
				filters = append(filters, key+" = "+tomlText(q[key]))
			}
		}
		path, value := fmt.Sprintf("review.queues[%d]", i), strings.Join(filters, ", ")
		group, _ := k.place(path)
		top := Where{Level: LevelTop}
		out = append(out, Setting{Key: k, Path: path, Group: group, Label: cmp.Or(name, fmt.Sprintf("Queue %d", i+1)),
			Raw: q, Value: value, By: &top, Layers: []Layer{{At: top, Path: path, Set: true, Value: value}}})
	}
	return out
}

// expand lists the concrete paths of a key. A stage key takes default and
// each agent Stage, and model and effort take only the Stages that start a
// Worker, as the loader allows. The Linear keys take the workspaces and the
// teams of the file.
func (f *File) expand(k Key) []string {
	parts := strings.Split(k.Path, ".")
	if !slices.ContainsFunc(parts, isPlaceholder) {
		return []string{k.Path}
	}
	var out []string
	switch parts[0] {
	case "stage":
		out = append(out, "stage.default."+parts[2])
		for _, st := range lifecycle.Stages() {
			if st.Agent() && (st.StartsWorker() || parts[2] == "skill" || parts[2] == "command") {
				out = append(out, "stage."+st.Key()+"."+parts[2])
			}
		}
	case "github":
		for _, ev := range trackerEvents {
			out = append(out, "github.labels."+ev)
		}
	case "linear":
		linear, _ := f.Raw["linear"].(map[string]any)
		for _, ws := range slices.Sorted(maps.Keys(linear)) {
			if len(parts) == 3 {
				out = append(out, "linear."+ws+"."+parts[2])
				continue
			}
			w, _ := linear[ws].(map[string]any)
			status, _ := w["status"].(map[string]any)
			for _, team := range slices.Sorted(maps.Keys(status)) {
				for _, ev := range trackerEvents {
					out = append(out, "linear."+ws+".status."+team+"."+ev)
				}
			}
		}
	}
	return out
}

// repoTable is the key of the [repos."<key>"] table whose key equals key in
// any case, as the file writes it.
func (f *File) repoTable(key string) (string, bool) {
	repos, _ := f.Raw["repos"].(map[string]any)
	for k := range repos {
		if strings.EqualFold(k, key) {
			return k, true
		}
	}
	return "", false
}

// SetUp reports whether the file has the Repo's own table, in any case.
func (f *File) SetUp(repoKey string) bool {
	_, ok := f.repoTable(repoKey)
	return ok
}

// Checkouts lists the checkouts below the roots of the file.
func (f *File) Checkouts() []Checkout {
	var roots []string
	list, _ := f.Raw["roots"].([]any)
	for _, r := range list {
		if s, ok := r.(string); ok {
			roots = append(roots, s)
		}
	}
	home, _ := os.UserHomeDir()
	var out []Checkout
	for _, p := range findCheckouts(home, roots) {
		out = append(out, Checkout{Path: p, Key: checkoutKey(p)})
	}
	return out
}

// tableOf is the path of the table at w inside Raw.
func tableOf(w Where) []string {
	switch w.Level {
	case LevelDefaults:
		return []string{"defaults"}
	case LevelOwner, LevelRepo:
		return []string{"repos", w.Table}
	}
	return nil
}

func (f *File) lookup(w Where, path string) (any, bool) {
	var v any = f.Raw
	for _, part := range slices.Concat(tableOf(w), strings.Split(path, ".")) {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[part]; !ok {
			return nil, false
		}
	}
	return v, true
}

// valueText is the text of a decoded value: a string as it is, and any
// other value as TOML writes it.
func valueText(v any) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return tomlText(v)
}

func tomlText(v any) string {
	switch v := v.(type) {
	case string:
		return strconv.Quote(v)
	case []any:
		items := make([]string, len(v))
		for i, it := range v {
			items[i] = tomlText(it)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		var items []string
		for _, k := range slices.Sorted(maps.Keys(v)) {
			items = append(items, k+" = "+tomlText(v[k]))
		}
		return "{" + strings.Join(items, ", ") + "}"
	}
	return fmt.Sprint(v)
}

// Edit sets or removes one key at one level.
type Edit struct {
	Level Level
	// Repo is the key of the Repo table: owner/* for LevelOwner, and
	// owner/repo for LevelRepo.
	Repo string
	// Path is the concrete path inside the level, such as budgets.ci_debug.
	Path string
	// Value is the TOML text of the new value. Nil removes the key.
	Value *string
}

// Apply changes the file by one edit. It edits the text and keeps each line
// that the edit does not touch, comments and blank lines included. It writes
// the new text only after the loader passes it and after it checks that the
// file on disk still holds the text that it read. A loader error comes back
// unchanged, and then the file stays as it was (PROD-CONFIG-005).
func (f *File) Apply(e Edit) error {
	if f.LoadErr != nil {
		return fmt.Errorf("the file does not load, so lineup changes nothing in it. Fix it first: %w", f.LoadErr)
	}
	k, ok := keyFor(e.Path)
	switch {
	case !ok:
		return fmt.Errorf("%s is not a key of the config file", e.Path)
	case !slices.Contains(k.Levels, e.Level):
		return fmt.Errorf("%s cannot be set at %s: set it at %s", e.Path, e.Level, levelList(k.Levels))
	case k.Type == TypeQueue:
		return fmt.Errorf("%s: edit the review queues in the file", e.Path)
	}
	table, newRepo, err := f.tablePath(e)
	if err != nil {
		return err
	}
	parts := strings.Split(e.Path, ".")
	table = append(table, parts[:len(parts)-1]...)
	text, err := editText(f.Text, table, parts[len(parts)-1], e.Value, newRepo)
	if errors.Is(err, errNotSet) {
		return fmt.Errorf("%s is not set at %s", e.Path, Where{e.Level, e.Repo})
	}
	if err != nil {
		return fmt.Errorf("%s: %w", e.Path, err)
	}
	if err := sameEdit(f.Text, text, append(table, parts[len(parts)-1]), e.Value); err != nil {
		return fmt.Errorf("%s: %w", e.Path, err)
	}
	if _, err := parseGlobal(f.Path, text); err != nil {
		return err
	}
	switch data, err := os.ReadFile(f.Path); {
	case f.exists && err == nil && string(data) == f.Text, !f.exists && errors.Is(err, fs.ErrNotExist):
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	default:
		return fmt.Errorf("%s: %w. Read it again, then make the change again", f.Path, ErrFileChanged)
	}
	if !f.exists {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
	}
	if err := writeAtomic(f.Path, []byte(text), f.perm); err != nil {
		return err
	}
	f.Text, f.exists = text, true
	f.decode()
	return nil
}

func levelList(levels []Level) string {
	names := make([]string, len(levels))
	for i, l := range levels {
		names[i] = l.String()
	}
	if len(names) > 1 {
		names[len(names)-1] = "or " + names[len(names)-1]
	}
	if len(names) > 2 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names, " ")
}

// tablePath is the path of the level's table. newRepo is true for a Repo
// table that the file does not have yet.
func (f *File) tablePath(e Edit) (table []string, newRepo bool, err error) {
	if e.Level == LevelTop || e.Level == LevelDefaults {
		if e.Repo != "" {
			return nil, false, fmt.Errorf("%s has no Repo, but the edit names %s", e.Level, e.Repo)
		}
		return tableOf(Where{Level: e.Level}), false, nil
	}
	if _, repo, ok := splitKey(e.Repo); !ok || (repo == "*") != (e.Level == LevelOwner) {
		return nil, false, fmt.Errorf("%q is not a key of an %s table", e.Repo, e.Level)
	}
	key, ok := f.repoTable(e.Repo)
	if !ok {
		key = e.Repo
	}
	return tableOf(Where{e.Level, key}), !ok, nil
}

var errNotSet = errors.New("the key is not set")

// errPlace refuses a key in a form that a line edit cannot change safely,
// such as a key in an inline table.
func errPlace(why string) error {
	return fmt.Errorf("%s, so edit it in the file", why)
}

// editText sets the key of the table to the TOML value, or removes the key
// when value is nil. A new key goes after the last key of its table. A new
// table goes after the tables that share the most of its path, and newRepo
// adds the header of its Repo table above it. Every other line stays.
func editText(text string, table []string, key string, value *string, newRepo bool) (string, error) {
	items, err := parseItems(text)
	if err != nil {
		return "", err
	}
	if text == "" {
		items = nil
	}
	target := append(slices.Clone(table), key)
	// under is the table of each item: a header's own table, and the table
	// that holds a key. inArray marks the items of an [[array]] table.
	under := make([][]string, len(items))
	inArray := make([]bool, len(items))
	found, header := -1, -1
	var cur []string
	arr, dotted := false, false
	for i, it := range items {
		switch it.kind {
		case '[':
			name, _, array, _ := splitHeader(it.lines[it.above])
			cur, arr = keyParts(name), array
			if startsWith(cur, target) || array && hasPrefix(cur, target) {
				return "", errPlace("the file has a table of that name")
			}
			if !array && slices.Equal(cur, table) {
				header = i
			}
		case '=':
			if arr {
				break
			}
			line := it.lines[it.above]
			path := append(slices.Clone(cur), keyParts(line[:valueStart(line)-1])...)
			switch {
			case slices.Equal(path, target):
				found = i
			case hasPrefix(target, path) || hasPrefix(path, target):
				return "", errPlace("the file sets it in an inline table or a dotted key")
			case hasPrefix(table, path) && len(cur) < len(table):
				dotted = true
			}
		}
		under[i], inArray[i] = cur, arr
	}

	if found >= 0 {
		it := items[found]
		own := it.lines[it.above:]
		if it.multi || slices.ContainsFunc(own[:len(own)-1], func(l string) bool { return strings.Contains(l, "#") }) {
			return "", errPlace("its value in the file spans lines with comments")
		}
		keyLine := own[0]
		comment := trailingComment(own[len(own)-1])
		lines := slices.Clone(it.lines[:it.above])
		if value != nil {
			line := strings.TrimRight(keyLine[:valueStart(keyLine)-1], " 	") + " = " + *value
			lines = append(lines, strings.TrimRight(line+" "+comment, " "))
		} else if comment != "" {
			lines = append(lines, indentOf(keyLine)+comment)
		}
		items[found].lines = lines
		if len(lines) == 0 {
			items = slices.Delete(items, found, found+1)
		}
		return endLine(keepLayout(items, text), text), nil
	}
	if value == nil {
		return "", errNotSet
	}
	if dotted {
		return "", errPlace("dotted keys in the file define its table")
	}

	keyItem := func(indent string) item {
		return item{lines: []string{indent + bareOrQuoted(key) + " = " + *value}, kind: '='}
	}
	blank := item{lines: []string{""}, kind: ' '}
	if len(table) == 0 || header >= 0 {
		// The table's keys run to the next header. The keys of the root
		// table come before the first header.
		end := len(items)
		for i := header + 1; i < len(items); i++ {
			if items[i].kind == '[' {
				end = i
				break
			}
		}
		at, indent := header+1, ""
		if header < 0 {
			at = end
		}
		for i := header + 1; i < end; i++ {
			if items[i].kind == '=' {
				at, indent = i+1, indentOf(items[i].lines[items[i].above])
			}
		}
		add := []item{keyItem(indent)}
		if header < 0 && at == end && end < len(items) {
			add = append(add, blank)
		}
		return endLine(keepLayout(slices.Insert(items, at, add...), text), text), nil
	}

	var block []string
	if newRepo && len(table) > 2 {
		block = append(block, headerText(false, joinKey(table[:2])))
	}
	block = append(block, headerText(false, joinKey(table)))
	add := []item{{lines: block, kind: '['}, keyItem("")}
	// The new table goes after the last line of the tables that share the
	// most of its path, or at the end.
	at, shared := len(items), 0
	for n := len(table) - 1; n >= 1 && shared == 0; n-- {
		for i := range items {
			if items[i].kind != ' ' && !inArray[i] && startsWith(under[i], table[:n]) {
				at, shared = i+1, n
			}
		}
	}
	if at < len(items) && items[at].kind != ' ' {
		add = append(add, blank)
	}
	if at > 0 && items[at-1].kind != ' ' && (shared < 2 || newRepo) {
		add = append([]item{blank}, add...)
	}
	return endLine(keepLayout(slices.Insert(items, at, add...), text), text), nil
}

// endLine ends a text that was empty with a newline.
func endLine(s, old string) string {
	if old == "" && s != "" && !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

// hasPrefix reports whether path starts with prefix and is longer.
func hasPrefix(prefix, path []string) bool {
	return len(path) > len(prefix) && startsWith(path, prefix)
}

// startsWith reports whether path starts with prefix or equals it.
func startsWith(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}

// keyParts splits a dotted TOML key into its parts, without their quotes.
func keyParts(s string) []string {
	var out []string
	for {
		first, rest := firstKey(s)
		out = append(out, first)
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ".") {
			return out
		}
		s = rest[1:]
	}
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func bareOrQuoted(part string) string {
	if bareKey.MatchString(part) {
		return part
	}
	return strconv.Quote(part)
}

func joinKey(parts []string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = bareOrQuoted(p)
	}
	return strings.Join(out, ".")
}

// sameEdit checks that the new text decodes to the old text with only the
// edit applied. A table that a removal leaves empty goes away, unless a
// header of the old text names it.
func sameEdit(oldText, newText string, path []string, value *string) error {
	var want, got map[string]any
	if _, err := toml.Decode(oldText, &want); err != nil {
		return err
	}
	if _, err := toml.Decode(newText, &got); err != nil {
		return fmt.Errorf("the edit does not give TOML (%w), so edit it in the file", err)
	}
	if want == nil {
		want = map[string]any{}
	}
	if got == nil {
		got = map[string]any{}
	}
	items, err := parseItems(oldText)
	if err != nil {
		return err
	}
	headers := map[string]bool{}
	for _, it := range items {
		if name, _, array, _ := splitHeader(it.lines[it.above]); it.kind == '[' && !array {
			headers[strings.Join(keyParts(name), "\x00")] = true
		}
	}
	tables := []map[string]any{want}
	for _, part := range path[:len(path)-1] {
		m := tables[len(tables)-1]
		next, ok := m[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[part] = next
		}
		tables = append(tables, next)
	}
	last := tables[len(tables)-1]
	if value == nil {
		delete(last, path[len(path)-1])
		for i := len(tables) - 1; i > 0 && len(tables[i]) == 0 && !headers[strings.Join(path[:i], "\x00")]; i-- {
			delete(tables[i-1], path[i-1])
		}
	} else {
		var v map[string]any
		if _, err := toml.Decode("v = "+*value, &v); err != nil {
			return fmt.Errorf("%s is not a TOML value: %w", *value, err)
		}
		last[path[len(path)-1]] = v["v"]
	}
	if !reflect.DeepEqual(got, want) {
		return errPlace("lineup cannot change it in the file safely")
	}
	return nil
}
