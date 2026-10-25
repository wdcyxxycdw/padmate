package main

import "github.com/wdcyxxycdw/padmate/daemon/internal/winhook"

// hookSource is the running hook as an eventSource: the keyboard hook, and
// the mouse channel of the mouse hook on its thread when there is one.
type hookSource struct {
	*winhook.Hook
	mouse <-chan winhook.MouseEvent // nil without a mouse hook
}

func (s hookSource) MouseEvents() <-chan winhook.MouseEvent { return s.mouse }

// Dropped counts the events dropped by both hooks.
func (s hookSource) Dropped() uint64 { return s.Hook.Dropped() + s.MouseDropped() }

// startHook installs the low-level keyboard hook, and with mouse not nil the
// low-level mouse hook beside it.
func startHook(swallow func(winhook.Event) bool, mouse *winhook.MouseOptions) (eventSource, error) {
	h, err := winhook.Start(swallow)
	if err != nil {
		return nil, err
	}
	if mouse == nil {
		return hookSource{Hook: h}, nil
	}
	events, err := h.EnableMouse(*mouse)
	if err != nil {
		// The keyboard hook swallows nothing once it is closed.
		_ = h.Close()
		return nil, err
	}
	return hookSource{Hook: h, mouse: events}, nil
}
