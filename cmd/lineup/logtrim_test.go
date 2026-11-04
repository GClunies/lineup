package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrimLogKeepsTheLastWholeLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.log")
	os.WriteFile(path, []byte("line one\nline two\nline three\n"), 0o644)
	if err := trimLog(path, 14); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "line three\n" {
		t.Fatalf("trimmed log = %q", b)
	}
}

func TestTrimLogLeavesASmallLogAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.log")
	os.WriteFile(path, []byte("line one\n"), 0o644)
	if err := trimLog(path, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "line one\n" {
		t.Fatalf("log = %q", b)
	}
	if err := trimLog(filepath.Join(t.TempDir(), "none.log"), 10); err != nil {
		t.Fatalf("missing log: %v", err)
	}
}

func TestTrimLogBoundsALargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.log")
	os.WriteFile(path, []byte(strings.Repeat("level=WARN msg=\"rex events\"\n", 100000)), 0o644)
	if err := trimLog(path, 1<<20); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if len(b) > 1<<20 || !strings.HasPrefix(string(b), "level=WARN") {
		t.Fatalf("trimmed log is %d bytes and starts %q", len(b), b[:20])
	}
}
