package keys_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// pin is the expected US mapping of one table entry.
type pin struct {
	name  string
	kind  keys.Kind
	scan  uint8
	ext   bool
	vk    uint16
	shift bool       // Plus only
	e1    bool       // Pause only
	mouse keys.Mouse // mouse keys only
}

const (
	mod  = keys.KindModifier
	nmd  = keys.KindNamed
	chr  = keys.KindChar
	phys = keys.KindPhysical
	msk  = keys.KindMouse
	med  = keys.KindMedia
)

// wantTable pins every entry of the key table, in listing order, against
// the Microsoft set-1 scan code table and the WinUser.h VK constants. It is
// written out independently of table.go on purpose.
var wantTable = []pin{
	// Modifiers, canonical order. Generic modifiers map to their left key.
	{name: "Ctrl", kind: mod, scan: 0x1D, vk: 0xA2},
	{name: "LCtrl", kind: mod, scan: 0x1D, vk: 0xA2},
	{name: "RCtrl", kind: mod, scan: 0x1D, ext: true, vk: 0xA3},
	{name: "Shift", kind: mod, scan: 0x2A, vk: 0xA0},
	{name: "LShift", kind: mod, scan: 0x2A, vk: 0xA0},
	{name: "RShift", kind: mod, scan: 0x36, vk: 0xA1},
	{name: "Alt", kind: mod, scan: 0x38, vk: 0xA4},
	{name: "LAlt", kind: mod, scan: 0x38, vk: 0xA4},
	{name: "RAlt", kind: mod, scan: 0x38, ext: true, vk: 0xA5},
	{name: "Win", kind: mod, scan: 0x5B, ext: true, vk: 0x5B},
	{name: "LWin", kind: mod, scan: 0x5B, ext: true, vk: 0x5B},
	{name: "RWin", kind: mod, scan: 0x5C, ext: true, vk: 0x5C},

	{name: "F1", kind: nmd, scan: 0x3B, vk: 0x70},
	{name: "F2", kind: nmd, scan: 0x3C, vk: 0x71},
	{name: "F3", kind: nmd, scan: 0x3D, vk: 0x72},
	{name: "F4", kind: nmd, scan: 0x3E, vk: 0x73},
	{name: "F5", kind: nmd, scan: 0x3F, vk: 0x74},
	{name: "F6", kind: nmd, scan: 0x40, vk: 0x75},
	{name: "F7", kind: nmd, scan: 0x41, vk: 0x76},
	{name: "F8", kind: nmd, scan: 0x42, vk: 0x77},
	{name: "F9", kind: nmd, scan: 0x43, vk: 0x78},
	{name: "F10", kind: nmd, scan: 0x44, vk: 0x79},
	{name: "F11", kind: nmd, scan: 0x57, vk: 0x7A},
	{name: "F12", kind: nmd, scan: 0x58, vk: 0x7B},
	{name: "F13", kind: nmd, scan: 0x64, vk: 0x7C},
	{name: "F14", kind: nmd, scan: 0x65, vk: 0x7D},
	{name: "F15", kind: nmd, scan: 0x66, vk: 0x7E},
	{name: "F16", kind: nmd, scan: 0x67, vk: 0x7F},
	{name: "F17", kind: nmd, scan: 0x68, vk: 0x80},
	{name: "F18", kind: nmd, scan: 0x69, vk: 0x81},
	{name: "F19", kind: nmd, scan: 0x6A, vk: 0x82},
	{name: "F20", kind: nmd, scan: 0x6B, vk: 0x83},
	{name: "F21", kind: nmd, scan: 0x6C, vk: 0x84},
	{name: "F22", kind: nmd, scan: 0x6D, vk: 0x85},
	{name: "F23", kind: nmd, scan: 0x6E, vk: 0x86},
	{name: "F24", kind: nmd, scan: 0x76, vk: 0x87},
	{name: "Esc", kind: nmd, scan: 0x01, vk: 0x1B},
	{name: "Tab", kind: nmd, scan: 0x0F, vk: 0x09},
	{name: "Space", kind: nmd, scan: 0x39, vk: 0x20},
	{name: "Enter", kind: nmd, scan: 0x1C, vk: 0x0D},
	{name: "Backspace", kind: nmd, scan: 0x0E, vk: 0x08},
	{name: "Insert", kind: nmd, scan: 0x52, ext: true, vk: 0x2D},
	{name: "Delete", kind: nmd, scan: 0x53, ext: true, vk: 0x2E},
	{name: "Home", kind: nmd, scan: 0x47, ext: true, vk: 0x24},
	{name: "End", kind: nmd, scan: 0x4F, ext: true, vk: 0x23},
	{name: "PageUp", kind: nmd, scan: 0x49, ext: true, vk: 0x21},
	{name: "PageDown", kind: nmd, scan: 0x51, ext: true, vk: 0x22},
	{name: "Up", kind: nmd, scan: 0x48, ext: true, vk: 0x26},
	{name: "Down", kind: nmd, scan: 0x50, ext: true, vk: 0x28},
	{name: "Left", kind: nmd, scan: 0x4B, ext: true, vk: 0x25},
	{name: "Right", kind: nmd, scan: 0x4D, ext: true, vk: 0x27},
	{name: "CapsLock", kind: nmd, scan: 0x3A, vk: 0x14},
	{name: "NumLock", kind: nmd, scan: 0x45, vk: 0x90},
	{name: "ScrollLock", kind: nmd, scan: 0x46, vk: 0x91},
	{name: "PrintScreen", kind: nmd, scan: 0x37, ext: true, vk: 0x2C},
	{name: "Pause", kind: nmd, scan: 0x45, e1: true, vk: 0x13},
	{name: "Menu", kind: nmd, scan: 0x5D, ext: true, vk: 0x5D},
	{name: "Num0", kind: nmd, scan: 0x52, vk: 0x60},
	{name: "Num1", kind: nmd, scan: 0x4F, vk: 0x61},
	{name: "Num2", kind: nmd, scan: 0x50, vk: 0x62},
	{name: "Num3", kind: nmd, scan: 0x51, vk: 0x63},
	{name: "Num4", kind: nmd, scan: 0x4B, vk: 0x64},
	{name: "Num5", kind: nmd, scan: 0x4C, vk: 0x65},
	{name: "Num6", kind: nmd, scan: 0x4D, vk: 0x66},
	{name: "Num7", kind: nmd, scan: 0x47, vk: 0x67},
	{name: "Num8", kind: nmd, scan: 0x48, vk: 0x68},
	{name: "Num9", kind: nmd, scan: 0x49, vk: 0x69},
	{name: "NumAdd", kind: nmd, scan: 0x4E, vk: 0x6B},
	{name: "NumSubtract", kind: nmd, scan: 0x4A, vk: 0x6D},
	{name: "NumMultiply", kind: nmd, scan: 0x37, vk: 0x6A},
	{name: "NumDivide", kind: nmd, scan: 0x35, ext: true, vk: 0x6F},
	{name: "NumDecimal", kind: nmd, scan: 0x53, vk: 0x6E},
	{name: "NumEnter", kind: nmd, scan: 0x1C, ext: true, vk: 0x0D},
	{name: "IntlBackslash", kind: nmd, scan: 0x56, vk: 0xE2},
	{name: "IntlRo", kind: nmd, scan: 0x73, vk: 0xE2},
	{name: "IntlYen", kind: nmd, scan: 0x7D, vk: 0xDC},
	{name: "Convert", kind: nmd, scan: 0x79, vk: 0x1C},
	{name: "NonConvert", kind: nmd, scan: 0x7B, vk: 0x1D},
	{name: "KanaMode", kind: nmd, scan: 0x70, vk: 0xF2}, // kbd106 maps scan 0x70 to VK_DBE_HIRAGANA

	{name: "A", kind: chr, scan: 0x1E, vk: 0x41},
	{name: "B", kind: chr, scan: 0x30, vk: 0x42},
	{name: "C", kind: chr, scan: 0x2E, vk: 0x43},
	{name: "D", kind: chr, scan: 0x20, vk: 0x44},
	{name: "E", kind: chr, scan: 0x12, vk: 0x45},
	{name: "F", kind: chr, scan: 0x21, vk: 0x46},
	{name: "G", kind: chr, scan: 0x22, vk: 0x47},
	{name: "H", kind: chr, scan: 0x23, vk: 0x48},
	{name: "I", kind: chr, scan: 0x17, vk: 0x49},
	{name: "J", kind: chr, scan: 0x24, vk: 0x4A},
	{name: "K", kind: chr, scan: 0x25, vk: 0x4B},
	{name: "L", kind: chr, scan: 0x26, vk: 0x4C},
	{name: "M", kind: chr, scan: 0x32, vk: 0x4D},
	{name: "N", kind: chr, scan: 0x31, vk: 0x4E},
	{name: "O", kind: chr, scan: 0x18, vk: 0x4F},
	{name: "P", kind: chr, scan: 0x19, vk: 0x50},
	{name: "Q", kind: chr, scan: 0x10, vk: 0x51},
	{name: "R", kind: chr, scan: 0x13, vk: 0x52},
	{name: "S", kind: chr, scan: 0x1F, vk: 0x53},
	{name: "T", kind: chr, scan: 0x14, vk: 0x54},
	{name: "U", kind: chr, scan: 0x16, vk: 0x55},
	{name: "V", kind: chr, scan: 0x2F, vk: 0x56},
	{name: "W", kind: chr, scan: 0x11, vk: 0x57},
	{name: "X", kind: chr, scan: 0x2D, vk: 0x58},
	{name: "Y", kind: chr, scan: 0x15, vk: 0x59},
	{name: "Z", kind: chr, scan: 0x2C, vk: 0x5A},
	{name: "0", kind: chr, scan: 0x0B, vk: 0x30},
	{name: "1", kind: chr, scan: 0x02, vk: 0x31},
	{name: "2", kind: chr, scan: 0x03, vk: 0x32},
	{name: "3", kind: chr, scan: 0x04, vk: 0x33},
	{name: "4", kind: chr, scan: 0x05, vk: 0x34},
	{name: "5", kind: chr, scan: 0x06, vk: 0x35},
	{name: "6", kind: chr, scan: 0x07, vk: 0x36},
	{name: "7", kind: chr, scan: 0x08, vk: 0x37},
	{name: "8", kind: chr, scan: 0x09, vk: 0x38},
	{name: "9", kind: chr, scan: 0x0A, vk: 0x39},
	{name: "-", kind: chr, scan: 0x0C, vk: 0xBD},
	{name: "=", kind: chr, scan: 0x0D, vk: 0xBB},
	{name: "[", kind: chr, scan: 0x1A, vk: 0xDB},
	{name: "]", kind: chr, scan: 0x1B, vk: 0xDD},
	{name: ";", kind: chr, scan: 0x27, vk: 0xBA},
	{name: "'", kind: chr, scan: 0x28, vk: 0xDE},
	{name: "`", kind: chr, scan: 0x29, vk: 0xC0},
	{name: "\\", kind: chr, scan: 0x2B, vk: 0xDC},
	{name: ",", kind: chr, scan: 0x33, vk: 0xBC},
	{name: ".", kind: chr, scan: 0x34, vk: 0xBE},
	{name: "/", kind: chr, scan: 0x35, vk: 0xBF},
	{name: "Plus", kind: chr, scan: 0x0D, vk: 0xBB, shift: true},

	{name: "KeyA", kind: phys, scan: 0x1E, vk: 0x41},
	{name: "KeyB", kind: phys, scan: 0x30, vk: 0x42},
	{name: "KeyC", kind: phys, scan: 0x2E, vk: 0x43},
	{name: "KeyD", kind: phys, scan: 0x20, vk: 0x44},
	{name: "KeyE", kind: phys, scan: 0x12, vk: 0x45},
	{name: "KeyF", kind: phys, scan: 0x21, vk: 0x46},
	{name: "KeyG", kind: phys, scan: 0x22, vk: 0x47},
	{name: "KeyH", kind: phys, scan: 0x23, vk: 0x48},
	{name: "KeyI", kind: phys, scan: 0x17, vk: 0x49},
	{name: "KeyJ", kind: phys, scan: 0x24, vk: 0x4A},
	{name: "KeyK", kind: phys, scan: 0x25, vk: 0x4B},
	{name: "KeyL", kind: phys, scan: 0x26, vk: 0x4C},
	{name: "KeyM", kind: phys, scan: 0x32, vk: 0x4D},
	{name: "KeyN", kind: phys, scan: 0x31, vk: 0x4E},
	{name: "KeyO", kind: phys, scan: 0x18, vk: 0x4F},
	{name: "KeyP", kind: phys, scan: 0x19, vk: 0x50},
	{name: "KeyQ", kind: phys, scan: 0x10, vk: 0x51},
	{name: "KeyR", kind: phys, scan: 0x13, vk: 0x52},
	{name: "KeyS", kind: phys, scan: 0x1F, vk: 0x53},
	{name: "KeyT", kind: phys, scan: 0x14, vk: 0x54},
	{name: "KeyU", kind: phys, scan: 0x16, vk: 0x55},
	{name: "KeyV", kind: phys, scan: 0x2F, vk: 0x56},
	{name: "KeyW", kind: phys, scan: 0x11, vk: 0x57},
	{name: "KeyX", kind: phys, scan: 0x2D, vk: 0x58},
	{name: "KeyY", kind: phys, scan: 0x15, vk: 0x59},
	{name: "KeyZ", kind: phys, scan: 0x2C, vk: 0x5A},
	{name: "Key0", kind: phys, scan: 0x0B, vk: 0x30},
	{name: "Key1", kind: phys, scan: 0x02, vk: 0x31},
	{name: "Key2", kind: phys, scan: 0x03, vk: 0x32},
	{name: "Key3", kind: phys, scan: 0x04, vk: 0x33},
	{name: "Key4", kind: phys, scan: 0x05, vk: 0x34},
	{name: "Key5", kind: phys, scan: 0x06, vk: 0x35},
	{name: "Key6", kind: phys, scan: 0x07, vk: 0x36},
	{name: "Key7", kind: phys, scan: 0x08, vk: 0x37},
	{name: "Key8", kind: phys, scan: 0x09, vk: 0x38},
	{name: "Key9", kind: phys, scan: 0x0A, vk: 0x39},
	{name: "Key-", kind: phys, scan: 0x0C, vk: 0xBD},
	{name: "Key=", kind: phys, scan: 0x0D, vk: 0xBB},
	{name: "Key[", kind: phys, scan: 0x1A, vk: 0xDB},
	{name: "Key]", kind: phys, scan: 0x1B, vk: 0xDD},
	{name: "Key;", kind: phys, scan: 0x27, vk: 0xBA},
	{name: "Key'", kind: phys, scan: 0x28, vk: 0xDE},
	{name: "Key`", kind: phys, scan: 0x29, vk: 0xC0},
	{name: "Key\\", kind: phys, scan: 0x2B, vk: 0xDC},
	{name: "Key,", kind: phys, scan: 0x33, vk: 0xBC},
	{name: "Key.", kind: phys, scan: 0x34, vk: 0xBE},
	{name: "Key/", kind: phys, scan: 0x35, vk: 0xBF},

	// Media and volume keys (ADR-0070, OUT-20): E0-extended scan codes of the
	// Microsoft set-1 table and the VK_VOLUME_* and VK_MEDIA_* constants.
	{name: "VolumeUp", kind: med, scan: 0x30, ext: true, vk: 0xAF},
	{name: "VolumeDown", kind: med, scan: 0x2E, ext: true, vk: 0xAE},
	{name: "VolumeMute", kind: med, scan: 0x20, ext: true, vk: 0xAD},
	{name: "MediaPlayPause", kind: med, scan: 0x22, ext: true, vk: 0xB3},
	{name: "MediaNext", kind: med, scan: 0x19, ext: true, vk: 0xB0},
	{name: "MediaPrevious", kind: med, scan: 0x10, ext: true, vk: 0xB1},
	{name: "MediaStop", kind: med, scan: 0x24, ext: true, vk: 0xB2},

	// Mouse keys (ADR-0062): no scan code and no VK. The buttons come after
	// the wheel keys, which keep their values.
	{name: "WheelUp", kind: msk, mouse: keys.WheelUp},
	{name: "WheelDown", kind: msk, mouse: keys.WheelDown},
	{name: "WheelLeft", kind: msk, mouse: keys.WheelLeft},
	{name: "WheelRight", kind: msk, mouse: keys.WheelRight},
	{name: "MouseLeft", kind: msk, mouse: keys.MouseLeft},
	{name: "MouseRight", kind: msk, mouse: keys.MouseRight},
	{name: "MouseMiddle", kind: msk, mouse: keys.MouseMiddle},
	{name: "MouseX1", kind: msk, mouse: keys.MouseX1},
	{name: "MouseX2", kind: msk, mouse: keys.MouseX2},
}

