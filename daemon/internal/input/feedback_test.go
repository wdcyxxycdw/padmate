package input

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
)

// These tests need no SDL: a Source that was never opened has devices that a
// test adds with a recording Effector, and T-input's functions are called
// from the test, which stands in for T-input.

var t0 = time.Unix(1_000_000, 0)

// at returns the time ms milliseconds after t0.
func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

// recEffector is a haptic.Effector that records its calls with the time they
// were made at, which the test sets in now.
type recEffector struct {
	now     *int
	calls   []string
	player  int
	fail    error // returned by every call, when set
	failLED error // returned by SetLED only, when set
	panics  bool  // panic in every call
}

func (e *recEffector) record(format string, a ...any) error {
	if e.panics {
		panic("driver")
	}
	e.calls = append(e.calls, fmt.Sprintf("@%d ", *e.now)+fmt.Sprintf(format, a...))
	return e.fail
}

func (e *recEffector) Rumble(low, high uint16, ms uint32) error {
	if low|high == 0 && ms == 0 {
		return e.record("rumble stop")
	}
	return e.record("rumble %d/%d %dms", low, high, ms)
}

func (e *recEffector) RumbleTriggers(l, r uint16, ms uint32) error {
	return e.record("trig %d/%d %dms", l, r, ms)
}

func (e *recEffector) SetLED(r, g, b uint8) error {
	if err := e.record("led #%02X%02X%02X", r, g, b); err != nil {
		return err
	}
	return e.failLED
}

func (e *recEffector) SetPlayerIndex(i int) error {
	if err := e.record("player %d", i); err != nil {
		return err
	}
	e.player = i
	return nil
}

func (e *recEffector) PlayerIndex() int { return e.player }

func (e *recEffector) PlayerIndexTaken(int) bool { return false }

// effectRig is a Source with fake gamepads.
type effectRig struct {
	t   *testing.T
	s   *Source
	now int
}

func newEffectRig(t *testing.T) *effectRig {
	t.Helper()
	return &effectRig{t: t, s: &Source{
		events:  make(chan Event, 16),
		devices: make(map[sdl.JoystickID]*device),
		log:     slog.New(slog.DiscardHandler),
	}}
}

// add adds a gamepad with the public id id that can do caps.
func (r *effectRig) add(id DeviceID, caps haptic.Caps) *recEffector {
	r.t.Helper()
	d := &device{id: sdl.JoystickID(id), pub: id}
	r.s.devices[d.id] = d
	e := &recEffector{now: &r.now, player: -1}
	r.s.effects().Add(haptic.Device(id), e, caps)
	return e
}

// advance moves the clock to ms, in steps of a millisecond from the last
// call, and advances the Source's effects.
func (r *effectRig) advance(ms int) {
	for r.now < ms {
		r.now++
		r.s.advanceEffects(at(r.now))
	}
}

func expect(t *testing.T, e *recEffector, want ...string) {
	t.Helper()
	if !slices.Equal(e.calls, want) {
		t.Errorf("calls:\n  %s\nwant:\n  %s", strings.Join(e.calls, "\n  "), strings.Join(want, "\n  "))
	}
	e.calls = nil
}

const m3Both = "28672/28672" // 0x7000 on both motors

// The pulse plan of M3 (the former TestFeedbackPulsePlan and
// TestFeedbackDefaults): the first pulse at once, the next ones Pulse+Gap
// after each other, at the strength on both motors; the defaults are one
// pulse of 70 ms at 0x7000.
func TestSetFeedbackPulsesAsM3Did(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true})
	f := Feedback{Device: 1, Pulse: true, Pulses: 3, PulseMs: 70, GapMs: 100, Strength: 0x7000}
	if err := r.s.setFeedback(f, at(0)); err != nil {
		t.Fatal(err)
	}
	r.advance(600)
	expect(t, e,
		"@0 rumble "+m3Both+" 70ms",
		"@170 rumble "+m3Both+" 70ms",
		"@340 rumble "+m3Both+" 70ms",
		"@410 rumble stop",
	)

	if err := r.s.setFeedback(Feedback{Device: 1, Pulse: true, Pulses: 0}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 300)
	expect(t, e, "@600 rumble "+m3Both+" 70ms", "@670 rumble stop")

	// Not a pulse: no rumble.
	if err := r.s.setFeedback(Feedback{Device: 1, Pulse: false, Pulses: 3}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 300)
	expect(t, e)
}

