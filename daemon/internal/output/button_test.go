package output_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The mouse buttons of ADR-0062 (second batch) are held like keys: unlike the
// wheel keys they go down with their first reference and up with their last,
// keep the minimum press, the gap between taps and the event gap, and every
// release is injected. What they never get is a second press while they are
// down, which an application would take for a double click: no typematic
// repeat, and no extra press for a tap of a button some hold has down. These
// tests check that, and that no path leaves a button down.

var buttonNames = []string{"MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2"}

// buttonDowns counts the injected presses of the button called name.
func buttonDowns(s tsim, name string) int {
	n := 0
	for _, r := range s.Out.Records() {
		if r.Injected && !r.Event.Up && r.Event.Key.String() == name {
			n++
		}
	}
	return n
}

// checkNoDoubleDown fails the test when the records show a button pressed
// while the Recorder had it down already, which the Recorder's own state
// would hide ("a repeated press keeps the key's place").
func checkNoDoubleDown(t *testing.T, s tsim) {
	t.Helper()
	down := map[output.KeyID]bool{}
	for _, r := range s.Out.Records() {
		if !r.Injected || !r.Event.Mouse.IsButton() {
			continue
		}
		id := r.Event.ID()
		switch {
		case r.Event.Up:
			delete(down, id)
		case down[id]:
			t.Errorf("%v pressed at %v while it was down: %s", r.Event.Key, r.T, s.Output())
			return
		default:
			down[id] = true
		}
	}
}

func TestButtonDownUp(t *testing.T) {
	for _, name := range buttonNames {
		t.Run(name, func(t *testing.T) {
			s := newSim(t)
			s.down(ownerA, name)
			s.ExpectOutput("+" + name + "@0")
			s.expectHeld(name + "(A)")
			if d := s.Out.Down(); len(d) != 1 || d[0].Mouse == keys.MouseNone {
				t.Errorf("physically down: %v, want the button", d)
			}
			s.Advance(30 * ms)
			s.up(ownerA, name)
			s.ExpectOutput("-" + name + "@30")
			s.expectHeld("")
			// Both events are injected: a button's release is not silent.
			for _, r := range s.Out.Records() {
				if r.Event.Silent() {
					t.Errorf("%v is silent", r.Event)
				}
			}
			if got := injected(s); got != "+"+name+"@0 -"+name+"@30" {
				t.Errorf("a real Sender injects %q, want the press and the release", got)
			}
		})
	}

	// The Windows names are the same keys.
	s := newSim(t)
	s.down(ownerA, "LButton")
	s.expectHeld("MouseLeft(A)")
	s.up(ownerA, "mouseleft")
	s.ExpectOutput("+MouseLeft@0 -MouseLeft")
}

// A button is referenced like a key: two holds press it once, and it goes up
// with the last one. The wheel keys, by contrast, turn once per hold.
func TestButtonSharedHold(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "MouseLeft")
	s.down(ownerB, "MouseLeft")
	s.ExpectOutput("+MouseLeft@0")
	s.expectHeld("MouseLeft(A B)")
	s.up(ownerA, "MouseLeft")
	s.ExpectOutput("")
	s.up(ownerB, "MouseLeft")
	s.ExpectOutput("-MouseLeft@0")

	// The same owner's chords that share the button.
	s.ClearOutput()
	s.down(ownerA, "Ctrl+MouseRight")
	s.down(ownerA, "Shift+MouseRight")
	s.ExpectOutput("+LCtrl@0 +MouseRight +LShift")
	s.up(ownerA, "Ctrl+MouseRight")
	s.ExpectOutput("-LCtrl")
	s.up(ownerA, "Shift+MouseRight")
	s.ExpectOutput("-MouseRight -LShift")
	checkNoDoubleDown(t, s)
}

