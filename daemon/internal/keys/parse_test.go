package keys_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		// Canonical names and case folding.
		{"ctrl+shift+z", "Ctrl+Shift+Z"},
		{"Ctrl+Shift+Z", "Ctrl+Shift+Z"},
		{"CTRL+SHIFT+Z", "Ctrl+Shift+Z"},
		{"z", "Z"},
		{"f13", "F13"},
		{"F24", "F24"},
		{"lshift", "LShift"},
		{"esc", "Esc"},

		// Spaces and tabs around tokens are dropped.
		{"shift + ctrl + z", "Ctrl+Shift+Z"},
		{"  Ctrl+Z  ", "Ctrl+Z"},
		{"\tAlt\t+\tTab\t", "Alt+Tab"},

		// Modifiers are sorted into canonical order.
		{"Shift+Ctrl+Z", "Ctrl+Shift+Z"},
		{"Win+Alt+Shift+Ctrl+Z", "Ctrl+Shift+Alt+Win+Z"},
		{"RWin+LWin+RAlt+LAlt+RShift+LShift+RCtrl+LCtrl+F1", "LCtrl+RCtrl+LShift+RShift+LAlt+RAlt+LWin+RWin+F1"},
		{"Alt+LCtrl+Z", "LCtrl+Alt+Z"},

		// Aliases format as canonical names.
		{"control+escape", "Ctrl+Esc"},
		{"ControlLeft+ArrowUp", "LCtrl+Up"},
		{"meta+return", "Win+Enter"},
		{"Apps", "Menu"},
		{"numpad0", "Num0"},
		{"Shift+Digit1", "Shift+Key1"},

		// Chords made only of modifiers are sets.
		{"Shift+Ctrl", "Ctrl+Shift"},
		{"Ctrl+Shift", "Ctrl+Shift"},
		{"Ctrl", "Ctrl"},
		{"rshift", "RShift"},
		{"Win+Shift+Alt+Ctrl", "Ctrl+Shift+Alt+Win"},
		{"RCtrl+LCtrl", "LCtrl+RCtrl"},

		// Characters, Plus and punctuation. "," is a plain key.
		{"Ctrl+Plus", "Ctrl+Plus"},
		{"ctrl+plus", "Ctrl+Plus"},
		{"Ctrl+=", "Ctrl+="},
		{"Ctrl+-", "Ctrl+-"},
		{",", ","},
		{"Ctrl+,", "Ctrl+,"},
		{"Ctrl+\\", "Ctrl+\\"},
		{"Ctrl+[", "Ctrl+["},
		{"Ctrl+`", "Ctrl+`"},
		{"Ctrl+'", "Ctrl+'"},

		// Physical keys.
		{"keyz", "KeyZ"},
		{"Ctrl+KeyZ", "Ctrl+KeyZ"},
		{"Ctrl+BracketLeft", "Ctrl+Key["},
		{"semicolon", "Key;"},

		// Mouse wheel keys are keys: the main key of a chord, case-folded.
		{"wheelup", "WheelUp"},
		{"WHEELDOWN", "WheelDown"},
		{"Ctrl+WheelUp", "Ctrl+WheelUp"},
		{"shift + ctrl + wheelleft", "Ctrl+Shift+WheelLeft"},
		{"RAlt+LCtrl+WheelRight", "LCtrl+RAlt+WheelRight"},

		// Mouse buttons are keys too, with the Windows names as aliases that
		// format as the canonical names.
		{"mouseleft", "MouseLeft"},
		{"LButton", "MouseLeft"},
		{"lbutton", "MouseLeft"},
		{"RButton", "MouseRight"},
		{"MButton", "MouseMiddle"},
		{"XBUTTON1", "MouseX1"},
		{"xbutton2", "MouseX2"},
		{"Shift+MouseLeft", "Shift+MouseLeft"},
		{"ctrl + alt + rbutton", "Ctrl+Alt+MouseRight"},
		{"RAlt+LCtrl+XButton1", "LCtrl+RAlt+MouseX1"},

		// Raw scan codes.
		{"sc:0x2c", "sc:0x2C"},
		{"SC:0X2C", "sc:0x2C"},
		{"sc:0xe01d", "sc:0xE01D"},
		{"sc:0x1", "sc:0x01"},
		{"sc:0x002C", "sc:0x2C"},
		{"sc:0x7F", "sc:0x7F"},
		{"sc:0xE07F", "sc:0xE07F"},
		{"Ctrl+sc:0x2C", "Ctrl+sc:0x2C"},
		{"ctrl+sc:0x1D", "Ctrl+sc:0x1D"},
	}
	for _, tt := range tests {
		c, err := keys.Parse(tt.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.in, err)
			continue
		}
		if got := c.String(); got != tt.want {
			t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
		}
		again, err := keys.Parse(c.String())
		if err != nil || again != c {
			t.Errorf("Parse(%q) round trip = %v, %v, want %v", c.String(), again, err, c)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		in       string
		msg      string // substring of the message
		off, len int
	}{
		{"", "empty key spec", 0, 0},
		{"   ", "empty key spec", 0, 3},
		{"Ctrl+", `missing key after "+"`, 4, 1},
		{"Ctrl+ ", "write Space for the space bar", 4, 1},
		{"Ctrl++", "write Plus for the + character", 5, 1},
		{"Ctrl++Z", "write Plus for the + character", 5, 1},
		{"Ctrl + + Z", "write Plus for the + character", 7, 1},
		{"+", "write Plus for the + character", 0, 1},
		{" + ", "write Plus for the + character", 1, 1},
		{"++", "write Plus for the + character", 0, 1},
		{"+Z", `missing modifier before "+"`, 0, 1},
		{"Ctrl+Foo", `unknown key "Foo"`, 5, 3},
		{"Ctrl+ Foo ", `unknown key "Foo"`, 6, 3},
		{"Escpe", `did you mean Esc?`, 0, 5},
		{"Ctrl+Shfit+Z", `did you mean Shift`, 5, 5},
		{"Ctrl+Shift+Enterr", `did you mean Enter`, 11, 6},
		{"F25", "function keys go from F1 to F24", 0, 3},
		{"F0", "function keys go from F1 to F24", 0, 2},
		{"ctrl-z", `joined with "+", e.g. Ctrl+Z`, 0, 6},
		{"Ctrl Shift Z", `joined with "+", e.g. Ctrl+Shift+Z`, 0, 12},
		{"!", `"!" is a shifted character; bind the unshifted key with Shift, e.g. Shift+1`, 0, 1},
		{"Ctrl+?", `e.g. Shift+/`, 5, 1},
		{"Ctrl+_", `e.g. Shift+-`, 5, 1},
		{"Ctrl+|", `e.g. Shift+\`, 5, 1},
		{"Ctrl+~", "e.g. Shift+`", 5, 1},
		{"Ctrl+Ctrl+Z", `duplicate modifier "Ctrl"`, 5, 4},
		{"Ctrl+Control+Z", `duplicate modifier "Ctrl"`, 5, 7},
		{"Shift+shift", `duplicate modifier "Shift"`, 6, 5},
		{"Ctrl+LCtrl+Z", `"LCtrl" conflicts with "Ctrl"`, 5, 5},
		{"RCtrl+Ctrl+Z", `"Ctrl" conflicts with "RCtrl"`, 6, 4},
		{"LCtrl+RCtrl+Ctrl", `"Ctrl" conflicts with "LCtrl"`, 12, 4},
		{"Win+RWin", `"RWin" conflicts with "Win"`, 4, 4},
		{"Z+Ctrl", `"Z" is not a modifier`, 0, 1},
		{"Ctrl+Z+X", `"Z" is not a modifier`, 5, 1},
		{"A+B", "write a sequence as an array", 0, 1},
		{"sc:0x2A+Z", `"sc:0x2A" is not a modifier`, 0, 7},
		{"sc:2C", "invalid scan code", 0, 5},
		{"sc:0x", "invalid scan code", 0, 5},
		{"sc:0xZZ", "invalid scan code", 0, 7},
		{"sc:0x12345", "invalid scan code", 0, 10},
		{"sc:0x-1", "invalid scan code", 0, 7},
		{"sc:0x00", "out of range", 0, 7},
		{"sc:0x80", "out of range", 0, 7},
		{"sc:0xE000", "out of range", 0, 9},
		{"sc:0xE080", "out of range", 0, 9},
		{"sc:0xE11D", "write Pause", 0, 9},
		{"Ctrl+é", `unknown key "é"`, 5, 2},

		// A wheel key is no modifier: it can only be the last key of a chord.
		{"WheelUp+Ctrl", `"WheelUp" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)`, 0, 7},
		{"Ctrl+WheelUp+Z", `"WheelUp" is not a modifier`, 5, 7},
		{"WheelUp+WheelDown", `"WheelUp" is not a modifier`, 0, 7},
		{"Shift+WheelUp+WheelUp", `"WheelUp" is not a modifier`, 6, 7},
		{"Ctrl+Wheel", `unknown key "Wheel"`, 5, 5},
		{"Ctrl+WhelUp", `unknown key "WhelUp"; did you mean WheelUp?`, 5, 6},
		{"wheelu", `did you mean WheelUp?`, 0, 6},
		{"WheelDwon", `did you mean WheelDown?`, 0, 9},
		{"MouseWheelUp", `unknown key "MouseWheelUp"`, 0, 12},

		// A button is no modifier either, and its names are suggested.
		{"MouseLeft+Ctrl", `"MouseLeft" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)`, 0, 9},
		{"Ctrl+LButton+Z", `"LButton" is not a modifier`, 5, 7},
		{"MouseLeft+MouseRight", `"MouseLeft" is not a modifier`, 0, 9},
		{"Shift+MouseLeft+WheelUp", `"MouseLeft" is not a modifier`, 6, 9},
		{"Ctrl+MousLeft", `unknown key "MousLeft"; did you mean MouseLeft?`, 5, 8},
		{"mouseright1", `unknown key "mouseright1"; did you mean MouseRight?`, 0, 11},
		{"Ctrl+MouseX3", `unknown key "MouseX3"`, 5, 7},
		{"XButton3", `unknown key "XButton3"`, 0, 8},
		{"Mouse", `unknown key "Mouse"`, 0, 5},
		{"Click", `unknown key "Click"`, 0, 5},
	}
	for _, tt := range tests {
		_, err := keys.Parse(tt.in)
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want error containing %q", tt.in, tt.msg)
			continue
		}
		var pe *keys.ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q) error %T, want *keys.ParseError", tt.in, err)
			continue
		}
		if !strings.Contains(pe.Msg, tt.msg) || pe.Error() != pe.Msg {
			t.Errorf("Parse(%q) error %q, want it to contain %q", tt.in, pe.Msg, tt.msg)
		}
		if pe.Input != tt.in || pe.Offset != tt.off || pe.Len != tt.len {
			t.Errorf("Parse(%q) error at %d+%d of %q, want %d+%d", tt.in, pe.Offset, pe.Len, pe.Input, tt.off, tt.len)
		}
	}
}

