package output_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The wheel keys of ADR-0062 are momentary: a press turns the wheel one
// notch, a release injects nothing. The Arbiter still sends both (its ledger,
// Repeat and the recorders rely on every press having its release); the
// Sender leaves the silent release out. These tests check both views: the
// Recorder's, which sees every event, and injected, which sees what a real
// Sender would put out.

// injected returns the output a real Sender injects: the Recorder's events
// without the silent ones, as a trace.
func injected(s tsim) string {
	var edges []simtest.Edge
	for _, r := range s.Out.Records() {
		if r.Injected && !r.Event.Silent() {
			edges = append(edges, r.Edge())
		}
	}
	return simtest.FormatTrace(edges)
}

// injectedSince is injected for the records after the first n, which
// recorded returns.
func injectedSince(s tsim, n int) string {
	var edges []simtest.Edge
	for _, r := range s.Out.Records()[n:] {
		if r.Injected && !r.Event.Silent() {
			edges = append(edges, r.Edge())
		}
	}
	return simtest.FormatTrace(edges)
}

// recorded counts the events the Recorder has been offered.
func recorded(s tsim) int { return len(s.Out.Records()) }

// notches counts the wheel presses a real Sender injects.
func notches(s tsim, name string) int {
	n := 0
	for _, r := range s.Out.Records() {
		if r.Injected && !r.Event.Up && r.Event.Key.String() == name {
			n++
		}
	}
	return n
}

func withTiming(minPress, minGap time.Duration) simtest.Option {
	return simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: minPress, MinGap: minGap})
}

func TestWheelTap(t *testing.T) {
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		t.Run(name, func(t *testing.T) {
			s := newSim(t)
			s.tap(ownerA, name, 0)
			s.ExpectOutput("+" + name + "@0 -" + name)
			if got := injected(s); got != "+"+name+"@0" {
				t.Errorf("a real Sender injects %q, want one notch and no release", got)
			}
			if got := s.Out.Calls(); got != 1 {
				t.Errorf("%d Send calls, want 1", got)
			}
		})
	}
}

func TestWheelTapHold(t *testing.T) {
	// A tap with a hold keeps the key referenced for the hold, but the notch
	// goes out with the press.
	s := newSim(t)
	s.tap(ownerA, "WheelUp", 30*ms)
	if got := injected(s); got != "+WheelUp@0" {
		t.Fatalf("injected %q, want the notch at once", got)
	}
	s.Settle()
	s.ExpectOutput("+WheelUp@0 -WheelUp@30")
	if got := notches(s, "WheelUp"); got != 1 {
		t.Errorf("%d notches, want 1", got)
	}
}

// Two holds of one wheel key give two notches, the same owner's or two
// owners'; the release comes with the last.
func TestWheelOverlappingHolds(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "WheelUp")
	s.down(ownerB, "WheelUp")
	s.ExpectOutput("+WheelUp@0 +WheelUp@0")
	s.up(ownerA, "WheelUp")
	s.ExpectOutput("") // B still references the key
	s.up(ownerB, "WheelUp")
	s.ExpectOutput("-WheelUp@0")
	if got := injected(s); got != "+WheelUp@0 +WheelUp@0" {
		t.Errorf("injected %q, want two notches", got)
	}

	// The same owner's chords that share the key: Ctrl+WheelUp and
	// Shift+WheelUp both press the wheel.
	s.ClearOutput()
	s.down(ownerA, "Ctrl+WheelUp")
	s.down(ownerA, "Shift+WheelUp")
	s.ExpectOutput("+LCtrl@0 +WheelUp +LShift +WheelUp")
	s.up(ownerA, "Ctrl+WheelUp")
	s.up(ownerA, "Shift+WheelUp")
	if got := notches(s, "WheelUp"); got != 4 {
		t.Errorf("%d notches in all, want 4", got)
	}
}

