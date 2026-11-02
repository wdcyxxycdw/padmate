package inputtest

import "github.com/wdcyxxycdw/padmate/daemon/internal/input"

// Nintendo's USB ids for Switch 1 Joy-Cons (SDL's usb_ids.h). The Switch 2
// ids (L 0x2067, R 0x2066, pair 0x2068) are not modeled.
const (
	VendorNintendo     = 0x057e
	ProductJoyConLeft  = 0x2006
	ProductJoyConRight = 0x2007
	ProductJoyConPair  = 0x2008
)

// JoyConPair describes a combined pair of Joy-Cons, as SDL 3.4.16 makes of a
// left and a right one connected together (SDL_gamepad.c, the HIDAPI Switch
// mappings, as read from the source). It models SDL's mapping, not the device:
// no report parsing, rotation or serial, and SDL pairs only HIDAPI Joy-Cons, so
// virtual ones are never combined.
func JoyConPair(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name:    name,
		Vendor:  VendorNintendo,
		Product: ProductJoyConPair,
		Buttons: []string{
			"south", "east", "west", "north", "back", "guide", "start",
			"leftstick", "rightstick", "leftshoulder", "rightshoulder",
			"dpad.up", "dpad.down", "dpad.left", "dpad.right",
			"misc1", "paddle1", "paddle2", "paddle3", "paddle4",
		},
		Axes: []string{"leftx", "lefty", "rightx", "righty", "lefttrigger", "righttrigger"},
	}
}

// JoyConLeft and JoyConRight describe a single Joy-Con held
// upright, which is how padmate always holds it, as SDL 3.4.16 maps it with
// SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=1: the left one has − (back), the
// D-pad, L and ZL, Capture (misc1) and its SL and SR as paddles; the right
// one + (start), the face buttons, R and ZR and its SR and SL.
func JoyConLeft(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name:    name,
		Vendor:  VendorNintendo,
		Product: ProductJoyConLeft,
		Buttons: []string{
			"back", "leftstick", "leftshoulder",
			"dpad.up", "dpad.down", "dpad.left", "dpad.right", "misc1", "paddle2", "paddle4",
		},
		Axes: []string{"leftx", "lefty", "lefttrigger"},
	}
}

// JoyConRight describes an upright Joy-Con (R); see
// JoyConLeft.
func JoyConRight(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name:    name,
		Vendor:  VendorNintendo,
		Product: ProductJoyConRight,
		Buttons: []string{
			"south", "east", "west", "north", "guide", "start",
			"rightshoulder", "rightstick", "paddle1", "paddle3",
		},
		Axes: []string{"rightx", "righty", "righttrigger"},
	}
}
