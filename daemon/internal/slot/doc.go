// Package slot connects the engine of one device slot to the Output
// Arbiter (ARCHITECTURE §4, §5): it turns input events into engine inputs,
// forwards the engine's key commands with a full output.Owner, and runs
// the engine's and the Arbiter's deadlines.
//
// # Scope in M1b
//
// A Slot is not safe for concurrent use and has no channels or timers of
// its own: its owner calls Handle for each event and Advance when
// NextDeadline is due, or lets Replay do both in virtual time. RunLoop is
// the slot loop of §5 (ADR-0005): a goroutine that owns the Slot, takes
// its edges and jobs from channels and runs its deadlines on one timer.
// The Arbiter is an interface: an *output.Arbiter the owner of the Slot
// also owns (dry runs, goldens, Replay), or an *output.RemoteArbiter,
// whose output.Loop, T-output, runs the Arbiter and its deadlines. The
// Slot keeps the State its events leave the controls in; Resync catches
// up with edges it missed and Reset forgets a state that is unknown. A
// Slot uses every event it is given, whatever its device: routing the
// events of a device to its slot is the job of the supervisor's router
// (package supervisor).
//
// # Owners
//
// The engine owns its commands with an engine.OwnerID; the Slot adds its
// index and profile generation (Owner). ReleaseAll ends every engine cycle,
// then has the Arbiter release every key of the slot's index, of any
// generation (OUT-07, §8.2). A removed device does the same, then the
// engine forgets which controls are down. Reload (PRO-03) switches the
// engine to a new Program, releases like ReleaseAll, and owns later
// commands with the new generation; the controls down across it stay down
// and do nothing until pressed again.
//
// # Pause
//
// SetPaused(true) (DMN-06) puts the engine in paused mode, which ends
// every cycle, and releases like ReleaseAll. Until SetPaused(false) every
// press is decided (engine.ReasonPaused) but sends nothing. Resuming
// presses nothing again: the controls down across it do nothing until
// pressed again. Config.Paused starts a slot paused. In RunLoop an Edge
// carries the pause (Edge.Pause): PauseOn before its Reset, Resync or
// Event, PauseOff after it; a PauseOn on a paused slot does nothing.
// ReleaseAll, Reset, Reload and a removed device keep the pause.
//
// # Errors
//
// Arbiter errors do not stop anything: the Slot keeps them until Errors
// takes them. So do events the engine does not know (ErrUnknownInput),
// once per control or kind; their events are dropped.
package slot
