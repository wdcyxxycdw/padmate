// Package supervisor runs padmated's slots (DMN-02): the router, which
// hands each slot the events of its gamepad, and the supervision of the
// slot loops. It is what ADR-0016 decision 7 calls the supervisor: T-input
// (input.Source) feeds one channel, and the router, the goroutine of Run,
// reads it.
//
// The router keeps a slot.State per connected gamepad and never blocks on
// a slot. A slot whose edge channel is full falls behind: its edges are
// skipped until there is room, and then it gets, in order, the release of
// the gamepad it had (its removal, or a reset) if one came meanwhile, the
// DeviceAdded of the gamepad it has now if that changed, and one Resync to
// the router's state. So DeviceAdded and DeviceRemoved alternate on a
// slot while it is enabled, and the last DeviceAdded it saw is of the
// gamepad assigned now; a gamepad assigned and gone again while the slot
// was behind may not appear. A disabled slot gets no edge, and one enabled
// again is fresh: its first edge is the DeviceAdded of its gamepad, with no
// removal of the one before. When input dropped events, every slot gets a
// reset in order with its edges: the held state is unknown, so padmate
// lets go of everything (CLAUDE.md invariant 1). Config.Dropped counts the
// drops of every gamepad together, so a drop resets every slot (a reset
// per gamepad is IN-27); the reset comes after the events queued in Events
// when the router saw the drop, but not in order with a Sync's barriers.
//
// Each slot runs slot.RunLoop on a goroutine of its own, under a runner
// that restarts it, fresh and with every control up, after a panic, with
// a backoff, and disables it after 3 panics in 10 s until a gamepad is
// next assigned; the slot then gets that gamepad's DeviceAdded, and a
// Resync when the gamepad connected earlier (a hand-off or a take-over)
// or the router assigned it before it saw the slot disabled. A restart
// does not resync: the resync would press again the buttons whose action
// may have caused the panic. A panic of the runner itself ends Run with a
// *PanicError.
//
// There are Config.Slots slots, MaxSlots by default, all running from Run
// on, with or without a gamepad (IN-08). Each connected gamepad that
// matches Config.Device either has a slot or waits in line (IN-05): one
// that connects goes to the back, and one that loses its slot to the
// front. A slot is assigned:
//   - to a gamepad that connects while a slot is free: it takes the free
//     slot of the lowest index, and the slot's home becomes its DeviceKey.
//     A slot whose home is that key, held by a provisional stand-in, is
//     rehomed first: its home becomes the stand-in's key, and it gets no
//     edge, so what the stand-in holds stays held;
//   - when the slot's gamepad leaves, at once to the first in line, and
//     the home stays: the slot gets a copy of its DeviceAdded and then a
//     Resync to the router's state of it, since it may hold buttons
//     already.
//
// Each assignment is provisional until the holder's first ButtonDown event
// of any button on the enabled slot, which makes it final and the slot's
// home the holder's key. Axis events do not, since the sticks and triggers
// of an idle gamepad jitter, nor do the buttons a Resync delivers as held
// when it got the slot, nor a press while the slot is disabled. A down SDL
// reports after the gamepad is added does, even for a button held before:
// Raw Input and HIDAPI report held buttons that way, while XInput reports
// only their ups. A final holder keeps the
// slot until it leaves. Until then, the holder loses the slot:
//   - when it is a stand-in, not the home, to a gamepad with the home's key
//     that connects while every slot is taken: it takes the slot back
//     (reclaims it), provisionally;
//   - to a waiting gamepad's ButtonDown event (a trigger is an axis): it
//     takes over the provisional slot assigned longest ago, which is final
//     at once, with its DeviceAdded, a Resync to its state before the
//     press, then the press. So an idle gamepad that stays connected, one
//     that took a free slot at startup or waits first in line, does not
//     keep the slot from the one in use.
//
// Either way the slot gets a removal of the holder first, which releases
// what it holds, and the holder waits again, first. An eviction happens
// only when no slot is free, so the holder cannot find one: no slot is
// free while a gamepad waits. When nobody waits, the slot is free and has
// no home: it is kept for no one.
//
// Every slot maps with the same program but is otherwise on its own: its
// engine state, runner, edge channel and the owners of its keys
// (output.Owner.Slot). A slot lets go of its own keys only, and a slot
// that is slow, stalled, panicking or disabled delays or releases no
// other; Sync and Reload still wait for every slot. The slots share one
// keyboard through the Arbiter's refcount: a key two slots hold goes down
// once and up at the last release, whichever slot lets go last, and a key
// one slot holds, a modifier too, combines with the keys of another.
// Output that several slots make at the same instant reaches Output in no
// set order; each slot's own keeps its order.
//
// Reload switches every slot to a new program (PRO-03). Config.Program is
// generation 1, and each Reload takes the next, for all slots, when the
// router takes it; the gamepads, homes and the line stay as they are. The
// router sends each slot a barrier with the program behind the edges
// delivered so far, as for Sync, so the events sent before Reload was
// called are handled with the old program; a slot that is behind catches
// up first. The slot then lets go of all its keys and taps
// (slot.Slot.Reload): the controls down stay down and do nothing until
// pressed again, and later presses own their keys with the new
// generation. A slot that waits to restart or is disabled switches at
// once: it starts with the new program when it runs again, and the edges
// queued for it meanwhile, those sent before Reload included, map with
// the new program.
//
// Pause pauses the mapping of every slot, or resumes it (DMN-06): the
// router owns the user's pause for all slots and tells Config.OnPause of
// each change. An input.ComboHeld event on Events toggles it too (DMN-07),
// from any gamepad, one that waits or that Config.Device leaves out
// included; no slot gets the event, and it neither takes a slot over nor
// confirms one. Output's user gate goes first: Output.BlockFor(BlockUser)
// lets go of every key and drops every press from then on, so Pause
// returns with every key up and no slot presses anything while paused.
// The pause is not saved, so Run starts unpaused. The log says "mapping
// paused" or "mapping resumed", with what did it (by: "api" or "combo"),
// for a combo the id of its gamepad, and the reasons of a suspension
// under way.
//
// Suspend sets the reasons of the system's suspension (DMN-09): the
// session is locked, disconnected, on another desktop, going to sleep or
// ending (winsession.Reason), apart from the user's pause. It never waits:
// it closes Output's system gate at once (Output.BlockFor(BlockSystem)),
// which lets go of every key, and wakes the router, which applies the rest
// and tells Config.OnSuspend of each change. When the router looks, the
// reasons set since it last did hold the slots first, also when they were
// cleared again before it looked (the log says "collapsed"); when the
// reasons are none, the events queued in Events meanwhile are routed
// first, still held, so that what was pressed during the suspension is
// canceled, not mapped after it, and so are those queued before it. Only
// the router opens the system gate, once the suspension it applied is
// none, after a barrier on every slot behind the edges that let the slots
// go: a slot that has not handled its pause yet could press through an
// open gate what it did before. So a slot that is behind, or waits to
// restart, keeps the gate closed for every slot until it has caught up or
// runs again (a disabled one does not), and a Sync returns only once the
// gate opened; an open that a later Suspend overtook does nothing. A press
// that a slot handles between the end and its barrier may meet the gate
// still closed and be dropped. The log says "mapping suspended", "mapping
// suspension changed" or "mapping unsuspended", with the reasons. A
// Suspend before Run holds the slots from the start.
//
// The mapping is held while paused or suspended (Status.Mapping), and the
// two hold it the same way; a pause or a resume during a suspension, and a
// suspension's start or end while paused, change no slot, and Output's
// gate stays closed until neither holds. Each slot gets an Edge.Pause in
// order with its edges: it cancels what its controls hold, which the gate
// already released, and its presses send nothing until the mapping is let
// go. A slot that is behind gets the hold with its Resync, and one that
// missed a hold let go meanwhile is paused before its Resync. A slot the
// runner creates, restarted or enabled again, starts paused while the
// mapping is held, and each edge delivered while held carries PauseOn: the
// slot handles the edges queued for it held as the mapping was when they
// came, also when it was let go before the slot got them. Letting go
// presses nothing: the controls down stay down and do nothing until
// pressed again; a waiting gamepad handed a slot or taking one over after
// the hold ends still gets a Resync that presses what it holds, as in any
// hand-off. The hold survives Reload, resets, reconnects and restarts.
//
// Shutdown order, for the caller: cancel Run's context or close Events.
// Run then closes each slot's edge channel, so each slot loop handles the
// edges queued, releases its keys through Output and ends, and Run
// returns once every slot loop has ended. Then call Output.Stop (a
// ReleaseAll and its retries), then close the input.Source. Run never
// stops Output: the caller owns it. Suspend may still be called then, by
// the source of the suspension (winsession.Watcher), which the caller may
// close last: after Run it only closes the system gate, and after
// Output.Stop it does nothing.
//
// The package reads time only from Config.Clock and is cross-platform.
package supervisor
