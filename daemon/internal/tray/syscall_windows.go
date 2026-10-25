package tray

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys postThreadMessage(thread uint32, message uint32, wParam uintptr, lParam uintptr) (err error) = user32.PostThreadMessageW
//sys peekMessage(message *nativeMessage, window windows.HWND, minMessage uint32, maxMessage uint32, remove uint32) (found bool) = user32.PeekMessageW
//sys findWindowEx(parent windows.HWND, after windows.HWND, class *uint16, title *uint16) (window windows.HWND) = user32.FindWindowExW
//sys getWindowThreadProcessID(window windows.HWND, pid *uint32) (thread uint32, err error) = user32.GetWindowThreadProcessId
//sys shellNotifyIcon(message uint32, data *nativeNotification) (err error) = shell32.Shell_NotifyIconW
