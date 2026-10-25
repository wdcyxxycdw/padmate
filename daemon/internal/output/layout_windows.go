package output

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
)

// The lookups of SystemLayouts; tests replace them.
var (
	getForegroundWindow      = windows.GetForegroundWindow
	getWindowThreadProcessID = windows.GetWindowThreadProcessId
	getKeyboardLayout        = windows.GetKeyboardLayout
)

// SystemLayouts returns the LayoutSource of this machine. Foreground is
// the layout of the foreground window's thread, GetKeyboardLayout of
// GetWindowThreadProcessId(GetForegroundWindow()); with no foreground
// window, or no thread for it, it is the calling thread's layout,
// GetKeyboardLayout(0). Layout reads the tables with kblayout.VKToScan
// and kblayout.Chars, and fails for a layout that is not loaded. Neither
// loads nor activates a layout.
func SystemLayouts() LayoutSource { return systemLayouts{} }

type systemLayouts struct{}

func (systemLayouts) Foreground() (HKL, error) {
	var tid uint32
	if hwnd := getForegroundWindow(); hwnd != 0 {
		// A window that is gone gives tid 0.
		tid, _ = getWindowThreadProcessID(hwnd, nil)
	}
	hkl := getKeyboardLayout(tid)
	if hkl == 0 {
		return 0, errors.New("output: GetKeyboardLayout gave no layout")
	}
	return HKL(hkl), nil
}

func (systemLayouts) Layout(hkl HKL) (*Layout, error) {
	vks, err := kblayout.VKToScan(windows.Handle(hkl))
	if err != nil {
		return nil, err
	}
	if len(vks) == 0 {
		// MapVirtualKeyExW gives 0 for every VK of a layout that is not
		// loaded, and VkKeyScanExW finds no character.
		return nil, fmt.Errorf("output: layout %v has no scan codes; it is not loaded", hkl)
	}
	chars, err := kblayout.Chars(windows.Handle(hkl))
	return NewLayout(vks, chars, err), nil
}