// A click is a tap: down, the minimum press, up; the next click of the same
// button waits for the gap between taps, as any key's does.
func TestButtonTap(t *testing.T) {
	for _, name := range buttonNames {
		t.Run(name, func(t *testing.T) {
			s := newSim(t)
			s.tap(ownerA, name, 0)
			s.ExpectOutput("+" + name + "@0 -" + name)
			if got := s.Out.Calls(); got != 1 {
				t.Errorf("%d Send calls, want 1", got)
			}
			s.tap(ownerA, name, 30*ms)
			s.Settle()
			s.ExpectOutput("+" + name + "@0 -" + name + "@30")
			if got := injected(s); strings.Count(got, "-"+name) != 2 {
				t.Errorf("injected %q, want both releases", got)
			}
		})
	}

	s := newSim(t, withTiming(20*ms, 50*ms))
	s.tap(ownerA, "MouseLeft", 0)
	s.Advance(5 * ms)
	s.tap(ownerB, "MouseLeft", 0) // waits for the first click and the gap
	s.expectDeadline(20 * ms)
	s.Settle()
	s.ExpectOutput("+MouseLeft@0 -MouseLeft@20 +MouseLeft@70 -MouseLeft@90")
	checkNoDoubleDown(t, s)
	if st := s.Arb.Stats(); st.QueuedTaps != 1 {
		t.Errorf("stats %+v, want the second click queued", st)
	}
}

// Ctrl+MouseLeft clicks with Ctrl down around the click, in one batch.
func TestButtonChordOrdering(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "Ctrl+Shift+MouseLeft", 0)
	s.ExpectOutput("+LCtrl@0 +LShift +MouseLeft -MouseLeft -LShift -LCtrl")
	if got := s.Out.Calls(); got != 1 {
		t.Errorf("%d Send calls, want the whole chord in one", got)
	}
	if got := injected(s); got != "+LCtrl@0 +LShift@0 +MouseLeft@0 -MouseLeft@0 -LShift@0 -LCtrl@0" {
		t.Errorf("injected %q, want Ctrl and Shift down, the click, Shift and Ctrl up", got)
	}
}

func TestButtonInSequence(t *testing.T) {
	s := newSim(t)
	items := []keys.Chord{keys.MustParse("Ctrl+K"), keys.MustParse("MouseLeft"), keys.MustParse("Shift+RButton"), keys.MustParse("XButton2")}
	if err := s.Arb.TapSequence(ownerA, items, 10*ms, 5*ms); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	s.ExpectOutput("+LCtrl@0 +K@0 -K@10 -LCtrl@10 " +
		"+MouseLeft@15 -MouseLeft@25 " +
		"+LShift@30 +MouseRight@30 -MouseRight@40 -LShift@40 " +
		"+MouseX2@45 -MouseX2@55")
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}
}

// Repeat is typematic: another press of the main key while it is down. A
// button is never pressed again, so Repeat of a held button chord injects
// nothing (profile validation rejects such a binding, so this is defense in
// depth). The modifiers of the chord are not pressed again either.
func TestButtonRepeatIsNoop(t *testing.T) {
	for _, name := range buttonNames {
		t.Run(name, func(t *testing.T) {
			s := newSim(t)
			s.down(ownerA, name)
			for range 3 {
				s.Advance(5 * ms)
				s.repeat(ownerA, name)
			}
			s.ExpectOutput("+" + name + "@0")
			if n := s.Out.Calls(); n != 1 {
				t.Errorf("%d Send calls, want only the press", n)
			}
			s.up(ownerA, name)
			s.ExpectOutput("-" + name + "@15")
			s.repeat(ownerA, name) // no longer held
			s.ExpectOutput("")
			if st := s.Arb.Stats(); st.InvalidRepeats != 1 {
				t.Errorf("stats %+v, want one invalid repeat, the one after the release", st)
			}
			checkNoDoubleDown(t, s)
		})
	}

	// A chord: neither Ctrl nor the button is pressed again.
	s := newSim(t)
	s.down(ownerA, "Ctrl+MouseLeft")
	s.repeat(ownerA, "Ctrl+MouseLeft")
	s.repeat(ownerA, "LCtrl+LButton")
	s.ExpectOutput("+LCtrl@0 +MouseLeft")
	s.up(ownerA, "Ctrl+MouseLeft")
	s.ExpectOutput("-MouseLeft -LCtrl")

	// The same through the event gap: nothing is queued either.
	g := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 20 * ms}))
	g.down(ownerA, "Shift+MouseRight")
	g.Advance(25 * ms)
	g.repeat(ownerA, "Shift+MouseRight")
	g.repeat(ownerA, "Shift+MouseRight")
	g.Advance(100 * ms)
	g.ExpectOutput("+LShift@0 +MouseRight@20")
	g.up(ownerA, "Shift+MouseRight")
	g.Settle()
	g.ExpectOutput("-MouseRight@125 -LShift@145")
	checkNoDoubleDown(t, g)
	if d, ok := g.Arb.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after the repeats, want none", d)
	}
}

