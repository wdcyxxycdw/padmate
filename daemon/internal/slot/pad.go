package slot

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// PadSnapshot is copied on the slot goroutine before it is passed to a
// script runtime. Candidates built on workers never read Slot directly.
type PadSnapshot struct {
	Connected  bool
	Info       input.DeviceInfo
	Key        string
	Down       [control.Count]bool
	Since      [control.Count]clock.Time
	Axes       [control.AxisCount]int16
	HasControl [control.Count]bool
	HasAxis    [control.AxisCount]bool
	Analog     profile.Analog
	// Power is the battery state the gamepad last reported (IN-12), HasPower
	// false until it has reported one: a script reads it as pad.info.battery.
	Power    input.Power
	HasPower bool
}

type padUpdater interface{ UpdatePad(PadSnapshot) }

func (s *Slot) updatePad(now clock.Time) {
	if s.scriptInputPreview != nil {
		s.scriptInputPreview.Apply(now)
	}
	var next PadSnapshot
	next.Analog = s.analog
	next.Axes = s.state.Axes
	for _, c := range control.All() {
		if c.IsTrigger() || int(c) > int(control.Touchpad) {
			next.Down[c] = s.eng.ControlDown(c)
			if s.scriptInputPreview != nil {
				next.Down[c] = s.scriptInputPreview.ControlDown(c)
			}
		} else {
			next.Down[c] = s.state.Buttons[c]
		}
		if next.Down[c] {
			switch {
			case s.scriptInputPreview != nil:
				next.Since[c] = s.scriptInputPad.Since[c]
			case s.pad.Down[c]:
				next.Since[c] = s.pad.Since[c]
			default:
				next.Since[c] = now
			}
		}
	}
	if s.info != nil {
		next.Connected = true
		next.Info = *s.info
		next.Key = s.deviceKey
		if s.power != nil {
			next.Power, next.HasPower = s.power(s.device)
		}
		known := s.info.ControlsKnown()
		for a := control.Axis(1); int(a) < control.AxisCount; a++ {
			if !known {
				next.HasAxis[a] = s.info.IsGamepad
			} else {
				next.HasAxis[a] = s.info.AxisMask&(1<<(a-1)) != 0
			}
		}
		for b := uint8(0); b < 32; b++ {
			c, ok := control.Parse(input.ButtonName(b))
			if ok && c.IsValid() {
				if !known {
					next.HasControl[c] = s.info.IsGamepad && c <= control.DpadRight
				} else {
					next.HasControl[c] = s.info.ButtonMask&(1<<b) != 0
				}
			}
		}
		next.HasControl[control.LT] = next.HasAxis[control.LeftTrigger]
		next.HasControl[control.RT] = next.HasAxis[control.RightTrigger]
		for _, c := range []control.Control{control.LSUp, control.LSDown} {
			next.HasControl[c] = next.HasAxis[control.LeftY]
		}
		for _, c := range []control.Control{control.LSLeft, control.LSRight} {
			next.HasControl[c] = next.HasAxis[control.LeftX]
		}
		for c := control.LSUpLeft; c <= control.LSDownRight; c++ {
			next.HasControl[c] = next.HasAxis[control.LeftX] && next.HasAxis[control.LeftY]
		}
		next.HasControl[control.LSActive] = next.HasAxis[control.LeftX] || next.HasAxis[control.LeftY]
		// The rings and the knob ticks read the depth and the angle: both axes.
		for _, c := range []control.Control{control.LSInner, control.LSOuter, control.LSCW, control.LSCCW} {
			next.HasControl[c] = next.HasAxis[control.LeftX] && next.HasAxis[control.LeftY]
		}
		for _, c := range []control.Control{control.RSUp, control.RSDown} {
			next.HasControl[c] = next.HasAxis[control.RightY]
		}
		for _, c := range []control.Control{control.RSLeft, control.RSRight} {
			next.HasControl[c] = next.HasAxis[control.RightX]
		}
		for c := control.RSUpLeft; c <= control.RSDownRight; c++ {
			next.HasControl[c] = next.HasAxis[control.RightX] && next.HasAxis[control.RightY]
		}
		next.HasControl[control.RSActive] = next.HasAxis[control.RightX] || next.HasAxis[control.RightY]
		for _, c := range []control.Control{control.RSInner, control.RSOuter, control.RSCW, control.RSCCW} {
			next.HasControl[c] = next.HasAxis[control.RightX] && next.HasAxis[control.RightY]
		}
		// The second stage of a trigger needs the trigger axis.
		next.HasControl[control.LTFull] = next.HasAxis[control.LeftTrigger]
		next.HasControl[control.RTFull] = next.HasAxis[control.RightTrigger]
		next.HasControl[control.DpadUpLeft] = next.HasControl[control.DpadUp] && next.HasControl[control.DpadLeft]
		next.HasControl[control.DpadUpRight] = next.HasControl[control.DpadUp] && next.HasControl[control.DpadRight]
		next.HasControl[control.DpadDownLeft] = next.HasControl[control.DpadDown] && next.HasControl[control.DpadLeft]
		next.HasControl[control.DpadDownRight] = next.HasControl[control.DpadDown] && next.HasControl[control.DpadRight]
	}
	s.pad = next
	if u, ok := s.script.(padUpdater); ok {
		u.UpdatePad(next)
	}
}
