package slot

// SetLayer runs on the slot owner, using the same layer semantics as a script.
// Existing press cycles keep their owners until release; no key is replayed.
func (s *Slot) SetLayer(op, name string) error { return s.eng.ChangeLayer(op, name) }
