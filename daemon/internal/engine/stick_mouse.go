package engine

import (
	"math"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// This file is the stick that moves the mouse pointer (MOU-01, MOU-04, MOU-05,
// MOU-06; ADR-0062, section 5). A stick in mode "mouse" has a target velocity,
// set at each axis input of the stick from its position: the direction of the
// push, at mouseSpeed times the curve of its depth times the precision factor.
// The depth is how far the stick is pushed between the edge of the deadzone and
// the saturation radius (stickReading.norm), so the pointer starts as soon as
// the stick leaves the deadzone; directionThreshold is for the controls
// (ls.active and the rings), not for the pointer. The velocity the pointer has follows the target through an exponential moving
// average (mouseSmoothingMs), and the pointer's distance is the integral of that
// velocity over the time the clock says has passed.
//
// The integral is exact for a target that is constant between two inputs, and
// does not depend on when the ticks come: every input and every tick first adds
// the time since the last one, at the old target, to a sub-pixel accumulator.
// The ticks only send the whole pixels in it, MouseTick apart, with the rest
// kept for the next one. So the two inputs of an axis report, which reach the
// engine microseconds apart, only ever have their half-updated target in force
// for those microseconds, and the pointer's distance over a push is the time of
// the push times its speed, however the ticks fall.

const (
	// MouseTick is how often a stick in mode "mouse" sends the pixels it has
	// moved by while its pointer is moving: 125 Hz, the rate of a gaming mouse
	// at its slowest and the rate Windows is happy to take, and well above the
	// refresh rate of the displays a drawing tablet's user looks at.
	MouseTick = 8 * time.Millisecond
	// MouseMaxDT is the longest time one step of the pointer integrates. After a
	// stall of the slot (a descheduled goroutine, a suspended laptop) the time
	// beyond it is dropped, so the pointer does not jump by what it would have
	// moved in the stall.
	MouseMaxDT = 50 * time.Millisecond
	// MouseStopSpeed is the speed, in pixels per second, under which a pointer
	// whose stick is back in the center is taken to have stopped. The smoothed
	// velocity only approaches 0, so something has to end the ticks.
	MouseStopSpeed = 0.5
)

// mouseTau returns the smoothing time constant of stick which in seconds.
func (e *Engine) mouseTau(which int) float64 {
	a := &e.prog.analog.LS
	if which == 1 {
		a = &e.prog.analog.RS
	}
	return float64(a.MouseSmoothingMs) / 1000
}

// precisionFactor returns the speed factor of the precision actions held now: 1
// when none is.
func (e *Engine) precisionFactor() float64 {
	if e.precision > 0 {
		return e.precision
	}
	return 1
}

// mouseTarget returns the velocity, in pixels per second right and down, the
// stick which wants for reading rd: nothing inside the deadzone, else its
// direction at mouseSpeed times the curve of the push depth (norm: 0 at the edge
// of the deadzone, 1 at saturation) times the precision factor. A screen's y
// grows downward, so a stick pushed up (angle 0) gives a negative y.
func (e *Engine) mouseTarget(which int, rd *stickReading) (tx, ty float64) {
	if rd.norm <= 0 {
		return 0, 0
	}
	speed := float64(rd.cfg.MouseSpeed) * e.prog.curves[curveLS+which].shape(rd.norm) * e.precisionFactor()
	sin, cos := math.Sincos(rd.angle * math.Pi / 180)
	return sin * speed, -cos * speed
}

// integrateMouse adds to the accumulator of s the distance its pointer moved
// from the time of the last step to now, and moves the velocity to where the
// smoothing has it then, both at the target as it was since the last step. Time
// that goes back counts as none, and a step of more than MouseMaxDT counts as
// MouseMaxDT (a stall). tau is the smoothing time constant in seconds; with 0
// the velocity is the target.
//
// For a constant target u, a starting velocity v0 and a time dt, the average
// of an exponential moving average with time constant tau moves by
// u*dt + (v0-u)*tau*(1-e^(-dt/tau)) and ends at u + (v0-u)*e^(-dt/tau).
func integrateMouse(s *stickState, now clock.Time, tau float64) {
	d := time.Duration(now - s.mouseLast)
	if d <= 0 {
		return
	}
	s.mouseLast = now
	dt := min(d, MouseMaxDT).Seconds()
	if tau <= 0 {
		s.mouseVx, s.mouseVy = s.mouseTx, s.mouseTy
		s.mouseAx += s.mouseTx * dt
		s.mouseAy += s.mouseTy * dt
		return
	}
	k := math.Exp(-dt / tau)
	s.mouseAx += s.mouseTx*dt + (s.mouseVx-s.mouseTx)*tau*(1-k)
	s.mouseAy += s.mouseTy*dt + (s.mouseVy-s.mouseTy)*tau*(1-k)
	s.mouseVx = s.mouseTx + (s.mouseVx-s.mouseTx)*k
	s.mouseVy = s.mouseTy + (s.mouseVy-s.mouseTy)*k
}

// resetMouse forgets the motion of s: the target, the velocity, the pixels not
// sent yet and the tick.
func resetMouse(s *stickState) {
	s.mouseOn, s.mouseAt, s.mouseLast = false, 0, 0
	s.mouseTx, s.mouseTy, s.mouseVx, s.mouseVy, s.mouseAx, s.mouseAy = 0, 0, 0, 0, 0, 0
}

// updateMouse is called for each axis input of a stick, after the rest of
// updateStick. In mode "mouse" it settles the motion up to t at the old target,
// then takes the target of the reading rd, and starts the ticks when the stick
// is moving the pointer and they were not running. A stick pushed across a
// safety boundary (mouseLock) does nothing until it is back inside the deadzone;
// while paused (and in an input preview) the pointer does not move. In any other
// mode it forgets the motion.
func (e *Engine) updateMouse(t clock.Time, which int, rd *stickReading) {
	s := &e.sticks[which]
	if e.inputOnly {
		return
	}
	if rd.cfg.Mode != "mouse" {
		resetMouse(s)
		s.mouseLock = false
		return
	}
	if s.mouseLock {
		if rd.norm > 0 {
			return
		}
		s.mouseLock = false
	}
	if e.paused {
		return
	}
	if s.mouseOn {
		integrateMouse(s, t, e.mouseTau(which))
	}
	s.mouseTx, s.mouseTy = e.mouseTarget(which, rd)
	if !s.mouseOn && (s.mouseTx != 0 || s.mouseTy != 0) {
		s.mouseOn, s.mouseLast, s.mouseAt = true, t, t.Add(MouseTick)
		s.mouseVx, s.mouseVy, s.mouseAx, s.mouseAy = 0, 0, 0, 0
	}
}

// tickMouse is the tick of stick which at now: it settles the motion up to now,
// sends the whole pixels of it, keeps the fractions, and plans the next tick
// unless the pointer has come to rest (the target is 0 and the velocity has
// decayed under MouseStopSpeed), when it forgets the fractions as well. A tick
// that falls late is followed by one MouseTick after it, not by a burst.
func (e *Engine) tickMouse(now clock.Time, which int) {
	s := &e.sticks[which]
	integrateMouse(s, now, e.mouseTau(which))
	dx, dy := wholePixels(s.mouseAx), wholePixels(s.mouseAy)
	s.mouseAx -= dx
	s.mouseAy -= dy
	if dx != 0 || dy != 0 {
		e.out.Move(OwnerID{Source: SourceDecl}, int32(dx), int32(dy))
	}
	if s.mouseTx == 0 && s.mouseTy == 0 && math.Hypot(s.mouseVx, s.mouseVy) < MouseStopSpeed {
		resetMouse(s)
		return
	}
	next := s.mouseAt.Add(MouseTick)
	if next <= now {
		next = now.Add(MouseTick)
	}
	s.mouseAt = next
}

// wholePixels returns the whole pixels in the distance a, toward 0. A sum a
// hair short of a whole number, which the float arithmetic of the intervals
// leaves (7.999999999999999 for 8), counts as that number.
func wholePixels(a float64) float64 {
	return math.Trunc(a + math.Copysign(1e-9, a))
}

// runMouseAction runs a mouse action of the cycle st at now: precision starts
// holding its factor for as long as the cycle holds (the control's release,
// CancelAll or a latch replaced by the next press end it), jump puts the pointer
// at its position once.
func (e *Engine) runMouseAction(now clock.Time, st *ctlState, a *profile.Action, owner OwnerID) {
	switch a.Op {
	case profile.MouseOpPrecision:
		if st.precision == 0 || a.Factor < st.precision {
			st.precision = a.Factor
		}
		e.updatePrecision(now)
	case profile.MouseOpJump:
		e.out.Jump(owner, jumpCoordinate(a.X), jumpCoordinate(a.Y))
	}
}

// jumpCoordinate converts a position as a fraction of the virtual desktop to
// the 0 to 65535 of an absolute move.
func jumpCoordinate(f float64) int32 {
	return int32(math.Round(unit(f) * 65535))
}

// updatePrecision sets the precision factor to the smallest of the precision
// actions held now, those of the controls held (ctlState.precision) and of the
// latched ones (latchState.precision). When it changes, a pointer that is moving
// first settles its motion up to now at the old factor, then takes its target
// from the stick again with the new one; the smoothing eases it over.
func (e *Engine) updatePrecision(now clock.Time) {
	f := 0.0 // none
	for c := range e.ctl {
		if p := e.ctl[c].precision; p > 0 && (f == 0 || p < f) {
			f = p
		}
		if p := e.latch[c].precision; p > 0 && (f == 0 || p < f) {
			f = p
		}
	}
	if f == e.precision {
		return
	}
	for i := range e.sticks {
		if s := &e.sticks[i]; s.mouseOn {
			integrateMouse(s, now, e.mouseTau(i))
		}
	}
	e.precision = f
	for i := range e.sticks {
		if s := &e.sticks[i]; s.mouseOn {
			rd := e.readStick(i)
			s.mouseTx, s.mouseTy = e.mouseTarget(i, &rd)
		}
	}
}
