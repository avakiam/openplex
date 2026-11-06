//go:build !unix

package files

func setUmask(m int) int { return m }
