package slot_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// westE is a binding of the other slot in the pause tests.
const westE = `"west": {"press": {"type": "keys", "keys": "E", "mode": "hold"}}`

// TestSetPaused: pausing lets go of the slot's own keys at once, the tap
// in flight included, and of no other slot's; presses then send nothing;
// pausing again decides and sends nothing. Resuming sends nothing, and the
// controls held across it stay canceled until they are released.
func TestSetPaused(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, southTap, ltAlt)
	other := h.newSlot(t, 1, 1, compile(t, westE))
	if h.s.Paused() {
		t.Fatal("Paused() = true for a new slot")
	}
	h.replay(t, "+east@0 lefttrigger=0.5@0")
	other.Handle(button("west", true))
	h.at(t, 10)
	h.s.Handle(button("south", true)) // the tap holds Ctrl+Z until 30 ms
	h.ExpectOutput("+Space@0 +LAlt@0 +E@0 +LCtrl@10 +Z@10")

	h.at(t, 15)
	if err := h.s.SetPaused(true); err != nil {
		t.Fatalf("SetPaused(true) = %v", err)
	}
	h.ExpectOutput("-Z@15 -Space@15 -LCtrl@15 -LAlt@15")
	if !h.s.Paused() {
		t.Error("Paused() = false after SetPaused(true)")
	}
	if got, want := owners(h.Arb), []string{"1/1/decl/1"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q after the pause, want %q: only slot 1's E", got, want)
	}
	if d, ok := h.s.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after the pause, want none: the tap ended", d)
	}

	// Presses send nothing, bound or not; the trigger, still pulled, is
	// canceled until it falls to the release threshold, then presses
	// nothing either.
	calls := h.Out.Calls()
	h.replay(t, "+west@20 -east@30 +east@40 lefttrigger=1@50 lefttrigger=0.2@60 lefttrigger=0.6@70 -south@80 +south@90")
	h.ExpectOutput("")
	for _, on := range []bool{true, false} {
		if err := h.s.SetPaused(on); err != nil {
			t.Fatalf("SetPaused(%v) = %v", on, err)
		}
	}
	if n := h.Out.Calls(); n != calls {
		t.Errorf("%d Send calls while paused, want %d", n, calls)
	}
	if h.s.Paused() {
		t.Error("Paused() = true after SetPaused(false)")
	}

	// Resumed: the controls held across it do nothing until released.
	h.replay(t, "-west@100 -east@110 lefttrigger=1@120 lefttrigger=0.2@130 -south@140")
	h.ExpectOutput("")
	h.replay(t, "+east@150 lefttrigger=0.5@160 -east@170 lefttrigger=0@180")
	h.ExpectOutput("+Space@150 +LAlt@160 -Space@170 -LAlt@180")
	other.Handle(button("west", false))
	h.ExpectOutput("-E@180")
	h.expectDecisions(t,
		"@0 east down dispatch", "@0 lt down dispatch", "@0 west down dispatch", // west: slot 1
		"@10 south down dispatch",
		"@15 south none cancel", "@15 east none cancel", "@15 lt none cancel",
		"@20 west down paused", "@30 east up canceled", "@40 east down paused",
		"@60 lt up canceled", "@70 lt down paused", "@80 south up canceled", "@90 south down paused",
		"@100 west up canceled", "@110 east up canceled", "@130 lt up canceled", "@140 south up canceled",
		"@150 east down dispatch", "@160 lt down dispatch", "@170 east up release", "@180 lt up release",
		"@180 west up release")
}

