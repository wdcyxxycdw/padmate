package jc2

import (
	"math"
	"slices"
	"testing"
)

// padmateControls mirrors padmate's control names (internal/input/controls.go
// ButtonName and AxisName); a test of package input checks the real ones.
// The d-pad names are "dpad.up" and so on, not "dpup".
var padmateControls = []string{
	"south", "east", "west", "north", "back", "guide", "start", "leftstick", "rightstick",
	"leftshoulder", "rightshoulder", "dpad.up", "dpad.down", "dpad.left", "dpad.right",
	"misc1", "misc2", "paddle1", "paddle2", "paddle3", "paddle4",
	"leftx", "lefty", "rightx", "righty", "lefttrigger", "righttrigger",
}

// pressed returns a report with only the buttons bs pressed and the stick at
// x, y.
func pressed(x, y uint16, bs ...Button) Report {
	r := Report{StickX: x, StickY: y}
	for _, b := range bs {
		r.Buttons |= 1 << b
	}
	return r
}

// stickTo returns a calibration-exact stick position: fx, fy in -1, 0, 1.
func stickTo(c StickCal, fx, fy int) (x, y uint16) {
	x, y = c.NeutralX, c.NeutralY
	switch fx {
	case 1:
		x += c.PosX
	case -1:
		x -= c.NegX
	}
	switch fy {
	case 1:
		y += c.PosY
	case -1:
		y -= c.NegY
	}
	return x, y
}

func TestControlNames(t *testing.T) {
	seen := map[string]bool{}
	for c := Control(0); c < numControls; c++ {
		n := c.Name()
		if !slices.Contains(padmateControls, n) {
			t.Errorf("control %d is called %q, which padmate has no control for", c, n)
		}
		if seen[n] {
			t.Errorf("name %q twice", n)
		}
		seen[n] = true
		if c.IsAxis() != (c >= ControlLeftX) {
			t.Errorf("%v IsAxis = %v", c, c.IsAxis())
		}
	}
	if len(seen) != len(padmateControls) {
		t.Errorf("%d controls, padmate has %d", len(seen), len(padmateControls))
	}
	if Control(200).Name() != "?" || Control(200).IsAxis() {
		t.Error("a control that does not exist")
	}
}

func TestLayoutTables(t *testing.T) {
	// The tables of the design, written out by physical button.
	tests := []struct {
		mode Mode
		side Side
		want map[Button]string
		x, y string // the axes the stick feeds
	}{
		{Vertical, SideL, map[Button]string{
			ButtonUp: "dpad.up", ButtonDown: "dpad.down", ButtonLeft: "dpad.left", ButtonRight: "dpad.right",
			ButtonL: "leftshoulder", ButtonZL: "lefttrigger", ButtonMinus: "back", ButtonCapture: "misc1",
			ButtonLStick: "leftstick", ButtonSL: "paddle2", ButtonSR: "paddle4",
		}, "leftx", "lefty"},
		{Vertical, SideR, map[Button]string{
			ButtonA: "east", ButtonB: "south", ButtonX: "north", ButtonY: "west",
			ButtonR: "rightshoulder", ButtonZR: "righttrigger", ButtonPlus: "start", ButtonHome: "guide",
			ButtonC: "misc2", ButtonRStick: "rightstick", ButtonSR: "paddle1", ButtonSL: "paddle3",
		}, "rightx", "righty"},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String()+tt.side.String(), func(t *testing.T) {
			l, ok := Layout(tt.mode, tt.side)
			if !ok {
				t.Fatal("no layout")
			}
			got := map[Button]string{}
			for _, b := range l.Bindings {
				if _, dup := got[b.From]; dup {
					t.Errorf("%v bound twice", b.From)
				}
				got[b.From] = b.To.Name()
			}
			if len(got) != len(tt.want) {
				t.Errorf("%d bindings, want %d: %v", len(got), len(tt.want), got)
			}
			for b, want := range tt.want {
				if got[b] != want {
					t.Errorf("%v = %q, want %q", b, got[b], want)
				}
			}
			if l.Stick.X.Name() != tt.x || l.Stick.Y.Name() != tt.y {
				t.Errorf("stick feeds %v, %v; want %s, %s", l.Stick.X, l.Stick.Y, tt.x, tt.y)
			}
		})
	}
	// Pair is the Vertical layout of each side.
	for _, side := range []Side{SideL, SideR} {
		p, _ := Layout(Pair, side)
		v, _ := Layout(Vertical, side)
		if !slices.Equal(p.Bindings, v.Bindings) || p.Stick != v.Stick {
			t.Errorf("Pair %v differs from Vertical", side)
		}
	}
}

