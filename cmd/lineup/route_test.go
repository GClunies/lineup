package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/linear"
	"github.com/gclunies/lineup/internal/route"
	"github.com/gclunies/lineup/internal/scheduler"
)

// fakeIssues holds Linear Issues by identifier and keeps each description
// that lineup writes, as "<issue id>: <description>".
type fakeIssues struct {
	issues map[string]linear.Issue
	reads  int
	writes []string
}

func (f *fakeIssues) issue(_ context.Context, _, identifier string) (linear.Issue, error) {
	f.reads++
	is, ok := f.issues[identifier]
	if !ok {
		return linear.Issue{}, errors.New("no such Issue")
	}
	return is, nil
}

func (f *fakeIssues) setDescription(_ context.Context, _, issueID, description string) error {
	f.writes = append(f.writes, issueID+": "+description)
	for k, is := range f.issues {
		if is.ID == issueID {
			is.Description = description
			f.issues[k] = is
		}
	}
	return nil
}

// sharedTeam has PD on crexi/backend and crexi/data, and ENG on crexi/data only.
type sharedTeam struct {
	p             configPort
	lin           *fakeIssues
	backend, data string
	verdict       string
	judged        int
}

func newSharedTeam(t *testing.T) *sharedTeam {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := &sharedTeam{
		backend: gitCheckout(t, filepath.Join(home, "repos", "crexi", "backend"), "git@github.com:crexi/backend.git"),
		data:    gitCheckout(t, filepath.Join(home, "repos", "crexi", "data"), "git@github.com:crexi/data.git"),
		lin:     &fakeIssues{issues: map[string]linear.Issue{}},
	}
	cfg := loadConfig(t, "roots = [\"~/repos\"]\n"+
		"[repos.\"crexi/backend\"]\nteams = [\"PD\"]\ntracker = \"linear\"\n"+
		"[repos.\"crexi/data\"]\nteams = [\"PD\", \"ENG\"]\ntracker = \"linear\"\n")
	s.p = configPort{cfg: cfg, choices: config.NewRepoChoices(filepath.Join(home, "repos.json")), linear: s.lin,
		judge: func(context.Context, route.Clues, []string) string { s.judged++; return s.verdict }}
	return s
}

// add puts a PD Issue in the fake Linear and returns it as the Scheduler sees it.
func (s *sharedTeam) add(id string, is linear.Issue) scheduler.Issue {
	is.ID, is.Identifier = "uuid-"+id, id
	s.lin.issues[id] = is
	team, _, _ := strings.Cut(id, "-")
	return scheduler.Issue{Kind: scheduler.KindLinear, Workspace: "ws", ID: is.ID, Identifier: id, TeamKey: team}
}

func TestEachRoutingStepSendsAPDIssueToOneRepo(t *testing.T) {
	for _, c := range []struct {
		name      string
		issue     linear.Issue
		verdict   string
		want      string // "backend" or "data"
		writes    bool
		judgments int
	}{
		{"a Repo line", linear.Issue{Description: "Fix it.\n\nRepo: crexi/backend"}, "crexi/data", "backend", false, 0},
		{"a repo label", linear.Issue{Labels: []string{"repo:data"}}, "crexi/backend", "data", false, 0},
		{"a link in the description", linear.Issue{Description: "Breaks https://github.com/crexi/data/actions/runs/1"}, "", "data", true, 0},
		{"an attached PR", linear.Issue{Links: []string{"https://github.com/crexi/backend/pull/9"}}, "", "backend", true, 0},
		{"the model's pick", linear.Issue{Title: "Add a dbt model"}, "crexi/data", "data", true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSharedTeam(t)
			s.verdict = c.verdict
			is := s.add("PD-1", c.issue)
			repo, err := s.p.RepoForIssue(context.Background(), is)
			want := map[string]string{"backend": s.backend, "data": s.data}[c.want]
			if err != nil || repo != want {
				t.Fatalf("repo %q %v, want %s", repo, err, want)
			}
			if s.judged != c.judgments {
				t.Fatalf("the model ran %d times, want %d", s.judged, c.judgments)
			}
			if !c.writes {
				if len(s.lin.writes) != 0 {
					t.Fatalf("wrote %q into an Issue that names its Repo", s.lin.writes)
				}
				return
			}
			if len(s.lin.writes) != 1 || !strings.HasSuffix(s.lin.writes[0], "Repo: crexi/"+c.want) || !strings.HasPrefix(s.lin.writes[0], "uuid-PD-1: ") {
				t.Fatalf("writes %q, want one Repo line for crexi/%s", s.lin.writes, c.want)
			}
			// The next start takes the Repo line and writes nothing.
			s.verdict = ""
			if again, err := s.p.RepoForIssue(context.Background(), is); err != nil || again != want || len(s.lin.writes) != 1 {
				t.Fatalf("second start: repo %q %v, writes %q", again, err, s.lin.writes)
			}
		})
	}
}

