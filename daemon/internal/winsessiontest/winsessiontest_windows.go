package winsessiontest

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// className is the class winsession registers for its session window.
const className = "padmate.winsession"

// hwndBroadcast is HWND_BROADCAST, which Send and Post refuse.
const hwndBroadcast = 0xFFFF

// Send sends msg to the session window hwnd with SendMessageW and returns
// the window procedure's result. It blocks until the session thread has
// handled the message. It panics unless hwnd is a session window of this
// process.
func Send(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	own(hwnd)
	return sendMessage(windows.HWND(hwnd), msg, wParam, lParam)
}

// Post posts msg to the session window hwnd with PostMessageW. It panics
// unless hwnd is a session window of this process.
func Post(hwnd uintptr, msg uint32, wParam, lParam uintptr) error {
	own(hwnd)
	if err := postMessage(windows.HWND(hwnd), msg, wParam, lParam); err != nil {
		return fmt.Errorf("winsessiontest: PostMessageW: %w", err)
	}
	return nil
}

// IsWindow reports whether hwnd is a window.
func IsWindow(hwnd uintptr) bool { return isWindow(windows.HWND(hwnd)) }

// InSendMessage reports whether the calling thread is handling a message
// that another thread sent: from a Querier, whether the session thread
// queries inside a sent message.
func InSendMessage() bool { return inSendMessage() }

// own panics unless hwnd is a window of this process whose class is the
// session window's.
func own(hwnd uintptr) {
	if hwnd == 0 || hwnd == hwndBroadcast {
		panic(fmt.Sprintf("winsessiontest: refusing window %#x", hwnd))
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid != windows.GetCurrentProcessId() {
		panic(fmt.Sprintf("winsessiontest: window %#x is not this process's (pid %d, err %v)", hwnd, pid, err))
	}
	buf := make([]uint16, len(className)+2)
	n, err := getClassName(windows.HWND(hwnd), &buf[0], int32(len(buf)))
	if n <= 0 || windows.UTF16ToString(buf[:n]) != className {
		panic(fmt.Sprintf("winsessiontest: window %#x is not a session window (class %q, err %v)", hwnd, windows.UTF16ToString(buf[:max(n, 0)]), err))
	}
}