// A new pulse train replaces the one that runs, and stops its pulse first. A
// request with a color only no longer cancels the train (the M3 shim did).
func TestSetFeedbackColorOnlyKeepsTheTrain(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, LED: true, RGBLED: true})
	train := Feedback{Device: 1, Pulse: true, Pulses: 3, PulseMs: 70, GapMs: 100, Strength: 0x7000}
	if err := r.s.setFeedback(train, at(0)); err != nil {
		t.Fatal(err)
	}
	r.advance(100)
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#112233"}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(450)
	expect(t, e,
		"@0 rumble "+m3Both+" 70ms",
		"@100 led #112233",
		"@170 rumble "+m3Both+" 70ms",
		"@340 rumble "+m3Both+" 70ms",
		"@410 rumble stop",
	)

	// Pulse and color together, as the engine sends them: the train replaces.
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#112233", Pulse: true, Pulses: 2, PulseMs: 70, GapMs: 100}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 100)
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#445566", Pulse: true, Pulses: 1, PulseMs: 210}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 500)
	expect(t, e,
		"@450 led #112233", // shown already, and written again: a request is (TestSetFeedbackWritesTheColorAgain)
		"@450 rumble "+m3Both+" 70ms",
		"@550 led #445566",
		"@550 rumble "+m3Both+" 210ms",
		"@760 rumble stop",
	)
}

// Every SetFeedback with a color writes it, even when the LED shows it
// already, as M3 did: SDL drops the repeat inside its window and sends it
// after, which brings back a light that a receiver reset (a layer change, a
// resume from pause, a reload and a refresh all end up here).
func TestSetFeedbackWritesTheColorAgain(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{LED: true, RGBLED: true})
	for ms := range 3 {
		if err := r.s.setFeedback(Feedback{Device: 1, Color: "#112233"}, at(ms*10)); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, e, "@0 led #112233", "@0 led #112233", "@0 led #112233")
}

// A color of "" releases the ambient color: #000000 on a color LED that
// padmate colored, nothing on a mono LED or on one it never colored.
func TestSetFeedbackReleasesTheColor(t *testing.T) {
	r := newEffectRig(t)
	rgb := r.add(1, haptic.Caps{LED: true, RGBLED: true})
	mono := r.add(2, haptic.Caps{LED: true})
	never := r.add(3, haptic.Caps{LED: true, RGBLED: true})
	none := r.add(4, haptic.Caps{Rumble: true})

	// Device 0 is every gamepad; the others it does not name are left alone.
	if err := r.s.setFeedback(Feedback{Device: 2, Color: "#FF8000"}, at(0)); err != nil {
		t.Fatal(err)
	}
	expect(t, mono, "@0 led #FF8000")
	expect(t, rgb)
	if err := r.s.setFeedback(Feedback{Color: "#00FF00"}, at(1)); err != nil {
		t.Fatal(err)
	}
	expect(t, rgb, "@0 led #00FF00")
	expect(t, mono, "@0 led #00FF00")
	expect(t, never, "@0 led #00FF00")
	expect(t, none)

	// The layer has no color any more.
	for _, id := range []DeviceID{1, 2} {
		if err := r.s.setFeedback(Feedback{Device: id}, at(2)); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, rgb, "@0 led #000000")
	expect(t, mono)
	// And again: nothing more to release.
	if err := r.s.setFeedback(Feedback{}, at(3)); err != nil {
		t.Fatal(err)
	}
	expect(t, rgb)
	expect(t, mono)
	// never was colored by Device 0 above, so it is released too.
	expect(t, never, "@0 led #000000")

	// A gamepad that was never colored is left alone by a release.
	fresh := r.add(5, haptic.Caps{LED: true, RGBLED: true})
	if err := r.s.setFeedback(Feedback{Device: 5}, at(4)); err != nil {
		t.Fatal(err)
	}
	expect(t, fresh)
}