// TestSetPausedSendFailures: a pause whose release fails returns the
// error; the release is retried until it goes out, and the slot stays
// paused.
func TestSetPausedSendFailures(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl)
	h.replay(t, "+east@0 +lb@10")
	h.at(t, 50)
	// The batch and its immediate resend fail, and so does the first retry.
	h.Out.FailNext(1, nil)
	h.Out.FailNext(0, nil)
	h.Out.FailNext(0, nil)
	if err := h.s.SetPaused(true); !errors.Is(err, simtest.ErrBlocked) {
		t.Fatalf("SetPaused(true) = %v, want ErrBlocked", err)
	}
	h.ExpectOutput("+Space@0 +LCtrl@10 -Space@50")
	if held := h.Arb.Held(); len(held) != 1 || !held[0].Pending {
		t.Fatalf("Held = %+v, want LCtrl pending release", held)
	}
	// The press sends nothing; the retry at 150 ms fails, the one at
	// 250 ms goes out.
	h.replay(t, "+south@60")
	h.ExpectOutput("-LCtrl@250")
	if errs := h.s.Errors(); len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) {
		t.Errorf("Errors = %v, want the failed retry", errs)
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if !h.s.Paused() {
		t.Error("Paused() = false after a pause whose release failed")
	}
	h.replay(t, "-east@300 -lb@300 -south@300")
	h.ExpectOutput("")
	h.expectDecisions(t,
		"@0 east down dispatch", "@10 leftshoulder down dispatch",
		"@50 east none cancel", "@50 leftshoulder none cancel", "@60 south down paused",
		"@300 east up canceled", "@300 leftshoulder up canceled", "@300 south up canceled")
}

// TestPauseKeptAcross: ReleaseAll, Reset, Reload and a removed device
// leave a paused slot paused.
func TestPauseKeptAcross(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	if err := h.s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		f    func() error
	}{
		{"ReleaseAll", h.s.ReleaseAll},
		{"Reset", func() error { h.s.Reset(); return nil }},
		{"Reload", func() error { return h.s.Reload(compile(t, eastShift), h.s.Gen()+1) }},
		{"DeviceRemoved", func() error {
			h.replay(t, "+east@0")
			h.s.Handle(input.Event{Kind: input.DeviceRemoved})
			return nil
		}},
	} {
		if err := tc.f(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !h.s.Paused() {
			t.Errorf("Paused() = false after %s", tc.name)
		}
	}
	h.replay(t, "+east@10 -east@20")
	h.ExpectOutput("")
	h.expectDecisions(t, "@0 east down paused", "@10 east down paused", "@20 east up canceled")
}