// A tap of a button some hold has down is no second press either: the tap
// does nothing for it, and is not queued behind the hold. A key gets an extra
// press in that case (TestArbiterTapOnHeld).
func TestButtonTapOnHeld(t *testing.T) {
	for _, hold := range []time.Duration{0, 20 * ms} {
		s := newSim(t)
		var completion []output.TapCompletion
		s.down(ownerB, "MouseLeft")
		if err := s.Arb.TapWithCompletion(ownerA, keys.MustParse("MouseLeft"), hold, func(c output.TapCompletion) { completion = append(completion, c) }); err != nil {
			t.Fatal(err)
		}
		s.Advance(30 * ms)
		s.ExpectOutput("+MouseLeft@0")
		s.expectHeld("MouseLeft(B)")
		s.expectDeadline(-1)
		s.expectStats(output.ArbiterStats{TapsOnHeld: 1})
		if len(completion) != 1 || completion[0].Dropped || completion[0].Canceled {
			t.Errorf("hold %v: tap completion %+v, want one normal completion", hold, completion)
		}
		s.up(ownerB, "MouseLeft")
		s.ExpectOutput("-MouseLeft@30")
		checkNoDoubleDown(t, s)
	}

	// With a modifier the whole tap does nothing: the button gets no second
	// press, and the modifier is not pressed around no click either (a lone Shift
	// toggles the Chinese IME). The tap still completes and counts.
	for _, hold := range []time.Duration{0, 20 * ms} {
		s := newSim(t)
		var completion []output.TapCompletion
		s.down(ownerB, "MouseRight")
		if err := s.Arb.TapWithCompletion(ownerA, keys.MustParse("Ctrl+Shift+MouseRight"), hold, func(c output.TapCompletion) { completion = append(completion, c) }); err != nil {
			t.Fatal(err)
		}
		s.Advance(30 * ms)
		s.ExpectOutput("+MouseRight@0")
		s.expectHeld("MouseRight(B)")
		s.expectDeadline(-1)
		s.expectStats(output.ArbiterStats{TapsOnHeld: 1})
		if len(completion) != 1 || completion[0].Dropped || completion[0].Canceled {
			t.Errorf("hold %v: tap completion %+v, want one normal completion", hold, completion)
		}
		s.up(ownerB, "MouseRight")
		s.ExpectOutput("-MouseRight@30")
		checkNoDoubleDown(t, s)
	}

	// The modifiers of the held chord itself are left alone as well: another
	// owner holds Shift+MouseLeft, and a tap of the same chord adds nothing.
	h := newSim(t)
	h.down(ownerB, "Shift+MouseLeft")
	h.tap(ownerA, "Shift+MouseLeft", 20*ms)
	h.tap(ownerA, "Ctrl+MouseLeft", 0)
	h.Advance(30 * ms)
	h.ExpectOutput("+LShift@0 +MouseLeft@0")
	h.up(ownerB, "Shift+MouseLeft")
	h.ExpectOutput("-MouseLeft@30 -LShift@30")
	checkNoDoubleDown(t, h)

	// A tap of a chord with a modifier on a button that is not held goes out as
	// always: the modifier, the click, the modifier's release.
	f := newSim(t)
	f.tap(ownerA, "Ctrl+MouseRight", 20*ms)
	f.Advance(20 * ms)
	f.ExpectOutput("+LCtrl@0 +MouseRight@0 -MouseRight@20 -LCtrl@20")
	checkNoDoubleDown(t, f)

	// With an event gap the tap queues nothing for the held button.
	g := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 20 * ms}))
	g.down(ownerB, "MouseMiddle")
	g.tap(ownerA, "MouseMiddle", 0)
	g.tap(ownerA, "MouseMiddle", 0)
	g.tap(ownerA, "Alt+MouseMiddle", 0)
	g.Advance(100 * ms)
	g.up(ownerB, "MouseMiddle")
	g.Settle()
	g.ExpectOutput("+MouseMiddle@0 -MouseMiddle@100")
	checkNoDoubleDown(t, g)
}

