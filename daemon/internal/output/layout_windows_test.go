package output

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// fakeWindows replaces the lookups of SystemLayouts until the test ends.
// The tests that use it must not run in parallel.
type fakeWindows struct {
	hwnd windows.HWND
	tid  uint32                    // the thread of hwnd; 0 when it is gone
	hkls map[uint32]windows.Handle // the layout of each thread

	tidCalls int
	asked    []uint32 // the GetKeyboardLayout arguments
}

func newFakeWindows(t *testing.T) *fakeWindows {
	f := &fakeWindows{hkls: map[uint32]windows.Handle{}}
	fg, tid, kl := getForegroundWindow, getWindowThreadProcessID, getKeyboardLayout
	t.Cleanup(func() { getForegroundWindow, getWindowThreadProcessID, getKeyboardLayout = fg, tid, kl })
	getForegroundWindow = func() windows.HWND { return f.hwnd }
	getWindowThreadProcessID = func(hwnd windows.HWND, pid *uint32) (uint32, error) {
		f.tidCalls++
		if hwnd != f.hwnd || pid != nil {
			t.Errorf("GetWindowThreadProcessId(%#x, %p), want (%#x, nil)", hwnd, pid, f.hwnd)
		}
		if f.tid == 0 {
			return 0, windows.ERROR_INVALID_WINDOW_HANDLE
		}
		return f.tid, nil
	}
	getKeyboardLayout = func(tid uint32) windows.Handle {
		f.asked = append(f.asked, tid)
		return f.hkls[tid]
	}
	return f
}

func TestSystemLayoutsForeground(t *testing.T) {
	const (
		us windows.Handle = 0x04090409
		fr windows.Handle = 0x040C040C
	)
	foreground := func(t *testing.T, want HKL) {
		t.Helper()
		got, err := SystemLayouts().Foreground()
		if err != nil || got != want {
			t.Errorf("Foreground() = %v, %v; want %v", got, err, want)
		}
	}

	t.Run("foreground thread", func(t *testing.T) {
		f := newFakeWindows(t)
		f.hwnd, f.tid = 0x1234, 42
		f.hkls[42], f.hkls[0] = fr, us
		foreground(t, HKL(fr))
		// A switch inside the window is seen by the next call.
		f.hkls[42] = us
		foreground(t, HKL(us))
		if len(f.asked) != 2 || f.asked[0] != 42 || f.asked[1] != 42 {
			t.Errorf("GetKeyboardLayout calls %v, want [42 42]", f.asked)
		}
	})

	t.Run("no foreground window", func(t *testing.T) {
		f := newFakeWindows(t)
		f.hkls[0] = us
		foreground(t, HKL(us))
		if f.tidCalls != 0 || len(f.asked) != 1 || f.asked[0] != 0 {
			t.Errorf("%d GetWindowThreadProcessId calls and GetKeyboardLayout calls %v, want 0 and [0]", f.tidCalls, f.asked)
		}
	})

	t.Run("window gone", func(t *testing.T) {
		f := newFakeWindows(t)
		f.hwnd = 0x1234
		f.hkls[0] = us
		foreground(t, HKL(us))
		if len(f.asked) != 1 || f.asked[0] != 0 {
			t.Errorf("GetKeyboardLayout calls %v, want [0]", f.asked)
		}
	})

	t.Run("no layout", func(t *testing.T) {
		newFakeWindows(t)
		if got, err := SystemLayouts().Foreground(); err == nil {
			t.Errorf("Foreground() = %v without a layout, want an error", got)
		}
	})
}

func TestHKLStringMatchesFormatHKL(t *testing.T) {
	for _, h := range []windows.Handle{0, 0x04090409, 0x040C040C, 0xF0020409, 0xFFFFFFFFF0020409} {
		if got, want := HKL(h).String(), kblayout.FormatHKL(h); got != want {
			t.Errorf("HKL(%#x).String() = %q, FormatHKL gives %q", uint64(h), got, want)
		}
	}
}

// A handle that is not loaded has no tables: Layout says so instead of
// returning empty ones, whose warnings would blame each VK.
func TestSystemLayoutsNotLoaded(t *testing.T) {
	loaded, err := kblayout.LayoutList()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []HKL{0xDEADBEEF, 0x1234040C} {
		if slices.Contains(loaded, windows.Handle(h)) {
			t.Fatalf("%v is loaded", h)
		}
		if _, err := SystemLayouts().Layout(h); err == nil || !strings.Contains(err.Error(), "not loaded") {
			t.Errorf("Layout(%v) gave the error %v, want a not loaded error", h, err)
		}
	}
}

// The tables of the layout this thread already has resolve every letter
// and digit. Reading them loads nothing.
func TestSystemLayoutsCurrentThread(t *testing.T) {
	hkl := HKL(windows.GetKeyboardLayout(0))
	l, err := SystemLayouts().Layout(hkl)
	if err != nil {
		t.Fatalf("Layout(%v): %v", hkl, err)
	}
	for _, k := range keys.Table() {
		ch, ok := k.Char()
		if _, isLetterDigit := letterDigitVK(ch); !ok || !isLetterDigit {
			continue
		}
		c := keys.MustParse("LCtrl+" + k.String())
		if _, why := l.stroke(c, ch); why != "" {
			t.Errorf("%v on %v: %s", c, hkl, why)
		}
	}
}
