package ownwindow

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

func image(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func parent(pid uint32) (uint32, error) {
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(h, &e); err != nil {
		return 0, err
	}
	for {
		if e.ProcessID == pid {
			return e.ParentProcessID, nil
		}
		if err := windows.Process32Next(h, &e); err != nil {
			return 0, err
		}
	}
}

// Verify requires that the connected bridge is an actual child of the UI and
// that both executable images belong to this installation.
func Verify(uiPID, bridgePID uint32) (string, error) {
	if uiPID == 0 || bridgePID == 0 {
		return "", errors.New("ownwindow: missing process identity")
	}
	p, err := parent(bridgePID)
	if err != nil {
		return "", err
	}
	if p != uiPID {
		return "", errors.New("ownwindow: bridge is not a child of the UI")
	}
	if err := sameUser(uiPID); err != nil {
		return "", err
	}
	ui, err := image(uiPID)
	if err != nil {
		return "", err
	}
	bridge, err := image(bridgePID)
	if err != nil {
		return "", err
	}
	daemon, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !TrustedPaths(daemon, ui, bridge) {
		return "", errors.New("ownwindow: UI or bridge image does not belong to this installation")
	}
	return ui, nil
}

func sameUser(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	owner, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !owner.User.Sid.Equals(current.User.Sid) {
		return errors.New("ownwindow: UI belongs to another user")
	}
	return nil
}

func ForegroundPID() uint32 {
	hwnd := getForegroundWindow()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	getWindowThreadProcessID(hwnd, &pid)
	return pid
}

func Matches(pid uint32, path string) bool {
	return pid != 0 && ForegroundPID() == pid
}

// Instance is the process creation timestamp; a reused PID cannot inherit a
// previous Electron window lease.
func Instance(pid uint32) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}

func Alive(pid uint32, path string, created uint64) bool {
	instance, err := Instance(pid)
	// Access denied is an unknown identity, never permission to resume
	// injection into a registered foreground window. A missing process or
	// successful query showing a different creation time ends its lease.
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	if instance != created {
		return false
	}
	actual, err := image(pid)
	return err != nil || strings.EqualFold(actual, path)
}
