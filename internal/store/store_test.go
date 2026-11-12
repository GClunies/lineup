package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTasksAndEventsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	if err := s.Save("ENG-1", []byte(`{"v":1}`), []Event{{TaskID: "ENG-1", At: at, Signal: "start", From: "Queued", To: "Working"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("ENG-1", []byte(`{"v":2}`), []Event{{TaskID: "ENG-1", At: at, Signal: "done", From: "Working", To: "CI running"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("wip_cap", "6"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tasks, err := s.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || string(tasks["ENG-1"]) != `{"v":2}` {
		t.Fatalf("tasks = %q, want ENG-1 at v2 only", tasks)
	}
	events, err := s.Events("ENG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Signal != "start" || events[1].To != "CI running" || !events[0].At.Equal(at) {
		t.Fatalf("events = %+v", events)
	}
	if v, _ := s.Setting("wip_cap"); v != "6" {
		t.Fatalf("wip_cap = %q", v)
	}
	if v, err := s.Setting("missing"); v != "" || err != nil {
		t.Fatalf("missing setting = %q, %v", v, err)
	}
}

func TestSkipsSurviveReopenAndIgnoreCase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 9, 17, 0, 0, 0, time.UTC)
	const url = "https://github.com/Umbrella/Data/pull/7"
	if added, err := s.AddSkip(Skip{URL: url, Title: "first", At: at}); err != nil || !added {
		t.Fatalf("add = %v, %v", added, err)
	}
	if added, err := s.AddSkip(Skip{URL: strings.ToLower(url), Title: "second", At: at.Add(time.Hour)}); err != nil || added {
		t.Fatalf("a repeat in another case: added = %v, %v, want false", added, err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Skips()
	if err != nil || len(got) != 1 || got[0].Title != "first" || got[0].URL != url || !got[0].At.Equal(at) {
		t.Fatalf("skips = %+v, %v, want the first write, in its own case", got, err)
	}
	if removed, err := s.RemoveSkip(strings.ToLower(url)); err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if removed, err := s.RemoveSkip(url); err != nil || removed {
		t.Fatalf("a second remove: removed = %v, %v, want false", removed, err)
	}
	if got, _ := s.Skips(); len(got) != 0 {
		t.Fatalf("skips = %+v after the remove", got)
	}
}
