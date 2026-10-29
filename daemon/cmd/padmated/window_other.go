//go:build !windows

package main

import "errors"

// startWindow fails: padmate's window is a Windows program.
func startWindow(string) error { return errors.New("no window on this platform") }