func TestSetFeedbackTargets(t *testing.T) {
	r := newEffectRig(t)
	a := r.add(1, haptic.Caps{Rumble: true})
	b := r.add(2, haptic.Caps{Rumble: true})
	c := r.add(3, haptic.Caps{Rumble: true})
	r.s.devices[3].dormant = true // reported removed by the receiver watchdog
	r.s.devices[9] = &device{id: 9, pub: 9}
	pulse := Feedback{Pulse: true, Pulses: 1, PulseMs: 50, Strength: 100}

	pulse.Device = 2
	if err := r.s.setFeedback(pulse, at(0)); err != nil {
		t.Fatal(err)
	}
	expect(t, a)
	expect(t, b, "@0 rumble 100/100 50ms")
	pulse.Device = 0 // every open gamepad, not the dormant one
	if err := r.s.setFeedback(pulse, at(100)); err != nil {
		t.Fatal(err)
	}
	r.advance(200)
	if len(a.calls) < 1 || len(b.calls) < 1 || len(c.calls) != 0 {
		t.Errorf("broadcast: calls %v, %v, %v; want the first two and none for the dormant one", a.calls, b.calls, c.calls)
	}
	a.calls, b.calls = nil, nil
	// A gamepad that is gone, or that has no effects, is ignored: not an error.
	for _, id := range []DeviceID{42, 9, 3} {
		pulse.Device = id
		if err := r.s.setFeedback(pulse, at(1000)); err != nil {
			t.Errorf("setFeedback(device %d): %v", id, err)
		}
	}
	expect(t, a)
	expect(t, c)

	// An invalid color is an error and changes nothing.
	for _, color := range []string{"red", "#12345", "#GG0000", "112233", "#1122334"} {
		if err := r.s.setFeedback(Feedback{Color: color, Pulse: true}, at(2000)); err == nil {
			t.Errorf("setFeedback(%q) succeeded", color)
		}
	}
	expect(t, a)
	expect(t, b)
}

// A gamepad whose LED write fails (the HOME light of the Nova in Switch mode,
// if its receiver does not take the subcommand) still gives the pulses of a
// layer, as it did in M3; only the LED is left alone for its cooldown.
func TestSetFeedbackLEDFailureKeepsThePulses(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, LED: true})
	boom := errors.New("hid_write failed")
	e.failLED = boom
	layer := Feedback{Device: 1, Color: "#112233", Pulse: true, Pulses: 2, PulseMs: 70, GapMs: 100, Strength: 0x7000}
	if err := r.s.setFeedback(layer, at(0)); !errors.Is(err, boom) {
		t.Fatalf("setFeedback = %v, want the failure of the LED", err)
	}
	r.advance(300)
	expect(t, e,
		"@0 led #112233",
		"@0 rumble "+m3Both+" 70ms", "@170 rumble "+m3Both+" 70ms", "@240 rumble stop",
	)
	// The next layer: the color waits for the LED's cooldown, the pulses play.
	layer.Color = "#445566"
	if err := r.s.setFeedback(layer, at(r.now)); err != nil {
		t.Fatalf("setFeedback in the LED's cooldown: %v", err)
	}
	r.advance(r.now + 300)
	expect(t, e,
		"@300 rumble "+m3Both+" 70ms", "@470 rumble "+m3Both+" 70ms", "@540 rumble stop",
	)
	e.failLED = nil
	r.advance(int(haptic.LEDCooldown/time.Millisecond) + 10)
	expect(t, e, fmt.Sprintf("@%d led #445566", int(haptic.LEDCooldown/time.Millisecond)))
}

// A call that fails goes back to the caller, and starts the Mixer's
// cooldown of its group (the motors, the LED, the player lights): nothing more
// reaches those of that gamepad for five seconds, seven for the LED.
func TestSetFeedbackFailureStartsTheCooldown(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, LED: true, RGBLED: true})
	boom := errors.New("hid_write failed")
	e.fail = boom
	err := r.s.setFeedback(Feedback{Device: 1, Color: "#112233", Pulse: true}, at(0))
	if !errors.Is(err, boom) {
		t.Fatalf("setFeedback = %v, want the failure", err)
	}
	e.fail = nil
	n := len(e.calls)
	r.advance(100)
	if err := r.s.setFeedback(Feedback{Device: 1, Pulse: true}, at(r.now)); err != nil {
		t.Errorf("setFeedback in the cooldown: %v", err)
	}
	r.advance(4999)
	if len(e.calls) != n {
		t.Errorf("calls in the cooldown: %v", e.calls[n:])
	}
}

