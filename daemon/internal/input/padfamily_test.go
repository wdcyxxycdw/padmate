package input_test

import (
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
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
		name string
		info input.DeviceInfo
		want padfamily.Family
	}{
		{"xbox", input.DeviceInfo{Type: "xboxone"}, padfamily.Standard},
		{"ps5", input.DeviceInfo{Type: "ps5"}, padfamily.Standard},
		{"switch pro", input.DeviceInfo{Type: "switchpro"}, padfamily.Standard},
		{"unknown type", input.DeviceInfo{Type: "unknown"}, padfamily.Standard},
		{"empty type", input.DeviceInfo{}, padfamily.Standard},
		{"pair", input.DeviceInfo{Type: "joyconpair"}, padfamily.JoyConPair},
		// A single Joy-Con is always upright: its type alone decides, whatever
		// buttons are known (a trace recorded sideways still replays).
		{"left", input.DeviceInfo{Type: "joyconleft"}, padfamily.JoyConLeft},
		{"right", input.DeviceInfo{Type: "joyconright"}, padfamily.JoyConRight},
		{"left, sideways buttons", input.DeviceInfo{Type: "joyconleft", ButtonMask: 0xa06ef}, padfamily.JoyConLeft},
		{"right, sideways buttons", input.DeviceInfo{Type: "joyconright", ButtonMask: 0x506ef}, padfamily.JoyConRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := input.FamilyOf(tc.info); got != tc.want {
				t.Errorf("FamilyOf = %s; want %s", got, tc.want)
			}
		})
	}
}

// TestFamilyMatchesThePad: for the virtual pads that model SDL's Switch 1
// mapping and for the Joy-Con 2's own mapping, the family is the one of the
// pad's type; what it offers as a function key is a control the pad has
// (a button, or the axis of a trigger); its buttons are all known to
// the family (HasButton); and its built-in combo matches input.DefaultCombo,
// which the family's table duplicates.
func TestFamilyMatchesThePad(t *testing.T) {
	type pad struct {
		name    string
		typ     string
		buttons []string
		axes    []string
		want    padfamily.Family
	}
	left, right := inputtest.JoyConLeft("l"), inputtest.JoyConRight("r")
	pads := []pad{
		{"SDL left", "joyconleft", left.Buttons, left.Axes, padfamily.JoyConLeft},
		{"SDL right", "joyconright", right.Buttons, right.Axes, padfamily.JoyConRight},
		{"SDL pair", "joyconpair", inputtest.JoyConPair("p").Buttons, inputtest.JoyConPair("p").Axes, padfamily.JoyConPair},
		{"Joy-Con 2 left", "joyconleft", jc2.Controls(jc2.Vertical, jc2.SideL), jc2.Axes(jc2.Vertical, jc2.SideL), padfamily.JoyConLeft},
		{"Joy-Con 2 right", "joyconright", jc2.Controls(jc2.Vertical, jc2.SideR), jc2.Axes(jc2.Vertical, jc2.SideR), padfamily.JoyConRight},
		{"Joy-Con 2 pair", "joyconpair", jc2.Controls(jc2.Pair, jc2.SideL), jc2.Axes(jc2.Pair, jc2.SideL), padfamily.JoyConPair},
	}
	for _, p := range pads {
		t.Run(p.name, func(t *testing.T) {
			info := input.DeviceInfo{Type: p.typ, ButtonMask: maskOf(t, p.buttons)}
			got := input.FamilyOf(info)
			if got != p.want {
				t.Fatalf("FamilyOf = %s; want %s", got, p.want)
			}
			for _, c := range got.FunctionKeys().Options {
				has := slices.Contains(p.buttons, c.String())
				if c.IsTrigger() {
					has = slices.Contains(p.axes, c.Axis().String())
				}
				if !has {
					t.Errorf("function key %s is not a control of the pad: buttons %v, axes %v", c, p.buttons, p.axes)
				}
			}
			for _, b := range p.buttons {
				if !got.HasButton(b) {
					t.Errorf("the pad has the button %s, which the family %s does not know", b, got)
				}
			}
			want := input.DefaultCombo(input.DeviceInfo{Type: p.typ})
			if !slices.Equal(got.DefaultPanic(), want.Buttons) || padfamily.DefaultPanicHoldMs != int(want.Hold.Milliseconds()) {
				t.Errorf("DefaultPanic = %v, hold %d ms; DefaultCombo = %v, %v", got.DefaultPanic(), padfamily.DefaultPanicHoldMs, want.Buttons, want.Hold)
			}
			for _, b := range got.DefaultPanic() {
				if !slices.Contains(p.buttons, b) {
					t.Errorf("the built-in combo has %s, which the pad lacks", b)
				}
			}
		})
	}
}

// TestFunctionKeyIsATriggerOrShoulder: a single Joy-Con's choices are the
// trigger (the default) and the shoulder button on its side, nothing else.
func TestFunctionKeyIsATriggerOrShoulder(t *testing.T) {
	if got := padfamily.JoyConLeft.FunctionKeys().Options; !slices.Equal(got, []control.Control{control.LT, control.LeftShoulder}) {
		t.Errorf("left: %v", got)
	}
	if got := padfamily.JoyConRight.FunctionKeys().Options; !slices.Equal(got, []control.Control{control.RT, control.RightShoulder}) {
		t.Errorf("right: %v", got)
	}
}
