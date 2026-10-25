package output

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// InputMark is the input edge that a slot is handling, for the latency
// statistics (DMN-15, ADR-0071): the Stamp that T-input read when it received
// the edge (input.Event.Stamp), and the key of the device it came from
// (input.DeviceKey), which the statistics are kept under. A zero Stamp is no
// edge.
//
// The slot sets it on its RemoteArbiter (MarkInput) for the duration of
// Slot.Handle, and clears it after. A command that the RemoteArbiter sends to
// the Loop in between carries a pointer to it, and the Arbiter ends the
// measurement at the first Send of that command that really goes out:
// ArbiterOptions.Latency gets the key and the time from the stamp to the
// return of Send, and the Stamp is set to 0 on T-output. The slot's goroutine
// reads it back after the command's reply, so that the later commands of the
// same edge are not measured again; the reply orders the two accesses.
type InputMark struct {
	Stamp latency.Stamp
	Key   string
}

// MarkInput sets the input edge that the following commands of this
// RemoteArbiter answer, until MarkInput(0, "") clears it (DMN-15). Only the
// commands that press keys carry it: Down, DownFor, Up, Tap, TapSequence,
// Sequence, TypeText and their variants with completion. Repeats, pointer
// moves, StopRepeat and the release of owners do not, and neither does any
// command that the Loop runs by itself at a deadline. It is not safe for
// concurrent use, like the other methods: the goroutine of the slot that owns
// the RemoteArbiter calls it.
func (r *RemoteArbiter) MarkInput(stamp latency.Stamp, key string) {
	r.mark = InputMark{Stamp: stamp, Key: key}
}

// do is Loop.Do for a command that carries the mark of the edge being handled,
// if there is one.
func (r *RemoteArbiter) do(f func(*Arbiter) error) error {
	if r.mark.Stamp == 0 {
		return r.l.do(f, nil)
	}
	return r.l.do(f, &r.mark)
}

// endMark ends the measurement of the mark of the command under way at the
// return of a Send that went out: it takes the time once, and gives the sink
// the latency when the Send injected all of its events (injected). It spends the
// mark whatever the Send did, so that the edge's later commands are not
// measured from its stamp. a.mark is nil outside the commands that carry one.
//
// The first Send of a command may be a retry of releases that failed before
// (Down retries them first): it takes the mark. This is rare, and the latency
// it gives is that of a key sent for the edge anyway.
func (a *Arbiter) endMark(injected bool) {
	m := a.mark
	if m == nil || m.Stamp == 0 {
		return
	}
	ret := latency.Now()
	stamp := m.Stamp
	m.Stamp = 0
	if injected && a.latency != nil {
		a.latency(m.Key, ret.Sub(stamp))
	}
}
