package winsession

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	registerClassEx(wc *wndClassEx) (atom uint16, err error) = user32.RegisterClassExW
// The tests' (export_windows_test.go): the class is never unregistered.
//sys	unregisterClass(className *uint16, instance windows.Handle) (err error) = user32.UnregisterClassW
//sys	getClassInfoEx(instance windows.Handle, className *uint16, wc *wndClassEx) (err error) = user32.GetClassInfoExW
//sys	createWindowEx(exStyle uint32, className *uint16, windowName *uint16, style uint32, x int32, y int32, width int32, height int32, parent windows.HWND, menu windows.Handle, instance windows.Handle, param uintptr) (hwnd windows.HWND, err error) = user32.CreateWindowExW
//sys	destroyWindow(hwnd windows.HWND) (err error) = user32.DestroyWindow
//sys	defWindowProc(hwnd windows.HWND, message uint32, wParam uintptr, lParam uintptr) (ret uintptr) = user32.DefWindowProcW
//sys	getMessage(m *msg, hwnd windows.HWND, msgMin uint32, msgMax uint32) (ret int32, err error) [failretval==-1] = user32.GetMessageW
//sys	dispatchMessage(m *msg) (ret uintptr) = user32.DispatchMessageW
//sys	postMessage(hwnd windows.HWND, message uint32, wParam uintptr, lParam uintptr) (err error) = user32.PostMessageW
//sys	postQuitMessage(code int32) = user32.PostQuitMessage
//sys	inSendMessage() (sent bool) = user32.InSendMessage
//sys	setTimer(hwnd windows.HWND, id uintptr, ms uint32, fn uintptr) (ret uintptr, err error) = user32.SetTimer
//sys	killTimer(hwnd windows.HWND, id uintptr) (err error) = user32.KillTimer
//sys	setWinEventHook(eventMin uint32, eventMax uint32, module windows.Handle, fn uintptr, pid uint32, tid uint32, flags uint32) (hook windows.Handle) = user32.SetWinEventHook
//sys	unhookWinEvent(hook windows.Handle) (ok bool) = user32.UnhookWinEvent
//sys	getThreadDesktop(tid uint32) (desk windows.Handle, err error) = user32.GetThreadDesktop
//sys	openInputDesktop(flags uint32, inherit bool, access uint32) (desk windows.Handle, err error) = user32.OpenInputDesktop
//sys	closeDesktop(desk windows.Handle) (err error) = user32.CloseDesktop
//sys	getUserObjectInformation(obj windows.Handle, index int32, info unsafe.Pointer, length uint32, needed *uint32) (err error) = user32.GetUserObjectInformationW
//sys	registerSuspendResumeNotification(recipient windows.HWND, flags uint32) (h windows.Handle, err error) = user32.RegisterSuspendResumeNotification
//sys	unregisterSuspendResumeNotification(h windows.Handle) (err error) = user32.UnregisterSuspendResumeNotification
//sys	wtsRegisterSessionNotification(hwnd windows.HWND, flags uint32) (err error) = wtsapi32.WTSRegisterSessionNotification
//sys	wtsUnRegisterSessionNotification(hwnd windows.HWND) (err error) = wtsapi32.WTSUnRegisterSessionNotification
//sys	wtsQuerySessionInformation(server windows.Handle, sessionID uint32, class uint32, buf **byte, n *uint32) (err error) = wtsapi32.WTSQuerySessionInformationW
//sys	queryUnbiasedInterruptTime(t *uint64) (err error) = kernel32.QueryUnbiasedInterruptTime
