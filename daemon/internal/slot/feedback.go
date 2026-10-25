package slot

// RefreshFeedback has the engine publish the layer and feedback state again,
// with the global feedback switches as they are now (UI-27, ADR-0067): after a
// committed config.json changed them, the supervisor runs it on every slot as a
// job. It sends no key and starts no pulse (a refresh never pulses); a slot
// without a controller publishes for device 0, which padmated drops.
func (s *Slot) RefreshFeedback() { s.eng.RefreshFeedback() }
