package output_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// Operations of FuzzArbiter. Each takes the opcode byte and two argument
// bytes a and b from the input.
const (
	fzDown         = iota // Down(owner a, chord b)
	fzUp                  // Up(owner a, chord b)
	fzTap                 // Tap(owner a, chord b, hold (a/3%4)*10ms)
	fzRepeat              // Repeat(owner a, chord b)
	fzAdvance             // Sim.Advance(b%64 ms), then Arbiter.Advance if a is odd
	fzFail                // the next b%3+1 Send calls inject at most a%4 events
	fzWarn                // the next Send call warns
	fzReleaseOwner        // ReleaseOwner(owner a)
	fzReleaseSlot         // ReleaseOwners(slot a%2)
	fzReleaseAll          // ReleaseAll
	fzBlock               // SetBlocked(a%4 == 3)
	fzOps
)

var fuzzOwners = [...]output.Owner{
	{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 1},
	{Slot: 0, Gen: 1, Source: output.SourceScript},
	{Slot: 1, Gen: 2, Source: output.SourceDecl, Cycle: 7},
}

// fuzzChords overlap on purpose: Z, KeyZ and sc:0x2C are one key, Plus
// and = another, Ctrl, LCtrl and sc:0x1D a third.
var fuzzChords = [...]string{
	"Z", "KeyZ", "sc:0x2C", "Ctrl+Z", "LCtrl+Z", "Ctrl+Shift+Z", "RCtrl+Z", "Plus",
	"=", "Shift", "Ctrl", "Ctrl+Shift", "X", "Alt+X", "sc:0x1D", "Pause", "Num1",
}

const fuzzRetry = 7 * ms