// A tap repeat, repeated clicks, is the supported way to click again and
// again: each RepeatTap starts a click when the button is free and waits when
// it is not, so the button is never pressed twice without a release.
func TestButtonRepeatTapClicks(t *testing.T) {
	s := newSim(t, withTiming(10*ms, 10*ms))
	for range 12 {
		if err := s.Arb.RepeatTap(ownerA, keys.MustParse("MouseLeft"), 4*ms); err != nil {
			t.Fatal(err)
		}
		s.Advance(7 * ms)
	}
	if err := s.Arb.StopRepeat(ownerA, keys.MustParse("MouseLeft")); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	checkNoDoubleDown(t, s)
	clicks := buttonDowns(s, "MouseLeft")
	if clicks < 3 {
		t.Fatalf("%d clicks from 12 repeat requests, want several: %s", clicks, s.Output())
	}
	// Every click is a release behind a press, and the gap between taps holds
	// from each release to the next press.
	var last time.Duration
	for _, r := range s.Out.Records() {
		if !r.Injected {
			continue
		}
		at := time.Duration(r.T)
		switch {
		case !r.Event.Up && last != 0 && at-last < 10*ms:
			t.Errorf("click at %v only %v after the last release: %s", at, at-last, s.Output())
		case r.Event.Up:
			last = at
		}
	}
}

// ReleaseAll, ReleaseOwner and the closed gate: every held button is
// released by the same paths as a key, and the modifiers of its chord last.
func TestButtonReleasePaths(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "MouseLeft")
	s.down(ownerB, "Ctrl+MouseRight")
	s.tap(ownerS, "Shift+MouseMiddle", 50*ms)
	s.down(ownerA, "XButton1")
	s.ClearOutput()
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	// The buttons first, the modifiers last, each in reverse press order.
	s.ExpectOutput("-MouseX1@0 -MouseMiddle -MouseRight -MouseLeft -LShift -LCtrl")
	if held := s.Arb.Held(); len(held) != 0 {
		t.Errorf("Held = %v after ReleaseAll", held)
	}
	if d := s.Out.Down(); len(d) != 0 {
		t.Errorf("%v physically down after ReleaseAll", d)
	}
	s.Settle()
	if got := s.Output(); strings.Contains(got, "+MouseLeft@50") {
		t.Errorf("the tap's own end sent more: %s", got)
	}

	// One owner's release: the other keeps its button down.
	s.ClearOutput()
	s.down(ownerA, "MouseX2")
	s.down(ownerB, "MouseX2")
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+MouseX2")
	s.expectHeld("MouseX2(B)")
	if err := s.Arb.ReleaseOwner(ownerB); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-MouseX2")

	// The closed gate presses no button and releases none that is down; the
	// release comes with ReleaseAll, as Loop.BlockFor does it.
	s.ClearOutput()
	s.down(ownerA, "MouseLeft")
	s.Arb.SetBlocked(true)
	s.tap(ownerA, "MouseRight", 0)
	s.down(ownerB, "MouseMiddle")
	s.repeat(ownerA, "MouseLeft")
	s.ExpectOutput("+MouseLeft")
	if st := s.Arb.Stats(); st.Blocked != 3 {
		t.Errorf("Blocked = %d, want 3", st.Blocked)
	}
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-MouseLeft")
	s.Arb.SetBlocked(false)
}

