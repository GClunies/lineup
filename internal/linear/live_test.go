package linear

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveMyIssues reads the user's My Issues from Linear with the first
// signed-in lineup workspace. It writes nothing. Run it with LINEUP_LINEAR_LIVE=1
// after `lineup login`.
func TestLiveMyIssues(t *testing.T) {
	if os.Getenv("LINEUP_LINEAR_LIVE") != "1" {
		t.Skip("set LINEUP_LINEAR_LIVE=1 to read from Linear")
	}
	ws, err := Workspaces()
	if err != nil || len(ws) == 0 {
		t.Fatalf("no signed-in workspace (run lineup login): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	issues, err := NewClient(ws[0]).MyIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d open issues assigned to you", ws[0].URLKey, len(issues))
	for _, is := range issues {
		t.Logf("%-8s %-12s %s %v", is.Identifier, is.State.Name, is.Title, BlockedBy(is))
	}
}
