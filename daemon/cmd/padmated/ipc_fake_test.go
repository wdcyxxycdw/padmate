package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func TestFakeIPCOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--profile", basicProfile, "--fake-input", "--dry-run", "--tray=false"},
		{"--profile", basicProfile, "--dry-run", "--fake-input"},
		{"--profile", basicProfile, "--fake-input=ipc", "--dry-run"},
	} {
		var out bytes.Buffer
		opts, code, ok := parseFlags(args, &out, &out)
		if !ok || code != 0 || opts.fakeInput != "ipc" {
			t.Fatal(args, opts.fakeInput, code, &out)
		}
		startup, err := startupArgs(opts)
		if err != nil || !strings.Contains(strings.Join(startup, " "), "--fake-input=ipc") {
			t.Fatal(startup, err)
		}
	}
	for _, args := range [][]string{
		{"--profile", basicProfile, "--fake-input", "--inject"},
		{"--profile", basicProfile, "--fake-input", "--inject", "--dry-run"},
		{"--profile", basicProfile, "--fake-input"},
	} {
		var out bytes.Buffer
		if _, code, ok := parseFlags(args, &out, &out); ok || code != 2 {
			t.Fatal(args, code, &out)
		}
	}
}

func TestFakeIPCDrivesMappingWithoutSDL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	const profile = `{"schemaVersion":0,"name":"fake","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}},"lt":{"press":{"type":"keys","keys":"F14"}}}}}`
	if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--fake-input=ipc", "--dry-run", "--watch=false")
	cfg.opts.device = ""
	cfg.open = func(input.Options) (eventSource, error) { panic("fake IPC opened SDL") }
	tl.start(t, cfg)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	call := func(method string, p any) {
		t.Helper()
		if err := client.CallResult(ctx, method, p, new(any)); err != nil {
			t.Fatal(method, err)
		}
	}
	var status rpcStatus
	if err := client.CallResult(ctx, "status.get", nil, &status); err != nil || !status.FakeInput || !status.DryRun || len(status.Devices) != 1 {
		t.Fatal(status, err)
	}
	call("fake.press", map[string]any{"control": "south"})
	tl.waitFor(t, tl.stdout, "send +F13\n")
	call("fake.release", map[string]any{"control": "south"})
	tl.waitFor(t, tl.stdout, "send -F13\n")
	call("fake.axis", map[string]any{"axis": "lefttrigger", "value": 32767})
	tl.waitFor(t, tl.stdout, "send +F14\n")
	call("fake.axis", map[string]any{"axis": "lefttrigger", "value": 0})
	tl.waitFor(t, tl.stdout, "send -F14\n")
	for _, tc := range []struct {
		method string
		p      any
	}{
		{"fake.press", map[string]any{"control": "lt"}},
		{"fake.press", map[string]any{"control": "unknown"}},
		{"fake.axis", map[string]any{"axis": "rightx", "value": 32768}},
		{"fake.axis", map[string]any{"axis": "lefttrigger", "value": -1}},
		{"fake.axis", map[string]any{"axis": "rightx"}},
	} {
		if err := client.CallResult(ctx, tc.method, tc.p, new(any)); err == nil {
			t.Fatal("invalid fake event accepted", tc)
		}
	}
	call("fake.press", map[string]any{"control": "south"})
	call("engine.pause", nil)
	call("engine.resume", nil)
	before := tl.stdout.String()
	call("fake.release", map[string]any{"control": "south"})
	if strings.Count(tl.stdout.String(), "send +F13") != strings.Count(before, "send +F13") {
		t.Fatal("resume replayed held fake button")
	}
	call("fake.press", map[string]any{"control": "south"})
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Fatal(code)
	}
	checkBalanced(t, tl.stdout.String())
}

func TestFakeIPCRejectedInHardwareMode(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "fake.press", map[string]string{"control": "south"}, new(any)); err == nil {
		t.Fatal("hardware mode accepted fake input")
	}
}

func TestFakeInputComboAndSnapshot(t *testing.T) {
	fired := make(chan struct{}, 1)
	src := newRPCInputSource(input.Options{OnCombo: func() { fired <- struct{}{} }, ComboFor: func(input.DeviceInfo, bool) input.Combo {
		return input.Combo{Buttons: []string{"back", "start"}, Hold: time.Millisecond}
	}})
	defer src.Close()
	<-src.Events()
	if err := src.button("back", true); err != nil {
		t.Fatal(err)
	}
	if err := src.button("start", true); err != nil {
		t.Fatal(err)
	}
	<-src.Events()
	<-src.Events()
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("fake combo did not fire")
	}
	select {
	case e := <-src.Events():
		if e.Kind != input.ComboHeld {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("missing combo event")
	}
	if err := src.Reenumerate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := <-src.Events()
	if e.Kind != input.DeviceState || len(e.Snapshot.Buttons) != 2 {
		t.Fatal(e)
	}
}

func TestFakeInputQueueFullDoesNotAcceptLostEdge(t *testing.T) {
	src := newRPCInputSource(input.Options{})
	defer src.Close()
	for len(src.events) < cap(src.events) {
		src.events <- input.Event{Kind: input.AxisMotion, Device: 1, Control: "rightx"}
	}
	if err := src.button("south", true); err == nil {
		t.Fatal("accepted event with no queue capacity")
	}
	<-src.events
	if err := src.button("south", true); err != nil {
		t.Fatal(err)
	}
	var last input.Event
	for len(src.events) > 0 {
		last = <-src.events
	}
	if last.Kind != input.ButtonDown || last.Control != "south" {
		t.Fatal("failed send changed source state", last)
	}
}

// The fake gamepad's key must be one config.json accepts, or the UI can not
// save settings for it, such as a calibration (IN-14): ParseDeviceKey wants
// the 32 hex digits of a real SDL GUID.
func TestFakeInputDeviceKeyIsCanonical(t *testing.T) {
	src := newRPCInputSource(input.Options{})
	defer src.Close()
	added := <-src.Events()
	key := input.KeyOf(added.Info).String()
	parsed, err := input.ParseDeviceKey(key)
	if err != nil {
		t.Fatalf("ParseDeviceKey(%q): %v", key, err)
	}
	if parsed.String() != key {
		t.Fatalf("key %q reads back as %q", key, parsed.String())
	}
}
