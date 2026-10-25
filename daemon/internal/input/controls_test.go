package input

import (
	"slices"
	"testing"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// TestButtonNames checks the name of every SDL gamepad button against
// ARCHITECTURE §7.1, and that the table covers the whole enum.
func TestButtonNames(t *testing.T) {
	want := map[sdl.GamepadButton]string{
		sdl.GAMEPAD_BUTTON_SOUTH:          "south",
		sdl.GAMEPAD_BUTTON_EAST:           "east",
		sdl.GAMEPAD_BUTTON_WEST:           "west",
		sdl.GAMEPAD_BUTTON_NORTH:          "north",
		sdl.GAMEPAD_BUTTON_BACK:           "back",
		sdl.GAMEPAD_BUTTON_GUIDE:          "guide",
		sdl.GAMEPAD_BUTTON_START:          "start",
		sdl.GAMEPAD_BUTTON_LEFT_STICK:     "leftstick",
		sdl.GAMEPAD_BUTTON_RIGHT_STICK:    "rightstick",
		sdl.GAMEPAD_BUTTON_LEFT_SHOULDER:  "leftshoulder",
		sdl.GAMEPAD_BUTTON_RIGHT_SHOULDER: "rightshoulder",
		sdl.GAMEPAD_BUTTON_DPAD_UP:        "dpad.up",
		sdl.GAMEPAD_BUTTON_DPAD_DOWN:      "dpad.down",
		sdl.GAMEPAD_BUTTON_DPAD_LEFT:      "dpad.left",
		sdl.GAMEPAD_BUTTON_DPAD_RIGHT:     "dpad.right",
		sdl.GAMEPAD_BUTTON_MISC1:          "misc1",
		// The paddles follow the SDL enum order (Elite P1, P3, P2, P4).
		sdl.GAMEPAD_BUTTON_RIGHT_PADDLE1: "paddle1",
		sdl.GAMEPAD_BUTTON_LEFT_PADDLE1:  "paddle2",
		sdl.GAMEPAD_BUTTON_RIGHT_PADDLE2: "paddle3",
		sdl.GAMEPAD_BUTTON_LEFT_PADDLE2:  "paddle4",
		sdl.GAMEPAD_BUTTON_TOUCHPAD:      "touchpad",
		sdl.GAMEPAD_BUTTON_MISC2:         "misc2",
		sdl.GAMEPAD_BUTTON_MISC3:         "misc3",
		sdl.GAMEPAD_BUTTON_MISC4:         "misc4",
		sdl.GAMEPAD_BUTTON_MISC5:         "misc5",
		sdl.GAMEPAD_BUTTON_MISC6:         "misc6",
	}
	if len(want) != int(sdl.GAMEPAD_BUTTON_COUNT) {
		t.Fatalf("the test lists %d buttons, SDL has %d", len(want), sdl.GAMEPAD_BUTTON_COUNT)
	}
	for b := range sdl.GAMEPAD_BUTTON_COUNT {
		name := ButtonName(uint8(b))
		if name != want[b] {
			t.Errorf("ButtonName(%d) = %q, want %q", b, name, want[b])
		}
		if i, ok := buttonIndex(name); !ok || i != int(b) {
			t.Errorf("buttonIndex(%q) = %d, %t, want %d", name, i, ok, b)
		}
	}
}

// TestNamesMatchControl checks that the names input puts on events are the
// control package's names, which profiles bind: every SDL button is a
// non-trigger control, every control but LT and RT is an SDL button, and
// the axes match in SDL order.
func TestNamesMatchControl(t *testing.T) {
	buttons := make(map[string]bool, len(buttonNames))
	for b, name := range buttonNames {
		c, ok := control.Parse(name)
		if !ok || c.IsTrigger() || c.String() != name {
			t.Errorf("button %d %q: control.Parse = %v, %t", b, name, c, ok)
		}
		buttons[name] = true
	}
	var nonTrigger int
	for _, c := range control.All() {
		if c.IsVirtual() {
			if c.IsTrigger() {
				// The trigger buttons are driven by the SDL trigger axes.
				if i, ok := axisIndex(c.Axis().String()); !ok || !isTrigger(i) {
					t.Errorf("%v.Axis() = %v, which is not an SDL trigger axis", c, c.Axis())
				}
			}
			continue
		}
		nonTrigger++
		if !buttons[c.String()] {
			t.Errorf("control %q is not an SDL button name", c)
		}
	}
	if nonTrigger != len(buttonNames) {
		t.Errorf("control has %d buttons, SDL %d", nonTrigger, len(buttonNames))
	}
	var axes []string
	for _, a := range control.AllAxes() {
		axes = append(axes, a.String())
	}
	if !slices.Equal(axes, axisNames[:]) {
		t.Errorf("control.AllAxes() = %q, want the SDL axis names %q", axes, axisNames)
	}
}

func TestAxisNames(t *testing.T) {
	want := map[sdl.GamepadAxis]string{
		sdl.GAMEPAD_AXIS_LEFTX:         "leftx",
		sdl.GAMEPAD_AXIS_LEFTY:         "lefty",
		sdl.GAMEPAD_AXIS_RIGHTX:        "rightx",
		sdl.GAMEPAD_AXIS_RIGHTY:        "righty",
		sdl.GAMEPAD_AXIS_LEFT_TRIGGER:  "lefttrigger",
		sdl.GAMEPAD_AXIS_RIGHT_TRIGGER: "righttrigger",
	}
	if len(want) != int(sdl.GAMEPAD_AXIS_COUNT) {
		t.Fatalf("the test lists %d axes, SDL has %d", len(want), sdl.GAMEPAD_AXIS_COUNT)
	}
	for a := range sdl.GAMEPAD_AXIS_COUNT {
		name := AxisName(uint8(a))
		if name != want[a] {
			t.Errorf("AxisName(%d) = %q, want %q", a, name, want[a])
		}
		if i, ok := axisIndex(name); !ok || i != int(a) {
			t.Errorf("axisIndex(%q) = %d, %t, want %d", name, i, ok, a)
		}
		trigger := a == sdl.GAMEPAD_AXIS_LEFT_TRIGGER || a == sdl.GAMEPAD_AXIS_RIGHT_TRIGGER
		if isTrigger(int(a)) != trigger {
			t.Errorf("isTrigger(%d) = %t, want %t", a, !trigger, trigger)
		}
	}
}

func TestTypeNames(t *testing.T) {
	want := map[sdl.GamepadType]string{
		sdl.GAMEPAD_TYPE_UNKNOWN:                      "unknown",
		sdl.GAMEPAD_TYPE_STANDARD:                     "standard",
		sdl.GAMEPAD_TYPE_XBOX360:                      "xbox360",
		sdl.GAMEPAD_TYPE_XBOXONE:                      "xboxone",
		sdl.GAMEPAD_TYPE_PS3:                          "ps3",
		sdl.GAMEPAD_TYPE_PS4:                          "ps4",
		sdl.GAMEPAD_TYPE_PS5:                          "ps5",
		sdl.GAMEPAD_TYPE_NINTENDO_SWITCH_PRO:          "switchpro",
		sdl.GAMEPAD_TYPE_NINTENDO_SWITCH_JOYCON_LEFT:  "joyconleft",
		sdl.GAMEPAD_TYPE_NINTENDO_SWITCH_JOYCON_RIGHT: "joyconright",
		sdl.GAMEPAD_TYPE_NINTENDO_SWITCH_JOYCON_PAIR:  "joyconpair",
		sdl.GAMEPAD_TYPE_GAMECUBE:                     "gamecube",
	}
	if len(want) != int(sdl.GAMEPAD_TYPE_COUNT) {
		t.Fatalf("the test lists %d types, SDL has %d", len(want), sdl.GAMEPAD_TYPE_COUNT)
	}
	for ty := range sdl.GAMEPAD_TYPE_COUNT {
		if got := TypeName(int(ty)); got != want[ty] {
			t.Errorf("TypeName(%d) = %q, want %q", ty, got, want[ty])
		}
	}
}

// TestUnknownControls checks the names of values that SDL 3.4 does not
// define, which a newer SDL could send.
func TestUnknownControls(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{ButtonName(uint8(sdl.GAMEPAD_BUTTON_COUNT)), "button26"},
		{ButtonName(255), "button255"},
		{AxisName(uint8(sdl.GAMEPAD_AXIS_COUNT)), "axis6"},
		{AxisName(255), "axis255"},
		{TypeName(int(sdl.GAMEPAD_TYPE_COUNT)), "type12"},
		{TypeName(-1), "type-1"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
	for _, name := range []string{"", "a", "button26", "South", "lt"} {
		if _, ok := buttonIndex(name); ok {
			t.Errorf("buttonIndex(%q) found a button", name)
		}
		if _, ok := axisIndex(name); ok {
			t.Errorf("axisIndex(%q) found an axis", name)
		}
	}
}

func TestEventKindString(t *testing.T) {
	for _, tc := range []struct {
		k    EventKind
		want string
	}{
		{DeviceAdded, "added"},
		{DeviceRemoved, "removed"},
		{ButtonDown, "down"},
		{ButtonUp, "up"},
		{AxisMotion, "axis"},
		{ComboHeld, "combo"},
		{DeviceState, "state"},
		{DevicePower, "power"},
		{DeviceRemapped, "remapped"},
		{0, "kind0"},
		{DeviceRemapped + 1, "kind10"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", uint8(tc.k), got, tc.want)
		}
	}
}

// TestTriggerRaw checks the conversion of trigger values to the raw axis
// values that SDL's default gamepad mapping maps back to them.
func TestTriggerRaw(t *testing.T) {
	for _, tc := range []struct{ v, want int16 }{
		{0, -32767},
		{-1, -32767},
		{-32768, -32767},
		{16384, 1},
		{32767, 32767},
	} {
		if got := triggerRaw(tc.v); got != tc.want {
			t.Errorf("triggerRaw(%d) = %d, want %d", tc.v, got, tc.want)
		}
	}
}
