package engine

import (
	"errors"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// PersistentLayers copies only layer choices that can survive a profile switch
// or process restart. Cycle positions follow the base layer, as cycle actions do.
func (e *Engine) PersistentLayers() profile.LayerMemory {
	s := profile.LayerMemory{Base: e.baseLayer}
	for _, layer := range e.overlays {
		if layer.toggled {
			s.Toggled = append(s.Toggled, layer.name)
		}
	}
	for _, info := range e.prog.bindings {
		a := info.Binding.Action(info.Gesture)
		if a == nil || a.Type != profile.ActionLayer || a.Op != "cycle" {
			continue
		}
		if i := slices.Index(a.Layers, e.baseLayer); i >= 0 {
			if s.Cycles == nil {
				s.Cycles = make(map[string]int)
			}
			s.Cycles[a.ID] = i
		}
	}
	return s
}

// RestoreLayers restores choices at a canceled/reset boundary. It never emits
// key commands. The caller must first release the previous Arbiter owners.
// Removed layer names are discarded and a missing base uses the new default.
func (e *Engine) RestoreLayers(s profile.LayerMemory) error {
	for _, st := range e.ctl {
		if st.down && !st.canceled || st.pending {
			return errors.New("engine: restore layers requires canceled controls")
		}
	}
	for _, latch := range e.latch {
		if latch.holdsN != 0 || latch.layerHoldsN != 0 {
			return errors.New("engine: restore layers requires released latches")
		}
	}
	for _, layer := range e.overlays {
		if layer.holds != 0 {
			return errors.New("engine: restore layers requires released layer holds")
		}
	}
	e.baseLayer = e.prog.defaultLayer
	if e.HasLayer(s.Base) {
		e.baseLayer = s.Base
	}
	e.layerEpoch++
	e.overlays = e.overlays[:0]
	for _, name := range s.Toggled {
		if e.HasLayer(name) && e.overlayIndex(name) < 0 {
			e.overlays = append(e.overlays, overlay{name: name, toggled: true})
		}
	}
	e.notifyLayer(0, false)
	return nil
}
