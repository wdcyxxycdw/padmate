//go:build !windows

package main

import "errors"

// openQuitProcess is Windows-only, like the daemon's IPC endpoint.
func openQuitProcess(uint32) (quitProcess, error) { return nil, errors.ErrUnsupported }

// quitLongPath is the identity where there are no short names.
func quitLongPath(p string) string { return p }
