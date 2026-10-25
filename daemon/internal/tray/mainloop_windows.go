package tray

import "golang.org/x/sys/windows"

// MainLoopExit prepares the current main thread's message queue and returns
// a thread-safe quit request. Call it before starting the desktop loop. The
// caller must finish daemon cleanup before using the returned function.
func MainLoopExit() func() error {
	thread := windows.GetCurrentThreadId()
	var message nativeMessage
	peekMessage(&message, 0, 0, 0, 0)
	return func() error { return postThreadMessage(thread, 0x0012, 0, 0) }
}

type nativeMessage struct {
	window         windows.HWND
	message        uint32
	wParam, lParam uintptr
	time           uint32
	x, y           int32
	private        uint32
}
