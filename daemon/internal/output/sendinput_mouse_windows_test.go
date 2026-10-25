package output

import (
	"math"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// A wheel press is an INPUT_MOUSE of one notch: no movement, no position, the
// wheel flag of its axis and mouseData of +-120 as a DWORD, and the same
// dwExtraInfo tag as the keys (ADR-0062).
func TestMakeMouseInput(t *testing.T) {
	const extra = uintptr(0x7061646D_00001234)
	tests := []struct {
		name  string
		mouse keys.Mouse
		flags uint32
		data  uint32
	}{
		{"WheelUp", keys.WheelUp, 0x0800, 120},            // MOUSEEVENTF_WHEEL
		{"WheelDown", keys.WheelDown, 0x0800, 0xFFFFFF88}, // -120
		{"WheelLeft", keys.WheelLeft, 0x1000, 0xFFFFFF88}, // MOUSEEVENTF_HWHEEL, -120
		{"WheelRight", keys.WheelRight, 0x1000, 120},      // MOUSEEVENTF_HWHEEL
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := KeyEventFor(mustKey(t, tt.name), false)
			if err != nil {
				t.Fatal(err)
			}
			if e.Mouse != tt.mouse {
				t.Fatalf("KeyEventFor(%s).Mouse = %v", tt.name, e.Mouse)
			}
			in := makeInput(e, extra)
			if in.Type != inputMouse {
				t.Errorf("Type = %d, want INPUT_MOUSE (0)", in.Type)
			}
			want := mouseInput{MouseData: tt.data, Flags: tt.flags, ExtraInfo: extra}
			if got := *in.mouse(); got != want {
				t.Errorf("MOUSEINPUT = %+v, want %+v", got, want)
			}
			// Spelled out: no MOVE (0x0001), ABSOLUTE (0x8000) or VIRTUALDESK
			// (0x4000) flag, no button flags and no coordinates.
			if mi := in.mouse(); mi.Flags&^(0x0800|0x1000) != 0 || mi.Dx != 0 || mi.Dy != 0 || mi.Time != 0 {
				t.Errorf("MOUSEINPUT %+v moves the cursor or sets more than the wheel", *mi)
			}
		})
	}
}

// A button press or release is an INPUT_MOUSE with the button's own down or up
// flag (MOUSEEVENTF_LEFTDOWN 0x0002, LEFTUP 0x0004, RIGHTDOWN 0x0008, RIGHTUP
// 0x0010, MIDDLEDOWN 0x0020, MIDDLEUP 0x0040, XDOWN 0x0080, XUP 0x0100), and for
// a side button XBUTTON1 (1) or XBUTTON2 (2) in mouseData. Nothing moves the
// cursor: no MOUSEEVENTF_MOVE, no ABSOLUTE or VIRTUALDESK flag, no position,
// no delta, and the tag is the keys'.
func TestMakeMouseButtonInput(t *testing.T) {
	const extra = uintptr(0x7061646D_00005678)
	tests := []struct {
		name     string
		mouse    keys.Mouse
		down, up uint32
		data     uint32
	}{
		{"MouseLeft", keys.MouseLeft, 0x0002, 0x0004, 0},
		{"MouseRight", keys.MouseRight, 0x0008, 0x0010, 0},
		{"MouseMiddle", keys.MouseMiddle, 0x0020, 0x0040, 0},
		{"MouseX1", keys.MouseX1, 0x0080, 0x0100, 1},
		{"MouseX2", keys.MouseX2, 0x0080, 0x0100, 2},
	}
	for _, tt := range tests {
		for _, up := range []bool{false, true} {
			name := tt.name + " down"
			flags := tt.down
			if up {
				name, flags = tt.name+" up", tt.up
			}
			t.Run(name, func(t *testing.T) {
				e := batchEvent(t, tt.name, up)
				if e.Mouse != tt.mouse || e.Silent() || e.momentary() {
					t.Fatalf("KeyEventFor(%s, up %v) = %+v, want the %v button event, not silent", tt.name, up, e, tt.mouse)
				}
				in := makeInput(e, extra)
				if in.Type != inputMouse {
					t.Errorf("Type = %d, want INPUT_MOUSE (0)", in.Type)
				}
				want := mouseInput{MouseData: tt.data, Flags: flags, ExtraInfo: extra}
				if got := *in.mouse(); got != want {
					t.Errorf("MOUSEINPUT = %+v, want %+v", got, want)
				}
				// MOVE 0x0001, WHEEL 0x0800, HWHEEL 0x1000, VIRTUALDESK 0x4000 and
				// ABSOLUTE 0x8000 are all clear, and so are the coordinates.
				if mi := in.mouse(); mi.Flags&(0x0001|0x0800|0x1000|0x4000|0x8000) != 0 || mi.Dx != 0 || mi.Dy != 0 || mi.Time != 0 {
					t.Errorf("MOUSEINPUT %+v moves the cursor or turns the wheel", *mi)
				}
			})
		}
	}
	// The two side buttons share their flags and differ only in mouseData.
	x1, x2 := makeInput(batchEvent(t, "XButton1", false), 0), makeInput(batchEvent(t, "XButton2", false), 0)
	if x1.mouse().Flags != x2.mouse().Flags || x1.mouse().MouseData == x2.mouse().MouseData {
		t.Errorf("X1 %+v and X2 %+v: want the same flags and different mouseData", *x1.mouse(), *x2.mouse())
	}
}

