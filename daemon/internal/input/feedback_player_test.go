package input

import (
	"fmt"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
)

// The player lights follow the mode of the layer (HUD-08): Feedback.PlayerMode
// is the ambient claim of the Mixer for them. These tests need no SDL; the
// recording gamepads are the ones of feedback_test.go.

// setMode sends the ambient state with the mode at ms.
func (r *effectRig) setMode(id DeviceID, mode, ms int) {
	r.t.Helper()
	r.now = ms
	if err := r.s.setFeedback(Feedback{Device: id, PlayerMode: mode}, at(ms)); err != nil {
		r.t.Fatalf("setFeedback(mode %d): %v", mode, err)
	}
}

func TestSetFeedbackLightsThePlayerOfTheMode(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{PlayerLED: true})
	e.player = 2 // the index SDL gave the gamepad

	r.setMode(1, 0, 0)
	expect(t, e) // no mode, and nothing was changed: nothing to give back
	// Mode n lights the light of player n, which is index n-1.
	for mode := 1; mode <= MaxPlayerMode; mode++ {
		r.setMode(1, mode, 10*mode)
		expect(t, e, fmt.Sprintf("@%d player %d", 10*mode, mode-1))
	}
	// The same mode again, as a reload or a refresh sends, changes nothing.
	r.setMode(1, 4, 100)
	expect(t, e)
	// A mode above four has no light: the claim is given back.
	r.setMode(1, 5, 110)
	expect(t, e, "@110 player 2")
	r.setMode(1, 6, 120)
	r.setMode(1, 0, 130)
	expect(t, e)
	// Back to a mode that has one.
	r.setMode(1, 2, 200)
	expect(t, e, "@200 player 1")
	// The profile's lights going off (feedback.led) is mode 0.
	r.setMode(1, 0, 210)
	expect(t, e, "@210 player 2")
}

func TestSetFeedbackPlayerLightsNeedTheCapability(t *testing.T) {
	r := newEffectRig(t)
	xbox := r.add(1, haptic.Caps{Rumble: true})
	light := r.add(2, haptic.Caps{LED: true, RGBLED: true})
	for _, mode := range []int{1, 2, 4, 5, 0} {
		r.setMode(0, mode, 10*mode) // every open gamepad
	}
	// Nothing at all for either: a color LED has its own lights, and a call for
	// the player lights of a gamepad without them would renumber the others.
	expect(t, xbox)
	expect(t, light)
}

func TestSetFeedbackPlayerLightsAreIndependentOfTheColor(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{LED: true, RGBLED: true, PlayerLED: true})
	e.player = 0
	r.now = 0
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#112233", PlayerMode: 2}, at(0)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@0 led #112233", "@0 player 1")
	// A new color with the same mode writes the color only.
	r.now = 10
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#445566", PlayerMode: 2}, at(10)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@10 led #445566")
	// Lights off in the profile: no color and no mode release both.
	r.now = 20
	if err := r.s.setFeedback(Feedback{Device: 1}, at(20)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@20 led #000000", "@20 player 0")
	r.now = 30
	if err := r.s.setFeedback(Feedback{Device: 1}, at(30)); err != nil {
		t.Fatal(err)
	}
	expect(t, e) // already released
}

func TestSetFeedbackPlayerLightsEndWithTheGamepad(t *testing.T) {
	// Loss by the receiver watchdog (the gamepad is still listed) and shutdown
	// give the index back (CLAUDE.md invariant 1, for the lights). A gamepad
	// that SDL removed gets no call: TestPlayerIndexOfARemovedGamepadIsNotWritten.
	for _, tc := range []struct {
		name string
		end  func(r *effectRig)
	}{
		{"lost", func(r *effectRig) {
			d := r.s.openDevice(1)
			r.s.stopEffects(d)
		}},
		{"shutdown", func(r *effectRig) { r.s.stopAllEffects() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newEffectRig(t)
			e := r.add(1, haptic.Caps{PlayerLED: true})
			e.player = 0
			r.setMode(1, 3, 0)
			expect(t, e, "@0 player 2")
			tc.end(r)
			if len(e.calls) != 1 || e.calls[0] != "@0 player 0" {
				t.Errorf("calls after the end: %v, want the original index back", e.calls)
			}
		})
	}
}

// SDL clears the player-index entry of a joystick when it removes it
// (SDL_PrivateJoystickRemoved), but a write for that joystick, which SDL allows
// while the handle is open, puts its dead id back, and nothing ever clears it
// again: every pad that connects later gets a higher index, and a Switch pad
// lights the wrong player. So a gamepad that SDL removed (Source.remove: the
// REMOVED events, and a rescan that no longer lists it) is forgotten without a
// call, while one that the receiver watchdog lost (lose) is still listed and
// still gets its index back.
func TestPlayerIndexOfARemovedGamepadIsNotWritten(t *testing.T) {
	const id = 7
	caps := haptic.Caps{Rumble: true, LED: true, RGBLED: true, PlayerLED: true}
	var clk int
	// start returns a Source with a watched Switch-style gamepad in mode 3
	// (index 2; SDL gave it 1), with a color and a long pulse running.
	start := func(t *testing.T) (*Source, *device, *recEffector) {
		t.Helper()
		var lb logBuffer
		s := testSource(t, &lb)
		d := watchedDevice(s, id, silent(), 0)
		e := &recEffector{now: &clk, player: 1}
		s.effects().Add(id, e, caps)
		f := Feedback{Device: id, Color: "#FF0000", PlayerMode: 3, Pulse: true, Pulses: 1, PulseMs: 3000, Strength: 0x7000}
		if err := s.setFeedback(f, at(0)); err != nil {
			t.Fatal(err)
		}
		if e.player != 2 {
			t.Fatalf("player index %d, want the index 2 of mode 3", e.player)
		}
		e.calls = nil
		clk = 100
		return s, d, e
	}

	t.Run("removed by SDL", func(t *testing.T) {
		s, _, e := start(t)
		s.remove(id, ms(100))
		expect(t, e) // not the index back, not the stop of the motors, not the LED
		if e.player != 2 {
			t.Errorf("player index %d, want 2: it must not be written", e.player)
		}
		if s.mix.Has(id) || len(s.devices) != 0 {
			t.Error("the removed gamepad is still known")
		}
		if evs := drainAll(s); len(evs) != 1 || evs[0].Kind != DeviceRemoved || evs[0].Device != id {
			t.Errorf("events %+v, want the removal of %d", evs, id)
		}
		s.stopAllEffects()
		expect(t, e)
	})
	t.Run("lost by the receiver, then removed by SDL", func(t *testing.T) {
		s, d, e := start(t)
		s.lose(d, ms(5000))
		// It is still listed, so everything is given back.
		expect(t, e, "@100 rumble stop", "@100 led #000000", "@100 player 1")
		if s.mix.Has(id) {
			t.Error("the lost gamepad still has effects")
		}
		s.remove(id, ms(6000))
		expect(t, e) // already given back, and gone now
		if evs := drainAll(s); len(evs) != 1 || evs[0].Kind != DeviceRemoved {
			t.Errorf("events %+v, want the one removal of the loss", evs)
		}
	})
}
