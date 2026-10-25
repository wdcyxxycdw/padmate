package injecttest

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	mapVirtualKeyEx(code uint32, mapType uint32, hkl windows.Handle) (ret uint32) = user32.MapVirtualKeyExW
//sys	getSystemMetrics(index int32) (ret int32) = user32.GetSystemMetrics