// A button event always makes an input, the release too (only a wheel
// release is silent), so a click is two inputs in the batch's order, between
// a chord's keyboard inputs, and Send's count of injected events follows the
// inputs one for one.
func TestButtonBatchInputs(t *testing.T) {
	ev := func(spec string, up bool) KeyEvent { return batchEvent(t, spec, up) }
	chord := []KeyEvent{ev("LShift", false), ev("MouseLeft", false), ev("MouseLeft", true), ev("LShift", true)}
	inputs, before := batchInputs(chord, 5)
	var types []uint32
	for _, in := range inputs {
		types = append(types, in.Type)
	}
	if !slices.Equal(types, []uint32{inputKeyboard, inputMouse, inputMouse, inputKeyboard}) || !slices.Equal(before, []int{0, 1, 2, 3}) {
		t.Fatalf("batchInputs = types %v, before %v; want keyboard, mouse, mouse, keyboard and [0 1 2 3]", types, before)
	}
	if got := []uint32{inputs[1].mouse().Flags, inputs[2].mouse().Flags}; !slices.Equal(got, []uint32{0x0002, 0x0004}) {
		t.Errorf("flags of the click = %#x, want LEFTDOWN then LEFTUP", got)
	}
	for n, want := range []int{0, 1, 2, 3, 4} {
		if got := injectedEvents(chord, before, n); got != want {
			t.Errorf("injectedEvents(n=%d) = %d, want %d: a button release is no silent event", n, got, want)
		}
	}
	// A release alone is an input, so a batch of button releases is sent.
	rel := []KeyEvent{ev("MouseLeft", true), ev("MouseX2", true)}
	if inputs, _ := batchInputs(rel, 0); len(inputs) != 2 {
		t.Errorf("batchInputs(button releases) made %d inputs, want 2", len(inputs))
	}
}

