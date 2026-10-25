package slot

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// State is what a Slot believes its device's controls are.
type State struct {
	Buttons [control.Count]bool      // by control.Control; triggers are axes and stay false
	Axes    [control.AxisCount]int16 // last value of each axis
}

// State returns the state the events Handle got leave the controls in:
// State.Apply over them.
func (s *Slot) State() State { return s.state }

// Apply updates st with e as Handle does its own: a button event sets its
// button (an alias the control it names), an axis event its axis, and
// DeviceRemoved zeroes st. Other kinds, triggers sent as buttons and
// names control does not parse change nothing. A router applies a
// device's events to know the State its slot will reach.
func (st *State) Apply(e input.Event) {
	switch e.Kind {
	case input.DeviceRemoved:
		*st = State{}
	case input.ButtonDown, input.ButtonUp:
		if c, ok := parseButton(e.Control); ok {
			st.Buttons[c] = e.Kind == input.ButtonDown
		}
	case input.AxisMotion:
		if a, ok := control.ParseAxis(e.Control); ok {
			st.Axes[a] = e.Value
		}
	}
}

// parseButton parses the name of a button event: a trigger is an axis.
func parseButton(name string) (control.Control, bool) {
	c, ok := control.Parse(name)
	return c, ok && !c.IsTrigger()
}

// Resync makes the slot's state st at the clock's Now by handling the
// edges it missed: releases, then presses, then the axes that differ,
// each group in control/axis order. It returns how many it handled.
//
// The edges go through Handle as events with Time and Device 0, so
// OnEvent and Trace see them. Entries of st that are not a button or an
// axis (control.Invalid, the triggers as buttons, control.InvalidAxis)
// are ignored.
func (s *Slot) Resync(st State) int {
	n := 0
	for _, down := range []bool{false, true} {
		kind := input.ButtonUp
		if down {
			kind = input.ButtonDown
		}
		for _, c := range control.All() {
			if c.IsTrigger() || st.Buttons[c] != down || s.state.Buttons[c] == down {
				continue
			}
			s.Handle(input.Event{Kind: kind, Control: c.String()})
			n++
		}
	}
	for _, a := range control.AllAxes() {
		if st.Axes[a] != s.state.Axes[a] {
			s.Handle(input.Event{Kind: input.AxisMotion, Control: a.String(), Value: st.Axes[a]})
			n++
		}
	}
	s.updatePad(s.clk.Now())
	if s.scriptDisconnected {
		s.scriptDisconnected = false
		if !s.Paused() {
			if p, ok := s.script.(scriptPauser); ok {
				s.keep(p.SetPaused(false))
			}
			s.scriptReleased = false
		}
	}
	return n
}

// Reset lets go of everything and forgets the device's state, as a
// DeviceRemoved does: ReleaseAll, then the engine counts every control as
// up, and State is zeroed. It is for when events were lost and
// the state is unknown.
func (s *Slot) Reset() { s.reset(s.clk.Now()) }

func (s *Slot) reset(now clock.Time) {
	// Close the script gate before ReleaseAll, so no replacement worker is
	// scheduled between releasing the old owner and forgetting the device.
	if p, ok := s.script.(scriptPauser); ok {
		s.keep(p.SetPaused(true))
	}
	s.keep(s.releaseAll())
	s.eng.Reset(now)
	s.state = State{}
	clear(s.scopes[:])
	clear(s.scriptCycles[:])
	clear(s.scriptStarted[:])
	clear(s.scriptCanceled[:])
	clear(s.macroCanceled[:])
	s.scriptDisconnected = true
	s.updatePad(now)
}
