package slot

import (
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// Cold script startup must finish before the engine freezes the reachable
// gesture registrations. Ordinary active runtimes bypass this gate entirely.
type scriptInputStatus interface{ InputStatus() (pending, ready bool) }
type scriptInput struct {
	event input.Event
	time  clock.Time
}

const maxScriptInput = 128

func (s *Slot) queueScriptInput(ev input.Event, now clock.Time) bool {
	if ev.Kind != input.ButtonDown && ev.Kind != input.ButtonUp && ev.Kind != input.AxisMotion {
		return false
	}
	if !s.scriptEnabled || s.Paused() || s.scriptDisconnected {
		return false
	}
	restart := s.scriptReleased && s.freshScriptInput(ev)
	status, ok := s.script.(scriptInputStatus)
	if !ok {
		return false
	}
	pending, ready := status.InputStatus()
	if !restart && !pending && !ready && !s.scriptReleased && len(s.scriptInputQueue) == 0 {
		// A worker can fail while the user is idle. Never let the first
		// subsequent edge fall through with an empty registration table.
		paused := s.Paused()
		s.eng.Pause(now)
		s.handleInput(ev, now)
		if !paused {
			s.eng.Resume()
		}
		s.keep(s.ReleaseAll())
		return true
	}
	if !restart && !pending && len(s.scriptInputQueue) == 0 {
		return false
	}
	if len(s.scriptInputQueue) == 0 {
		s.scriptInputGen = s.gen
		// The pad snapshot still contains the last processed input; state
		// already contains the incoming physical edge for safety inspection.
		s.scriptInputBase = s.state
		s.scriptInputBasePad = s.pad
		s.scriptInputPad = s.pad
		if ev.Kind == input.AxisMotion {
			if a, ok := control.ParseAxis(ev.Control); ok {
				s.scriptInputBase.Axes[a] = s.pad.Axes[a]
			}
		} else if c, ok := parseButton(ev.Control); ok {
			s.scriptInputBase.Buttons[c] = s.pad.Down[c]
		}
		s.scriptInputPreview = s.eng.PreviewInput(func(e engine.ButtonEvent) {
			if e.Control.IsTrigger() || e.Control > control.Touchpad {
				s.observeScriptInput(e.Control, e.Down, e.Time)
			}
		})
	}
	if len(s.scriptInputQueue) == maxScriptInput {
		s.discardScriptInput()
		// Consume the overflowing edge while paused too, so its held state
		// is canceled and cannot reappear after the next worker completes.
		paused := s.Paused()
		s.eng.Pause(now)
		s.handleInput(ev, now)
		if !paused {
			s.eng.Resume()
		}
		s.keep(errors.New("slot: script startup input queue overflow; mapping released"))
		s.keep(s.ReleaseAll())
		return true
	}
	s.scriptInputQueue = append(s.scriptInputQueue, scriptInput{ev, now})
	if c, ok := parseButton(ev.Control); ok && ev.Kind != input.AxisMotion {
		s.observeScriptInput(c, ev.Kind == input.ButtonDown, now)
	}
	if in, ok := scriptEngineInput(ev, now); ok {
		s.scriptInputPreview.Step(in)
	}
	// Publish the physical snapshot before a fresh worker copies its setup
	// inputs. Binding recognition still waits for its registrations.
	s.updatePad(now)
	if restart {
		if p, ok := s.script.(scriptPauser); ok {
			s.keep(p.SetPaused(false))
		}
		s.scriptReleased = false
		pending, ready = status.InputStatus()
	}
	if !pending && ready {
		s.flushScriptInput()
	}
	return true
}

func (s *Slot) observeScriptInput(c control.Control, down bool, at clock.Time) {
	if down && !s.scriptInputPad.Down[c] {
		s.scriptInputPad.Since[c] = at
	}
	if !down {
		s.scriptInputPad.Since[c] = 0
	}
	s.scriptInputPad.Down[c] = down
}

func scriptEngineInput(ev input.Event, now clock.Time) (engine.Input, bool) {
	if ev.Kind == input.AxisMotion {
		a, ok := control.ParseAxis(ev.Control)
		return engine.Input{T: now, Kind: engine.InputAxis, Axis: a, Value: ev.Value}, ok
	}
	c, ok := parseButton(ev.Control)
	return engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: ev.Kind == input.ButtonDown}, ok
}

func (s *Slot) freshScriptInput(ev input.Event) bool {
	if ev.Kind == input.ButtonDown {
		c, ok := parseButton(ev.Control)
		return ok && !s.scriptCanceled[c] && !s.eng.ControlDown(c)
	}
	if ev.Kind != input.AxisMotion {
		return false
	}
	a, ok := control.ParseAxis(ev.Control)
	if !ok {
		return false
	}
	threshold := s.analog.LS.Deadzone
	switch a {
	case control.RightX, control.RightY:
		threshold = s.analog.RS.Deadzone
	case control.LeftTrigger:
		threshold = s.analog.LT.Release
	case control.RightTrigger:
		threshold = s.analog.RT.Release
	}
	neutral := func(v int16) bool {
		n := int(v)
		if n < 0 {
			n = -n
		}
		return float64(n) <= threshold*32767
	}
	return neutral(s.pad.Axes[a]) && !neutral(ev.Value)
}

func (s *Slot) flushScriptInput() {
	if len(s.scriptInputQueue) == 0 {
		return
	}
	status, ok := s.script.(scriptInputStatus)
	if !ok {
		s.discardScriptInput()
		return
	}
	pending, ready := status.InputStatus()
	if pending {
		return
	}
	if !ready || s.Paused() || s.scriptDisconnected || s.scriptInputGen != s.gen {
		s.keep(s.ReleaseAll())
		return
	}
	queued := s.scriptInputQueue
	s.scriptInputQueue = nil
	s.scriptInputPreview = nil
	actual := s.state
	s.state = s.scriptInputBase
	s.pad = s.scriptInputBasePad
	// Activation saw the latest physical input. Rewind only for dispatch so
	// the host's existing old-held guard can distinguish queued fresh cycles
	// from controls already held before startup. Their canceled state stays
	// in the main engine and scriptCanceled throughout.
	s.updatePad(queued[0].time)
	for i, item := range queued {
		pending, ready = status.InputStatus()
		if pending || !ready || s.Paused() || s.scriptDisconnected || s.scriptInputGen != s.gen {
			s.state = actual
			s.scriptInputQueue = queued[i:]
			s.keep(s.ReleaseAll())
			return
		}
		s.advanceScriptInput(item.time)
		s.state.Apply(item.event)
		s.handleInput(item.event, item.time)
	}
	s.advanceScriptInput(s.clk.Now())
	s.state = actual
	s.updatePad(s.clk.Now())
}

func (s *Slot) advanceScriptInput(now clock.Time) {
	for {
		d, ok := s.eng.InputDeadline()
		if !ok || d > now {
			break
		}
		s.eng.Apply(d)
	}
	s.eng.Apply(now)
}

// Discard remembers all observed physical levels without dispatching actions.
// Safety boundaries therefore cancel held controls even if startup never ran.
func (s *Slot) discardScriptInput() {
	if len(s.scriptInputQueue) == 0 {
		return
	}
	queued := s.scriptInputQueue
	s.scriptInputQueue = nil
	s.scriptInputPreview = nil
	paused := s.Paused()
	s.eng.Pause(s.clk.Now())
	for _, item := range queued {
		s.handleInput(item.event, s.clk.Now())
	}
	if !paused {
		s.eng.Resume()
	}
	s.cancelScriptHeld()
	s.updatePad(s.clk.Now())
}
