package main

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

// getWindowLongPtr has no error result: it returns 0 both on failure and for
// a style that is 0, and a generated function turns a last error of 0 into
// EINVAL. window_windows.go checks IsWindow before and after instead.
//sys getWindowLongPtr(hwnd windows.HWND, index int32) (value uintptr) = user32.GetWindowLongPtrW
