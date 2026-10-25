package winsessiontest_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winsessiontest"
)

// TestRefuses checks that Send and Post refuse, before sending anything,
// every window that is not a session window of this process: none, the
// broadcast handle, and windows of other processes.
func TestRefuses(t *testing.T) {
	const wmNull = 0 // WM_NULL, in case a check is ever missing
	windowsOf := []struct {
		name string
		hwnd uintptr
	}{
		{"none", 0},
		{"HWND_BROADCAST", 0xFFFF},
		{"the desktop window", uintptr(windows.GetDesktopWindow())},
		{"the shell window", uintptr(windows.GetShellWindow())},
	}
	for _, w := range windowsOf {
		if w.hwnd == 0 && w.name != "none" {
			t.Logf("%s: none here", w.name)
			continue
		}
		for _, call := range []struct {
			name string
			f    func()
		}{
			{"Send", func() { winsessiontest.Send(w.hwnd, wmNull, 0, 0) }},
			{"Post", func() { _ = winsessiontest.Post(w.hwnd, wmNull, 0, 0) }},
		} {
			if msg := panics(call.f); !strings.HasPrefix(msg, "winsessiontest: ") {
				t.Errorf("%s(%s): panic %q, want a refusal", call.name, w.name, msg)
			}
		}
	}
}

// panics returns what f panicked with, or "" if it returned.
func panics(f func()) (msg string) {
	defer func() {
		if p := recover(); p != nil {
			msg = fmt.Sprint(p)
		}
	}()
	f()
	return ""
}
