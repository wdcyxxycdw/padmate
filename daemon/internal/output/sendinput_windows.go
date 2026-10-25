package output

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// INPUT.type, KEYBDINPUT.dwFlags and MOUSEINPUT.dwFlags values from
// WinUser.h.
const (
	inputMouse             = 0      // INPUT_MOUSE
	inputKeyboard          = 1      // INPUT_KEYBOARD
	keyeventfExtendedKey   = 0x0001 // KEYEVENTF_EXTENDEDKEY
	keyeventfKeyUp         = 0x0002 // KEYEVENTF_KEYUP
	keyeventfUnicode       = 0x0004 // KEYEVENTF_UNICODE
	keyeventfScanCode      = 0x0008 // KEYEVENTF_SCANCODE
	mouseeventfMove        = 0x0001 // MOUSEEVENTF_MOVE
	mouseeventfLeftDown    = 0x0002 // MOUSEEVENTF_LEFTDOWN
	mouseeventfLeftUp      = 0x0004 // MOUSEEVENTF_LEFTUP
	mouseeventfRightDown   = 0x0008 // MOUSEEVENTF_RIGHTDOWN
	mouseeventfRightUp     = 0x0010 // MOUSEEVENTF_RIGHTUP
	mouseeventfMidDown     = 0x0020 // MOUSEEVENTF_MIDDLEDOWN
	mouseeventfMidUp       = 0x0040 // MOUSEEVENTF_MIDDLEUP
	mouseeventfXDown       = 0x0080 // MOUSEEVENTF_XDOWN
	mouseeventfXUp         = 0x0100 // MOUSEEVENTF_XUP
	mouseeventfWheel       = 0x0800 // MOUSEEVENTF_WHEEL
	mouseeventfHWheel      = 0x1000 // MOUSEEVENTF_HWHEEL
	mouseeventfVirtualDesk = 0x4000 // MOUSEEVENTF_VIRTUALDESK
	mouseeventfAbsolute    = 0x8000 // MOUSEEVENTF_ABSOLUTE

	// wheelDelta is WHEEL_DELTA: one notch of the wheel. A positive value
	// turns it forward (away from the user) or to the right.
	wheelDelta = 120

	// xButton1 and xButton2 are XBUTTON1 and XBUTTON2: the mouseData of an
	// MOUSEEVENTF_XDOWN or MOUSEEVENTF_XUP says which side button it is.
	xButton1 = 0x0001
	xButton2 = 0x0002
)

// keybdInput is the Win32 KEYBDINPUT structure.
type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// mouseInput is the Win32 MOUSEINPUT structure. It is the largest member
// of the INPUT union and also sizes that union.
type mouseInput struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// input is the Win32 INPUT structure with its union holding a KEYBDINPUT,
// padded to the size of MOUSEINPUT. On amd64 that is 4 bytes of type, 4 of
// padding and a 32-byte union: 40 bytes, which SendInput checks through
// its cbSize argument. The union holds a KEYBDINPUT when Type is
// inputKeyboard and a MOUSEINPUT, which mouse returns, when it is
// inputMouse.
type input struct {
	Type uint32
	Ki   keybdInput
	_    [unsafe.Sizeof(mouseInput{}) - unsafe.Sizeof(keybdInput{})]byte
}

// mouse returns the union of in as a MOUSEINPUT. Ki is the first member of
// the union, so the pointer is the union's address, as aligned as
// MOUSEINPUT needs, and the whole 32 bytes of the union are in in.
func (in *input) mouse() *mouseInput { return (*mouseInput)(unsafe.Pointer(&in.Ki)) }

