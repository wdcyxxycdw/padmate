package supervisor

import (
	"math"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// suspendControls survives artificial releases and empty SDL snapshots.
// Controls observed active across a system suspension must establish a
// release after resume. User pause alone does not create this quarantine.
type suspendControls struct {
	buttons    [control.Count]bool
	needDown   [control.Count]bool
	axes       [control.AxisCount]bool
	needActive [control.AxisCount]bool
	raw        slot.State
}

// An input select may beat the suspension wake channel. Capture before
// processing that input so a synthetic up cannot erase the old held state.
func (r *router) recoverySuspended() bool {
	v := r.s.susp.Load()
	cnt := v >> 8
	r.captureRecovery(cnt)
	return r.suspended != 0 || v&0xff != 0 || cnt != r.suspCount
}

func (r *router) captureRecovery(cnt uint64) {
	if cnt == r.recoveryCount {
		return
	}
	r.recoveryCount = cnt
	for _, d := range r.devices {
		d.recovery.capture(d.state, r.program)
	}
}

func analogSettings(p *engine.Program) profile.Analog {
	if p == nil {
		return profile.DefaultAnalog()
	}
	return p.Analog()
}

func axisNeutral(a control.Axis, raw slot.State, p *engine.Program) bool {
	settings := analogSettings(p)
	switch a {
	case control.LeftX, control.LeftY:
		return math.Hypot(float64(raw.Axes[control.LeftX])/32767, float64(raw.Axes[control.LeftY])/32767) <= settings.LS.Deadzone
	case control.RightX, control.RightY:
		return math.Hypot(float64(raw.Axes[control.RightX])/32767, float64(raw.Axes[control.RightY])/32767) <= settings.RS.Deadzone
	case control.LeftTrigger:
		return float64(raw.Axes[a])/32767 <= settings.LT.Release
	case control.RightTrigger:
		return float64(raw.Axes[a])/32767 <= settings.RT.Release
	}
	return true
}

func (q *suspendControls) capture(st slot.State, p *engine.Program) {
	q.raw = st
	for c, down := range st.Buttons {
		if down {
			q.buttons[c] = true
		}
	}
	for _, a := range control.AllAxes() {
		if !axisNeutral(a, st, p) {
			q.axes[a] = true
		}
	}
}

func (q *suspendControls) filter(e input.Event, suspended bool, p *engine.Program) bool {
	q.raw.Apply(e)
	switch e.Kind {
	case input.ButtonDown, input.ButtonUp:
		c, ok := control.Parse(e.Control)
		if !ok || c.IsTrigger() {
			return false
		}
		if suspended && e.Kind == input.ButtonDown {
			q.buttons[c] = true
		}
		if !q.buttons[c] {
			return false
		}
		switch {
		case suspended:
			// Neither SDL's release nor its next empty scan is proof of
			// owner release while the input desktop is unavailable.
			if e.Kind == input.ButtonUp {
				q.needDown[c] = true
			} else {
				q.needDown[c] = false
			}
			return false // paused slots still observe input and cancel cycles
		case e.Kind == input.ButtonDown:
			q.needDown[c] = false
		case !q.needDown[c]:
			q.buttons[c] = false
			return false // release the slot's canceled pre-suspend cycle too
		}
		return true
	case input.AxisMotion:
		a, ok := control.ParseAxis(e.Control)
		if !ok {
			return false
		}
		if !suspended && !q.axes[a] {
			return false
		}
		neutral := axisNeutral(a, q.raw, p)
		if suspended && !neutral {
			q.axisGroup(a, func(axis control.Axis) { q.axes[axis] = true })
		}
		if !q.axes[a] {
			return false
		}
		switch {
		case suspended:
			if neutral {
				q.axisGroup(a, func(axis control.Axis) { q.needActive[axis] = true })
			} else {
				q.axisGroup(a, func(axis control.Axis) { q.needActive[axis] = false })
			}
			return false
		case !neutral:
			q.axisGroup(a, func(axis control.Axis) { q.needActive[axis] = false })
		case !q.needActive[a]:
			q.axisGroup(a, func(axis control.Axis) { q.axes[axis] = false })
			return false
		}
		return true
	}
	return false
}

func (q *suspendControls) axisGroup(a control.Axis, apply func(control.Axis)) {
	switch a {
	case control.LeftX, control.LeftY:
		apply(control.LeftX)
		apply(control.LeftY)
	case control.RightX, control.RightY:
		apply(control.RightX)
		apply(control.RightY)
	default:
		apply(a)
	}
}

func (q *suspendControls) mask(st *slot.State) bool {
	changed := false
	for c, blocked := range q.buttons {
		if blocked {
			changed = changed || st.Buttons[c]
			st.Buttons[c] = false
		}
	}
	return q.maskAxes(st) || changed
}

func (q *suspendControls) maskAxes(st *slot.State) bool {
	changed := false
	for a, blocked := range q.axes {
		if blocked {
			changed = changed || st.Axes[a] != 0
			st.Axes[a] = 0
		}
	}
	return changed
}
