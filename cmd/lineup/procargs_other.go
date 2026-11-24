//go:build !darwin

package main

import (
	"os"
	"strconv"
	"strings"
)

// procArgs returns the argv of the process pid, or nil if /proc does not give
// it.
func procArgs(pid int) []string {
	if pid <= 0 {
		return nil
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}
