package input

import (
	"fmt"

	"github.com/Zyko0/go-sdl3/sdl"
)

// buttonNames names every SDL gamepad button (ARCHITECTURE §7.1). The
// index is the SDL_GamepadButton value.
//
// paddle1..paddle4 follow the SDL enum order, which is also the order of
// SDL's own mapping-string names "paddle1".."paddle4": RIGHT_PADDLE1,
// LEFT_PADDLE1, RIGHT_PADDLE2, LEFT_PADDLE2. According to SDL_gamepad.h
// these are the Xbox Elite paddles P1, P3, P2 and P4, and on a DualSense
// Edge the RB, LB, right Fn and left Fn buttons.
var buttonNames = [sdl.GAMEPAD_BUTTON_COUNT]string{
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
	sdl.GAMEPAD_BUTTON_RIGHT_PADDLE1:  "paddle1",
	sdl.GAMEPAD_BUTTON_LEFT_PADDLE1:   "paddle2",
	sdl.GAMEPAD_BUTTON_RIGHT_PADDLE2:  "paddle3",
	sdl.GAMEPAD_BUTTON_LEFT_PADDLE2:   "paddle4",
	sdl.GAMEPAD_BUTTON_TOUCHPAD:       "touchpad",
	sdl.GAMEPAD_BUTTON_MISC2:          "misc2",
	sdl.GAMEPAD_BUTTON_MISC3:          "misc3",
	sdl.GAMEPAD_BUTTON_MISC4:          "misc4",
	sdl.GAMEPAD_BUTTON_MISC5:          "misc5",
	sdl.GAMEPAD_BUTTON_MISC6:          "misc6",
}

// axisNames names every SDL gamepad axis. The index is the
// SDL_GamepadAxis value.
var axisNames = [sdl.GAMEPAD_AXIS_COUNT]string{
	sdl.GAMEPAD_AXIS_LEFTX:         "leftx",
	sdl.GAMEPAD_AXIS_LEFTY:         "lefty",
	sdl.GAMEPAD_AXIS_RIGHTX:        "rightx",
	sdl.GAMEPAD_AXIS_RIGHTY:        "righty",
	sdl.GAMEPAD_AXIS_LEFT_TRIGGER:  "lefttrigger",
	sdl.GAMEPAD_AXIS_RIGHT_TRIGGER: "righttrigger",
}

// typeNames names every SDL gamepad type, with SDL's own mapping-string
// names. The index is the SDL_GamepadType value.
var typeNames = [sdl.GAMEPAD_TYPE_COUNT]string{
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

// ButtonName returns the name of SDL gamepad button b, or "button<b>" for
// a value this SDL version does not define.
func ButtonName(b uint8) string {
	if int(b) < len(buttonNames) {
		return buttonNames[b]
	}
	return fmt.Sprintf("button%d", b)
}

// AxisName returns the name of SDL gamepad axis a, or "axis<a>" for a
// value this SDL version does not define.
func AxisName(a uint8) string {
	if int(a) < len(axisNames) {
		return axisNames[a]
	}
	return fmt.Sprintf("axis%d", a)
}

// TypeName returns the short name of SDL gamepad type t, or "type<t>" for
// a value this SDL version does not define.
func TypeName(t int) string {
	if t >= 0 && t < len(typeNames) {
		return typeNames[t]
	}
	return fmt.Sprintf("type%d", t)
}

// MayHaveButton reports whether the gamepad d may have the button named
// name: false for a name that is not a button, and for a gamepad whose
// controls are known (ControlsKnown) and whose ButtonMask lacks it.
func (d DeviceInfo) MayHaveButton(name string) bool {
	i, ok := buttonIndex(name)
	if !ok {
		return false
	}
	return !d.ControlsKnown() || d.ButtonMask&(uint32(1)<<i) != 0
}

// buttonIndex returns the SDL gamepad button named name.
func buttonIndex(name string) (int, bool) {
	for i, n := range buttonNames {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// axisIndex returns the SDL gamepad axis named name.
func axisIndex(name string) (int, bool) {
	for i, n := range axisNames {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// isTrigger reports whether SDL gamepad axis a is a trigger, whose value
// runs from 0 (released) to 32767.
func isTrigger(a int) bool {
	return a == int(sdl.GAMEPAD_AXIS_LEFT_TRIGGER) || a == int(sdl.GAMEPAD_AXIS_RIGHT_TRIGGER)
}
