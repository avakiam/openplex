//go:build !linux

package docker

// killSession is a no-op off Linux (development hosts only).
func killSession(int) {}

// killTree is a no-op off Linux (development hosts only).
func killTree(int) {}
