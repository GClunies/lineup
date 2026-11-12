// Package localissues is the Local Tracker: one folder of Markdown Issues,
// each a file local-<n>.md with a frontmatter of title, repo, status, and
// blocked_by.
package localissues

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Status string

const (
	Backlog  Status = "backlog"
	Todo     Status = "todo"
	Started  Status = "started"
	InReview Status = "in review"
	Done     Status = "done"
	Canceled Status = "canceled"
)

// Statuses are the values of the status field, in workflow order.
var Statuses = []Status{Backlog, Todo, Started, InReview, Done, Canceled}

type Issue struct {
	N         int
	Title     string
	Repo      string
	Status    Status
	BlockedBy []string
	Body      string
}

func (is Issue) ID() string { return "local-" + strconv.Itoa(is.N) }

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Branch is local-<n>-<title slug>.
func (is Issue) Branch() string {
	slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(is.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		return is.ID()
	}
	return is.ID() + "-" + slug
}

// Broken is a file in the folder that does not parse as an Issue.
type Broken struct {
	File string
	Err  error
}

var fileName = regexp.MustCompile(`^local-([1-9][0-9]*)\.md$`)

// ParseID reads n from local-<n>, in any case.
func ParseID(id string) (int, error) {
	m := fileName.FindStringSubmatch(strings.ToLower(id) + ".md")
	if m == nil {
		return 0, fmt.Errorf("%q is not a local Issue ID such as local-7", id)
	}
	return strconv.Atoi(m[1])
}

type Store struct{ Dir string }

func (s Store) path(n int) string { return filepath.Join(s.Dir, "local-"+strconv.Itoa(n)+".md") }

// List reads every Issue in the folder, lowest ID first. A missing folder
// holds no Issues.
func (s Store) List() ([]Issue, []Broken, error) {
	ents, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Issue
	var broken []Broken
	for _, e := range ents {
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		is, err := s.read(n)
		if err != nil {
			broken = append(broken, Broken{File: e.Name(), Err: err})
			continue
		}
		out = append(out, is)
	}
	slices.SortFunc(out, func(a, b Issue) int { return a.N - b.N })
	return out, broken, nil
}

func (s Store) Get(id string) (Issue, error) {
	n, err := ParseID(id)
	if err != nil {
		return Issue{}, err
	}
	return s.read(n)
}

func (s Store) read(n int) (Issue, error) {
	data, err := os.ReadFile(s.path(n))
	if err != nil {
		return Issue{}, err
	}
	is, err := Parse(data)
	is.N = n
	return is, err
}

// Create writes a new Issue in the backlog with the next free ID. O_EXCL
// makes two writers that pick the same ID retry instead of overwriting each
// other.
func (s Store) Create(title, repo, body string) (Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return Issue{}, errors.New("an Issue needs a title of one line")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Issue{}, err
	}
	is := Issue{Title: title, Repo: strings.TrimSpace(repo), Status: Backlog, Body: body}
	for range 100 {
		n, err := s.next()
		if err != nil {
			return Issue{}, err
		}
		f, err := os.OpenFile(s.path(n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return Issue{}, err
		}
		is.N = n
		_, werr := f.Write(is.Marshal())
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return is, werr
	}
	return Issue{}, errors.New("no free local Issue ID after 100 tries")
}

func (s Store) next() (int, error) {
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return 0, err
	}
	hi := 0
	for _, e := range ents {
		if m := fileName.FindStringSubmatch(e.Name()); m != nil {
			n, _ := strconv.Atoi(m[1])
			hi = max(hi, n)
		}
	}
	return hi + 1, nil
}

// SetStatus rewrites the status line and leaves the rest of the file as it is.
func (s Store) SetStatus(id string, st Status) error {
	if !slices.Contains(Statuses, st) {
		return fmt.Errorf("unknown status %q", st)
	}
	return s.setField(id, "status", string(st))
}

// SetRepo rewrites the repo line, or adds one.
func (s Store) SetRepo(id, repo string) error { return s.setField(id, "repo", repo) }

