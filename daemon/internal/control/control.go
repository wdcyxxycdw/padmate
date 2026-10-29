// Package control names the gamepad controls a profile can bind (IN-04,
// ARCHITECTURE §7.1). Names describe where a control sits, not what is
// printed on it, so one profile works the same on Xbox, PlayStation and
// Nintendo pads: "south" is the bottom face button, which is A on Xbox,
// Cross on PlayStation and B on Nintendo. The package also accepts the Xbox
// style aliases (a, b, lb, ...) as input, and names the raw axes used by
// input, traces and padmatectl monitor.
//
// The stick directions, active controls, d-pad diagonals and half-axis
// controls are virtual buttons produced by the analog recognizer (M4). M15
// adds the stick rings (inner, outer), the virtual knob ticks (cw, ccw) and
// the second stage of the triggers (lt.full, rt.full).
//
// The package is pure data with no platform dependencies.
package control

import "strconv"

// Control is a bindable control: a gamepad button, or a trigger used as a
// button. The zero value is Invalid.
type Control uint8

// Controls, in the order of ARCHITECTURE §7.1.
const (
	Invalid Control = iota
	South
	East
	West
	North
	Back
	Guide
	Start
	LeftStick
	RightStick
	LeftShoulder
	RightShoulder
	DpadUp
	DpadDown
	DpadLeft
	DpadRight
	Misc1
	Misc2
	Misc3
	Misc4
	Misc5
	Misc6
	Paddle1
	Paddle2
	Paddle3
	Paddle4
	Touchpad
	// LT and RT are virtual buttons driven by the trigger axes (ANA-01).
	LT
	RT
	LSUp
	LSDown
	LSLeft
	LSRight
	LSUpLeft
	LSUpRight
	LSDownLeft
	LSDownRight
	LSActive
	RSUp
	RSDown
	RSLeft
	RSRight
	RSUpLeft
	RSUpRight
	RSDownLeft
	RSDownRight
	RSActive
	DpadUpLeft
	DpadUpRight
	DpadDownLeft
	DpadDownRight
	// Half-axis controls. The canonical spelling is axis+ or axis- (for
	// example leftx+ and leftx-), and Parse also accepts .positive/.negative.
	LeftXPositive
	LeftXNegative
	LeftYPositive
	LeftYNegative
	RightXPositive
	RightXNegative
	RightYPositive
	RightYNegative
	LeftTriggerPositive
	LeftTriggerNegative
	RightTriggerPositive
	RightTriggerNegative
	// Stick rings, virtual knob ticks and the second stage of the triggers
	// (ANA-10, ANA-11, ANA-14). They are appended after the half axes so that
	// no existing value changes.
	LSInner
	LSOuter
	LSCW
	LSCCW
	RSInner
	RSOuter
	RSCW
	RSCCW
	LTFull
	RTFull
)

// lastControl is the highest declared Control.
const lastControl = RTFull

// Count is the number of declared Control values, Invalid included: a
// [Count]T array indexed by Control covers Invalid and every valid control.
const Count = int(lastControl) + 1

