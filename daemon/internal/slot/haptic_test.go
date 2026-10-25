package slot_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// TestOnHaptic: a haptic of the engine (MAP-25) reaches Config.OnHaptic with
// the controller the slot has at that moment, 0 when it has none; a paused
// slot plays none, and a reload takes the new profile's haptics.
func TestOnHaptic(t *testing.T) {
	const bind = `"south": {"haptic": %q, "press": {"type": "keys", "keys": "A", "mode": "tap"}}`
	sim := simtest.New(t)
	var got []string
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compile(t, fmt.Sprintf(bind, "tick")), Arbiter: sim.Arb, Clock: sim.Clock,
		OnHaptic: func(dev input.DeviceID, h engine.Haptic) {
			got = append(got, fmt.Sprintf("%d:%s/%d", dev, h.Preset, h.Strength))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// replay presses and releases south, 50 ms apart, from the clock's Now.
	replay := func(extra ...input.Event) {
		t.Helper()
		now := uint64(sim.Clock.Now())
		evs := slices.Clone(extra)
		for i := range evs {
			evs[i].Time = now
		}
		evs = append(evs,
			input.Event{Time: now, Kind: input.ButtonDown, Control: "south"},
			input.Event{Time: now + uint64(50*time.Millisecond), Kind: input.ButtonUp, Control: "south"})
		if err := s.Replay(sim.Clock, evs); err != nil {
			t.Fatal(err)
		}
	}
	check := func(what string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: haptics %v; want %v", what, got, want)
		}
		got = nil
	}
	replay()
	check("no controller yet", "0:tick/28672")
	replay(input.Event{Kind: input.DeviceAdded, Device: 7, Info: &input.DeviceInfo{ID: 7}})
	check("controller 7", "7:tick/28672")
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	replay()
	check("paused")
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(compile(t, fmt.Sprintf(bind, "confirm")), 2); err != nil {
		t.Fatal(err)
	}
	replay()
	check("reloaded", "7:confirm/28672")
	replay(input.Event{Kind: input.DeviceRemoved, Device: 7})
	check("controller removed", "0:confirm/28672")
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

// TestSwitchesAndRefreshFeedback: Config.Switches is the engine's source of the
// global feedback switches (UI-27, ADR-0067). The rumble switch silences a
// haptic as soon as it is off, and RefreshFeedback has the engine publish the
// layer and feedback state again with the switches as they are now.
func TestSwitchesAndRefreshFeedback(t *testing.T) {
	const bind = `"south": {"haptic": "tick", "press": {"type": "keys", "keys": "A", "mode": "tap"}}`
	sim := simtest.New(t)
	sw := profile.DefaultFeedbackSwitches()
	var haptics int
	var states []engine.Feedback
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compile(t, bind), Arbiter: sim.Arb, Clock: sim.Clock,
		Switches: func() profile.FeedbackSwitches { return sw },
		OnHaptic: func(input.DeviceID, engine.Haptic) { haptics++ },
		Feedback: func(f engine.Feedback) { states = append(states, f) },
	})
	if err != nil {
		t.Fatal(err)
	}
	tap := func() {
		t.Helper()
		now := uint64(sim.Clock.Now())
		if err := s.Replay(sim.Clock, []input.Event{
			{Time: now, Kind: input.ButtonDown, Control: "south"},
			{Time: now + uint64(50*time.Millisecond), Kind: input.ButtonUp, Control: "south"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	tap()
	sw.Rumble = false
	tap()
	if haptics != 1 {
		t.Errorf("%d haptics; want 1 (the tap with rumble off plays none)", haptics)
	}
	states = nil
	sw.LED = false
	s.RefreshFeedback()
	if len(states) != 1 || states[0].LED || states[0].Pulse || states[0].Color != "" {
		t.Errorf("states after RefreshFeedback = %+v; want one without lights or pulse", states)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}