// A tap on a wheel key some hold has down turns the wheel too, and a tap
// started while an earlier tap's hold is running does not wait for it.
func TestWheelTapOnHeldAndOnTap(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "WheelDown")
	s.tap(ownerB, "WheelDown", 0)
	s.ExpectOutput("+WheelDown@0 +WheelDown@0")
	s.up(ownerA, "WheelDown")
	s.ExpectOutput("-WheelDown@0")
	if st := s.Arb.Stats(); st.TapsOnHeld != 1 || st.QueuedTaps != 0 {
		t.Errorf("stats %+v, want one tap on a held key and none queued", st)
	}

	s.ClearOutput()
	s.tap(ownerA, "WheelLeft", 40*ms)
	s.Advance(5 * ms)
	s.tap(ownerB, "WheelLeft", 40*ms)
	s.Advance(5 * ms)
	s.tap(ownerA, "WheelLeft", 0)
	if got := notches(s, "WheelLeft"); got != 3 {
		t.Errorf("%d notches after three taps, want 3 (no tap waits for the key)", got)
	}
	if st := s.Arb.Stats(); st.QueuedTaps != 0 || st.MergedTaps != 0 || s.Arb.Queued() != 0 {
		t.Errorf("stats %+v, queued %d: a wheel tap never queues", st, s.Arb.Queued())
	}
	s.Settle()
}

func TestWheelRepeat(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "WheelDown")
	for range 3 {
		s.repeat(ownerA, "WheelDown")
	}
	s.ExpectOutput("+WheelDown@0 +WheelDown +WheelDown +WheelDown")
	s.up(ownerA, "WheelDown")
	s.ExpectOutput("-WheelDown@0")
	if got := notches(s, "WheelDown"); got != 4 {
		t.Errorf("%d notches, want the press and three repeats", got)
	}

	// A chord: each repeat is one more notch of the main key only; Ctrl
	// stays down, and goes up after the last.
	s.ClearOutput()
	s.down(ownerA, "Ctrl+WheelUp")
	s.repeat(ownerA, "Ctrl+WheelUp")
	s.repeat(ownerA, "Ctrl+WheelUp")
	s.ExpectOutput("+LCtrl@0 +WheelUp +WheelUp +WheelUp")
	s.up(ownerA, "Ctrl+WheelUp")
	s.ExpectOutput("-WheelUp -LCtrl")
	if got := injected(s); !strings.HasSuffix(got, "-LCtrl@0") || strings.Contains(got, "-WheelUp") {
		t.Errorf("injected %q: the release of the wheel must not be injected, Ctrl's must", got)
	}
}

// A wheel key keeps no minimum press and no gap between taps. The modifier
// of its chord keeps both.
func TestWheelNoMinimumNoGap(t *testing.T) {
	s := newSim(t, withTiming(20*ms, 50*ms))
	s.tap(ownerA, "WheelUp", 0)
	s.ExpectOutput("+WheelUp@0 -WheelUp")
	s.Advance(1 * ms)
	s.tap(ownerB, "WheelUp", 0)
	s.Advance(1 * ms)
	s.down(ownerA, "WheelUp")
	s.up(ownerA, "WheelUp")
	s.ExpectOutput("+WheelUp@1 -WheelUp +WheelUp@2 -WheelUp")
	if d, ok := s.Arb.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v: a wheel key leaves no deadline", d)
	}
	if st := s.Arb.Stats(); st.QueuedTaps != 0 || st.PendingReleases != 0 {
		t.Errorf("stats %+v, want nothing queued or pending", st)
	}
	if got := notches(s, "WheelUp"); got != 3 {
		t.Errorf("%d notches, want 3", got)
	}

	// Ctrl+WheelUp: the notch is not delayed, Ctrl's release is.
	s.ClearOutput()
	s.down(ownerA, "Ctrl+WheelDown")
	s.Advance(5 * ms)
	s.up(ownerA, "Ctrl+WheelDown")
	s.ExpectOutput("+LCtrl@2 +WheelDown -WheelDown@7")
	if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(22*ms) {
		t.Errorf("NextDeadline = %v, %v; want Ctrl's minimum at 22ms", d, ok)
	}
	s.Advance(15 * ms)
	s.ExpectOutput("-LCtrl@22")
}

