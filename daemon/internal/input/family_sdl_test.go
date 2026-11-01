package input_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// TestVirtualFamilyControls checks the type and the controls Source
// reports for each pad family that SDL's virtual gamepads model. The masks
// are those of app/src/renderer/testing/sdl-pads.ts, from which the mapping
// page's tests decide each pad's chips; change both together.
func TestVirtualFamilyControls(t *testing.T) {
	for _, tc := range []struct {
		name, typ     string
		desc          input.VirtualDesc
		buttons, axes uint32
	}{
		{"DualShock 4", "ps4", inputtest.DualShock4("padmate family DS4"), 0x107fff, 0x3f},
		{"DualSense", "ps5", inputtest.DualSense("padmate family DualSense"), 0x10ffff, 0x3f},
		{"DualSense Edge", "ps5", inputtest.DualSenseEdge("padmate family Edge"), 0x1fffff, 0x3f},
		{"Switch Pro", "switchpro", inputtest.SwitchPro("padmate family Pro"), 0xffff, 0x3f},
		{"Joy-Con pair", "joyconpair", inputtest.JoyConPair("padmate family pair"), 0xfffff, 0x3f},
		{"Joy-Con (L) sideways", "joyconleft", inputtest.JoyConLeft("padmate family L"), 0xa06ef, 0x3},
		{"Joy-Con (R) sideways", "joyconright", inputtest.JoyConRight("padmate family R"), 0x506ef, 0x3},
		{"Joy-Con (L) upright", "joyconleft", inputtest.JoyConLeftUpright("padmate family L up"), 0xafa90, 0x13},
		{"Joy-Con (R) upright", "joyconright", inputtest.JoyConRightUpright("padmate family R up"), 0x5056f, 0x2c},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t, nil)
			v, err := s.AttachVirtualDesc(tc.desc)
			if err != nil {
				t.Fatal(err)
			}
			info := collect(t, s, v.ID(), 1)[0].Info
			if info == nil {
				t.Fatal("no DeviceInfo")
			}
			if info.Type != tc.typ || info.ButtonMask != tc.buttons || uint32(info.AxisMask) != tc.axes {
				t.Fatalf("type %s, buttons %#x, axes %#x; want %s, %#x, %#x",
					info.Type, info.ButtonMask, info.AxisMask, tc.typ, tc.buttons, tc.axes)
			}
			// The Xbox, PS or Home button is never mapped, but SDL reports
			// it where the pad has one.
			if info.MayHaveButton("guide") != (tc.buttons&(1<<5) != 0) {
				t.Errorf("MayHaveButton(guide) = %v", info.MayHaveButton("guide"))
			}
		})
	}
}
