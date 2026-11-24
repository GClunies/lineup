package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// decodedKeys walks the type of a table and gives the path of each key that
// the loader decodes in it. A map gives a placeholder part for its keys, and
// a list of tables gives one placeholder for the keys of its entries.
func decodedKeys(t reflect.Type, prefix []string, add func(path []string)) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			tag := f.Tag.Get("toml")
			if f.Anonymous && tag == "" {
				decodedKeys(f.Type, prefix, add)
				continue
			}
			decodedKeys(f.Type, append(slices.Clone(prefix), tag), add)
		}
	case t.Kind() == reflect.Map:
		decodedKeys(t.Elem(), append(slices.Clone(prefix), "<>"), add)
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct:
		add(append(slices.Clone(prefix), "<>"))
	default:
		add(prefix)
	}
}

// placeholdersErased writes each placeholder of a Path as <>.
func placeholdersErased(path string) string {
	parts := strings.Split(path, ".")
	for i, p := range parts {
		if isPlaceholder(p) {
			parts[i] = "<>"
		}
	}
	return strings.Join(parts, ".")
}

// TestTheCatalogListsEachDecodedKey ties the catalog to the loader: each key
// that the loader decodes has an entry, each entry is a key that the loader
// decodes, and each entry sits only at a level where the loader reads it.
func TestTheCatalogListsEachDecodedKey(t *testing.T) {
	where := map[string][]Level{}
	addAt := func(levels ...Level) func([]string) {
		return func(path []string) {
			p := strings.Join(path, ".")
			for _, l := range levels {
				if !slices.Contains(where[p], l) {
					where[p] = append(where[p], l)
				}
			}
		}
	}
	file := reflect.TypeFor[globalFile]()
	for i := range file.NumField() {
		f := file.Field(i)
		switch tag := f.Tag.Get("toml"); tag {
		case "defaults":
			decodedKeys(f.Type, nil, addAt(LevelDefaults))
		case "repos":
			decodedKeys(f.Type.Elem(), nil, addAt(LevelOwner, LevelRepo))
		default:
			decodedKeys(f.Type, []string{tag}, addAt(LevelTop))
		}
	}
	if len(where) < 30 {
		t.Fatalf("the walk found %d keys, too few: %v", len(where), where)
	}

	entries := map[string]bool{}
	for _, k := range Catalog {
		if entries[k.Path] {
			t.Errorf("%s has two entries", k.Path)
		}
		entries[k.Path] = true
		if k.Doc == "" || k.Default == "" || k.Restart == "" || len(k.Levels) == 0 {
			t.Errorf("%s: an entry needs a doc, a default, a restart, and its levels: %+v", k.Path, k)
		}
		if len(k.Group) == 0 || slices.Contains(k.Group, "") || k.Label == "" {
			t.Errorf("%s: an entry needs a group and a label: %q %q", k.Path, k.Group, k.Label)
		}
		// Each placeholder names a group or the label, so that two concrete
		// keys of one entry never share a place. A queue takes its own name.
		for _, p := range strings.Split(k.Path, ".") {
			named := strings.Contains(strings.Join(append(slices.Clone(k.Group), k.Label), "\x00"), p)
			if isPlaceholder(p) && k.Type != TypeQueue && !named {
				t.Errorf("%s: no group or label names %s", k.Path, p)
			}
		}
	}
	for path := range where {
		if !slices.ContainsFunc(Catalog, func(k Key) bool { return placeholdersErased(k.Path) == path }) {
			t.Errorf("the loader decodes %s, and the catalog has no entry for it", path)
		}
	}
	for _, k := range Catalog {
		var levels []Level
		found := false
		for path, ls := range where {
			if fits(strings.Split(path, "."), strings.Split(placeholdersErased(k.Path), ".")) {
				found, levels = true, ls
				break
			}
		}
		if !found {
			t.Errorf("the catalog lists %s, and the loader decodes no such key", k.Path)
			continue
		}
		for _, l := range k.Levels {
			if !slices.Contains(levels, l) {
				t.Errorf("the catalog puts %s at %s, and the loader reads it only at %v", k.Path, l, levels)
			}
		}
	}
}

