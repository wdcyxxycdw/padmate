package inputtest

import "github.com/wdcyxxycdw/padmate/daemon/internal/input"

// Sony's USB ids (SDL's usb_ids.h): the DualShock 4 (second model, also
// what GameSir's Nova 2 Lite says it is in PS4 mode), the DualSense and the
// DualSense Edge.
const (
	VendorSony           = 0x054c
	ProductDualShock4    = 0x09cc
	ProductDualSense     = 0x0ce6
	ProductDualSenseEdge = 0x0df2
)

// standardButtons are the 15 buttons of SDL's standard gamepad.
var standardButtons = []string{
	"south", "east", "west", "north", "back", "guide", "start",
	"leftstick", "rightstick", "leftshoulder", "rightshoulder",
	"dpad.up", "dpad.down", "dpad.left", "dpad.right",
}

// allAxes are the six axes of SDL's gamepad.
var allAxes = []string{"leftx", "lefty", "rightx", "righty", "lefttrigger", "righttrigger"}

// DualShock4, DualSense and DualSenseEdge describe virtual gamepads with
// the ids and controls that SDL 3.4.16 gives these pads: the standard ones
// and the touchpad's click; on a DualSense the mute button too (misc1), on
// an Edge its back and Fn buttons (paddle1 to paddle4). They model SDL's
// mapping (SDL_gamepad.c), not the device: no touch positions, sensors or
// reports.
func DualShock4(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name: name, Vendor: VendorSony, Product: ProductDualShock4,
		Buttons: append(append([]string{}, standardButtons...), "touchpad"),
		Axes:    allAxes,
	}
}

// DualSense describes a DualSense; see DualShock4.
func DualSense(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name: name, Vendor: VendorSony, Product: ProductDualSense,
		Buttons: append(append([]string{}, standardButtons...), "touchpad", "misc1"),
		Axes:    allAxes,
	}
}

// DualSenseEdge describes a DualSense Edge; see DualShock4.
func DualSenseEdge(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name: name, Vendor: VendorSony, Product: ProductDualSenseEdge,
		Buttons: append(append([]string{}, standardButtons...),
			"touchpad", "misc1", "paddle1", "paddle2", "paddle3", "paddle4"),
		Axes: allAxes,
	}
}