// fuzzSeeds are named regression and coverage inputs, as op, a, b triples.
var fuzzSeeds = []struct {
	name string
	ops  []byte
}{
	{"down up", []byte{fzDown, 0, 3, fzUp, 0, 3}},
	{"shared key", []byte{fzDown, 0, 0, fzDown, 1, 1, fzDown, 2, 2, fzUp, 1, 1, fzUp, 0, 0}},
	{"left held", []byte{fzDown, 0, 5, fzDown, 1, 10, fzTap, 2, 7}},
	{"tap on held", []byte{fzDown, 1, 0, fzTap, 3, 3, fzAdvance, 0, 15, fzUp, 1, 0}},
	{"tap then down", []byte{fzTap, 3, 0, fzAdvance, 0, 5, fzDown, 1, 1, fzAdvance, 0, 20}},
	{"tap released", []byte{fzTap, 9, 5, fzReleaseOwner, 0, 0, fzAdvance, 0, 40}},
	{"slot released", []byte{fzDown, 0, 3, fzDown, 2, 4, fzTap, 4, 12, fzReleaseSlot, 0, 0}},
	{"repeat", []byte{fzDown, 0, 11, fzRepeat, 0, 11, fzUp, 0, 11, fzRepeat, 0, 11}},
	{"partial press", []byte{fzFail, 1, 0, fzDown, 0, 5, fzUp, 0, 5}},
	{"pending release", []byte{fzDown, 0, 5, fzFail, 1, 1, fzUp, 0, 5, fzAdvance, 0, 3, fzAdvance, 0, 10}},
	{"pending pressed", []byte{fzDown, 0, 3, fzFail, 1, 2, fzUp, 0, 3, fzDown, 1, 10}},
	{"pending flushed", []byte{fzDown, 0, 3, fzFail, 0, 1, fzUp, 0, 3, fzDown, 1, 12, fzReleaseAll, 0, 0}},
	{"tap end fails", []byte{fzTap, 3, 5, fzFail, 1, 2, fzAdvance, 1, 30, fzAdvance, 0, 50}},
	{"warn", []byte{fzWarn, 0, 0, fzDown, 0, 7, fzWarn, 0, 0, fzUp, 0, 7}},
	{"early advance", []byte{fzTap, 6, 4, fzAdvance, 1, 3, fzDown, 0, 0, fzFail, 2, 2, fzUp, 0, 0, fzAdvance, 1, 1}},
	{"blocked", []byte{
		fzDown, 0, 3, fzBlock, 3, 0, fzDown, 1, 5, fzTap, 2, 7, fzRepeat, 0, 3, fzUp, 0, 3, fzBlock, 0, 0, fzDown, 1, 5,
	}},
	{"blocked tap end", []byte{fzTap, 3, 0, fzBlock, 7, 0, fzAdvance, 0, 20, fzDown, 0, 0}},
	{"blocked pending", []byte{fzDown, 0, 5, fzFail, 1, 1, fzUp, 0, 5, fzBlock, 3, 0, fzTap, 9, 5, fzAdvance, 0, 10}},
	{"queued merged", []byte{fzTap, 3, 0, fzTap, 1, 3, fzTap, 2, 5, fzAdvance, 0, 40}},
	{"queued owner released", []byte{fzTap, 3, 0, fzTap, 1, 3, fzReleaseOwner, 1, 0}},
	{"queued blocked", []byte{fzTap, 3, 0, fzTap, 1, 3, fzBlock, 3, 0}},
	{"queued then held", []byte{fzTap, 3, 0, fzTap, 1, 3, fzDown, 2, 0}},
	{"queued release fails", []byte{fzTap, 3, 0, fzTap, 1, 3, fzFail, 0, 1, fzAdvance, 0, 40}},
	// Z's release failed twice and is pending. A tap of Z waits if the resend
	// before it fails again, and starts at once if it works (OUT-10).
	{"tap on pending key", []byte{fzDown, 0, 0, fzFail, 0, 1, fzUp, 0, 0, fzFail, 0, 0, fzTap, 1, 0, fzAdvance, 1, 20}},
	{"tap on pending key resent", []byte{fzDown, 0, 0, fzFail, 0, 1, fzUp, 0, 0, fzTap, 1, 0}},
	{"taps merged on pending key", []byte{
		fzDown, 0, 0, fzFail, 0, 1, fzUp, 0, 0, fzFail, 0, 1, fzTap, 1, 0, fzTap, 2, 0, fzAdvance, 1, 20,
	}},
	// The resend frees the key of a queued tap, which starts before the
	// next tap and leaves the key free, or down for 10 ms.
	{"queued tap freed", []byte{fzDown, 0, 0, fzFail, 0, 1, fzUp, 0, 0, fzFail, 0, 0, fzTap, 1, 0, fzTap, 2, 0}},
	{"queued tap freed holds", []byte{
		fzDown, 0, 0, fzFail, 0, 1, fzUp, 0, 0, fzFail, 0, 0, fzTap, 4, 0, fzTap, 2, 0, fzAdvance, 1, 30,
	}},
	// sc:0x1D and Z pending as ReleaseAll leaves them, then a tap of Z (the
	// corpus entry 58c4f94561fbda16).
	{"tap after release all fails", []byte{
		fzWarn, 0, 0, fzWarn, 0, 0, fzFail, 0, 2, fzDown, 0, 14, fzDown, 0, 4, fzReleaseAll, 0, 0, fzTap, 0, 0, fzAdvance, 1, 30,
	}},
	// A tap of sc:0x1D waits for its pending release. The resend of the Down
	// of sc:0x1D frees the key, so the tap starts before the Down takes the
	// key: no tap on a held key (the corpus entry 8ff0748752119d43).
	{"down after the resend", []byte{
		fzWarn, 0, 0, fzWarn, 0, 0, fzFail, 0, 2, fzDown, 0, 14, fzDown, 0, 15, fzReleaseAll, 0, 0, fzTap, 0, 14, fzDown, 0, 14,
	}},
	// Both pending keys are freed by the resend of the Down of sc:0x1D, but
	// the queued tap of Ctrl+Z starts first and holds Ctrl for 20 ms: the tap
	// of Ctrl starts after the Down, on the key it holds (the corpus entry
	// acb1ba7414f89e3b).
	{"down after the resend, key taken", []byte{
		fzFail, 0, 2, fzFail, 0, 2, fzFail, 0, 0, fzDown, 0, 14, fzTap, 0, 4, fzReleaseAll, 0, 0, fzTap, 7, 4, fzTap, 0, 14, fzDown, 0, 14,
	}},
	// Ctrl and Z are pending. The tap of sc:0x1D (Ctrl) waits for Ctrl, the
	// tap of Ctrl+Z for Z. The resend of the Down of sc:0x1D frees only Z, so
	// the tap of Ctrl+Z starts, presses Ctrl and lets go of it at once (a hold
	// of 0): Ctrl is free, and the tap of sc:0x1D, which was first in the
	// queue, starts too, before the Down takes the key. Neither is a tap on a
	// held key (the corpus entry b75adb93763a17e6).
	{"down after the resend, key freed by a tap", []byte{
		fzDown, 0, 4, fzFail, 0, 2, fzFail, 0, 0, fzReleaseAll, 0, 0, fzTap, 0, 14, fzTap, 0, 4, fzFail, 1, 0, fzDown, 0, 14,
	}},
}