func TestShiftedCharactersRejected(t *testing.T) {
	for _, s := range strings.Split(`! @ # $ % ^ & * ( ) _ { } | : " < > ? ~`, " ") {
		_, err := keys.Parse(s)
		if err == nil || !strings.Contains(err.Error(), "is a shifted character") {
			t.Errorf("Parse(%q) = %v, want a shifted character hint", s, err)
		}
	}
}

func TestDistinctSpellings(t *testing.T) {
	// Z, KeyZ and sc:0x2C share a position but are distinct map keys.
	held := map[keys.Key]int{}
	for _, s := range []string{"Z", "KeyZ", "sc:0x2C", "z", "keyZ", "SC:0x2c"} {
		k, ok := keys.Lookup(s)
		if !ok {
			t.Fatalf("Lookup(%q) failed", s)
		}
		held[k]++
	}
	if len(held) != 3 {
		t.Fatalf("got %d distinct keys, want 3: %v", len(held), held)
	}
	for k, n := range held {
		if n != 2 {
			t.Errorf("%s seen %d times, want 2", k, n)
		}
		if k.US().Scan != 0x2C || k.US().Ext {
			t.Errorf("%s US mapping %+v, want scan 0x2C", k, k.US())
		}
	}

	chords := map[keys.Chord]bool{
		keys.MustParse("Ctrl+Shift+Z"): true,
		keys.MustParse("shift+ctrl+z"): true,
		keys.MustParse("Ctrl+Shift"):   true,
		keys.MustParse("Shift+Ctrl"):   true,
	}
	if len(chords) != 2 {
		t.Errorf("got %d distinct chords, want 2", len(chords))
	}
}