// A press that failed still counts as down, so its release goes out, and one
// that failed to release is pending and resent, for a button as for a key.
func TestButtonSendFailures(t *testing.T) {
	s := newSim(t, withRetry(50*ms))
	s.Out.FailNext(1, nil) // Ctrl goes down, the button does not
	if err := s.Arb.Tap(ownerA, keys.MustParse("Ctrl+MouseLeft"), 0); err == nil {
		t.Fatal("Tap returned no error for a partial Send")
	}
	s.ExpectOutput("+LCtrl@0 -MouseLeft -LCtrl")
	if n := buttonDowns(s, "MouseLeft"); n != 0 {
		t.Errorf("%d presses of the button, want none: the press failed", n)
	}

	// A release that fails twice leaves the button pending; the retry sends it.
	s.ClearOutput()
	s.down(ownerA, "MouseRight")
	s.Out.FailNext(0, nil)
	s.Out.FailNext(0, nil)
	if err := s.Arb.Up(ownerA, keys.MustParse("MouseRight")); err == nil {
		t.Fatal("Up returned no error for a release that failed twice")
	}
	if st := s.Arb.Stats(); st.PendingReleases != 1 {
		t.Fatalf("PendingReleases = %d, want the button", st.PendingReleases)
	}
	if d := s.Out.Down(); len(d) != 1 {
		t.Fatalf("physically down: %v, want the button", d)
	}
	s.Advance(50 * ms)
	s.ExpectOutput("+MouseRight -MouseRight@50")
	if st := s.Arb.Stats(); st.PendingReleases != 0 {
		t.Errorf("PendingReleases = %d after the retry", st.PendingReleases)
	}

	// A press while that release is pending takes the button over, with no
	// second press, as a key is.
	s.ClearOutput()
	s.down(ownerA, "MouseLeft")
	s.Out.FailNext(0, nil)
	s.Out.FailNext(0, nil)
	_ = s.Arb.Up(ownerA, keys.MustParse("MouseLeft"))
	s.down(ownerB, "MouseLeft")
	s.expectHeld("MouseLeft(B)")
	s.up(ownerB, "MouseLeft")
	s.Settle()
	checkNoDoubleDown(t, s)
}

// The event gap paces buttons like keys: every event goes out alone, the gap
// apart, a click's release too. A forced release drops the press that never
// went out and releases only what did.
func TestButtonEventGap(t *testing.T) {
	opts := simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 50 * ms})

	t.Run("chord", func(t *testing.T) {
		s := newSim(t, opts)
		s.tap(ownerA, "Ctrl+MouseLeft", 0)
		s.Settle()
		s.ExpectOutput("+LCtrl@0 +MouseLeft@50 -MouseLeft@100 -LCtrl@150")
		if got := s.Out.Calls(); got != 4 {
			t.Errorf("%d Send calls, want one per event", got)
		}
	})

	t.Run("release all", func(t *testing.T) {
		s := newSim(t, opts)
		s.down(ownerA, "Ctrl+MouseLeft")
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		// The press that never went out is dropped with its key; Ctrl, which
		// did go down, is released behind the mask key (a lone Ctrl must not
		// make a hotkey), and no release of the button is injected.
		if got := injected(s); got != "+LCtrl@0 +vk:0xE8@0 -vk:0xE8@0 -LCtrl@0" {
			t.Errorf("injected %q", got)
		}
		s.Settle()
	})

	t.Run("release all with the button down", func(t *testing.T) {
		s := newSim(t, opts)
		s.down(ownerA, "MouseLeft")
		s.down(ownerB, "MouseRight")
		s.Advance(10 * ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		// Only the left button went out; the right one's press was queued.
		s.Settle()
		s.ExpectOutput("+MouseLeft@0 -MouseLeft@10")
	})

	t.Run("excess clicks merge", func(t *testing.T) {
		s := newSim(t, opts)
		for range 4 {
			s.tap(ownerA, "MouseLeft", 0)
			s.Advance(10 * ms)
		}
		s.Settle()
		checkNoDoubleDown(t, s)
		if st := s.Arb.Stats(); st.QueuedTaps != 1 || st.MergedTaps != 2 {
			t.Errorf("stats %+v, want one click queued and two merged into it", st)
		}
	})
}

