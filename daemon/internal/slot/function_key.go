package slot

import (
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// The function key of a slot belongs to its controller (ADR-0012, ADR-0017):
// controllers differ, and the one that has no start has a paddle. The profile's
// Program is compiled once with a default function key, and the slot runs the
// variant of it that has the function key of the controller it has now
// (engine.Program.ForFunctionKey), which is a map lookup after the first slot
// asked for that key. A change of controller or setting switches the Program
// the engine runs, through the engine's Reload: it lets go of everything first.

// functionKeyOf asks Config.FunctionKey for the function key of the slot's
// controller, control.Invalid when the Config has no such function.
func (s *Slot) functionKeyOf() control.Control {
	if s.functionKey == nil {
		return control.Invalid
	}
	var info input.DeviceInfo
	if s.info != nil {
		info = *s.info
	}
	return s.functionKey(s.deviceKey, info)
}

// programFor returns the Program the engine runs for the profile's Program
// base: base itself when the Config has no function key for the controller,
// else its variant with that function key. It cannot fail for a profile
// that compiled; an error (reported once, as an unknown input is) leaves
// base.
func (s *Slot) programFor(base *engine.Program) *engine.Program {
	want := s.functionKeyOf()
	if want == control.Invalid {
		return base
	}
	prog, err := base.ForFunctionKey(want)
	if err != nil {
		if key := "function key " + want.String() + " " + s.profileKey; !s.reported[key] {
			s.reported[key] = true
			s.keep(fmt.Errorf("slot: the profile %q does not compile with %s as its function key: %w; the profile's own is used", s.profileKey, want, err))
		}
		return base
	}
	return prog
}

// RefreshFunctionKey asks Config.FunctionKey again for the function key of the
// slot's controller and applies it: after a committed config.json changed a
// controller's settings, the supervisor runs it on every slot as a job. A slot
// whose function key is the same does nothing at all: no key is let go and the
// held function key stays held. A slot that changes it lets go of everything
// first (switchProgram), so a key held under the old function key, the old
// function key and the keys of controls held meanwhile all end; the controls
// down stay down and do nothing until pressed again, and the new function key,
// if down, takes effect from its next press.
func (s *Slot) RefreshFunctionKey() { s.applyFunctionKey(s.clk.Now(), false) }

// applyFunctionKey switches to the Program of the controller's function key if
// that is not the one running. attach says that the controller just came or
// went: the layers are not saved before, since the engine's were just reset, and
// not restored after, which the caller does.
func (s *Slot) applyFunctionKey(now clock.Time, attach bool) {
	if s.functionKey == nil {
		return
	}
	if prog := s.programFor(s.base); prog != s.prog {
		s.switchProgram(now, prog, attach)
	}
}

// switchProgram makes the engine run prog, a variant of the profile's Program
// with another function key, without a new profile generation. It is a Reload
// of the slot without the script: the engine ends every press cycle, and the
// Arbiter releases the keys of every owner of the slot, of any generation, which
// is the ReleaseAll of invariant 1 whatever the function key was holding or
// the controls pressed under it. The controls down stay down, each doing
// nothing until it is pressed again, and the pause stays.
func (s *Slot) switchProgram(now clock.Time, prog *engine.Program, attach bool) {
	if !attach {
		s.saveLayers()
	}
	s.discardScriptInput()
	was := s.restoringLayers
	s.restoringLayers = true
	s.cancelScriptHeld()
	s.eng.Reload(now, prog)
	s.prog = prog
	s.syncCalibration(now)
	s.updatePad(now)
	s.connectGestures()
	s.keep(s.releaseOwn())
	s.restoringLayers = was
	if !attach {
		s.restoreLayers()
	}
	if s.onFunctionKey != nil {
		s.onFunctionKey(s.deviceKey, prog)
	}
}
