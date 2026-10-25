package winfocus

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys setWinEventHook(eventMin uint32, eventMax uint32, module windows.Handle, fn uintptr, pid uint32, tid uint32, flags uint32) (hook windows.Handle) = user32.SetWinEventHook
//sys unhookWinEvent(hook windows.Handle) (ok bool) = user32.UnhookWinEvent
//sys postMessage(hwnd windows.HWND, message uint32, wParam uintptr, lParam uintptr) (err error) = user32.PostMessageW
//sys setTimer(hwnd windows.HWND, id uintptr, ms uint32, fn uintptr) (ret uintptr, err error) = user32.SetTimer
//sys killTimer(hwnd windows.HWND, id uintptr) (err error) = user32.KillTimer
//sys getForegroundWindow() (hwnd windows.HWND) = user32.GetForegroundWindow
//sys getWindowThreadProcessID(hwnd windows.HWND, pid *uint32) (tid uint32) = user32.GetWindowThreadProcessId
//sys getClassName(hwnd windows.HWND, name *uint16, size int32) (n int32) = user32.GetClassNameW
//sys enumChildWindows(hwnd windows.HWND, callback uintptr, param uintptr) (ok bool) = user32.EnumChildWindows
//sys inSendMessage() (sent bool) = user32.InSendMessage
