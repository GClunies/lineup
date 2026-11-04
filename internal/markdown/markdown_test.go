package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderedNotShownRaw(t *testing.T) {
	lines := New(true).Lines("k", "## Plan\n\nShip **the brief** today.\n\n- one\n- two\n\n```go\nfmt.Println(1)\n```", 60)
	out := ansi.Strip(strings.Join(lines, "\n"))
	for _, raw := range []string{"**", "```", "## "} {
		if strings.Contains(out, raw) {
			t.Errorf("raw %q left in:\n%s", raw, out)
		}
	}
	for _, want := range []string{"Plan", "the brief", "one", "fmt.Println(1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if strings.TrimSpace(ansi.Strip(lines[0])) == "" || strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		t.Error("blank lines at the edges should be trimmed")
	}
}

func TestFitsWidthAndCachesPerWidth(t *testing.T) {
	r := New(false)
	text := strings.Repeat("word ", 200)
	for _, l := range r.Lines("k", text, 50) {
		if w := len([]rune(ansi.Strip(l))); w > 50 {
			t.Fatalf("line wide %d > 50: %q", w, ansi.Strip(l))
		}
	}
	if len(r.cache) != 1 {
		t.Fatalf("cache %d", len(r.cache))
	}
	r.Lines("k", text, 50)
	r.Lines("k", text, 70)
	if len(r.cache) != 2 {
		t.Fatalf("same width should hit the cache and a new width should add one: %d entries", len(r.cache))
	}
}

func TestEmptyIsNothing(t *testing.T) {
	if l := New(true).Lines("k", "  \n ", 40); l != nil {
		t.Fatalf("%q", l)
	}
}

func TestEntityEncodedEscapesNeverReachTheScreen(t *testing.T) {
	desc := "Clip &#27;]52;c;ZXZpbA==&#7; and `&#x1b;[2J` and &#155;31m done &#x9d;0;t&#x9c;"
	frame := strings.Join(New(true).Lines("k", desc, 80), "\n")
	if !strings.Contains(ansi.Strip(frame), "done") {
		t.Fatalf("not rendered:\n%s", ansi.Strip(frame))
	}
	for i := 0; i < len(frame); i++ {
		if frame[i] != 0x1b {
			if c := rune(frame[i]); c < 0x20 && c != '\n' {
				t.Fatalf("control byte %#x", c)
			}
			continue
		}
		j := strings.IndexByte(frame[i:], 'm')
		if frame[i+1] != '[' || j < 0 {
			t.Fatalf("non-SGR escape: %q", frame[i:min(len(frame), i+12)])
		}
		i += j
	}
	for _, r := range frame {
		if r >= 0x80 && r <= 0x9f {
			t.Fatalf("C1 control %#x", r)
		}
	}
}

func TestEntityEncodedStylingIsNotApplied(t *testing.T) {
	desc := "before `&#27;[8m`hidden after, and `&#x1b;[5m`blink, and a real &amp; and &#169; stay"
	frame := strings.Join(New(true).Lines("k", desc, 80), "\n")
	for _, sgr := range []string{"\x1b[8m", "\x1b[5m"} {
		if strings.Contains(frame, sgr) {
			t.Fatalf("collaborator-written %q reached the screen", sgr)
		}
	}
	text := ansi.Strip(frame)
	for _, want := range []string{"hidden", "blink", "&", "©"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q lost:\n%s", want, text)
		}
	}
}
