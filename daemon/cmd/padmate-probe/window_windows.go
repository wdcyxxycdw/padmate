package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// gwlExStyle is GWL_EXSTYLE, the index of the extended window style.
const gwlExStyle = -20

// winWindowReader reads window state with user32. It only reads: no window
// is activated, moved or sent a message.
type winWindowReader struct{}

func newWindowReader() (windowReader, error) {
	return winWindowReader{}, nil
}

// foregroundAttempts is how often Foreground asks again when the foreground
// window is destroyed between GetForegroundWindow and the query of its
// process.
const foregroundAttempts = 3

func (winWindowReader) Foreground() (uintptr, uint32, error) {
	var err error
	for range foregroundAttempts {
		hwnd := windows.GetForegroundWindow()
		if hwnd == 0 {
			return 0, 0, nil
		}
		var pid uint32
		if pid, err = ownerPID(hwnd); err == nil {
			return uintptr(hwnd), pid, nil
		}
	}
	return 0, 0, fmt.Errorf("foreground window: %w", err)
}

func (winWindowReader) Window(hwnd uintptr) (windowInfo, error) {
	h := windows.HWND(hwnd)
	if !windows.IsWindow(h) {
		return windowInfo{}, errNotWindow
	}
	info := windowInfo{
		HWND:    hwnd,
		ExStyle: uint32(getWindowLongPtr(h, gwlExStyle)), // the style is a DWORD; drop any sign extension
		Visible: windows.IsWindowVisible(h),
	}
	pid, err := ownerPID(h)
	// A window destroyed while it was read has no style or process left to
	// report: say so, not that a query failed.
	if !windows.IsWindow(h) {
		return windowInfo{}, errNotWindow
	}
	if err != nil {
		return windowInfo{}, err
	}
	info.PID = pid
	return info, nil
}

// ownerPID returns the id of the process that owns hwnd.
func ownerPID(hwnd windows.HWND) (uint32, error) {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil {
		return 0, err
	}
	if pid == 0 {
		return 0, errors.New("the window has no process")
	}
	return pid, nil
}