// FuzzArbiter runs random commands from three owners over overlapping
// chords, with random clock advances and Send failures, and closes and
// opens the gate on presses. It checks every batch, no press among them
// while the gate is closed, and after every step that the Arbiter's view,
// the Recorder's physical keys and a model agree. At the end, with
// failures off and the gate as it was, it releases everything, with Up of
// every hold and Settle after an odd number of steps, else with
// ReleaseAll: nothing may be left down, physically or logically.
func FuzzArbiter(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed.ops)
	}
	chords := make([]keys.Chord, len(fuzzChords))
	for i, spec := range fuzzChords {
		chords[i] = keys.MustParse(spec)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 3*300 {
			data = data[:3*300]
		}
		fz := newFuzzer(t, chords)
		steps := 0
		for i := 0; i < len(data); i += 3 {
			var a, b byte
			if i+1 < len(data) {
				a = data[i+1]
			}
			if i+2 < len(data) {
				b = data[i+2]
			}
			fz.step(i/3, data[i]%fzOps, a, b)
			steps++
		}
		fz.finish(steps%2 == 1)
	})
}

type holdKey struct {
	o output.Owner
	c keys.Chord
}

// lastEvent is the last event offered to the sender for a key.
type lastEvent struct {
	up, injected bool
}

type fuzzer struct {
	t      *testing.T
	s      tsim
	chords []keys.Chord

	holds  []holdKey           // the holds the model expects, sided
	want   output.ArbiterStats // Retries: the last value seen
	queued []output.QueuedTap  // independent pending-main-key model

	plans   []int // per upcoming Send call: events to inject, or -1 to warn
	used    int   // plans used during the current step
	off     bool  // failures disabled
	blocked bool  // the gate is closed
	last    map[output.KeyID]lastEvent
	desc    string // the current step, for messages
}

func newFuzzer(t *testing.T, chords []keys.Chord) *fuzzer {
	fz := &fuzzer{t: t, chords: chords, last: map[output.KeyID]lastEvent{}}
	fz.s = newSim(t, withRetry(fuzzRetry))
	fz.s.Out.Inject = fz.inject
	return fz
}