var controlNames = [Count]string{
	South:                "south",
	East:                 "east",
	West:                 "west",
	North:                "north",
	Back:                 "back",
	Guide:                "guide",
	Start:                "start",
	LeftStick:            "leftstick",
	RightStick:           "rightstick",
	LeftShoulder:         "leftshoulder",
	RightShoulder:        "rightshoulder",
	DpadUp:               "dpad.up",
	DpadDown:             "dpad.down",
	DpadLeft:             "dpad.left",
	DpadRight:            "dpad.right",
	Misc1:                "misc1",
	Misc2:                "misc2",
	Misc3:                "misc3",
	Misc4:                "misc4",
	Misc5:                "misc5",
	Misc6:                "misc6",
	Paddle1:              "paddle1",
	Paddle2:              "paddle2",
	Paddle3:              "paddle3",
	Paddle4:              "paddle4",
	Touchpad:             "touchpad",
	LT:                   "lt",
	RT:                   "rt",
	LSUp:                 "ls.up",
	LSDown:               "ls.down",
	LSLeft:               "ls.left",
	LSRight:              "ls.right",
	LSUpLeft:             "ls.upleft",
	LSUpRight:            "ls.upright",
	LSDownLeft:           "ls.downleft",
	LSDownRight:          "ls.downright",
	LSActive:             "ls.active",
	RSUp:                 "rs.up",
	RSDown:               "rs.down",
	RSLeft:               "rs.left",
	RSRight:              "rs.right",
	RSUpLeft:             "rs.upleft",
	RSUpRight:            "rs.upright",
	RSDownLeft:           "rs.downleft",
	RSDownRight:          "rs.downright",
	RSActive:             "rs.active",
	DpadUpLeft:           "dpad.upleft",
	DpadUpRight:          "dpad.upright",
	DpadDownLeft:         "dpad.downleft",
	DpadDownRight:        "dpad.downright",
	LeftXPositive:        "leftx+",
	LeftXNegative:        "leftx-",
	LeftYPositive:        "lefty+",
	LeftYNegative:        "lefty-",
	RightXPositive:       "rightx+",
	RightXNegative:       "rightx-",
	RightYPositive:       "righty+",
	RightYNegative:       "righty-",
	LeftTriggerPositive:  "lefttrigger+",
	LeftTriggerNegative:  "lefttrigger-",
	RightTriggerPositive: "righttrigger+",
	RightTriggerNegative: "righttrigger-",
	LSInner:              "ls.inner",
	LSOuter:              "ls.outer",
	LSCW:                 "ls.cw",
	LSCCW:                "ls.ccw",
	RSInner:              "rs.inner",
	RSOuter:              "rs.outer",
	RSCW:                 "rs.cw",
	RSCCW:                "rs.ccw",
	LTFull:               "lt.full",
	RTFull:               "rt.full",
}

// Alias is an alternative name that Parse accepts for a control.
type Alias struct {
	Name    string
	Control Control
}

// aliases are the Xbox style names of §7.1. The triggers have none: lt and
// rt are already short.
var aliases = [...]Alias{
	{"a", South},
	{"b", East},
	{"x", West},
	{"y", North},
	{"lb", LeftShoulder},
	{"rb", RightShoulder},
	{"l3", LeftStick},
	{"r3", RightStick},
	{"select", Back},
}

// byName maps every canonical name and alias to its control. It is built
// once and only read afterwards.
var byName = func() map[string]Control {
	m := make(map[string]Control, Count+len(aliases))
	for c := South; c <= lastControl; c++ {
		m[controlNames[c]] = c
	}
	for _, a := range aliases {
		m[a.Name] = a.Control
	}
	// Readable spellings are useful in generated profiles and accepted as
	// input aliases; String always uses the compact canonical spelling.
	for a := LeftXPositive; a <= RightTriggerNegative; a++ {
		name := controlNames[a]
		if len(name) > 0 && (name[len(name)-1] == '+' || name[len(name)-1] == '-') {
			suffix := ".positive"
			if name[len(name)-1] == '-' {
				suffix = ".negative"
			}
			m[name[:len(name)-1]+suffix] = a
		}
	}
	return m
}()

// String returns the canonical name of c, such as "south", "dpad.up" or
// "lt", or "control(N)" when c is not valid. It never returns an alias.
func (c Control) String() string {
	if c.IsValid() {
		return controlNames[c]
	}
	return "control(" + strconv.Itoa(int(c)) + ")"
}

// IsValid reports whether c is one of the declared controls.
func (c Control) IsValid() bool { return c >= South && c <= lastControl }

// DefaultFunctionKey is the function key when config.json does not choose
// one: Start (Menu, Options, Plus; MAP-27, ADR-0075).
const DefaultFunctionKey = Start