func TestAPDIssueThatNothingRoutesAsksAndThePickIsWritten(t *testing.T) {
	s := newSharedTeam(t)
	s.verdict = "unsure"
	is := s.add("PD-2", linear.Issue{Title: "Tidy up", Description: "Make it nicer."})
	_, err := s.p.RepoForIssue(context.Background(), is)
	var ask *scheduler.RepoAsk
	if !errors.As(err, &ask) || ask.Issue != "PD-2" || !slices.Equal(ask.Repos, []string{"crexi/backend", "crexi/data"}) {
		t.Fatalf("err %v, want a RepoAsk with both Repos", err)
	}
	if len(s.lin.writes) != 0 {
		t.Fatalf("an ask wrote %q", s.lin.writes)
	}
	if err := s.p.PickedRepo(context.Background(), is, s.data); err != nil {
		t.Fatal(err)
	}
	if want := []string{"uuid-PD-2: Make it nicer.\n\nRepo: crexi/data"}; !slices.Equal(s.lin.writes, want) {
		t.Fatalf("writes %q, want %q", s.lin.writes, want)
	}
	if repo, err := s.p.RepoForIssue(context.Background(), is); err != nil || repo != s.data {
		t.Fatalf("the next start: repo %q %v, want %s from the Repo line", repo, err, s.data)
	}
	if err := s.p.PickedRepo(context.Background(), is, s.data); err != nil || len(s.lin.writes) != 1 {
		t.Fatalf("a pick that the Issue already names wrote again: %q %v", s.lin.writes, err)
	}
	if err := s.p.PickedRepo(context.Background(), is, s.backend); err != nil {
		t.Fatal(err)
	}
	if got := s.lin.writes[len(s.lin.writes)-1]; got != "uuid-PD-2: Make it nicer.\n\nRepo: crexi/backend" {
		t.Fatalf("another pick wrote %q, want the one Repo line for crexi/backend", got)
	}
}

func TestATeamOnOneRepoReadsNothingFromLinear(t *testing.T) {
	s := newSharedTeam(t)
	is := s.add("ENG-3", linear.Issue{Description: "Repo: crexi/backend"})
	if repo, err := s.p.RepoForIssue(context.Background(), is); err != nil || repo != s.data {
		t.Fatalf("repo %q %v, want %s, the one Repo of ENG", repo, err, s.data)
	}
	if err := s.p.PickedRepo(context.Background(), is, s.backend); err != nil {
		t.Fatal(err)
	}
	if s.lin.reads != 0 || len(s.lin.writes) != 0 || s.judged != 0 {
		t.Fatalf("reads %d, writes %q, judged %d, want none", s.lin.reads, s.lin.writes, s.judged)
	}
}