// inject is the Recorder's hook. It checks the batch against Held(), which
// still lists the keys being released, then applies the failure plans and
// counts their outcome.
func (fz *fuzzer) inject(events []output.KeyEvent) (int, error) {
	held := fz.held()
	rank := 0
	for _, e := range events {
		h, ok := held[e.ID()]
		if !ok {
			fz.t.Fatalf("%s: batch [%s] has %s, which is not in Held() %s",
				fz.desc, eventsString(events), keyName(e), heldString(fz.s.Arb.Held()))
		}
		if e.Up && len(h.Owners) > 0 {
			fz.t.Fatalf("%s: release of %s sent while held by %v", fz.desc, keyName(e), h.Owners)
		}
		if !e.Up && fz.blocked {
			fz.t.Fatalf("%s: press of %s sent while the gate is closed", fz.desc, keyName(e))
		}
		// Every event of a key, extra presses included, repeats its press.
		if want := withUp(h.Press, e.Up); e != want {
			fz.t.Fatalf("%s: sent %+v for a key pressed with %+v", fz.desc, e, h.Press)
		}
		// OUT-05: presses, modifiers first, then releases, modifiers last.
		r := eventRank(e)
		if r < rank {
			fz.t.Fatalf("%s: batch [%s] is out of order", fz.desc, eventsString(events))
		}
		rank = r
	}
	n, err := len(events), error(nil)
	if !fz.off && len(fz.plans) > 0 {
		p := fz.plans[0]
		fz.plans = fz.plans[1:]
		fz.used++
		if p < 0 {
			err = errWarn
		} else {
			n, err = min(p, n), errBlocked
		}
	}
	for i, e := range events {
		fz.last[e.ID()] = lastEvent{up: e.Up, injected: i < n}
	}
	switch {
	case n < len(events):
		fz.want.SendErrors++
	case err != nil:
		fz.want.SendWarnings++
	}
	return n, err
}

func withUp(e output.KeyEvent, up bool) output.KeyEvent {
	e.Up = up
	return e
}

// eventRank orders the events of a batch: modifier presses, other
// presses, other releases, modifier releases.
func eventRank(e output.KeyEvent) int {
	mod := fuzzModifiers[e.ID()]
	switch {
	case !e.Up && mod:
		return 0
	case !e.Up:
		return 1
	case !mod:
		return 2
	}
	return 3
}

// fuzzModifiers are the physical keys of the modifiers on the US layout.
var fuzzModifiers = func() map[output.KeyID]bool {
	m := map[output.KeyID]bool{}
	for _, k := range keys.Table() {
		if e, err := output.KeyEventFor(k, false); k.IsModifier() && err == nil {
			m[e.ID()] = true
		}
	}
	return m
}()

func eventsString(events []output.KeyEvent) string {
	parts := make([]string, len(events))
	for i, e := range events {
		parts[i] = "+" + keyName(e)
		if e.Up {
			parts[i] = "-" + keyName(e)
		}
	}
	return strings.Join(parts, " ")
}

func (fz *fuzzer) held() map[output.KeyID]output.HeldKey {
	m := map[output.KeyID]output.HeldKey{}
	for _, h := range fz.s.Arb.Held() {
		m[h.Press.ID()] = h
	}
	return m
}