func TestLayoutInvalid(t *testing.T) {
	for _, c := range []struct {
		mode Mode
		side Side
	}{{0, SideL}, {Vertical, 0}, {4, SideR}, {Vertical, 3}, {Mode(1) + 3, SideL}} {
		if l, ok := Layout(c.mode, c.side); ok || l.Bindings != nil {
			t.Errorf("Layout(%v, %v) = %+v, %v", c.mode, c.side, l, ok)
		}
		if Controls(c.mode, c.side) != nil || Axes(c.mode, c.side) != nil {
			t.Errorf("Controls(%v, %v) = %v", c.mode, c.side, Controls(c.mode, c.side))
		}
		var r Report
		r.Buttons = ^Buttons(0)
		if s := Map(c.mode, c.side, r, DefaultStick); s != (State{}) {
			t.Errorf("Map(%v, %v) = %+v", c.mode, c.side, s)
		}
	}
	// The copy is the caller's.
	l, _ := Layout(Vertical, SideL)
	l.Bindings[0].To = ControlGuide
	if l2, _ := Layout(Vertical, SideL); l2.Bindings[0].To != ControlDpadUp {
		t.Error("Layout shares its table")
	}
	if Mode(9).String() != "?" {
		t.Error("Mode.String")
	}
}

func TestControlsAndAxes(t *testing.T) {
	tests := []struct {
		mode    Mode
		side    Side
		buttons []string
		axes    []string
	}{
		{Vertical, SideL, []string{"back", "leftstick", "leftshoulder", "dpad.up", "dpad.down", "dpad.left", "dpad.right", "misc1", "paddle2", "paddle4"},
			[]string{"leftx", "lefty", "lefttrigger"}},
		{Vertical, SideR, []string{"east", "south", "north", "west", "guide", "start", "rightstick", "rightshoulder", "paddle1", "paddle3", "misc2"},
			[]string{"rightx", "righty", "righttrigger"}},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String()+tt.side.String(), func(t *testing.T) {
			// Sort the want list into Control order by name lookup.
			if got := Controls(tt.mode, tt.side); !sameSet(got, tt.buttons) || !inControlOrder(got) {
				t.Errorf("Controls = %v\nwant %v", got, tt.buttons)
			}
			if got := Axes(tt.mode, tt.side); !sameSet(got, tt.axes) || !inControlOrder(got) {
				t.Errorf("Axes = %v, want %v", got, tt.axes)
			}
		})
	}

	// Pair lists both halves whichever side is asked.
	pb, pa := Controls(Pair, SideL), Axes(Pair, SideR)
	lb, rb := Controls(Vertical, SideL), Controls(Vertical, SideR)
	want := map[string]bool{}
	for _, n := range append(slices.Clone(lb), rb...) {
		want[n] = true
	}
	if len(pb) != len(want) {
		t.Errorf("Pair has %d buttons, want %d", len(pb), len(want))
	}
	for _, n := range pb {
		if !want[n] {
			t.Errorf("Pair has stray %q", n)
		}
	}
	if !slices.Equal(pb, Controls(Pair, SideR)) || !slices.Equal(pa, Axes(Pair, SideL)) {
		t.Error("Pair depends on the side")
	}
	if !sameSet(pa, []string{"leftx", "lefty", "rightx", "righty", "lefttrigger", "righttrigger"}) {
		t.Errorf("Pair axes = %v", pa)
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// inControlOrder reports whether names are in Control order.
func inControlOrder(names []string) bool {
	last := -1
	for _, n := range names {
		for c := range numControls {
			if c.Name() == n {
				if int(c) <= last {
					return false
				}
				last = int(c)
			}
		}
	}
	return true
}

func TestMapButtons(t *testing.T) {
	for _, mode := range []Mode{Vertical, Pair} {
		for _, side := range []Side{SideL, SideR} {
			l, _ := Layout(mode, side)
			for _, b := range l.Bindings {
				s := Map(mode, side, pressed(2048, 2048, b.From), DefaultStick)
				for c := Control(0); c < numControls; c++ {
					switch {
					case c == b.To && c.IsAxis():
						if s.Axis(c) != 32767 {
							t.Errorf("%v %v %v: %v = %d", mode, side, b.From, c, s.Axis(c))
						}
					case c == b.To:
						if !s.Down(c) {
							t.Errorf("%v %v %v: %v not down", mode, side, b.From, c)
						}
					case !c.IsAxis():
						if s.Down(c) {
							t.Errorf("%v %v %v: %v down too", mode, side, b.From, c)
						}
					}
				}
			}
			// A button the side has not stays up: R buttons on L and back.
			if s := Map(mode, side, pressed(2048, 2048), DefaultStick); s != (State{}) {
				t.Errorf("%v %v at rest: %+v", mode, side, s)
			}
		}
	}
	// Buttons of the other side do nothing.
	if s := Map(Vertical, SideL, pressed(2048, 2048, ButtonA, ButtonB, ButtonX, ButtonY, ButtonPlus), DefaultStick); s != (State{}) {
		t.Errorf("R buttons on L: %+v", s)
	}
}

func TestMapSticks(t *testing.T) {
	// fx, fy are the push in the controller's own frame: +1 right and up
	// (Norm's convention). The wants are the axes of the gamepad that the
	// Joy-Con is held as: x right, y down.
	tests := []struct {
		name   string
		mode   Mode
		side   Side
		cal    StickCal
		fx, fy int
		x, y   Control // the axes
		wx, wy int16
	}{
		{"vertical L up", Vertical, SideL, calL, 0, 1, ControlLeftX, ControlLeftY, 0, -32767},
		{"vertical L right", Vertical, SideL, calL, 1, 0, ControlLeftX, ControlLeftY, 32767, 0},
		{"vertical R down", Vertical, SideR, calR, 0, -1, ControlRightX, ControlRightY, 0, 32767},
		{"vertical R left", Vertical, SideR, calR, -1, 0, ControlRightX, ControlRightY, -32767, 0},
		{"pair L up", Pair, SideL, calL, 0, 1, ControlLeftX, ControlLeftY, 0, -32767},
		{"pair R right", Pair, SideR, calR, 1, 0, ControlRightX, ControlRightY, 32767, 0},
		{"rest", Vertical, SideL, calL, 0, 0, ControlLeftX, ControlLeftY, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y := stickTo(tt.cal, tt.fx, tt.fy)
			s := Map(tt.mode, tt.side, pressed(x, y), tt.cal)
			if s.Axis(tt.x) != tt.wx || s.Axis(tt.y) != tt.wy {
				t.Errorf("%v, %v = %d, %d; want %d, %d", tt.x, tt.y, s.Axis(tt.x), s.Axis(tt.y), tt.wx, tt.wy)
			}
			for c := ControlLeftX; c < numControls; c++ {
				if c != tt.x && c != tt.y && s.Axis(c) != 0 {
					t.Errorf("%v = %d", c, s.Axis(c))
				}
			}
		})
	}
}

