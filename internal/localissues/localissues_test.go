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
			os.WriteFile(s.path(DefaultPrefix, n), Issue{Title: "old", Status: Todo}.Marshal(), 0o644)
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

func TestCreateWritesANewIssueInTheBacklog(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	is, err := s.Create("Retry failed loads", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, is.File()))
	if !strings.Contains(string(b), "\nstatus: backlog\n") {
		t.Fatalf("file\n%s\nwant status: backlog", b)
	}
	if got, err := s.Get(is.ID()); err != nil || got.Status != Backlog {
		t.Fatalf("read back %+v, %v, want status backlog", got, err)
	}
}

func TestABrokenFileIsReportedAndTheRestStillList(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Create("Good one", "data", ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.Dir, "local-2.md"), []byte("no frontmatter here"), 0o644)
	os.WriteFile(filepath.Join(s.Dir, "notes.md"), []byte("not an Issue file"), 0o644)
	got, broken, err := s.List()
	if err != nil || len(got) != 1 || got[0].ID() != "LOCAL-1" {
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

func TestPromoteCancelsTheIssueAndEndsTheBodyWithTheLinearID(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	rapid.Check(t, func(t *rapid.T) {
		want := genIssue(t)
		want.N = 7
		os.WriteFile(s.path(DefaultPrefix, 7), want.Marshal(), 0o644)
		if err := s.Promote("local-7", "ENG-612"); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get("local-7")
		if err != nil {
			t.Fatalf("parse after Promote: %v", err)
		}
		body := want.Body
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if body != "" {
			body += "\n"
		}
		want.Status, want.Body = Canceled, body+"Promoted to ENG-612.\n"
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("after Promote\n got %#v\nwant %#v", got, want)
		}
	})
}

func TestBranchAndParseID(t *testing.T) {
	is := Issue{N: 7, Title: "Retry failed loads before alerting!"}
	if got := is.Branch(); got != "local-7-retry-failed-loads-before-alerting" {
		t.Errorf("branch = %q", got)
	}
	if got := (Issue{Prefix: "hod", N: 3, Title: "Block Linear writes"}).Branch(); got != "hod-3-block-linear-writes" {
		t.Errorf("prefixed branch = %q", got)
	}
	s := Store{Prefixes: []string{"hod", "web-2"}}
	for id, want := range map[string]struct {
		prefix string
		n      int
	}{
		"local-7": {"local", 7}, "LOCAL-12": {"local", 12}, "hod-3": {"hod", 3}, "HOD-3": {"hod", 3}, "web-2-9": {"web-2", 9},
	} {
		if p, n, err := s.ParseID(id); err != nil || p != want.prefix || n != want.n {
			t.Errorf("ParseID(%q) = %q, %d, %v", id, p, n, err)
		}
	}
	for _, id := range []string{"local-0", "local-x", "ENG-7", "local-07", "hod-0", "web-9", "hod", "dex-3"} {
		if _, _, err := s.ParseID(id); err == nil {
			t.Errorf("ParseID(%q) passed", id)
		}
	}
}

func TestAPrefixedIssueLivesInItsOwnFileAndNumbersOnItsOwn(t *testing.T) {
	s := Store{Dir: t.TempDir(), Prefixes: []string{"hod", "web"}}
	for name, is := range map[string]Issue{
		"local-1.md": {Title: "a", Status: Todo},
		"hod-1.md":   {Title: "b", Repo: "hod", Status: Todo},
		"hod-4.md":   {Title: "c", Status: Backlog},
		"web-2.md":   {Title: "d", Status: Todo},
		"dex-9.md":   {Title: "no such prefix", Status: Todo},
		"HOD-7.md":   {Title: "upper case", Status: Todo},
	} {
		os.WriteFile(filepath.Join(s.Dir, name), is.Marshal(), 0o644)
	}
	list, broken, err := s.List()
	if err != nil || len(broken) > 0 {
		t.Fatalf("list: %v %v", broken, err)
	}
	var ids []string
	for _, is := range list {
		ids = append(ids, is.ID())
	}
	if want := []string{"LOCAL-1", "HOD-1", "HOD-4", "WEB-2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("List IDs = %q, want %q", ids, want)
	}
	for prefix, want := range map[string]int{"local": 1, "hod": 4, "web": 2, "dex": 0} {
		if got, err := s.Highest(prefix); err != nil || got != want {
			t.Errorf("Highest(%s) = %d, %v, want %d", prefix, got, err, want)
		}
	}
	got, err := s.Get("HOD-4")
	if err != nil || got.ID() != "HOD-4" || got.File() != "hod-4.md" || got.Title != "c" {
		t.Fatalf("Get(HOD-4) = %+v, %v", got, err)
	}
	if err := s.SetStatus("hod-4", Started); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("hod-4"); got.Status != Started {
		t.Fatalf("hod-4 status %q after SetStatus", got.Status)
	}
	made, err := s.Create("e", "", "")
	if err != nil || made.ID() != "LOCAL-2" || made.File() != "local-2.md" {
		t.Fatalf("Create = %q, %v, want LOCAL-2 in local-2.md", made.ID(), err)
	}
	if b := (Issue{Prefix: "hod", N: 3, Title: "Block Linear writes"}).Branch(); b != "hod-3-block-linear-writes" {
		t.Fatalf("Branch = %q, want hod-3-block-linear-writes in lower case", b)
	}
	if n, _ := (Store{Dir: filepath.Join(s.Dir, "none")}).Highest("hod"); n != 0 {
		t.Fatalf("a missing folder has Highest %d", n)
	}
}
