package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// stickCal is the deadzone and saturation one stick recognizes with when the
// controller's calibration changes the profile's (IN-14).
type stickCal struct {
	on                   bool
	deadzone, saturation float64
}

// SetCalibration sets the stick calibration of the controller this engine
// belongs to (IN-14, ARCHITECTURE §7.5): the deadzone and saturation cal sets
// replace the program's for that stick, one it leaves out keeps the program's,
// and nil, or a calibration that sets nothing, goes back to the program's. The
// Program stays shared and unchanged; the calibration is the engine's, like
// its state, and Reload and Reset keep it.
//
// A stick whose calibrated pair does not hold (saturation must be greater
// than deadzone, with the program's value for a member cal leaves out) keeps
// the program's pair whole, and ignored says so (index 0 is the left stick):
// the same calibration can meet any profile, so mapping goes on with the
// profile's values and the caller tells the user. CalibrationIgnored returns
// the same after any later Reload.
//
// When the recognized values change, a stick that is held is settled as at a
// Reload (settleVirtual) at now, with no CancelAll: the directions, rings and
// ls.active it no longer wants are released through the normal path, those it
// newly wants are only remembered and press once the stick has left and come
// back, so a change presses nothing; the keys of buttons held stay down. The
// pointer of a stick in mode "mouse" that is pushed does not stop: it goes on
// with the new deadzone and saturation. A call that changes nothing the engine
// recognizes with does nothing at all.
func (e *Engine) SetCalibration(now clock.Time, cal *profile.Calibration) (ignored [2]bool) {
	e.calSrc = cloneCalibration(cal)
	if e.resolveCalibration() {
		e.settleVirtual(now, false, false)
		for i := range e.sticks {
			// A knob that is not engaged any more takes its reference anew.
			if s := &e.sticks[i]; s.knobOn && !s.active {
				s.knobOn = false
			}
		}
	}
	return e.calIgnored
}

// CalibrationIgnored reports the sticks whose calibration SetCalibration (or
// the Program of the last Reload) made ignored, left stick first.
func (e *Engine) CalibrationIgnored() [2]bool { return e.calIgnored }

// Analog returns the analog settings the engine recognizes with: the
// Program's with the calibration of the controller laid over the deadzone and
// saturation of each stick. Without a calibration it is Program.Analog().
func (e *Engine) Analog() profile.Analog {
	a := e.prog.analog
	if c := e.cal[0]; c.on {
		a.LS.Deadzone, a.LS.Saturation = c.deadzone, c.saturation
	}
	if c := e.cal[1]; c.on {
		a.RS.Deadzone, a.RS.Saturation = c.deadzone, c.saturation
	}
	return a
}

// resolveCalibration works out what calSrc gives with the engine's program
// and reports whether the values the engine recognizes with changed.
func (e *Engine) resolveCalibration() (changed bool) {
	eff, ignored := e.prog.analog.Calibrated(e.calSrc)
	var next [2]stickCal
	for i, s := range [2]profile.Stick{eff.LS, eff.RS} {
		base := e.prog.analog.LS
		if i == 1 {
			base = e.prog.analog.RS
		}
		if s.Deadzone != base.Deadzone || s.Saturation != base.Saturation {
			next[i] = stickCal{on: true, deadzone: s.Deadzone, saturation: s.Saturation}
		}
	}
	changed = next != e.cal
	e.cal, e.calIgnored = next, ignored
	return changed
}

// cloneCalibration returns a deep copy of cal for the engine to keep, or nil
// when cal sets nothing: the caller's pointers are into its config, which it
// may replace.
func cloneCalibration(cal *profile.Calibration) *profile.Calibration {
	if cal.IsEmpty() {
		return nil
	}
	cp := func(s *profile.StickCalibration) *profile.StickCalibration {
		if s == nil {
			return nil
		}
		out := &profile.StickCalibration{}
		if s.Deadzone != nil {
			v := *s.Deadzone
			out.Deadzone = &v
		}
		if s.Saturation != nil {
			v := *s.Saturation
			out.Saturation = &v
		}
		return out
	}
	return &profile.Calibration{LS: cp(cal.LS), RS: cp(cal.RS)}
}