// makeInput converts e into an input that carries extra as dwExtraInfo.
// Scan code events set KEYEVENTF_SCANCODE, so the system ignores wVk and
// derives the VK from the scan code, the keyboard layout of the
// foreground thread and, for the numpad, the NumLock state. VK events
// (Pause, the numpad digits and NumDecimal) leave KEYEVENTF_SCANCODE
// clear and pass the scan code in wScan as well, which hooks then report.
//
// The press of a wheel key becomes an INPUT_MOUSE of one notch: a
// MOUSEEVENTF_WHEEL (up positive, down negative) or MOUSEEVENTF_HWHEEL
// (right positive, left negative) of WHEEL_DELTA, with no movement and no
// absolute position. The release of a wheel key injects nothing (Silent):
// Send leaves it out and makeInput must not be given it. A mouse button is an
// INPUT_MOUSE too, its press the button's down flag and its release the up
// flag, again with no movement and no position. A Unicode event is a keyboard
// input with KEYEVENTF_UNICODE (makeUnicodeInput).
func makeInput(e KeyEvent, extra uintptr) input {
	if e.Mouse != keys.MouseNone {
		return makeMouseInput(e, extra)
	}
	if e.Unicode {
		return makeUnicodeInput(e, extra)
	}
	in := input{Type: inputKeyboard}
	in.Ki.Scan = uint16(e.Scan)
	in.Ki.ExtraInfo = extra
	if e.ByVK {
		in.Ki.Vk = e.VK
	} else {
		in.Ki.Flags |= keyeventfScanCode
	}
	if e.Ext {
		in.Ki.Flags |= keyeventfExtendedKey
	}
	if e.Up {
		in.Ki.Flags |= keyeventfKeyUp
	}
	return in
}

// makeUnicodeInput converts a Unicode event (a UTF-16 code unit of a typed text,
// OUT-21) into a keyboard input with KEYEVENTF_UNICODE: wVk 0, the code unit in
// wScan, and KEYEVENTF_KEYUP for a release. The system makes a VK_PACKET key of
// it for the foreground thread, and the layout, the IME and NumLock do not come
// into it; neither KEYEVENTF_SCANCODE nor KEYEVENTF_EXTENDEDKEY is set.
func makeUnicodeInput(e KeyEvent, extra uintptr) input {
	in := input{Type: inputKeyboard}
	in.Ki.Scan = e.Unit
	in.Ki.Flags = keyeventfUnicode
	in.Ki.ExtraInfo = extra
	if e.Up {
		in.Ki.Flags |= keyeventfKeyUp
	}
	return in
}

// makeMouseInput converts a mouse event into an input: the press of a wheel
// key, the press or release of a button, or a pointer move. A keys.MouseMove is
// MOUSEEVENTF_MOVE with dx and dy, relative: a distance in the units of the
// mouse, which the system scales with the pointer speed and, when "enhance
// pointer precision" is on, the acceleration curve (padmate changes neither). It
// carries no mouseData and no other flag; the mouse canary of the injection
// tests is two of them. A keys.MouseJump is the absolute position, 0 to 65535
// across the virtual desktop (MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK).
func makeMouseInput(e KeyEvent, extra uintptr) input {
	in := input{Type: inputMouse}
	mi := in.mouse()
	mi.ExtraInfo = extra
	switch e.Mouse {
	case keys.MouseMove:
		mi.Flags, mi.Dx, mi.Dy = mouseeventfMove, e.Dx, e.Dy
		return in
	case keys.MouseJump:
		mi.Flags = mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk
		mi.Dx, mi.Dy = e.Dx, e.Dy
		return in
	}
	if e.Mouse.IsButton() {
		mi.Flags, mi.MouseData = buttonFlags(e.Mouse, e.Up)
		return in
	}
	delta := int32(wheelDelta)
	switch e.Mouse {
	case keys.WheelUp:
		mi.Flags = mouseeventfWheel
	case keys.WheelDown:
		mi.Flags, delta = mouseeventfWheel, -wheelDelta
	case keys.WheelLeft:
		mi.Flags, delta = mouseeventfHWheel, -wheelDelta
	case keys.WheelRight:
		mi.Flags = mouseeventfHWheel
	default:
		panic("output: no mouse input for " + e.Mouse.String())
	}
	// mouseData is a DWORD holding the two's complement of a negative delta.
	mi.MouseData = uint32(delta)
	return in
}

