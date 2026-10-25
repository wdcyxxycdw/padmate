package engine

import (
	"fmt"
	"slices"
)

// LayerAccess is the pure, slot-owned seam used by the script layer API.
// Methods are synchronous and are called only on the engine's goroutine.
type LayerAccess interface {
	LayerState() LayerState
	HasLayer(string) bool
	LayerNames() []string
	ChangeLayer(op, name string) error
	HoldLayer(name string) (func(), error)
}

func (e *Engine) HasLayer(name string) bool { return e.prog.layer(name) != nil }

func (e *Engine) LayerNames() []string {
	names := make([]string, 0, len(e.prog.layers))
	for name := range e.prog.layers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (e *Engine) ChangeLayer(op, name string) error {
	if !e.HasLayer(name) {
		return fmt.Errorf("engine: unknown layer %q", name)
	}
	switch op {
	case "switch":
		e.baseLayer = name
	case "enable", "toggle":
		i := e.overlayIndex(name)
		if i < 0 {
			e.overlays = append(e.overlays, overlay{name: name, toggled: true})
		} else {
			if op == "toggle" {
				e.overlays[i].toggled = !e.overlays[i].toggled
			} else {
				e.overlays[i].toggled = true
			}
			if !e.overlays[i].toggled && e.overlays[i].holds == 0 {
				e.removeOverlayAt(i)
			}
		}
	case "disable":
		if i := e.overlayIndex(name); i >= 0 {
			e.overlays[i].toggled = false
			if e.overlays[i].holds == 0 {
				e.removeOverlayAt(i)
			}
		}
	default:
		return fmt.Errorf("engine: unknown layer operation %q", op)
	}
	e.notifyLayer(0, true)
	return nil
}

func (e *Engine) HoldLayer(name string) (func(), error) {
	if !e.HasLayer(name) {
		return nil, fmt.Errorf("engine: unknown layer %q", name)
	}
	i := e.overlayIndex(name)
	if i < 0 {
		e.overlays = append(e.overlays, overlay{name: name})
		i = len(e.overlays) - 1
	}
	e.overlays[i].holds++
	e.notifyLayer(0, true)
	released := false
	epoch := e.layerEpoch
	return func() {
		if !released && epoch == e.layerEpoch {
			released = true
			e.releaseLayers(0, []string{name})
		}
	}, nil
}
