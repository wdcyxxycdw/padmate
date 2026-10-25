package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

func inputPipeFixture(t *testing.T, malformed bool) (context.Context, string) {
	t.Helper()
	e, err := ipc.Listen(`\\.\pipe\padmatectl-input-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	hub := ipc.NewHub()
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, hub, func(*ipc.Session) jrpc2.Assigner {
			return handler.Map{
				"devices.list": func(context.Context, *jrpc2.Request) (any, error) {
					hub.Publish("input", map[string]any{"timeNs": 99_000_000_000, "kind": "added", "device": 1, "info": input.DeviceInfo{ID: 1, Name: "already connected", IsGamepad: true, Type: "standard"}})
					hub.Publish("input", map[string]any{"timeNs": 100_000_000_000, "kind": "down", "device": 1, "control": "south"})
					if malformed {
						hub.Publish("input", map[string]any{"timeNs": 100_000_000_001, "kind": "bogus", "device": 1})
					} else {
						hub.Publish("input", map[string]any{"timeNs": 100_012_000_000, "kind": "up", "device": 1, "control": "south"})
					}
					return []map[string]any{{"info": input.DeviceInfo{ID: 1, Name: "already connected", IsGamepad: true, Type: "standard"}, "key": "key"}}, nil
				},
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return ctx, e.Dir
}

func TestMonitorUsesIPCByDefault(t *testing.T) {
	ctx, dir := inputPipeFixture(t, false)
	t.Setenv("PADMATE_SDL3", "must-not-open-sdl")
	var out, errOut bytes.Buffer
	code := monitorMain(ctx, []string{"--ipc-dir", dir, "--json", "--count=3"}, &out, &errOut)
	if code != 0 {
		t.Fatal(code, &errOut)
	}
	events, err := input.ReadEvents(strings.NewReader(out.String()))
	if err != nil || len(events) != 3 || events[0].Info.Name != "already connected" || events[2].Kind != input.ButtonUp {
		t.Fatal(events, err, &out)
	}
}

func TestRecordIPCRebasesFirstTransition(t *testing.T) {
	ctx, dir := inputPipeFixture(t, false)
	var out, errOut bytes.Buffer
	code := recordMain(ctx, []string{"--ipc-dir", dir, "--count=3"}, &out, &errOut)
	if code != 0 {
		t.Fatal(code, &errOut)
	}
	events, err := input.ReadEvents(strings.NewReader(out.String()))
	if err != nil || len(events) != 3 || events[0].Time != 0 || events[1].Time != 0 || events[2].Time != 12_000_000 {
		t.Fatal(events, err, &out)
	}
}

func TestRecordIPCRejectsMalformedStream(t *testing.T) {
	ctx, dir := inputPipeFixture(t, true)
	var out, errOut bytes.Buffer
	if code := recordMain(ctx, []string{"--ipc-dir", dir, "--count=3"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "unknown kind") {
		t.Fatal(code, &errOut)
	}
}

// TestMonitorListIPCShowsBattery (IN-12): "monitor --list" through the daemon
// prints the battery that devices.list carries, "not read" for a gamepad
// without one, and says that the connection and the player index are for
// --direct. The --json form stays the trace format.
func TestMonitorListIPCShowsBattery(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmatectl-list-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	hub := ipc.NewHub()
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, hub, func(*ipc.Session) jrpc2.Assigner {
			return handler.Map{
				"devices.list": func(context.Context, *jrpc2.Request) (any, error) {
					return []map[string]any{
						{"info": input.DeviceInfo{ID: 1, Name: "Pad One", IsGamepad: true, Type: "standard", Capabilities: input.Capabilities{Known: true, Rumble: true}},
							"key": "k1", "battery": input.Power{State: input.PowerDischarging, Percent: 25}},
						{"info": input.DeviceInfo{ID: 2, Name: "Pad Two", IsGamepad: true, Type: "standard"}, "key": "k2"},
					}, nil
				},
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })

	var out, errOut bytes.Buffer
	if code := monitorMain(ctx, []string{"--ipc-dir", e.Dir, "--list"}, &out, &errOut); code != 0 {
		t.Fatal(code, &errOut)
	}
	text := out.String()
	for _, want := range []string{
		"+device 1 standard \"Pad One\"",
		"pad 1 \"Pad One\"\n  capabilities  rumble\n  power         discharging 25%\n  connection    not reported by the daemon; use --direct\n",
		"pad 2 \"Pad Two\"\n  capabilities  unknown\n  power         not read\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	out.Reset()
	errOut.Reset()
	if code := monitorMain(ctx, []string{"--ipc-dir", e.Dir, "--list", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(code, &errOut)
	}
	events, err := input.ReadEvents(strings.NewReader(out.String()))
	if err != nil || len(events) != 2 || events[0].Info.Name != "Pad One" {
		t.Fatalf("--list --json = %v, %v:\n%s", events, err, out.String())
	}
}

// TestMonitorIPCShowsAMappingChange (IN-18): through the daemon, "monitor"
// prints the joystick that got a mapping as the gamepad it is now, and the
// remap of a gamepad, as it does with --direct. A repeat of the device that
// devices.list gave is still dropped.
func TestMonitorIPCShowsAMappingChange(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmatectl-remap-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	hub := ipc.NewHub()
	done := make(chan error, 1)
	pad := func(gamepad bool) input.DeviceInfo {
		return input.DeviceInfo{ID: 1, Name: "Cheap Pad", IsGamepad: gamepad, Type: "standard"}
	}
	go func() {
		done <- ipc.Serve(ctx, e, hub, func(*ipc.Session) jrpc2.Assigner {
			return handler.Map{
				"devices.list": func(context.Context, *jrpc2.Request) (any, error) {
					hub.Publish("input", map[string]any{"timeNs": 1_000, "kind": "added", "device": 1, "info": pad(false)}) // a repeat
					hub.Publish("input", map[string]any{"timeNs": 2_000, "kind": "added", "device": 1, "info": pad(true)})
					hub.Publish("input", map[string]any{"timeNs": 3_000, "kind": "remapped", "device": 1, "info": map[string]any{"id": 1, "gamepad": true, "buttonMask": 21, "axisMask": 3}})
					return []map[string]any{{"info": pad(false), "key": "key"}}, nil
				},
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	var out, errOut bytes.Buffer
	if code := monitorMain(ctx, []string{"--ipc-dir", e.Dir, "--count=3"}, &out, &errOut); code != 0 {
		t.Fatal(code, &errOut)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "+device 1") || !strings.Contains(lines[0], "joystick-only") ||
		!strings.Contains(lines[1], "+device 1") || strings.Contains(lines[1], "joystick-only") ||
		!strings.HasSuffix(lines[2], "~device 1 remapped buttons=0x15 axes=0x3") {
		t.Fatalf("monitor printed:\n%s", &out)
	}
}
