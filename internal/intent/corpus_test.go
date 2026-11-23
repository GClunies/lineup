package intent

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const repoRoot = "../.."

type corpus struct {
	dir, prefix, kind, intro string
	notes                    bool
}

var corpora = []corpus{
	{dir: "docs/product-intent", prefix: "PROD", kind: "Product Intent", intro: "This document states the current intended experience for "},
	{dir: "docs/architecture-intent", prefix: "ARCH", kind: "Architecture Intent", intro: "This file states ", notes: true},
}

var (
	entryRe   = regexp.MustCompile(`^- \[([^\]]+)\]\(([a-z-]+\.md)\): (.+)\.$`)
	headingRe = regexp.MustCompile(`^## ((PROD|ARCH)-([A-Z]+)-(\d{3})): (\S.*)$`)
	idRe      = regexp.MustCompile(`\b(?:PROD|ARCH)-[A-Z]+-\d{3}\b`)
	codeRe    = regexp.MustCompile("`[^`]*`")
	pathRe    = regexp.MustCompile(`(^|[\s(])(~/|/[A-Za-z]|\./)|\b(internal|cmd)/|\.(go|lua|toml|json|jsonl|md|db|plist|lock)\b`)
)

type entry struct{ name, file, covers string }

func readEntries(t *testing.T, c corpus) []entry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, c.dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var out []entry
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "## Domains" {
			in = true
			continue
		}
		if !in {
			continue
		}
		if m := entryRe.FindStringSubmatch(line); m != nil {
			out = append(out, entry{m[1], m[2], m[3]})
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s/README.md: no domain entries under ## Domains", c.dir)
	}
	return out
}

// words counts the words of s, with each code span as one word.
func words(s string) int { return len(strings.Fields(codeRe.ReplaceAllString(s, "x"))) }

