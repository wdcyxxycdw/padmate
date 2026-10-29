package tray

import (
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// nativeNotification follows NOTIFYICONDATAW, including the single DWORD
// timeout/version union. Kept separate from the systray library's private ABI.
type nativeNotification struct {
	Size                uint32
	Window              windows.HWND
	ID, Flags, Callback uint32
	Icon                windows.Handle
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	TimeoutOrVersion    uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         windows.Handle
}

func balloonData(window windows.HWND, n Notification) nativeNotification {
	title, body := n.Text()
	t, _ := windows.UTF16FromString(title)
	b, _ := windows.UTF16FromString(body)
	data := nativeNotification{Window: window, ID: 100, Flags: 0x10, InfoFlags: 2}
	data.Size = uint32(unsafe.Sizeof(data))
	copy(data.InfoTitle[:], t)
	copy(data.Info[:], b)
	return data
}

// Notify adds a warning balloon to this process's existing systray icon.
// The pinned fyne.io/systray version uses SystrayClass and icon ID 100.
// No window is activated and no second icon is created.
func Notify(n Notification) error {
	window, err := ownWindow()
	if err != nil {
		return fmt.Errorf("tray notification: %w", err)
	}
	data := balloonData(window, n)
	return shellNotifyIcon(1, &data)
}

// ownWindow is the hidden window of this process's systray icon.
func ownWindow() (windows.HWND, error) {
	class, _ := windows.UTF16PtrFromString("SystrayClass")
	var after windows.HWND
	for {
		after = findWindowEx(0, after, class, nil)
		if after == 0 {
			return 0, errors.New("no owned systray window")
		}
		var pid uint32
		if _, err := getWindowThreadProcessID(after, &pid); err != nil {
			continue
		}
		if pid == uint32(os.Getpid()) {
			return after, nil
		}
	}
}

// The pinned fyne.io/systray version's callback message of the icon
// (WM_USER + 1, with the mouse message in lParam) and the release of the
// right button, after which it shows the menu.
const (
	systrayMessage = 0x0400 + 1
	rightButtonUp  = 0x0205
)

// ShowMenu shows the menu of this process's systray icon, as a click on it
// does: it posts the icon's callback message for a release of the right
// button, which the library answers with its menu, on its own thread
// (SetOnSecondaryTapped is not set). Taps calls it for a single left click.
func ShowMenu() error {
	window, err := ownWindow()
	if err != nil {
		return fmt.Errorf("tray menu: %w", err)
	}
	return postMessage(window, systrayMessage, 0, rightButtonUp)
}

// AllowForeground lets the process that this one starts next bring its
// window to the front (AllowSetForegroundWindow(ASFW_ANY)). It works while
// this process may set the foreground itself, as it may right after a click
// on its icon; otherwise it fails and changes nothing.
func AllowForeground() error {
	return allowSetForegroundWindow(0xFFFFFFFF)
}

// DoubleClickInterval is the double-click time of the user's mouse settings.
func DoubleClickInterval() time.Duration {
	return time.Duration(getDoubleClickTime()) * time.Millisecond
}
