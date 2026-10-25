package slot

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// Edge is one item of a slot's edge channel: an input event; when Resync
// is set, the full state to resync to; when Reset is set, a Slot.Reset,
// for when the state is unknown. Reset takes precedence over Resync, and
// Resync over Event: the loop ignores the others. Whoever lost edges must
// deliver the Resync or Reset without waiting for the device's next
// event: a device sends only changes, so the lost edges may be its last
// releases (CLAUDE.md invariant 1).
//
// Pause, when not PauseKeep, pauses or resumes the slot (Slot.SetPaused)
// (DMN-06). PauseOn goes before the Reset, Resync or Event and PauseOff
// after it, so the presses a Resync brings in stay canceled either way, as
// they would on a slot that was not behind. A PauseOn on a paused slot
// does nothing, so a router can mark each edge it delivers while paused:
// a slot started since, which may not know of the pause, still handles it
// paused. An edge with a Pause and an Event of no Kind carries the pause
// alone.
type Edge struct {
	Event  input.Event
	Resync *State
	Reset  bool
	Pause  Pause
}

// Pause is the pause value of an Edge (DMN-06).
type Pause uint8

const (
	PauseKeep Pause = iota // leave the slot as it is
	PauseOn                // Slot.SetPaused(true)
	PauseOff               // Slot.SetPaused(false)
)

// LoopConfig configures RunLoop.
type LoopConfig struct {
	Slot  *Slot
	Edges <-chan Edge        // required; closing it ends the loop
	Jobs  <-chan func(*Slot) // may be nil; each job runs on the loop goroutine
	// Lock locks the goroutine running RunLoop to its OS thread until
	// RunLoop returns (LockOSThread, deferred UnlockOSThread) (ADR-0005).
	Lock bool
	// Errors, when set, gets Slot.Errors() after each wake when there are
	// any, on the loop goroutine. It must not block or call the Slot.
	Errors func([]error)
}

// PanicError is RunLoop's error after a panic on the loop goroutine.
type PanicError struct {
	Value      any    // what was recovered
	Stack      []byte // debug.Stack() at the recover
	ReleaseErr error  // error of the release after the panic
}

func (e *PanicError) Error() string {
	msg := fmt.Sprintf("slot: panic in the slot loop: %v", e.Value)
	if e.ReleaseErr != nil {
		msg += "; releasing the keys after it: " + e.ReleaseErr.Error()
	}
	return msg
}

// RunLoop runs the slot loop until Edges is closed (returns nil), ctx is
// done (returns ctx.Err()) or a panic (returns *PanicError). It lets go
// of every key of the slot before it returns (invariant 1).
//
// The loop owns the Slot while it runs: it handles the edges, runs the
// jobs, and runs Advance on one timer that follows Slot.NextDeadline. It
// runs the work that is due before each edge and job. After every wake it
// re-arms the timer, then hands the Slot's errors to Errors. A job runs
// after the edges queued in Edges when the loop takes it, the re-arm and
// the hand-over included, so a job sent after edges is a barrier behind
// them however the channel is buffered. At a normal end it runs
// Slot.ReleaseAll and the work that is then due, and hands over the
// errors. After a panic the engine's state is suspect: the loop releases
// the keys of every owner with the slot's index through the Arbiter, not
// the engine. Time is read only from the slot's clock.
//
// After a runtime.Goexit on its goroutine, RunLoop releases the keys the
// same way and hands an error of that release to Errors, but it cannot
// return: a caller that must notice uses a defer of its own.
func RunLoop(ctx context.Context, cfg LoopConfig) (err error) {
	s := cfg.Slot
	switch {
	case s == nil:
		return errors.New("slot: RunLoop needs a Slot")
	case cfg.Edges == nil:
		return errors.New("slot: RunLoop needs an edge channel")
	}
	if cfg.Lock {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	alarm := clock.NewAlarm(s.clk)
	defer alarm.Stop()
	finished := false
	defer func() {
		if finished {
			return
		}
		v := recover()
		if v == nil {
			// runtime.Goexit (panic(nil) recovers a *runtime.PanicNilError):
			// the goroutine goes on exiting, so no one gets err.
			rerr := errors.Join(s.Dispose(), releaseSlot(s.arb, s.index))
			if rerr != nil && cfg.Errors != nil {
				cfg.Errors([]error{rerr})
			}
			return
		}
		err = &PanicError{Value: v, Stack: debug.Stack(), ReleaseErr: errors.Join(s.Dispose(), releaseSlot(s.arb, s.index))}
	}()

	due := func() {
		if d, ok := s.NextDeadline(); ok && d <= s.clk.Now() {
			s.Advance()
		}
	}
	deliver := func() {
		if cfg.Errors == nil {
			return
		}
		if errs := s.Errors(); len(errs) > 0 {
			cfg.Errors(errs)
		}
	}
	end := func(err error) error {
		s.preserveLayers()
		// Not ReleaseAll: it would rebuild a macro runtime that Dispose
		// then tears down.
		s.keep(s.releaseAll())
		s.keep(s.Dispose())
		due()
		deliver()
		finished = true
		return err
	}

	handle := func(e Edge) {
		if e.Pause == PauseOn && !s.Paused() {
			s.keep(s.SetPaused(true))
		}
		switch {
		case e.Reset:
			s.Reset()
		case e.Resync != nil:
			s.Resync(*e.Resync)
		case e.Pause == PauseKeep, e.Event.Kind != 0:
			s.Handle(e.Event)
		}
		if e.Pause == PauseOff {
			s.keep(s.SetPaused(false))
		}
	}

	jobs := cfg.Jobs
	wake := s.wake()
	alarm.Set(s.NextDeadline())
	for {
		select {
		case <-ctx.Done():
			return end(ctx.Err())
		case e, ok := <-cfg.Edges:
			if !ok {
				return end(nil)
			}
			due()
			handle(e)
		case <-alarm.C():
			alarm.Fired()
			due()
		case <-wake:
			// The watcher has queued work on the script runtime. Its deadline
			// did not exist when this loop last armed its alarm.
			s.Advance()
		case job, ok := <-jobs:
			switch {
			case !ok:
				jobs = nil // jobs end; edges go on
			case job != nil:
				// A select picks at random: the edges sent before the job
				// may still be queued. The job sees them handled, the
				// timer re-armed and their errors handed over.
				for n := len(cfg.Edges); n > 0; n-- {
					e, ok := <-cfg.Edges
					if !ok {
						break
					}
					due()
					handle(e)
				}
				due()
				alarm.Set(s.NextDeadline())
				deliver()
				job(s)
			}
		}
		alarm.Set(s.NextDeadline())
		deliver()
	}
}

// releaseSlot releases the keys of every owner with the slot's index,
// of any generation, and recovers a panic of its own.
func releaseSlot(arb Arbiter, index uint8) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("slot: panic releasing the keys of slot %d: %v", index, v)
		}
	}()
	return arb.ReleaseOwners(func(o output.Owner) bool { return o.Slot == index })
}
