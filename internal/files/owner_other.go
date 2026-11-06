//go:build !unix

package files

import "io/fs"

// ownerUID is unknown on non-unix platforms (development only).
func ownerUID(fs.FileInfo) int { return -1 }
