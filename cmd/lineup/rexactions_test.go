package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRexActionsWritesTheLineupPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rex")
	reloads := 0
	reload := func() error { reloads++; return nil }
	read := func() string {
		data, err := os.ReadFile(filepath.Join(dir, "lineup.lua"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	if err := installRexActions(dir, "/Users/me/go bin/lineup", reload); err != nil {
		t.Fatal(err)
	}
	src := read()
	if !strings.Contains(src, `local LINEUP = "/Users/me/go bin/lineup"`) || strings.Contains(src, "{{lineup}}") {
		t.Fatalf("lineup.lua does not run /Users/me/go bin/lineup:\n%s", src)
	}
	if reloads != 1 {
		t.Fatalf("a new file reloaded Rex %d times, want 1", reloads)
	}

	if err := installRexActions(dir, "/Users/me/go bin/lineup", reload); err != nil {
		t.Fatal(err)
	}
	if reloads != 1 {
		t.Fatal("an unchanged file reloaded Rex again")
	}

	if err := installRexActions(dir, "/opt/lineup", reload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(), `local LINEUP = "/opt/lineup"`) || reloads != 2 {
		t.Fatalf("a new path did not replace the file and reload Rex (%d reloads)", reloads)
	}
}

func TestRexConfigDirFollowsRex(t *testing.T) {
	t.Setenv("HOME", "/Users/me")
	for _, c := range []struct{ rexConfig, xdg, want string }{
		{"", "", "/Users/me/.config/rex"},
		{"", "/xdg", "/xdg/rex"},
		{"/dotfiles/rex/init.lua", "/xdg", "/dotfiles/rex"},
	} {
		t.Setenv("REX_CONFIG", c.rexConfig)
		t.Setenv("XDG_CONFIG_HOME", c.xdg)
		if got := rexConfigDir(); got != c.want {
			t.Errorf("REX_CONFIG=%q XDG_CONFIG_HOME=%q: %s, want %s", c.rexConfig, c.xdg, got, c.want)
		}
	}
}

func TestLuaStringEscapesForLua51(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/lineup": `"/usr/local/bin/lineup"`,
		`/a "b"\c`:              `"/a \"b\"\\c"`,
		"/Users/zoë/lineup":     `"/Users/zo\195\171/lineup"`,
		"/tmp/a\nb":             `"/tmp/a\010b"`,
	} {
		if got := luaString(in); got != want {
			t.Errorf("luaString(%q) = %s, want %s", in, got, want)
		}
	}
}
