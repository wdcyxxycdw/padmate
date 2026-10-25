package supervisor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

func TestInspectMappingOnSlotOwner(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"Drawing","layers":{
  "base":{"east":{"latch":true,"press":{"type":"keys","keys":"Space","mode":"hold"}},
    "south":{"press":{"type":"layer","op":"hold","layer":"ink"}}},
  "ink":{}}}`))
	if p == nil {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := start(t, opts{program: prog})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.send(button(1, "east", false))
	h.send(button(1, "south", true))
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	views, err := h.sup.Inspect(ctx)
	if err != nil || len(views) != 1 {
		t.Fatalf("Inspect: %v, %v", views, err)
	}
	v := views[0]
	if v.Device != 1 || v.Mapping.Profile != "Drawing" || v.Mapping.Layers.Base != "base" ||
		len(v.Mapping.Layers.Overlays) != 1 || v.Mapping.Layers.Overlays[0] != "ink" ||
		len(v.Mapping.Latches) != 1 || v.Mapping.Latches[0] != "east" || v.Mapping.Mode != 2 {
		t.Fatalf("snapshot lost mapping state: %+v", v)
	}
	v.Mapping.Layers.Overlays[0] = "mutated"
	v.Mapping.Latches[0] = "mutated"
	next, err := h.sup.Inspect(ctx)
	if err != nil || next[0].Mapping.Latches[0] != "east" || next[0].Mapping.Layers.Overlays[0] != "ink" {
		t.Fatalf("snapshot aliases engine state: %v, %v", next, err)
	}
	h.pause(true)
	paused, err := h.sup.Inspect(ctx)
	if err != nil || len(paused[0].Mapping.Latches) != 0 || len(paused[0].Mapping.Layers.Overlays) != 0 {
		t.Fatalf("pause left stale status: %v, %v", paused, err)
	}
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sup.Inspect(ctx); !errors.Is(err, supervisor.ErrStopped) {
		t.Fatalf("inspect after stop: %v", err)
	}
}

func TestKnownEmptyCapabilitiesDoNotInferControls(t *testing.T) {
	for _, known := range []bool{false, true} {
		h := start(t, opts{})
		info := *padA
		info.Capabilities.Known = known
		h.send(added(&info))
		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		views, err := h.sup.Inspect(ctx)
		cancel()
		if err != nil || len(views) != 1 {
			t.Fatalf("views=%+v %v", views, err)
		}
		if views[0].HasControl[control.South] == known || views[0].HasAxis[control.LeftX] == known {
			t.Fatalf("known=%v got %+v", known, views[0])
		}
	}
}

func TestDevicesIncludesWaitingIgnoredAndUnmapped(t *testing.T) {
	h := start(t, opts{slots: 1, device: "Pad"})
	h.send(added(padB))
	h.send(added(padA))
	ignored := *padC
	ignored.Name = "ignored"
	h.send(added(&ignored))
	h.send(input.Event{Device: 4, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 4, Name: "wheel"}})
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	devices, err := h.sup.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 4 {
		t.Fatalf("devices=%+v", devices)
	}
	for i, want := range []string{"waiting", "mapped", "ignored", "unmapped"} {
		if devices[i].Info.ID != input.DeviceID(i+1) || devices[i].Status != want {
			t.Fatalf("device %d = %+v", i, devices[i])
		}
	}
	if devices[1].Slot != 0 || devices[0].Slot != -1 {
		t.Fatal("incorrect slot")
	}
	devices[0].Info.Name = "mutated"
	next, err := h.sup.Devices(ctx)
	if err != nil || next[0].Info.Name == "mutated" {
		t.Fatal("aliased device snapshot")
	}
	h.send(input.Event{Device: 4, Kind: input.DeviceRemoved})
	next, err = h.sup.Devices(ctx)
	if err != nil || len(next) != 3 {
		t.Fatal("removed joystick remains in snapshot")
	}
}
