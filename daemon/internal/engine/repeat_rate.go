package engine

import (
	"math"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// This file is the rate-modulated repeat (ANA-12, ANA-15) and the response
// curves (ANA-13). A repeat that sets minRateHz goes at minRateHz when its
// control has just reached its activation threshold, and at its rate at full
// travel; how far a control is pushed is its depth, and the curve of its stick
// or trigger shapes what lies between. The curve acts on the depth for the rate
// only: no threshold moves with it, so changing a curve never changes where a
// key fires (ARCHITECTURE §7.6).

// curveKind is a response curve, by profile name.
type curveKind uint8

const (
	curveLinear curveKind = iota
	curveQuadratic
	curveCubic
)

// parseCurve returns the curve called name; the empty name is linear, as in a
// model built by hand that does not set one.
func parseCurve(name string) (curveKind, bool) {
	switch name {
	case "", profile.CurveLinear:
		return curveLinear, true
	case profile.CurveQuadratic:
		return curveQuadratic, true
	case profile.CurveCubic:
		return curveCubic, true
	}
	return curveLinear, false
}

// shape applies the curve to a depth in [0, 1].
func (k curveKind) shape(d float64) float64 {
	switch k {
	case curveQuadratic:
		return d * d
	case curveCubic:
		return d * d * d
	}
	return d
}

// The curves of a Program: the two sticks, then the two triggers.
const (
	curveLS = iota
	curveRS
	curveLT
	curveRT
)

// depthKind is how a control gets its depth.
type depthKind uint8

const (
	depthNone        depthKind = iota
	depthStick                 // ls.up, ls.active, ls.outer, ...: the stick's norm
	depthStickAxis             // leftx+, righty-, ...: one side of one axis
	depthTrigger               // lt, rt: the first stage
	depthSecond                // lt.full, rt.full: the second stage
	depthTriggerAxis           // lefttrigger+, ...: the trigger as a half axis
)

// depthSource says which reading a control's depth comes from. stick is 0 for
// the left stick and 1 for the right, and trig is 0 for LT and 1 for RT.
type depthSource struct {
	kind  depthKind
	stick int
	trig  int
	axis  control.Axis // depthStickAxis, depthTriggerAxis
	neg   bool         // the negative side of axis
}

// sourceOf returns where the depth of control c comes from: depthNone for a
// control that has none (control.Control.HasDepth).
func sourceOf(c control.Control) depthSource {
	switch {
	case c == control.LT || c == control.RT:
		return depthSource{kind: depthTrigger, trig: int(c - control.LT)}
	case c == control.LTFull:
		return depthSource{kind: depthSecond}
	case c == control.RTFull:
		return depthSource{kind: depthSecond, trig: 1}
	case c >= control.LSUp && c <= control.LSActive, c == control.LSInner, c == control.LSOuter:
		return depthSource{kind: depthStick}
	case c >= control.RSUp && c <= control.RSActive, c == control.RSInner, c == control.RSOuter:
		return depthSource{kind: depthStick, stick: 1}
	case c >= control.LeftXPositive && c <= control.RightTriggerNegative:
		i := int(c - control.LeftXPositive)
		a := control.LeftX + control.Axis(i/2)
		s := depthSource{kind: depthStickAxis, stick: i / 4, axis: a, neg: i%2 == 1}
		if a == control.LeftTrigger || a == control.RightTrigger {
			s.kind, s.stick, s.trig = depthTriggerAxis, 0, int(a-control.LeftTrigger)
		}
		return s
	}
	return depthSource{}
}

// uses reports whether the depth reads axis a.
func (s depthSource) uses(a control.Axis) bool {
	switch s.kind {
	case depthStick:
		return a == axisForStick(s.stick, true) || a == axisForStick(s.stick, false)
	case depthStickAxis, depthTriggerAxis:
		return a == s.axis
	case depthTrigger, depthSecond:
		return a == control.LeftTrigger+control.Axis(s.trig)
	}
	return false
}

// unit returns x clamped to [0, 1].
func unit(x float64) float64 {
	switch {
	case x < 0:
		return 0
	case x > 1:
		return 1
	}
	return x
}

// above returns how far v is from threshold toward 1, in [0, 1]: 0 at the
// threshold or below it, 1 at the end of the travel. A threshold at or above 1
// leaves nothing to travel, and the control is as deep as it gets.
func above(v, threshold float64) float64 {
	if threshold >= 1 {
		return 1
	}
	return unit((v - threshold) / (1 - threshold))
}

// axisThreshold is the fraction of an axis a half-axis control needs
// (axisThreshold, else the left stick's directionThreshold, else the default).
func (e *Engine) axisThreshold() float64 {
	threshold := e.prog.analog.AxisThreshold
	if threshold <= 0 {
		threshold = e.prog.analog.LS.DirectionThreshold
	}
	if threshold <= 0 {
		threshold = DefaultDirectionThreshold
	}
	return threshold
}

// depth returns how far the control behind s is pushed now, in [0, 1], before
// the curve: 0 at its activation threshold and 1 at full travel.
func (e *Engine) depth(s depthSource) float64 {
	switch s.kind {
	case depthStick:
		rd := e.readStick(s.stick)
		return above(rd.norm, rd.cfg.DirectionThreshold)
	case depthStickAxis:
		v := float64(e.axes[s.axis]) / 32767
		if s.neg {
			v = -v
		}
		return above(v, e.axisThreshold())
	case depthTriggerAxis:
		v := float64(e.axes[s.axis]) / 32767
		if s.neg {
			v = 0
		}
		return above(max(v, 0), e.axisThreshold())
	case depthTrigger:
		return above(max(float64(e.axes[control.LeftTrigger+control.Axis(s.trig)])/32767, 0), e.triggerSettings(s.trig).Press)
	case depthSecond:
		return above(max(float64(e.axes[control.LeftTrigger+control.Axis(s.trig)])/32767, 0), e.triggerSettings(s.trig).FullPress)
	}
	return 0
}

// triggerSettings returns the settings of trigger i, 0 for LT and 1 for RT.
func (e *Engine) triggerSettings(i int) profile.Trigger {
	if i == 1 {
		return e.prog.analog.RT
	}
	return e.prog.analog.LT
}

// curveOf returns the curve that shapes the depth of s.
func (e *Engine) curveOf(s depthSource) curveKind {
	switch s.kind {
	case depthStick, depthStickAxis:
		return e.prog.curves[curveLS+s.stick]
	case depthTrigger, depthSecond, depthTriggerAxis:
		return e.prog.curves[curveLT+s.trig]
	}
	return curveLinear
}

// repeatInterval returns the time between two repeats of action a of control
// c, now: the repeat rate, or for a repeat with minRateHz the rate its
// control's depth gives, and in either case no shorter than the same key can
// follow (repeatMinimum). mod is whether the repeat is rate-modulated.
func (e *Engine) repeatInterval(c control.Control, a *profile.Action, binding *profile.Binding, mod bool) time.Duration {
	rate := profile.DefaultRepeatRateHz
	if a.Repeat != nil {
		rate = a.Repeat.EffectiveRateHz()
	}
	if mod {
		src := sourceOf(c)
		lo := float64(min(a.Repeat.MinRateHz, rate))
		hz := lo + (float64(rate)-lo)*e.curveOf(src).shape(e.depth(src))
		// Whole microseconds: no timer is finer, and the last bit of a float
		// does not reach the schedule.
		period := time.Duration(math.Round(float64(time.Second/time.Microsecond)/hz)) * time.Microsecond
		return max(time.Nanosecond, period, e.repeatMinimum(binding))
	}
	return max(time.Nanosecond, time.Second/time.Duration(rate), e.repeatMinimum(binding))
}

// isModulated reports whether the repeat of action a on control c follows the
// control's depth.
func isModulated(c control.Control, a *profile.Action) bool {
	return a.Repeat != nil && a.Repeat.MinRateHz > 0 && c.HasDepth()
}

// retimeRepeats moves the next repeat of every rate-modulated repeat that
// reads axis a: when the depth of its control changes, the next repeat is one
// period of the new depth after the last one, earlier or later than it was
// planned. A repeat still in its initial delay is not moved, and one that is
// already due goes once at the next Apply, not once for each period missed.
func (e *Engine) retimeRepeats(a control.Axis) {
	if e.modulated == 0 {
		return
	}
	n := 0
	for c := int(control.LT); c < control.Count; c++ {
		for i := range e.actions[c] {
			st := &e.actions[c][i]
			if st.action == nil || !st.mod {
				continue
			}
			n++
			if st.armed && sourceOf(control.Control(c)).uses(a) {
				st.next = st.last.Add(e.repeatInterval(control.Control(c), st.action, st.binding, true))
			}
		}
	}
	// A recount, so that the guard in front of the loop cannot drift.
	e.modulated = n
}

// startRepeat plans the repeats of action a of control c, started at now. The
// first one waits delay; a delay of 0 means the first period, and then a
// rate-modulated repeat is on its depth from the start.
func (e *Engine) startRepeat(now clock.Time, c control.Control, st *ctlState, a *profile.Action, owner OwnerID) {
	for i := range e.actions[c] {
		if e.actions[c][i].action != nil {
			continue
		}
		delay := DefaultRepeatDelay
		if a.Repeat.DelaySet || a.Repeat.DelayMs > 0 {
			delay = time.Duration(a.Repeat.DelayMs) * time.Millisecond
		}
		style := a.Repeat.StyleFor(a.Mode)
		mod := isModulated(c, a)
		noDelay := delay == 0
		if noDelay {
			delay = e.repeatInterval(c, a, st.binding, mod)
		}
		if style == profile.RepeatTap {
			delay = max(delay, e.repeatMinimum(st.binding))
		}
		e.actions[c][i] = actionState{
			action: a, owner: owner, binding: st.binding, style: style, next: now.Add(delay), repeat: true,
			last: now, mod: mod, armed: mod && noDelay,
		}
		if mod {
			e.modulated++
		}
		return
	}
}
