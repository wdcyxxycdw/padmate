package jc2

import "math"

// Mode is how Joy-Cons are presented as gamepads, with the same meaning as
// SDL's for the first Joy-Con (ARCHITECTURE §7.1).
type Mode uint8

const (
	// Vertical is a single Joy-Con held upright, like half a gamepad. It is
	// the only way padmate holds a single Joy-Con.
	Vertical Mode = iota + 1
	// Pair is a left and a right Joy-Con as one gamepad: both Vertical
	// layouts together.
	Pair
)

func (m Mode) String() string {
	switch m {
	case Vertical:
		return "vertical"
	case Pair:
		return "pair"
	}
	return "?"
}

// Control is a control of padmate's gamepad: a button, or an axis. The
// values count in SDL's order, buttons first.
type Control uint8

// The buttons and axes of the gamepad. Their names are the ones of
// input.ButtonName and input.AxisName.
const (
	ControlSouth Control = iota
	ControlEast
	ControlWest
	ControlNorth
	ControlBack
	ControlGuide
	ControlStart
	ControlLeftStick
	ControlRightStick
	ControlLeftShoulder
	ControlRightShoulder
	ControlDpadUp
	ControlDpadDown
	ControlDpadLeft
	ControlDpadRight
	ControlMisc1
	ControlPaddle1
	ControlPaddle2
	ControlPaddle3
	ControlPaddle4
	ControlMisc2

	ControlLeftX
	ControlLeftY
	ControlRightX
	ControlRightY
	ControlLeftTrigger
	ControlRightTrigger

	numControls

	firstAxis = ControlLeftX
	numAxes   = int(numControls - firstAxis)
)

var controlNames = [numControls]string{
	ControlSouth: "south", ControlEast: "east", ControlWest: "west", ControlNorth: "north",
	ControlBack: "back", ControlGuide: "guide", ControlStart: "start",
	ControlLeftStick: "leftstick", ControlRightStick: "rightstick",
	ControlLeftShoulder: "leftshoulder", ControlRightShoulder: "rightshoulder",
	ControlDpadUp: "dpad.up", ControlDpadDown: "dpad.down", ControlDpadLeft: "dpad.left", ControlDpadRight: "dpad.right",
	ControlMisc1: "misc1", ControlPaddle1: "paddle1", ControlPaddle2: "paddle2",
	ControlPaddle3: "paddle3", ControlPaddle4: "paddle4", ControlMisc2: "misc2",
	ControlLeftX: "leftx", ControlLeftY: "lefty", ControlRightX: "rightx", ControlRightY: "righty",
	ControlLeftTrigger: "lefttrigger", ControlRightTrigger: "righttrigger",
}

// Name returns the control's name, "?" for a value that is no control.
func (c Control) Name() string {
	if c < numControls {
		return controlNames[c]
	}
	return "?"
}

func (c Control) String() string { return c.Name() }

// IsAxis reports whether c is an axis.
func (c Control) IsAxis() bool { return c >= firstAxis && c < numControls }

// State is the gamepad that a report (or two) makes: which buttons are down
// and the axes. It is a value, so two States compare with ==.
type State struct {
	// Buttons has bit c set while the button control c is down.
	Buttons uint32
	// Axes holds the axes in SDL's convention: sticks -32768..32767 with y
	// down, triggers 0..32767. Axes[0] is ControlLeftX.
	Axes [numAxes]int16
}

// Down reports whether the button control c is down.
func (s State) Down(c Control) bool { return c < firstAxis && s.Buttons&(1<<c) != 0 }

// Axis returns the value of the axis control c, 0 for a button.
func (s State) Axis(c Control) int16 {
	if !c.IsAxis() {
		return 0
	}
	return s.Axes[c-firstAxis]
}

// Binding says that physical button From is the control To (a button, or for
// a trigger an axis that is 0 or 32767).
type Binding struct {
	From Button
	To   Control
}

// StickMap says how the stick becomes two axes. SignX and SignY are +1 or
// -1. The raw values are Norm's: positive is right and up.
type StickMap struct {
	X, Y         Control
	SignX, SignY int8
}

// Mapping is how a Joy-Con of one side is mapped in one mode.
type Mapping struct {
	Bindings []Binding
	Stick    StickMap
}