func TestTablePinned(t *testing.T) {
	got := keys.Table()
	if len(got) != len(wantTable) {
		t.Fatalf("table has %d keys, want %d", len(got), len(wantTable))
	}
	for i, w := range wantTable {
		k := got[i]
		want := keys.USMapping{Scan: w.scan, Ext: w.ext, E1: w.e1, VK: w.vk, Shift: w.shift}
		if k.String() != w.name || k.Kind() != w.kind || k.US() != want {
			t.Errorf("entry %d = %s (%v) %+v, want %s (%v) %+v", i, k, k.Kind(), k.US(), w.name, w.kind, want)
		}
		m, ok := k.Mouse()
		if m != w.mouse || ok != (w.kind == msk) {
			t.Errorf("entry %d = %s: Mouse() = %v, %v, want %v, %v", i, k, m, ok, w.mouse, w.kind == msk)
		}
	}
}

// The Mouse values are stable: the buttons were appended behind the wheel
// keys, and nothing may renumber them.
func TestMouseValues(t *testing.T) {
	for i, m := range []keys.Mouse{keys.MouseNone, keys.WheelUp, keys.WheelDown, keys.WheelLeft, keys.WheelRight,
		keys.MouseLeft, keys.MouseRight, keys.MouseMiddle, keys.MouseX1, keys.MouseX2} {
		if int(m) != i {
			t.Errorf("%q = %d, want %d", m, m, i)
		}
		if wheel, button := i >= 1 && i <= 4, i >= 5; m.IsWheel() != wheel || m.IsButton() != button || m.IsPointer() {
			t.Errorf("%q: IsWheel %v, IsButton %v, IsPointer %v, want %v, %v, false", m, m.IsWheel(), m.IsButton(), m.IsPointer(), wheel, button)
		}
	}
}

