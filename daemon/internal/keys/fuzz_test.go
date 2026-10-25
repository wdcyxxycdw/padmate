package keys_test

import (
	"errors"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// FuzzParseChord checks that Parse never panics, that every chord it
// accepts formats to a spec that parses back to the same chord, that the
// canonical form is a fixed point, that Sided keeps it canonical, and that
// errors point inside the input.
func FuzzParseChord(f *testing.F) {
	for _, s := range []string{
		"", " ", "+", "++", "+++", "Ctrl+", "Ctrl++", "+Z",
		"ctrl+shift+z", "shift + ctrl + z", "Shift+Ctrl", "Win+Shift+Alt+Ctrl",
		"Ctrl+Plus", "Ctrl+,", "Ctrl+\\", "!", "Ctrl+?",
		"KeyZ", "Digit1", "BracketLeft", "sc:0x2c", "sc:0xe01d", "sc:0xE11D", "sc:0x", "SC:0X7f",
		"Ctrl+LCtrl+Z", "Ctrl+Ctrl", "LCtrl+RCtrl", "Z+Ctrl", "A+B",
		"ctrl-z", "Escpe", "F25", "numpad0", "Ctrl+é", "\tAlt\t+\tTab",
		"LCtrl + LAlt + LShift + RWin + KeyZ",
		"WheelUp", "wheeldown", "Ctrl+WheelUp", "Shift+Ctrl+WheelLeft", "WheelUp+Ctrl", "WheelUp+WheelDown", "WhelUp", "Ctrl+WheelRight+",
		"VolumeUp", "ctrl+VolumeDown", "Shift+AudioVolumeMute", "MediaPlayPause+Ctrl", "MediaTrackNext", "Ctrl+MediaStop+", "VolumeUpp",
		"MouseLeft", "lbutton", "Shift+MouseRight", "Ctrl+XButton1", "MouseMiddle+Ctrl", "MouseX1+MouseX2", "MousLeft", "Ctrl+MButton+",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := keys.Parse(s)
		if err != nil {
			var pe *keys.ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Parse(%q) error %T, want *keys.ParseError", s, err)
			}
			if pe.Input != s || pe.Offset < 0 || pe.Len < 0 || pe.Offset+pe.Len > len(s) {
				t.Fatalf("Parse(%q) error at %d+%d of %q is out of bounds", s, pe.Offset, pe.Len, pe.Input)
			}
			if pe.Msg == "" {
				t.Fatalf("Parse(%q) error has no message", s)
			}
			return
		}
		if !c.IsValid() {
			t.Fatalf("Parse(%q) returned an invalid chord", s)
		}
		canon := c.String()
		c2, err := keys.Parse(canon)
		if err != nil {
			t.Fatalf("Parse(%q) = %q, which does not parse: %v", s, canon, err)
		}
		if c2 != c {
			t.Fatalf("Parse(%q) = %q, which parses to a different chord %q", s, canon, c2)
		}
		if again := c2.String(); again != canon {
			t.Fatalf("String is not idempotent: %q -> %q", canon, again)
		}
		if c.Key().IsModifier() && c.Mods()&c.Key().Mod() != 0 {
			t.Fatalf("Parse(%q) = %q has its key in its modifier set", s, canon)
		}
		checkSided(t, c)
	})
}

// checkSided checks that c.Sided() is canonical, idempotent, free of
// generic modifiers, and presses the same US keys in the same order as c.
func checkSided(t *testing.T, c keys.Chord) {
	t.Helper()
	sided := c.Sided()
	if !sided.IsValid() {
		t.Fatalf("%q.Sided() is invalid", c)
	}
	if again := sided.Sided(); again != sided {
		t.Fatalf("Sided is not idempotent: %q -> %q -> %q", c, sided, again)
	}
	if back, err := keys.Parse(sided.String()); err != nil || back != sided {
		t.Fatalf("%q.Sided() = %q, which parses to %q, %v", c, sided, back, err)
	}
	if sided.Key().IsModifier() && sided.Mods()&sided.Key().Mod() != 0 {
		t.Fatalf("%q.Sided() = %q has its key in its modifier set", c, sided)
	}
	generic := keys.ModCtrl | keys.ModShift | keys.ModAlt | keys.ModWin
	if (sided.Mods()|sided.Key().Mod())&generic != 0 {
		t.Fatalf("%q.Sided() = %q has a generic modifier", c, sided)
	}
	ck, sk := c.Keys(), sided.Keys()
	if len(ck) != len(sk) {
		t.Fatalf("%q.Sided() = %q has %d keys, want %d", c, sided, len(sk), len(ck))
	}
	for i := range ck {
		if ck[i].US() != sk[i].US() {
			t.Fatalf("%q.Sided() = %q: key %d maps to %+v, want %+v", c, sided, i, sk[i].US(), ck[i].US())
		}
	}
}