func TestTheProjectsRepoComesAfterTheRepoLineAndBeforeLinks(t *testing.T) {
	s := newSharedTeam(t)
	if err := s.p.choices.Set("p1", "crexi/backend"); err != nil {
		t.Fatal(err)
	}
	is := s.add("PD-4", linear.Issue{Links: []string{"https://github.com/crexi/data/pull/1"}})
	is.ProjectID = "p1"
	if repo, err := s.p.RepoForIssue(context.Background(), is); err != nil || repo != s.backend || len(s.lin.writes) != 0 {
		t.Fatalf("repo %q %v, writes %q, want %s from the Project and no write", repo, err, s.lin.writes, s.backend)
	}
	named := s.add("PD-5", linear.Issue{Description: "Repo: crexi/data"})
	named.ProjectID = "p1"
	if repo, _ := s.p.RepoForIssue(context.Background(), named); repo != s.data {
		t.Fatalf("repo %q, want %s from the Repo line", repo, s.data)
	}
}

func TestATeamOnNoRepoKeepsTheProjectsRepo(t *testing.T) {
	s := newSharedTeam(t)
	s.p.choices.Set("p1", s.backend)
	is := s.add("OPS-1", linear.Issue{})
	if _, err := s.p.RepoForIssue(context.Background(), is); !errors.Is(err, scheduler.ErrNoRepo) {
		t.Fatalf("err %v, want ErrNoRepo", err)
	}
	is.ProjectID = "p1"
	if repo, err := s.p.RepoForIssue(context.Background(), is); err != nil || repo != s.backend {
		t.Fatalf("repo %q %v, want %s", repo, err, s.backend)
	}
}

// fakeClaude puts a claude on PATH that saves its arguments and its input in
// dir, then prints out.
func fakeClaude(t *testing.T, out string, code int) (dir string) {
	t.Helper()
	dir = t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + filepath.Join(dir, "args") +
		"\ncat > " + filepath.Join(dir, "stdin") + "\nprintf '%s' '" + out + "'\nexit " + string(rune('0'+code)) + "\n"
	os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return dir
}

func TestTheJudgeAsksClaudeWithNoToolsAndOnlyTheCandidates(t *testing.T) {
	s := newSharedTeam(t)
	os.WriteFile(filepath.Join(s.data, "README.md"), []byte("# data\n\nThe dbt project for analytics."), 0o644)
	os.WriteFile(filepath.Join(s.backend, "CLAUDE.md"), []byte("The .NET API."), 0o644)
	dir := fakeClaude(t, `{"type":"result","is_error":false,"structured_output":{"repo":"crexi/data"}}`, 0)
	j := claudeJudge{cfg: s.p.cfg, log: slog.New(slog.DiscardHandler)}
	if got := j.pick(context.Background(), route.Clues{Title: "Add a model", Description: "Daily views."}, []string{"crexi/backend", "crexi/data"}); got != "crexi/data" {
		t.Fatalf("pick = %q", got)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	for _, want := range []string{"-p\n", "--safe-mode\n", "--tools\n\n", "--no-session-persistence\n",
		`"enum":["crexi/backend","crexi/data","unsure"]`} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args lack %q:\n%s", want, args)
		}
	}
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	for _, want := range []string{"Title: Add a model", "Daily views.", "The dbt project for analytics.", "The .NET API."} {
		if !strings.Contains(string(stdin), want) {
			t.Errorf("prompt lacks %q:\n%s", want, stdin)
		}
	}
}

func TestAJudgeThatFailsIsNotSure(t *testing.T) {
	s := newSharedTeam(t)
	j := claudeJudge{cfg: s.p.cfg, log: slog.New(slog.DiscardHandler)}
	for name, run := range map[string]func(){
		"exit 1":   func() { fakeClaude(t, "not logged in", 1) },
		"is_error": func() { fakeClaude(t, `{"is_error":true,"result":"overloaded"}`, 0) },
		"not JSON": func() { fakeClaude(t, "hello", 0) },
	} {
		run()
		if got := j.pick(context.Background(), route.Clues{}, []string{"crexi/backend", "crexi/data"}); got != "" {
			t.Errorf("%s: pick = %q, want \"\"", name, got)
		}
	}
}
