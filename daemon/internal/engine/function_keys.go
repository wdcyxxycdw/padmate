package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The function key (ADR-0012, ADR-0017): back (View), start (Menu) or, on a
// Joy-Con held upright, a shoulder button (L, R) or a trigger (ZL, ZR: LT,
// RT) is the function key, the same in every profile; the controller's
// settings in config.json choose it
// (WithFunctionKey of Compile; start by default; Program.ForFunctionKey for
// the controllers that have another). guide (the Xbox, PS or Home
// button) cannot be: padmate never maps it, and lookupWith finds nothing on it. It sends nothing and is not bindable: a binding
// on it in a layer, in common or in the function set is left out by Compile
// (Program.IgnoredFunctionKeyBindings). Its edges never enter gesture
// recognition: they start no cycle, are not buffered, are not seen by a
// script's handlers, and do not interrupt the pending gesture of another
// control, so they add no delay to anything. All they do is tell the presses
// that come after them which set of bindings to look at first.
//
// While the key is held, a press looks its control up in the profile's
// function set before every layer (lookupWith); a control the set does not
// bind falls through to the normal lookup. The set is chosen when the press
// arrives, not when a buffered press is replayed (Input.fn), and the release
// goes to the binding the press chose (press-owner, ARCHITECTURE §7.4): a key
// that is down when the function key goes down keeps its normal binding, and
// one pressed under it keeps the set's after the function key is let go.
//
// The function key is physical state, like any button: CancelAll, Pause and
// Reload (with the same function key) leave it as it is (they send nothing to
// undo), Reset, for a controller that went away, clears it. A Reload that
// changes the function key clears it too: the new key is a control like any
// other until it is pressed again (Reload). The function key has no feedback
// of its own: no pulse, color, layer or HUD.
//
// A trigger as the function key (LT, RT) is the virtual button of the trigger
// axis, pressed and released at the profile's trigger thresholds like any
// trigger. Its second stage (lt.full, rt.full) and half axis are never pressed
// while it is the key (onFunctionKey), and the trigger still holds no output.

// ActiveFunctionKey returns the function key when it is held, and
// control.Invalid when it is not.
func (e *Engine) ActiveFunctionKey() control.Control { return e.fnActive }

// stepFunctionKey handles an edge of the function key, whose physical state is
// already updated. It records a decision (ReasonFunction) and tells the
// presses to come which set to look up.
func (e *Engine) stepFunctionKey(t clock.Time, down bool) {
	was := e.fnActive
	e.fnActive = control.Invalid
	if down {
		e.fnActive = e.prog.fnKey
	}
	if e.trace != nil {
		e.trace(Decision{T: t, Control: e.prog.fnKey, Edge: edgeOf(down), Reason: ReasonFunction})
	}
	if e.fnActive != was && e.hints.on {
		// The key hints show the set that a press would use now.
		e.hints.list = e.hintList()
	}
}

// functionSet returns the function set and its level name when fn, the
// function key held, is one: nil when there is none held or the profile has
// no set.
func (p *Program) functionSet(fn control.Control) (*profile.Layer, string) {
	if fn == control.Invalid || p.fnSet == nil {
		return nil, ""
	}
	return p.fnSet, p.fnLevel
}

// isFunctionLevel reports whether level is the name of the function set of p.
func (p *Program) isFunctionLevel(level string) bool { return level != "" && level == p.fnLevel }

// onFunctionKey reports whether c is the function key of p or sits on the same
// physical control: when the function key is a trigger, its second stage
// (lt.full, rt.full) and its half axis (lefttrigger+, righttrigger+) are the
// same trigger, so a binding on them is left out by Compile like one on the
// key, and the engine never presses them (stepTrigger, stepHalfAxis).
func (p *Program) onFunctionKey(c control.Control) bool {
	if c == p.fnKey {
		return true
	}
	switch p.fnKey {
	case control.LT:
		return c == control.LTFull || c == control.LeftTriggerPositive
	case control.RT:
		return c == control.RTFull || c == control.RightTriggerPositive
	}
	return false
}