// MouseMove and MouseJump are appended behind the buttons and are not keys: no
// table row has them, and they do not parse as a key name, alone or in a chord
// (ADR-0062, section 5).
func TestPointerMouseValues(t *testing.T) {
	for i, m := range []keys.Mouse{keys.MouseMove, keys.MouseJump} {
		if int(m) != int(keys.MouseX2)+1+i {
			t.Errorf("%q = %d, want %d", m, m, int(keys.MouseX2)+1+i)
		}
		if !m.IsPointer() || m.IsWheel() || m.IsButton() {
			t.Errorf("%q: IsPointer %v, IsWheel %v, IsButton %v, want true, false, false", m, m.IsPointer(), m.IsWheel(), m.IsButton())
		}
		for _, spec := range []string{m.String(), strings.ToLower(m.String()), "Ctrl+" + m.String()} {
			if c, err := keys.Parse(spec); err == nil {
				t.Errorf("Parse(%q) = %v, want an error: %q is not a key", spec, c, m)
			}
		}
		for _, k := range keys.Table() {
			if got, ok := k.Mouse(); ok && got == m {
				t.Errorf("table row %s has the pointer input %q", k, m)
			}
		}
	}
	if keys.MouseJump.String() != "MouseJump" || keys.MouseMove.String() != "MouseMove" {
		t.Errorf("names = %q, %q", keys.MouseMove, keys.MouseJump)
	}
}

