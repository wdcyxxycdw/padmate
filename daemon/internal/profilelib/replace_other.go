//go:build !windows

package profilelib

// fileInUse is false off Windows, where an open file does not stop a rename
// over it.
func fileInUse(error) bool { return false }