func TestToAxis(t *testing.T) {
	tests := []struct {
		f    float64
		want int16
	}{
		{0, 0}, {1, 32767}, {-1, -32767}, {0.5, 16384}, {-0.5, -16384},
		{1.0 / 32767, 1}, {0.4 / 32767, 0}, {2, 32767}, {-2, -32768},
	}
	for _, tt := range tests {
		if got := toAxis(tt.f); got != tt.want {
			t.Errorf("toAxis(%v) = %d, want %d", tt.f, got, tt.want)
		}
	}
	// A real half deflection of the right stick.
	x, y := calR.NeutralX+calR.PosX/2, calR.NeutralY
	s := Map(Vertical, SideR, pressed(x, y), calR)
	if want := int16(math.Round(612.0 / 1225.0 * 32767)); s.Axis(ControlRightX) != want {
		t.Errorf("right x = %d, want %d", s.Axis(ControlRightX), want)
	}
}

func TestMapPair(t *testing.T) {
	lx, ly := stickTo(calL, 1, 1)
	rx, ry := stickTo(calR, -1, 0)
	l := pressed(lx, ly, ButtonMinus, ButtonZL, ButtonUp, ButtonSL, ButtonSR)
	r := pressed(rx, ry, ButtonPlus, ButtonZR, ButtonA, ButtonSL, ButtonSR, ButtonRStick)
	s := MapPair(l, r, calL, calR)

	for _, c := range []Control{ControlBack, ControlStart, ControlDpadUp, ControlPaddle2, ControlPaddle4,
		ControlEast, ControlPaddle1, ControlPaddle3, ControlRightStick} {
		if !s.Down(c) {
			t.Errorf("%v not down", c)
		}
	}
	if n := popcount(s.Buttons); n != 9 {
		t.Errorf("%d buttons down, want 9", n)
	}
	want := [numAxes]int16{
		ControlLeftX - firstAxis:        32767,
		ControlLeftY - firstAxis:        -32767,
		ControlRightX - firstAxis:       -32767,
		ControlRightY - firstAxis:       0,
		ControlLeftTrigger - firstAxis:  32767,
		ControlRightTrigger - firstAxis: 32767,
	}
	if s.Axes != want {
		t.Errorf("axes = %v, want %v", s.Axes, want)
	}
	// It is the union of the halves.
	a, b := Map(Pair, SideL, l, calL), Map(Pair, SideR, r, calR)
	if s.Buttons != a.Buttons|b.Buttons {
		t.Error("buttons are not the union")
	}
	if got := MapPair(pressed(calL.NeutralX, calL.NeutralY), pressed(calR.NeutralX, calR.NeutralY), calL, calR); got != (State{}) {
		t.Errorf("rest = %+v", got)
	}
}