// The mouse rows come last: the indices of the keyboard keys never move, and
// a mouse key is no keyboard key. The wheel keys have no aliases; the buttons
// each have the Windows name (LButton, ...).
func TestMouseKeys(t *testing.T) {
	tbl := keys.Table()
	seenMouse := false
	wantAliases := map[string]string{"MouseLeft": "LButton", "MouseRight": "RButton", "MouseMiddle": "MButton", "MouseX1": "XButton1", "MouseX2": "XButton2"}
	for _, k := range tbl {
		if k.Kind() == keys.KindMouse {
			seenMouse = true
			m, ok := k.Mouse()
			var aliases []string
			if a, button := wantAliases[k.String()]; button {
				aliases = []string{a}
			}
			if k.US() != (keys.USMapping{}) || k.IsModifier() || k.Mod() != 0 || !slices.Equal(k.Aliases(), aliases) {
				t.Errorf("%s: US %+v, modifier %v, mod %v, aliases %v, want a bare mouse key with aliases %v", k, k.US(), k.IsModifier(), k.Mod(), k.Aliases(), aliases)
			}
			if r, ok := k.Char(); ok || r != 0 {
				t.Errorf("%s: Char() = %q, %v, want 0, false", k, r, ok)
			}
			if !ok || m.IsWheel() == m.IsButton() || m.String() != k.String() || k.IsMouseButton() != m.IsButton() {
				t.Errorf("%s: Mouse() = %v (%q), %v, IsMouseButton %v, want its own wheel or button input", k, m, m, ok, k.IsMouseButton())
			}
		} else if seenMouse {
			t.Errorf("%s (%v) comes after a mouse key; the mouse rows come last", k, k.Kind())
		}
	}
	if !seenMouse {
		t.Fatal("the table has no mouse key")
	}

	// Case folds like any name; the buttons are found by their aliases, which
	// name the same key.
	for spelling, want := range map[string]string{
		"WheelUp": "WheelUp", "wheelup": "WheelUp", "WHEELUP": "WheelUp", "wheelDOWN": "WheelDown", "WheelLeft": "WheelLeft", "wheelright": "WheelRight",
		"MouseLeft": "MouseLeft", "mouseleft": "MouseLeft", "LButton": "MouseLeft", "lbutton": "MouseLeft",
		"MouseRight": "MouseRight", "RBUTTON": "MouseRight", "MouseMiddle": "MouseMiddle", "mbutton": "MouseMiddle",
		"MouseX1": "MouseX1", "XButton1": "MouseX1", "xbutton1": "MouseX1", "MouseX2": "MouseX2", "XBUTTON2": "MouseX2",
	} {
		k, ok := keys.Lookup(spelling)
		if !ok || k.Kind() != keys.KindMouse || k.String() != want {
			t.Errorf("Lookup(%q) = %v, %v, want the mouse key %s", spelling, k, ok, want)
		}
	}
	for _, name := range []string{"Wheel", "ScrollUp", "MouseWheelUp", "WheelUp ", "Wheel Up", "Mouse", "Mouse1", "MouseButton1", "LeftClick", "Click",
		"XButton", "XButton3", "MouseX3", "Mouse Left", "LMB"} {
		if k, ok := keys.Lookup(name); ok {
			t.Errorf("Lookup(%q) = %v, want failure: no such mouse key or alias", name, k)
		}
	}

	// MouseNone, and values that name no input, are neither wheels nor buttons.
	for _, m := range []keys.Mouse{keys.MouseNone, keys.Mouse(200)} {
		if m.IsWheel() || m.IsButton() || m.String() != "" {
			t.Errorf("Mouse(%d): IsWheel %v, IsButton %v, String %q, want false and empty", m, m.IsWheel(), m.IsButton(), m.String())
		}
	}
	// A keyboard key and the zero Key are not mouse keys.
	for _, k := range []keys.Key{{}, keys.MustParse("Z").Key(), keys.MustParse("Ctrl").Key(), keys.MustParse("sc:0x2C").Key()} {
		if m, ok := k.Mouse(); ok || m != keys.MouseNone || k.IsMouseButton() {
			t.Errorf("Mouse() of %q = %v, %v, IsMouseButton %v, want MouseNone, false, false", k, m, ok, k.IsMouseButton())
		}
	}
	// The wheel keys are no buttons.
	if keys.MustParse("WheelUp").Key().IsMouseButton() || !keys.MustParse("Ctrl+MButton").Key().IsMouseButton() {
		t.Error("IsMouseButton: a wheel key is no button, and MButton is")
	}
}

