package supervisor_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type padKeyCapture struct{ snapshots chan slot.PadSnapshot }

func (c *padKeyCapture) UpdatePad(p slot.PadSnapshot)            { c.snapshots <- p }
func (c *padKeyCapture) Dispatch(slot.ScriptEvent) (bool, error) { return false, nil }
func (c *padKeyCapture) RunDue() error                           { return nil }
func (c *padKeyCapture) NextDeadline() (clock.Time, bool)        { return 0, false }
func (c *padKeyCapture) ReleaseAll() error                       { return nil }
func (c *padKeyCapture) Reload(uint32) error                     { return nil }
func (c *padKeyCapture) Dispose() error                          { return nil }

func TestScriptPadKeyMatchesSupervisorDuplicateAssignments(t *testing.T) {
	var captures [2]*padKeyCapture
	for i := range captures {
		captures[i] = &padKeyCapture{snapshots: make(chan slot.PadSnapshot, 32)}
	}
	h := start(t, opts{slots: 2, scriptFactory: func(index uint8, _ uint32, _ clock.Clock, _ slot.Arbiter) (slot.ScriptRuntime, error) {
		return captures[index], nil
	}})
	h.send(added(pad(1, "Same Pad", "03ab")))
	h.send(added(pad(2, "Same Pad", "03ab")))
	h.sync()
	for i, c := range captures {
		want := "03ab"
		if i == 1 {
			want = "03ab#1"
		}
		got := ""
		for len(c.snapshots) > 0 {
			p := <-c.snapshots
			if p.Connected {
				got = p.Key
			}
		}
		if got != want {
			t.Errorf("slot %d pad.info.key snapshot = %q, want %q", i, got, want)
		}
		if status := h.sup.Status().Slots[i]; status.Key.String() != got {
			t.Errorf("slot %d status key=%q, snapshot=%q", i, status.Key.String(), got)
		}
	}
}

// A script's pad.info.battery (SCR-23) comes from the snapshot the slot copies
// for its runtime: it has no power until the gamepad reported one, shows the
// latest after the slot's next event, and loses it with the gamepad.
func TestScriptPadSnapshotCarriesPower(t *testing.T) {
	capture := &padKeyCapture{snapshots: make(chan slot.PadSnapshot, 64)}
	h := start(t, opts{slots: 1, scriptFactory: func(uint8, uint32, clock.Clock, slot.Arbiter) (slot.ScriptRuntime, error) {
		return capture, nil
	}})
	// last drains the snapshots, and returns the latest one of a connected gamepad.
	last := func() slot.PadSnapshot {
		t.Helper()
		var got slot.PadSnapshot
		for len(capture.snapshots) > 0 {
			if p := <-capture.snapshots; p.Connected {
				got = p
			}
		}
		return got
	}
	h.send(added(pad(1, "Pad", "03ab")))
	h.sync()
	if p := last(); !p.Connected || p.HasPower {
		t.Fatalf("snapshot after the gamepad came: %+v, want connected without power", p)
	}
	h.send(powerOf(1, input.PowerDischarging, 64))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.sync()
	if p := last(); !p.HasPower || p.Power != (input.Power{State: input.PowerDischarging, Percent: 64}) {
		t.Fatalf("snapshot after a power: %+v", p)
	}
	h.send(powerOf(1, input.PowerCharging, -1))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.sync()
	if p := last(); !p.HasPower || p.Power != (input.Power{State: input.PowerCharging, Percent: -1}) {
		t.Fatalf("snapshot after a change: %+v", p)
	}
	// The gamepad comes back with a new id: no power until it reads one.
	h.send(removed(1))
	h.send(added(pad(2, "Pad", "03ab")))
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.sync()
	if p := last(); !p.Connected || p.HasPower {
		t.Fatalf("snapshot of the revived gamepad: %+v, want no power", p)
	}
	h.send(powerOf(2, input.PowerWired, -1))
	h.send(button(2, "south", true))
	h.sync()
	if p := last(); !p.HasPower || p.Power.State != input.PowerWired {
		t.Fatalf("snapshot after the revived gamepad read: %+v", p)
	}
}
