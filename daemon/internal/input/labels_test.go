package input

import (
	"testing"

	"github.com/Zyko0/go-sdl3/sdl"
)

func TestButtonLabelNames(t *testing.T) {
	for _, tc := range []struct {
		value sdl.GamepadButtonLabel
		name  string
	}{
		{sdl.GAMEPAD_BUTTON_LABEL_A, "A"}, {sdl.GAMEPAD_BUTTON_LABEL_B, "B"}, {sdl.GAMEPAD_BUTTON_LABEL_X, "X"}, {sdl.GAMEPAD_BUTTON_LABEL_Y, "Y"},
		{sdl.GAMEPAD_BUTTON_LABEL_CROSS, "Cross"}, {sdl.GAMEPAD_BUTTON_LABEL_CIRCLE, "Circle"}, {sdl.GAMEPAD_BUTTON_LABEL_SQUARE, "Square"}, {sdl.GAMEPAD_BUTTON_LABEL_TRIANGLE, "Triangle"}, {sdl.GAMEPAD_BUTTON_LABEL_UNKNOWN, ""},
	} {
		if got := buttonLabelName(tc.value); got != tc.name {
			t.Errorf("%d: %q", tc.value, got)
		}
	}
}
