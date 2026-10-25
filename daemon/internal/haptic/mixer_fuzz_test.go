package haptic

import (
	"strings"
	"testing"
	"time"
)

// FuzzMixer drives a Mixer with random requests, time and failing calls. The
// Mixer never panics, never asks a controller for what its Caps say it
// cannot do, never starts an endless or overlong rumble, and once the last
// failure is past and StopAll has run, the motors are still, the LED is
// released and the player lights are what they were (CLAUDE.md invariant 1,
// for haptics). The fake controller drops an LED call for the color SDL has
// kept, as SDL does for a window after a call (a failed one included), so a
// retry that came too early would leave the LED unreleased and fail the run.
func FuzzMixer(f *testing.F) {
	f.Add([]byte{0x1f, 0, 2, 3, 9, 20, 0, 3, 5, 9, 200, 10, 9, 255, 11})
	f.Add([]byte{0xff, 1, 4, 255, 255, 255, 255, 255, 9, 50, 12, 1, 4, 9, 30, 12, 9, 250})
	f.Add([]byte{0x0f, 3, 3, 1, 0, 255, 0, 255, 4, 4, 77, 200, 9, 100, 5, 4, 9, 255, 9, 255})
	f.Add([]byte{0x30, 6, 1, 2, 7, 4, 9, 255, 8, 4, 10, 9, 250, 9, 250, 11, 2, 0})
	f.Add([]byte{0x21, 0, 4, 0, 9, 5, 0, 2, 7, 9, 5, 0, 1, 7, 9, 5})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		caps := Caps{
			Rumble: data[0]&1 != 0, TriggerRumble: data[0]&2 != 0, RGBLED: data[0]&8 != 0,
			PlayerLED: data[0]&16 != 0,
		}
		caps.LED = caps.RGBLED || data[0]&4 != 0
		r := newRig(t, caps)
		orig := int(data[0]>>5) - 1
		r.f.player = orig
		r.f.limits = &caps
		r.f.ledLag = time.Second // a failing LED write may take that long

		pos := 1
		next := func() byte {
			if pos >= len(data) {
				return 0
			}
			pos++
			return data[pos-1]
		}
		class := func() Class { return Class(next()%5 + 1) }
		ms := 0
		fail := false
		r.f.fail = func(string) error {
			if fail {
				return errBoom
			}
			return nil
		}
		for ops := 0; pos < len(data) && ops < 300; ops++ {
			now := r.at(ms)
			switch next() % 16 {
			case 0:
				c := class()
				p := Preset(next() % 9)
				_, _ = r.m.Play(dv, c, p.Steps(uint16(next())<<8|uint16(next()), int(next())*10, int(next())*10), now)
			case 1:
				var steps []Step
				for range next() % 6 {
					steps = append(steps, Step{
						Low: uint16(next()) * 257, High: uint16(next()) * 257,
						LeftTrig: uint16(next()) * 257, RightTrig: uint16(next()) * 257,
						Ms: uint16(next())<<8 | uint16(next()),
					})
				}
				_, _ = r.m.Play(dv, class(), steps, now)
			case 2:
				_ = r.m.Cancel(dv, class(), now)
			case 3:
				_ = r.m.SetLED(dv, class(), RGB{next(), next(), next()}, now)
			case 4:
				c := class()
				_ = r.m.BlinkLED(dv, c, Blink(RGB{next(), next(), next()}, int(next())*8, int(next())*8, int(next()%5)), now)
			case 5:
				_ = r.m.ReleaseLED(dv, class(), now)
			case 6:
				_ = r.m.SetPlayer(dv, class(), int(next())%12-2, now)
			case 7:
				c := class()
				phases := make([]PlayerPhase, next()%5)
				for i := range phases {
					phases[i] = PlayerPhase{Value: int(next()) % 6, Show: next()&1 != 0, Ms: uint16(next()) * 8}
				}
				_ = r.m.PlayerPattern(dv, c, phases, now)
			case 8:
				_ = r.m.ReleasePlayer(dv, class(), now)
			case 9:
				ms += int(next()) * 4
				_ = r.m.Advance(r.at(ms))
			case 10:
				// Other controllers hold some of the first four player indices.
				held := next()
				r.f.taken = map[int]bool{0: held&1 != 0, 1: held&2 != 0, 2: held&4 != 0, 3: held&8 != 0}
				_, _ = r.m.Identify(dv, now)
			case 11:
				_ = r.m.Stop(dv, now)
			case 12:
				fail = !fail
			case 13:
				// Another controller's restore moves this one to a free slot
				// behind the Mixer's back. A controller that the Mixer has not
				// changed has the new index as its own, which is the one to
				// restore from now on.
				r.f.player = int(next())%9 - 1
				if !r.m.devs[dv].playerChanged {
					orig = r.f.player
				}
			case 14:
				_, _ = r.m.AlertBlink(dv, now)
			case 15:
				_ = r.m.StopRumble(dv, class(), now)
			}
		}

		// Let the last failure's cooldown end (the LED's is the longest), then
		// stop everything.
		fail = false
		ms += int(LEDCooldown/time.Millisecond) + 1
		_ = r.m.Advance(r.at(ms))
		ms++
		if err := r.m.StopAll(r.at(ms)); err != nil {
			t.Fatalf("StopAll after the cooldown: %v", err)
		}
		if bad := r.f.violations; len(bad) > 0 {
			t.Fatalf("calls that the controller's caps %+v forbid:\n  %s", caps, strings.Join(bad, "\n  "))
		}
		if r.f.endless != 0 {
			t.Fatalf("%d rumbles that never end", r.f.endless)
		}
		if !r.f.quiet(r.at(ms + sdlLagMs)) {
			t.Fatalf("the motors run after StopAll: %+v", r.f)
		}
		if caps.RGBLED && r.f.ledSet && r.f.led != (RGB{}) {
			t.Fatalf("the LED is %v after StopAll, want released (#000000)", r.f.led)
		}
		if !caps.LED && r.f.ledSet {
			t.Fatalf("a controller without an LED was given a color")
		}
		if r.f.player != orig {
			t.Fatalf("player index %d after StopAll, want the original %d", r.f.player, orig)
		}
	})
}
