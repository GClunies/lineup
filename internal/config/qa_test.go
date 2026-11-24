package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gclunies/lineup/internal/lifecycle"
)

// QA is off by default, and a Repo waits 48h for a verdict (PROD-QA-001,
// PROD-QA-008). Each key of [qa] layers on its own (PROD-CONFIG-002).
func TestQALayersFromTheDefaultsToTheRepo(t *testing.T) {
	g, home := loadFixture(t, `
roots = ["~/repos"]

[qa]
slots = 2

[defaults.qa]
max_wait = "36h"

[repos."crexi/*".qa]
enabled = true

[repos."crexi/web".qa]
enabled = false
max_wait = "90m"
`)
	for rel, want := range map[string]QA{
		"repos/crexi/data":      {Enabled: true, MaxWait: 36 * time.Hour},
		"repos/crexi/web":       {MaxWait: 90 * time.Minute},
		"repos/gclunies/lineup": {MaxWait: 36 * time.Hour},
	} {
		r, err := g.Resolve(filepath.Join(home, rel), nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.QA != want {
			t.Errorf("%s: qa = %+v, want %+v", rel, r.QA, want)
		}
	}
	if g.QASlots() != 2 {
		t.Errorf("slots = %d, want 2", g.QASlots())
	}
	d := load(t, "")
	r, err := d.Resolve(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.QA != (QA{MaxWait: 48 * time.Hour}) || d.QASlots() != 0 {
		t.Errorf("defaults: qa = %+v, slots %d, want off, 48h, and no cap", r.QA, d.QASlots())
	}
}

// Each [qa] key has one level, and a key at the other level names the right
// place (PROD-CONFIG-003, PROD-CONFIG-005).
func TestAQAKeyAtTheWrongLevelStopsTheLoad(t *testing.T) {
	for text, want := range map[string]string{
		"[qa]\nenabled = true\n":                  "qa.enabled belongs in the [qa] table of [defaults] or of a Repo",
		"[qa]\nmax_wait = \"2h\"\n":               "qa.max_wait belongs in the [qa] table of [defaults] or of a Repo",
		"[defaults.qa]\nslots = 2\n":              "defaults: qa.slots belongs in the top-level [qa] table",
		"[repos.\"o/r\".qa]\nslots = 2\n":         `repos."o/r": qa.slots belongs in the top-level [qa] table`,
		"[qa]\nslots = 0\n":                       "qa.slots: slots must be at least 1",
		"[defaults.qa]\nmax_wait = \"0s\"\n":      `defaults: qa.max_wait "0s" must be a duration above 0, such as 48h`,
		"[defaults.qa]\nmax_wait = \"-1h\"\n":     `qa.max_wait "-1h" must be a duration above 0`,
		"[repos.\"o/r\".qa]\nmax_wait = \"2d\"\n": `repos."o/r": qa.max_wait "2d" must be a duration above 0`,
		"[defaults.qa]\nenable = true\n":          "unknown keys: defaults.qa.enable",
	} {
		if err := loadErr(t, text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
}

// A Linear team and the GitHub labels can map the QA pass (PROD-TRACK-011,
// PROD-TRACK-012).
func TestTheTrackerMapsTakeTheQAPass(t *testing.T) {
	g := load(t, "[github.labels]\nqa_passed = \"verified\"\n[linear.crexi.status.PD]\nqa_passed = \"Done\"\n")
	if got := g.GitHubLabels()["qa_passed"]; got != "verified" {
		t.Errorf("label = %q, want verified", got)
	}
	if got, ok := g.LinearStatus("crexi", "PD", "qa_passed"); !ok || got != "Done" {
		t.Errorf("status = %q %v, want Done", got, ok)
	}
}

// QA- starts each QA Task ID, so no Repo prefix can be QA or start with QA-
// (PROD-TRACK-015).
func TestAPrefixCannotMakeAQATaskID(t *testing.T) {
	hod := "[repos.\"GClunies/hod\"]\n"
	for body, want := range map[string]string{
		hod + `prefix = "qa"`:     `prefix "QA" is reserved: lineup starts its own IDs with LOCAL, AH, PR, QA`,
		hod + `prefix = "Qa-eng"`: `prefix "QA-ENG" starts with QA-, which starts the ID of each QA Task`,
	} {
		if err := loadErr(t, body+"\n"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", body, err, want)
		}
	}
	load(t, hod+`prefix = "qahod"`+"\n")
}

// A QA Worker starts in Verifying, so [stage.verifying] sets its model and
// effort (PROD-WORKER-002).
func TestTheVerifyingStageSetsTheQAWorkersModel(t *testing.T) {
	g := load(t, "[defaults.stage.default]\nmodel = \"m\"\n[defaults.stage.verifying]\nmodel = \"v\"\neffort = \"max\"\nskill = \"qa-check\"\n")
	r, err := g.Resolve(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Stages[lifecycle.Verifying]; got != (StageConfig{Model: "v", Effort: "max", Skill: "qa-check"}) {
		t.Errorf("verifying = %+v", got)
	}
	if got := r.Stages[lifecycle.Working]; got.Model != "m" || got.Skill != "" {
		t.Errorf("[stage.verifying] reached working: %+v", got)
	}
}
