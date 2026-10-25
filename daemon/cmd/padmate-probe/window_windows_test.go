package main

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWinWindowReader runs the real reader against windows every desktop has.
// It only reads, so it needs no hook and does not move the foreground. It
// checks the plumbing, not the value of an extended style: the desktop window
// is not known to have a style other than 0, so a wrong GWL_EXSTYLE index
// would go unnoticed here. The style of a window with known flags is
// SPK-09's E2E (app/tests/e2e/overlay.spec.ts), where the overlay must have
// WS_EX_NOACTIVATE, WS_EX_TRANSPARENT and WS_EX_TOPMOST.
func TestWinWindowReader(t *testing.T) {
	r, err := newWindowReader()
	if err != nil {
		t.Fatal(err)
	}

	// The foreground window can change at any moment, or be none: only the
	// pair is checked.
	hwnd, pid, err := r.Foreground()
	if err != nil {
		t.Fatalf("Foreground: %v", err)
	}
	if (hwnd == 0) != (pid == 0) {
		t.Errorf("Foreground = %#x, pid %d: want both or neither", hwnd, pid)
	}
	t.Logf("foreground window %#x, process %d", hwnd, pid)

	desktop := uintptr(windows.GetDesktopWindow())
	if desktop == 0 {
		t.Skip("this session has no desktop window")
	}
	info, err := r.Window(desktop)
	if err != nil {
		t.Fatalf("Window(desktop %#x): %v", desktop, err)
	}
	if info.HWND != desktop || info.PID == 0 {
		t.Errorf("Window(desktop) = %+v, want its handle echoed and a process", info)
	}
	t.Logf("desktop window %#x: process %d, visible %v, exstyle %#x (%s)", info.HWND, info.PID, info.Visible, info.ExStyle, exStyleString(info.ExStyle))

	// The desktop window does not change while the test runs.
	again, err := r.Window(desktop)
	if err != nil || again != info {
		t.Errorf("second Window(desktop) = %+v, %v; want %+v", again, err, info)
	}
}

func TestWinWindowReaderNotWindow(t *testing.T) {
	r, err := newWindowReader()
	if err != nil {
		t.Fatal(err)
	}
	for _, hwnd := range []uintptr{0, 1, ^uintptr(0)} {
		if _, err := r.Window(hwnd); !errors.Is(err, errNotWindow) {
			t.Errorf("Window(%#x) error = %v, want errNotWindow", hwnd, err)
		}
	}
}
