package winfocus

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func foreground() (App, error) {
	hwnd := getForegroundWindow()
	if hwnd == 0 {
		return App{}, nil
	}
	app, err := queryWindow(hwnd)
	if err != nil {
		return App{}, err
	}
	if getForegroundWindow() != hwnd {
		return App{}, errors.New("winfocus: foreground changed during query")
	}
	return app, nil
}

func windowClass(hwnd windows.HWND) string {
	var name [256]uint16
	if getClassName(hwnd, &name[0], int32(len(name))) == 0 {
		return ""
	}
	return windows.UTF16ToString(name[:])
}

func queryWindow(hwnd windows.HWND) (App, error) {
	app := App{Window: uintptr(hwnd), Class: windowClass(hwnd)}
	if IgnoredClass(app.Class) {
		return app, nil
	}
	getWindowThreadProcessID(hwnd, &app.PID)
	if app.PID == 0 {
		return App{}, errors.New("winfocus: foreground has no process")
	}
	if err := queryProcess(&app); err != nil {
		return App{}, err
	}
	if strings.EqualFold(app.Exe, "ApplicationFrameHost.exe") {
		if pid := coreWindowProcess(hwnd, app.PID); pid != 0 {
			app.PID = pid
			if err := queryProcess(&app); err != nil {
				return App{}, err
			}
		}
	}
	return app, nil
}

func queryProcess(app *App) error {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, app.PID)
	if err != nil {
		return fmt.Errorf("winfocus: OpenProcess %d: %w", app.PID, err)
	}
	defer func() { _ = windows.CloseHandle(p) }()
	name := make([]uint16, 32768)
	n := uint32(len(name))
	if err := windows.QueryFullProcessImageName(p, 0, &name[0], &n); err != nil {
		return err
	}
	app.Path = windows.UTF16ToString(name[:n])
	app.Exe = path.Base(strings.ReplaceAll(app.Path, `\`, "/"))
	var token windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("winfocus: OpenProcessToken: %w", err)
	}
	defer token.Close()
	high, err := highIntegrity(token)
	app.Elevated = high
	return err
}

func highIntegrity(token windows.Token) (bool, error) {
	var n uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &n)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return false, fmt.Errorf("winfocus: token integrity size: %w", err)
	}
	if n < uint32(unsafe.Sizeof(windows.Tokenmandatorylabel{})) || n > 65536 {
		return false, errors.New("winfocus: invalid token integrity size")
	}
	b := make([]byte, n)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &b[0], n, &n); err != nil {
		return false, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&b[0]))
	sid := label.Label.Sid
	if sid == nil || sid.SubAuthorityCount() == 0 {
		return false, errors.New("winfocus: invalid integrity SID")
	}
	return sid.SubAuthority(uint32(sid.SubAuthorityCount()-1)) >= 0x3000, nil
}

// EnumChildWindows is synchronous. This context is protected against any
// concurrent diagnostic query, and its process-lifetime callback is reused.
var (
	childMu       sync.Mutex
	childOnce     sync.Once
	childCallback uintptr
	framePID      uint32
	corePID       uint32
)

func coreWindowProcess(hwnd windows.HWND, pid uint32) uint32 {
	childMu.Lock()
	defer childMu.Unlock()
	childOnce.Do(func() {
		childCallback = windows.NewCallback(func(child, _ uintptr) uintptr {
			if windowClass(windows.HWND(child)) == "Windows.UI.Core.CoreWindow" {
				var candidate uint32
				getWindowThreadProcessID(windows.HWND(child), &candidate)
				if candidate != 0 && candidate != framePID {
					corePID = candidate
					return 0
				}
			}
			return 1
		})
	})
	framePID, corePID = pid, 0
	enumChildWindows(hwnd, childCallback, 0)
	return corePID
}
