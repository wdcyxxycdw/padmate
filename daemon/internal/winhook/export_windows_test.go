package winhook

import "errors"

// SetPostQuit makes Close post WM_QUIT with f until restore is called.
func SetPostQuit(f func(tid uint32) error) (restore func()) {
	prev := postQuit
	postQuit = f
	return func() { postQuit = prev }
}

// Handle runs the hook procedure's handling of e on h, as if the system
// had reported e, and reports whether h swallowed it. h must still be
// installed.
func (h *Hook) Handle(e Event) bool { return h.handle(e) }

// SetAfterGet makes the hook thread call f with the message and wParam of
// each message GetMessageW returns, until restore is called. Call it, and
// restore, while no hook is installed.
func SetAfterGet(f func(message uint32, wParam uintptr)) (restore func()) {
	prev := afterGet
	afterGet = f
	return func() { afterGet = prev }
}

// PostActive posts message with wParam to the thread of the installed
// hook.
func PostActive(message uint32, wParam uintptr) error {
	h := active.Load()
	if h == nil {
		return ErrNotActive
	}
	return postThreadMessage(h.tid, message, wParam, 0)
}

// ErrNotActive is returned by PostActive when no hook is installed.
var ErrNotActive = errors.New("winhook: no hook is installed")
