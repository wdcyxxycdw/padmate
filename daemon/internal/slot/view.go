package slot

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

// View belongs to its caller and contains no mutable engine or device pointers.
type View struct {
	Index      uint8
	Device     input.DeviceID
	Mapping    engine.View
	Gen        uint32
	Key        string
	Info       input.DeviceInfo
	HasControl [control.Count]bool
	HasAxis    [control.AxisCount]bool
	// Panels are the hud panels of the slot's script runtime (SCR-22), nil
	// when it has none.
	Panels []script.HUDPanel
}

// scriptHUD is a script runtime whose scripts can keep panels on the overlay.
type scriptHUD interface{ HUDPanels() []script.HUDPanel }

func (s *Slot) View() View {
	v := View{Index: s.index, Device: s.device, Mapping: s.eng.View(), Gen: s.gen, Key: s.deviceKey, Info: s.pad.Info, HasControl: s.pad.HasControl, HasAxis: s.pad.HasAxis}
	if hud, ok := s.script.(scriptHUD); ok {
		v.Panels = hud.HUDPanels()
	}
	return v
}
