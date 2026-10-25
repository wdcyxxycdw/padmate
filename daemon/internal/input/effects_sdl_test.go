package input_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// These tests need the SDL library, as those of source_test.go do. A virtual
// gamepad can do no rumble, LED or player light (TestVirtualCapabilitiesAreKnown),
// so they check that the effect requests find it, do nothing, and do not
// hang, and, with a made-up gamepad that records its Effector calls
// (AddEffectDevice), that closing the Source ends what runs. Every request
// names a virtual gamepad or the made-up one: nothing here may reach a gamepad
// of the machine, so no SetFeedback with Device 0 either.

// effectRecorder is a haptic.Effector that the made-up gamepad uses; T-input
// calls it, and the test reads it after Close.
type effectRecorder struct {
	mu     sync.Mutex
	calls  []string
	player int
}

func (e *effectRecorder) record(format string, a ...any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, fmt.Sprintf(format, a...))
	return nil
}

func (e *effectRecorder) Rumble(low, high uint16, ms uint32) error {
	return e.record("rumble %d/%d %d", low, high, ms)
}

func (e *effectRecorder) RumbleTriggers(l, r uint16, ms uint32) error {
	return e.record("trig %d/%d %d", l, r, ms)
}

func (e *effectRecorder) SetLED(r, g, b uint8) error { return e.record("led %d,%d,%d", r, g, b) }

func (e *effectRecorder) SetPlayerIndex(i int) error {
	e.mu.Lock()
	e.player = i
	e.mu.Unlock()
	return e.record("player %d", i)
}

func (e *effectRecorder) PlayerIndex() int { e.mu.Lock(); defer e.mu.Unlock(); return e.player }

func (e *effectRecorder) PlayerIndexTaken(int) bool { return false }

func (e *effectRecorder) last(prefix string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.calls) - 1; i >= 0; i-- {
		if len(e.calls[i]) >= len(prefix) && e.calls[i][:len(prefix)] == prefix {
			return e.calls[i]
		}
	}
	return ""
}

// fakeEffectID is the id of the made-up gamepad: far above SDL's instance ids
// (which count up from 1) and below the aliases of revived devices.
const fakeEffectID input.DeviceID = 0x7ff00001

// TestEffectsOnVirtualPad: a virtual gamepad is found by Identify, Play and
// SetFeedback, which do nothing for it and fail on nothing; a gamepad that is
// gone, and device 0, are refused by the new API.
func TestEffectsOnVirtualPad(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "effects pad")

	done := make(chan struct{})
	go func() {
		defer close(done)
		cues, err := s.Identify(v.ID())
		if err != nil || cues.Any() {
			t.Errorf("Identify = %+v, %v; want a gamepad without a cue", cues, err)
		}
		if err := s.Play(input.Effect{Device: v.ID(), Class: haptic.Event, Preset: haptic.Tick}); err != nil {
			t.Errorf("Play: %v", err)
		}
		if err := s.SetFeedback(input.Feedback{Device: v.ID(), Color: "#123456", Pulse: true, Pulses: 2}); err != nil {
			t.Errorf("SetFeedback: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an effect request on a virtual gamepad hung")
	}
	if err := s.Play(input.Effect{Class: haptic.Event, Preset: haptic.Tick}); err == nil {
		t.Error("Play with device 0 succeeded")
	}

	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
	collect(t, s, v.ID(), 1) // its removal
	if _, err := s.Identify(v.ID()); !errors.Is(err, input.ErrNoDevice) {
		t.Errorf("Identify after the removal: %v, want ErrNoDevice", err)
	}
	if err := s.Play(input.Effect{Device: v.ID(), Class: haptic.Event, Preset: haptic.Tick}); !errors.Is(err, input.ErrNoDevice) {
		t.Errorf("Play after the removal: %v, want ErrNoDevice", err)
	}
}

// TestEffectsEndWhenTheSourceCloses: Close ends what runs on a gamepad with
// every effect: the motors stop, the color is released, the player lights are
// restored, and Close does not hang on it.
func TestEffectsEndWhenTheSourceCloses(t *testing.T) {
	s, err := input.Open(input.Options{Library: inputtest.Library(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	rec := &effectRecorder{player: 2}
	caps := haptic.Caps{Rumble: true, LED: true, RGBLED: true, PlayerLED: true}
	if err := s.AddEffectDevice(fakeEffectID, rec, caps); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFeedback(input.Feedback{Device: fakeEffectID, Color: "#FF0000", Pulse: true, Pulses: 1, PulseMs: 3000}); err != nil {
		t.Fatal(err)
	}
	cues, err := s.Identify(fakeEffectID)
	if err != nil || cues != (haptic.Cues{Rumble: true, LED: true}) {
		t.Fatalf("Identify = %+v, %v", cues, err)
	}
	if rec.last("rumble") == "" || rec.last("led") == "" {
		t.Fatalf("the effects did not start: %v", rec.calls)
	}

	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung with effects running")
	}
	if got := rec.last("rumble"); got != "rumble 0/0 0" {
		t.Errorf("last rumble call %q, want the stop", got)
	}
	if got := rec.last("led"); got != "led 0,0,0" {
		t.Errorf("last LED call %q, want the release (#000000)", got)
	}
}

// TestWriteToARemovedGamepadLeavesNoPlayerIndexBehind: SDL clears the
// player-index entry of a joystick when it removes it, but SDL_SetJoystickPlayerIndex
// still accepts a joystick whose handle is open, and would put the dead
// instance id back into the table, where nothing clears it: the next gamepad to
// connect would get a higher index (a Switch pad would light the wrong player).
// The Effector of a gamepad that SDL no longer lists does nothing, so a write
// that the Mixer makes before T-input has read the REMOVED event (the shutdown,
// or a rescan) leaves the table alone. The detach and the write are one T-input
// call, so the Source still holds the gamepad when the write comes.
func TestWriteToARemovedGamepadLeavesNoPlayerIndexBehind(t *testing.T) {
	s := open(t, nil)
	a, _ := attach(t, s, "player pad A")
	ra, err := s.Readout(a.ID())
	if err != nil {
		t.Fatal(err)
	}
	if ra.PlayerIndex < 0 {
		t.Skipf("SDL gave the virtual gamepad no player index (%d)", ra.PlayerIndex)
	}
	if err := s.DetachThenSetPlayerIndex(a, ra.PlayerIndex); err != nil {
		t.Fatal(err)
	}
	collect(t, s, a.ID(), 1) // its removal

	// The next gamepad takes the first free slot, the one A left.
	b, _ := attach(t, s, "player pad B")
	rb, err := s.Readout(b.ID())
	if err != nil {
		t.Fatal(err)
	}
	if rb.PlayerIndex != ra.PlayerIndex {
		t.Errorf("the next gamepad got player index %d, want %d: the slot of the removed gamepad must be free", rb.PlayerIndex, ra.PlayerIndex)
	}
}