// Each button has its own KeyID: distinct from the others, from the wheel
// keys, from every keyboard key and from the zero KeyID.
func TestButtonKeyIDs(t *testing.T) {
	seen := map[output.KeyID]string{{}: "the zero KeyID"}
	for _, k := range keys.Table() {
		e, err := output.KeyEventFor(k, false)
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := k.Mouse(); ok {
			if e.Mouse != m || e.ID().Mouse != m || e.ID().String() != m.String() {
				t.Errorf("%s: event %+v, ID %v", k, e, e.ID())
			}
			if other, dup := seen[e.ID()]; dup {
				t.Errorf("%s has the KeyID of %s", k, other)
			}
			seen[e.ID()] = k.String()
		}
		if e.Up || e.Silent() {
			t.Errorf("%s: the press is %+v", k, e)
		}
	}
	for _, name := range buttonNames {
		press, err := output.KeyEventFor(keys.MustParse(name).Key(), false)
		if err != nil {
			t.Fatal(err)
		}
		release := press
		release.Up = true
		if press.Silent() || release.Silent() {
			t.Errorf("%s: a button event is never silent", name)
		}
		if press.ID() != release.ID() {
			t.Errorf("%s: press and release are different keys", name)
		}
	}
	// A different alias is the same key.
	a, _ := output.KeyEventFor(keys.MustParse("LButton").Key(), false)
	b, _ := output.KeyEventFor(keys.MustParse("MouseLeft").Key(), false)
	if a.ID() != b.ID() {
		t.Errorf("LButton %v and MouseLeft %v are different keys", a.ID(), b.ID())
	}
}

// Whatever the commands, a button is never pressed while it is down, whoever
// else references it, with or without a minimum press, a gap or an event gap;
// no Repeat adds a press; and nothing is left held. Failed releases are not
// part of this model (TestButtonSendFailures).
func TestButtonRandomCommands(t *testing.T) {
	specs := []string{"MouseLeft", "Ctrl+MouseLeft", "MouseRight", "Shift+MouseRight", "Alt+MouseMiddle", "XButton1", "MouseX2", "WheelUp"}
	owners := []output.Owner{ownerA, ownerB, ownerS}
	for seed := range 120 {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), 11))
			var s tsim
			switch {
			case seed >= 80:
				s = newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{
					MinPress: time.Duration(rng.IntN(30)) * ms, MinGap: time.Duration(rng.IntN(60)) * ms,
					EventGap: time.Duration(1+rng.IntN(40)) * ms,
				}))
			case seed%3 == 0:
				s = newSim(t)
			default:
				s = newSim(t, withTiming(time.Duration(rng.IntN(30))*ms, time.Duration(rng.IntN(60))*ms))
			}
			for range 200 {
				o, spec := owners[rng.IntN(len(owners))], specs[rng.IntN(len(specs))]
				switch rng.IntN(10) {
				case 0, 1:
					s.down(o, spec)
				case 2, 3:
					s.up(o, spec)
				case 4, 5:
					s.tap(o, spec, time.Duration(rng.IntN(3))*15*ms)
				case 6:
					s.repeat(o, spec)
				case 7:
					if err := s.Arb.RepeatTap(o, keys.MustParse(spec), time.Duration(rng.IntN(3))*15*ms); err != nil {
						t.Fatal(err)
					}
				case 8:
					s.Advance(time.Duration(rng.IntN(25)) * ms)
				case 9:
					if rng.IntN(4) == 0 {
						if err := s.Arb.ReleaseOwner(o); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err := s.Arb.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			s.Settle()
			// The wheel key in the specs is momentary and exempt from the rule:
			// checkNoDoubleDown looks at the buttons only.
			checkNoDoubleDown(t, s)
			for _, r := range s.Out.Records() {
				if r.Event.Mouse.IsButton() && r.Event.Silent() {
					t.Errorf("release %v of a button is silent", r.Event)
				}
			}
			for _, e := range s.Out.Down() {
				t.Errorf("%v still down", e)
			}
			if errs := s.Errors(); len(errs) > 0 {
				t.Errorf("errors: %v", errs)
			}
		})
	}
}