// The media and volume keys (ADR-0070, OUT-20) are keyboard keys of their own
// kind. They sit behind the character and physical keys and before the mouse
// keys, so that no keyboard key moves; each has an E0 scan code and the VK of
// its VK_VOLUME_* or VK_MEDIA_* constant, and the W3C name as an alias.
func TestMediaKeys(t *testing.T) {
	want := []struct {
		name    string
		scan    string
		vk      uint16
		aliases []string
	}{
		{"VolumeUp", "E0 30", 0xAF, []string{"AudioVolumeUp"}},
		{"VolumeDown", "E0 2E", 0xAE, []string{"AudioVolumeDown"}},
		{"VolumeMute", "E0 20", 0xAD, []string{"AudioVolumeMute"}},
		{"MediaPlayPause", "E0 22", 0xB3, nil},
		{"MediaNext", "E0 19", 0xB0, []string{"MediaTrackNext"}},
		{"MediaPrevious", "E0 10", 0xB1, []string{"MediaTrackPrevious"}},
		{"MediaStop", "E0 24", 0xB2, nil},
	}
	var media []keys.Key
	last := keys.KindInvalid
	for _, k := range keys.Table() {
		if k.Kind() == keys.KindMedia {
			if last != keys.KindPhysical && last != keys.KindMedia {
				t.Errorf("%s follows a %v key, want the physical keys or another media key", k, last)
			}
			media = append(media, k)
		} else if last == keys.KindMedia && k.Kind() != keys.KindMouse {
			t.Errorf("%s (%v) follows a media key, want a mouse key", k, k.Kind())
		}
		last = k.Kind()
	}
	if len(media) != len(want) {
		t.Fatalf("the table has %d media keys, want %d", len(media), len(want))
	}
	for i, w := range want {
		k := media[i]
		if k.String() != w.name || k.Kind() != keys.KindMedia || k.IsModifier() || k.Mod() != 0 || !k.IsValid() {
			t.Errorf("media key %d = %s (%v), want %s", i, k, k.Kind(), w.name)
		}
		us := k.US()
		if us.ScanString() != w.scan || us.VK != w.vk || !us.Ext || us.E1 || us.Shift {
			t.Errorf("%s: US %+v (%s), want scan %s and VK %#x", k, us, us.ScanString(), w.scan, w.vk)
		}
		if !slices.Equal(k.Aliases(), w.aliases) {
			t.Errorf("%s: aliases %v, want %v", k, k.Aliases(), w.aliases)
		}
		if r, ok := k.Char(); ok || r != 0 {
			t.Errorf("%s: Char() = %q, %v, want 0, false", k, r, ok)
		}
		if m, ok := k.Mouse(); ok || m != keys.MouseNone || k.IsMouseButton() {
			t.Errorf("%s is a mouse key: %v, %v", k, m, ok)
		}
		// Every spelling, and every alias in any case, finds the same key.
		for _, spelling := range append([]string{w.name, strings.ToLower(w.name), strings.ToUpper(w.name)}, w.aliases...) {
			if got, ok := keys.Lookup(spelling); !ok || got != k {
				t.Errorf("Lookup(%q) = %v, %v, want %s", spelling, got, ok, k)
			}
		}
		// A raw scan code is another key, even though it presses the same
		// physical key on a keyboard: it has no VK.
		raw := keys.MustParse("sc:0xE0" + strings.Fields(w.scan)[1]).Key()
		if raw == k || raw.Kind() != keys.KindScan {
			t.Errorf("sc:0xE0%s = %v (%v), want a scan key distinct from %s", strings.Fields(w.scan)[1], raw, raw.Kind(), k)
		}
	}

	// Modifiers go before a media key like before any key, and the key is
	// never a modifier.
	for spec, canonical := range map[string]string{
		"volumeup":                      "VolumeUp",
		"ctrl+shift+VolumeDown":         "Ctrl+Shift+VolumeDown",
		"shift + ctrl + mediaplaypause": "Ctrl+Shift+MediaPlayPause",
		"LAlt+AudioVolumeMute":          "LAlt+VolumeMute",
		"Win+MediaTrackNext":            "Win+MediaNext",
		"RCtrl+MediaTrackPrevious":      "RCtrl+MediaPrevious",
	} {
		c, err := keys.Parse(spec)
		if err != nil {
			t.Errorf("Parse(%q): %v", spec, err)
			continue
		}
		if c.String() != canonical || c.Key().Kind() != keys.KindMedia {
			t.Errorf("Parse(%q) = %q (key kind %v), want %q", spec, c, c.Key().Kind(), canonical)
		}
		if again, err := keys.Parse(c.String()); err != nil || again != c {
			t.Errorf("Parse(%q) = %v, %v, want %v", c, again, err, c)
		}
	}
	if got := keys.MustParse("Ctrl+VolumeUp").Sided().String(); got != "LCtrl+VolumeUp" {
		t.Errorf("Sided() = %q, want LCtrl+VolumeUp", got)
	}
	if _, err := keys.Parse("VolumeUp+Ctrl"); err == nil || !strings.Contains(err.Error(), `"VolumeUp" is not a modifier`) {
		t.Errorf("Parse(VolumeUp+Ctrl) error = %v, want VolumeUp is not a modifier", err)
	}
	seq, err := keys.ParseSequence([]string{"VolumeUp", "Ctrl+Z", "MediaNext"})
	if err != nil || len(seq) != 3 || seq[0].Key().Kind() != keys.KindMedia || seq[2].Key().Kind() != keys.KindMedia {
		t.Errorf("ParseSequence = %v, %v", seq, err)
	}

	// No other names: the aliases are the W3C ones only.
	for _, name := range []string{"Volume", "Mute", "VolUp", "PlayPause", "Play", "Next", "Prev", "MediaPrev", "MediaTrack",
		"MediaPlay", "MediaPause", "Stop", "AudioVolume", "VolumeToggle", "VolumeUp ", "Volume Up", "VK_VOLUME_UP"} {
		if k, ok := keys.Lookup(name); ok {
			t.Errorf("Lookup(%q) = %v, want failure", name, k)
		}
	}
	if _, err := keys.Parse("Volumeup2"); err == nil || !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("Parse(Volumeup2) error = %v, want a suggestion", err)
	}
}

