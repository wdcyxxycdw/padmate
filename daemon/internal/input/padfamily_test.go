package input_test

import (
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"
)

// maskOf is the ButtonMask of a gamepad with the buttons names: one bit per
// SDL gamepad button (input.ButtonName).
func maskOf(t *testing.T, names []string) uint32 {
	t.Helper()
	var mask uint32
	for _, name := range names {
		found := false
		for b := range uint8(32) {
			if input.ButtonName(b) == name {
				mask |= 1 << b
				found = true
			}
		}
		if !found {
			t.Fatalf("%q is no button", name)
		}
	}
	return mask
}

func TestFamilyOf(t *testing.T) {
	for _, tc := range []struct {
		name     string
		info     input.DeviceInfo
		vertical bool
		want     padfamily.Family
	}{
		{"xbox", input.DeviceInfo{Type: "xboxone"}, false, padfamily.Standard},
		{"ps5", input.DeviceInfo{Type: "ps5"}, true, padfamily.Standard},
		{"switch pro", input.DeviceInfo{Type: "switchpro"}, false, padfamily.Standard},
		{"unknown type", input.DeviceInfo{Type: "unknown"}, false, padfamily.Standard},
		{"empty type", input.DeviceInfo{}, false, padfamily.Standard},
		{"pair", input.DeviceInfo{Type: "joyconpair"}, false, padfamily.JoyConPair},
		{"pair ignores the grip hint", input.DeviceInfo{Type: "joyconpair"}, true, padfamily.JoyConPair},
		// Without known buttons the hint decides.
		{"left, hint unset", input.DeviceInfo{Type: "joyconleft"}, false, padfamily.JoyConLeft},
		{"right, hint unset", input.DeviceInfo{Type: "joyconright"}, false, padfamily.JoyConRight},
		{"left, vertical hint", input.DeviceInfo{Type: "joyconleft"}, true, padfamily.JoyConLeftVertical},
		{"right, vertical hint", input.DeviceInfo{Type: "joyconright"}, true, padfamily.JoyConRightVertical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := input.FamilyOf(tc.info, tc.vertical); got != tc.want {
				t.Errorf("FamilyOf = %s; want %s", got, tc.want)
			}
		})
	}
}

// TestFamilyOfFollowsTheButtons: the grip of a single Joy-Con is judged by
// the buttons it reports, not by the hint, once they are known, for the
// virtual pads that model SDL's Switch 1 mapping and for the Joy-Con 2's
// own mapping; and what the family offers as a function key is a button the
// pad has.
func TestFamilyOfFollowsTheButtons(t *testing.T) {
	type pad struct {
		name    string
		typ     string
		buttons []string
		want    padfamily.Family
	}
	pads := []pad{
		{"SDL left sideways", "joyconleft", inputtest.JoyConLeft("l").Buttons, padfamily.JoyConLeft},
		{"SDL right sideways", "joyconright", inputtest.JoyConRight("r").Buttons, padfamily.JoyConRight},
		{"SDL left upright", "joyconleft", inputtest.JoyConLeftUpright("l").Buttons, padfamily.JoyConLeftVertical},
		{"SDL right upright", "joyconright", inputtest.JoyConRightUpright("r").Buttons, padfamily.JoyConRightVertical},
		{"SDL pair", "joyconpair", inputtest.JoyConPair("p").Buttons, padfamily.JoyConPair},
		{"Joy-Con 2 left sideways", "joyconleft", jc2.Controls(jc2.Horizontal, jc2.SideL), padfamily.JoyConLeft},
		{"Joy-Con 2 right sideways", "joyconright", jc2.Controls(jc2.Horizontal, jc2.SideR), padfamily.JoyConRight},
		{"Joy-Con 2 left upright", "joyconleft", jc2.Controls(jc2.Vertical, jc2.SideL), padfamily.JoyConLeftVertical},
		{"Joy-Con 2 right upright", "joyconright", jc2.Controls(jc2.Vertical, jc2.SideR), padfamily.JoyConRightVertical},
		{"Joy-Con 2 pair", "joyconpair", jc2.Controls(jc2.Pair, jc2.SideL), padfamily.JoyConPair},
	}
	for _, p := range pads {
		for _, vertical := range []bool{false, true} {
			t.Run(p.name, func(t *testing.T) {
				info := input.DeviceInfo{Type: p.typ, ButtonMask: maskOf(t, p.buttons)}
				got := input.FamilyOf(info, vertical)
				if got != p.want {
					t.Fatalf("FamilyOf(vertical=%t) = %s; want %s", vertical, got, p.want)
				}
				// The function keys of the family are buttons the pad has,
				// the sideways ones the paddles (SDL's L, ZL, R, ZR).
				for _, c := range got.FunctionKeys().Options {
					if !slices.Contains(p.buttons, c.String()) {
						t.Errorf("function key %s is not a button of the pad: %v", c, p.buttons)
					}
				}
				// The built-in combo matches input.DefaultCombo, which the
				// family's table duplicates.
				want := input.DefaultCombo(input.DeviceInfo{Type: p.typ}, got == padfamily.JoyConLeftVertical || got == padfamily.JoyConRightVertical)
				if !slices.Equal(got.DefaultPanic(), want.Buttons) || padfamily.DefaultPanicHoldMs != int(want.Hold.Milliseconds()) {
					t.Errorf("DefaultPanic = %v, hold %d ms; DefaultCombo = %v, %v", got.DefaultPanic(), padfamily.DefaultPanicHoldMs, want.Buttons, want.Hold)
				}
			})
		}
	}
}
