package diagnostics

import (
	"slices"
	"testing"
)

// TestSteamInputClass: the class of Steam Input settings that may govern a
// gamepad (IN-24, ADR-0071), from what SDL reports of it. The cases are the
// gamepads of the owner's machine (hardware-matrix.md) and the others the
// research of the ADR named.
func TestSteamInputClass(t *testing.T) {
	for _, tc := range []struct {
		name string
		pad  SteamPad
		want SteamClass
	}{
		{"Nova 2 Lite, Xbox mode", SteamPad{Type: "xboxone", Vendor: 0x3537, Product: 0x1098, Driver: "xinput"}, SteamXbox},
		{"Nova 2 Lite, Switch mode", SteamPad{Type: "switchpro", Vendor: 0x057e, Product: 0x2009, Driver: "hidapi"}, SteamSwitch},
		{"Nova 2 Lite, PS4 mode", SteamPad{Type: "ps4", Vendor: 0x054c, Product: 0x09cc, Driver: "hidapi"}, SteamPlayStation},
		{"DualSense", SteamPad{Type: "ps5", Vendor: 0x054c, Product: 0x0ce6, Driver: "hidapi"}, SteamPlayStation},
		{"DualShock 3", SteamPad{Type: "ps3", Vendor: 0x054c, Product: 0x0268, Driver: "hidapi"}, SteamPlayStation},
		{"Joy-Con (L)", SteamPad{Type: "joyconleft", Vendor: 0x057e, Product: 0x2006, Driver: "hidapi"}, SteamSwitch},
		{"Joy-Con (R)", SteamPad{Type: "joyconright", Vendor: 0x057e, Product: 0x2007, Driver: "hidapi"}, SteamSwitch},
		{"Joy-Con pair", SteamPad{Type: "joyconpair", Vendor: 0x057e, Product: 0x2008, Driver: "hidapi"}, SteamSwitch},
		{"Xbox 360 wired", SteamPad{Type: "xbox360", Vendor: 0x045e, Product: 0x028e, Driver: "xinput"}, SteamXbox},
		{"Xbox One over HID", SteamPad{Type: "xboxone", Vendor: 0x045e, Product: 0x02ea, Driver: "hidapi"}, SteamXbox},
		{"a generic pad on XInput", SteamPad{Type: "standard", Vendor: 0x046d, Product: 0xc21d, Driver: "xinput"}, SteamXbox},
		{"Steam Deck", SteamPad{Type: "standard", Vendor: 0x28de, Product: 0x1205, Driver: "hidapi"}, SteamHardware},
		{"Steam Controller", SteamPad{Type: "unknown", Vendor: 0x28de, Product: 0x1102, Driver: "hidapi"}, SteamHardware},
		{"a Steam pad of a PlayStation type", SteamPad{Type: "ps4", Vendor: 0x28de, Product: 0x1302, Driver: "hidapi"}, SteamHardware},
		{"unknown", SteamPad{Type: "unknown", Vendor: 0x1234, Product: 0x5678, Driver: "dinput"}, SteamNone},
		{"a generic pad on DirectInput", SteamPad{Type: "standard", Vendor: 0x0079, Product: 0x0006, Driver: "dinput"}, SteamNone},
		{"GameCube adapter", SteamPad{Type: "gamecube", Vendor: 0x057e, Product: 0x0337, Driver: "hidapi"}, SteamNone},
		{"nothing known", SteamPad{}, SteamNone},
		// Steam's own emulated pad is what Steam made, not a pad it can take over.
		{"Steam's virtual gamepad", SteamPad{Type: "xboxone", Vendor: 0x28de, Product: 0x11ff, Driver: "xinput"}, SteamNone},
		// An SDL virtual joystick, such as a test's, belongs to the process that made it.
		{"an SDL virtual joystick", SteamPad{Type: "xboxone", Vendor: 0x045e, Product: 0x028e, Driver: "virtual", Virtual: true}, SteamNone},
	} {
		if got := ClassifySteamInput(tc.pad); got != tc.want {
			t.Errorf("%s: class %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSteamClasses: the classes of several gamepads are listed once each, in
// a fixed order, and a gamepad Steam does not govern is left out.
func TestSteamClasses(t *testing.T) {
	xbox := SteamPad{Type: "xboxone", Driver: "xinput"}
	ps := SteamPad{Type: "ps5", Driver: "hidapi"}
	sw := SteamPad{Type: "switchpro", Driver: "hidapi"}
	deck := SteamPad{Type: "standard", Vendor: 0x28de, Product: 0x1205}
	other := SteamPad{Type: "unknown", Vendor: 0x1234}
	for _, tc := range []struct {
		name string
		pads []SteamPad
		want []string
	}{
		{"none", nil, nil},
		{"only others", []SteamPad{other}, nil},
		{"one", []SteamPad{sw}, []string{"switch"}},
		{"two of a class", []SteamPad{xbox, other, xbox}, []string{"xbox"}},
		{"all, out of order", []SteamPad{deck, sw, other, ps, xbox}, []string{"xbox", "playstation", "switch", "steamHardware"}},
	} {
		if got := SteamClasses(tc.pads); !slices.Equal(got, tc.want) {
			t.Errorf("%s: classes %v, want %v", tc.name, got, tc.want)
		}
	}
	// The words are the ones the UI reads.
	for class, word := range map[SteamClass]string{SteamNone: "none", SteamXbox: "xbox", SteamPlayStation: "playstation", SteamSwitch: "switch", SteamHardware: "steamHardware"} {
		if string(class) != word {
			t.Errorf("class %q, want %q", class, word)
		}
	}
}
