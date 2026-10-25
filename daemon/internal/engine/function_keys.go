package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The function keys (MAP-27, ADR-0069): back (View) and start (Menu) are
// function keys in every profile. They send nothing and are not bindable; a
// binding on them in a layer is dropped by Compile. Their edges never enter
// gesture recognition: they start no cycle, are not buffered, are not seen by
// a script's handlers, and do not interrupt the pending gesture of another
// control, so they add no delay to anything. All they do is tell the presses
// that come after them which set of bindings to look at first.
//
// The active function key is the one pressed first of those still held: the
// other, pressed while it is held, does not switch the set, and once the first
// is let go the other becomes the active one. While one is active, a press
// looks its control up in that key's set before every layer (lookupWith); a
// control the set does not bind falls through to the normal lookup. The set is
// chosen when the press arrives, not when a buffered press is replayed
// (Input.fn), and the release goes to the binding the press chose (press-owner,
// ARCHITECTURE §7.4): a key that is down when the function key goes down keeps
// its normal binding, and one pressed under it keeps the set's after the
// function key is let go.
//
// The function keys are physical state, like any button: CancelAll, Pause and
// Reload leave the active key as it is (they send nothing to undo), Reset, for
// a controller that went away, clears it. They have no feedback of their own:
// no pulse, color, layer or HUD (ADR-0069).

// ActiveFunctionKey returns the function key whose set a press looks up first,
// control.Back or control.Start, or control.Invalid when none is held.
func (e *Engine) ActiveFunctionKey() control.Control { return e.fnActive }

// otherFunctionKey returns the function key that is not c.
func otherFunctionKey(c control.Control) control.Control {
	if c == control.Back {
		return control.Start
	}
	return control.Back
}

// stepFunctionKey handles an edge of function key c, whose physical state is
// already updated. It records a decision (ReasonFunction) and moves the active
// key.
func (e *Engine) stepFunctionKey(t clock.Time, c control.Control, down bool) {
	was := e.fnActive
	switch {
	case down:
		if e.fnActive == control.Invalid || !e.physical[e.fnActive] {
			e.fnActive = c
		}
	case e.fnActive == c:
		e.fnActive = control.Invalid
		if other := otherFunctionKey(c); e.physical[other] {
			e.fnActive = other
		}
	}
	if e.trace != nil {
		e.trace(Decision{T: t, Control: c, Edge: edgeOf(down), Reason: ReasonFunction})
	}
	if e.fnActive != was && e.hints.on {
		// The key hints show the set that a press would use now.
		e.hints.list = e.hintList()
	}
}

// functionSet returns the set of function key fn and its level name, or nil
// when fn has none or is not a function key.
func (p *Program) functionSet(fn control.Control) (*profile.Layer, string) {
	switch fn {
	case control.Back:
		return p.fnSets[0], levelFunctionBack
	case control.Start:
		return p.fnSets[1], levelFunctionStart
	}
	return nil, ""
}

// isFunctionLevel reports whether level is the name of a function key set.
func isFunctionLevel(level string) bool {
	return level == levelFunctionBack || level == levelFunctionStart
}

// The names of the levels of the function key sets, as profile.FunctionLevel
// gives them, in lookups and decisions.
var (
	levelFunctionBack  = profile.FunctionLevel(control.Back)
	levelFunctionStart = profile.FunctionLevel(control.Start)
)
