package main

import (
	"bytes"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

func TestStartNamesTheTaskByItsCanonicalID(t *testing.T) {
	for typed, want := range map[string]string{"eng-614": "ENG-614 started\n", "ah-3": "AH-3 started\n", "acme/data#12": "acme/data#12 started\n"} {
		c, _ := fakeScheduler(t, map[string]scheduler.Response{"start": {OK: true}})
		var out bytes.Buffer
		if err := cmdStart(c, []string{typed}, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("start %s printed %q, want %q", typed, out.String(), want)
		}
	}
}
