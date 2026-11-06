//go:build unix

package files

import "syscall"

func setUmask(m int) int { return syscall.Umask(m) }
