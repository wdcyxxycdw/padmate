package kblayout

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	getKeyboardLayoutList(n int32, list *windows.Handle) (count int32, err error) = user32.GetKeyboardLayoutList
//sys	mapVirtualKeyEx(code uint32, mapType uint32, hkl windows.Handle) (ret uint32) = user32.MapVirtualKeyExW
//sys	vkKeyScanEx(ch uint16, hkl windows.Handle) (ret uint16) = user32.VkKeyScanExW
