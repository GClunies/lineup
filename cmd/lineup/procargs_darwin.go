package main

import (
	"bytes"
	"encoding/binary"
	"strings"

	"golang.org/x/sys/unix"
)

// procArgs returns the argv of the process pid, or nil if the kernel does not
// give it. kern.procargs2 holds argc, the executable's path, NUL padding, then
// argv and the environment, each string ended by a NUL.
func procArgs(pid int) []string {
	if pid <= 0 {
		return nil
	}
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(buf))
	rest := buf[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil
	}
	args := strings.SplitN(string(bytes.TrimLeft(rest[end:], "\x00")), "\x00", argc+1)
	if len(args) <= argc {
		return nil
	}
	return args[:argc]
}