// buttonFlags returns the dwFlags and the mouseData of the INPUT_MOUSE that
// presses, or with up releases, the mouse button m: the left, right or middle
// button's own down and up flags with no mouseData, and for a side button
// MOUSEEVENTF_XDOWN or MOUSEEVENTF_XUP with XBUTTON1 or XBUTTON2 in mouseData.
// None moves the cursor (no MOUSEEVENTF_MOVE) or carries a position.
func buttonFlags(m keys.Mouse, up bool) (flags, data uint32) {
	pick := func(down, release uint32) uint32 {
		if up {
			return release
		}
		return down
	}
	switch m {
	case keys.MouseLeft:
		return pick(mouseeventfLeftDown, mouseeventfLeftUp), 0
	case keys.MouseRight:
		return pick(mouseeventfRightDown, mouseeventfRightUp), 0
	case keys.MouseMiddle:
		return pick(mouseeventfMidDown, mouseeventfMidUp), 0
	case keys.MouseX1:
		return pick(mouseeventfXDown, mouseeventfXUp), xButton1
	case keys.MouseX2:
		return pick(mouseeventfXDown, mouseeventfXUp), xButton2
	}
	panic("output: no mouse button " + m.String())
}

// batchInputs converts events into the inputs of one SendInput call, in
// order. An event that injects nothing (Silent) makes no input. before[i] is
// the number of inputs made for the events before events[i].
func batchInputs(events []KeyEvent, extra uintptr) (inputs []input, before []int) {
	inputs = make([]input, 0, len(events))
	before = make([]int, len(events))
	for i, e := range events {
		before[i] = len(inputs)
		if !e.Silent() {
			inputs = append(inputs, makeInput(e, extra))
		}
	}
	return inputs, before
}

// injectedEvents returns how many of events were injected when SendInput
// accepted the first n inputs of batchInputs: always a prefix. An event with
// an input is injected when its input was; an event without one (Silent) when
// every input before it was, also when the next input was not.
func injectedEvents(events []KeyEvent, before []int, n int) int {
	for i, e := range events {
		if e.Silent() && n < before[i] || !e.Silent() && n <= before[i] {
			return i
		}
	}
	return len(events)
}

// SendInput is the Sender that injects events with the Win32 SendInput
// function. Every event carries dwExtraInfo = ExtraInfo(Nonce): the
// keyboard events as KEYBDINPUT and the wheel presses, the button presses
// and releases and the pointer moves as MOUSEINPUT, in one call and in order.
// Windows hands a mouse hook the low 32 bits of the dwExtraInfo of a mouse
// event, the nonce, and not the tag (measured 2026-10-19).
//
// In the daemon only the T-output thread may use it (CLAUDE.md invariant
// 3). The type does not enforce that: the daemon must pass it only to
// StartLoop with LoopOptions.Lock, whose one locked thread then makes
// every call.
type SendInput struct {
	Nonce uint32
}

// Send injects events with one SendInput call, so that other input cannot
// interleave with them. The release of a wheel key injects nothing: it
// counts as injected when every input before it was, and a batch of such
// releases alone makes no call.
func (s SendInput) Send(events []KeyEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	inputs, before := batchInputs(events, ExtraInfo(s.Nonce))
	if len(inputs) == 0 {
		return len(events), nil
	}
	count, callErr := sendInput(uint32(len(inputs)), &inputs[0], int32(unsafe.Sizeof(input{})))
	n := int(count)
	if n >= len(inputs) {
		return len(events), nil
	}
	msg := fmt.Sprintf("output: SendInput injected %d of %d inputs", n, len(inputs))
	// The generated binding reports a zero last error as EINVAL.
	if callErr != nil && !errors.Is(callErr, syscall.EINVAL) {
		msg += ": " + callErr.Error()
	}
	return injectedEvents(events, before, n), errors.New(msg + " (input is blocked while the secure desktop or the lock screen is shown," +
		" and by UIPI when the foreground window runs elevated)")
}
