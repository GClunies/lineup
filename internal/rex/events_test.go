package rex

import (
	"os"
	"testing"
)

func TestParseEvent(t *testing.T) {
	const s = "session:01a17f40-dc99-7c3c-8508-ce72793c3375"
	cases := []struct {
		file string
		ok   bool
		want Event
	}{
		{"child_exited.json", true, Event{Kind: ChildExited, SessionID: s, BlockID: "block:01a17f40-dceb-752c-99b3-0395665b4240", ExitCode: 3}},
		{"block_closed.json", true, Event{Kind: BlockClosed, SessionID: s, BlockID: "block:01a17f40-dceb-752c-99b3-0395665b4240"}},
		{"window_closed.json", true, Event{Kind: WindowClosed, SessionID: s, WindowID: "window:01a17f40-dd00-7887-9ea8-6871674bfb8d"}},
		{"session_destroyed.json", true, Event{Kind: SessionDestroyed, SessionID: s}},
		{"status_working.json", true, Event{
			Kind: ProgramStatusChanged, SessionID: "session:01a17f43-7147-7849-8b40-4d4c1dfea5e4", BlockID: "block:01a17f43-71b1-7af8-a514-96a53a516e97",
			Status: StatusRecord{State: "working", App: "claude-code", Msg: "Creating empty file named x in working directory"}}},
		{"status_done.json", true, Event{
			Kind: ProgramStatusChanged, SessionID: "session:01a17f43-7147-7849-8b40-4d4c1dfea5e4", BlockID: "block:01a17f43-71b1-7af8-a514-96a53a516e97",
			Status: StatusRecord{State: "done", App: "claude-code", Msg: "ran touch x; completed with no output or error"}}},
		{"status_blocked.json", true, Event{
			Kind: ProgramStatusChanged, SessionID: "session:01a17f44-7e48-7dc3-aedc-1e950839e735", BlockID: "block:01a17f44-7ea9-70da-a919-154b7a93b63c",
			Status: StatusRecord{State: "blocked", Kind: "permission", App: "claude-code", Msg: "approve Bash: touch x"}}},
		{"ignored_process_changed.json", false, Event{}},
		{"ignored_session_created.json", false, Event{}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			line, err := os.ReadFile("testdata/" + c.file)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := ParseEvent(line)
			if ok != c.ok || got != c.want {
				t.Fatalf("got %+v ok=%v, want %+v ok=%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParseEventRejectsGarbage(t *testing.T) {
	for _, l := range []string{"", "not json", "{}", `{"type":"session_event"}`} {
		if ev, ok := ParseEvent([]byte(l)); ok {
			t.Fatalf("%q parsed as %+v", l, ev)
		}
	}
}
