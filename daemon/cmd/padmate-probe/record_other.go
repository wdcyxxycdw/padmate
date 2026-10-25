//go:build !windows

package main

import "github.com/wdcyxxycdw/padmate/daemon/internal/winhook"

// startHook fails: low-level keyboard and mouse hooks are a Windows feature.
func startHook(func(winhook.Event) bool, *winhook.MouseOptions) (eventSource, error) {
	return nil, errUnsupported
}
