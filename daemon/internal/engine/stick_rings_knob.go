package engine

import (
	"math"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// This file is the stick rings (ANA-10) and the virtual knob (ANA-11). Both
// are recognizers on top of stickReading, driven by updateStick, Apply and
// settleVirtual (ARCHITECTURE §7.5).

// ringControls returns the controls of the inner and outer ring of stick
// which.
func ringControls(which int) (inner, outer control.Control) {
	if which == 0 {
		return control.LSInner, control.LSOuter
	}
	return control.RSInner, control.RSOuter
}

// knobControl returns the control of one clockwise or counter-clockwise tick
// of stick which.
func knobControl(which int, cw bool) control.Control {
	switch {
	case which == 0 && cw:
		return control.LSCW
	case which == 0:
		return control.LSCCW
	case cw:
		return control.RSCW
	}
	return control.RSCCW
}

// ringWant returns the rings stick s is in for reading rd: outer from the
// outer threshold on, until the depth falls RingHysteresis below it; inner
// while the stick is active and not outer. With an outer threshold at or
// below the direction threshold every active stick is outer, so inner never
// goes down.
func ringWant(s *stickState, rd *stickReading) (inner, outer bool) {
	outer = s.outer
	switch {
	case !rd.active:
		outer = false
	case rd.norm >= rd.cfg.OuterThreshold:
		outer = true
	case rd.norm < rd.cfg.OuterThreshold-RingHysteresis:
		outer = false
	}
	return rd.active && !outer, outer
}

// updateRings steps the ring controls of stick which for reading rd: the
// releases first, then the presses, so that a stick going from the inner to
// the outer ring releases inner before it presses outer. A ring is pressed
// only when a binding (or a script) wants it, and released only when it is
// down, like ls.active. The rings are not delayed by directionDelayMs.
func (e *Engine) updateRings(t clock.Time, which int, rd *stickReading) {
	s := &e.sticks[which]
	inner, outer := ringWant(s, rd)
	wasInner, wasOuter := s.inner, s.outer
	if inner == wasInner && outer == wasOuter {
		return
	}
	s.inner, s.outer = inner, outer
	innerCtl, outerCtl := ringControls(which)
	if wasInner && !inner && e.physical[innerCtl] {
		e.stepButton(t, innerCtl, false, false)
	}
	if wasOuter && !outer && e.physical[outerCtl] {
		e.stepButton(t, outerCtl, false, false)
	}
	if !wasInner && inner && e.virtualBound(innerCtl) {
		e.stepButton(t, innerCtl, true, false)
	}
	if !wasOuter && outer && e.virtualBound(outerCtl) {
		e.stepButton(t, outerCtl, true, false)
	}
}

// angleDelta returns the signed shortest rotation from angle from to angle to
// in degrees, in (-180, 180]: positive is clockwise.
func angleDelta(from, to float64) float64 {
	d := math.Remainder(to-from, 360)
	if d <= -180 {
		d += 360
	}
	return d
}

// updateKnob is called for each axis input of a stick. In mode "knob" it
// does not read the angle: the two axes of one report arrive as two inputs,
// and the angle between them is not the stick's. It schedules the reading
// KnobSettle after the first input that nothing is waiting on (later inputs do
// not push it out, so a continuous turn is still read every KnobSettle), and
// Apply runs settleKnob then. A stick at rest and not engaged schedules
// nothing.
func (e *Engine) updateKnob(t clock.Time, which int, rd *stickReading) {
	s := &e.sticks[which]
	if e.inputOnly {
		return
	}
	if rd.cfg.Mode != "knob" {
		s.knobOn, s.knobEvalSet = false, false
		e.emitKnob(t, which)
		return
	}
	if !s.knobEvalSet && (s.knobOn || rd.active) {
		s.knobEvalSet, s.knobEvalAt = true, t.Add(KnobSettle)
	}
}

// settleKnob follows the angle of a stick in mode "knob", read from the axes
// as they are at now. The stick is engaged while it is active: it takes the
// angle it was engaged at as the reference, and every full step
// (knobStepDeg) of rotation away from the reference queues one tick and
// moves the reference by a step. Leaving the active zone forgets the
// reference, so the spring back to the center makes no ticks. Ticks already
// queued stay queued and are emitted by emitKnob.
func (e *Engine) settleKnob(now clock.Time, which int) {
	s := &e.sticks[which]
	s.knobEvalSet = false
	rd := e.readStick(which)
	if rd.cfg.Mode != "knob" || !rd.active {
		s.knobOn = false
		e.emitKnob(now, which)
		return
	}
	// Engaging, and turning while paused, take the angle as it is: the
	// stick may turn while paused, but nothing is emitted after the resume.
	if !s.knobOn || e.paused {
		s.knobOn, s.knobRef = true, rd.angle
		return
	}
	step := float64(rd.cfg.KnobStepDeg)
	limit := int(math.Ceil(360 / step))
	for d := angleDelta(s.knobRef, rd.angle); d >= step || d <= -step; d = angleDelta(s.knobRef, rd.angle) {
		cw := d >= step
		if cw {
			s.knobRef = math.Mod(s.knobRef+step, 360)
		} else {
			s.knobRef = math.Mod(s.knobRef-step+360, 360)
		}
		// A tick the other way first cancels a queued one. At most one
		// revolution is queued; the ticks beyond that are dropped.
		if cw && s.ticks < limit {
			s.ticks++
		} else if !cw && s.ticks > -limit {
			s.ticks--
		}
	}
	e.emitKnob(now, which)
}

// emitKnob emits at most one queued tick of stick which, when its time has
// come, and never more than one per call: ticks do not catch up in a burst.
// A tick is a press and a release of its control at the same time, which go
// through the normal gesture path (decisions, scripts, observers). After a
// tick the next one waits for the minimum period of the key its control
// resolves to (tap press plus tap gap), so the output keeps up with it. A
// tick whose control nothing binds is dropped. With knobTick the step also
// plays the tick haptic (HUD-10), just before its key; a dropped tick plays
// none.
func (e *Engine) emitKnob(now clock.Time, which int) {
	s := &e.sticks[which]
	if s.ticks == 0 || e.inputOnly {
		return
	}
	c := knobControl(which, s.ticks > 0)
	if !e.virtualBound(c) {
		s.ticks = 0
		return
	}
	if now < s.knobNext {
		return
	}
	if s.ticks > 0 {
		s.ticks--
	} else {
		s.ticks++
	}
	_, b, _, _ := e.lookup(c, GesturePress)
	s.knobNext = now.Add(max(time.Nanosecond, e.repeatMinimum(b)))
	// The tick of the knob (HUD-10) is played where the step's binding is
	// dispatched (runAction, knobTick), not here: only the dispatch knows
	// whether a declarative binding ran.
	e.stepButton(now, c, true, false)
	e.stepButton(now, c, false, false)
}

// clearKnob drops the queued ticks of every stick, and a reading that was
// waiting to be taken, and lets the next tick go at once.
func (e *Engine) clearKnob() {
	for i := range e.sticks {
		e.sticks[i].ticks = 0
		e.sticks[i].knobNext = 0
		e.sticks[i].knobEvalSet = false
	}
}
