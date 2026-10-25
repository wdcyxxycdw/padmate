package input

import (
	"strings"
	"testing"
)

func TestWriteHeader(t *testing.T) {
	s := &Source{
		library: `C:\padmate\SDL3.dll`,
		version: "3.4.16",
		hintsRead: []Hint{
			{HintBackgroundEvents, "1"},
			{"SDL_JOYSTICK_HIDAPI", ""},
		},
	}
	var b strings.Builder
	if err := s.WriteHeader(&b, "x: ", "default"); err != nil {
		t.Fatal(err)
	}
	want := `x: SDL 3.4.16 from C:\padmate\SDL3.dll
x: preset default; hints in effect:
x:   SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS=1
x:   SDL_JOYSTICK_HIDAPI unset (SDL's default)
`
	if got := b.String(); got != want {
		t.Errorf("WriteHeader:\n got %q\nwant %q", got, want)
	}
}
