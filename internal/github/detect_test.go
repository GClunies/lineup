package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func repoFacts(t *testing.T, reply string) (RepoFacts, []string) {
	t.Helper()
	bin, log := fakeGH(t, reply, "")
	f, err := (&Client{Bin: bin}).RepoFacts(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	return f, calls(t, log)
}

func TestRepoFactsReadsGitHubInOneQuery(t *testing.T) {
	for _, c := range []struct {
		fixture string
		want    RepoFacts
	}{
		// streamlit/streamlit: classic protection that any reader sees
		// requires ten checks, and Greptile commented on recent PRs.
		{"repo_facts_classic_greptile", RepoFacts{HasIssues: true, DefaultBranch: "develop", ReviewBots: []string{"greptile"},
			RequiredChecks: []string{"check-required-labels", "cli-regression", "enforce-pre-commit", "ensure-relative-imports", "js-unit-tests",
				"playwright-e2e-tests", "py-bare-executions", "py-min-deps-test", "py-unit-tests (max)", "py-unit-tests (min)"}}},
		// Hammerspoon/Hammerspoon2: a ruleset requires two checks, and
		// Greptile commented on recent PRs.
		{"repo_facts_ruleset_greptile", RepoFacts{HasIssues: true, DefaultBranch: "main", ReviewBots: []string{"greptile"},
			RequiredChecks: []string{"Documentation Tests", "Run Tests"}}},
		// sharkdp/fd: people commented, no bot did, and no check is required.
		{"repo_facts_none", RepoFacts{HasIssues: true, DefaultBranch: "master"}},
		// GClunies/lineup as its admin reads it: the classic protection and
		// a ruleset both require test.
		{"repo_facts_admin", RepoFacts{HasIssues: true, DefaultBranch: "main", RequiredChecks: []string{"test"}}},
	} {
		got, log := repoFacts(t, filepath.Join("testdata", c.fixture+".json"))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.fixture, got, c.want)
		}
		if len(log) != 1 || !strings.HasSuffix(log[0], "|-f|o=o|-f|n=r") {
			t.Errorf("%s: gh calls %q, want one query for o/r", c.fixture, log)
		}
	}
}

// Only an admin reads branchProtectionRule. GClunies/lineup's refUpdateRule
// lists no check, so without its ruleset the admin's view alone requires test.
func TestRepoFactsReadsTheAdminsBranchProtection(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "repo_facts_admin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	ref := d["data"].(map[string]any)["repository"].(map[string]any)["defaultBranchRef"].(map[string]any)
	ref["rules"] = map[string]any{"nodes": []any{}}
	out, _ := json.Marshal(d)
	path := filepath.Join(t.TempDir(), "facts.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := repoFacts(t, path); !reflect.DeepEqual(got.RequiredChecks, []string{"test"}) {
		t.Errorf("required %q, want test from branchProtectionRule", got.RequiredChecks)
	}
}

func TestRepoFactsFailsForAMissingRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.json")
	reply := `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository with the name 'o/r'."}]}`
	if err := os.WriteFile(path, []byte(reply), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeGH(t, path, "")
	if _, err := (&Client{Bin: bin}).RepoFacts(context.Background(), "o/r"); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("err = %v, want GitHub's error", err)
	}
}