func TestFunctionKeys(t *testing.T) {
	// OUT-03: F13-F21 are 0x64-0x6C, F22 0x6D, F23 0x6E, F24 0x76;
	// VK_F13..VK_F24 are 0x7C..0x87. None is E0-extended.
	scans := []uint8{0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6A, 0x6B, 0x6C, 0x6D, 0x6E, 0x76}
	for i, scan := range scans {
		n := 13 + i
		name := fmt.Sprintf("F%d", n)
		k, ok := keys.Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) failed", name)
		}
		want := keys.USMapping{Scan: scan, VK: uint16(0x7C + i)}
		if k.US() != want || k.Kind() != keys.KindNamed {
			t.Errorf("%s: %v %+v, want named %+v", name, k.Kind(), k.US(), want)
		}
	}
}

func TestSpecialScanCodes(t *testing.T) {
	tests := []struct {
		name string
		scan string
	}{
		{"PrintScreen", "E0 37"},
		{"Pause", "E1 1D 45"},
		{"NumLock", "0x45"},
		{"NumDivide", "E0 35"},
		{"NumEnter", "E0 1C"},
		{"RCtrl", "E0 1D"},
		{"Z", "0x2C"},
	}
	for _, tt := range tests {
		k, ok := keys.Lookup(tt.name)
		if !ok {
			t.Fatalf("Lookup(%q) failed", tt.name)
		}
		if got := k.US().ScanString(); got != tt.scan {
			t.Errorf("%s: ScanString() = %q, want %q", tt.name, got, tt.scan)
		}
	}
}