func (fz *fuzzer) step(i int, op, a, b byte) {
	o := fuzzOwners[int(a)%len(fuzzOwners)]
	c := fz.chords[int(b)%len(fz.chords)]
	sided := c.Sided()
	hk := holdKey{o, sided}
	holding := slices.Contains(fz.holds, hk)
	arb := fz.s.Arb
	fz.used = 0
	var err error
	var released func(output.Owner) bool // owners a release op dropped
	var early map[output.KeyID]bool      // keys a Down's own resend frees
	switch op {
	case fzDown:
		fz.desc = describe(i, "Down", o, c)
		early = fz.startedKeys(fz.retryFreed())
		switch {
		case fz.blocked:
			fz.want.Blocked++
		case holding:
			fz.want.DuplicateDowns++
		default:
			fz.holds = append(fz.holds, hk)
		}
		err = arb.Down(o, c)
	case fzUp:
		fz.desc = describe(i, "Up", o, c)
		if holding {
			fz.holds = slices.DeleteFunc(fz.holds, func(h holdKey) bool { return h == hk })
		} else {
			fz.want.UnmatchedUps++
		}
		err = arb.Up(o, c)
	case fzTap:
		hold := time.Duration(a/3%4) * 10 * ms
		fz.desc = describe(i, "Tap "+hold.String(), o, c)
		st, _ := output.ChordStroke(sided)
		main := st.Down[len(st.Down)-1].ID()
		observe := false
		if fz.blocked {
			fz.want.Blocked++
		} else {
			observe = fz.expectTap(o, sided, main)
		}
		before := arb.Stats()
		err = arb.Tap(o, c, hold)
		if observe {
			fz.observeTap(o, sided, main, before)
		}
	case fzRepeat:
		fz.desc = describe(i, "Repeat", o, c)
		switch {
		case fz.blocked:
			fz.want.Blocked++
		case !holding:
			fz.want.InvalidRepeats++
		}
		err = arb.Repeat(o, c)
	case fzAdvance:
		d := time.Duration(b%64) * ms
		fz.desc = describe(i, "Advance "+d.String(), output.Owner{}, keys.Chord{})
		fz.s.Advance(d)
		err = errors.Join(fz.s.Errors()...)
		if a%2 == 1 {
			err = errors.Join(err, arb.Advance())
		}
	case fzFail:
		for range b%3 + 1 {
			fz.plans = append(fz.plans, int(a%4))
		}
		return
	case fzWarn:
		fz.plans = append(fz.plans, -1)
		return
	case fzReleaseOwner:
		fz.desc = describe(i, "ReleaseOwner", o, keys.Chord{})
		released = func(r output.Owner) bool { return r == o }
		fz.holds = slices.DeleteFunc(fz.holds, func(h holdKey) bool { return released(h.o) })
		err = arb.ReleaseOwner(o)
	case fzReleaseSlot:
		slot := a % 2
		fz.desc = describe(i, fmt.Sprintf("ReleaseOwners slot %d", slot), output.Owner{}, keys.Chord{})
		released = func(r output.Owner) bool { return r.Slot == slot }
		fz.holds = slices.DeleteFunc(fz.holds, func(h holdKey) bool { return released(h.o) })
		err = arb.ReleaseOwners(released)
	case fzReleaseAll:
		fz.desc = describe(i, "ReleaseAll", output.Owner{}, keys.Chord{})
		released = func(output.Owner) bool { return true }
		fz.holds = nil
		err = arb.ReleaseAll()
	case fzBlock:
		on := a%4 == 3
		fz.desc = describe(i, fmt.Sprintf("SetBlocked(%v)", on), output.Owner{}, keys.Chord{})
		calls := fz.s.Out.Calls()
		arb.SetBlocked(on)
		fz.blocked = on
		fz.syncQueue(nil, on, nil)
		if got := fz.s.Out.Calls(); got != calls {
			fz.t.Fatalf("%s: sent %s", fz.desc, batches(fz.s.Out, calls))
		}
		if arb.Blocked() != on {
			fz.t.Fatalf("%s: Blocked() = %v", fz.desc, arb.Blocked())
		}
		// It resends nothing: the pending releases wait for the next step.
		fz.check()
		return
	}
	if maybe := fz.syncQueue(released, false, early); maybe > 0 {
		fz.observeTapsOnHeld(maybe)
	}
	// A release drops every hold and pending tap of the owners it matches.
	for _, h := range fz.s.Arb.Held() {
		if released != nil && slices.ContainsFunc(h.Owners, released) {
			fz.t.Fatalf("%s: %s is still held by %v", fz.desc, keyName(h.Press), h.Owners)
		}
	}
	// Every Send error reaches the caller, and there are no others.
	if (err != nil) != (fz.used > 0) {
		fz.t.Fatalf("%s: error %v after %d failed or warned Send calls", fz.desc, err, fz.used)
	}
	if fz.used == 0 {
		// Every Send call went out whole, so the step resent the pending
		// releases, unless it was a Sim.Advance that stopped short of the
		// retry; and a ReleaseAll left nothing at all.
		if n := arb.Stats().PendingReleases; n > 0 && (op != fzAdvance || a%2 == 1) {
			fz.t.Fatalf("%s: %d releases pending after a clean step: %s", fz.desc, n, heldString(arb.Held()))
		}
		if op == fzReleaseAll {
			fz.expectEmpty()
		}
	}
	fz.check()
}

func (fz *fuzzer) heldByDown(id output.KeyID) bool {
	for _, h := range fz.holds {
		stroke, _ := output.ChordStroke(h.c)
		if slices.ContainsFunc(stroke.Down, func(e output.KeyEvent) bool { return e.ID() == id }) {
			return true
		}
	}
	return false
}

