package csp

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func TestParseCSPKey(t *testing.T) {
	tests := []struct {
		name string
		mod  int
		want string // KeySpec, "" when not converted
		st   KeyStatus
	}{
		{"Z", 4, "Ctrl+Z", KeyOK},
		{"z", 4, "Ctrl+Z", KeyOK},
		{"A", 7, "Alt+Shift+Ctrl+A", KeyOK},
		{"A", 1, "Alt+A", KeyOK},
		{"7", 0, "7", KeyOK},
		{"F1", 0, "F1", KeyOK},
		{"F24", 2, "Shift+F24", KeyOK},
		{"NUM0", 0, "Num0", KeyOK},
		{"NUM+", 0, "NumAdd", KeyOK},
		{"NUM-", 0, "NumSubtract", KeyOK},
		{"NUM*", 0, "NumMultiply", KeyOK},
		{"NUM/", 0, "NumDivide", KeyOK},
		{"NUM.", 0, "NumDecimal", KeyOK},
		{"TAB", 0, "Tab", KeyOK},
		{"SPACE", 1, "Alt+Space", KeyOK},
		{"BACKSPACE", 0, "Backspace", KeyOK},
		{"DELETE", 0, "Delete", KeyOK},
		{"ESC", 0, "Esc", KeyOK},
		{"HOME", 0, "Home", KeyOK},
		{"END", 0, "End", KeyOK},
		{"PAGEUP", 0, "PageUp", KeyOK},
		{"PAGEDOWN", 0, "PageDown", KeyOK},
		{"LEFT", 0, "Left", KeyOK},
		{"RIGHT", 0, "Right", KeyOK},
		{"UP", 0, "Up", KeyOK},
		{"DOWN", 0, "Down", KeyOK},
		{"INSERT", 0, "Insert", KeyOK},
		{"[", 0, "", KeyUnconfirmed},
		{"\\", 4, "", KeyUnconfirmed},
		{"-", 0, "", KeyUnconfirmed},
		{"HELP", 0, "", KeyUnsupported},
		{"CLEAR", 0, "", KeyUnsupported},
		{"NUMCLEAR", 0, "", KeyUnsupported},
		{"NUM=", 0, "", KeyUnsupported},
		{"NUM,", 0, "", KeyUnsupported},
		{"YEN", 0, "", KeyUnsupported},
		{"T30", 0, "", KeyUnsupported},
		{"S2", 0, "", KeyUnsupported},
		{"E1", 0, "", KeyUnsupported},
		{"GAMEPADBUTTON8", 0, "", KeyUnsupported},
		{"Z", 8, "", KeyUnsupported},
		{"Z", -1, "", KeyUnsupported},
		{"NULL", 0, "", KeyNone},
		{"BOGUS", 0, "", KeyUnknown},
		{"", 0, "", KeyUnknown},
		{"T31", 0, "", KeyUnknown},
	}
	for _, tt := range tests {
		c, st := ParseCSPKey(tt.name, tt.mod)
		if st != tt.st {
			t.Errorf("ParseCSPKey(%q,%d) status = %v, want %v", tt.name, tt.mod, st, tt.st)
			continue
		}
		if tt.want == "" {
			if c.IsValid() {
				t.Errorf("ParseCSPKey(%q,%d) returned a chord %v", tt.name, tt.mod, c)
			}
			continue
		}
		if want := keys.MustParse(tt.want); c != want {
			t.Errorf("ParseCSPKey(%q,%d) = %v, want %v", tt.name, tt.mod, c, want)
		}
	}
}

func TestFormatCSPKey(t *testing.T) {
	tests := []struct {
		spec string
		name string
		mod  int
		st   KeyStatus
	}{
		{"Ctrl+Z", "Z", 4, KeyOK},
		{"LCtrl+RShift+Z", "Z", 6, KeyOK},
		{"Alt+Shift+Ctrl+F13", "F13", 7, KeyOK},
		{"NumAdd", "NUM+", 0, KeyOK},
		{"Esc", "ESC", 0, KeyOK},
		{"Win+Z", "", 0, KeyUnsupported},
		{"Ctrl+Shift", "", 0, KeyUnsupported},
		{"Enter", "", 0, KeyUnsupported},
		{"KeyZ", "", 0, KeyUnsupported},
		{"MouseLeft", "", 0, KeyUnsupported},
	}
	for _, tt := range tests {
		name, mod, st := FormatCSPKey(keys.MustParse(tt.spec))
		if name != tt.name || mod != tt.mod || st != tt.st {
			t.Errorf("FormatCSPKey(%s) = %q,%d,%v want %q,%d,%v", tt.spec, name, mod, st, tt.name, tt.mod, tt.st)
		}
	}
	if _, _, st := FormatCSPKey(keys.Chord{}); st != KeyUnknown {
		t.Errorf("zero chord status = %v", st)
	}
}

func TestRoundTripAllSupported(t *testing.T) {
	for name := range supportedKeys {
		for mod := 0; mod <= modMask; mod++ {
			c, st := ParseCSPKey(name, mod)
			if st != KeyOK {
				t.Fatalf("%s/%d: %v", name, mod, st)
			}
			n2, m2, st := FormatCSPKey(c)
			if st != KeyOK || n2 != name || m2 != mod {
				t.Fatalf("%s/%d round trip = %q,%d,%v", name, mod, n2, m2, st)
			}
		}
	}
}

func TestToolKey(t *testing.T) {
	for code := 1; code <= 26; code++ {
		c, ok := ToolKeyChord(code)
		if !ok {
			t.Fatalf("ToolKeyChord(%d)", code)
		}
		if back, ok := ToolKeyCode(c); !ok || back != code {
			t.Fatalf("ToolKeyCode(%v) = %d,%v", c, back, ok)
		}
	}
	if c, _ := ToolKeyChord(1); c != keys.MustParse("A") {
		t.Errorf("code 1 = %v", c)
	}
	if c, _ := ToolKeyChord(26); c != keys.MustParse("Z") {
		t.Errorf("code 26 = %v", c)
	}
	for _, code := range []int{0, 27, -1} {
		if _, ok := ToolKeyChord(code); ok {
			t.Errorf("ToolKeyChord(%d) ok", code)
		}
	}
	for _, spec := range []string{"Ctrl+A", "1", "F1", "Shift+A"} {
		if _, ok := ToolKeyCode(keys.MustParse(spec)); ok {
			t.Errorf("ToolKeyCode(%s) ok", spec)
		}
	}
}

func FuzzParseCSPKey(f *testing.F) {
	for _, s := range []string{"Z", "NUM+", "[", "NULL", "T1", "f12", "GAMEPADBUTTON3", "\\", "", "\xff"} {
		f.Add(s, 4)
	}
	f.Add("A", 8)
	f.Add("A", -1)
	f.Fuzz(func(t *testing.T, name string, mod int) {
		c, st := ParseCSPKey(name, mod)
		if (st == KeyOK) != c.IsValid() {
			t.Fatalf("status %v with chord %v", st, c)
		}
		if st != KeyOK {
			return
		}
		n2, m2, st2 := FormatCSPKey(c)
		if st2 != KeyOK || m2 != mod || n2 != strings.ToUpper(name) {
			t.Fatalf("round trip of %q/%d = %q,%d,%v", name, mod, n2, m2, st2)
		}
	})
}