// ReleaseAll, ReleaseOwners and the gate keep their bookkeeping, send the
// silent releases, and inject nothing for the wheel keys; the modifiers of a
// wheel chord are released as ever.
func TestWheelReleasePaths(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+WheelUp")
	s.down(ownerB, "WheelDown")
	s.tap(ownerS, "Shift+WheelLeft", 50*ms)
	s.ClearOutput()
	before := recorded(s)
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	// The wheel keys first, the modifiers last, as for any keys.
	s.ExpectOutput("-WheelLeft@0 -WheelDown -WheelUp -LShift -LCtrl")
	if got := injectedSince(s, before); got != "-LShift@0 -LCtrl@0" {
		t.Errorf("injected %q, want only the releases of the two modifiers", got)
	}
	if held := s.Arb.Held(); len(held) != 0 {
		t.Errorf("Held = %v after ReleaseAll", held)
	}
	if n := len(s.Out.Down()); n != 0 {
		t.Errorf("%d keys physically down after ReleaseAll", n)
	}
	s.Settle()

	// One owner's release: the other keeps its wheel key.
	s.ClearOutput()
	s.down(ownerA, "WheelUp")
	s.down(ownerB, "WheelUp")
	s.ClearOutput()
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("")
	if err := s.Arb.ReleaseOwner(ownerB); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-WheelUp")

	// The closed gate drops wheel presses like any others.
	s.ClearOutput()
	s.Arb.SetBlocked(true)
	s.tap(ownerA, "WheelUp", 0)
	s.down(ownerA, "WheelDown")
	s.ExpectOutput("")
	if st := s.Arb.Stats(); st.Blocked != 2 {
		t.Errorf("Blocked = %d, want 2", st.Blocked)
	}
	s.Arb.SetBlocked(false)
}

// Ctrl+WheelUp in one batch: the modifier goes down, the wheel turns, the
// modifier comes up, with nothing between.
func TestWheelChordOrdering(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "Ctrl+Shift+WheelUp", 0)
	s.ExpectOutput("+LCtrl@0 +LShift +WheelUp -WheelUp -LShift -LCtrl")
	if got := injected(s); got != "+LCtrl@0 +LShift@0 +WheelUp@0 -LShift@0 -LCtrl@0" {
		t.Errorf("injected %q, want Ctrl down, Shift down, the notch, Shift up, Ctrl up", got)
	}
	if got := s.Out.Calls(); got != 1 {
		t.Errorf("%d Send calls, want the whole chord in one", got)
	}
	// Every record of the batch is one Send call's.
	for _, r := range s.Out.Records() {
		if r.Batch != 0 || !r.Injected {
			t.Errorf("record %+v not in the one batch", r)
		}
	}
}

func TestWheelInSequence(t *testing.T) {
	s := newSim(t)
	items := []keys.Chord{keys.MustParse("Ctrl+K"), keys.MustParse("WheelDown"), keys.MustParse("Ctrl+WheelUp"), keys.MustParse("Z")}
	if err := s.Arb.TapSequence(ownerA, items, 10*ms, 5*ms); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	want := "+LCtrl@0 +K@0 -K@10 -LCtrl@10 " +
		"+WheelDown@15 -WheelDown@25 " +
		"+LCtrl@30 +WheelUp@30 -WheelUp@40 -LCtrl@40 " +
		"+Z@45 -Z@55"
	s.ExpectOutput(want)
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := injected(s); strings.Contains(got, "-WheelDown") || strings.Contains(got, "-WheelUp") || strings.Count(got, "Wheel") != 2 {
		t.Errorf("injected %q, want the two notches and no release of them", got)
	}
}