func TestKeyProperties(t *testing.T) {
	tests := []struct {
		in       string
		kind     keys.Kind
		modifier bool
		mod      keys.Mods
		us       keys.USMapping
	}{
		{"F13", keys.KindNamed, false, 0, keys.USMapping{Scan: 0x64, VK: 0x7C}},
		{"Z", keys.KindChar, false, 0, keys.USMapping{Scan: 0x2C, VK: 0x5A}},
		{"Plus", keys.KindChar, false, 0, keys.USMapping{Scan: 0x0D, VK: 0xBB, Shift: true}},
		{"KeyZ", keys.KindPhysical, false, 0, keys.USMapping{Scan: 0x2C, VK: 0x5A}},
		{"sc:0x2C", keys.KindScan, false, 0, keys.USMapping{Scan: 0x2C}},
		{"sc:0xE01D", keys.KindScan, false, 0, keys.USMapping{Scan: 0x1D, Ext: true}},
		{"sc:0x1D", keys.KindScan, false, 0, keys.USMapping{Scan: 0x1D}},
		{"Ctrl", keys.KindModifier, true, keys.ModCtrl, keys.USMapping{Scan: 0x1D, VK: 0xA2}},
		{"RAlt", keys.KindModifier, true, keys.ModRAlt, keys.USMapping{Scan: 0x38, Ext: true, VK: 0xA5}},
		{"Win", keys.KindModifier, true, keys.ModWin, keys.USMapping{Scan: 0x5B, Ext: true, VK: 0x5B}},
		{"Pause", keys.KindNamed, false, 0, keys.USMapping{Scan: 0x45, E1: true, VK: 0x13}},
		// Media keys are E0 keys with the VK of their VK_VOLUME_* or VK_MEDIA_*
		// constant (ADR-0070).
		{"VolumeUp", keys.KindMedia, false, 0, keys.USMapping{Scan: 0x30, Ext: true, VK: 0xAF}},
		{"audiovolumemute", keys.KindMedia, false, 0, keys.USMapping{Scan: 0x20, Ext: true, VK: 0xAD}},
		{"MediaPlayPause", keys.KindMedia, false, 0, keys.USMapping{Scan: 0x22, Ext: true, VK: 0xB3}},
		// Mouse keys are keys, with the zero mapping.
		{"WheelUp", keys.KindMouse, false, 0, keys.USMapping{}},
		{"wheeldown", keys.KindMouse, false, 0, keys.USMapping{}},
		{"WheelLeft", keys.KindMouse, false, 0, keys.USMapping{}},
		{"WheelRight", keys.KindMouse, false, 0, keys.USMapping{}},
		{"MouseLeft", keys.KindMouse, false, 0, keys.USMapping{}},
		{"lbutton", keys.KindMouse, false, 0, keys.USMapping{}},
		{"RButton", keys.KindMouse, false, 0, keys.USMapping{}},
		{"MouseMiddle", keys.KindMouse, false, 0, keys.USMapping{}},
		{"XButton1", keys.KindMouse, false, 0, keys.USMapping{}},
		{"mousex2", keys.KindMouse, false, 0, keys.USMapping{}},
	}
	for _, tt := range tests {
		k, ok := keys.Lookup(tt.in)
		if !ok {
			t.Fatalf("Lookup(%q) failed", tt.in)
		}
		if k.Kind() != tt.kind || k.IsModifier() != tt.modifier || k.Mod() != tt.mod || k.US() != tt.us || !k.IsValid() {
			t.Errorf("%s: kind %v modifier %v mod %v us %+v, want %v %v %v %+v",
				tt.in, k.Kind(), k.IsModifier(), k.Mod(), k.US(), tt.kind, tt.modifier, tt.mod, tt.us)
		}
		if k.String() != keys.MustParse(tt.in).String() {
			t.Errorf("%s: String() = %q", tt.in, k)
		}
	}

	var zero keys.Key
	if zero.IsValid() || zero.String() != "" || zero.Kind() != keys.KindInvalid || zero.US() != (keys.USMapping{}) {
		t.Errorf("zero Key: valid %v, %q, %v, %+v", zero.IsValid(), zero, zero.Kind(), zero.US())
	}
	if zero.Aliases() != nil || zero.Mod() != 0 || zero.IsModifier() {
		t.Error("zero Key has aliases or a modifier bit")
	}
	var zc keys.Chord
	if zc.IsValid() || zc.String() != "" || zc.Keys() != nil {
		t.Errorf("zero Chord: valid %v, %q, %v", zc.IsValid(), zc, zc.Keys())
	}
	if got := keys.Kind(200).String(); got != "invalid" {
		t.Errorf("Kind(200).String() = %q", got)
	}
}

