package slot

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

type gestureRuntime interface {
	GestureEntries() []engine.GestureEntry
	ObserveGesture(engine.GestureEvent) error
	DispatchGesture(uint64, engine.GestureEvent) (bool, error)
	EndGesture(engine.GestureEvent) error
	BindLayers(engine.LayerAccess)
}

type actionRuntime interface {
	InvokeAction(string, map[string]any, ScriptEvent) error
}

// macroRuntime runs macros (SCR-50, ADR-0055). RunMacro starts one run of
// the macro file, or remembers it until the runtime is ready; the runtime
// reports a missing or broken macro and ignores a macro that is running.
type macroRuntime interface {
	RunMacro(file string, e ScriptEvent) error
}

func (s *Slot) invokeAction(e engine.ScriptActionEvent) {
	canceled := s.scriptCanceled[e.Control]
	if e.Macro != "" {
		canceled = s.macroCanceled[e.Control]
	}
	if s.Paused() || s.scriptDisconnected || canceled {
		return
	}
	s.updatePad(e.Time)
	ev := ScriptEvent{Control: e.Control.String(), Gesture: e.Gesture.String(), Time: e.Time, Duration: e.Duration, Count: e.Count, Scope: e.Scope, Cycle: int64(e.Cycle), Held: e.Held}
	if e.Macro != "" {
		if runtime, ok := s.script.(macroRuntime); ok {
			s.keep(runtime.RunMacro(e.Macro, ev))
		}
		return
	}
	if runtime, ok := s.script.(actionRuntime); ok {
		s.keep(runtime.InvokeAction(e.Name, e.Args, ev))
	}
}

func (s *Slot) ObserveGesture(e engine.GestureEvent) {
	if s.Paused() || s.scriptDisconnected || s.scriptCanceled[e.Control] {
		return
	}
	s.updatePad(e.Time)
	if runtime, ok := s.script.(gestureRuntime); ok {
		s.keep(runtime.ObserveGesture(e))
	}
}

// connectGestures installs the engine hooks for the runtime: a legacy
// script (Configured) sees every button transition and its gesture
// registrations join the recognizer; a runtime only Needed for macros gets
// the bookkeeping observer and nothing else, so its profile's input goes
// through the engine as without a script.
func (s *Slot) connectGestures() {
	s.eng.SetButtonObserver(nil)
	if runtime, ok := s.script.(gestureRuntime); ok && s.scriptEnabled {
		runtime.BindLayers(s.eng)
		s.eng.SetGestures(s)
		s.eng.SetButton(s.gestureButton)
	} else {
		s.eng.SetGestures(nil)
		if s.scriptEnabled {
			s.eng.SetButton(s.dispatchButton)
		} else {
			s.eng.SetButton(nil)
			if s.scriptNeeded {
				s.eng.SetButtonObserver(s.macroButton)
			}
		}
	}
}

// macroButton keeps the bookkeeping of a runtime that only runs macros: a
// control pressed while the slot is paused or waits for its first Resync
// runs no macro in that press cycle, its release, tap or hold included; a
// later press starts a fresh cycle (macroCanceled, which the hooks of a
// legacy script keep too). It is the engine's ButtonObserver, so it
// consumes nothing, and it does not restart a released runtime: ReleaseAll
// does that at once (restartMacros).
func (s *Slot) macroButton(e engine.ButtonEvent) {
	if e.Down {
		s.macroCanceled[e.Control] = s.Paused() || s.scriptDisconnected
	}
}

func (s *Slot) GestureEntries() []engine.GestureEntry {
	if s.Paused() || s.scriptDisconnected {
		return nil
	}
	runtime, ok := s.script.(gestureRuntime)
	if !ok {
		return nil
	}
	return runtime.GestureEntries()
}

func (s *Slot) DispatchGesture(id uint64, e engine.GestureEvent) bool {
	if s.Paused() || s.scriptDisconnected || s.scriptCanceled[e.Control] {
		return false
	}
	s.updatePad(e.Time)
	runtime, ok := s.script.(gestureRuntime)
	if !ok {
		return false
	}
	consumed, err := runtime.DispatchGesture(id, e)
	s.keep(err)
	return consumed
}

func (s *Slot) EndGesture(e engine.GestureEvent) {
	if runtime, ok := s.script.(gestureRuntime); ok {
		s.keep(runtime.EndGesture(e))
	}
}

func (s *Slot) gestureButton(e engine.ButtonEvent) bool {
	if e.Down {
		s.scriptStarted[e.Control] = e.Time
	} else {
		s.scriptStarted[e.Control] = 0
	}
	if e.Down {
		s.macroCanceled[e.Control] = s.Paused() || s.scriptDisconnected
	}
	if e.Down && (s.Paused() || s.scriptDisconnected) {
		s.scriptCanceled[e.Control] = true
	}
	canceled := s.scriptCanceled[e.Control]
	if !e.Down {
		s.scriptCanceled[e.Control] = false
	}
	if e.Down && !canceled && !s.Paused() && !s.scriptDisconnected && s.scriptReleased {
		if p, ok := s.script.(scriptPauser); ok {
			s.keep(p.SetPaused(false))
		}
		s.scriptReleased = false
	}
	s.updatePad(e.Time)
	return false
}

// cancelScriptHeld follows recognized virtual controls, not raw d-pad bits.
// Clearing false entries also removes stale cancellation after script removal.
func (s *Slot) cancelScriptHeld() {
	for c := range s.scriptCanceled {
		down := s.eng.ControlDown(control.Control(c))
		s.scriptCanceled[c] = down
		s.macroCanceled[c] = down
	}
}
