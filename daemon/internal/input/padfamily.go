package input

import "github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"

// FamilyOf returns the family of the gamepad info (ADR-0017): which button
// can be its function key and what its built-in emergency combo is. A pair of
// Joy-Cons is joyconpair; a single Joy-Con (joyconleft, joyconright) is held
// upright, always (SDL gets SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS, and the
// Joy-Con 2 driver maps the same way), so its family follows from its type
// alone, not from the buttons SDL gives it. Every other gamepad has the
// standard layout.
func FamilyOf(info DeviceInfo) padfamily.Family {
	switch info.Type {
	case "joyconpair":
		return padfamily.JoyConPair
	case "joyconleft":
		return padfamily.JoyConLeft
	case "joyconright":
		return padfamily.JoyConRight
	}
	return padfamily.Standard
}
