package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// InputScope reports the declarative level that currently owns a control.
// It is read on the engine's goroutine before a button press is dispatched.
// A control that the set of the held function key binds is "global", the
// nearest scope of the script API: the handlers of base and of the layers
// are not for it (MAP-27).
func (e *Engine) InputScope(c control.Control) string {
	if set, _ := e.prog.functionSet(e.fnActive); set != nil && set.Binding(c) != nil {
		return "global"
	}
	level, binding, _, _ := e.lookup(c, GesturePress)
	if binding == nil || level == e.baseLayer {
		return "base"
	}
	for _, overlay := range e.overlays {
		if overlay.name == level {
			return "layer:" + level
		}
	}
	if e.prog.common != nil && level == e.prog.common.Name {
		return "common"
	}
	return "base"
}

// overlay is one active layer. Hold references and its toggle are independent.
// Stack order changes only when an inactive layer becomes active.
type overlay struct {
	name    string
	holds   int
	toggled bool
}

func (e *Engine) overlayIndex(name string) int {
	for i := range e.overlays {
		if e.overlays[i].name == name {
			return i
		}
	}
	return -1
}

func (e *Engine) applyLayerAction(now clock.Time, a *profile.Action, st *ctlState) {
	changed := false
	switch a.Op {
	case "hold":
		i := e.overlayIndex(a.Layer)
		if i < 0 {
			e.overlays = append(e.overlays, overlay{name: a.Layer})
			i = len(e.overlays) - 1
			changed = true
		}
		e.overlays[i].holds++
		st.layerHolds[st.layerHoldsN] = a.Layer
		st.layerHoldsN++
	case "toggle":
		i := e.overlayIndex(a.Layer)
		if i < 0 {
			e.overlays = append(e.overlays, overlay{name: a.Layer, toggled: true})
			changed = true
		} else {
			e.overlays[i].toggled = !e.overlays[i].toggled
			if e.overlays[i].holds == 0 && !e.overlays[i].toggled {
				e.removeOverlayAt(i)
				changed = true
			}
		}
	case "switch":
		changed = e.baseLayer != a.Layer
		e.baseLayer = a.Layer
	case "cycle":
		next := a.Layers[0]
		for i, name := range a.Layers {
			if name == e.baseLayer {
				next = a.Layers[(i+1)%len(a.Layers)]
				break
			}
		}
		changed = e.baseLayer != next
		e.baseLayer = next
	}
	if changed {
		e.notifyLayer(now, true)
	} else if a.Op == "toggle" && e.layerChange != nil {
		// A momentary hold may hide the visible change; persistence still
		// needs the updated toggle, without another feedback pulse.
		e.layerChange(e.currentLayer())
	}
}

func (e *Engine) removeOverlayAt(i int) {
	copy(e.overlays[i:], e.overlays[i+1:])
	e.overlays[len(e.overlays)-1] = overlay{}
	e.overlays = e.overlays[:len(e.overlays)-1]
}

func (e *Engine) releaseLayers(now clock.Time, names []string) {
	changed := false
	for i := len(names) - 1; i >= 0; i-- {
		n := e.overlayIndex(names[i])
		if n < 0 {
			continue
		}
		e.overlays[n].holds--
		if e.overlays[n].holds == 0 && !e.overlays[n].toggled {
			e.removeOverlayAt(n)
			changed = true
		}
	}
	if changed {
		e.notifyLayer(now, true)
	}
}

func (e *Engine) notifyLayer(_ clock.Time, pulse bool) {
	// The key hints show the bindings of the layer stack that is current, so a
	// change of the stack while they are on makes a new list (hints.go).
	if e.hints.on {
		e.hints.list = e.hintList()
	}
	if e.layerChange == nil && e.feedback == nil {
		return
	}
	state := e.currentLayer()
	if e.layerChange != nil {
		e.layerChange(state)
	}
	e.emitFeedback(pulse)
}

