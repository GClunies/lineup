package localissues

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

func genIssue(t *rapid.T) Issue {
	line := rapid.StringMatching(`[^\r\n]{0,30}`)
	is := Issue{
		Title:  rapid.StringMatching(`[^\r\n]*[^\s][^\r\n]*`).Draw(t, "title"),
		Repo:   line.Draw(t, "repo"),
		Status: rapid.SampledFrom(Statuses).Draw(t, "status"),
		Body:   rapid.StringMatching(`[^\r]{0,80}`).Draw(t, "body"),
	}
	for _, n := range rapid.SliceOfN(rapid.IntRange(1, 999), 0, 3).Draw(t, "blocked") {
		is.BlockedBy = append(is.BlockedBy, fmt.Sprintf("local-%d", n))
	}
	return is
}

func TestParseAfterMarshalGivesTheSameIssue(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want := genIssue(t)
		got, err := Parse(want.Marshal())
		if err != nil {
			t.Fatalf("parse %q: %v", want.Marshal(), err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip\n got %#v\nwant %#v\nfile %q", got, want, want.Marshal())
		}
	})
}

func TestCreateNeverReusesAnID(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := Store{Dir: filepath.Join(os.TempDir(), "localissues-"+rapid.StringMatching(`[a-z]{12}`).Draw(t, "dir"))}
		defer os.RemoveAll(s.Dir)
		os.MkdirAll(s.Dir, 0o755)
		for _, n := range rapid.SliceOfNDistinct(rapid.IntRange(1, 50), 0, 5, rapid.ID[int]).Draw(t, "existing") {
			os.WriteFile(s.path(n), Issue{Title: "old", Status: Todo}.Marshal(), 0o644)
		}
		writers := rapid.IntRange(1, 8).Draw(t, "writers")
		ids := make(chan string, writers)
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				is, err := s.Create(fmt.Sprintf("new %d", i), "", "")
				if err != nil {
					t.Errorf("create: %v", err)
					return
				}
				ids <- is.ID()
			}()
		}
		wg.Wait()
		close(ids)
		seen := map[string]bool{}
		for id := range ids {
			if seen[id] {
				t.Fatalf("two creates took %s", id)
			}
			seen[id] = true
			got, err := s.Get(id)
			if err != nil || !strings.HasPrefix(got.Title, "new ") {
				t.Fatalf("%s holds %+v, %v: a create overwrote another file", id, got, err)
			}
		}
	})
}

func TestABrokenFileIsReportedAndTheRestStillList(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Create("Good one", "data", ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.Dir, "local-2.md"), []byte("no frontmatter here"), 0o644)
	os.WriteFile(filepath.Join(s.Dir, "notes.md"), []byte("not an Issue file"), 0o644)
	got, broken, err := s.List()
	if err != nil || len(got) != 1 || got[0].ID() != "local-1" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if len(broken) != 1 || broken[0].File != "local-2.md" {
		t.Fatalf("broken = %+v", broken)
	}
}

func TestSetStatusKeepsTheRestOfTheFile(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	file := "---\n# written by the Planner\ntitle: Retry failed loads\nestimate: 3\nstatus: todo\nblocked_by:\n  - local-3\n---\n\nKeep this body.\n"
	os.WriteFile(filepath.Join(s.Dir, "local-7.md"), []byte(file), 0o644)
	if err := s.SetStatus("LOCAL-7", InReview); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepo("local-7", "data"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, "local-7.md"))
	want := "---\n# written by the Planner\ntitle: Retry failed loads\nestimate: 3\nstatus: in review\nblocked_by:\n  - local-3\nrepo: data\n---\n\nKeep this body.\n"
	if string(b) != want {
		t.Fatalf("file\n%s\nwant\n%s", b, want)
	}
	is, err := s.Get("local-7")
	if err != nil || is.Status != InReview || is.Repo != "data" || !reflect.DeepEqual(is.BlockedBy, []string{"local-3"}) {
		t.Fatalf("%+v %v", is, err)
	}
	if err := s.SetStatus("local-7", "blocked"); err == nil {
		t.Fatal("an unknown status was written")
	}
}

func TestBranchAndParseID(t *testing.T) {
	is := Issue{N: 7, Title: "Retry failed loads before alerting!"}
	if got := is.Branch(); got != "local-7-retry-failed-loads-before-alerting" {
		t.Errorf("branch = %q", got)
	}
	for id, want := range map[string]int{"local-7": 7, "LOCAL-12": 12} {
		if n, err := ParseID(id); err != nil || n != want {
			t.Errorf("ParseID(%q) = %d, %v", id, n, err)
		}
	}
	for _, id := range []string{"local-0", "local-x", "ENG-7", "local-07"} {
		if _, err := ParseID(id); err == nil {
			t.Errorf("ParseID(%q) passed", id)
		}
	}
}
