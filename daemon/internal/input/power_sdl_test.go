package input_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// These tests need the SDL library, as those of source_test.go do. They
// attach a virtual gamepad, which reports nothing about its power: SDL
// starts a joystick at SDL_POWERSTATE_UNKNOWN and -1 percent
// (SDL_OpenJoystick in SDL_joystick.c), and its virtual driver never sends
// power info. So they check the first read and that nothing floods, and
// with FakePower that the loop's poll and the receiver's revival read
// again; the mapping of the values is tested without SDL (power_test.go).

// openPower opens a Source with PowerEvents on or off, and closes it at the
// end of the test.
func openPower(t *testing.T, events bool) *input.Source {
	t.Helper()
	s, err := input.Open(input.Options{Library: inputtest.Library(t), PowerEvents: events})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestPowerEventsVirtualPad: with PowerEvents, the first read of a
// gamepad follows its DeviceAdded as one DevicePower (unknown, for a
// virtual one) and nothing more comes while it sits there; without
// PowerEvents the consumer sees none. Readout reads either way, and fails
// for a device that is not an open gamepad.
func TestPowerEventsVirtualPad(t *testing.T) {
	for _, on := range []bool{true, false} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			s := openPower(t, on)
			v, added := attach(t, s, "power pad")
			if added.Kind != input.DeviceAdded {
				t.Fatalf("first event %+v, want DeviceAdded", added)
			}
			var powers []input.Event
			deadline := time.After(400 * time.Millisecond)
		wait:
			for {
				select {
				case e := <-s.Events():
					if e.Device == v.ID() {
						if e.Kind != input.DevicePower {
							t.Fatalf("unexpected event %+v", e)
						}
						powers = append(powers, e)
					}
				case <-deadline:
					break wait
				}
			}
			if !on {
				if len(powers) != 0 {
					t.Fatalf("a Source without PowerEvents sent %+v", powers)
				}
			} else {
				if len(powers) != 1 {
					t.Fatalf("%d DevicePower events %+v, want the first read only", len(powers), powers)
				}
				if p := powers[0].Power; p == nil || *p != (input.Power{State: input.PowerUnknown, Percent: -1}) {
					t.Errorf("power %+v, want unknown without a percent", p)
				}
			}
			r, err := s.Readout(v.ID())
			if err != nil {
				t.Fatalf("Readout: %v", err)
			}
			if r.Power.State != input.PowerUnknown || r.Power.Percent != -1 {
				t.Errorf("Readout power %+v, want unknown", r.Power)
			}
			switch r.Connection {
			case "wired", "wireless", "unknown":
			default:
				t.Errorf("Readout connection %q", r.Connection)
			}
			if r.PlayerIndex < -1 {
				t.Errorf("Readout player index %d, want -1 or more", r.PlayerIndex)
			}
			if _, err := s.Readout(input.DeviceID(1 << 30)); err == nil {
				t.Error("Readout of a device that does not exist succeeded")
			}
			if err := v.Detach(); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if _, err := s.Readout(v.ID()); !errors.Is(err, input.ErrClosed) {
				t.Errorf("Readout after Close = %v, want ErrClosed", err)
			}
		})
	}
}

// TestPowerPollReadsAgain: the loop of T-input polls the power (here every
// 50 ms in place of 30 s) and sends a DevicePower when the value read
// changes, and only then.
func TestPowerPollReadsAgain(t *testing.T) {
	s, err := input.Open(input.Options{Library: inputtest.Library(t), PowerEvents: true, PowerPollEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	v, _ := attach(t, s, "power poll")
	if e := events(t, s, 1, v.ID())[0]; e.Kind != input.DevicePower {
		t.Fatalf("event %+v, want the first DevicePower", e)
	}
	if err := s.FakePower(v.ID(), sdl.POWERSTATE_ON_BATTERY, 50); err != nil {
		t.Fatal(err)
	}
	e := events(t, s, 1, v.ID())[0]
	if e.Kind != input.DevicePower || e.Power == nil || *e.Power != (input.Power{State: input.PowerDischarging, Percent: 50}) {
		t.Fatalf("event %+v, want the polled discharging 50", e)
	}
	silence(t, s, 300*time.Millisecond, v.ID())
}

// TestPowerReadAfterRevival: a gamepad that the receiver watchdog reported
// removed and that came back as a new id gets its power read again: the
// consumer dropped the old id's value with the removal.
func TestPowerReadAfterRevival(t *testing.T) {
	s, err := input.Open(input.Options{Library: inputtest.Library(t), ReceiverTimeout: recvTimeout, PowerEvents: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver power"))
	if e := events(t, s, 1, v.ID())[0]; e.Kind != input.DevicePower {
		t.Fatalf("event %+v, want the first DevicePower", e)
	}
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	stopped(t, s, v.ID(), stop)
	const next = input.DeviceID(math.MaxUint32)
	setButton(t, v, "east", true)
	var added, power bool
	for _, e := range events(t, s, 3, v.ID(), next) {
		switch {
		case e.Device != next:
			t.Fatalf("event %+v of the old id", e)
		case e.Kind == input.DeviceAdded:
			added = true
		case e.Kind == input.DevicePower:
			if !added {
				t.Fatalf("DevicePower %+v before the DeviceAdded of the new id", e)
			}
			power = true
		}
	}
	if !added || !power {
		t.Fatalf("after the revival: added %v, power %v; want both", added, power)
	}
	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
}
