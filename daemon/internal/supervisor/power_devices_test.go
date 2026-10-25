package supervisor_test

import (
	"context"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func powerOf(id input.DeviceID, state input.PowerState, percent int) input.Event {
	p := input.Power{State: state, Percent: percent}
	return input.Event{Device: id, Kind: input.DevicePower, Power: &p}
}

// TestDevicesCarryBattery (IN-12): Devices, which devices.list and
// status.get show, carries the latest power of each gamepad, for a mapped
// one, a waiting one and an ignored one; it is left out until a power is
// read, and is gone with the gamepad. OnInput never sees a DevicePower.
func TestDevicesCarryBattery(t *testing.T) {
	var inputKinds []input.EventKind
	h := start(t, opts{slots: 1, device: "Pad", onInput: func(e input.Event) { inputKinds = append(inputKinds, e.Kind) }})
	ignored := *padC
	ignored.Name = "ignored"
	h.send(added(padA))
	h.send(added(padB))
	h.send(added(&ignored))
	h.send(input.Event{Device: 4, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 4, Name: "wheel"}})
	h.send(powerOf(1, input.PowerDischarging, 64))
	h.send(powerOf(2, input.PowerCharging, -1))
	h.send(powerOf(3, input.PowerUnknown, -1))
	h.send(powerOf(4, input.PowerWired, -1)) // a joystick without a gamepad: ignored
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	devices, err := h.sup.Devices(ctx)
	if err != nil || len(devices) != 4 {
		t.Fatalf("devices=%+v %v", devices, err)
	}
	want := []*input.Power{
		{State: input.PowerDischarging, Percent: 64},
		{State: input.PowerCharging, Percent: -1},
		{State: input.PowerUnknown, Percent: -1},
		nil,
	}
	for i, w := range want {
		got := devices[i].Battery
		if (got == nil) != (w == nil) || got != nil && *got != *w {
			t.Errorf("device %d (%s) battery = %+v, want %+v", devices[i].Info.ID, devices[i].Status, got, w)
		}
	}
	// A later value replaces the earlier one.
	h.send(powerOf(1, input.PowerDischarging, 63))
	devices, err = h.sup.Devices(ctx)
	if err != nil || devices[0].Battery == nil || devices[0].Battery.Percent != 63 {
		t.Fatalf("devices=%+v %v", devices, err)
	}
	// The gamepad leaves; the one that comes back with a new id has no
	// battery until it reads.
	h.send(removed(1))
	h.send(added(&input.DeviceInfo{ID: 5, Name: padA.Name, GUID: padA.GUID, Serial: padA.Serial, Path: padA.Path, IsGamepad: true}))
	devices, err = h.sup.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		if d.Info.ID == 1 {
			t.Errorf("removed gamepad in the devices: %+v", d)
		}
		if d.Info.ID == 5 && d.Battery != nil {
			t.Errorf("the revived gamepad has a battery: %+v", d.Battery)
		}
	}
	h.send(powerOf(5, input.PowerDischarging, 63))
	devices, _ = h.sup.Devices(ctx)
	for _, d := range devices {
		if d.Info.ID == 5 && (d.Battery == nil || d.Battery.Percent != 63) {
			t.Errorf("the revived gamepad's battery = %+v after a read", d.Battery)
		}
	}
	h.sync()
	for _, k := range inputKinds {
		if k == input.DevicePower {
			t.Fatalf("OnInput saw a DevicePower; kinds %v", inputKinds)
		}
	}
}
