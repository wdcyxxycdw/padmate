package engine

import "github.com/wdcyxxycdw/padmate/daemon/internal/control"

// View is a detached status snapshot, read only on the engine's owner loop.
type View struct {
	Profile string
	Layers  LayerState
	Latches []string
	Mode    int
	// Hints is the key hints (HUD-03) while a hints action is held: the entry
	// of each control with something to show in the current layer stack, in
	// control order. It is nil when the hints are off and not nil, possibly
	// empty, when they are on.
	Hints []Hint
	// FunctionKey is the function key (control.Back, control.Start or
	// control.Guide) while it is held and a press looks its control up in the
	// function set first (ActiveFunctionKey), control.Invalid when it is not
	// held (profiles.explain, ADR-0071).
	FunctionKey control.Control
}

func (e *Engine) View() View {
	v := View{Profile: e.prog.prof.Name, Layers: e.LayerState(), FunctionKey: e.fnActive}
	name := v.Layers.Base
	if n := len(v.Layers.Overlays); n > 0 {
		name = v.Layers.Overlays[n-1]
	}
	v.Mode, _ = e.prog.feedbackMode(name)
	for c, l := range e.latch {
		if l.holdsN > 0 || l.layerHoldsN > 0 {
			v.Latches = append(v.Latches, control.Control(c).String())
		}
	}
	if e.hints.on {
		v.Hints = make([]Hint, len(e.hints.list))
		copy(v.Hints, e.hints.list)
	}
	return v
}