// RefreshFeedback publishes the current layer and feedback state without
// changing the mapping. Slots call it after a device is assigned so a pad
// that joins while an overlay is active receives the current indication.
func (e *Engine) RefreshFeedback() { e.notifyLayer(0, false) }

func (e *Engine) emitFeedback(pulse bool) { e.publishFeedback(pulse, false) }

// publishFeedback gives the feedback hook the state of the layer and of the
// pause. pulse asks for the pulses; pauseChange says the state changed because
// the mapping paused or resumed, whose pulse and color the global pause switch
// switches off (UI-27, ADR-0067). That switch works through rumble and led:
// the pulse still needs the rumble switch, the pause color the led switch.
// The switches are read now, so a state published again after a change of
// config.json (RefreshFeedback) follows it.
func (e *Engine) publishFeedback(pulse, pauseChange bool) {
	if e.feedback == nil {
		return
	}
	cfg := profile.DefaultFeedback()
	if e.prog.prof != nil && e.prog.prof.Feedback != nil {
		cfg = *e.prog.prof.Feedback
	}
	sw := e.feedbackSwitches()
	state := e.currentLayer()
	name := state.Base
	if n := len(state.Overlays); n > 0 {
		name = state.Overlays[n-1]
	}
	pulses, base := e.prog.feedbackMode(name)
	// The pause is shown only with its switch on; with it off the layer's
	// pulses and color go on as if the mapping were not paused.
	pauseShown := e.paused && sw.Pause
	f := Feedback{
		Layer:    state,
		Pulse:    pulse && sw.Rumble && cfg.Strength > 0 && (sw.Pause || !pauseChange),
		Pulses:   pulses,
		PulseMs:  cfg.ShortMs,
		GapMs:    cfg.GapMs,
		Strength: uint16(cfg.Strength),
		Paused:   e.paused,
		Mode:     pulses,
		LED:      sw.LED,
	}
	if base {
		f.PulseMs = cfg.LongMs
	}
	if sw.LED {
		f.Color = state.Color
		if pauseShown {
			f.Color = cfg.PausedColor
		}
	}
	if pauseShown {
		f.Pulses = 1
		f.PulseMs = cfg.LongMs
	}
	e.feedback(f)
}

// feedbackMode gives a stable mode number. A declared cycle is authoritative;
// profiles without one use their sorted layer order. The named base layer is
// the long, single-pulse return state.
func (p *Program) feedbackMode(name string) (pulses int, base bool) {
	for _, info := range p.bindings {
		for _, g := range []profile.Gesture{
			profile.GesturePress, profile.GestureRelease, profile.GestureTap,
			profile.GestureDoubleTap, profile.GestureTripleTap, profile.GestureQuadTap,
			profile.GestureHold,
		} {
			a := info.Binding.Action(g)
			if a == nil || a.Type != profile.ActionLayer || a.Op != "cycle" {
				continue
			}
			for i, layer := range a.Layers {
				if layer == name {
					return i + 1, name == "base"
				}
			}
		}
	}
	if name == "base" {
		return 1, true
	}
	for i, layer := range p.prof.Layers {
		if layer.Name == name {
			return i + 1, false
		}
	}
	return 1, false
}

// LayerState returns an independent snapshot; callers cannot mutate the engine.
func (e *Engine) LayerState() LayerState { return e.currentLayer() }

func (e *Engine) currentLayer() LayerState {
	s := LayerState{Base: e.baseLayer, Overlays: make([]string, len(e.overlays))}
	for i, o := range e.overlays {
		s.Overlays[i] = o.name
	}
	for i := len(e.overlays) - 1; i >= 0; i-- {
		if l := e.prog.layer(e.overlays[i].name); l != nil && l.Color != "" {
			s.Color = l.Color
			break
		}
	}
	if s.Color == "" {
		if l := e.prog.layer(e.baseLayer); l != nil {
			s.Color = l.Color
		}
	}
	return s
}
