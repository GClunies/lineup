package board

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/scheduler"
)

// startEnv is mineEnv with ENG-9 selected and the listed skills.
func startEnv(t *testing.T, skills ...string) *workEnv {
	t.Helper()
	e, _ := mineEnv(t)
	e.m.opts.StartSkills = skills
	e.m.sel = "ENG-9"
	return e
}

func lastAdd(t *testing.T, e *workEnv) scheduler.Request {
	t.Helper()
	for _, c := range slices.Backward(e.sched.calls) {
		if c.Op == "add" {
			return c
		}
	}
	t.Fatalf("no add in %v", e.sched.ops())
	return scheduler.Request{}
}

func TestTheSkillPickerListsJustGoFirstAndJustGoSetsNoSkill(t *testing.T) {
	e := startEnv(t, "kit:test-first", "diagnosing-bugs")
	e.press("s")
	if e.m.mode != modeSkill || len(e.sched.calls) != 0 {
		t.Fatalf("mode %d, ops %v, want the skill picker and no op", e.m.mode, e.sched.ops())
	}
	v := e.m.View()
	i, j, k := strings.Index(v, "just go"), strings.Index(v, "kit:test-first"), strings.Index(v, "diagnosing-bugs")
	if i < 0 || i > j || j > k {
		t.Fatalf("picker rows out of order:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "start"}) {
		t.Fatalf("just go ops = %v", got)
	}
	if !strings.Contains(e.m.status, "start ENG-9 ok") || strings.Contains(e.m.status, "·") {
		t.Fatalf("status %q, want start ENG-9 ok with no skill", e.m.status)
	}
}

func TestASkillStartSetsTheSkillBeforeTheStart(t *testing.T) {
	e := startEnv(t, "kit:test-first", "diagnosing-bugs")
	e.press("s", "down", "enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[1]; c.Task != "ENG-9" || c.Key != "skill" || c.Value != "kit:test-first" {
		t.Fatalf("set = %+v", c)
	}
	if !strings.Contains(e.m.status, "start ENG-9 ok · kit:test-first") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestEscInTheSkillPickerCancelsTheStart(t *testing.T) {
	e := startEnv(t, "kit:test-first")
	e.press("s", "down", "esc")
	if e.m.mode != modeNormal || len(e.sched.calls) != 0 {
		t.Fatalf("mode %d, ops %v, want the keys back and no op", e.m.mode, e.sched.ops())
	}
	if !strings.Contains(e.m.status, "start cancelled") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestThePickerOpensWithNoListedSkills(t *testing.T) {
	e := startEnv(t)
	e.press("s")
	if e.m.mode != modeSkill || len(e.sched.calls) != 0 {
		t.Fatalf("mode %d, ops %v", e.m.mode, e.sched.ops())
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "start"}) {
		t.Fatalf("just go ops = %v", got)
	}
}

func TestTypedTextFiltersTheListedSkills(t *testing.T) {
	e := startEnv(t, "kit:test-first", "diagnosing-bugs")
	e.press("s", "test")
	v := e.m.View()
	if strings.Contains(v, "just go") || strings.Contains(v, "diagnosing-bugs") {
		t.Fatalf("typing must hide just go and the rows that do not match:\n%s", v)
	}
	e.press("enter")
	if c := e.sched.calls[1]; c.Key != "skill" || c.Value != "kit:test-first" {
		t.Fatalf("set = %+v", c)
	}
}

func TestASkillThatIsNotListedStartsFromTheTypedText(t *testing.T) {
	e := startEnv(t, "kit:test-first")
	e.press("s", "/kit:plan")
	if v := e.m.View(); !strings.Contains(v, "kit:plan") {
		t.Fatalf("the typed skill is not a row:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[1]; c.Value != "kit:plan" {
		t.Fatalf("set = %+v, want the typed skill without its slash", c)
	}
	if !strings.Contains(e.m.status, "start ENG-9 ok · kit:plan") {
		t.Fatalf("status %q", e.m.status)
	}
}

func TestSkillRows(t *testing.T) {
	listed := []string{"kit:test-first", "diagnosing-bugs"}
	for _, c := range []struct {
		typed string
		want  []string
	}{
		{"", []string{"", "kit:test-first", "diagnosing-bugs"}},
		{"test", []string{"kit:test-first", "test"}},
		{"diagnosing-bugs", []string{"diagnosing-bugs"}},
		{" /tdd ", []string{"tdd"}},
	} {
		if got := skillRows(c.typed, listed); !slices.Equal(got, c.want) {
			t.Errorf("skillRows(%q) = %q, want %q", c.typed, got, c.want)
		}
	}
}

func TestTheRepoPickerFiltersAndRemembersTheRepoOfTheProject(t *testing.T) {
	e := startEnv(t)
	e.sched.errs["add"] = errors.New(noMapping + " for ENG-9: pass a repo")
	e.press("s", "enter")
	if e.m.mode != modeRepo {
		t.Fatalf("mode %d, status %q, want the Repo picker", e.m.mode, e.m.status)
	}
	if v := e.m.View(); !strings.Contains(v, "Which Repo for ENG-9?") || !strings.Contains(v, "acme/data") || !strings.Contains(v, "acme/web") {
		t.Fatalf("picker:\n%s", v)
	}
	e.press("w", "e")
	if vis := rank(e.m.st.repoIn.Value(), repoLabels(e.m.st.repos)); len(vis) != 1 {
		t.Fatalf("fuzzy 'we' should leave web, got %v", vis)
	}
	delete(e.sched.errs, "add")
	e.press("enter")
	if got := e.remem["p1"]; got != "/Users/g/repos/acme/web" {
		t.Fatalf("remembered %v, want web for the Project p1", e.remem)
	}
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := lastAdd(t, e); c.Repo != "/Users/g/repos/acme/web" || c.Issue != "ENG-9" {
		t.Fatalf("second add = %+v", c)
	}
}

func TestTheSkillCarriesThroughTheRepoPicker(t *testing.T) {
	e := startEnv(t, "kit:test-first")
	e.sched.errs["add"] = errors.New(noMapping + " for ENG-9: pass a repo")
	e.press("s", "down", "enter")
	if e.m.mode != modeRepo {
		t.Fatalf("mode %d, status %q, want the Repo picker", e.m.mode, e.m.status)
	}
	delete(e.sched.errs, "add")
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add", "set", "start"}) {
		t.Fatalf("ops = %v", got)
	}
	if c := e.sched.calls[2]; c.Key != "skill" || c.Value != "kit:test-first" {
		t.Fatalf("set = %+v", c)
	}
}

func TestAStartOverTheCapUsesTheCapPrompt(t *testing.T) {
	for _, keys := range [][]string{{"s", "enter"}, {"s", "down", "enter"}} {
		e := startEnv(t, "kit:test-first")
		e.sched.errs["start"] = errors.New(capFull)
		e.press(keys...)
		if e.m.mode != modeCap || e.m.pendingTask != "ENG-9" {
			t.Fatalf("%v: mode=%d pending=%q, want the cap prompt for ENG-9", keys, e.m.mode, e.m.pendingTask)
		}
		delete(e.sched.errs, "start")
		e.press("o")
		if last := e.sched.calls[len(e.sched.calls)-1]; last.Op != "start" || !last.OverCap || last.Task != "ENG-9" {
			t.Fatalf("%v: over-cap start = %+v", keys, last)
		}
	}
}

func TestSearchPutsAnExactWordFirst(t *testing.T) {
	labels := []string{"ENGX  Engineering Experience", "ENG  Engineering", "XENG  xENG (Platform Chapter)"}
	if got := rank("eng", labels); got[0] != 1 {
		t.Fatalf("rank = %v, want ENG first", got)
	}
}
