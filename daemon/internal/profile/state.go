package profile

import (
	"maps"
	"slices"
)

func (s LayerMemory) Clone() LayerMemory {
	s.Toggled = slices.Clone(s.Toggled)
	s.Cycles = maps.Clone(s.Cycles)
	return s
}

func (s State) Clone() State {
	out := s
	out.JoyCon2 = slices.Clone(s.JoyCon2)
	if s.Devices != nil {
		out.Devices = make(map[string]DeviceMemory, len(s.Devices))
	}
	for key, dev := range s.Devices {
		dst := DeviceMemory{Profiles: make(map[string]LayerMemory, len(dev.Profiles))}
		for id, layers := range dev.Profiles {
			dst.Profiles[id] = layers.Clone()
		}
		out.Devices[key] = dst
	}
	return out
}
