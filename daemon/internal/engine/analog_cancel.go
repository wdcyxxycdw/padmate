package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// settleVirtual recognizes held analog inputs without starting new cycles.
// An input crossing a safety boundary must leave before it may enter again.
// It releases the controls the new settings no longer want, the rings and the
// second trigger stage among them, and never presses one; queued knob ticks
// are gone (CancelAll), and at a reload the knob takes the angle it is at as
// its reference.
//
// The pointer of a stick in mode "mouse" depends on lockPointer. With it (the
// safety boundaries: CancelAll) it stops and a stick still pushed is locked
// until it is back inside the deadzone. Without it (a reload or a calibration
// change) it only takes the new settings: a stick that is still pushed in mode
// "mouse" keeps moving the pointer with them, and a lock from before stays.
func (e *Engine) settleVirtual(now clock.Time, reload, lockPointer bool) {
	for i := range e.sticks {
		s := &e.sticks[i]
		s.pendingSet, s.deadline = false, 0
		if reload {
			s.current = [8]bool{}
		}
		rd := e.readStick(i)
		want := e.directionsFor(i, &rd)
		e.releaseMissing(now, stickControls(i), want)
		s.current = want
		c := control.LSActive
		if i == 1 {
			c = control.RSActive
		}
		if !rd.active && e.physical[c] {
			e.stepButton(now, c, false, false)
		}
		s.active = rd.active
		inner, outer := ringWant(s, &rd)
		innerCtl, outerCtl := ringControls(i)
		if !inner && e.physical[innerCtl] {
			e.stepButton(now, innerCtl, false, false)
		}
		if !outer && e.physical[outerCtl] {
			e.stepButton(now, outerCtl, false, false)
		}
		s.inner, s.outer = inner, outer
		if reload {
			s.knobOn, s.knobRef = rd.active && rd.cfg.Mode == "knob" && !e.inputOnly, rd.angle
		}
		if lockPointer {
			// The pointer stops, with the pixels it has not sent and its smoothed
			// speed, and a stick still pushed has to come back inside the deadzone
			// before it moves it again: nothing starts across a safety boundary.
			resetMouse(s)
			s.mouseLock = rd.norm > 0 && rd.cfg.Mode == "mouse" && !e.inputOnly
		} else {
			e.updateMouse(now, i, &rd)
		}
	}
	want := [8]bool{}
	switch e.prog.analog.Dpad.Mode {
	case "eightWay":
		want = e.dpadWantEight()
	case "fourWay":
		want = e.dpadWantFour()
	default:
		copy(want[:4], e.dpadPhysical[:])
	}
	e.releaseMissing(now, dpadControls(), want)
	e.dpadVirtual = want
	if reload {
		e.settleTriggerFull(now)
		for a := control.Axis(1); int(a) < control.AxisCount; a++ {
			pos, neg := e.halfAxisState(a, e.axes[a])
			for i, on := range [2]bool{pos, neg} {
				c := halfAxisControl(a, i == 0)
				if !on && e.physical[c] {
					e.stepButton(now, c, false, false)
				}
				e.halfAxis[int(a)*2+i] = on
			}
		}
	}
}

func (e *Engine) releaseMissing(now clock.Time, cs [8]control.Control, want [8]bool) {
	for i, c := range cs {
		if e.physical[c] && !want[i] {
			e.stepButton(now, c, false, false)
		}
	}
}

func (e *Engine) trackDpadButton(c control.Control, down bool) {
	i := dpadIndex(c)
	if e.dpadPhysical[i] == down {
		return
	}
	e.dpadPhysical[i] = down
	if down {
		e.dpadOrder[e.dpadOrderN] = c
		e.dpadOrderN++
		return
	}
	for n := 0; n < e.dpadOrderN; n++ {
		if e.dpadOrder[n] == c {
			copy(e.dpadOrder[n:], e.dpadOrder[n+1:e.dpadOrderN])
			e.dpadOrderN--
			return
		}
	}
}

// settleTriggerFull applies the second-stage thresholds of the new program
// to the triggers as they are held: a second stage that is no longer wanted
// is released, and one that has become wanted is only remembered, not
// pressed, so the trigger has to fall below fullRelease before it presses
// again (ANA-14).
func (e *Engine) settleTriggerFull(now clock.Time) {
	for i, a := range [2]control.Axis{control.LeftTrigger, control.RightTrigger} {
		v := e.axes[a]
		full := e.triggerFull[i]
		if !full && v >= e.prog.triggerFullPressRaw[i] {
			full = true
		}
		if full && v <= e.prog.triggerFullReleaseRaw[i] {
			full = false
		}
		c := control.LTFull
		if i == 1 {
			c = control.RTFull
		}
		if !full && e.physical[c] {
			e.stepButton(now, c, false, false)
		}
		e.triggerFull[i] = full
	}
}