// A partial Send: a wheel press that failed is not resent; the releases still
// are, and nothing stays down.
func TestWheelPartialSend(t *testing.T) {
	s := newSim(t)
	s.Out.FailNext(1, nil) // Ctrl goes down, the notch does not
	err := s.Arb.Tap(ownerA, keys.MustParse("Ctrl+WheelUp"), 0)
	if err == nil {
		t.Fatal("Tap returned no error for a partial Send")
	}
	s.ExpectOutput("+LCtrl@0 -WheelUp -LCtrl")
	if n := notches(s, "WheelUp"); n != 0 {
		t.Errorf("%d notches, want none: the press failed", n)
	}
}

// A release that failed twice leaves the wheel key pending like any key, and a
// press of it meanwhile starts afresh and turns the wheel: the pending
// release injects nothing, so there is nothing to take over.
func TestWheelPressWhileReleasePending(t *testing.T) {
	s := newSim(t, withRetry(50*ms))
	s.down(ownerA, "Ctrl+WheelDown")
	s.Out.FailNext(0, nil)
	s.Out.FailNext(0, nil)
	if err := s.Arb.Up(ownerA, keys.MustParse("Ctrl+WheelDown")); err == nil {
		t.Fatal("Up returned no error for a release that failed twice")
	}
	if st := s.Arb.Stats(); st.PendingReleases != 2 {
		t.Fatalf("PendingReleases = %d, want the wheel key and Ctrl", st.PendingReleases)
	}
	// B's tap first resends the pending releases, which fail again; the
	// notch goes out all the same.
	s.Out.FailNext(0, nil)
	before := recorded(s)
	if err := s.Arb.Tap(ownerB, keys.MustParse("WheelDown"), 0); err == nil {
		t.Error("Tap returned no error for the pending releases that failed again")
	}
	if got := injectedSince(s, before); got != "+WheelDown@0" {
		t.Errorf("injected %q, want the notch", got)
	}
	// Ctrl's release is the only one left pending.
	if st := s.Arb.Stats(); st.PendingReleases != 1 {
		t.Errorf("PendingReleases = %d, want only Ctrl's", st.PendingReleases)
	}
	s.Advance(50 * ms)
	s.Settle()
	if st := s.Arb.Stats(); st.PendingReleases != 0 {
		t.Errorf("PendingReleases = %d after the retry", st.PendingReleases)
	}
}

// The event gap (OUT-17) paces the events that inject something; the
// release of a wheel key injects nothing and takes no gap.
func TestWheelEventGap(t *testing.T) {
	opts := simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 50 * ms})

	t.Run("chord", func(t *testing.T) {
		s := newSim(t, opts)
		s.tap(ownerA, "Ctrl+WheelUp", 0)
		s.Settle()
		// The silent release goes with the notch, so Ctrl's release waits one
		// gap, not two.
		s.ExpectOutput("+LCtrl@0 +WheelUp@50 -WheelUp@50 -LCtrl@100")
		if got := s.Out.Calls(); got != 4 {
			t.Errorf("%d Send calls, want one per event", got)
		}
	})

	t.Run("silent release alone", func(t *testing.T) {
		s := newSim(t, opts)
		s.tap(ownerA, "WheelDown", 0)
		s.Advance(10 * ms)
		s.tap(ownerB, "Z", 0)
		s.Settle()
		// The notch at 0, then Z a gap after the notch, not after the release.
		s.ExpectOutput("+WheelDown@0 -WheelDown@0 +Z@50 -Z@100")
	})

	t.Run("excess taps", func(t *testing.T) {
		// Taps that come faster than the gap lets them out wait one at a time
		// and further ones merge, as for any key (OUT-10, OUT-17): the queue
		// stays bounded.
		s := newSim(t, opts)
		for range 4 {
			s.tap(ownerA, "WheelUp", 0)
			s.Advance(10 * ms)
		}
		s.Settle()
		s.ExpectOutput("+WheelUp@0 -WheelUp@0 +WheelUp@50 -WheelUp@50 +WheelUp@100 -WheelUp@100")
		if st := s.Arb.Stats(); st.QueuedTaps != 1 || st.MergedTaps != 1 {
			t.Errorf("stats %+v, want one tap queued and one merged", st)
		}
	})

	t.Run("hold and repeat", func(t *testing.T) {
		s := newSim(t, opts)
		s.down(ownerA, "WheelUp")
		s.repeat(ownerA, "WheelUp")
		s.repeat(ownerA, "WheelUp") // one extra press waits at a time
		s.Advance(60 * ms)
		s.up(ownerA, "WheelUp")
		s.Settle()
		s.ExpectOutput("+WheelUp@0 +WheelUp@50 -WheelUp@60")
		if got := notches(s, "WheelUp"); got != 2 {
			t.Errorf("%d notches, want the press and one repeat", got)
		}
	})

	t.Run("release all", func(t *testing.T) {
		s := newSim(t, opts)
		s.down(ownerA, "Ctrl+WheelUp")
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		// The notch that never went out is dropped with its key; Ctrl, which
		// did go down, is released behind the mask key, as for any key dropped
		// this way (a lone Ctrl must not make a hotkey).
		if got := injected(s); got != "+LCtrl@0 +vk:0xE8@0 -vk:0xE8@0 -LCtrl@0" {
			t.Errorf("injected %q", got)
		}
		s.Settle()
	})
}