func TestLookupStrict(t *testing.T) {
	for _, s := range []string{"", " Z", "Z ", "Ctrl+Z", "Foo", "sc:0x00", "sc:", strings.Repeat("a", 40)} {
		if k, ok := keys.Lookup(s); ok {
			t.Errorf("Lookup(%q) = %v, want failure", s, k)
		}
	}
}

func TestChordKeys(t *testing.T) {
	c := keys.MustParse("shift+alt+ctrl+z")
	names := func(ks []keys.Key) []string {
		var out []string
		for _, k := range ks {
			out = append(out, k.String())
		}
		return out
	}
	if got, want := names(c.Keys()), []string{"Ctrl", "Shift", "Alt", "Z"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
	if c.Mods() != keys.ModCtrl|keys.ModShift|keys.ModAlt || c.Key().String() != "Z" {
		t.Errorf("Mods() = %v, Key() = %v", c.Mods(), c.Key())
	}
	if !c.Mods().Has(keys.ModCtrl|keys.ModAlt) || c.Mods().Has(keys.ModWin) {
		t.Errorf("Has() wrong for %v", c.Mods())
	}
	if got := c.Mods().String(); got != "Ctrl+Shift+Alt" {
		t.Errorf("Mods().String() = %q", got)
	}
	if got := keys.Mods(0).String(); got != "" {
		t.Errorf("Mods(0).String() = %q", got)
	}

	m := keys.MustParse("Shift+Ctrl")
	if m.Mods() != keys.ModCtrl || m.Key().String() != "Shift" {
		t.Errorf("Shift+Ctrl: Mods() = %v, Key() = %v", m.Mods(), m.Key())
	}
	if got, want := names(m.Keys()), []string{"Ctrl", "Shift"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}

	all := keys.Mods(0xFFFF)
	if got := len(all.Keys()); got != 12 {
		t.Errorf("Mods(0xFFFF).Keys() has %d keys, want 12", got)
	}
}

func TestChordSided(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Z", "Z"},
		{"Ctrl+Z", "LCtrl+Z"},
		{"LCtrl+Z", "LCtrl+Z"},
		{"RCtrl+Z", "RCtrl+Z"},
		{"Ctrl+Shift+Alt+Win+Z", "LCtrl+LShift+LAlt+LWin+Z"},
		{"Ctrl+RShift+F13", "LCtrl+RShift+F13"},
		{"Ctrl+Plus", "LCtrl+Plus"},
		{"Shift+KeyZ", "LShift+KeyZ"},
		{"Alt+sc:0x2C", "LAlt+sc:0x2C"},
		{"Ctrl+WheelUp", "LCtrl+WheelUp"},
		{"WheelDown", "WheelDown"},
		{"Shift+Alt+WheelLeft", "LShift+LAlt+WheelLeft"},
		{"Ctrl+MouseLeft", "LCtrl+MouseLeft"},
		{"MouseRight", "MouseRight"},
		{"Shift+Alt+XButton2", "LShift+LAlt+MouseX2"},
		{"Ctrl+sc:0x1D", "LCtrl+sc:0x1D"},
		// Modifier-only chords: the key is a modifier too.
		{"Ctrl", "LCtrl"},
		{"Win", "LWin"},
		{"RShift", "RShift"},
		{"Ctrl+Shift", "LCtrl+LShift"},
		{"Shift+RCtrl", "RCtrl+LShift"},
		{"RAlt+Win", "RAlt+LWin"},
		{"Shift+RWin", "LShift+RWin"},
		{"LCtrl+RCtrl", "LCtrl+RCtrl"},
		{"Win+Shift+Alt+Ctrl", "LCtrl+LShift+LAlt+LWin"},
	}
	for _, tt := range tests {
		c := keys.MustParse(tt.in)
		if got := c.Sided().String(); got != tt.want {
			t.Errorf("%s.Sided() = %q, want %q", tt.in, got, tt.want)
		}
		checkSided(t, c)
	}
	if s := (keys.Chord{}).Sided(); s != (keys.Chord{}) {
		t.Errorf("zero Chord.Sided() = %q, want the zero Chord", s)
	}
}

