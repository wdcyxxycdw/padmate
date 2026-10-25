package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// latencyResult is the result of latency.get, as the UI and padmatectl read it.
type latencyResult struct {
	Devices []struct {
		Device  string `json:"device"`
		ID      uint32 `json:"id"`
		Name    string `json:"name"`
		Samples int    `json:"samples"`
		P50     int64  `json:"p50"`
		P90     int64  `json:"p90"`
		P99     int64  `json:"p99"`
		Max     int64  `json:"max"`
		Warn    bool   `json:"warn"`
	} `json:"devices"`
	DryRun         bool  `json:"dryRun"`
	WarnP99Us      int64 `json:"warnP99Us"`
	WarnMinSamples int   `json:"warnMinSamples"`
}

func latencyGet(ctx context.Context, t *testing.T, client *jrpc2.Client, params any) latencyResult {
	t.Helper()
	var res latencyResult
	if err := client.CallResult(ctx, "latency.get", params, &res); err != nil {
		t.Fatalf("latency.get: %v", err)
	}
	return res
}

// stampedButton sends an edge of control on the gamepad of sessionLive the way
// T-input delivers it, with the Stamp of its receipt.
func stampedButton(src *fakeSource, control string, down bool) {
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	src.events <- input.Event{Device: 1, Kind: kind, Control: control, Stamp: latency.Now()}
}

// TestIPCLatency (DMN-15, ADR-0071): padmated measures, for each device, the
// time from T-input's receipt of an input edge to the return of the Send of its
// keys, and latency.get gives the distribution in microseconds. Under
// --dry-run the Sender is the printer: it counts, and the result says so
// (dryRun). An edge without a Stamp (a trace) is not measured. latency.reset
// forgets one device's samples or all. A selector is a connection ID or a
// DeviceKey as in devices.list; an unknown one is -32004. The default profile
// holds Space on east.
func TestIPCLatency(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var devices []supervisor.DeviceView
	if err := client.CallResult(ctx, "devices.list", nil, &devices); err != nil || len(devices) != 1 {
		t.Fatalf("devices.list: %v, %v", devices, err)
	}
	key := devices[0].Key

	// Before any key: the mapped device is listed with no samples, and the
	// constants of the warning are the daemon's.
	res := latencyGet(ctx, t, client, nil)
	if len(res.Devices) != 1 || res.Devices[0].Device != key || res.Devices[0].ID != 1 || res.Devices[0].Samples != 0 {
		t.Fatalf("latency.get = %+v, want the one device %s without samples", res, key)
	}
	if !res.DryRun || res.WarnP99Us != 1500 || res.WarnMinSamples != 50 || res.Devices[0].Name != liveTestPad {
		t.Errorf("dryRun %v, warnP99Us %d, warnMinSamples %d, name %q; want true, 1500, 50, %q", res.DryRun, res.WarnP99Us, res.WarnMinSamples, res.Devices[0].Name, liveTestPad)
	}

	// An edge without a Stamp sends a key and is not measured.
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -Space\n")
	if res := latencyGet(ctx, t, client, nil); res.Devices[0].Samples != 0 {
		t.Fatalf("edges without a Stamp were measured: %+v", res.Devices[0])
	}

	// Stamped edges: a sample each, for the press and for the release, which goes
	// out at once once the key has been down for its minimum press (a release
	// before that waits for a timer, and no edge is behind it).
	holdEast(t, tl, src, 2)
	tl.waitUntil(t, "two samples", func() bool { return latencyGet(ctx, t, client, nil).Devices[0].Samples == 2 })
	d := latencyGet(ctx, t, client, nil).Devices[0]
	if d.P50 < 0 || d.P50 > d.P90 || d.P90 > d.P99 || d.P99 > d.Max || d.Max > 5_000_000 {
		t.Errorf("percentiles %d %d %d %d µs are not ordered, or are not microseconds", d.P50, d.P90, d.P99, d.Max)
	}
	if d.Warn {
		t.Errorf("2 samples warn: %+v", d)
	}

	// Both selectors give the device; unknown ones are -32004; extra fields are -32602.
	for _, sel := range []string{key, "1"} {
		if res := latencyGet(ctx, t, client, map[string]string{"device": sel}); len(res.Devices) != 1 || res.Devices[0].Samples != 2 {
			t.Errorf("latency.get {device:%q} = %+v", sel, res)
		}
	}
	rpcCode := func(err error) jrpc2.Code {
		t.Helper()
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("error %v is not a JSON-RPC error", err)
		}
		return rpcErr.Code
	}
	for _, method := range []string{"latency.get", "latency.reset"} {
		if err := client.CallResult(ctx, method, map[string]string{"device": "nobody"}, new(any)); err == nil || rpcCode(err) != -32004 {
			t.Errorf("%s of an unknown device: %v, want -32004", method, err)
		}
		if err := client.CallResult(ctx, method, map[string]int{"oops": 1}, new(any)); err == nil || rpcCode(err) != jrpc2.InvalidParams {
			t.Errorf("%s with an unknown parameter: %v, want -32602", method, err)
		}
	}

	// Reset by device, then by nothing.
	var done struct {
		Reset bool `json:"reset"`
	}
	if err := client.CallResult(ctx, "latency.reset", map[string]string{"device": key}, &done); err != nil || !done.Reset {
		t.Fatalf("latency.reset {device}: %+v, %v", done, err)
	}
	if res := latencyGet(ctx, t, client, nil); res.Devices[0].Samples != 0 {
		t.Errorf("after reset of the device: %+v", res.Devices[0])
	}
	holdEast(t, tl, src, 3)
	tl.waitUntil(t, "two samples again", func() bool { return latencyGet(ctx, t, client, nil).Devices[0].Samples == 2 })
	if err := client.CallResult(ctx, "latency.reset", nil, &done); err != nil || !done.Reset {
		t.Fatalf("latency.reset: %+v, %v", done, err)
	}
	if res := latencyGet(ctx, t, client, nil); res.Devices[0].Samples != 0 {
		t.Errorf("after reset of all: %+v", res.Devices[0])
	}
}

