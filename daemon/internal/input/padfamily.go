package input

import "github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"

// FamilyOf returns the family of the gamepad info (ADR-0017): which button
// can be its function key and what its built-in emergency combo is. A pair of
// Joy-Cons is joyconpair; a single Joy-Con is held sideways or upright. When
// its controls are known, the grip is judged by the buttons SDL gives it, as
// the UI's isSidewaysJoyCon and padlabel do: it has a south button and no
// right stick when held sideways (the face buttons are the four directions or
// A/B/X/Y, and its one stick is the left stick), and an upright right one has
// face buttons too but the right stick. Without known controls, vertical, the
// hint SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS (Source.VerticalJoyCons), decides,
// as it does for the emergency combo (DefaultCombo). Every other gamepad has
// the standard layout.
func FamilyOf(info DeviceInfo, vertical bool) padfamily.Family {
	switch info.Type {
	case "joyconpair":
		return padfamily.JoyConPair
	case "joyconleft", "joyconright":
		sideways := !vertical
		if info.ButtonMask != 0 {
			south, _ := buttonIndex("south")
			rightStick, _ := buttonIndex("rightstick")
			sideways = info.ButtonMask&(1<<south) != 0 && info.ButtonMask&(1<<rightStick) == 0
		}
		switch {
		case info.Type == "joyconleft" && sideways:
			return padfamily.JoyConLeft
		case info.Type == "joyconleft":
			return padfamily.JoyConLeftVertical
		case sideways:
			return padfamily.JoyConRight
		default:
			return padfamily.JoyConRightVertical
		}
	}
	return padfamily.Standard
}
