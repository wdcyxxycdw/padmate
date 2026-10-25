//go:build !windows

package main

// newWindowReader fails: window state is read through the Win32 API.
func newWindowReader() (windowReader, error) {
	return nil, errUnsupported
}
