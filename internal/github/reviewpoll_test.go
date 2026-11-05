package github

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParsePRRef(t *testing.T) {
	want := PRRef{"umbrella/data", 4955}
	for _, s := range []string{
		"https://github.com/umbrella/data/pull/4955",
		"https://github.com/umbrella/data/pull/4955/files",
		"github.com/umbrella/data/pull/4955#discussion_r1",
		" umbrella/data#4955 ",
	} {
		if got, err := ParsePRRef(s); err != nil || got != want {
			t.Errorf("ParsePRRef(%q) = %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"https://github.com/umbrella/data/issues/4955", "data#4955", "umbrella/data#0", "https://gitlab.com/a/b/pull/1"} {
		if _, err := ParsePRRef(s); err == nil {
			t.Errorf("ParsePRRef(%q) took a URL that names no PR", s)
		}
	}
}

func reviewed(t *testing.T, fixture string, refs ...PRRef) (map[PRRef]*ReviewedPR, []string) {
	t.Helper()
	bin, log := fakeGH(t, filepath.Join("testdata", fixture+".json"), "")
	prs, err := (&Client{Bin: bin}).ReviewedPRs(context.Background(), "octocat", refs)
	if err != nil {
		t.Fatal(err)
	}
	return prs, calls(t, log)
}

func TestReviewedPRsReadsEachPRByNumber(t *testing.T) {
	open, merged, closed, today := PRRef{"umbrella/data", 4916}, PRRef{"umbrella/data", 4915}, PRRef{"umbrella/data", 4911}, PRRef{"umbrella/data", 4959}
	prs, log := reviewed(t, "review_poll", open, merged, closed, today)
	if len(log) != 1 {
		t.Fatalf("made %d gh calls, want 1", len(log))
	}
	for _, arg := range []string{"viewer=octocat", "o0=umbrella", "n0=data", "p0=4916", "p3=4959", "-F"} {
		if !slices.Contains(strings.Split(log[0], "|"), arg) {
			t.Errorf("call lacks %q: %s", arg, log[0])
		}
	}
	if strings.Contains(log[0], "headRefName") {
		t.Errorf("the review poll reads a branch: %s", log[0])
	}
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	cases := []struct {
		ref      PRRef
		state    string
		head     string
		reviewed time.Time
	}{
		{open, "OPEN", "77ca03ed2a555181b6861959fcbca729eaffd6f3", at("2026-11-04T07:52:04Z")},
		{merged, "MERGED", "b36ff439428277464e0853ad6ded11bdf5aef87c", at("2026-11-04T18:55:47Z")},
		{closed, "CLOSED", "96b2711b7852507a577ab018f3175c6ca0f4967d", time.Time{}},
		{today, "OPEN", "994cbc7df96e1c3cab2e144d8952064d6a530e50", at("2026-11-05T19:53:37Z")},
	}
	for _, c := range cases {
		pr := prs[c.ref]
		if pr == nil {
			t.Fatalf("%s: missing", c.ref)
		}
		if pr.State != c.state || pr.Head != c.head || !pr.ReviewedAt.Equal(c.reviewed) || pr.Base != "develop" || pr.Fork || pr.Repo != "umbrella/data" {
			t.Errorf("%s = %+v", c.ref, *pr)
		}
	}
	if prs[open].Title != "CORE-34: Replace the legacy sale flag with a category column" || prs[open].URL != "https://github.com/umbrella/data/pull/4916" {
		t.Errorf("title or URL: %+v", *prs[open])
	}
}

func TestAPendingReviewIsNotAReview(t *testing.T) {
	ref := PRRef{"umbrella/data", 4916}
	prs, _ := reviewed(t, "review_poll_pending", ref)
	if pr := prs[ref]; pr == nil || !pr.ReviewedAt.IsZero() {
		t.Fatalf("a pending review counted: %+v", pr)
	}
}

// gh exits with an error when one PR is missing and prints the others.
func TestAMissingPRLeavesTheOthers(t *testing.T) {
	dir := t.TempDir()
	partial := filepath.Join(dir, "partial.json")
	os.WriteFile(partial, []byte(`{"data":{"r0":{"pullRequest":null},"r1":{"pullRequest":{"number":4,"state":"OPEN","repository":{"nameWithOwner":"o/r"},"reviews":{"nodes":[]}}}},
"errors":[{"type":"NOT_FOUND","path":["r0","pullRequest"],"message":"Could not resolve to a PullRequest with the number of 3."}]}`), 0o644)
	bin := filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\ncat "+partial+"\nexit 1\n"), 0o755)
	prs, err := (&Client{Bin: bin}).ReviewedPRs(context.Background(), "me", []PRRef{{"o/r", 3}, {"o/r", 4}})
	if err != nil || prs[PRRef{"o/r", 3}] != nil || prs[PRRef{"o/r", 4}] == nil {
		t.Fatalf("prs %v err %v, want #4 only", prs, err)
	}
	if _, err := (&Client{Bin: bin}).ReviewedPRs(context.Background(), "me", []PRRef{{"o/r", 3}}); err == nil || !strings.Contains(err.Error(), "number of 3") {
		t.Fatalf("a lone missing PR: err %v, want GitHub's message", err)
	}
}