func batchEvent(t *testing.T, spec string, up bool) KeyEvent {
	t.Helper()
	e, err := KeyEventFor(mustKey(t, spec), up)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBatchInputs(t *testing.T) {
	ev := func(spec string, up bool) KeyEvent { return batchEvent(t, spec, up) }
	tests := []struct {
		name   string
		events []KeyEvent
		types  []uint32 // the Type of each input
		before []int
	}{
		{"keyboard only", []KeyEvent{ev("Z", false), ev("Z", true)}, []uint32{inputKeyboard, inputKeyboard}, []int{0, 1}},
		{"wheel chord in one batch", []KeyEvent{ev("LCtrl", false), ev("WheelUp", false), ev("WheelUp", true), ev("LCtrl", true)},
			[]uint32{inputKeyboard, inputMouse, inputKeyboard}, []int{0, 1, 2, 2}},
		{"wheel releases alone", []KeyEvent{ev("WheelDown", true), ev("WheelLeft", true)}, nil, []int{0, 0}},
		{"release first", []KeyEvent{ev("WheelRight", true), ev("Z", false)}, []uint32{inputKeyboard}, []int{0, 0}},
		{"two notches", []KeyEvent{ev("WheelUp", false), ev("WheelUp", false), ev("WheelUp", true)}, []uint32{inputMouse, inputMouse}, []int{0, 1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs, before := batchInputs(tt.events, 7)
			var types []uint32
			for _, in := range inputs {
				types = append(types, in.Type)
			}
			if !slices.Equal(types, tt.types) || !slices.Equal(before, tt.before) {
				t.Errorf("batchInputs = types %v, before %v; want %v, %v", types, before, tt.types, tt.before)
			}
			for i, in := range inputs {
				extra := in.Ki.ExtraInfo
				if in.Type == inputMouse {
					extra = in.mouse().ExtraInfo
				}
				if extra != 7 {
					t.Errorf("input %d carries dwExtraInfo %d, want the tag 7", i, extra)
				}
			}
		})
	}
}

// Send reports how many of the caller's events were injected, always a
// prefix: a silent release counts when every input before it was injected.
func TestInjectedEvents(t *testing.T) {
	ev := func(spec string, up bool) KeyEvent { return batchEvent(t, spec, up) }
	chord := []KeyEvent{ev("LCtrl", false), ev("WheelUp", false), ev("WheelUp", true), ev("LCtrl", true)} // 3 inputs
	tests := []struct {
		name   string
		events []KeyEvent
		n      int // inputs SendInput accepted
		want   int
	}{
		{"chord, none", chord, 0, 0},
		{"chord, Ctrl down only", chord, 1, 1},
		{"chord, Ctrl down and the notch", chord, 2, 3}, // the release between counts
		{"chord, all", chord, 3, 4},
		{"release then key, none", []KeyEvent{ev("WheelUp", true), ev("Z", false)}, 0, 1},
		{"release then key, all", []KeyEvent{ev("WheelUp", true), ev("Z", false)}, 1, 2},
		{"release after a failed key", []KeyEvent{ev("Z", false), ev("WheelUp", true), ev("X", false)}, 0, 0},
		{"release after an injected key", []KeyEvent{ev("Z", false), ev("WheelUp", true), ev("X", false)}, 1, 2},
		{"releases alone", []KeyEvent{ev("WheelUp", true), ev("WheelDown", true)}, 0, 2},
		{"trailing release, last input failed", []KeyEvent{ev("Z", false), ev("WheelUp", false), ev("WheelUp", true)}, 1, 1},
		{"trailing release, all inputs", []KeyEvent{ev("Z", false), ev("WheelUp", false), ev("WheelUp", true)}, 2, 3},
		{"keyboard only, partial", []KeyEvent{ev("Z", false), ev("X", false), ev("X", true)}, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs, before := batchInputs(tt.events, 0)
			if tt.n > len(inputs) {
				t.Fatalf("test bug: %d inputs accepted of %d", tt.n, len(inputs))
			}
			if got := injectedEvents(tt.events, before, tt.n); got != tt.want {
				t.Errorf("injectedEvents(n=%d) = %d, want %d", tt.n, got, tt.want)
			}
		})
	}
}

// A batch of wheel releases alone makes no input, so Send makes no SendInput
// call and reports every event injected. The test checks it through
// batchInputs rather than Send: if silent events ever made inputs, calling the
// real Send here would turn the wheel on the machine running the tests.
func TestSendInputOnlySilentEvents(t *testing.T) {
	var events []KeyEvent
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		events = append(events, batchEvent(t, name, true))
	}
	inputs, before := batchInputs(events, ExtraInfo(1))
	if len(inputs) != 0 {
		t.Fatalf("batchInputs(releases of wheel keys) made %d inputs, want none", len(inputs))
	}
	if n := injectedEvents(events, before, 0); n != len(events) {
		t.Errorf("injectedEvents(releases of wheel keys, 0 inputs) = %d, want %d", n, len(events))
	}
}

