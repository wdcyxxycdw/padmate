package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// TestMappingWizardWithVirtualJoystick (IN-17, IN-18, IN-19, ADR-0072) goes
// through the wizard's calls against SDL's own virtual joystick, which SDL
// reads as no gamepad until it has a mapping: the elements come as input.raw
// events and no key is sent; a mapping turns it into a gamepad that maps like
// any other, and is saved in config.json; resetting it returns the joystick.
func TestMappingWizardWithVirtualJoystick(t *testing.T) {
	lib := inputtest.Library(t)
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space","mode":"hold"}}}}}`)
	writeLastProfile(t, dir, "first")
	var src *input.Source
	tl := startLibraryLiveOpen(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true}, func(o input.Options) (eventSource, error) {
		o.Library = lib
		s, err := input.Open(o)
		src = s
		return s, err
	})
	v, err := src.AttachRawVirtual(input.RawJoystickDesc{Name: "padmate wizard pad", Axes: 4, Buttons: 8, Hats: 1})
	if err != nil {
		t.Fatal(err)
	}
	id := v.ID()
	sel := strconv.FormatUint(uint64(id), 10)
	var guid string
	device := func() (input.DeviceInfo, bool) {
		t.Helper()
		devices, err := tl.sup.Devices(mappingCtx(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range devices {
			if d.Info.ID == id {
				return d.Info, true
			}
		}
		return input.DeviceInfo{}, false
	}
	tl.waitUntil(t, "the joystick, without a mapping", func() bool {
		info, ok := device()
		guid = info.GUID
		return ok && !info.IsGamepad
	})

	client, rows := rawSubscriber(t, tl, "input.raw")
	ctx := mappingCtx(t)
	var info rpcRawInfo
	if err := client.CallResult(ctx, "devices.rawInfo", map[string]any{"device": sel}, &info); err != nil {
		t.Fatal(err)
	}
	if info.Axes != 4 || info.Buttons != 8 || info.Hats != 1 || info.Gamepad || info.GUID != guid {
		t.Fatalf("rawInfo = %+v", info)
	}
	nextRaw := func(what string) rpcRaw {
		t.Helper()
		select {
		case row := <-rows:
			var e rpcRaw
			if row.Topic != "input.raw" || json.Unmarshal(row.Data, &e) != nil {
				t.Fatalf("%s: %s %s", what, row.Topic, row.Data)
			}
			return e
		case <-time.After(liveWait):
			t.Fatalf("no input.raw event: %s", what)
		}
		return rpcRaw{}
	}
	if err := v.SetRawButton(3, true); err != nil {
		t.Fatal(err)
	}
	if e := nextRaw("press"); e.Device != id || e.Kind != "button" || e.Index != 3 || e.Value != 1 {
		t.Errorf("press = %+v", e)
	}
	if err := v.SetRawButton(3, false); err != nil {
		t.Fatal(err)
	}
	nextRaw("release")
	tl.sync()
	if strings.Contains(tl.stdout.String(), "send") {
		t.Fatalf("a joystick without a mapping sent keys:\n%s", tl.stdout)
	}

	// The mapping the wizard built: east is button 3.
	var res rpcMappingResult
	mapping := guid + ",*,a:b0,b:b3,back:b6,start:b7,platform:Windows"
	if err := client.CallResult(ctx, "devices.setMapping", map[string]any{"device": sel, "mapping": mapping}, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Applied || res.Problem != "" || len(res.Keys) != 1 || res.Keys[0] != guid || len(res.Devices) != 1 || !res.Devices[0].Gamepad || !res.Devices[0].Applied {
		t.Fatalf("setMapping = %+v", res)
	}
	for _, w := range res.Warnings {
		t.Errorf("warning %q for a mapping with View and Menu", w.Code)
	}
	tl.waitUntil(t, "the gamepad", func() bool { info, ok := device(); return ok && info.IsGamepad })
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	if c, _ := profile.ParseConfig([]byte(doc.Text)); c == nil || c.Devices[guid].Mapping != mapping {
		t.Fatalf("config.json:\n%s", doc.Text)
	}
	// The wizard's raw events go on for the gamepad.
	if err := v.SetRawAxis(2, 9000); err != nil {
		t.Fatal(err)
	}
	for {
		if e := nextRaw("axis"); e.Kind == "axis" && e.Index == 2 {
			break
		}
	}
	// A gamepad: the mapping is live, and the press sends Space.
	if err := v.SetRawButton(3, true); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	if err := v.SetRawButton(3, false); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.stdout, "send -Space\n")

	// Resetting returns the joystick; its button sends nothing, and a key held
	// at that moment is released.
	if err := v.SetRawButton(3, true); err != nil {
		t.Fatal(err)
	}
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
	if err := client.CallResult(ctx, "devices.resetMapping", map[string]any{"device": sel}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Mapping != "" || !res.Changed || !res.Applied {
		t.Fatalf("resetMapping = %+v", res)
	}
	tl.waitForCount(t, tl.stdout, "send -Space\n", 2)
	tl.waitUntil(t, "the joystick again", func() bool { info, ok := device(); return ok && !info.IsGamepad })
	if err := v.SetRawButton(3, false); err != nil {
		t.Fatal(err)
	}
	if err := v.SetRawButton(3, true); err != nil {
		t.Fatal(err)
	}
	if err := v.SetRawButton(3, false); err != nil {
		t.Fatal(err)
	}
	tl.sync()
	time.Sleep(100 * time.Millisecond)
	tl.sync()
	if n := strings.Count(tl.stdout.String(), "send +Space\n"); n != 2 {
		t.Errorf("%d presses of Space, want 2: the joystick sent keys after the reset:\n%s", n, tl.stdout)
	}
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	if c, _ := profile.ParseConfig([]byte(doc.Text)); c == nil || len(profile.DeviceMappings(c.Devices)) != 0 {
		t.Errorf("config.json after the reset:\n%s", doc.Text)
	}
	checkBalanced(t, tl.stdout.String())
}

// TestRemapWhileHeldReleasesTheKey (IN-18, invariant 1): SDL releases nothing
// when it changes the mapping of a gamepad, and the button that was held reads
// as another element afterwards; the key it held goes up at once, and nothing
// stays held when the old button is let go.
func TestRemapWhileHeldReleasesTheKey(t *testing.T) {
	lib := inputtest.Library(t)
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space","mode":"hold"}}}}}`)
	writeLastProfile(t, dir, "first")
	name := "padmate remap pad"
	m1 := ""
	var src *input.Source
	tl := startLibraryLiveOpen(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true}, func(o input.Options) (eventSource, error) {
		o.Library = lib
		s, err := input.Open(o)
		src = s
		return s, err
	})
	v, err := src.AttachRawVirtual(input.RawJoystickDesc{Name: name, Axes: 2, Buttons: 8})
	if err != nil {
		t.Fatal(err)
	}
	sel := strconv.FormatUint(uint64(v.ID()), 10)
	tl.waitUntil(t, "the joystick", func() bool {
		devices, _ := tl.sup.Devices(mappingCtx(t))
		for _, d := range devices {
			if d.Info.ID == v.ID() {
				m1 = d.Info.GUID
			}
		}
		return m1 != ""
	})
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)
	set := func(fields string) {
		t.Helper()
		var res rpcMappingResult
		if err := client.CallResult(ctx, "devices.setMapping", map[string]string{"device": sel, "mapping": m1 + ",*," + fields + ",platform:Windows"}, &res); err != nil {
			t.Fatal(err)
		}
		if !res.Applied {
			t.Fatalf("setMapping = %+v", res)
		}
	}
	set("a:b0,b:b3")
	tl.waitUntil(t, "the gamepad", func() bool {
		devices, _ := tl.sup.Devices(mappingCtx(t))
		for _, d := range devices {
			if d.Info.ID == v.ID() {
				return d.Info.IsGamepad && d.Slot >= 0
			}
		}
		return false
	})
	if err := v.SetRawButton(3, true); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	// Joystick button 3 is east no more; button 5 is.
	set("a:b0,b:b5")
	tl.waitFor(t, tl.stdout, "send -Space\n")
	if err := v.SetRawButton(3, false); err != nil {
		t.Fatal(err)
	}
	// The next press of the new east button maps again.
	if err := v.SetRawButton(5, true); err != nil {
		t.Fatal(err)
	}
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
	if err := v.SetRawButton(5, false); err != nil {
		t.Fatal(err)
	}
	tl.waitForCount(t, tl.stdout, "send -Space\n", 2)
	checkBalanced(t, tl.stdout.String())
}
