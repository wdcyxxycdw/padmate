// Package engine is the declarative mapping engine (MAP-01, ARCHITECTURE
// §4, §7): a pure state machine that turns control edges into key
// commands.
//
// Compile turns a validated profile.Profile into an immutable Program that
// any number of engines can share. An Engine is the mutable state of one
// slot: the slot calls Step with each input edge, Apply when NextDeadline
// is due, CancelAll or Reset when every key must go up, Reload when the
// profile changes, and Pause and Resume when the mapping is paused and
// resumed; the engine sends its commands to an Output, which the slot
// forwards to the Output Arbiter with a full output.Owner.
//
// # Dependencies
//
// The engine imports keys, control, clock and profile, and nothing that
// touches the system (invariant 2, enforced by depguard and forbidigo): it
// never reads the time, which comes from Input.T and the now arguments, so
// it runs and is tested the same on any platform. profile never imports
// engine. The gesture names belong to profile, which reads them from the
// file; Gesture here is an alias of profile.Gesture, so M2 can add
// gestures to both without an import cycle.
//
// # Press cycles
//
// Every press of a control starts a press cycle. Cycle ids come from one
// counter per Engine, start at 1 and are never reused, not even after
// Reset. All output of a cycle has the owner OwnerID{SourceDecl, cycle}.
// The release of a control uses what its press found (press-owner rule,
// §7.4): a hold-mode press remembers its chord, and the release lets go of
// that chord without looking the binding up again.
//
// The trigger axes drive the virtual controls LT and RT (ANA-01, §7.6): a
// trigger presses at TriggerPress of its travel and releases at
// TriggerRelease, with nothing in between (hysteresis). A second stage,
// LTFull and RTFull (ANA-14), presses at TriggerFullPress and releases at
// TriggerFullRelease; LT stays down while it is, it presses after LT and
// releases before it.
//
// # Stick rings and the knob
//
// A stick that is active (pushed past directionThreshold) is in the inner
// ring, or in the outer ring from outerThreshold on, until it falls
// RingHysteresis below it (ANA-10): LSInner and LSOuter (RS*) are overlays
// like LSActive. With mode "knob" the stick sends no directions; every
// knobStepDeg of rotation queues one tick, LSCW or LSCCW, which Apply sends
// as a press and a release of that control, at most one per call and not
// closer than the key can follow (ANA-11). The queue is one revolution long.
// The knob reads the angle KnobSettle after an axis input, when both axes of
// a report are in, not at the input.
//
// # Ending every cycle
//
// CancelAll ends every active cycle. It sends nothing but the Up of the
// latched chords (MAP-10): the slot then has the Arbiter release the rest
// of the keys (OUT-07, §8.2). It also drops buffered edges, pending
// gestures, multi-tap windows, repeats and overlays. Controls stay down in
// the engine's view, so their physical release does nothing and a trigger
// must fall to TriggerRelease or below before it presses again.
// Reset also forgets which controls are down, for a device that went away.
// Reload (PRO-03) runs CancelAll and then looks every later press up in
// the new Program: a control held across it does nothing until it is
// released and pressed again, and is never pressed again by the reload. The
// one thing it does not stop is the pointer of a stick still pushed in mode
// "mouse" (stick_mouse.go): that goes on with the new settings, as it does
// across a calibration change, while CancelAll stops it and locks the stick
// until it is back inside the deadzone.
//
// # Paused mode
//
// Pause (DMN-06) runs CancelAll and keeps the engine paused until Resume:
// a press still starts a cycle and looks its binding up, but the cycle is
// canceled at once, with ReasonPaused, and sends nothing, so its release
// sends nothing either. Resume sends nothing and presses nothing again: a
// control held across it does nothing until it is released and pressed
// again, as after CancelAll. Reset, Reload and CancelAll keep the engine
// paused.
//
// # Decisions
//
// With Options.Trace set, the engine reports a Decision for every step
// that changes or could change something, and for every cycle CancelAll
// ends (ENG-20): the control, the edge, the cycle, what the lookup found
// and why the engine did what it did. A decision comes before the output
// it causes, so a trace of both reads as cause, then effect; a cancel
// decision comes before the Arbiter releases the keys.
//
// # Gestures and deadlines
//
// NextDeadline is the earliest timer: a hold threshold, a multi-tap window,
// a repeat or a stick direction delay; Apply runs the due ones once. The
// gestures that run after the control is released (release, tap and the
// multi-taps, profile.Gesture.Momentary) only tap keys and never hold keys
// or a layer, nor repeat: Compile rejects that, as profile.Parse does. A
// multi-tap that its window or another press commits runs on a copy of the
// control's state, so a hold it started would never end (invariant 1).
//
// While a gesture with both a tap and a hold is pending, the button edges
// that come after it wait in a buffer and replay in their order once it
// resolves (MAP-09); only the release of a control already dispatched goes
// at once, and only when nothing is buffered. Only the decision, the hold
// or tap action, comes before the edges the gesture captured: the pending
// control's own release decides a tap from its place in the buffer and
// replays there, so the keys and layers its press holds, its release
// action and its multi-tap window stay in input order. The recognizers see
// the original times of replayed edges: a replayed press's hold threshold
// and a replayed release's multi-tap window count from them.
//
// The lookup of a binding walks the active levels (ENG-21, M3): the
// Registry's when Options.Registry is set, otherwise the overlays from the
// top, then the base layer and common. Above them is the set of the function
// key that is held, if there is one and it binds the control.
//
// # Function keys
//
// One of back, start and guide is the function key (MAP-27, ADR-0069,
// ADR-0075, function_keys.go): start unless Compile is given another with
// WithFunctionKey. Its edges never enter gesture recognition: it starts no
// cycle, is not buffered, interrupts nothing, sends nothing and is not shown
// to a script, so it adds no delay to any key. It only decides which set of
// bindings (the profile's function set) a later press looks up first. The set
// is chosen when the press arrives (Input.fn travels with a buffered edge),
// and the release goes to what the press chose. The function key follows the
// physical state: CancelAll, Pause and Reload keep it, Reset clears it, and so
// does a Reload that changes the function key. Its edges are decisions with
// ReasonFunction. Compile ignores bindings on it in a layer, in common or in
// the set (Program.IgnoredFunctionKeyBindings); the other two of the three
// are ordinary controls.
package engine
