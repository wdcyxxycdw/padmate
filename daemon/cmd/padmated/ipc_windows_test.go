package main

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

func TestLiveAuthenticatedIPCStatusAndShutdown(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmate-live-ipc-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	tl, src := sessionLive(t, func(cfg *liveConfig) { cfg.endpoint = e }, nil, "--dry-run", "--watch=false")
	src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, d, err := ipc.Dial(ctx, e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ipc.NewClient(ctx, conn, d, "integration", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var st rpcStatus
	if err := client.CallResult(ctx, "status.get", nil, &st); err != nil {
		t.Fatal(err)
	}
	if !st.DryRun || st.Paused || len(st.Slots) == 0 || st.Slots[0].Device != 1 {
		t.Fatalf("status=%+v", st)
	}
	if err := client.CallResult(ctx, "status.get", map[string]string{"device": "missing"}, &st); err == nil {
		t.Fatal("unknown device accepted")
	}
	src.events <- input.Event{Device: 2, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 2, Name: "filtered out", GUID: "11111111111111111111111111111111", IsGamepad: true, Capabilities: input.Capabilities{Known: true, Gyro: true}}}
	var devices []supervisor.DeviceView
	if err := client.CallResult(ctx, "devices.list", nil, &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[1].Status != "ignored" || !devices[1].Info.Capabilities.Gyro {
		t.Fatalf("devices=%+v", devices)
	}
	if err := client.CallResult(ctx, "status.get", map[string]string{"device": "2"}, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Slots) != 0 || len(st.Devices) != 1 || st.Devices[0].Info.ID != 2 {
		t.Fatalf("ignored device status=%+v", st)
	}
	if err := client.CallResult(ctx, "devices.list", map[string]string{"device": devices[1].Key}, &devices); err != nil || len(devices) != 1 {
		t.Fatalf("key selection=%+v %v", devices, err)
	}
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("discovery left after shutdown: %v", err)
	}
	checkBalanced(t, tl.stdout.String())
}

func TestIPCCaptureDisconnectAndControlRelease(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmate-capture-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	tl, src := sessionLive(t, func(cfg *liveConfig) { cfg.endpoint = e }, nil, "--dry-run", "--watch=false")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inputs := make(chan string, 64)
	connect := func() *jrpc2.Client {
		t.Helper()
		conn, d, err := ipc.Dial(ctx, e.Dir)
		if err != nil {
			t.Fatal(err)
		}
		client, err := ipc.NewClient(ctx, conn, d, "capture-test", &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
			if req.Method() == "input" {
				inputs <- req.ParamString()
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	a, b := connect(), connect()
	if err := b.CallResult(ctx, "engine.setLayer", map[string]string{"device": "1", "layer": "base"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if err := b.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"input"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	call := func(c *jrpc2.Client, method string) {
		t.Helper()
		if err := c.CallResult(ctx, method, nil, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	call(a, "capture.start")
	tl.waitFor(t, tl.stdout, "send -Space\n")
	call(b, "capture.start")
	src.events <- input.Event{Device: 2, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 2, Name: "ignored", IsGamepad: true}}
	src.events <- input.Event{Device: 2, Kind: input.ButtonDown, Control: "south"}
	for {
		select {
		case event := <-inputs:
			if strings.Contains(event, `"device":2`) && strings.Contains(event, `"control":"south"`) {
				goto inputReceived
			}
		case <-ctx.Done():
			t.Fatal("capture did not report ignored device input")
		}
	}
inputReceived:
	_ = a.Close()
	call(b, "engine.pause")
	call(b, "capture.stop")
	tl.waitUntil(t, "both capture leases released", func() bool { return !tl.sup.Status().Capturing })
	if st := tl.sup.Status(); !st.Paused || st.Capturing {
		t.Fatalf("state=%+v", st)
	}
	call(b, "engine.resume")
	button(src, "east", false)
	button(src, "east", true)
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
	call(b, "engine.releaseAll")
	tl.waitForCount(t, tl.stdout, "send -Space\n", 2)
	call(b, "capture.start")
	_ = b.Close()
	tl.waitUntil(t, "capture released after disconnect", func() bool { return !tl.sup.Status().Capturing })
	button(src, "east", false)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitForCount(t, tl.stdout, "send +Space\n", 3)
	tl.waitForCount(t, tl.stdout, "send -Space\n", 3)
	checkBalanced(t, tl.stdout.String())
}

func TestIPCCaptureDisconnectWaitsForBusyRouter(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmate-capture-busy-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	tl, _ := sessionLive(t, func(cfg *liveConfig) { cfg.endpoint = e }, nil, "--dry-run", "--watch=false")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, d, err := ipc.Dial(ctx, e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ipc.NewClient(ctx, conn, d, "busy-router", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.CallResult(ctx, "capture.start", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	// Pause's printer runs on the router. Hold it longer than the old one
	// second disconnect timeout, without changing production scheduling.
	paused := make(chan error, 1)
	func() {
		tl.stdout.mu.Lock()
		defer tl.stdout.mu.Unlock()
		go func() { paused <- tl.sup.Pause(ctx, true) }()
		tl.waitUntil(t, "router entered pause", func() bool { return tl.sup.Status().Paused })
		_ = client.Close()
		time.Sleep(1200 * time.Millisecond)
	}()
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "busy router released disconnected capture", func() bool { return !tl.sup.Status().Capturing })
	if !tl.sup.Status().Paused {
		t.Fatal("disconnect cleared the user's pause")
	}
}
