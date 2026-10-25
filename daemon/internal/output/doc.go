// Package output turns keys into keyboard events and injects them. The mouse
// wheel keys are events too (ADR-0062): a notch is momentary, so its press is
// the whole action and its release (KeyEvent.Silent) injects nothing. The
// mouse buttons are held like keys, with a button down and a button up, and
// are never pressed again while they are down.
//
// It holds the backend-agnostic key-event model (KeyEvent, Stroke), the
// dwExtraInfo tag that marks every event padmate injects, Tap, which sends
// a stroke without ever leaving a key held, the Output Arbiter (OUT-06),
// the Resolver, which maps character keys onto the foreground keyboard
// layout (OUT-04), and on Windows the SendInput sink. The Arbiter owns the
// key state of the daemon: owners hold and tap chords through it, it
// reference-counts the physical keys, and its Sender is the only writer of
// the sink (CLAUDE.md invariant 3). Below the Arbiter, a MenuMask masks the
// release of a lone Alt or Win key (OUT-08). A Loop is T-output: a
// goroutine, which the daemon must lock to its OS thread, that owns the
// Arbiter and runs the commands other goroutines hand it, directly or
// through a RemoteArbiter. Loop.BlockFor releases every key and closes the
// Arbiter's gate on presses for one owner, without waiting for the loop:
// BlockUser is the pause (DMN-06), which Loop.Block uses, and BlockSystem
// the system's suspension (DMN-09), and BlockEmergency fences the offline
// release-all operation while slots discard old script work. The gate stays
// closed while any owner keeps it closed.
//
// Everything except sendinput_windows.go and layout_windows.go builds and
// tests on every OS. SystemLayouts exists everywhere: on Windows it reads
// the system's layouts (layout_windows.go); elsewhere it has none
// (layout_other.go), so character keys fall back to US.
package output
