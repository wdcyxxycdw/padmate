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
		// Sideways: ZL and L (paddle4, paddle2), ZR and R (paddle3, paddle1).
		{JoyConLeft, []control.Control{control.Paddle4, control.Paddle2}},
		{JoyConRight, []control.Control{control.Paddle3, control.Paddle1}},
		{JoyConLeftVertical, []control.Control{control.Back}},
		{JoyConRightVertical, []control.Control{control.Start}},
		{JoyConPair, []control.Control{control.Back, control.Start}},
	} {
		t.Run(tc.family.String(), func(t *testing.T) {
			k := tc.family.FunctionKeys()
			if !slices.Equal(k.Options, tc.options) || k.Default != tc.options[0] {
				t.Fatalf("FunctionKeys = %+v; want options %v, default first", k, tc.options)
			}
			for _, c := range k.Options {
				if !k.Allows(c) || c == control.Guide {
					t.Errorf("%s is not allowed or is guide", c)
				}
			}
			if k.Allows(control.Guide) || k.Allows(control.South) {
				t.Error("guide or south allowed")
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
	if Family(200).String() != "unknown" {
		t.Error("out-of-range family should read as unknown")
	}
	// The names of the single Joy-Con families are those of the retired
	// safety.panic families; every other controller is standard there.
	for f, want := range map[Family]string{
		Unknown: "standard", Standard: "standard", JoyConPair: "standard",
		JoyConLeft: "joyconLeft", JoyConRight: "joyconRight",
		JoyConLeftVertical: "joyconLeftVertical", JoyConRightVertical: "joyconRightVertical",
	} {
		if got := f.PanicFamily(); got != want {
			t.Errorf("%s.PanicFamily() = %s; want %s", f, got, want)
		}
	}
}

func TestDefaultPanic(t *testing.T) {
	for f, want := range map[Family][]string{
		Unknown: {"back", "start"}, Standard: {"back", "start"}, JoyConPair: {"back", "start"},
		JoyConLeft: {"leftshoulder", "rightshoulder"}, JoyConRight: {"leftshoulder", "rightshoulder"},
		JoyConLeftVertical: {"paddle2", "paddle4"}, JoyConRightVertical: {"paddle3", "paddle1"},
	} {
		if got := f.DefaultPanic(); !slices.Equal(got, want) {
			t.Errorf("%s.DefaultPanic() = %v; want %v", f, got, want)
		}
	}
}

func TestFromGUID(t *testing.T) {
	xbox := guid(0x03, 0x045e, 0x0b13)
	for _, tc := range []struct {
		name     string
		guid     string
		vertical bool
		want     Family
	}{
		{"xbox", xbox, false, Standard},
		{"switch pro", guid(0x03, 0x057e, 0x2009), false, Standard},
		{"joycon L", guid(0x03, 0x057e, 0x2006), false, JoyConLeft},
		{"joycon R", guid(0x05, 0x057e, 0x2007), false, JoyConRight},
		{"joycon pair", guid(0x05, 0x057e, 0x2008), false, JoyConPair},
		{"joycon 2 L", guid(0x05, 0x057e, 0x2067), false, JoyConLeft},
		{"joycon 2 R", guid(0x05, 0x057e, 0x2066), false, JoyConRight},
		{"joycon 2 pair", guid(0x05, 0x057e, 0x2068), false, JoyConPair},
		{"joycon L upright", guid(0x03, 0x057e, 0x2006), true, JoyConLeftVertical},
		{"joycon 2 R upright", guid(0x05, 0x057e, 0x2066), true, JoyConRightVertical},
		{"pair is not turned upright", guid(0x05, 0x057e, 0x2068), true, JoyConPair},
		{"uppercase hex", "05000000" + "7E050000" + "67200000" + "00000000", false, JoyConLeft},
		{"empty", "", false, Unknown},
		{"short", "0300", false, Unknown},
		{"not hex", "zz000000" + "7e050000" + "06200000" + "00000000", false, Unknown},
		{"no vendor: the name follows", "03000000" + "00000000" + "06200000" + "00000000", false, Unknown},
		{"bytes after the vendor are not zero", "03000000" + "7e050100" + "06200000" + "00000000", false, Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromGUID(tc.guid, tc.vertical); got != tc.want {
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
		// Either grip: sideways ZL and L, upright −.
		{"left", guid(0x05, 0x057e, 0x2067), []control.Control{control.Paddle4, control.Paddle2, control.Back}},
		{"right", guid(0x05, 0x057e, 0x2066), []control.Control{control.Paddle3, control.Paddle1, control.Start}},
		{"unknown", "", []control.Control{control.Start, control.Back, control.Paddle1, control.Paddle2, control.Paddle3, control.Paddle4}},
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
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, guid string, vertical bool) {
		fam := FromGUID(guid, vertical)
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
