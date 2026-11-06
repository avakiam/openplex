//go:build unix

package files

import (
	"io/fs"
	"syscall"
)

// ownerUID returns the file owner's uid (-1 if unknown).
func ownerUID(st fs.FileInfo) int {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid)
	}
	return -1
}