func TestPlay(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true})
	tick := Effect{Device: 1, Class: haptic.Event, Preset: haptic.Tick, Strength: 1000}

	if err := r.s.play(tick, at(0)); err != nil {
		t.Fatal(err)
	}
	r.advance(100)
	expect(t, e, "@0 rumble 0/1000 25ms", "@25 rumble stop")
	// Strength 0 is the default; ShortMs and GapMs give the pulses of pulseN.
	if err := r.s.play(Effect{Device: 1, Class: haptic.Script, Preset: haptic.Pulse2, ShortMs: 40, GapMs: 30}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 200)
	expect(t, e, "@100 rumble "+m3Both+" 40ms", "@170 rumble "+m3Both+" 40ms", "@210 rumble stop")

	// The new API wants a gamepad of its own, a class it serves, and a preset.
	for name, bad := range map[string]Effect{
		"device 0":      {Class: haptic.Event, Preset: haptic.Tick},
		"class ambient": {Device: 1, Class: haptic.Ambient, Preset: haptic.Tick},
		"class":         {Device: 1, Class: 0, Preset: haptic.Tick},
		"identify":      {Device: 1, Class: haptic.Identify, Preset: haptic.Tick},
		"preset":        {Device: 1, Class: haptic.Event},
		"preset 99":     {Device: 1, Class: haptic.Event, Preset: 99},
	} {
		if err := r.s.play(bad, at(1000)); err == nil {
			t.Errorf("%s: Play succeeded", name)
		}
	}
	expect(t, e)
	if err := r.s.play(Effect{Device: 7, Class: haptic.Event, Preset: haptic.Tick}, at(1000)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Play(unknown gamepad) = %v, want ErrNoDevice", err)
	}
	r.s.devices[1].dormant = true
	if err := r.s.play(tick, at(1000)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Play(dormant gamepad) = %v, want ErrNoDevice", err)
	}
	// A joystick without a gamepad has no effects, and no error.
	r.s.devices[5] = &device{id: 5, pub: 5}
	if err := r.s.play(Effect{Device: 5, Class: haptic.Event, Preset: haptic.Tick}, at(1000)); err != nil {
		t.Errorf("Play(joystick) = %v", err)
	}
	expect(t, e)
}

// An event never cuts the pulses of a layer, and two events in a burst are
// spaced by the Mixer.
func TestPlayDoesNotCutAmbientPulses(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true})
	if err := r.s.setFeedback(Feedback{Device: 1, Pulse: true, Pulses: 2, PulseMs: 70, GapMs: 100, Strength: 0x7000}, at(0)); err != nil {
		t.Fatal(err)
	}
	if err := r.s.play(Effect{Device: 1, Class: haptic.Event, Preset: haptic.Tick}, at(10)); err != nil {
		t.Fatal(err)
	}
	r.advance(300)
	expect(t, e, "@0 rumble "+m3Both+" 70ms", "@170 rumble "+m3Both+" 70ms", "@240 rumble stop")
}

