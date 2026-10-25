package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// remapped is the DeviceRemapped event of info (IN-18).
func remapped(info *input.DeviceInfo) input.Event {
	return input.Event{Device: info.ID, Kind: input.DeviceRemapped, Info: info}
}

// TestRemapReleasesHeldKeys (IN-18, invariant 1): SDL releases nothing when a
// mapping changes, so a control held by the old mapping may never send its
// release. The router treats DeviceRemapped as a reset of the device: the key
// goes up at once, and a physical hold that goes on counts again only after it
// was released and pressed.
func TestRemapReleasesHeldKeys(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(50)
	h.expect("+Space@0")
	re := *padA
	re.ButtonMask = 1 << 1
	h.send(remapped(&re))
	h.sync()
	h.expect("+Space@0 -Space@50")
	// The late release of the held button changes nothing...
	h.send(button(1, "east", false))
	h.set(60)
	h.expect("+Space@0 -Space@50")
	// ...and the next press maps as a new one.
	h.send(button(1, "east", true))
	h.set(70)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@50 +Space@60 -Space@70")
}

// TestRemapKeepsAHeldButtonFromCountingAgain: a button that stays held across
// the remap and is not released does not press again by itself; an axis
// that is held out is back at 0 for the engine until it moves.
func TestRemapDoesNotRepress(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.send(remapped(padA))
	h.set(20)
	h.expect("+Space@0 -Space@10")
	h.send(axis(1, "leftx", 20000))
	h.sync()
	h.set(30)
	h.expect("+Space@0 -Space@10")
}

// TestRemapRefreshesTheDevice: the Info the router keeps, which devices.list
// and status.get show, is the new one, and the slot stays with the gamepad.
func TestRemapRefreshesTheDevice(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(added(padB)) // waits: one slot
	re := *padA
	re.ButtonMask, re.AxisMask = 0x853, 0x03
	h.send(remapped(&re))
	h.sync()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	views, err := h.sup.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range views {
		if v.Info.ID == 1 {
			found = true
			if v.Info.ButtonMask != 0x853 || v.Info.AxisMask != 0x03 || v.Slot != 0 {
				t.Errorf("device 1 = %+v slot %d, want the new masks and slot 0", v.Info, v.Slot)
			}
		}
		if v.Info.ID == 2 && v.Slot != -1 {
			t.Errorf("device 2 is in slot %d; the remap of device 1 must not change the queue", v.Slot)
		}
	}
	if !found {
		t.Fatal("device 1 is not listed")
	}
	if got := h.slot0(); got.ID != 1 {
		t.Errorf("Status %+v, want Pad A still in the slot", got)
	}
}

// TestRemapOfUnmappedAndUnknownDevices: a joystick without a gamepad mapping
// keeps being listed as unmapped, with the new Info; a device the router
// never heard of is ignored.
func TestRemapOfUnmappedAndUnknownDevices(t *testing.T) {
	h := start(t, opts{})
	js := &input.DeviceInfo{ID: 5, Name: "Raw Stick", GUID: "05aa"}
	h.send(added(js))
	again := *js
	again.Name = "Raw Stick 2"
	h.send(remapped(&again))
	h.send(remapped(&input.DeviceInfo{ID: 99, Name: "Nobody", IsGamepad: true}))
	h.sync()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	views, err := h.sup.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Info.ID != 5 || views[0].Status != "unmapped" || views[0].Info.Name != "Raw Stick 2" {
		t.Errorf("devices = %+v, want the unmapped joystick with its new Info", views)
	}
}

// TestUpgradeAndDowngrade (IN-18): the second DeviceAdded of a joystick, now
// a gamepad, gives it a slot; its removal and a DeviceAdded without a mapping
// leave it listed as unmapped again, and release what it held.
func TestUpgradeAndDowngrade(t *testing.T) {
	h := start(t, opts{})
	js := &input.DeviceInfo{ID: 5, Name: "Raw Stick", GUID: "05aa"}
	h.send(added(js))
	h.sync()
	if got := h.slot0(); got.ID != 0 {
		t.Fatalf("Status %+v; a joystick without a mapping must not take a slot", got)
	}
	gp := *js
	gp.IsGamepad = true
	h.send(added(&gp))
	h.send(button(5, "east", true))
	h.set(40)
	h.expect("+Space@0")
	if got := h.slot0(); got.ID != 5 {
		t.Fatalf("Status %+v, want the upgraded device in the slot", got)
	}
	h.send(removed(5))
	h.send(added(js))
	h.sync()
	h.expect("+Space@0 -Space@40")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	views, err := h.sup.Devices(ctx)
	if err != nil || len(views) != 1 || views[0].Status != "unmapped" {
		t.Errorf("devices = %+v, %v; want the joystick, unmapped", views, err)
	}
}
