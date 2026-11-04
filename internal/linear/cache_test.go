package linear

import (
	"os"
	"testing"
)

func TestCacheRoundTripsIssuesByKey(t *testing.T) {
	useStateDir(t, t.TempDir())
	if _, ok := ReadCached("w1/my"); ok {
		t.Fatal("a list never stored reads as stored")
	}
	want := []Issue{{ID: "i1", Identifier: "ENG-1", Title: "t", Blockers: []Ref{{ID: "i0", Identifier: "ENG-0"}}, Project: &Project{ID: "p", Name: "P"}}}
	WriteCache("w1/my", want)
	WriteCache("w1/project/abc", []Issue{{Identifier: "ENG-2"}})
	got, ok := ReadCached("w1/my")
	if !ok || len(got) != 1 || got[0].Blockers[0].Identifier != "ENG-0" || got[0].Project.Name != "P" {
		t.Fatalf("got %+v %v", got, ok)
	}
	if other, _ := ReadCached("w1/project/abc"); len(other) != 1 || other[0].Identifier != "ENG-2" {
		t.Fatalf("keys collided: %+v", other)
	}
}

func TestAnEmptyListThatLoadedIsNotMissing(t *testing.T) {
	useStateDir(t, t.TempDir())
	WriteCache("w1/my", nil)
	if got, ok := ReadCached("w1/my"); !ok || got == nil || len(got) != 0 {
		t.Fatalf("%v %v", got, ok)
	}
}

func TestACorruptCacheReadsAsMissing(t *testing.T) {
	useStateDir(t, t.TempDir())
	WriteCache("w1/my", []Issue{{Identifier: "ENG-1"}})
	if err := os.WriteFile(cachePath("w1/my"), []byte(`[{"id":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadCached("w1/my"); ok {
		t.Fatal("corrupt cache was used")
	}
}

func TestDropCacheTakesOnlyThatWorkspace(t *testing.T) {
	useStateDir(t, t.TempDir())
	WriteCache("w1/my", nil)
	WriteCache("w10/my", nil)
	DropCache("w1")
	if _, ok := ReadCached("w1/my"); ok {
		t.Error("w1 survived")
	}
	if _, ok := ReadCached("w10/my"); !ok {
		t.Error("w10 went with w1")
	}
}
