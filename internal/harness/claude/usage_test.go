package claude

import (
	"testing"
	"time"
)

// usageProfile holds the transcripts c1 and c2 in its projects folder.
const usageProfile = "testdata/usage"

func readUsage(t *testing.T, id string) Usage {
	t.Helper()
	u, err := ReadUsage(usageProfile, id)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil {
		t.Fatalf("usage of %s is unknown", id)
	}
	return *u
}

func tokensOf(u Usage) [4]int64 { return [4]int64{u.Input, u.Output, u.CacheWrite, u.CacheRead} }

// c1 writes msg_1 in two entries, one for each content block. Its subagent
// writes msg_s1. Its last line is half written.
func TestReadUsageCountsEachMessageOnceAndAddsTheSubagents(t *testing.T) {
	want := [4]int64{10 + 20 + 3, 100 + 200 + 30, 1000 + 2000 + 300, 10000 + 20000 + 3000}
	if got := tokensOf(readUsage(t, "c1")); got != want {
		t.Errorf("input, output, cache write, cache read = %v, want %v", got, want)
	}
}

// c1's turn 1 takes 60 s, and the user sends prompt 2 30 minutes after it
// ends. Turn 2 takes 90 s.
func TestReadUsageLeavesTheWaitForTheUserOutOfTheAgentTime(t *testing.T) {
	if got := readUsage(t, "c1").AgentTime; got != 150*time.Second {
		t.Errorf("agent time = %s, want 2m30s", got)
	}
}

// A resume from another folder wrote c2's first turn into a second file,
// then went on there with a second turn.
func TestReadUsageCountsTheCopyOfAResumeOnce(t *testing.T) {
	u := readUsage(t, "c2")
	if got, want := tokensOf(u), [4]int64{1 + 2, 10 + 20, 100 + 200, 1000 + 2000}; got != want {
		t.Errorf("input, output, cache write, cache read = %v, want %v", got, want)
	}
	if u.AgentTime != 90*time.Second {
		t.Errorf("agent time = %s, want 1m30s", u.AgentTime)
	}
}

func TestReadUsageOfAMissingConversationIsUnknown(t *testing.T) {
	u, err := ReadUsage(usageProfile, "gone")
	if u != nil || err != nil {
		t.Fatalf("usage %+v, err %v, want nil and no error", u, err)
	}
}