func TestParseSequence(t *testing.T) {
	cs, err := keys.ParseSequence([]string{"ctrl+k", "Ctrl+S", ",", "Plus"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.String())
	}
	if want := []string{"Ctrl+K", "Ctrl+S", ",", "Plus"}; !slices.Equal(got, want) {
		t.Errorf("ParseSequence = %v, want %v", got, want)
	}

	_, err = keys.ParseSequence([]string{"Ctrl+K", "Ctrl+Foo"})
	var se *keys.SequenceError
	if !errors.As(err, &se) || se.Index != 1 {
		t.Fatalf("ParseSequence error = %v, want a SequenceError at index 1", err)
	}
	var pe *keys.ParseError
	if !errors.As(err, &pe) || pe.Input != "Ctrl+Foo" || pe.Offset != 5 {
		t.Errorf("ParseSequence error does not unwrap to the ParseError: %v", err)
	}
	if want := `element 1: unknown key "Foo"`; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}

	if _, err := keys.ParseSequence(nil); !errors.Is(err, keys.ErrEmptySequence) {
		t.Errorf("ParseSequence(nil) = %v, want ErrEmptySequence", err)
	}
}

// Wheel keys are chords like any other, so a key sequence can scroll between
// shortcuts.
func TestParseSequenceWheel(t *testing.T) {
	cs, err := keys.ParseSequence([]string{"ctrl+k", "wheeldown", "Ctrl+WheelUp"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.String())
	}
	if want := []string{"Ctrl+K", "WheelDown", "Ctrl+WheelUp"}; !slices.Equal(got, want) {
		t.Errorf("ParseSequence = %v, want %v", got, want)
	}
	if m, ok := cs[2].Key().Mouse(); !ok || m != keys.WheelUp || cs[2].Mods() != keys.ModCtrl {
		t.Errorf("Ctrl+WheelUp: mods %v, key %v (mouse %v, %v)", cs[2].Mods(), cs[2].Key(), m, ok)
	}

	_, err = keys.ParseSequence([]string{"WheelUp", "WheelUp+Ctrl"})
	var se *keys.SequenceError
	if !errors.As(err, &se) || se.Index != 1 {
		t.Fatalf("ParseSequence error = %v, want a SequenceError at index 1", err)
	}
}