// expectTap updates the model for a Tap of the sided chord whose main key is
// main, which the gate lets through. A tap on a key that Down holds presses
// it once more. Otherwise it waits behind its main key while the key is in
// the ledger (OUT-10): held by another tap, or pending release, where it
// stays until a release is injected; it merges into the waiting tap of the
// key, if any. The Arbiter first resends the pending releases, so a pending
// key is free for this tap if that resend injects its release (retryFreed).
//
// The resend also frees the keys of the queued taps that wait for it, and
// those start before this tap: they may press the key themselves, or leave
// it pending again if their release fails. Then expectTap reports that it
// cannot say, and observeTap checks what the Arbiter did.
func (fz *fuzzer) expectTap(o output.Owner, sided keys.Chord, main output.KeyID) (observe bool) {
	freed := fz.retryFreed()
	h, inLedger := fz.held()[main]
	switch {
	case fz.heldByDown(main):
		fz.want.TapsOnHeld++
	case slices.ContainsFunc(fz.queued, func(q output.QueuedTap) bool { return freed[q.Main] }):
		return true
	case inLedger && (!h.Pending || !freed[main]):
		if slices.ContainsFunc(fz.queued, func(q output.QueuedTap) bool { return q.Main == main }) {
			fz.want.MergedTaps++
		} else {
			fz.want.QueuedTaps++
			fz.queued = append(fz.queued, output.QueuedTap{Owner: o, Chord: sided, Main: main})
		}
	}
	return false
}

// retryFreed returns the keys whose pending release the next command's
// resend injects (Arbiter.retryPending): the Send call it makes, if any
// key is pending, takes the next failure plan. It releases the keys other
// than modifiers first, then the modifiers, each in reverse press order.
func (fz *fuzzer) retryFreed() map[output.KeyID]bool {
	var ups, mods []output.KeyID
	held := fz.s.Arb.Held()
	for i := len(held) - 1; i >= 0; i-- {
		id := held[i].Press.ID()
		switch {
		case !held[i].Pending:
		case fuzzModifiers[id]:
			mods = append(mods, id)
		default:
			ups = append(ups, id)
		}
	}
	ups = append(ups, mods...)
	n := len(ups)
	if !fz.off && len(fz.plans) > 0 && fz.plans[0] >= 0 {
		n = min(n, fz.plans[0])
	}
	freed := map[output.KeyID]bool{}
	for _, id := range ups[:n] {
		freed[id] = true
	}
	return freed
}

// startedKeys extends the keys that the resend of a Down frees (retryFreed)
// with those of the queued taps that start because of it: a queued tap whose
// main key is free starts before the Down takes its keys, and presses all the
// keys of its chord, the modifiers too. With a hold of 0 it lets go of them
// at once, which frees a modifier that was pending, and the queued tap of that
// key starts as well, whichever of the two is first in the queue (runReady
// goes on while a tap starts); with a hold it keeps them. Either way the key
// may be taken or free when the Down takes it, which syncQueue cannot tell
// (early). It returns a new map.
func (fz *fuzzer) startedKeys(freed map[output.KeyID]bool) map[output.KeyID]bool {
	out := maps.Clone(freed)
	for changed := true; changed; {
		changed = false
		for _, q := range fz.queued {
			if !out[q.Main] {
				continue
			}
			stroke, _ := output.ChordStroke(q.Chord)
			for _, e := range stroke.Down {
				if !out[e.ID()] {
					out[e.ID()] = true
					changed = true
				}
			}
		}
	}
	return out
}