// TestConfigPaused: a slot made with Config.Paused starts paused, and New
// sends and decides nothing.
func TestConfigPaused(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	s, err := slot.New(slot.Config{
		Gen: 1, Program: compile(t, eastHold, ltAlt), Arbiter: h.Arb, Clock: h.Clock, Paused: true,
		Trace: func(d engine.Decision) { h.decs = append(h.decs, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	if !s.Paused() {
		t.Error("Paused() = false with Config.Paused")
	}
	if n := h.Out.Calls(); n != 0 || len(h.decs) != 0 {
		t.Errorf("New sent %d batches and made %d decisions, want none", n, len(h.decs))
	}
	h.replay(t, "+east@0 lefttrigger=1@10")
	h.ExpectOutput("")
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	h.replay(t, "-east@20 +east@30 lefttrigger=0@40 lefttrigger=1@50 -east@60 lefttrigger=0@70")
	h.ExpectOutput("+Space@30 +LAlt@50 -Space@60 -LAlt@70")
	h.expectDecisions(t,
		"@0 east down paused", "@10 lt down paused",
		"@20 east up canceled", "@30 east down dispatch", "@40 lt up canceled", "@50 lt down dispatch",
		"@60 east up release", "@70 lt up release")
	if errs := s.Errors(); len(errs) > 0 {
		t.Errorf("Errors = %v, want none", errs)
	}
}

// edge hands e to the loop and waits until the loop has handled it.
func (h *loopHarness) edge(e slot.Edge) {
	h.t.Helper()
	h.send(e)
	h.sync()
}

// expectSlot checks, on the loop, whether the slot is paused and its
// State.
func (h *loopHarness) expectSlot(paused bool, st slot.State) {
	h.t.Helper()
	h.job(func(s *slot.Slot) {
		if got := s.Paused(); got != paused {
			h.t.Errorf("Paused() = %v, want %v", got, paused)
		}
		if got := s.State(); got != st {
			h.t.Errorf("State() = %+v, want %+v", got, st)
		}
	})
}

// TestEdgePauseOrder: an edge's PauseOn comes before its Reset, Resync or
// Event and its PauseOff after it, so the presses the Resync or Event makes
// are canceled either way; an edge with a Pause and an Event of no Kind is
// the pause alone. The same through an output.Loop.
func TestEdgePauseOrder(t *testing.T) {
	east := stateWith(t, "east")
	eastSouth := stateWith(t, "east", "south")
	for _, tc := range []struct {
		name string
		run  func(h *loopHarness)
		out  string
	}{{
		name: "on, then resync",
		run: func(h *loopHarness) {
			h.edge(slot.Edge{Pause: slot.PauseOn, Resync: &east})
			h.expectSlot(true, east)
			h.set(10)
			h.button("east", false)
		},
	}, {
		name: "resync, then off",
		run: func(h *loopHarness) {
			h.button("east", true)
			h.set(10)
			h.edge(slot.Edge{Pause: slot.PauseOn})
			h.expectSlot(true, east)
			h.set(20)
			h.button("east", false)
			h.edge(slot.Edge{Pause: slot.PauseOff, Resync: &eastSouth})
			h.expectSlot(false, eastSouth)
			h.set(30)
			h.button("east", false)
			h.button("south", false)
			h.set(40)
			h.button("east", true)
			h.set(50)
			h.button("east", false)
		},
		out: "+Space@0 -Space@10 +Space@40 -Space@50",
	}, {
		name: "with an event",
		run: func(h *loopHarness) {
			h.edge(slot.Edge{Event: button("east", true), Pause: slot.PauseOn}) // paused first
			h.expectSlot(true, east)
			h.set(10)
			h.edge(slot.Edge{Event: button("south", true), Pause: slot.PauseOn}) // paused already
			h.edge(slot.Edge{Event: button("south", false), Pause: slot.PauseOff})
			h.expectSlot(false, east)
			h.set(20)
			h.button("east", false) // canceled
			h.set(30)
			h.edge(slot.Edge{Event: button("east", true), Pause: slot.PauseOff}) // not paused
			h.expectSlot(false, east)
			h.set(40)
			h.button("east", false)
		},
		out: "+Space@30 -Space@40",
	}, {
		name: "pause alone",
		run: func(h *loopHarness) {
			h.button("east", true)
			h.set(10)
			h.edge(slot.Edge{Pause: slot.PauseOn}) // an Event of no Kind is left out
			h.expectSlot(true, east)
			h.edge(slot.Edge{Pause: slot.PauseOff})
			h.expectSlot(false, east)
			h.set(20)
			h.button("east", false)
			h.button("east", true)
		},
		out: "+Space@0 -Space@10 +Space@20 -Space@20",
	}, {
		name: "on twice",
		run: func(h *loopHarness) {
			h.button("east", true)
			h.set(10)
			h.edge(slot.Edge{Pause: slot.PauseOn})
			h.set(20)
			h.edge(slot.Edge{Pause: slot.PauseOn})
			h.expectSlot(true, east)
			h.button("south", true)
		},
		out: "+Space@0 -Space@10",
	}, {
		name: "with a reset",
		run: func(h *loopHarness) {
			h.button("east", true)
			h.set(10)
			h.edge(slot.Edge{Reset: true, Pause: slot.PauseOn, Resync: &east})
			h.expectSlot(true, slot.State{})
			h.button("east", true)
			h.set(20)
			h.edge(slot.Edge{Reset: true, Pause: slot.PauseOff})
			h.expectSlot(false, slot.State{})
			h.set(30)
			h.button("east", true) // up since the reset
		},
		out: "+Space@0 -Space@10 +Space@30 -Space@30",
	}} {
		for _, remote := range []bool{false, true} {
			name := tc.name
			if remote {
				name += " remote"
			}
			t.Run(name, func(t *testing.T) {
				h := startSlotLoop(t, loopOpts{remote: remote})
				tc.run(h)
				close(h.edges)
				if err := h.wait(); err != nil {
					t.Errorf("RunLoop = %v, want nil", err)
				}
				expect(t, h.rec, tc.out)
				h.noErrors() // a Pause edge handled as its zero Event would be an unknown input
			})
		}
	}
}

// comboTrace converts trace, in input.trace notation with "combo@t" for a
// ComboHeld of device 1 at t ms, to events.
func comboTrace(t *testing.T, trace string) []input.Event {
	t.Helper()
	var evs []input.Event
	for _, tok := range strings.Fields(trace) {
		if at, ok := strings.CutPrefix(tok, "combo@"); ok {
			msec, err := strconv.Atoi(at)
			if err != nil {
				t.Fatalf("comboTrace: %q: %v", tok, err)
			}
			evs = append(evs, input.Event{Time: uint64(time.Duration(msec) * ms), Device: 1, Kind: input.ComboHeld})
			continue
		}
		e, err := parseInput(tok)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e...)
	}
	return evs
}

// TestReplayCombo: in Replay, a ComboHeld (DMN-07) pauses a running slot,
// which lets go of its keys, the tap in flight included, at the time of
// the ComboHeld; the next one resumes it. OnEvent sees each, State does
// not change, and neither is an unknown input. A release that fails goes
// to Errors, and Replay retries it.
func TestReplayCombo(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	var seen []string
	s, err := slot.New(slot.Config{
		Gen: 1, Program: compile(t, eastHold, southTap), Arbiter: h.Arb, Clock: h.Clock,
		Trace:   func(d engine.Decision) { h.decs = append(h.decs, d) },
		OnEvent: func(ev input.Event) { seen = append(seen, ev.Kind.String()+"@"+h.Clock.Now().String()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	replay := func(trace string) {
		t.Helper()
		if err := s.Replay(h.Clock, comboTrace(t, trace)); err != nil {
			t.Fatalf("Replay(%s): %v", trace, err)
		}
	}

	replay("+east@0 +south@10 combo@15")
	h.ExpectOutput("+Space@0 +LCtrl@10 +Z@10 -Z@15 -Space@15 -LCtrl@15")
	if !s.Paused() {
		t.Error("Paused() = false after a ComboHeld")
	}
	if got, want := s.State(), stateWith(t, "east", "south"); got != want {
		t.Errorf("State() = %+v after a ComboHeld, want %+v", got, want)
	}
	replay("-east@20 +east@30 -south@35 combo@40 -east@50 +east@60 -east@70")
	h.ExpectOutput("+Space@60 -Space@70")
	if s.Paused() {
		t.Error("Paused() = true after a second ComboHeld")
	}
	h.expectDecisions(t,
		"@0 east down dispatch", "@10 south down dispatch",
		"@15 south none cancel", "@15 east none cancel",
		"@20 east up canceled", "@30 east down paused", "@35 south up canceled",
		"@50 east up canceled", "@60 east down dispatch", "@70 east up release")
	if want := []string{
		"down@0s", "down@10ms", "combo@15ms", "up@20ms", "down@30ms", "up@35ms",
		"combo@40ms", "up@50ms", "down@60ms", "up@70ms",
	}; !slices.Equal(seen, want) {
		t.Errorf("OnEvent saw:\n got %q\nwant %q", seen, want)
	}
	if errs := s.Errors(); len(errs) > 0 {
		t.Errorf("Errors = %v, want none", errs)
	}

	// The batch of the release and its immediate resend fail; the retry
	// 100 ms later, which Replay runs as it settles, goes out.
	replay("+east@100")
	h.Out.FailNext(0, nil)
	h.Out.FailNext(0, nil)
	replay("combo@110")
	h.ExpectOutput("+Space@100 -Space@210")
	if errs := s.Errors(); len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) {
		t.Errorf("Errors = %v, want the failed release", errs)
	}
	if !s.Paused() {
		t.Error("Paused() = false after a ComboHeld whose release failed")
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	replay("-east@300 combo@310")
	h.ExpectOutput("")
	h.expectDecisions(t, "@100 east down dispatch", "@110 east none cancel", "@300 east up canceled")
	if s.Paused() {
		t.Error("Paused() = true after the last ComboHeld")
	}
}