// A pointer move is one relative MOUSEEVENTF_MOVE with the distance in dx and
// dy, tagged, and nothing else: no absolute position, no mouseData and no other
// flag. The mouse canary of the injection tests is a pair of them, +1 then -1
// along x: a hook sees them (a wheel event of no distance, and a move of no
// distance, never reach one, measured 2026-10-19), and if they ever leak the
// cursor ends where it was.
func TestMakeMouseInputMove(t *testing.T) {
	const extra = uintptr(0x7061646D_00000042)
	for _, d := range []struct{ dx, dy int32 }{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {12, -7}, {-500, 500}, {math.MaxInt32, math.MinInt32}} {
		in := makeInput(KeyEvent{Mouse: keys.MouseMove, Dx: d.dx, Dy: d.dy}, extra)
		want := mouseInput{Dx: d.dx, Dy: d.dy, Flags: 0x0001, ExtraInfo: extra} // MOUSEEVENTF_MOVE
		if in.Type != inputMouse || *in.mouse() != want {
			t.Errorf("move %d,%d: type %d, MOUSEINPUT %+v; want INPUT_MOUSE, %+v", d.dx, d.dy, in.Type, *in.mouse(), want)
		}
	}
	// A press of a wheel key keeps its notch.
	plain := makeInput(KeyEvent{Mouse: keys.WheelUp}, extra)
	if mi := plain.mouse(); mi.MouseData != 120 || mi.Flags != 0x0800 || mi.Dx != 0 {
		t.Errorf("WheelUp MOUSEINPUT = %+v, want the +120 notch", *mi)
	}
}

// A jump is the absolute position over the virtual desktop: MOVE, ABSOLUTE and
// VIRTUALDESK together (0x0001, 0x8000, 0x4000), with dx and dy the position in
// 0 to 65535 and no mouseData.
func TestMakeMouseInputJump(t *testing.T) {
	const extra = uintptr(0x7061646D_00000042)
	for _, d := range []struct{ x, y int32 }{{0, 0}, {32768, 32768}, {65535, 0}, {100, 65535}} {
		in := makeInput(KeyEvent{Mouse: keys.MouseJump, Dx: d.x, Dy: d.y}, extra)
		want := mouseInput{Dx: d.x, Dy: d.y, Flags: 0x0001 | 0x8000 | 0x4000, ExtraInfo: extra}
		if in.Type != inputMouse || *in.mouse() != want {
			t.Errorf("jump %d,%d: type %d, MOUSEINPUT %+v; want INPUT_MOUSE, %+v", d.x, d.y, in.Type, *in.mouse(), want)
		}
	}
}

// The moves go through batchInputs like any other mouse event: one input
// each, in order, behind the keyboard events and the keyboard canary of a
// batch.
func TestBatchInputsMoves(t *testing.T) {
	ev := func(spec string, up bool) KeyEvent { return batchEvent(t, spec, up) }
	events := []KeyEvent{ev("Z", false), {Mouse: keys.MouseMove, Dx: 1}, {Mouse: keys.MouseMove, Dx: -1}}
	inputs, before := batchInputs(events, 9)
	if len(inputs) != 3 || !slices.Equal(before, []int{0, 1, 2}) {
		t.Fatalf("batchInputs made %d inputs, before %v; want 3, [0 1 2]", len(inputs), before)
	}
	if got := []int32{inputs[1].mouse().Dx, inputs[2].mouse().Dx}; !slices.Equal(got, []int32{1, -1}) {
		t.Errorf("dx of the moves = %v, want [1 -1]", got)
	}
	if n := injectedEvents(events, before, 2); n != 2 {
		t.Errorf("injectedEvents after the first move = %d, want 2", n)
	}
}