// An alert (IN-12) plays its preset and flashes the lights side by side, each
// alone when the other is not asked for; it outranks the pulses of the layer and
// the layer color comes back after the last flash; only an alert blinks.
func TestPlayAlert(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, LED: true, RGBLED: true})
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#102030"}, at(0)); err != nil {
		t.Fatal(err)
	}
	r.advance(5)
	expect(t, e, "@0 led #102030")
	alert := Effect{Device: 1, Class: haptic.Alert, Preset: haptic.Pulse2, ShortMs: 40, GapMs: 30, Blink: true}
	if err := r.s.play(alert, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(1500)
	expect(t, e,
		"@5 rumble "+m3Both+" 40ms", "@5 led #FF0000",
		"@75 rumble "+m3Both+" 40ms", "@115 rumble stop",
		"@255 led #102030", "@505 led #FF0000", "@755 led #102030", "@1005 led #FF0000", "@1255 led #102030",
	)

	// The preset alone, and the lights alone.
	if err := r.s.play(Effect{Device: 1, Class: haptic.Alert, Preset: haptic.Tick}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 100)
	expect(t, e, "@1500 rumble 0/28672 25ms", "@1525 rumble stop")
	if err := r.s.play(Effect{Device: 1, Class: haptic.Alert, Blink: true}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 1500)
	expect(t, e, "@1600 led #FF0000", "@1850 led #102030", "@2100 led #FF0000", "@2350 led #102030", "@2600 led #FF0000", "@2850 led #102030")

	// It outranks the pulses of the layer.
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#102030", Pulse: true, Pulses: 3, PulseMs: 70, GapMs: 100, Strength: 0x7000}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(r.now + 30)
	expect(t, e, "@3100 led #102030", "@3100 rumble "+m3Both+" 70ms")
	if err := r.s.play(Effect{Device: 1, Class: haptic.Alert, Preset: haptic.Pulse1}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@3130 rumble stop", "@3130 rumble "+m3Both+" 70ms")

	// Only an alert blinks, and a blink needs no preset but a plain effect does.
	for name, bad := range map[string]Effect{
		"event blink":  {Device: 1, Class: haptic.Event, Preset: haptic.Tick, Blink: true},
		"script blink": {Device: 1, Class: haptic.Script, Blink: true},
		"no preset":    {Device: 1, Class: haptic.Alert},
		"bad preset":   {Device: 1, Class: haptic.Alert, Preset: 99, Blink: true},
	} {
		if err := r.s.play(bad, at(r.now)); err == nil {
			t.Errorf("%s: Play succeeded", name)
		}
	}
	expect(t, e)
}

// A gamepad with player lights and no color LED flashes them for an alert;
// one that cannot flash is not an error.
func TestPlayAlertBlinkNeedsALight(t *testing.T) {
	r := newEffectRig(t)
	players := r.add(1, haptic.Caps{Rumble: true, PlayerLED: true})
	players.player = 1
	plain := r.add(2, haptic.Caps{Rumble: true, LED: true}) // a mono LED does not flash
	if err := r.s.play(Effect{Device: 1, Class: haptic.Alert, Preset: haptic.Tick, Blink: true}, at(0)); err != nil {
		t.Fatal(err)
	}
	if err := r.s.play(Effect{Device: 2, Class: haptic.Alert, Preset: haptic.Tick, Blink: true}, at(0)); err != nil {
		t.Fatal(err)
	}
	r.advance(1500)
	expect(t, players, "@0 rumble 0/28672 25ms", "@0 player 3", "@25 rumble stop", "@250 player -1", "@500 player 3", "@750 player -1", "@1000 player 3", "@1250 player 1")
	expect(t, plain, "@0 rumble 0/28672 25ms", "@25 rumble stop")
}

func TestIdentify(t *testing.T) {
	r := newEffectRig(t)
	full := r.add(1, haptic.Caps{Rumble: true, LED: true, RGBLED: true})
	players := r.add(2, haptic.Caps{PlayerLED: true})
	r.add(3, haptic.Caps{})
	r.s.devices[8] = &device{id: 8, pub: 8} // a joystick without a gamepad
	r.s.devices[4] = &device{id: 4, pub: 4, dormant: true}

	cues, err := r.s.identify(1, at(0))
	if err != nil || cues != (haptic.Cues{Rumble: true, LED: true}) {
		t.Fatalf("identify(1) = %+v, %v", cues, err)
	}
	if cues, err = r.s.identify(2, at(0)); err != nil || cues != (haptic.Cues{Player: true}) {
		t.Fatalf("identify(2) = %+v, %v", cues, err)
	}
	for _, id := range []DeviceID{3, 8} {
		if cues, err = r.s.identify(id, at(0)); err != nil || cues.Any() {
			t.Errorf("identify(%d) = %+v, %v; want no cue and no error", id, cues, err)
		}
	}
	for _, id := range []DeviceID{4, 5, 0} {
		if _, err = r.s.identify(id, at(0)); !errors.Is(err, ErrNoDevice) {
			t.Errorf("identify(%d) = %v, want ErrNoDevice", id, err)
		}
	}
	r.advance(2000)
	expect(t, full,
		"@0 rumble "+m3Both+" 150ms", "@0 led #FFFFFF",
		"@250 led #000000", "@300 rumble "+m3Both+" 150ms",
		"@500 led #FFFFFF", "@600 rumble "+m3Both+" 150ms",
		"@750 rumble stop", "@750 led #000000",
		"@1000 led #FFFFFF",
		"@1250 led #000000",
	)
	expect(t, players, "@0 player 0", "@375 player 1", "@750 player 2", "@1125 player 3", "@1500 player -1")
}

// Identify gives the layer color back, and does not touch the pulses of the
// layer, which it only outranks.
func TestIdentifyDoesNotDisturbAmbient(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{LED: true, RGBLED: true})
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#102030"}, at(0)); err != nil {
		t.Fatal(err)
	}
	r.now = 100
	if _, err := r.s.identify(1, at(100)); err != nil {
		t.Fatal(err)
	}
	r.advance(2000)
	expect(t, e,
		"@0 led #102030",
		"@100 led #FFFFFF", "@350 led #102030", "@600 led #FFFFFF", "@850 led #102030", "@1100 led #FFFFFF", "@1350 led #102030",
	)
}

