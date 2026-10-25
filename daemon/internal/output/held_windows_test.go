//go:build windows

package output

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// TestAsyncKeyDownVK pins the VKs AsyncKeyDown asks GetAsyncKeyState
// about: a raw scan code by MapVirtualKeyEx, which tells left from right,
// a key sent by VK by its VK, and the keys the held-key log names by their
// US VK.
func TestAsyncKeyDownVK(t *testing.T) {
	ev := func(spec string) KeyEvent {
		t.Helper()
		e, err := KeyEventFor(keys.MustParse(spec).Key(), true)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for _, tc := range []struct {
		name string
		e    KeyEvent
		vk   uint32
	}{
		{"sc 1D", KeyEvent{Scan: 0x1D, Up: true}, 0xA2},
		{"sc E0 1D", KeyEvent{Scan: 0x1D, Ext: true, Up: true}, 0xA3},
		{"sc 2A", KeyEvent{Scan: 0x2A, Up: true}, 0xA0},
		{"sc 36", KeyEvent{Scan: 0x36, Up: true}, 0xA1},
		{"sc E0 38", KeyEvent{Scan: 0x38, Ext: true, Up: true}, 0xA5},
		{"LCtrl", ev("LCtrl"), 0xA2},
		{"RWin", ev("RWin"), 0x5C},
		{"F13", ev("F13"), 0x7C},
		{"Pause", ev("Pause"), 0x13},
		{"Num0", ev("Num0"), 0x60},
		{"menu mask", MenuMaskEvent(true), MenuMaskVK},
		// The mouse buttons are looked up by their own VKs: GetAsyncKeyState
		// reports the physical buttons, by VK_LBUTTON and its kin.
		{"MouseLeft", ev("MouseLeft"), 0x01},
		{"MouseRight", ev("RButton"), 0x02},
		{"MouseMiddle", ev("MouseMiddle"), 0x04},
		{"MouseX1", ev("XButton1"), 0x05},
		{"MouseX2", ev("MouseX2"), 0x06},
		// The log's own names, as the sweep reads them back.
		{"logged LCtrl", heldKeyEvents[KeyID{Scan: 0x1D}], 0xA2},
		{"logged RAlt", heldKeyEvents[KeyID{Scan: 0x38, Ext: true}], 0xA5},
		{"logged MouseLeft", heldKeyEvents[KeyID{Mouse: keys.MouseLeft}], 0x01},
		{"logged MouseX2", heldKeyEvents[KeyID{Mouse: keys.MouseX2}], 0x06},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vk, ok := virtualKey(tc.e)
			if !ok || vk != tc.vk {
				t.Errorf("virtualKey(%v) = %#x, %v; want %#x, true", tc.e, vk, ok, tc.vk)
			}
		})
	}
	if vk, ok := virtualKey(KeyEvent{ByVK: true}); ok {
		t.Errorf("virtualKey(vk 0) = %#x, true; want false", vk)
	}
	// A wheel notch is no key that stays down: GetAsyncKeyState has nothing
	// to say about it.
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		if vk, ok := virtualKey(ev(name)); ok {
			t.Errorf("virtualKey(%s) = %#x, true; want false", name, vk)
		}
	}
}
