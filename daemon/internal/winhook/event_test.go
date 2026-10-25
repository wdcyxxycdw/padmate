package winhook

import "testing"

func TestEventFlags(t *testing.T) {
	tests := []struct {
		flags                                 uint32
		ext, lowerIL, injected, altDown, isUp bool
	}{
		{0x00, false, false, false, false, false},
		{0x01, true, false, false, false, false},
		{0x02, false, true, false, false, false},
		{0x10, false, false, true, false, false},
		{0x20, false, false, false, true, false},
		{0x80, false, false, false, false, true},
		{0x12, false, true, true, false, false},
		{0x31, true, false, true, true, false},
		{0x91, true, false, true, false, true},
		{0xB3, true, true, true, true, true},
	}
	for _, tt := range tests {
		e := Event{Flags: tt.flags}
		got := [5]bool{e.Ext(), e.LowerILInjected(), e.Injected(), e.AltDown(), e.Up()}
		want := [5]bool{tt.ext, tt.lowerIL, tt.injected, tt.altDown, tt.isUp}
		if got != want {
			t.Errorf("flags 0x%02X: Ext, LowerILInjected, Injected, AltDown, Up = %v, want %v", tt.flags, got, want)
		}
	}
}

func TestMsgName(t *testing.T) {
	tests := []struct {
		msg  uint32
		want string
	}{
		{WMKeyDown, "WM_KEYDOWN"},
		{WMKeyUp, "WM_KEYUP"},
		{WMSysKeyDown, "WM_SYSKEYDOWN"},
		{WMSysKeyUp, "WM_SYSKEYUP"},
		{0x0102, "0x0102"},
		{0, "0x0000"},
	}
	for _, tt := range tests {
		if got := MsgName(tt.msg); got != tt.want {
			t.Errorf("MsgName(%#x) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestMouseEventFlags(t *testing.T) {
	tests := []struct {
		flags                uint32
		injected, lowerILInj bool
	}{
		{0x00, false, false},
		{0x01, true, false},
		{0x02, false, true},
		{0x03, true, true},
		// The keyboard's injected bit (0x10) is not the mouse's.
		{0x10, false, false},
	}
	for _, tt := range tests {
		e := MouseEvent{Flags: tt.flags}
		if e.Injected() != tt.injected || e.LowerILInjected() != tt.lowerILInj {
			t.Errorf("flags 0x%02X: Injected, LowerILInjected = %v, %v, want %v, %v",
				tt.flags, e.Injected(), e.LowerILInjected(), tt.injected, tt.lowerILInj)
		}
	}
}

// The delta of a wheel event is the signed high word of mouseData; the low
// word is reserved and does not count.
func TestMouseWheelDelta(t *testing.T) {
	tests := []struct {
		name      string
		msg       uint32
		data      uint32
		delta     int
		isWheel   bool
		xbutton   int
		wantPlain string
	}{
		{"up", WMMouseWheel, 120 << 16, 120, true, 0, "WM_MOUSEWHEEL"},
		{"down", WMMouseWheel, 0xFF880000, -120, true, 0, "WM_MOUSEWHEEL"},
		{"right", WMMouseHWheel, 120 << 16, 120, true, 0, "WM_MOUSEHWHEEL"},
		{"left", WMMouseHWheel, 0xFF880000, -120, true, 0, "WM_MOUSEHWHEEL"},
		{"zero", WMMouseWheel, 0, 0, true, 0, "WM_MOUSEWHEEL"},
		{"low word ignored", WMMouseWheel, 0x00780001, 120, true, 0, "WM_MOUSEWHEEL"},
		{"fine notch", WMMouseWheel, 30 << 16, 30, true, 0, "WM_MOUSEWHEEL"},
		{"not a wheel", WMLButtonDown, 120 << 16, 0, false, 0, "WM_LBUTTONDOWN"},
		{"x1", WMXButtonDown, 1 << 16, 0, false, 1, "WM_XBUTTONDOWN"},
		{"x2 up", WMXButtonUp, 2 << 16, 0, false, 2, "WM_XBUTTONUP"},
		{"move", WMMouseMove, 0, 0, false, 0, "WM_MOUSEMOVE"},
		{"unknown", 0x0203, 0, 0, false, 0, "0x0203"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := MouseEvent{Msg: tt.msg, MouseData: tt.data}
			if e.WheelDelta() != tt.delta || e.IsWheel() != tt.isWheel || e.XButton() != tt.xbutton {
				t.Errorf("WheelDelta, IsWheel, XButton = %d, %v, %d, want %d, %v, %d",
					e.WheelDelta(), e.IsWheel(), e.XButton(), tt.delta, tt.isWheel, tt.xbutton)
			}
			if got := MouseMsgName(tt.msg); got != tt.wantPlain {
				t.Errorf("MouseMsgName(%#x) = %q, want %q", tt.msg, got, tt.wantPlain)
			}
		})
	}
}

func TestMouseMsgNameAll(t *testing.T) {
	for msg, want := range map[uint32]string{
		WMLButtonUp: "WM_LBUTTONUP", WMRButtonDown: "WM_RBUTTONDOWN", WMRButtonUp: "WM_RBUTTONUP",
		WMMButtonDown: "WM_MBUTTONDOWN", WMMButtonUp: "WM_MBUTTONUP",
	} {
		if got := MouseMsgName(msg); got != want {
			t.Errorf("MouseMsgName(%#x) = %q, want %q", msg, got, want)
		}
	}
}

// The window messages a mouse hook reports for the buttons are WinUser.h's:
// the injection tests compare them with these numbers, spelled out here so that
// a typo in the constants cannot agree with itself. A side button's number is
// the high word of mouseData, for the press and the release alike, and the
// other buttons have none.
func TestMouseButtonMessages(t *testing.T) {
	for _, tt := range []struct {
		name string
		got  uint32
		want uint32
		text string
	}{
		{"WM_LBUTTONDOWN", WMLButtonDown, 0x0201, "WM_LBUTTONDOWN"},
		{"WM_LBUTTONUP", WMLButtonUp, 0x0202, "WM_LBUTTONUP"},
		{"WM_RBUTTONDOWN", WMRButtonDown, 0x0204, "WM_RBUTTONDOWN"},
		{"WM_RBUTTONUP", WMRButtonUp, 0x0205, "WM_RBUTTONUP"},
		{"WM_MBUTTONDOWN", WMMButtonDown, 0x0207, "WM_MBUTTONDOWN"},
		{"WM_MBUTTONUP", WMMButtonUp, 0x0208, "WM_MBUTTONUP"},
		{"WM_XBUTTONDOWN", WMXButtonDown, 0x020B, "WM_XBUTTONDOWN"},
		{"WM_XBUTTONUP", WMXButtonUp, 0x020C, "WM_XBUTTONUP"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %#x, want %#x", tt.name, tt.got, tt.want)
		}
		if got := MouseMsgName(tt.want); got != tt.text {
			t.Errorf("MouseMsgName(%#x) = %q, want %q", tt.want, got, tt.text)
		}
	}
	for _, tt := range []struct {
		msg  uint32
		data uint32
		want int
	}{
		{WMXButtonDown, 1 << 16, 1}, {WMXButtonUp, 1 << 16, 1},
		{WMXButtonDown, 2 << 16, 2}, {WMXButtonUp, 2 << 16, 2},
		// The low word is reserved and does not count.
		{WMXButtonDown, 2<<16 | 0xFFFF, 2},
		// Only an X button message has a button number; a left button's mouseData is not one.
		{WMLButtonDown, 1 << 16, 0}, {WMRButtonUp, 2 << 16, 0}, {WMMButtonDown, 1 << 16, 0},
	} {
		if got := (MouseEvent{Msg: tt.msg, MouseData: tt.data}).XButton(); got != tt.want {
			t.Errorf("XButton() of %s with mouseData %#x = %d, want %d", MouseMsgName(tt.msg), tt.data, got, tt.want)
		}
	}
}
