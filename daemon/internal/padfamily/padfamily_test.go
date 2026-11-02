package padfamily

import (
	"fmt"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// guid lays out an SDL GUID: bus, CRC of the name, vendor, 0, product, 0.
func guid(bus, vendor, product uint16) string {
	return fmt.Sprintf("%02x%02x0000%02x%02x0000%02x%02x000000000000", bus&0xff, bus>>8, vendor&0xff, vendor>>8, product&0xff, product>>8)
}

func TestFunctionKeys(t *testing.T) {
	for _, tc := range []struct {
		family  Family
		options []control.Control
	}{
		{Unknown, []control.Control{control.Start, control.Back}},
		{Standard, []control.Control{control.Start, control.Back}},
		// A single Joy-Con, upright: ZL and L, ZR and R. No − or +, no paddles.
		{JoyConLeft, []control.Control{control.LT, control.LeftShoulder}},
		{JoyConRight, []control.Control{control.RT, control.RightShoulder}},
		{JoyConPair, []control.Control{control.Back, control.Start}},
	} {
		t.Run(tc.family.String(), func(t *testing.T) {
			k := tc.family.FunctionKeys()
			if !slices.Equal(k.Options, tc.options) || k.Default != tc.options[0] {
				t.Fatalf("FunctionKeys = %+v; want options %v, default first", k, tc.options)
			}
			for _, c := range k.Options {
				if !k.Allows(c) || c == control.Guide || !c.CanBeFunctionKey() {
					t.Errorf("%s is not allowed, is guide, or the engine cannot compile it", c)
				}
			}
			if k.Allows(control.Guide) || k.Allows(control.South) || k.Allows(control.Paddle1) {
				t.Error("guide, south or a paddle allowed")
			}
		})
	}
}

func TestFamilyNames(t *testing.T) {
	for f := Unknown; f <= JoyConPair; f++ {
		got, ok := Parse(f.String())
		if !ok || got != f {
			t.Errorf("Parse(%q) = %v, %v", f.String(), got, ok)
		}
	}
	if _, ok := Parse("joyconleft"); ok {
		t.Error("names are case-sensitive")
	}
	for _, retired := range RetiredPanicFamilies {
		if _, ok := Parse(retired); ok {
			t.Errorf("%s is a family: the grip is gone", retired)
		}
	}
	if Family(200).String() != "unknown" {
		t.Error("out-of-range family should read as unknown")
	}
	// The names of the single Joy-Con families are those of the safety.panic
	// families that still count; every other controller is standard there.
	for f, want := range map[Family]string{
		Unknown: "standard", Standard: "standard", JoyConPair: "standard",
		JoyConLeft: "joyconLeft", JoyConRight: "joyconRight",
	} {
		if got := f.PanicFamily(); got != want {
			t.Errorf("%s.PanicFamily() = %s; want %s", f, got, want)
		}
	}
}

func TestDefaultPanic(t *testing.T) {
	for f, want := range map[Family][]string{
		Unknown: {"back", "start"}, Standard: {"back", "start"}, JoyConPair: {"back", "start"},
		JoyConLeft: {"paddle2", "paddle4"}, JoyConRight: {"paddle3", "paddle1"},
	} {
		if got := f.DefaultPanic(); !slices.Equal(got, want) {
			t.Errorf("%s.DefaultPanic() = %v; want %v", f, got, want)
		}
	}
}

// TestHasButton: a single Joy-Con has its half of the buttons only, and the
// built-in emergency combo and the shoulder function keys are among them.
func TestHasButton(t *testing.T) {
	for _, tc := range []struct {
		family Family
		has    []string
		hasNot []string
	}{
		{JoyConLeft, []string{"back", "dpad.left", "leftshoulder", "leftstick", "misc1", "paddle2", "paddle4"},
			[]string{"south", "start", "rightshoulder", "rightstick", "paddle1", "paddle3", "guide"}},
		{JoyConRight, []string{"south", "east", "west", "north", "start", "guide", "rightshoulder", "rightstick", "paddle1", "paddle3"},
			[]string{"back", "dpad.up", "leftshoulder", "leftstick", "paddle2", "paddle4"}},
		{JoyConPair, []string{"back", "start", "south", "paddle1"}, nil},
		{Standard, []string{"back", "start", "paddle4", "touchpad"}, nil},
		{Unknown, []string{"back", "start"}, nil},
	} {
		for _, b := range tc.has {
			if !tc.family.HasButton(b) {
				t.Errorf("%s lacks %s", tc.family, b)
			}
		}
		for _, b := range tc.hasNot {
			if tc.family.HasButton(b) {
				t.Errorf("%s has %s", tc.family, b)
			}
		}
		for _, b := range tc.family.DefaultPanic() {
			if !tc.family.HasButton(b) {
				t.Errorf("%s: its built-in combo has %s, which it lacks", tc.family, b)
			}
		}
	}
	for _, c := range JoyConLeft.FunctionKeys().Options {
		if c == control.LeftShoulder && !JoyConLeft.HasButton(c.String()) {
			t.Error("left: L is not a button of it")
		}
	}
}

func TestFromGUID(t *testing.T) {
	xbox := guid(0x03, 0x045e, 0x0b13)
	for _, tc := range []struct {
		name string
		guid string
		want Family
	}{
		{"xbox", xbox, Standard},
		{"switch pro", guid(0x03, 0x057e, 0x2009), Standard},
		{"joycon L", guid(0x03, 0x057e, 0x2006), JoyConLeft},
		{"joycon R", guid(0x05, 0x057e, 0x2007), JoyConRight},
		{"joycon pair", guid(0x05, 0x057e, 0x2008), JoyConPair},
		{"joycon 2 L", guid(0x05, 0x057e, 0x2067), JoyConLeft},
		{"joycon 2 R", guid(0x05, 0x057e, 0x2066), JoyConRight},
		{"joycon 2 pair", guid(0x05, 0x057e, 0x2068), JoyConPair},
		{"uppercase hex", "05000000" + "7E050000" + "67200000" + "00000000", JoyConLeft},
		{"empty", "", Unknown},
		{"short", "0300", Unknown},
		{"not hex", "zz000000" + "7e050000" + "06200000" + "00000000", Unknown},
		{"no vendor: the name follows", "03000000" + "00000000" + "06200000" + "00000000", Unknown},
		{"bytes after the vendor are not zero", "03000000" + "7e050100" + "06200000" + "00000000", Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromGUID(tc.guid); got != tc.want {
				t.Errorf("FromGUID = %s; want %s", got, tc.want)
			}
		})
	}
}

func TestPossibleFunctionKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		guid string
		want []control.Control
	}{
		{"standard", guid(0x03, 0x045e, 0x0b13), []control.Control{control.Start, control.Back}},
		{"pair", guid(0x05, 0x057e, 0x2068), []control.Control{control.Back, control.Start}},
		{"left", guid(0x05, 0x057e, 0x2067), []control.Control{control.LT, control.LeftShoulder}},
		{"right", guid(0x05, 0x057e, 0x2066), []control.Control{control.RT, control.RightShoulder}},
		{"unknown", "", []control.Control{control.Start, control.Back, control.LT, control.RT, control.LeftShoulder, control.RightShoulder}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PossibleFunctionKeys(tc.guid)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("PossibleFunctionKeys = %v; want %v", got, tc.want)
			}
			got[0] = control.Guide // a copy
			if PossibleFunctionKeys(tc.guid)[0] == control.Guide {
				t.Error("PossibleFunctionKeys returned shared storage")
			}
		})
	}
}

func FuzzFromGUID(f *testing.F) {
	for _, s := range []string{"", guid(0x03, 0x057e, 0x2006), "zz", guid(0x05, 0x057e, 0x2068)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, guid string) {
		fam := FromGUID(guid)
		keys := PossibleFunctionKeys(guid)
		if len(keys) == 0 {
			t.Fatal("no function keys")
		}
		// What a family offers is always possible for its GUID.
		for _, c := range fam.FunctionKeys().Options {
			if fam != Unknown && !slices.Contains(keys, c) {
				t.Fatalf("%s offers %s, which %q cannot hold", fam, c, guid)
			}
		}
	})
}
