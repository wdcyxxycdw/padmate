package winhook

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	setWindowsHookEx(idHook int32, fn uintptr, module windows.Handle, threadID uint32) (hhk windows.Handle, err error) = user32.SetWindowsHookExW
//sys	unhookWindowsHookEx(hhk windows.Handle) (err error) = user32.UnhookWindowsHookEx
//sys	callNextHookEx(hhk windows.Handle, nCode int32, wParam uintptr, lParam uintptr) (ret uintptr) = user32.CallNextHookEx
//sys	getMessage(m *msg, hwnd windows.HWND, msgMin uint32, msgMax uint32) (ret int32, err error) [failretval==-1] = user32.GetMessageW
//sys	peekMessage(m *msg, hwnd windows.HWND, msgMin uint32, msgMax uint32, remove uint32) (ok bool) = user32.PeekMessageW
//sys	postThreadMessage(threadID uint32, message uint32, wParam uintptr, lParam uintptr) (err error) = user32.PostThreadMessageW
