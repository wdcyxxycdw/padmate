package slot

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

type ScriptView struct {
	Index   uint8             `json:"slot"`
	Device  input.DeviceID    `json:"device"`
	Key     string            `json:"key"`
	Profile string            `json:"profileId"`
	Gen     uint32            `json:"generation"`
	Script  script.Inspection `json:"script"`
}

// InspectScript runs only on the slot owner. Metadata copies are optional so
// periodic state observers do not copy every action/registration.
func (s *Slot) InspectScript(metadata bool) ScriptView {
	v := ScriptView{Index: s.index, Device: s.device, Key: s.deviceKey, Profile: s.profileKey, Gen: s.gen,
		Script: script.Inspection{State: "Absent", Metadata: script.EmptyMetadata()}}
	if inspector, ok := s.script.(interface{ InspectScript(bool) script.Inspection }); ok {
		v.Script = inspector.InspectScript(metadata)
	}
	return v
}

// ReloadScript never starts a worker for an unassigned/disconnected slot.
// Paused runtimes retain their own gate. The asynchronous swap releases old
// script owners without canceling unrelated declarative presses.
func (s *Slot) ReloadScript() error {
	if s.script == nil || !s.scriptNeeded || s.scriptDisconnected {
		return nil
	}
	s.discardScriptInput()
	if err := s.script.Reload(s.gen); err != nil {
		return err
	}
	if s.scriptReleased && !s.Paused() {
		s.scriptReleased = false
		if p, ok := s.script.(scriptPauser); ok {
			return p.SetPaused(false)
		}
	}
	return nil
}
