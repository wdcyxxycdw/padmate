package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// fakePlay is the part of a play call of fake.effects that the low-battery
// alert is about.
type fakePlay struct {
	Kind   string `json:"kind"`
	Device int    `json:"device"`
	Class  string `json:"class"`
	Preset string `json:"preset"`
	Blink  bool   `json:"blink"`
}

type fakePlays struct {
	Effects []fakePlay `json:"effects"`
}

// alertPlays returns the Alert-class play calls of fake.effects.
func alertPlays(ctx context.Context, t *testing.T, client *jrpc2.Client) []fakePlay {
	t.Helper()
	var got fakePlays
	if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
		t.Fatal(err)
	}
	var out []fakePlay
	for _, e := range got.Effects {
		if e.Kind == "play" && e.Class == "alert" {
			out = append(out, e)
		}
	}
	return out
}

// TestFakeBattery (IN-12): fake.battery makes the fake gamepad report a power,
// as a real driver does, so the whole alert can be seen without a battery:
// devices.list shows it, an alert is an Alert-class play with pulse3 and the
// blink in fake.effects, a charge re-arms the alert, and the next drop
// alerts again.
func TestFakeBattery(t *testing.T) {
	tl, client := startFakeEffects(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	battery := func() *supervisor.DeviceView {
		t.Helper()
		var devices []supervisor.DeviceView
		if err := client.CallResult(ctx, "devices.list", nil, &devices); err != nil || len(devices) != 1 {
			t.Fatalf("devices.list = %+v, %v", devices, err)
		}
		return &devices[0]
	}
	call := func(params map[string]any) error {
		return client.CallResult(ctx, "fake.battery", params, new(any))
	}
	if b := battery().Battery; b != nil {
		t.Fatalf("the fake gamepad has a battery before fake.battery: %+v", b)
	}

	if err := call(map[string]any{"state": "discharging", "percent": 80}); err != nil {
		t.Fatal(err)
	}
	if b := battery().Battery; b == nil || b.State.String() != "discharging" || b.Percent != 80 {
		t.Fatalf("battery after discharging 80 = %+v", b)
	}
	if got := alertPlays(ctx, t, client); len(got) != 0 {
		t.Fatalf("alerts at 80%%: %+v", got)
	}

	if err := call(map[string]any{"state": "discharging", "percent": 15}); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "the alert", func() bool { return len(alertPlays(ctx, t, client)) == 1 })
	if a := alertPlays(ctx, t, client)[0]; a.Device != 1 || a.Preset != "pulse3" || !a.Blink {
		t.Errorf("alert = %+v, want pulse3 with the blink for device 1", a)
	}

	// Charging re-arms; the next drop alerts again.
	if err := call(map[string]any{"state": "charging", "percent": 40}); err != nil {
		t.Fatal(err)
	}
	if err := call(map[string]any{"state": "discharging", "percent": 15}); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "the second alert", func() bool { return len(alertPlays(ctx, t, client)) == 2 })

	// No charge reported: the member is left out, and nothing alerts.
	if err := call(map[string]any{"state": "wired"}); err != nil {
		t.Fatal(err)
	}
	if b := battery().Battery; b == nil || b.State.String() != "wired" || b.Percent != -1 {
		t.Fatalf("battery after wired = %+v", b)
	}
	if err := call(map[string]any{"state": "unknown"}); err != nil {
		t.Fatal(err)
	}
	// The effect queue is first in, first out: when it has handed over the
	// ambient state of a device nobody uses, it has handed over what was
	// posted before it.
	tl.effects.post(input.Feedback{Device: 999, Color: "#000001"})
	tl.waitUntil(t, "the effect queue to be drained", func() bool {
		var got fakePlays
		if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(got.Effects, func(e fakePlay) bool { return e.Kind == "feedback" && e.Device == 999 })
	})
	if got := alertPlays(ctx, t, client); len(got) != 2 {
		t.Fatalf("alerts = %+v, want the two of the two episodes", got)
	}
}

// TestFakeBatteryRejectsBadParameters: the state is one of the four, the
// percent 0 to 100, and an unknown state has none; members it does not know
// are refused too.
func TestFakeBatteryRejectsBadParameters(t *testing.T) {
	_, client := startFakeEffects(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for name, params := range map[string]any{
		"none":              map[string]any{},
		"bad state":         map[string]any{"state": "full"},
		"upper case":        map[string]any{"state": "Discharging"},
		"percent too high":  map[string]any{"state": "discharging", "percent": 101},
		"percent negative":  map[string]any{"state": "discharging", "percent": -1},
		"percent a string":  map[string]any{"state": "discharging", "percent": "5"},
		"unknown and 50":    map[string]any{"state": "unknown", "percent": 50},
		"an extra member":   map[string]any{"state": "discharging", "percent": 5, "device": 1},
		"percent is a null": map[string]any{"state": "wired", "percent": nil, "extra": true},
	} {
		err := client.CallResult(ctx, "fake.battery", params, new(any))
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.InvalidParams {
			t.Errorf("%s: fake.battery = %v, want invalid params", name, err)
		}
	}
}

// TestFakeBatteryNeedsFakeInput: a daemon on real input (here a fake source that
// is not the IPC one) refuses fake.battery with -32010, as it does fake.press.
func TestFakeBatteryNeedsFakeInput(t *testing.T) {
	tl, _ := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := client.CallResult(ctx, "fake.battery", map[string]any{"state": "discharging", "percent": 5}, new(any))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32010 {
		t.Fatalf("fake.battery on real input = %v, want -32010", err)
	}
}