// observeTap handles a Tap that expectTap could not predict, which the
// Arbiter has run. A tap queues, merges into the queued tap of its main key,
// or starts, and only a key in the ledger makes it wait; the model takes
// what the Arbiter did and checks it against the queue and the ledger.
func (fz *fuzzer) observeTap(o output.Owner, sided keys.Chord, main output.KeyID, before output.ArbiterStats) {
	after := fz.s.Arb.Stats()
	queued, merged := after.QueuedTaps-before.QueuedTaps, after.MergedTaps-before.MergedTaps
	got := output.QueuedTaps(fz.s.Arb)
	waiting := slices.ContainsFunc(got, func(q output.QueuedTap) bool { return q.Main == main })
	_, inLedger := fz.held()[main]
	switch {
	case queued+merged > 1:
		fz.t.Fatalf("%s: one tap was queued %d times and merged %d times", fz.desc, queued, merged)
	case queued+merged == 1 && !inLedger:
		fz.t.Fatalf("%s: tap waits behind %+v, which is not in the ledger", fz.desc, main)
	case queued+merged == 1 && !waiting:
		fz.t.Fatalf("%s: tap queued or merged, but no tap waits for %+v: %+v", fz.desc, main, got)
	case queued+merged == 0 && waiting:
		fz.t.Fatalf("%s: tap started while the tap of %+v waits: %+v", fz.desc, main, got)
	}
	if queued == 1 {
		item := output.QueuedTap{Owner: o, Chord: sided, Main: main}
		if len(got) == 0 || got[len(got)-1] != item {
			fz.t.Fatalf("%s: queued tap is not last in %+v", fz.desc, got)
		}
		fz.queued = append(fz.queued, item)
	}
	fz.want.QueuedTaps += queued
	fz.want.MergedTaps += merged
}

// syncQueue takes the queue of the Arbiter after a command, checks that it is
// an ordered subset of the model's and counts the taps that left it for a key
// that Down holds. A Down resends the pending releases and starts the queued
// taps that can start (early) before it takes its keys, and such a tap presses
// its key itself; but an earlier tap that started in the same run may occupy
// the key, and then the tap starts after the Down as a tap on a held key. The
// model cannot tell, and syncQueue returns how many taps may have been that
// one (observeTapsOnHeld).
func (fz *fuzzer) syncQueue(released func(output.Owner) bool, blocked bool, early map[output.KeyID]bool) (maybe int) {
	got := output.QueuedTaps(fz.s.Arb)
	next := 0
	for _, q := range fz.queued {
		if next < len(got) && q == got[next] {
			// A queued tap waits while its main key is in the ledger, held by
			// a tap or pending release (OUT-10), and not held by Down.
			if _, inLedger := fz.held()[q.Main]; !inLedger || fz.heldByDown(q.Main) {
				fz.t.Fatalf("%s: ready queue item remained: %+v", fz.desc, q)
			}
			next++
			continue
		}
		if blocked {
			fz.want.Blocked++
		} else if released == nil || !released(q.Owner) {
			switch {
			case !fz.heldByDown(q.Main):
			case early[q.Main]:
				maybe++
			default:
				fz.want.TapsOnHeld++
			}
		}
	}
	if next != len(got) {
		fz.t.Fatalf("%s: queue %+v is not an ordered subset of model %+v", fz.desc, got, fz.queued)
	}
	fz.queued = got
	return maybe
}

// observeTapsOnHeld takes the count of TapsOnHeld from the Arbiter, which may
// have counted up to maybe more taps than the model did.
func (fz *fuzzer) observeTapsOnHeld(maybe int) {
	got := fz.s.Arb.Stats().TapsOnHeld
	if got < fz.want.TapsOnHeld || got > fz.want.TapsOnHeld+uint64(maybe) {
		fz.t.Fatalf("%s: TapsOnHeld = %d, want %d to %d", fz.desc, got, fz.want.TapsOnHeld, fz.want.TapsOnHeld+uint64(maybe))
	}
	fz.want.TapsOnHeld = got
}

func describe(i int, op string, o output.Owner, c keys.Chord) string {
	s := fmt.Sprintf("step %d: %s", i, op)
	if o.IsValid() {
		s += " " + o.String()
	}
	if c.IsValid() {
		s += " " + c.String()
	}
	return s
}