func TestTableRoundTrip(t *testing.T) {
	for _, k := range keys.Table() {
		name := k.String()
		for _, spelling := range []string{name, strings.ToLower(name), strings.ToUpper(name)} {
			c, err := keys.Parse(spelling)
			if err != nil {
				t.Errorf("Parse(%q): %v", spelling, err)
				continue
			}
			if c.String() != name || c.Key() != k || c.Mods() != 0 {
				t.Errorf("Parse(%q) = %q (key %v, mods %v), want %q", spelling, c, c.Key(), c.Mods(), name)
			}
		}
		if got, ok := keys.Lookup(name); !ok || got != k {
			t.Errorf("Lookup(%q) = %v, %v", name, got, ok)
		}
		for _, alias := range k.Aliases() {
			for _, spelling := range []string{alias, strings.ToLower(alias), strings.ToUpper(alias)} {
				if got, ok := keys.Lookup(spelling); !ok || got != k {
					t.Errorf("alias Lookup(%q) = %v, %v, want %v", spelling, got, ok, k)
				}
				if c, err := keys.Parse(spelling); err != nil || c.String() != name {
					t.Errorf("alias Parse(%q) = %q, %v, want %q", spelling, c, err, name)
				}
			}
		}
	}
}

func TestTableUnique(t *testing.T) {
	// Names and aliases are unique case-insensitively, across all kinds.
	owner := map[string]string{}
	for _, k := range keys.Table() {
		for _, s := range append([]string{k.String()}, k.Aliases()...) {
			folded := strings.ToLower(s)
			if prev, dup := owner[folded]; dup {
				t.Errorf("%q of %s collides with %s", s, k, prev)
			}
			owner[folded] = k.String()
		}
	}

	// Within each kind, US mappings are unique, except that a generic
	// modifier shares the mapping of its left key, Plus shares "=" with
	// an implied Shift, and the mouse keys, which are no keyboard keys, all
	// have the zero mapping.
	generic := map[string]bool{"Ctrl": true, "Shift": true, "Alt": true, "Win": true}
	type slot struct {
		kind keys.Kind
		us   keys.USMapping
	}
	seen := map[slot]string{}
	for _, k := range keys.Table() {
		if generic[k.String()] || k.Kind() == keys.KindMouse {
			continue
		}
		s := slot{k.Kind(), k.US()}
		s.us.VK = 0 // VKs may repeat (Enter and NumEnter are both VK_RETURN)
		if prev, dup := seen[s]; dup {
			t.Errorf("%s and %s share scan code %s", k, prev, k.US().ScanString())
		}
		seen[s] = k.String()
	}

	// Named keys and modifiers never share a scan code with each other or
	// with the main block.
	type pos struct {
		scan    uint8
		ext, e1 bool
	}
	byPos := map[pos]string{}
	for _, k := range keys.Table() {
		if generic[k.String()] || k.Kind() == keys.KindPhysical || k.Kind() == keys.KindMouse || k.String() == "Plus" {
			continue
		}
		us := k.US()
		p := pos{us.Scan, us.Ext, us.E1}
		if prev, dup := byPos[p]; dup {
			t.Errorf("%s and %s share scan code %s", k, prev, us.ScanString())
		}
		byPos[p] = k.String()
	}
}