func sentences(s string) []string {
	s = codeRe.ReplaceAllString(s, "x")
	var out []string
	for _, p := range regexp.MustCompile(`[.!?]\s+`).Split(s, -1) {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

type statement struct {
	id, title string
	lines     []string
}

func parseDomain(t *testing.T, c corpus, e entry) []statement {
	t.Helper()
	path := filepath.Join(c.dir, e.file)
	data, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Errorf("%s: %v (listed in README)", path, err)
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 {
		t.Errorf("%s: needs a title and an intro", path)
		return nil
	}
	if want := "# " + e.name + " " + c.kind; lines[0] != want {
		t.Errorf("%s: title %q, want %q", path, lines[0], want)
	}
	if want := c.intro + e.covers + "."; lines[1] != want {
		t.Errorf("%s: intro %q, want %q", path, lines[1], want)
	}
	var out []statement
	for _, l := range lines[2:] {
		if strings.HasPrefix(l, "#") {
			m := headingRe.FindStringSubmatch(l)
			if m == nil || m[2] != c.prefix {
				t.Errorf("%s: heading %q is not ## %s-<DOMAIN>-<NNN>: <title>", path, l, c.prefix)
				continue
			}
			out = append(out, statement{id: m[1], title: m[5]})
			continue
		}
		if len(out) == 0 {
			t.Errorf("%s: text before the first statement: %q", path, l)
			continue
		}
		out[len(out)-1].lines = append(out[len(out)-1].lines, l)
	}
	if len(out) == 0 {
		t.Errorf("%s: no statements", path)
	}
	return out
}

func checkStatement(t *testing.T, c corpus, path string, s statement) {
	t.Helper()
	var rule []string
	var because, not int
	for i, l := range s.lines {
		switch {
		case strings.HasPrefix(l, "Because: "):
			because++
			if not > 0 {
				t.Errorf("%s %s: Because: comes after Not:", path, s.id)
			}
		case strings.HasPrefix(l, "Not: "):
			not++
		default:
			if because+not > 0 {
				t.Errorf("%s %s: rule text after a Because: or Not: line (line %d)", path, s.id, i+1)
			}
			rule = append(rule, l)
		}
	}
	if !c.notes && because+not > 0 {
		t.Errorf("%s %s: a PROD statement has no Because: or Not: line", path, s.id)
	}
	if because > 1 || not > 1 {
		t.Errorf("%s %s: at most one Because: line and one Not: line", path, s.id)
	}
	text := strings.Join(rule, " ")
	if text == "" {
		t.Errorf("%s %s: no rule", path, s.id)
	}
	if n := words(text); n > 100 {
		t.Errorf("%s %s: rule has %d words, the cap is 100", path, s.id, n)
	}
	all := strings.Join(s.lines, " ")
	for _, sen := range sentences(strings.NewReplacer("Because: ", "", "Not: ", "").Replace(all)) {
		if n := words(sen); n > 35 {
			t.Errorf("%s %s: a sentence has %d words, the cap is 35: %q", path, s.id, n, sen)
		}
	}
	if id := idRe.FindString(s.title + " " + all); id != "" {
		t.Errorf("%s %s: points to %s. State the rule on its own terms", path, s.id, id)
	}
	if m := pathRe.FindString(codeRe.ReplaceAllStringFunc(all, func(code string) string {
		if strings.Contains(code, " ") {
			return "x"
		}
		return code
	})); m != "" {
		t.Errorf("%s %s: names a file path (%q)", path, s.id, strings.TrimSpace(m))
	}
}

// statementIDs checks each corpus and returns every ID that it holds.
func statementIDs(t *testing.T) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, c := range corpora {
		entries := readEntries(t, c)
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = strings.ToLower(e.name)
		}
		if !sort.StringsAreSorted(names) {
			t.Errorf("%s/README.md: domain entries are not in alphabetical order", c.dir)
		}
		listed := map[string]bool{"README.md": true}
		domains := map[string]string{}
		fileDomain := map[string]string{}
		for _, e := range entries {
			listed[e.file] = true
			path := filepath.Join(c.dir, e.file)
			last := 0
			for _, s := range parseDomain(t, c, e) {
				parts := strings.Split(s.id, "-")
				if other, ok := domains[parts[1]]; ok && other != e.file {
					t.Errorf("%s %s: domain %s belongs to %s", path, s.id, parts[1], other)
				}
				domains[parts[1]] = e.file
				if code, ok := fileDomain[e.file]; ok && code != parts[1] {
					t.Errorf("%s %s: the file holds domain %s", path, s.id, code)
				}
				fileDomain[e.file] = parts[1]
				n, _ := strconv.Atoi(parts[2])
				if n <= last {
					t.Errorf("%s %s: numbers must rise through the file", path, s.id)
				}
				last = n
				if ids[s.id] {
					t.Errorf("%s: %s is used twice", path, s.id)
				}
				ids[s.id] = true
				checkStatement(t, c, path, s)
			}
		}
		files, _ := os.ReadDir(filepath.Join(repoRoot, c.dir))
		for _, f := range files {
			if !listed[f.Name()] {
				t.Errorf("%s/%s: not listed under ## Domains in the README", c.dir, f.Name())
			}
		}
	}
	return ids
}

func TestCorpora(t *testing.T) {
	statementIDs(t)
}

func TestCitationsNameRealStatements(t *testing.T) {
	ids := statementIDs(t)
	skip := map[string]bool{".git": true, ".audit": true, "node_modules": true}
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".md", ".html", ".lua", ".toml", ".txt":
		default:
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for n := 1; sc.Scan(); n++ {
			for _, id := range idRe.FindAllString(sc.Text(), -1) {
				if !ids[id] && !strings.HasSuffix(path, "corpus_test.go") {
					t.Errorf("%s:%d cites %s, which no statement holds", path, n, id)
				}
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoADRsOrSpecs(t *testing.T) {
	for _, dir := range []string{filepath.Join("docs", "adr"), filepath.Join("docs", "spec")} {
		if _, err := os.Stat(filepath.Join(repoRoot, dir)); err == nil {
			t.Errorf("%s exists. lineup keeps its intent in docs/product-intent and docs/architecture-intent", dir)
		} else if !os.IsNotExist(err) {
			t.Fatal(fmt.Errorf("%s: %w", dir, err))
		}
	}
}
