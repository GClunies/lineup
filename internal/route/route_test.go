package route

import (
	"context"
	"errors"
	"slices"
	"testing"
)

var both = []string{"crexi/backend", "crexi/data"}

// choose runs Choose on the clues, with a judge that answers verdict and
// counts its calls.
func choose(t *testing.T, cands []string, project string, c Clues, verdict string) (string, Step, int) {
	t.Helper()
	asked := 0
	key, step, err := Choose(context.Background(), Inputs{
		Candidates: cands, Project: project,
		Clues: func(context.Context) (Clues, error) { return c, nil },
		Judge: func(_ context.Context, got Clues, cs []string) string {
			asked++
			if got.Title != c.Title || !slices.Equal(cs, cands) {
				t.Errorf("the judge got %+v %v", got, cs)
			}
			return verdict
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return key, step, asked
}

func TestEachStepPicksTheRepo(t *testing.T) {
	for _, c := range []struct {
		name    string
		cands   []string
		project string
		clues   Clues
		verdict string
		key     string
		step    Step
	}{
		{"one candidate", []string{"crexi/data"}, "", Clues{}, "crexi/backend", "crexi/data", One},
		{"a Repo line", both, "", Clues{Description: "Fix the load.\n\nRepo: crexi/data\n"}, "", "crexi/data", Named},
		{"a Repo line in backticks and another case", both, "", Clues{Description: "repo: `Crexi/Data`"}, "", "crexi/data", Named},
		{"a repo label by name", both, "", Clues{Labels: []string{"bug", "repo:data"}}, "", "crexi/data", Named},
		{"a repo label by key", both, "", Clues{Labels: []string{"Repo: crexi/backend"}}, "", "crexi/backend", Named},
		{"the Project's Repo", both, "crexi/backend", Clues{}, "", "crexi/backend", Project},
		{"the name beats the Project", both, "crexi/backend", Clues{Description: "Repo: crexi/data"}, "", "crexi/data", Named},
		{"a link in the description", both, "", Clues{Description: "See https://github.com/crexi/data/blob/main/models/x.sql"}, "", "crexi/data", Linked},
		{"an attached PR", both, "", Clues{Links: []string{"https://github.com/crexi/backend/pull/12"}}, "", "crexi/backend", Linked},
		{"a link to a Repo that is no candidate", both, "", Clues{Description: "https://github.com/crexi/web/pull/3"}, "crexi/data", "crexi/data", Content},
		{"the model's pick", both, "", Clues{Title: "Add a dbt model"}, "crexi/data", "crexi/data", Content},
		{"the model is not sure", both, "", Clues{Title: "Tidy up"}, "unsure", "", ""},
		{"the model names no candidate", both, "", Clues{Title: "Tidy up"}, "crexi/web", "", ""},
		{"the model fails", both, "", Clues{Title: "Tidy up"}, "", "", ""},
		{"links to two candidates", both, "", Clues{Description: "https://github.com/crexi/data/pull/1 https://github.com/crexi/backend/pull/2"}, "", "", ""},
		{"Repo lines that name two candidates", both, "", Clues{Description: "Repo: crexi/data\nRepo: crexi/backend"}, "crexi/data", "crexi/data", Content},
	} {
		t.Run(c.name, func(t *testing.T) {
			key, step, _ := choose(t, c.cands, c.project, c.clues, c.verdict)
			if key != c.key || step != c.step {
				t.Fatalf("Choose = %q %q, want %q %q", key, step, c.key, c.step)
			}
		})
	}
}

func TestOnlyTheModelStepRunsTheModel(t *testing.T) {
	if _, _, asked := choose(t, both, "", Clues{Description: "Repo: crexi/data"}, "crexi/backend"); asked != 0 {
		t.Fatalf("a named Repo ran the model %d times", asked)
	}
	if _, _, asked := choose(t, both, "", Clues{}, "unsure"); asked != 1 {
		t.Fatalf("an Issue with no clue ran the model %d times, want 1", asked)
	}
}

func TestOneCandidateReadsNoClues(t *testing.T) {
	key, step, err := Choose(context.Background(), Inputs{
		Candidates: []string{"crexi/data"},
		Clues:      func(context.Context) (Clues, error) { return Clues{}, errors.New("Linear is down") },
	})
	if err != nil || key != "crexi/data" || step != One {
		t.Fatalf("Choose = %q %q %v", key, step, err)
	}
}

func TestChooseReportsAFailedRead(t *testing.T) {
	_, _, err := Choose(context.Background(), Inputs{
		Candidates: both,
		Clues:      func(context.Context) (Clues, error) { return Clues{}, errors.New("Linear is down") },
	})
	if err == nil {
		t.Fatal("a failed read picked a Repo")
	}
}

func TestOnlyTheLinkAndModelStepsWriteTheRepoLine(t *testing.T) {
	for step, want := range map[Step]bool{One: false, Named: false, Project: false, Linked: true, Content: true} {
		if step.Writes() != want {
			t.Errorf("%q.Writes() = %v, want %v", step, !want, want)
		}
	}
}

func TestWithRepoLineLeavesOneRepoLineThatNamedRepoReads(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "Repo: crexi/data"},
		{"Fix the load.\n", "Fix the load.\n\nRepo: crexi/data"},
		{"Fix the load.\n\nRepo: crexi/backend\n", "Fix the load.\n\nRepo: crexi/data"},
	} {
		got := WithRepoLine(c.in, "crexi/data")
		if got != c.want {
			t.Errorf("WithRepoLine(%q) = %q, want %q", c.in, got, c.want)
		}
		if k := NamedRepo(Clues{Description: got}, both); k != "crexi/data" {
			t.Errorf("NamedRepo(%q) = %q", got, k)
		}
	}
}