// Every way a gamepad can leave ends its effects: reported removed by the
// receiver watchdog (stopEffects), and the Source shutting down. A gamepad that
// SDL removed is forgotten without a call (below, and
// TestPlayerIndexOfARemovedGamepadIsNotWritten).
func TestEffectsStopOnEveryPath(t *testing.T) {
	long := Feedback{Pulse: true, Pulses: 1, PulseMs: 2000, Strength: 0x7000}
	start := func(r *effectRig, id DeviceID, caps haptic.Caps) *recEffector {
		t.Helper()
		e := r.add(id, caps)
		e.player = 1
		long.Device, long.Color = id, "#FF0000"
		if err := r.s.setFeedback(long, at(0)); err != nil {
			t.Fatal(err)
		}
		if err := r.s.effects().SetPlayer(haptic.Device(id), haptic.Ambient, 0, at(0)); err != nil {
			t.Fatal(err)
		}
		e.calls = nil
		return e
	}
	caps := haptic.Caps{Rumble: true, LED: true, RGBLED: true, PlayerLED: true}
	want := []string{"@50 rumble stop", "@50 led #000000", "@50 player 1"}

	t.Run("removed by SDL", func(t *testing.T) {
		// SDL is done with the gamepad: SDL_CloseJoystick, which remove calls,
		// stops the rumble, and a write of the player index would leave the
		// dead id in SDL's table. So not one call.
		r := newEffectRig(t)
		e := start(r, 1, caps)
		r.now = 50
		r.s.remove(1, 123)
		expect(t, e)
		if r.s.mix.Has(1) || len(r.s.devices) != 0 {
			t.Error("the removed gamepad is still known")
		}
		// And nothing comes back for it: not on the next turn of the loop, not
		// at the shutdown.
		r.advance(5000)
		r.s.stopAllEffects()
		expect(t, e)
	})
	t.Run("lost", func(t *testing.T) {
		r := newEffectRig(t)
		e := start(r, 1, caps)
		r.now = 50
		r.s.stopEffects(r.s.devices[1])
		expect(t, e, want...)
		// It stays in the Source (dormant, its gamepad open), but no effect
		// reaches it, by the old id or any.
		r.s.devices[1].dormant = true
		if err := r.s.setFeedback(Feedback{Color: "#00FF00", Pulse: true}, at(60)); err != nil {
			t.Fatal(err)
		}
		expect(t, e)
	})
	t.Run("shutdown", func(t *testing.T) {
		r := newEffectRig(t)
		a := start(r, 1, caps)
		b := start(r, 2, caps)
		r.now = 50
		r.s.stopAllEffects()
		expect(t, a, want...)
		expect(t, b, want...)
	})
	t.Run("a driver that panics does not stop the shutdown", func(t *testing.T) {
		r := newEffectRig(t)
		a := start(r, 1, caps)
		a.panics = true
		defer func() {
			if v := recover(); v != nil {
				t.Errorf("stopAllEffects panicked: %v", v)
			}
		}()
		r.s.stopAllEffects()
	})
}

func TestCapabilitiesHapticCaps(t *testing.T) {
	// Not known (the properties could not be read): rumble and the LED are
	// tried, as M3 did for every gamepad, but not what needs to be sure.
	if got, want := (Capabilities{}).HapticCaps(), (haptic.Caps{Rumble: true, LED: true}); got != want {
		t.Errorf("unknown capabilities: %+v, want %+v", got, want)
	}
	c := Capabilities{Known: true, Rumble: true, TriggerRumble: true, LED: true, RGBLED: true, PlayerLED: true, Gyro: true, Touchpads: 2}
	if got, want := c.HapticCaps(), (haptic.Caps{Rumble: true, TriggerRumble: true, LED: true, RGBLED: true, PlayerLED: true}); got != want {
		t.Errorf("capabilities %+v: %+v, want %+v", c, got, want)
	}
	if got := (Capabilities{Known: true}).HapticCaps(); got != (haptic.Caps{}) {
		t.Errorf("known capabilities without effects: %+v, want none", got)
	}
	if got := c.Cues(); got != (haptic.Cues{Rumble: true, LED: true}) {
		t.Errorf("Cues = %+v", got)
	}
	if got := (Capabilities{Known: true}).Cues(); got.Any() {
		t.Errorf("Cues of a gamepad without effects = %+v", got)
	}
}

