package scheduler

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTabLabel(t *testing.T) {
	review := &ReviewPR{Repo: "acme/data", Number: 1}
	tests := []struct {
		name string
		rec  Record
		want string
	}{
		{"a Linear title keeps its own prefix",
			Record{ID: "PD-688", Title: "DE - Collapse sale transactions within 30 days into one headline sale in the property record stream"},
			"PD-688 DE - Collapse sale…"},
		{"a build Task cuts at a word",
			Record{ID: "PD-645", Title: "Drop the kyp_v3 Snowflake tables and views with a one-time script"},
			"PD-645 Drop the kyp_v3…"},
		{"a title that fits stays whole",
			Record{ID: "ENG-1", Title: "Add billing model"},
			"ENG-1 Add billing model"},
		{"a Task with no title is its ID",
			Record{ID: "ENG-2"},
			"ENG-2"},
		{"an ad hoc Task",
			Record{ID: "ah-49", Title: "compare the findings of https://claude.ai/artifact/YWPhJttg1"},
			"ah-49 compare the findings of…"},
		{"a first word that does not fit is cut hard",
			Record{ID: "ah-7", Title: strings.Repeat("x", 50)},
			"ah-7 " + strings.Repeat("x", 26) + "…"},
		{"a review loses the repo prefix and a commit type",
			Record{ID: "pr-3553", Review: review, Title: "data#3553 docs(adr): order cross-pipeline deployments and gate catalog parity (ADR-0036, revived on develop)"},
			"pr-3553 order cross-pipeline…"},
		{"a review loses the repo prefix and an issue key",
			Record{ID: "pr-5132", Review: review, Title: "data#5132 PD-376: Build core.dim_land, the land of each property"},
			"pr-5132 Build core.dim_land…"},
		{"a review with a short title",
			Record{ID: "pr-5166", Review: review, Title: "data#5166 PD-712: Fill the empty US zoning tiles in dev"},
			"pr-5166 Fill the empty US…"},
		{"a cut that lands on a space drops the space",
			Record{ID: "pr-5184", Review: review, Title: "data#5184 PD-324: Start stress run 1 and reuse topic 3 between runs"},
			"pr-5184 Start stress run 1 and…"},
		{"a review loses a long issue key",
			Record{ID: "pr-5160", Review: review, Title: "data#5160 DATA-2793: Sync Has Integrations License to HubSpot contacts"},
			"pr-5160 Sync Has Integrations…"},
		{"a review loses a breaking commit type",
			Record{ID: "pr-9-web", Review: review, Title: "web#9 feat!: Add dark mode"},
			"pr-9-web Add dark mode"},
		{"a review title with no prefix stays whole",
			Record{ID: "pr-9", Review: review, Title: "web#9 Fix: tooltip"},
			"pr-9 Fix: tooltip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rec.tabLabel()
			if got != tt.want {
				t.Errorf("tabLabel() = %q, want %q", got, tt.want)
			}
			if n := utf8.RuneCountInString(got); n > 32 {
				t.Errorf("tabLabel() = %q has %d characters, want at most 32", got, n)
			}
			if !strings.HasPrefix(got, tt.rec.ID) {
				t.Errorf("tabLabel() = %q does not start with the ID %q", got, tt.rec.ID)
			}
		})
	}
}

func TestPlainTitle(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Start a sessions to explore the ideas and UI here:\n- https:/", "Start a sessions to explore the ideas and UI here"},
		{"start a task to address all comments here: https://github.co", "start a task to address all comments here"},
		{"compare the findings of https://claude.ai/artifact/YWPhJttg1", "compare the findings of"},
		{"grant access (DSC-1057)", "grant access (DSC-1057)"},
		{"see www.example.com for the steps", "see for the steps"},
		{"https://x.com/abc\n\nFix the login retry", "Fix the login retry"},
		{"- https://x.com/abc\nFix the login retry", "Fix the login retry"},
		{"Fix   the\tlogin  retry -", "Fix the login retry"},
		{"End the sentence.", "End the sentence."},
		{"Open the docs (", "Open the docs"},
		{"https://x.com/abc", "https://x.com/abc"},
		{"", ""},
	} {
		if got := PlainTitle(c.in); got != c.want {
			t.Errorf("PlainTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
