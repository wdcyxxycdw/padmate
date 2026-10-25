//go:build !windows

package main

import "github.com/wdcyxxycdw/padmate/daemon/internal/output"

// platformSender fails: keys are injected with the Win32 SendInput.
// --dry-run works everywhere.
func platformSender() (output.Sender, error) {
	return nil, errUnsupported
}