// TestAttachGamepadRegistersALateOpenedPad is the late open of reenumerate: a
// gamepad that SDL lists but that failed to open when it was reported has no
// effects and no power reader, and gets both, as addGamepad gives them, when it
// opens after a sleep. The gamepad handle is a stand-in that is never called.
func TestAttachGamepadRegistersALateOpenedPad(t *testing.T) {
	r := newEffectRig(t)
	s := r.s
	d := &device{id: 7, pub: 7}
	s.devices[d.id] = d
	describe := func(d *device) *DeviceInfo {
		if d.gp == nil {
			t.Error("describe ran before the gamepad was attached")
		}
		return &DeviceInfo{ID: d.pub, Capabilities: Capabilities{Known: true, Rumble: true, LED: true, RGBLED: true}}
	}

	// Reported without a gamepad: addGamepad registers nothing.
	s.registerEffects(d, &DeviceInfo{ID: d.pub})
	dv := haptic.Device(d.pub)
	if s.effects().Has(dv) || d.powerFn != nil {
		t.Fatalf("a device without a gamepad has effects (%v) or a power reader (%v)", s.effects().Has(dv), d.powerFn != nil)
	}
	if err := s.setFeedback(Feedback{Device: d.pub, Color: "#FF0000"}, at(0)); err != nil {
		t.Fatalf("setFeedback on a pad that has no effects: %v", err)
	}

	gp := new(sdl.Gamepad)
	info := s.attachGamepad(d, gp, describe)
	if d.gp != gp || d.powerFn == nil {
		t.Errorf("after attaching: gp %v, power reader set %v; want the gamepad and a reader", d.gp, d.powerFn != nil)
	}
	if !s.effects().Has(dv) {
		t.Fatal("the late-opened gamepad is not registered with the Mixer")
	}
	caps, _ := s.effects().CapsOf(dv)
	if want := info.Capabilities.HapticCaps(); caps != want || !caps.RGBLED {
		t.Errorf("registered with %+v, want %+v", caps, want)
	}
	if info.ID != d.pub || !info.Capabilities.Known {
		t.Errorf("attachGamepad returned %+v", info)
	}
}

