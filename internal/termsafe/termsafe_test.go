package termsafe

import "testing"

func TestCleanStripsEscapeSequences(t *testing.T) {
	cases := map[string]string{
		"plain":                             "plain",
		"clip\x1b]52;c;ZXZpbA==\x07board":   "clipboard",
		"title\x1b]0;pwned\x1b\\ok":         "titleok",
		"\x1b[2J\x1b[Hfake prompt":          "fake prompt",
		"red\x1b[31mtext\x1b[0m":            "redtext",
		"c1\u009b31mcsi":                    "c1csi",
		"osc8\x1b]8;;https://evil\x07link":  "osc8link",
		"reset\x1bcdone":                    "resetdone",
		"dcs\x1bPq#0;2;0;0;0\x1b\\after":    "dcsafter",
		"bell\x07 and nul\x00 and del\x7f.": "bell and nul and del.",
		"unterminated\x1b]52;c;AAAA":        "unterminated",
		"trailing esc\x1b":                  "trailing esc",
		"Café ✓ 日本 ◔":                       "Café ✓ 日本 ◔",
	}
	for in, want := range cases {
		if got := Clean(in, false); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanLineBreaks(t *testing.T) {
	if got := Clean("a\nb\tc\r\nd", false); got != "a b c d" {
		t.Errorf("one-line: %q", got)
	}
	if got := Clean("a\nb\tc\r\nd\re", true); got != "a\nb\tc\nde" {
		t.Errorf("prose: %q", got)
	}
}

func TestScreenSafeKeepsStylingOnly(t *testing.T) {
	in := "\x1b[1;38;2;255;0;0mbold red\x1b[0m\n\x1b]52;c;AA\x07x\x1b[2Jy\x1b[?25lz\x07"
	if got := ScreenSafe(in); got != "\x1b[1;38;2;255;0;0mbold red\x1b[0m\nxyz" {
		t.Fatalf("%q", got)
	}
}

func TestSanitizeKeepsBreaksOnlyInDescription(t *testing.T) {
	d := struct {
		Description string
		Title       string
	}{"line one\n\x1b]52;c;AA\x07line two", "High\nx"}
	Sanitize(&d)
	if d.Description != "line one\nline two" || d.Title != "High x" {
		t.Fatalf("%q / %q", d.Description, d.Title)
	}
}
