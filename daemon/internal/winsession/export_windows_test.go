package winsession

import (
	"log/slog"
	"time"
	"unsafe"
)

// SetRegister replaces WTSRegisterSessionNotification.
func (c *Config) SetRegister(f func(hwnd uintptr) error) { c.register = f }

// SetAwake replaces the awake time.
func (c *Config) SetAwake(f func() time.Duration) { c.awake = f }

// SetDispatched is called after each dispatched message; an error or a
// panic ends the session thread.
func (c *Config) SetDispatched(f func(msg uint32, wParam uintptr) error) { c.dispatched = f }

// SetTrace is told the result of each registration and teardown call.
func (c *Config) SetTrace(f func(op string, err error)) { c.trace = f }

// SetHook sets whether Start installs the desktop switch hook.
func (c *Config) SetHook(install bool) { c.noHook = !install }

// FailTimer makes SetTimer fail, so that a goroutine posts the ticks.
func (c *Config) FailTimer() { c.noTimer = true }

// UnregisterClass unregisters the session window's class, as Windows does
// when the process's last GUI thread exits.
func UnregisterClass() error { return unregisterClass(classUTF16, instance) }

// ClassRegistered reports whether the session window's class is
// registered.
func ClassRegistered() bool {
	var wc wndClassEx
	wc.size = uint32(unsafe.Sizeof(wc))
	return getClassInfoEx(instance, classUTF16, &wc) == nil
}

// SystemQuerier returns the system's Querier.
func SystemQuerier(log *slog.Logger) Querier { return newSystemQuerier(log) }

// The session window's messages and its timer.
const (
	WMNull          = wmNull
	WMClose         = wmClose
	WMTimer         = wmTimer
	WMDesktopSwitch = wmDesktopSwitch
	WMStop          = wmStop
	WMRecheck       = wmRecheck
	TimerTick       = timerTick
	ClassName       = className
)

// The sizes of the Win32 structures.
var (
	MsgSize        = unsafe.Sizeof(msg{})
	WndClassExSize = unsafe.Sizeof(wndClassEx{})
)
