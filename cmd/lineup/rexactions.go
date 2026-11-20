package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed rex/lineup.lua
var rexActions string

func installRexActions(dir, self string, reload func() error) error {
	src := strings.ReplaceAll(rexActions, `"{{lineup}}"`, luaString(self))
	path := filepath.Join(dir, "lineup.lua")
	if old, err := os.ReadFile(path); err == nil && string(old) == src {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeReplacing(path, []byte(src)); err != nil {
		return err
	}
	return reload()
}

// rexConfigDir finds the folder of init.lua by the rule in rex config --help.
func rexConfigDir() string {
	if f := os.Getenv("REX_CONFIG"); f != "" {
		return filepath.Dir(f)
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "rex")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "rex")
}

// luaString quotes s for Lua 5.1, which has no \u escape, so each byte outside
// printable ASCII becomes a decimal escape.
func luaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20 || c >= 0x7f:
			fmt.Fprintf(&b, "\\%03d", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
