package amgp

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// spec returns the KeySpec a code stands for, "" for a code with none.
func spec(code int) string {
	k := qtKeyFor(code)
	if k.drop != "" {
		return ""
	}
	return strings.Join(append(append([]string{}, k.mods...), k.name), "+")
}

func TestQtKeyTable(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		// Letters, digits, space and the characters of the main block.
		{'A', "A"}, {'Z', "Z"}, {'0', "0"}, {'9', "9"}, {0x20, "Space"},
		{0x2c, ","}, {0x2d, "-"}, {0x2e, "."}, {0x2f, "/"}, {0x3b, ";"}, {0x3d, "="},
		{0x5b, "["}, {0x5c, "\\"}, {0x5d, "]"}, {0x27, "'"}, {0x60, "`"}, {0x2b, "Plus"},
		// Characters that need Shift on a US keyboard.
		{'!', "Shift+1"}, {'@', "Shift+2"}, {'#', "Shift+3"}, {'$', "Shift+4"}, {'%', "Shift+5"},
		{'^', "Shift+6"}, {'&', "Shift+7"}, {'*', "Shift+8"}, {'(', "Shift+9"}, {')', "Shift+0"},
		{'_', "Shift+-"}, {':', "Shift+;"}, {'"', "Shift+'"}, {'<', "Shift+,"}, {'>', "Shift+."},
		{'?', "Shift+/"}, {'{', "Shift+["}, {'}', "Shift+]"}, {'|', "Shift+\\"}, {'~', "Shift+`"},
		// Named keys.
		{0x01000000, "Esc"}, {0x01000001, "Tab"}, {0x01000002, "Shift+Tab"}, {0x01000003, "Backspace"},
		{0x01000004, "Enter"}, {0x01000005, "Enter"}, {0x01000006, "Insert"}, {0x01000007, "Delete"},
		{0x01000008, "Pause"}, {0x01000009, "PrintScreen"},
		{0x01000010, "Home"}, {0x01000011, "End"}, {0x01000012, "Left"}, {0x01000013, "Up"},
		{0x01000014, "Right"}, {0x01000015, "Down"}, {0x01000016, "PageUp"}, {0x01000017, "PageDown"},
		{0x01000024, "CapsLock"}, {0x01000025, "NumLock"}, {0x01000026, "ScrollLock"}, {0x01000055, "Menu"},
		// Modifiers, which AntiMicroX writes for the left-hand key, and the
		// right-hand ones with the custom prefix.
		{0x01000020, "LShift"}, {0x01000021, "LCtrl"}, {0x01000022, "LWin"}, {0x01000023, "LAlt"},
		{0x11000020, "RShift"}, {0x11000021, "RCtrl"}, {0x11000022, "RWin"}, {0x11000023, "RAlt"},
		{0x01000053, "LWin"}, {0x01000054, "RWin"},
		// Function keys.
		{0x01000030, "F1"}, {0x0100003b, "F12"}, {0x01000047, "F24"},
		// The keypad.
		{0x2000ffb0, "Num0"}, {0x2000ffb9, "Num9"}, {0x2000ff8d, "NumEnter"}, {0x2000ff9f, "NumDecimal"},
		{0x1000002a, "NumMultiply"}, {0x1000002b, "NumAdd"}, {0x1000002d, "NumSubtract"},
		{0x1000002e, "NumDecimal"}, {0x1000002f, "NumDivide"},
		{0x11000006, "Num0"}, {0x11000007, "NumDecimal"}, {0x1100000b, "Num5"},
		{0x11000010, "Num7"}, {0x11000011, "Num1"}, {0x11000012, "Num4"}, {0x11000013, "Num8"},
		{0x11000014, "Num6"}, {0x11000015, "Num2"}, {0x11000016, "Num9"}, {0x11000017, "Num3"},
		// Volume and media.
		{0x01000070, "VolumeDown"}, {0x01000071, "VolumeMute"}, {0x01000072, "VolumeUp"},
		{0x01000080, "MediaPlayPause"}, {0x01000081, "MediaStop"}, {0x01000082, "MediaPrevious"},
		{0x01000083, "MediaNext"}, {0x01000086, "MediaPlayPause"},
		// No padmate key.
		{0x01000048, ""}, {0x01000052, ""}, {0x0100000a, ""}, {0x0100000b, ""}, {0x01000058, ""},
		{0x01001103, ""}, {0x01000061, ""}, {0x01000092, ""}, {0x010000a0, ""}, {0x01000085, ""},
		{0x60000041, ""}, {0x1234567, ""}, {0x61, ""}, {0x7f, ""}, {0x41 | 0x80, ""},
	}
	for _, tc := range tests {
		if got := spec(tc.code); got != tc.want {
			t.Errorf("code 0x%x: got %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestQtKeyFlags(t *testing.T) {
	for _, code := range []int{0x01000020, 0x11000023, 0x01000053} {
		if k := qtKeyFor(code); !k.modifier {
			t.Errorf("code 0x%x should be a modifier", code)
		}
	}
	for _, code := range []int{'A', 0x01000001, 0x2000ffb0} {
		if k := qtKeyFor(code); k.modifier {
			t.Errorf("code 0x%x should not be a modifier", code)
		}
	}
	// A shifted character and a keypad key with NumLock off are approximate;
	// a plain letter is not.
	if qtKeyFor('!').approx == "" || qtKeyFor(0x11000013).approx == "" {
		t.Error("shifted characters and navigation keypad keys should say they are approximate")
	}
	if qtKeyFor('A').approx != "" || qtKeyFor(0x01000030).approx != "" {
		t.Error("letters and function keys are exact")
	}
}

// Every code that has a key names one that KeySpec reads, on its own and
// with the modifiers it adds, and every code that has none says why.
func TestQtKeyAlwaysReadable(t *testing.T) {
	ranges := [][2]int{
		{0, 0x300}, {0x01000000, 0x01000200}, {0x01001100, 0x01001200}, {0x01020000, 0x01020010},
		{0x11000000, 0x11000100}, {0x1000002a, 0x10000030}, {0x2000ff00, 0x2000ffff}, {0x60000000, 0x60000100},
	}
	for _, r := range ranges {
		for code := r[0]; code < r[1]; code++ {
			k := qtKeyFor(code)
			if k.drop != "" {
				if k.name != "" || len(k.mods) > 0 {
					t.Errorf("code 0x%x is dropped but has a name", code)
				}
				continue
			}
			if _, err := keys.Parse(spec(code)); err != nil {
				t.Errorf("code 0x%x: %q is not a KeySpec: %v", code, spec(code), err)
			}
		}
	}
}

func TestMouseButtonNames(t *testing.T) {
	// The order is AntiMicroX's: 2 is the middle button and 3 the right.
	want := map[int]string{
		1: "MouseLeft", 2: "MouseMiddle", 3: "MouseRight", 4: "WheelUp", 5: "WheelDown",
		6: "WheelLeft", 7: "WheelRight", 8: "MouseX1", 9: "MouseX2",
	}
	for code, name := range want {
		if mouseButtonNames[code] != name {
			t.Errorf("mouse button %d: got %q, want %q", code, mouseButtonNames[code], name)
		}
		if _, err := keys.Parse(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for code := range mouseButtonNames {
		if _, ok := want[code]; !ok {
			t.Errorf("unexpected mouse button %d", code)
		}
	}
	if w, h := isWheel(5); !w || h {
		t.Error("code 5 is a vertical wheel")
	}
	if w, h := isWheel(6); !w || !h {
		t.Error("code 6 is a horizontal wheel")
	}
	if w, _ := isWheel(1); w {
		t.Error("code 1 is not a wheel")
	}
}
