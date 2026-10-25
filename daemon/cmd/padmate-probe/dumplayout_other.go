//go:build !windows

package main

// newLayoutDumper fails: keyboard layouts are read through the Win32 API.
func newLayoutDumper() (layoutDumper, error) {
	return nil, errUnsupported
}
