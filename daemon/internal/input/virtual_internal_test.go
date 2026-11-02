package input

import (
	"testing"

	"github.com/Zyko0/go-sdl3/sdl"
)

// joyConL is the button list of inputtest.JoyConLeft, which cannot be
// imported here; TestJoyConLCopy keeps the two equal.
var joyConL = []string{
	"back", "leftstick", "leftshoulder",
	"dpad.up", "dpad.down", "dpad.left", "dpad.right", "misc1", "paddle2", "paddle4",
}

// bitsOf returns the mask with the bits bs.
func bitsOf(bs ...int) uint32 {
	var m uint32
	for _, b := range bs {
		m |= 1 << b
	}
	return m
}

func TestControlMask(t *testing.T) {
	const nb, na = int(sdl.GAMEPAD_BUTTON_COUNT), int(sdl.GAMEPAD_AXIS_COUNT)
	button := func(names ...string) (uint32, error) { return controlMask("button", names, buttonIndex, nb) }
	axis := func(names ...string) (uint32, error) { return controlMask("axis", names, axisIndex, na) }
	for _, tc := range []struct {
		name  string
		mask  func(...string) (uint32, error)
		names []string
		want  uint32
		err   string // the error, or "" for none
	}{
		{"nil buttons", button, nil, 1<<26 - 1, ""},
		{"nil axes", axis, nil, 1<<6 - 1, ""},
		{"no buttons", button, []string{}, 0, ""},
		{"no axes", axis, []string{}, 0, ""},
		{"Joy-Con L", button, joyConL, bitsOf(4, 7, 9, 11, 12, 13, 14, 15, 17, 19), ""},
		{"sticks", axis, []string{"lefty", "leftx"}, bitsOf(0, 1), ""},
		{"unknown", button, []string{"south", "sl"}, 0, `unknown button "sl"`},
		{"repeated", button, []string{"south", "east", "south"}, 0, `button "south" is listed twice`},
		{"axis among buttons", button, []string{"south", "leftx"}, 0, `unknown button "leftx"`},
		{"button among axes", axis, []string{"leftx", "south"}, 0, `unknown axis "south"`},
		{"repeated axis", axis, []string{"leftx", "leftx"}, 0, `axis "leftx" is listed twice`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.mask(tc.names...)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("controlMask(%q) = %#x, %v; want error %q", tc.names, got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("controlMask(%q) = %#x, %v; want %#x", tc.names, got, err, tc.want)
			}
		})
	}
}

func TestPackIndex(t *testing.T) {
	// want returns the packing that maps the controls named to 0, 1, ...
	// in order and every other one to -1.
	want := func(index func(string) (int, bool), names ...string) [32]int8 {
		var p [32]int8
		for i := range p {
			p[i] = -1
		}
		for j, name := range names {
			i, ok := index(name)
			if !ok {
				t.Fatalf("unknown control %q", name)
			}
			p[i] = int8(j)
		}
		return p
	}
	var identity [32]int8
	for i := range identity {
		identity[i] = -1
		if i < int(sdl.GAMEPAD_BUTTON_COUNT) {
			identity[i] = int8(i)
		}
	}
	var none [32]int8
	for i := range none {
		none[i] = -1
	}
	joyConMask, err := controlMask("button", joyConL, buttonIndex, int(sdl.GAMEPAD_BUTTON_COUNT))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		mask uint32
		want [32]int8
	}{
		{"all buttons", 1<<sdl.GAMEPAD_BUTTON_COUNT - 1, identity},
		// The list is in SDL enum order, so the packed index is the
		// position in it: back 0, ..., paddle4 9.
		{"Joy-Con L", joyConMask, want(buttonIndex, joyConL...)},
		{"sticks", bitsOf(0, 1), want(axisIndex, "leftx", "lefty")},
		{"left trigger", bitsOf(int(sdl.GAMEPAD_AXIS_LEFT_TRIGGER)), want(axisIndex, "lefttrigger")},
		{"none", 0, none},
	} {
		if got := packIndex(tc.mask); got != tc.want {
			t.Errorf("%s: packIndex(%#x) = %v, want %v", tc.name, tc.mask, got, tc.want)
		}
	}
	// Spot-check the Joy-Con L by name, the buttons it lacks included.
	p := packIndex(joyConMask)
	for name, j := range map[string]int8{
		"back": 0, "leftstick": 1, "leftshoulder": 2,
		"dpad.up": 3, "dpad.down": 4, "dpad.left": 5, "dpad.right": 6, "misc1": 7, "paddle2": 8, "paddle4": 9,
		"south": -1, "east": -1, "west": -1, "north": -1, "guide": -1, "start": -1,
		"rightstick": -1, "rightshoulder": -1, "paddle1": -1, "paddle3": -1,
	} {
		if i, _ := buttonIndex(name); p[i] != j {
			t.Errorf("Joy-Con L: %s packs to %d, want %d", name, p[i], j)
		}
	}
}
