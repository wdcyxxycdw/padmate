package tray

import (
	"errors"
	"os"
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
	class, _ := windows.UTF16PtrFromString("SystrayClass")
	var after windows.HWND
	for {
		after = findWindowEx(0, after, class, nil)
		if after == 0 {
			return errors.New("tray notification: no owned systray window")
		}
		var pid uint32
		if _, err := getWindowThreadProcessID(after, &pid); err != nil {
			continue
		}
		if pid != uint32(os.Getpid()) {
			continue
		}
		data := balloonData(after, n)
		return shellNotifyIcon(1, &data)
	}
}
