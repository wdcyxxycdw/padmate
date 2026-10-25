package ownwindow

import "golang.org/x/sys/windows"

var _ windows.HWND

//sys getForegroundWindow() (hwnd windows.HWND) = user32.GetForegroundWindow
//sys getWindowThreadProcessID(hwnd windows.HWND, pid *uint32) (thread uint32) = user32.GetWindowThreadProcessId