func popcount(v uint32) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

// TestEmergencyCombos: SL+SR is the members of input.DefaultCombo.
func TestEmergencyCombos(t *testing.T) {
	tests := []struct {
		name   string
		got    State
		want   []Control
		reason string
	}{
		{"vertical L", Map(Vertical, SideL, pressed(2048, 2048, ButtonSL, ButtonSR), DefaultStick),
			[]Control{ControlPaddle2, ControlPaddle4}, "paddle2+paddle4"},
		{"vertical R", Map(Vertical, SideR, pressed(2048, 2048, ButtonSL, ButtonSR), DefaultStick),
			[]Control{ControlPaddle3, ControlPaddle1}, "paddle3+paddle1"},
		{"pair", MapPair(pressed(2048, 2048, ButtonMinus), pressed(2048, 2048, ButtonPlus), DefaultStick, DefaultStick),
			[]Control{ControlBack, ControlStart}, "back+start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var set uint32
			for _, c := range tt.want {
				set |= 1 << c
			}
			if tt.got.Buttons != set {
				t.Errorf("%s: buttons %#x, want %#x", tt.reason, tt.got.Buttons, set)
			}
		})
	}
}

func TestMapDoesNotAllocate(t *testing.T) {
	r := pressed(3000, 1000, ButtonA, ButtonZR, ButtonSL)
	var sink State
	if n := testing.AllocsPerRun(100, func() { sink = Map(Vertical, SideR, r, calR) }); n != 0 {
		t.Errorf("Map allocates %v times", n)
	}
	if n := testing.AllocsPerRun(100, func() { sink = MapPair(r, r, calL, calR) }); n != 0 {
		t.Errorf("MapPair allocates %v times", n)
	}
	_ = sink
}