// TestIPCLatencyOwnWindow (DMN-15, ADR-0071): while padmate's own window is in
// front the keys go to the local dry run and no Sender is called, so they are
// not measured; with --inject the result says dryRun false. Back in the
// application's front, the keys are measured again.
func TestIPCLatencyOwnWindow(t *testing.T) {
	var foreground, alive atomic.Bool
	alive.Store(true)
	rec := new(ownRecorder)
	tl, src := sessionLive(t, ownTestConfig(&foreground, &alive), func() (output.Sender, error) { return rec, nil }, "--inject", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "ui.register", map[string]uint32{"pid": 42}, new(any)); err != nil {
		t.Fatal(err)
	}
	samples := func() int { return latencyGet(ctx, t, client, nil).Devices[0].Samples }

	if res := latencyGet(ctx, t, client, nil); res.DryRun {
		t.Error("dryRun is true under --inject")
	}
	stampedButton(src, "east", true) // holds Space: one Send
	tl.waitUntil(t, "the real press", func() bool { return rec.count() == 1 && samples() == 1 })

	foreground.Store(true)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, true)
	if rec.count() != 2 { // entering the window releases the hold, which is no edge's key
		t.Fatalf("%d events, want the press and the release", rec.count())
	}
	before := rec.count()
	stampedButton(src, "east", false)
	stampedButton(src, "south", true)
	stampedButton(src, "south", false)
	stampedButton(src, "east", true)
	stampedButton(src, "east", false)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != before || samples() != 1 {
		t.Errorf("in the own window: %d Sends (was %d), %d samples; want no Send and the sample of before", rec.count(), before, samples())
	}

	foreground.Store(false)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, false)
	stampedButton(src, "east", true)
	tl.waitUntil(t, "a measured press after the window left", func() bool { return samples() == 2 && rec.count() == before+1 })
	time.Sleep(minHold)
	stampedButton(src, "east", false)
	tl.waitUntil(t, "its release", func() bool { return samples() == 3 && rec.count() == before+2 })
}

// minHold is longer than the minimum press of the profile (20 ms): a key held
// that long is released at the edge, not by a timer.
const minHold = 60 * time.Millisecond

// holdEast presses east with a Stamp, holds it for minHold and releases it with
// a Stamp, as the nth time in the test that padmated sent Space down and up.
func holdEast(t *testing.T, tl *testLive, src *fakeSource, n int) {
	t.Helper()
	stampedButton(src, "east", true)
	tl.waitForCount(t, tl.stdout, "send +Space\n", n)
	time.Sleep(minHold)
	stampedButton(src, "east", false)
	tl.waitForCount(t, tl.stdout, "send -Space\n", n)
}