func TestKeyForPrefersTheEntryOfTheExactPath(t *testing.T) {
	for path, want := range map[string]string{
		"github.labels.backlog":         "github.labels.backlog",
		"github.labels.started":         "github.labels.<event>",
		"stage.working.model":           "stage.<stage>.model",
		"linear.acme.status.ENG.merged": "linear.<workspace>.status.<team>.<event>",
		"wip_cap":                       "wip_cap",
	} {
		if k, ok := keyFor(path); !ok || k.Path != want {
			t.Errorf("keyFor(%s) = %s %v, want %s", path, k.Path, ok, want)
		}
	}
	for _, path := range []string{"wip_capp", "stage.working", "stage..model", "budgets"} {
		if k, ok := keyFor(path); ok {
			t.Errorf("keyFor(%s) = %s, want no entry", path, k.Path)
		}
	}
}

func TestEncodeWritesTheTOMLOfEachType(t *testing.T) {
	key := func(path string) Key {
		k, ok := keyFor(path)
		if !ok {
			t.Fatalf("no entry for %s", path)
		}
		return k
	}
	for _, c := range []struct{ path, in, want string }{
		{"wip_cap", " 7 ", "7"},
		{"poll_interval", "1m", `"1m"`},
		{"local_tracker", "true", "true"},
		{"landing_bar.checks", "all", `"all"`},
		{"roots", "~/repos, ~/work", `["~/repos", "~/work"]`},
		{"board.hidden", "", "[]"},
		{"stage.default.skill", `say "hi"`, `"say \"hi\""`},
	} {
		if got, err := key(c.path).Encode(c.in); err != nil || got != c.want {
			t.Errorf("%s: Encode(%q) = %s %v, want %s", c.path, c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ path, in string }{
		{"wip_cap", "five"}, {"local_tracker", "yes"}, {"roots", "a,,b"}, {"review.queues.<field>", "x"},
	} {
		if got, err := key(c.path).Encode(c.in); err == nil {
			t.Errorf("%s: Encode(%q) = %s, want an error", c.path, c.in, got)
		}
	}
}

func TestEachKeyHasAPlainPlace(t *testing.T) {
	for path, want := range map[string]string{
		"linear.acme.status.ENG.in_review": "Linear › Workspace acme › Statuses for team ENG › In review",
		"linear.acme.client_id":            "Linear › Workspace acme › OAuth app",
		"linear_client_id":                 "Linear › OAuth app",
		"github.labels.started":            "GitHub labels › At start",
		"github.labels.backlog":            "GitHub labels › Backlog",
		"stage.default.model":              "Workers › Every Stage › Model",
		"stage.ci_debug.skill":             "Workers › Stage CI debug › Skill",
		"review_bot.min_review_score":      "Review bot › Min pass score",
	} {
		k, ok := keyFor(path)
		if !ok {
			t.Fatalf("no entry for %s", path)
		}
		group, label := k.place(path)
		if got := strings.Join(append(group, label), " › "); got != want {
			t.Errorf("%s is at %q, want %q", path, got, want)
		}
	}
	for _, ev := range trackerEvents {
		if eventNames[ev] == "" {
			t.Errorf("the event %s has no plain name", ev)
		}
	}
}

// TestEachTopGroupHasADoc ties Groups to the catalog: each top group of a key
// has a doc, and each entry of Groups is a top group.
func TestEachTopGroupHasADoc(t *testing.T) {
	tops := []string{DefaultsGroup}
	for _, k := range Catalog {
		if !slices.Contains(tops, k.Group[0]) {
			tops = append(tops, k.Group[0])
		}
	}
	for _, title := range tops {
		if GroupDoc(title) == "" {
			t.Errorf("the group %s has no doc", title)
		}
	}
	for _, g := range Groups {
		if !slices.Contains(tops, g.Title) {
			t.Errorf("Groups has %s, which is no top group of the catalog", g.Title)
		}
	}
}