// A wheel press on a key some reference holds already is an extra press in the
// event gap's queue, like Repeat's: it must not make the key's own press look
// unsent, or a forced release would drop the key from the ledger while it is
// down and never release it.
func TestWheelEventGapExtraPressDoesNotHideTheOwnPress(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 14 * ms, MinPress: 26 * ms, MinGap: 25 * ms}))
	s.down(ownerA, "WheelUp")        // out at 0
	s.tap(ownerB, "Ctrl+WheelUp", 0) // Ctrl at 14, its notch an extra press behind it
	s.down(ownerS, "Ctrl+WheelUp")   // another notch behind that
	s.Advance(15 * ms)
	s.Advance(7 * ms)
	s.down(ownerB, "WheelUp") // and one more, still queued when everything is released
	s.Advance(13 * ms)
	s.Advance(8 * ms)
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	// The cleanup of the Sim checks that the Recorder has nothing down and the
	// Arbiter holds nothing: the wheel key was sent, so its release is too.
	foundUp := false
	for _, r := range s.Out.Records() {
		foundUp = foundUp || r.Event.Mouse == keys.WheelUp && r.Event.Up
	}
	if !foundUp {
		t.Errorf("the release of WheelUp never reached the Sender: %s", s.Output())
	}
}

// Holds of different wheel keys and keyboard keys together: the ledger keeps
// each by its own KeyID.
func TestWheelKeysAreDistinct(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "WheelUp")
	s.down(ownerA, "WheelDown")
	s.down(ownerA, "WheelLeft")
	s.down(ownerA, "WheelRight")
	s.down(ownerA, "F13")
	held := s.Arb.Held()
	if len(held) != 5 {
		t.Fatalf("Held = %d keys, want 5", len(held))
	}
	ids := map[output.KeyID]bool{}
	for _, h := range held {
		ids[h.Press.ID()] = true
	}
	if len(ids) != 5 {
		t.Errorf("%d distinct KeyIDs among the held keys, want 5", len(ids))
	}
	s.up(ownerA, "WheelDown")
	for _, h := range s.Arb.Held() {
		if h.Press.Mouse == keys.WheelDown {
			t.Errorf("WheelDown still held after its Up")
		}
	}
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

