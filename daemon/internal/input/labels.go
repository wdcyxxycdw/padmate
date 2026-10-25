package input

import "github.com/Zyko0/go-sdl3/sdl"

// Labels come from SDL's open gamepad handle rather than the device's name.
// Unknown labels remain absent. Virtual devices do not prove the printed
// labels of real Joy-Cons, whose physical mapping needs hardware acceptance.
func buttonLabelName(label sdl.GamepadButtonLabel) string {
	switch label {
	case sdl.GAMEPAD_BUTTON_LABEL_A:
		return "A"
	case sdl.GAMEPAD_BUTTON_LABEL_B:
		return "B"
	case sdl.GAMEPAD_BUTTON_LABEL_X:
		return "X"
	case sdl.GAMEPAD_BUTTON_LABEL_Y:
		return "Y"
	case sdl.GAMEPAD_BUTTON_LABEL_CROSS:
		return "Cross"
	case sdl.GAMEPAD_BUTTON_LABEL_CIRCLE:
		return "Circle"
	case sdl.GAMEPAD_BUTTON_LABEL_SQUARE:
		return "Square"
	case sdl.GAMEPAD_BUTTON_LABEL_TRIANGLE:
		return "Triangle"
	default:
		return ""
	}
}