// The mappings, by mode and side. Pair has no table of its own: it is the
// Vertical mapping of each side.
var (
	verticalL = Mapping{
		Bindings: []Binding{
			{ButtonUp, ControlDpadUp}, {ButtonDown, ControlDpadDown}, {ButtonLeft, ControlDpadLeft}, {ButtonRight, ControlDpadRight},
			{ButtonL, ControlLeftShoulder}, {ButtonZL, ControlLeftTrigger},
			{ButtonMinus, ControlBack}, {ButtonCapture, ControlMisc1}, {ButtonLStick, ControlLeftStick},
			{ButtonSL, ControlPaddle2}, {ButtonSR, ControlPaddle4},
		},
		Stick: StickMap{X: ControlLeftX, Y: ControlLeftY, SignX: 1, SignY: -1},
	}
	verticalR = Mapping{
		Bindings: []Binding{
			{ButtonA, ControlEast}, {ButtonB, ControlSouth}, {ButtonX, ControlNorth}, {ButtonY, ControlWest},
			{ButtonR, ControlRightShoulder}, {ButtonZR, ControlRightTrigger},
			{ButtonPlus, ControlStart}, {ButtonHome, ControlGuide}, {ButtonC, ControlMisc2}, {ButtonRStick, ControlRightStick},
			{ButtonSR, ControlPaddle1}, {ButtonSL, ControlPaddle3},
		},
		Stick: StickMap{X: ControlRightX, Y: ControlRightY, SignX: 1, SignY: -1},
	}
)

// mappingOf returns the mapping of side in mode, nil for a mode or side that
// does not exist.
func mappingOf(mode Mode, side Side) *Mapping {
	switch {
	case (mode == Vertical || mode == Pair) && side == SideL:
		return &verticalL
	case (mode == Vertical || mode == Pair) && side == SideR:
		return &verticalR
	}
	return nil
}

// Layout returns how the Joy-Con of side is mapped in mode, or false for a
// mode or side that does not exist. In Pair mode it is the mapping of that
// one side's half.
func Layout(mode Mode, side Side) (Mapping, bool) {
	m := mappingOf(mode, side)
	if m == nil {
		return Mapping{}, false
	}
	out := *m
	out.Bindings = append([]Binding(nil), m.Bindings...)
	return out, true
}

// used returns the set of controls that the mappings of mode (of side, or
// for Pair of both sides) can set, one bit per Control.
func used(mode Mode, side Side) uint32 {
	var set uint32
	add := func(m *Mapping) {
		if m == nil {
			return
		}
		for _, b := range m.Bindings {
			set |= 1 << b.To
		}
		set |= 1<<m.Stick.X | 1<<m.Stick.Y
	}
	if mode == Pair {
		add(mappingOf(Pair, SideL))
		add(mappingOf(Pair, SideR))
	} else {
		add(mappingOf(mode, side))
	}
	return set
}

// names lists the controls of set in order, the buttons or the axes.
func names(set uint32, axes bool) []string {
	var out []string
	for c := Control(0); c < numControls; c++ {
		if set&(1<<c) != 0 && c.IsAxis() == axes {
			out = append(out, c.Name())
		}
	}
	return out
}

// Controls returns the names of the buttons that a Joy-Con of side can press
// in mode, in Control order. For Pair it lists both Joy-Cons and side is
// ignored. A mode or side that does not exist has none.
func Controls(mode Mode, side Side) []string { return names(used(mode, side), false) }

// Axes returns the names of the axes (sticks and triggers) that a Joy-Con of
// side can move in mode, in Control order. For Pair it lists both Joy-Cons
// and side is ignored.
func Axes(mode Mode, side Side) []string { return names(used(mode, side), true) }

// axisMax is the full deflection of an axis.
const axisMax = 32767

// toAxis converts -1..1 to an axis value.
func toAxis(f float64) int16 {
	return int16(max(-32768, min(axisMax, math.Round(f*axisMax))))
}

// mapInto adds to s what report r of the Joy-Con of side presses and moves.
func mapInto(s *State, mode Mode, side Side, r Report, cal StickCal) {
	m := mappingOf(mode, side)
	if m == nil {
		return
	}
	for _, b := range m.Bindings {
		if !r.Buttons.Has(b.From) {
			continue
		}
		if b.To.IsAxis() {
			s.Axes[b.To-firstAxis] = axisMax
		} else {
			s.Buttons |= 1 << b.To
		}
	}
	fx, fy := cal.Norm(r.StickX, r.StickY)
	s.Axes[m.Stick.X-firstAxis] = toAxis(float64(m.Stick.SignX) * fx)
	s.Axes[m.Stick.Y-firstAxis] = toAxis(float64(m.Stick.SignY) * fy)
}

// Map returns the gamepad that report r of the Joy-Con of side makes in mode,
// with the stick calibrated by cal. In Pair mode it is that side's half; use
// MapPair for both.
func Map(mode Mode, side Side, r Report, cal StickCal) State {
	var s State
	mapInto(&s, mode, side, r, cal)
	return s
}

// MapPair returns the gamepad that a left and a right report make together:
// the union of their Pair halves.
func MapPair(l, r Report, lcal, rcal StickCal) State {
	var s State
	mapInto(&s, Pair, SideL, l, lcal)
	mapInto(&s, Pair, SideR, r, rcal)
	return s
}
