package slot

import "github.com/wdcyxxycdw/padmate/daemon/internal/profile"

// LayerMemory stores copies in memory; implementations must not do disk I/O
// on the slot's owner loop. The daemon flushes snapshots on its own worker.
type LayerMemory interface {
	Load(device, id string) (profile.LayerMemory, bool)
	Save(device, id string, state profile.LayerMemory)
}

func (s *Slot) saveLayers() {
	if s.layerMemory != nil && s.eng != nil && s.device != 0 && !s.restoringLayers {
		s.layerMemory.Save(s.deviceKey, s.profileKey, s.eng.PersistentLayers())
	}
}

func (s *Slot) restoreLayers() {
	if s.layerMemory == nil || s.device == 0 {
		return
	}
	if memory, ok := s.layerMemory.Load(s.deviceKey, s.profileKey); ok {
		was := s.restoringLayers
		s.restoringLayers = true
		s.keep(s.eng.RestoreLayers(memory))
		s.restoringLayers = was
	}
}

func (s *Slot) preserveLayers() {
	s.saveLayers()
	s.restoringLayers = true
}