func TestPhysicalMatchesChar(t *testing.T) {
	chars := map[string]keys.Key{}
	for _, k := range keys.Table() {
		if k.Kind() == keys.KindChar && k.String() != "Plus" {
			chars[k.String()] = k
		}
	}
	physical := 0
	for _, k := range keys.Table() {
		if k.Kind() != keys.KindPhysical {
			continue
		}
		physical++
		c, ok := chars[strings.TrimPrefix(k.String(), "Key")]
		if !ok {
			t.Errorf("%s has no character key", k)
			continue
		}
		if c.US() != k.US() {
			t.Errorf("%s maps to %+v, but %s maps to %+v", k, k.US(), c, c.US())
		}
		if c == k {
			t.Errorf("%s and %s must be distinct keys", k, c)
		}
	}
	if physical != len(chars) {
		t.Errorf("%d physical keys for %d character keys", physical, len(chars))
	}
}

func TestW3CAliases(t *testing.T) {
	tests := map[string]string{
		"Digit1": "Key1", "Minus": "Key-", "Equal": "Key=", "BracketLeft": "Key[", "BracketRight": "Key]",
		"Semicolon": "Key;", "Quote": "Key'", "Backquote": "Key`", "Backslash": "Key\\", "Comma": "Key,",
		"Period": "Key.", "Slash": "Key/", "Escape": "Esc", "Return": "Enter", "ArrowUp": "Up",
		"Numpad0": "Num0", "NumpadAdd": "NumAdd", "NumpadEnter": "NumEnter", "ContextMenu": "Menu",
		"Apps": "Menu", "ControlLeft": "LCtrl", "ShiftLeft": "LShift", "AltLeft": "LAlt", "MetaLeft": "LWin",
		"Meta": "Win", "Control": "Ctrl", "Del": "Delete", "Ins": "Insert", "PgUp": "PageUp", "PgDn": "PageDown",
		"AltGr": "RAlt", "PrtSc": "PrintScreen",
	}
	for alias, want := range tests {
		k, ok := keys.Lookup(alias)
		if !ok || k.String() != want {
			t.Errorf("Lookup(%q) = %q, %v, want %q", alias, k, ok, want)
		}
	}
}

func TestChar(t *testing.T) {
	// Every character key types its own character, lower case for letters,
	// in table order. The layout resolver (M1b) looks keys up by this rune.
	const want = "abcdefghijklmnopqrstuvwxyz0123456789-=[];'`\\,./+"
	var got []rune
	for _, k := range keys.Table() {
		r, ok := k.Char()
		if ok != (k.Kind() == keys.KindChar) {
			t.Errorf("%s (%v): Char() ok = %v", k, k.Kind(), ok)
		}
		if ok {
			got = append(got, r)
		} else if r != 0 {
			t.Errorf("%s: Char() = %q, want 0 with ok false", k, r)
		}
	}
	if string(got) != want {
		t.Errorf("character keys type %q, want %q", string(got), want)
	}

	tests := []struct {
		spec string
		char rune
		ok   bool
	}{
		{"Ctrl+Z", 'z', true},
		{"Ctrl+Shift+Z", 'z', true},
		{"z", 'z', true},
		{"Plus", '+', true},
		{"Shift+=", '=', true},
		{"1", '1', true},
		{"KeyZ", 0, false},
		{"sc:0x2C", 0, false},
		{"F13", 0, false},
		{"Shift", 0, false},
		{"WheelUp", 0, false},
		{"Ctrl+WheelRight", 0, false},
	}
	for _, tt := range tests {
		r, ok := keys.MustParse(tt.spec).Key().Char()
		if r != tt.char || ok != tt.ok {
			t.Errorf("Char() of %q = %q, %v, want %q, %v", tt.spec, r, ok, tt.char, tt.ok)
		}
	}
	if r, ok := (keys.Key{}).Char(); r != 0 || ok {
		t.Errorf("Char() of the zero Key = %q, %v, want 0, false", r, ok)
	}
}