// CanBeFunctionKey reports whether c is one of the controls that config.json
// can make the function key: Back, Start and Guide (MAP-27, ADR-0075). There
// is only one function key at a time, the same for every profile: it sends
// nothing and cannot be bound, and while it is held the other controls use
// the profile's function set first. Every other control, these two included,
// is an ordinary control.
func (c Control) CanBeFunctionKey() bool { return c == Back || c == Guide || c == Start }

// IsTrigger reports whether c is LT or RT, a virtual button driven by a
// trigger axis.
func (c Control) IsTrigger() bool { return c == LT || c == RT }

// IsVirtual reports whether c is produced by an analog recognizer rather
// than arriving as an SDL gamepad button. LT/RT are virtual trigger buttons;
// M4 adds stick, d-pad diagonal and half-axis controls.
func (c Control) IsVirtual() bool { return c >= LT }

// HasDepth reports whether c follows how far a stick or a trigger is pushed:
// a stick direction, active, inner or outer, a half axis, LT, RT or the
// second stage of a trigger. A repeat may give such a control a minimum rate
// (ANA-12, ANA-15). The knob ticks, the d-pad diagonals and the buttons have
// no depth.
func (c Control) HasDepth() bool {
	switch {
	case c >= LT && c <= RSActive, c >= LeftXPositive && c <= RightTriggerNegative:
		return true
	case c == LSInner, c == LSOuter, c == RSInner, c == RSOuter, c == LTFull, c == RTFull:
		return true
	}
	return false
}

// Axis returns the trigger axis behind LT or RT, or InvalidAxis for every
// other control.
func (c Control) Axis() Axis {
	switch c {
	case LT:
		return LeftTrigger
	case RT:
		return RightTrigger
	default:
		return InvalidAxis
	}
}

// Parse returns the control named name: a canonical name or an alias. The
// match is exact and case-sensitive, like JSON object keys.
func Parse(name string) (Control, bool) {
	c, ok := byName[name]
	return c, ok
}

// All returns every valid control in declaration order. The caller owns
// the slice.
func All() []Control {
	out := make([]Control, 0, Count-1)
	for c := South; c <= lastControl; c++ {
		out = append(out, c)
	}
	return out
}

// Aliases returns the aliases in a fixed order: a b x y lb rb l3 r3
// select. The caller owns the slice.
func Aliases() []Alias {
	out := make([]Alias, len(aliases))
	copy(out, aliases[:])
	return out
}

// Axis is a raw gamepad axis. The zero value is InvalidAxis. The order is
// SDL's gamepad axis order.
type Axis uint8

// Axes.
const (
	InvalidAxis Axis = iota
	LeftX
	LeftY
	RightX
	RightY
	LeftTrigger
	RightTrigger
)

// AxisCount is the number of declared Axis values, InvalidAxis included: an
// [AxisCount]T array indexed by Axis covers InvalidAxis and every valid axis.
const AxisCount = int(RightTrigger) + 1

var axisNames = [AxisCount]string{
	LeftX:        "leftx",
	LeftY:        "lefty",
	RightX:       "rightx",
	RightY:       "righty",
	LeftTrigger:  "lefttrigger",
	RightTrigger: "righttrigger",
}

// String returns the name of a, such as "leftx" or "righttrigger", or
// "axis(N)" when a is not valid.
func (a Axis) String() string {
	if a.IsValid() {
		return axisNames[a]
	}
	return "axis(" + strconv.Itoa(int(a)) + ")"
}

// IsValid reports whether a is one of the declared axes.
func (a Axis) IsValid() bool { return a >= LeftX && a <= RightTrigger }

// ParseAxis returns the axis named name. The match is exact.
func ParseAxis(name string) (Axis, bool) {
	for a := LeftX; a <= RightTrigger; a++ {
		if axisNames[a] == name {
			return a, true
		}
	}
	return InvalidAxis, false
}

// AllAxes returns every valid axis in declaration order. The caller owns
// the slice.
func AllAxes() []Axis {
	out := make([]Axis, 0, AxisCount-1)
	for a := LeftX; a <= RightTrigger; a++ {
		out = append(out, a)
	}
	return out
}