// check compares the Arbiter with the Recorder and the model.
func (fz *fuzzer) check() {
	t, s := fz.t, fz.s
	held := fz.held()
	pending := 0
	for _, h := range held {
		if h.Pending != (len(h.Owners) == 0) {
			t.Fatalf("%s: %s is pending %v with owners %v", fz.desc, keyName(h.Press), h.Pending, h.Owners)
		}
		if h.Pending {
			pending++
		}
	}
	// A key that is physically down is one the Arbiter knows about.
	phys := map[output.KeyID]bool{}
	for _, e := range s.Out.Down() {
		phys[e.ID()] = true
		if _, ok := held[e.ID()]; !ok {
			t.Fatalf("%s: %s is down but not in Held() %s", fz.desc, keyName(e), heldString(s.Arb.Held()))
		}
	}
	// A key the Arbiter holds is physically down, unless its press failed.
	for id, h := range held {
		if l := fz.last[id]; !h.Pending && !phys[id] && (l.up || l.injected) {
			t.Fatalf("%s: %s is held by %v but physically up", fz.desc, keyName(h.Press), h.Owners)
		}
	}
	// Every hold references all of its keys.
	for _, hk := range fz.holds {
		st, _ := output.ChordStroke(hk.c)
		for _, e := range st.Down {
			if h := held[e.ID()]; !slices.Contains(h.Owners, hk.o) {
				t.Fatalf("%s: %v holds %v but %s has owners %v", fz.desc, hk.o, hk.c, keyName(e), h.Owners)
			}
		}
	}
	// The model does not follow retries, but each one resends releases of a
	// distinct failed Send call.
	got := s.Arb.Stats()
	want := fz.want
	want.Retries, want.PendingReleases = got.Retries, pending
	if got != want || got.Retries < fz.want.Retries || got.Retries > got.SendErrors {
		t.Fatalf("%s: Stats() = %+v, want %+v with Retries from %d up to SendErrors",
			fz.desc, got, want, fz.want.Retries)
	}
	fz.want.Retries = got.Retries
	if pending > 0 {
		now := s.Clock.Now()
		d, ok := s.Arb.NextDeadline()
		if !ok || d <= now || d > now.Add(fuzzRetry) {
			t.Fatalf("%s: %d releases pending at %v, NextDeadline() = %v, %v", fz.desc, pending, now, d, ok)
		}
	}
}

// finish turns failures off and releases everything: with Up of every
// hold and Settle, the normal way, or with ReleaseAll. Either way nothing
// may be left, and a second ReleaseAll sends nothing.
func (fz *fuzzer) finish(viaUp bool) {
	t, s := fz.t, fz.s
	fz.off = true
	if viaUp {
		fz.desc = "finish: Up and Settle"
		for _, hk := range fz.holds {
			if err := s.Arb.Up(hk.o, hk.c); err != nil {
				t.Fatalf("%s: Up(%v, %v): %v", fz.desc, hk.o, hk.c, err)
			}
		}
		s.Settle()
		if errs := s.Errors(); len(errs) > 0 {
			t.Fatalf("%s: Advance errors %q", fz.desc, errs)
		}
	} else {
		fz.desc = "finish: ReleaseAll"
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatalf("%s: %v", fz.desc, err)
		}
	}
	fz.holds = nil
	fz.check()
	fz.expectEmpty()
	calls := s.Out.Calls()
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatalf("%s: second ReleaseAll: %v", fz.desc, err)
	}
	if s.Out.Calls() != calls {
		t.Fatalf("%s: second ReleaseAll sent %s", fz.desc, batches(s.Out, calls))
	}
	s.ClearOutput()
}

// expectEmpty checks that nothing is down, physically or logically, and
// nothing is due.
func (fz *fuzzer) expectEmpty() {
	t, s := fz.t, fz.s
	if held := s.Arb.Held(); len(held) > 0 {
		t.Fatalf("%s: still held: %s", fz.desc, heldString(held))
	}
	if down := s.Out.Down(); len(down) > 0 {
		t.Fatalf("%s: still down: [%s]", fz.desc, eventsString(down))
	}
	if d, ok := s.Arb.NextDeadline(); ok {
		t.Fatalf("%s: NextDeadline() = %v", fz.desc, d)
	}
}
