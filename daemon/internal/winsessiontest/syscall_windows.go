package winsessiontest

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	sendMessage(hwnd windows.HWND, message uint32, wParam uintptr, lParam uintptr) (ret uintptr) = user32.SendMessageW
//sys	postMessage(hwnd windows.HWND, message uint32, wParam uintptr, lParam uintptr) (err error) = user32.PostMessageW
//sys	isWindow(hwnd windows.HWND) (ok bool) = user32.IsWindow
//sys	inSendMessage() (sent bool) = user32.InSendMessage
//sys	getClassName(hwnd windows.HWND, name *uint16, n int32) (copied int32, err error) = user32.GetClassNameW
