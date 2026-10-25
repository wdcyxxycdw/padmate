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

// JoyConLeft, JoyConRight and JoyConPair describe virtual gamepads with
// the ids and controls that SDL 3.4.16 gives a Switch 1 Joy-Con with
// padmate's hints: one alone, held sideways, and the pair that
// "--hint SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=1" gives (SDL_gamepad.c,
// the HIDAPI Switch mappings, as read from the source). They model SDL's
// mapping, not the device: no report parsing, rotation or serial.
func JoyConLeft(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name:    name,
		Vendor:  VendorNintendo,
		Product: ProductJoyConLeft,
		Buttons: []string{
			"south", "east", "west", "north", "guide", "start", "leftstick",
			"leftshoulder", "rightshoulder", "paddle2", "paddle4",
		},
		Axes: []string{"leftx", "lefty"},
	}
}

// JoyConRight describes a sideways Joy-Con (R); see JoyConLeft.
func JoyConRight(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name:    name,
		Vendor:  VendorNintendo,
		Product: ProductJoyConRight,
		Buttons: []string{
			"south", "east", "west", "north", "guide", "start", "leftstick",
			"leftshoulder", "rightshoulder", "paddle1", "paddle3",
		},
		Axes: []string{"leftx", "lefty"},
	}
}

// JoyConPair describes a combined pair of Joy-Cons; see JoyConLeft.
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
