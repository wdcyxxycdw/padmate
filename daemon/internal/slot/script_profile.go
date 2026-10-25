package slot

// SetProfileKey stages the identity for the next Reload. The old profile's
// layer memory is saved before the identity and script session are switched.
func (s *Slot) SetProfileKey(key string) {
	s.nextProfileKey = &key
}
