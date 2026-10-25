package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// addTypedPad adds a gamepad with the SDL description of a real one on src
// and waits until the supervisor has it.
func addTypedPad(t *testing.T, tl *testLive, src *fakeSource, id input.DeviceID, name, typ, driver string, vendor, product uint16) {
	t.Helper()
	info := &input.DeviceInfo{ID: id, Name: name, Path: "fake" + name, GUID: strings.Repeat("a", 31) + string(rune('0'+id)), Type: typ, Driver: driver, Vendor: vendor, Product: product, IsGamepad: true}
	src.events <- input.Event{Device: id, Kind: input.DeviceAdded, Info: info}
	tl.sync()
}

func steamInputOf(conflicts []rpcConflict) (rpcConflict, bool) {
	for _, c := range conflicts {
		if c.Kind == "steamInput" {
			return c, true
		}
	}
	return rpcConflict{}, false
}

// TestSteamInputConflict (IN-24, ADR-0071): while Steam runs, the conflicts
// list the classes of Steam Input settings that may govern the connected
// gamepads. It says "may": padmate cannot see that Steam took one.
func TestSteamInputConflict(t *testing.T) {
	tl, src := sessionStart(t, nil, nil, "--dry-run", "--watch=false")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	steam := []string{"Steam"}

	// No gamepad: Steam alone is the old conflict.
	conflicts := tl.conflictList(ctx, steam)
	if _, ok := steamInputOf(conflicts); ok || len(conflicts) != 1 || conflicts[0].Kind != "conflict" || conflicts[0].Process != "Steam" {
		t.Fatalf("conflicts %+v, want the Steam process only", conflicts)
	}

	// The Nova 2 Lite in Xbox mode.
	addTypedPad(t, tl, src, 1, liveTestPad+" XInput Controller #1", "xboxone", "xinput", 0x3537, 0x1098)
	got, ok := steamInputOf(tl.conflictList(ctx, steam))
	if !ok || !slices.Equal(got.Classes, []string{"xbox"}) || got.Process != "Steam" {
		t.Fatalf("steamInput conflict %+v, %v", got, ok)
	}
	if !strings.Contains(got.Message, "may") || got.Document == "" {
		t.Errorf("message %q, document %q", got.Message, got.Document)
	}

	// Another gamepad of another class, one Steam does not know, and a joystick.
	addTypedPad(t, tl, src, 2, liveTestPad+" DualSense", "ps5", "hidapi", 0x054c, 0x0ce6)
	addTypedPad(t, tl, src, 3, liveTestPad+" odd pad", "unknown", "dinput", 0x1234, 0x5678)
	src.events <- input.Event{Device: 4, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 4, Name: liveTestPad + " wheel", Type: "standard", Driver: "xinput", Vendor: 0x045e}}
	tl.sync()
	got, _ = steamInputOf(tl.conflictList(ctx, steam))
	if !slices.Equal(got.Classes, []string{"xbox", "playstation"}) {
		t.Errorf("classes %v, want xbox and playstation (a joystick without a gamepad mapping is no gamepad)", got.Classes)
	}

	// Without Steam there is no such conflict, whatever else runs.
	for _, running := range [][]string{nil, {"DS4Windows"}} {
		if c, ok := steamInputOf(tl.conflictList(ctx, running)); ok {
			t.Errorf("steamInput %+v with %v running", c, running)
		}
	}
	if c := tl.conflictList(ctx, []string{"DS4Windows", "Steam"}); len(c) != 3 || c[0].Process != "DS4Windows" || c[1].Process != "Steam" || c[1].Kind != "conflict" || c[2].Kind != "steamInput" {
		t.Errorf("conflicts %+v, want DS4Windows, Steam, then the Steam Input one", c)
	}
}

// A gamepad that the ignore setting leaves out is not one that Steam might
// take away from padmate.
func TestSteamInputConflictSkipsIgnoredPads(t *testing.T) {
	tl, src := sessionStart(t, nil, nil, "--dry-run", "--watch=false", "--device", "keep")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addTypedPad(t, tl, src, 1, "skip this one", "ps4", "hidapi", 0x054c, 0x09cc)
	if c, ok := steamInputOf(tl.conflictList(ctx, []string{"Steam"})); ok {
		t.Errorf("steamInput %+v for a gamepad padmate ignores", c)
	}
	addTypedPad(t, tl, src, 2, "keep this one", "switchpro", "hidapi", 0x057e, 0x2009)
	if c, _ := steamInputOf(tl.conflictList(ctx, []string{"Steam"})); !slices.Equal(c.Classes, []string{"switch"}) {
		t.Errorf("classes %v, want switch", c.Classes)
	}
}

// The scan of the processes and the gamepads come together in the published
// conflicts of the production diagnostics loop, also when the gamepad is
// connected after the scan.
func TestSteamInputConflictIsPublished(t *testing.T) {
	tl, src := sessionStart(t, func(c *liveConfig) {
		c.processes = func() ([]string, error) { return []string{"steam.exe"}, nil }
	}, nil, "--dry-run", "--watch=false")
	tl.waitUntil(t, "the Steam conflict", func() bool {
		c := tl.conflicts.Load()
		return c != nil && len(*c) == 1
	})
	addTypedPad(t, tl, src, 1, liveTestPad+" Switch Pro", "switchpro", "hidapi", 0x057e, 0x2009)
	tl.waitUntil(t, "the Steam Input conflict", func() bool {
		c := tl.conflicts.Load()
		if c == nil {
			return false
		}
		got, ok := steamInputOf(*c)
		return ok && slices.Equal(got.Classes, []string{"switch"})
	})
}
