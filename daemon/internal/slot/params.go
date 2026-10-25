package slot

// scriptParamsPreparer is optional for runtimes that support parameter-only
// saves. Both preparation and commit run on the same owning slot goroutine.
type scriptParamsPreparer interface {
	PrepareParams(map[string]any) (func(), error)
}

// PrepareScriptParams prepares an in-place script update without changing
// engine cycles, registrations, output ownership, or the profile generation.
func (s *Slot) PrepareScriptParams(raw map[string]any) (func(), error) {
	if runtime, ok := s.script.(scriptParamsPreparer); ok {
		return runtime.PrepareParams(raw)
	}
	return func() {}, nil
}