// A script's rumble (SCR-23) is a program of steps of its own, played in the
// Script class: it replaces the script's own earlier one, does not cut the
// pulses of a layer, and an effect with both a preset and steps is refused.
func TestPlaySteps(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, TriggerRumble: true})
	buzz := Effect{Device: 1, Class: haptic.Script, Steps: []haptic.Step{{Low: 100, High: 200, Ms: 50}, {Ms: 20}, {LeftTrig: 7, RightTrig: 8, Ms: 30}}}
	if err := r.s.play(buzz, at(0)); err != nil {
		t.Fatal(err)
	}
	r.advance(200)
	expect(t, e, "@0 rumble 100/200 50ms", "@70 trig 7/8 30ms", "@100 rumble stop", "@100 trig 0/0 0ms")

	// The script's program replaces its earlier one.
	if err := r.s.play(Effect{Device: 1, Class: haptic.Script, Steps: []haptic.Step{{Low: 9, Ms: 500}}}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(210)
	if err := r.s.play(Effect{Device: 1, Class: haptic.Script, Steps: []haptic.Step{{High: 5, Ms: 40}}}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(300)
	expect(t, e, "@200 rumble 9/0 500ms", "@210 rumble stop", "@210 rumble 0/5 40ms", "@250 rumble stop")

	// A layer's pulses are not cut by it.
	if err := r.s.setFeedback(Feedback{Device: 1, Pulse: true, Pulses: 1, PulseMs: 70, Strength: 0x7000}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(310)
	if err := r.s.play(Effect{Device: 1, Class: haptic.Script, Steps: []haptic.Step{{Low: 1, Ms: 40}}}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	r.advance(400)
	expect(t, e, "@300 rumble "+m3Both+" 70ms", "@370 rumble stop")

	for name, bad := range map[string]Effect{
		"preset and steps": {Device: 1, Class: haptic.Script, Preset: haptic.Tick, Steps: []haptic.Step{{Low: 1, Ms: 5}}},
		"device 0":         {Class: haptic.Script, Steps: []haptic.Step{{Low: 1, Ms: 5}}},
		"class ambient":    {Device: 1, Class: haptic.Ambient, Steps: []haptic.Step{{Low: 1, Ms: 5}}},
	} {
		if err := r.s.play(bad, at(r.now)); err == nil {
			t.Errorf("%s: Play succeeded", name)
		}
	}
	expect(t, e)
}

// SetLight (SCR-23): the script's color shows over the layer's and gives the
// layer's back on release; only the script and alert classes may ask.
func TestSetLight(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, LED: true, RGBLED: true})
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#102030"}, at(0)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@0 led #102030")
	r.advance(10)
	if err := r.s.setLight(Light{Device: 1, Class: haptic.Script, Color: haptic.RGB{0xAA, 0xBB, 0xCC}}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@10 led #AABBCC")
	// A layer change under the script's color is not shown.
	r.advance(20)
	if err := r.s.setFeedback(Feedback{Device: 1, Color: "#112233"}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e)
	r.advance(30)
	if err := r.s.setLight(Light{Device: 1, Class: haptic.Script, Release: true}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@30 led #112233")

	for name, bad := range map[string]Light{
		"device 0": {Class: haptic.Script, Color: haptic.RGB{1, 2, 3}},
		"ambient":  {Device: 1, Class: haptic.Ambient, Color: haptic.RGB{1, 2, 3}},
		"event":    {Device: 1, Class: haptic.Event, Color: haptic.RGB{1, 2, 3}},
		"identify": {Device: 1, Class: haptic.Identify, Color: haptic.RGB{1, 2, 3}},
	} {
		if err := r.s.setLight(bad, at(r.now)); err == nil {
			t.Errorf("%s: SetLight succeeded", name)
		}
	}
	if err := r.s.setLight(Light{Device: 7, Class: haptic.Script, Color: haptic.RGB{1, 2, 3}}, at(r.now)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("SetLight(unknown gamepad) = %v, want ErrNoDevice", err)
	}
	r.s.devices[5] = &device{id: 5, pub: 5}
	if err := r.s.setLight(Light{Device: 5, Class: haptic.Script, Color: haptic.RGB{1, 2, 3}}, at(r.now)); err != nil {
		t.Errorf("SetLight(joystick) = %v", err)
	}
	expect(t, e)
}

// Stop (SCR-23) ends the rumble of its class, and with Everything the claims
// too, and nothing of the other classes.
func TestStop(t *testing.T) {
	r := newEffectRig(t)
	e := r.add(1, haptic.Caps{Rumble: true, LED: true, RGBLED: true})
	if err := r.s.play(Effect{Device: 1, Class: haptic.Script, Steps: []haptic.Step{{Low: 3, Ms: 1000}}}, at(0)); err != nil {
		t.Fatal(err)
	}
	if err := r.s.setLight(Light{Device: 1, Class: haptic.Script, Color: haptic.RGB{1, 2, 3}}, at(0)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@0 rumble 3/0 1000ms", "@0 led #010203")

	// Not the script's rumble to stop for another class.
	r.advance(5)
	if err := r.s.stop(Stop{Device: 1, Class: haptic.Alert}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e)
	// The rumble alone: the color stays.
	r.advance(10)
	if err := r.s.stop(Stop{Device: 1, Class: haptic.Script}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@10 rumble stop")
	// Everything: the color goes (padmate colored the LED, so it is released).
	r.advance(20)
	if err := r.s.stop(Stop{Device: 1, Class: haptic.Script, Everything: true}, at(r.now)); err != nil {
		t.Fatal(err)
	}
	expect(t, e, "@20 led #000000")

	for name, bad := range map[string]Stop{
		"device 0": {Class: haptic.Script},
		"ambient":  {Device: 1, Class: haptic.Ambient},
		"identify": {Device: 1, Class: haptic.Identify},
		"class 0":  {Device: 1},
	} {
		if err := r.s.stop(bad, at(r.now)); err == nil {
			t.Errorf("%s: Stop succeeded", name)
		}
	}
	if err := r.s.stop(Stop{Device: 7, Class: haptic.Script}, at(r.now)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Stop(unknown gamepad) = %v, want ErrNoDevice", err)
	}
	r.s.devices[1].dormant = true
	if err := r.s.stop(Stop{Device: 1, Class: haptic.Script}, at(r.now)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Stop(dormant gamepad) = %v, want ErrNoDevice", err)
	}
	expect(t, e)
}