func (s Store) setField(id, key, value string) error {
	n, err := ParseID(id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(s.path(n))
	if err != nil {
		return err
	}
	if _, err := Parse(data); err != nil {
		return err
	}
	front, rest, _ := split(data)
	lines := strings.Split(front, "\n")
	line := key + ": " + quote(value)
	at := slices.IndexFunc(lines, func(l string) bool { k, _, ok := strings.Cut(l, ":"); return ok && strings.TrimSpace(k) == key })
	if at >= 0 {
		lines[at] = line
	} else {
		lines = append(lines, line)
	}
	out := "---\n" + strings.Join(lines, "\n") + "\n---\n" + rest
	tmp := s.path(n) + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(n))
}

// split returns the frontmatter without its fences, and the text after it.
func split(data []byte) (front, rest string, err error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", "", errors.New("no frontmatter: the file must start with ---")
	}
	s = s[4:]
	if strings.HasPrefix(s, "---\n") || s == "---" {
		return "", strings.TrimPrefix(s[3:], "\n"), nil
	}
	end := strings.Index(s, "\n---\n")
	if end < 0 {
		if strings.HasSuffix(s, "\n---") {
			return s[:len(s)-4], "", nil
		}
		return "", "", errors.New("the frontmatter has no closing ---")
	}
	return s[:end], s[end+5:], nil
}

// Parse reads one Issue file. Fields other than the four it knows stay in
// the file and are ignored here.
func Parse(data []byte) (Issue, error) {
	front, rest, err := split(data)
	if err != nil {
		return Issue{}, err
	}
	var is Issue
	var inList bool
	for _, l := range strings.Split(front, "\n") {
		if inList {
			if item, ok := strings.CutPrefix(strings.TrimSpace(l), "- "); ok {
				is.BlockedBy = append(is.BlockedBy, unquote(item))
				continue
			}
			inList = false
		}
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			return Issue{}, fmt.Errorf("frontmatter line %q has no key", l)
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "title":
			is.Title = unquote(v)
		case "repo":
			is.Repo = unquote(v)
		case "status":
			is.Status = Status(unquote(v))
		case "blocked_by":
			switch {
			case v == "":
				inList = true
			case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
				for _, x := range strings.Split(v[1:len(v)-1], ",") {
					if x = unquote(strings.TrimSpace(x)); x != "" {
						is.BlockedBy = append(is.BlockedBy, x)
					}
				}
			default:
				return Issue{}, fmt.Errorf("blocked_by must be a list, such as [local-3]")
			}
		}
	}
	if is.Title == "" {
		return Issue{}, errors.New("no title")
	}
	if !slices.Contains(Statuses, is.Status) {
		return Issue{}, fmt.Errorf("status %q is not one of backlog, todo, started, in review, done, canceled", is.Status)
	}
	is.Body = strings.TrimPrefix(rest, "\n")
	return is, nil
}

// Marshal writes the Issue in the form that Parse reads.
func (is Issue) Marshal() []byte {
	var b bytes.Buffer
	b.WriteString("---\ntitle: " + quote(is.Title) + "\n")
	if is.Repo != "" {
		b.WriteString("repo: " + quote(is.Repo) + "\n")
	}
	b.WriteString("status: " + quote(string(is.Status)) + "\n")
	if len(is.BlockedBy) > 0 {
		q := make([]string, len(is.BlockedBy))
		for i, x := range is.BlockedBy {
			q[i] = quote(x)
		}
		b.WriteString("blocked_by: [" + strings.Join(q, ", ") + "]\n")
	}
	b.WriteString("---\n")
	if is.Body != "" {
		b.WriteString("\n" + is.Body)
	}
	return b.Bytes()
}

// quote double-quotes a value that a plain scalar can't hold.
func quote(s string) string {
	if s == "" || strings.ContainsAny(s, `:#[]{},"'\`) || s != strings.TrimSpace(s) ||
		strings.ContainsAny(s[:1], "-&*!|>%@`?") {
		return strconv.Quote(s)
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}