// Buttons are chords too, so a key sequence can click between shortcuts.
func TestParseSequenceButtons(t *testing.T) {
	cs, err := keys.ParseSequence([]string{"ctrl+k", "mouseleft", "Shift+RButton", "XButton2"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.String())
	}
	if want := []string{"Ctrl+K", "MouseLeft", "Shift+MouseRight", "MouseX2"}; !slices.Equal(got, want) {
		t.Errorf("ParseSequence = %v, want %v", got, want)
	}
	if m, ok := cs[2].Key().Mouse(); !ok || m != keys.MouseRight || cs[2].Mods() != keys.ModShift || !cs[2].Key().IsMouseButton() {
		t.Errorf("Shift+RButton: mods %v, key %v (mouse %v, %v)", cs[2].Mods(), cs[2].Key(), m, ok)
	}

	_, err = keys.ParseSequence([]string{"MouseLeft", "MouseLeft+Ctrl"})
	var se *keys.SequenceError
	if !errors.As(err, &se) || se.Index != 1 {
		t.Fatalf("ParseSequence error = %v, want a SequenceError at index 1", err)
	}
}

func TestMustParsePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), `keys.MustParse("Ctrl+Foo")`) {
			t.Errorf("MustParse panic = %v", r)
		}
	}()
	keys.MustParse("Ctrl+Foo")
}

func BenchmarkParse(b *testing.B) {
	for _, s := range []string{"F13", "ctrl+shift+z", "LCtrl + LAlt + LShift + KeyZ", "sc:0xE01D"} {
		b.Run(s, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := keys.Parse(s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkString(b *testing.B) {
	c := keys.MustParse("Ctrl+Shift+Z")
	b.ReportAllocs()
	for b.Loop() {
		_ = c.String()
	}
}
