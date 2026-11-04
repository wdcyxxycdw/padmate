//go:build !windows

package cspfile

// Running is unsupported off Windows.
func Running() (bool, error) { return false, ErrUnsupported }