// Whatever the commands, every press of a wheel key turns the wheel exactly
// once, whoever else references the key, with or without a minimum press
// and gap; nothing is left held; and the release of a wheel key is never
// among what a real Sender injects. The model counts the notches the
// commands ask for: a Down of a chord its owner does not hold, a tap, and a
// Repeat of a hold. With an event gap taps and holds that come faster than the
// gap lets them out are merged or dropped as for any key, so there the model is
// an upper bound, and what matters is that nothing stays held.
func TestWheelRandomCommands(t *testing.T) {
	specs := []string{"WheelUp", "Ctrl+WheelUp", "WheelDown", "Shift+WheelLeft", "Alt+WheelRight"}
	owners := []output.Owner{ownerA, ownerB, ownerS}
	for seed := range 100 {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), 7))
			paced := seed >= 60
			var s tsim
			switch {
			case paced:
				s = newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{
					MinPress: time.Duration(rng.IntN(30)) * ms, MinGap: time.Duration(rng.IntN(60)) * ms,
					EventGap: time.Duration(1+rng.IntN(40)) * ms,
				}))
			case seed%3 == 0:
				s = newSim(t)
			default:
				s = newSim(t, withTiming(time.Duration(rng.IntN(30))*ms, time.Duration(rng.IntN(60))*ms))
			}
			type hold struct {
				o    output.Owner
				spec string
			}
			holds := map[hold]bool{}
			want := 0
			for range 200 {
				o, spec := owners[rng.IntN(len(owners))], specs[rng.IntN(len(specs))]
				switch rng.IntN(8) {
				case 0, 1:
					s.down(o, spec)
					if !holds[hold{o, spec}] {
						want++
					}
					holds[hold{o, spec}] = true
				case 2:
					s.up(o, spec)
					delete(holds, hold{o, spec})
				case 3, 4:
					s.tap(o, spec, time.Duration(rng.IntN(3))*15*ms)
					want++
				case 5:
					s.repeat(o, spec)
					if holds[hold{o, spec}] {
						want++
					}
				case 6:
					s.Advance(time.Duration(rng.IntN(25)) * ms)
				case 7:
					if rng.IntN(4) == 0 {
						if err := s.Arb.ReleaseOwner(o); err != nil {
							t.Fatal(err)
						}
						for h := range holds {
							if h.o == o {
								delete(holds, h)
							}
						}
					}
				}
			}
			if err := s.Arb.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			s.Settle()
			got := 0
			for _, r := range s.Out.Records() {
				if r.Event.Mouse == keys.MouseNone || r.Event.Up {
					continue
				}
				got++
			}
			if got != want && (!paced || got > want) {
				t.Errorf("%d notches, want %d (paced %v)", got, want, paced)
			}
			for _, r := range s.Out.Records() {
				if r.Event.Up && !r.Event.Silent() && r.Event.Mouse != keys.MouseNone {
					t.Errorf("release %v is not silent", r.Event)
				}
			}
			if errs := s.Errors(); len(errs) > 0 {
				t.Errorf("errors: %v", errs)
			}
		})
	}
}

// The Tap function sends a wheel chord in one batch like any chord, and its
// retry of a partial batch covers the silent release too, so nothing stays
// down in the Recorder.
func TestTapFunctionWheel(t *testing.T) {
	st, err := output.ChordStroke(keys.MustParse("Ctrl+WheelDown"))
	if err != nil {
		t.Fatal(err)
	}
	for _, injected := range []int{-1, 0, 1, 2, 3} { // -1: the batch goes out whole
		rec := simtest.NewRecorder(clock.NewFake())
		if injected >= 0 {
			rec.FailNext(injected, nil)
		}
		err := output.Tap(context.Background(), rec, st, 0)
		if (injected >= 0) != (err != nil) {
			t.Errorf("injected %d: Tap = %v", injected, err)
		}
		if down := rec.Down(); len(down) != 0 {
			t.Errorf("injected %d: %v still down", injected, down)
		}
		if injected < 0 && rec.Calls() != 1 {
			t.Errorf("%d Send calls for one chord, want 1", rec.Calls())
		}
	}
}
