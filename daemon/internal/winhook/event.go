package winhook

import (
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// Event is one keyboard event as seen by the low-level hook: a copy of the
// KBDLLHOOKSTRUCT plus the window message it arrived with, and when the
// hook saw it.
type Event struct {
	// Time is KBDLLHOOKSTRUCT.time, the GetTickCount-based message time in
	// milliseconds.
	Time uint32
	// VK is the virtual-key code. For keys injected by scan code the
	// system derives it from the scan code and the keyboard layout.
	VK uint32
	// Scan is the scan code without prefix bytes; Ext reports the E0
	// prefix.
	Scan uint32
	// Flags is the LLKHF_* bit set; see the Flag constants.
	Flags uint32
	// Extra is dwExtraInfo, the value the injector passed in its INPUT.
	Extra uintptr
	// Msg is WM_KEYDOWN, WM_KEYUP, WM_SYSKEYDOWN or WM_SYSKEYUP.
	Msg uint32
	// Swallowed reports that the swallow predicate returned true, so the
	// hook discarded the event and no application received it.
	Swallowed bool
	// Stamp is the counter when the hook callback ran (latency.Now), the
	// first thing the callback reads.
	Stamp latency.Stamp
	// Seq numbers the events the hook thread delivered, keyboard and mouse
	// events (MouseEvent.Seq) together, from 1, in the order the thread saw
	// them. It orders a keyboard event against a mouse event, which come on
	// different channels.
	Seq uint64
}

// LLKHF_* flags of KBDLLHOOKSTRUCT.flags.
const (
	FlagExtended        = 0x01 // LLKHF_EXTENDED: E0-prefixed key
	FlagLowerILInjected = 0x02 // LLKHF_LOWER_IL_INJECTED: injected from a lower integrity level
	FlagInjected        = 0x10 // LLKHF_INJECTED: injected, by SendInput or a driver
	FlagAltDown         = 0x20 // LLKHF_ALTDOWN: Alt is held
	FlagUp              = 0x80 // LLKHF_UP: key release
)

// Keyboard window messages a low-level hook receives as wParam.
const (
	WMKeyDown    = 0x0100 // WM_KEYDOWN
	WMKeyUp      = 0x0101 // WM_KEYUP
	WMSysKeyDown = 0x0104 // WM_SYSKEYDOWN
	WMSysKeyUp   = 0x0105 // WM_SYSKEYUP
)

// Ext reports an E0-extended key (LLKHF_EXTENDED).
func (e Event) Ext() bool { return e.Flags&FlagExtended != 0 }

// Injected reports an injected event (LLKHF_INJECTED).
func (e Event) Injected() bool { return e.Flags&FlagInjected != 0 }

// LowerILInjected reports an event injected by a process running at a
// lower integrity level (LLKHF_LOWER_IL_INJECTED).
func (e Event) LowerILInjected() bool { return e.Flags&FlagLowerILInjected != 0 }

// AltDown reports that Alt was held (LLKHF_ALTDOWN).
func (e Event) AltDown() bool { return e.Flags&FlagAltDown != 0 }

// Up reports a key release (LLKHF_UP).
func (e Event) Up() bool { return e.Flags&FlagUp != 0 }

// MsgName returns the WinUser.h name of a keyboard message, such as
// "WM_KEYDOWN", or its hex value when it is not one of the four.
func MsgName(msg uint32) string {
	switch msg {
	case WMKeyDown:
		return "WM_KEYDOWN"
	case WMKeyUp:
		return "WM_KEYUP"
	case WMSysKeyDown:
		return "WM_SYSKEYDOWN"
	case WMSysKeyUp:
		return "WM_SYSKEYUP"
	}
	return fmt.Sprintf("0x%04X", msg)
}

// MouseEvent is one mouse event as seen by the low-level mouse hook: a copy
// of the MSLLHOOKSTRUCT plus the window message it arrived with, and when
// the hook saw it. A hook with a Watch predicate reports only the events the
// predicate accepts; all others pass without being copied to the channel.
type MouseEvent struct {
	// X and Y are MSLLHOOKSTRUCT.pt, the cursor position in screen
	// coordinates.
	X, Y int32
	// MouseData is MSLLHOOKSTRUCT.mouseData. For a wheel message its high
	// word is the wheel delta; for WM_XBUTTONDOWN and WM_XBUTTONUP it is the
	// button number; the low word is reserved. WheelDelta decodes it.
	MouseData uint32
	// Flags is the LLMHF_* bit set; see the MouseFlag constants.
	Flags uint32
	// Time is MSLLHOOKSTRUCT.time, the GetTickCount-based message time in
	// milliseconds.
	Time uint32
	// Extra is dwExtraInfo, the value the injector passed in its INPUT.
	Extra uintptr
	// Msg is the window message the event stands for: WM_MOUSEMOVE,
	// WM_MOUSEWHEEL, WM_LBUTTONDOWN and so on.
	Msg uint32
	// Swallowed reports that the swallow predicate returned true, so the
	// hook discarded the event and no application received it.
	Swallowed bool
	// Stamp is the counter when the hook callback ran (latency.Now).
	Stamp latency.Stamp
	// Seq numbers the events the hook thread delivered, with Event.Seq.
	Seq uint64
}

// MouseOptions configures the mouse hook of EnableMouse. Both predicates run
// on the hook thread for every mouse event of the system, physical moves
// included, and must be fast and must not block or panic.
type MouseOptions struct {
	// Watch decides which events the hook reports. An event it rejects passes
	// straight to the next hook: it is not delivered on the channel and
	// cannot be swallowed. A nil Watch accepts the injected events (the
	// MouseEvent.Injected ones) and none of the physical mouse's, which
	// produces hundreds of moves a second: a hook watches the physical mouse
	// only when a Watch says so.
	Watch func(MouseEvent) bool
	// Swallow, when not nil, is called for each watched event; returning
	// true discards the event so that no application receives it. Swallow
	// only events you injected yourself, never the physical mouse.
	Swallow func(MouseEvent) bool
}

// LLMHF_* flags of MSLLHOOKSTRUCT.flags. They are not the LLKHF_* values: the
// injected bit is 0x01 here and 0x10 on a keyboard event.
const (
	MouseFlagInjected        = 0x01 // LLMHF_INJECTED: injected, by SendInput or a driver
	MouseFlagLowerILInjected = 0x02 // LLMHF_LOWER_IL_INJECTED: injected from a lower integrity level
)

// Mouse window messages a low-level mouse hook receives as wParam.
const (
	WMMouseMove   = 0x0200 // WM_MOUSEMOVE
	WMLButtonDown = 0x0201 // WM_LBUTTONDOWN
	WMLButtonUp   = 0x0202 // WM_LBUTTONUP
	WMRButtonDown = 0x0204 // WM_RBUTTONDOWN
	WMRButtonUp   = 0x0205 // WM_RBUTTONUP
	WMMButtonDown = 0x0207 // WM_MBUTTONDOWN
	WMMButtonUp   = 0x0208 // WM_MBUTTONUP
	WMMouseWheel  = 0x020A // WM_MOUSEWHEEL
	WMXButtonDown = 0x020B // WM_XBUTTONDOWN
	WMXButtonUp   = 0x020C // WM_XBUTTONUP
	WMMouseHWheel = 0x020E // WM_MOUSEHWHEEL
)

// Injected reports an injected event (LLMHF_INJECTED).
func (e MouseEvent) Injected() bool { return e.Flags&MouseFlagInjected != 0 }

// LowerILInjected reports an event injected by a process running at a lower
// integrity level (LLMHF_LOWER_IL_INJECTED).
func (e MouseEvent) LowerILInjected() bool { return e.Flags&MouseFlagLowerILInjected != 0 }

// IsWheel reports a vertical or horizontal wheel event.
func (e MouseEvent) IsWheel() bool { return e.Msg == WMMouseWheel || e.Msg == WMMouseHWheel }

// WheelDelta returns the signed wheel delta of a wheel event, the high word
// of MouseData: positive is forward (away from the user) or to the right,
// and a notch is 120. It is 0 for any other event.
func (e MouseEvent) WheelDelta() int {
	if !e.IsWheel() {
		return 0
	}
	return int(int16(e.MouseData >> 16))
}

// XButton returns the number of the extra button of a WM_XBUTTONDOWN or
// WM_XBUTTONUP event, 1 or 2, and 0 for any other event.
func (e MouseEvent) XButton() int {
	if e.Msg != WMXButtonDown && e.Msg != WMXButtonUp {
		return 0
	}
	return int(e.MouseData >> 16)
}

// MouseMsgName returns the WinUser.h name of a mouse message, such as
// "WM_MOUSEWHEEL", or its hex value when it is not one of those a low-level
// mouse hook reports.
func MouseMsgName(msg uint32) string {
	switch msg {
	case WMMouseMove:
		return "WM_MOUSEMOVE"
	case WMLButtonDown:
		return "WM_LBUTTONDOWN"
	case WMLButtonUp:
		return "WM_LBUTTONUP"
	case WMRButtonDown:
		return "WM_RBUTTONDOWN"
	case WMRButtonUp:
		return "WM_RBUTTONUP"
	case WMMButtonDown:
		return "WM_MBUTTONDOWN"
	case WMMButtonUp:
		return "WM_MBUTTONUP"
	case WMMouseWheel:
		return "WM_MOUSEWHEEL"
	case WMXButtonDown:
		return "WM_XBUTTONDOWN"
	case WMXButtonUp:
		return "WM_XBUTTONUP"
	case WMMouseHWheel:
		return "WM_MOUSEHWHEEL"
	}
	return fmt.Sprintf("0x%04X", msg)
}
