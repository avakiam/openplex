//go:build linux

package docker

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killSession hangs up every process in the session led by pid (the shell and
// anything it started), then SIGKILLs survivors. pid is a host PID.
func killSession(pid int) {
	members := func() []int {
		var out []int
		ents, _ := os.ReadDir("/proc")
		for _, e := range ents {
			p, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if err != nil {
				continue
			}
			// Fields after the parenthesised comm: state ppid pgrp session ...
			s := string(b)
			i := strings.LastIndexByte(s, ')')
			if i < 0 {
				continue
			}
			f := strings.Fields(s[i+1:])
			if len(f) > 3 && f[3] == strconv.Itoa(pid) {
				out = append(out, p)
			}
		}
		return out
	}
	for _, p := range members() {
		_ = syscall.Kill(p, syscall.SIGHUP)
	}
	time.Sleep(1500 * time.Millisecond)
	for _, p := range members() {
		_ = syscall.Kill(p, syscall.SIGKILL)
	}
}
