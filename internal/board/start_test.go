package board

import (
	"errors"
	"maps"
	"reflect"
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
	if got := e.sched.ops(); !slices.Equal(got, []string{"add"}) {
		t.Fatalf("just go ops = %v, want one add", got)
	}
	if set := e.sched.calls[0].Set; set != nil {
		t.Fatalf("just go set %v, want no override", set)
	}
	if !strings.Contains(e.m.status, "start ENG-9 ok") || strings.Contains(e.m.status, "·") {
		t.Fatalf("status %q, want start ENG-9 ok with no skill", e.m.status)
	}
}

func TestASkillStartSendsOneAddWithTheSkill(t *testing.T) {
	e := startEnv(t, "kit:test-first", "diagnosing-bugs")
	e.press("s", "down", "enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add"}) {
		t.Fatalf("ops = %v, want one add", got)
	}
	if c := e.sched.calls[0]; c.Issue != "ENG-9" || !maps.Equal(c.Set, map[string]string{"skill": "kit:test-first"}) {
		t.Fatalf("add = %+v, want ENG-9 with the skill kit:test-first", c)
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
	if got := e.sched.ops(); !slices.Equal(got, []string{"add"}) {
		t.Fatalf("just go ops = %v, want one add", got)
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
	if c := lastAdd(t, e); c.Set["skill"] != "kit:test-first" {
		t.Fatalf("add = %+v, want the skill kit:test-first", c)
	}
}

func TestASkillThatIsNotListedStartsFromTheTypedText(t *testing.T) {
	e := startEnv(t, "kit:test-first")
	e.press("s", "/kit:plan")
	if v := e.m.View(); !strings.Contains(v, "kit:plan") {
		t.Fatalf("the typed skill is not a row:\n%s", v)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add"}) {
		t.Fatalf("ops = %v, want one add", got)
	}
	if c := e.sched.calls[0]; c.Set["skill"] != "kit:plan" {
		t.Fatalf("add = %+v, want the typed skill without its slash", c)
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
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add"}) {
		t.Fatalf("ops = %v, want the add, then the add with the Repo", got)
	}
	if c := lastAdd(t, e); c.Repo != "/Users/g/repos/acme/web" || c.Issue != "ENG-9" {
		t.Fatalf("second add = %+v", c)
	}
}

// The Repo picker asks for the Repos of the row's Kind, so it offers no Repo
// that is locked to other Trackers.
func TestTheRepoPickerAsksForTheReposOfTheRowsKind(t *testing.T) {
	e := startEnv(t)
	e.sched.errs["add"] = errors.New(noMapping + " for ENG-9: pass a repo")
	e.press("s", "enter")
	if e.m.mode != modeRepo {
		t.Fatalf("mode %d, status %q, want the Repo picker", e.m.mode, e.m.status)
	}
	if !slices.Equal(e.reposKinds, []string{scheduler.KindLinear}) {
		t.Fatalf("Repos kinds = %q, want linear", e.reposKinds)
	}
}

func TestTheSkillCarriesThroughTheRepoPicker(t *testing.T) {
	e := startEnv(t, "kit:test-first")
	e.sched.errs["add"] = errors.New(noMapping + " for ENG-9: pass a repo")
	e.press("s", "down", "enter")
	if e.m.mode != modeRepo {
		t.Fatalf("mode %d, status %q, want the Repo picker", e.m.mode, e.m.status)
	}
	e.press("enter")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add"}) {
		t.Fatalf("ops = %v, want the add, then the add with the Repo", got)
	}
	if c := lastAdd(t, e); c.Repo != "/Users/g/repos/acme/data" || c.Set["skill"] != "kit:test-first" {
		t.Fatalf("add = %+v, want the picked Repo and the skill", c)
	}
}

// fullCapStart starts ENG-9 with the keys while the WIP cap is full, so the
// add fails and the cap prompt opens. It returns the add that failed.
func fullCapStart(t *testing.T, keys ...string) (*workEnv, scheduler.Request) {
	t.Helper()
	e := startEnv(t, "kit:test-first")
	e.sched.errs["add"] = errors.New(capFull)
	e.press(keys...)
	if !strings.Contains(e.m.footer(), "wip cap full: start once over the cap (o) or raise the cap to 6 (c)?") {
		t.Fatalf("%v: footer %q, want the cap prompt", keys, e.m.footer())
	}
	return e, lastAdd(t, e)
}

func TestTheCapPromptSendsTheAddAgainOverTheCap(t *testing.T) {
	for _, keys := range [][]string{{"s", "enter"}, {"s", "down", "enter"}} {
		e, failed := fullCapStart(t, keys...)
		e.press("o")
		want := failed
		want.OverCap = true
		if got := e.sched.ops(); !slices.Equal(got, []string{"add", "add"}) {
			t.Fatalf("%v: ops = %v, want the add, then the add over the cap", keys, got)
		}
		if last := lastAdd(t, e); !reflect.DeepEqual(last, want) {
			t.Fatalf("%v: second add = %+v, want %+v", keys, last, want)
		}
	}
}

func TestRaisingTheCapSendsTheAddAgain(t *testing.T) {
	e, failed := fullCapStart(t, "s", "down", "enter")
	e.press("c")
	if got := e.sched.ops(); !slices.Equal(got, []string{"add", "cap", "add"}) {
		t.Fatalf("ops = %v, want the add, the cap, then the add again", got)
	}
	if c := e.sched.calls[1]; c.N != 6 {
		t.Fatalf("cap = %+v, want 6", c)
	}
	if last := lastAdd(t, e); !reflect.DeepEqual(last, failed) {
		t.Fatalf("second add = %+v, want %+v", last, failed)
	}
}

func TestAnyOtherKeyCancelsTheCapPrompt(t *testing.T) {
	e, _ := fullCapStart(t, "s", "enter")
	e.press("esc")
	if e.m.mode != modeNormal || !slices.Equal(e.sched.ops(), []string{"add"}) {
		t.Fatalf("mode %d, ops %v, want the keys back and no other op", e.m.mode, e.sched.ops())
	}
}

func TestSearchPutsAnExactWordFirst(t *testing.T) {
	labels := []string{"ENGX  Engineering Experience", "ENG  Engineering", "XENG  xENG (Platform Chapter)"}
	if got := rank("eng", labels); got[0] != 1 {
		t.Fatalf("rank = %v, want ENG first", got)
	}
}
