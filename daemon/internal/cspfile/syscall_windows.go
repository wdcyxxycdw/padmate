//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

package cspfile

//sys replaceFile(replaced *uint16, replacement *uint16, backup *uint16, flags uint32, exclude uintptr, reserved uintptr) (err error) = kernel32.ReplaceFileW
