package main

import (
	"context"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
)

// TestFakeJoyCon2: the fake input answers the Joy-Con 2 methods with a
// simulated backend, so that a UI can be developed against it: the window,
// fake.joycon2 as the user holding SYNC, remembering, disconnecting and
// forgetting, and the joycon2.changed pushes. Nothing is persisted.
func TestFakeJoyCon2(t *testing.T) {
	events := make(chan rpcJoyCon2Status, 32)
	tl, _ := startFakeEffects(t, "")
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		if r.Method() != joyCon2Topic {
			return
		}
		var st rpcJoyCon2Status
		if err := r.UnmarshalParams(&st); err != nil {
			t.Error(err)
		}
		events <- st
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{joyCon2Topic}}, new(any)); err != nil {
		t.Fatal(err)
	}
	status := func() rpcJoyCon2Status {
		t.Helper()
		var st rpcJoyCon2Status
		if err := client.CallResult(ctx, "joycon2.status", nil, &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	next := func(what string) rpcJoyCon2Status {
		t.Helper()
		select {
		case st := <-events:
			return st
		case <-ctx.Done():
			t.Fatal("no event:", what)
			return rpcJoyCon2Status{}
		}
	}
	sync := func(params map[string]any) map[string]any {
		t.Helper()
		var got map[string]any
		if err := client.CallResult(ctx, "fake.joycon2", params, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if st := status(); !st.Available || !st.AutoConnect || st.WindowOpen || len(st.Known) != 0 || len(st.Links) != 0 {
		t.Fatalf("start: %+v", st)
	}
	// SYNC outside the window does nothing.
	if got := sync(nil); got["accepted"] != false {
		t.Fatalf("sync outside the window: %v", got)
	}
	if st := status(); len(st.Known) != 0 {
		t.Fatalf("remembered outside the window: %+v", st)
	}

	// The window opens, a left Joy-Con is found and remembered, the window closes.
	var st rpcJoyCon2Status
	if err := client.CallResult(ctx, "joycon2.pair", nil, &st); err != nil || !st.WindowOpen || st.WindowLeftMs < 29000 || !st.Scanning {
		t.Fatalf("pair: %+v, %v", st, err)
	}
	if st := next("window"); !st.WindowOpen {
		t.Fatalf("event: %+v", st)
	}
	got := sync(nil)
	if got["accepted"] != true || got["serial"] != "FAKEJC2L0001" || got["side"] != "L" || got["device"] != float64(1001) {
		t.Fatalf("sync: %v", got)
	}
	st = next("remembered")
	if st.WindowOpen || len(st.Known) != 1 || st.Known[0].Serial != "FAKEJC2L0001" || st.Known[0].Side != "L" || !st.Known[0].Connected ||
		len(st.Links) != 1 || st.Links[0].Device != 1001 || st.Links[0].State != "connected" || !st.Links[0].Known {
		t.Fatalf("remembered: %+v", st)
	}

	// A right one, in a window of 5 s that is canceled first.
	if err := client.CallResult(ctx, "joycon2.pair", map[string]any{"seconds": 5}, new(any)); err != nil {
		t.Fatal(err)
	}
	next("second window")
	if err := client.CallResult(ctx, "joycon2.cancelPair", nil, &st); err != nil || st.WindowOpen {
		t.Fatalf("cancel: %+v, %v", st, err)
	}
	next("canceled")
	if got := sync(map[string]any{"side": "R"}); got["accepted"] != false {
		t.Fatalf("sync after the cancel: %v", got)
	}
	if err := client.CallResult(ctx, "joycon2.pair", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	next("third window")
	if got := sync(map[string]any{"side": "R"}); got["serial"] != "FAKEJC2R0001" || got["device"] != float64(1002) {
		t.Fatalf("sync R: %v", got)
	}
	st = next("right remembered")
	if len(st.Known) != 2 || st.Known[1].Side != "R" || len(st.Links) != 2 {
		t.Fatalf("right remembered: %+v", st)
	}

	// Disconnect keeps the controller remembered; an unknown ID is -32004.
	if err := client.CallResult(ctx, "joycon2.disconnect", map[string]any{"id": 1001}, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Known) != 2 || st.Known[0].Connected || len(st.Links) != 1 || st.Links[0].Device != 1002 {
		t.Fatalf("disconnect: %+v", st)
	}
	if err := client.CallResult(ctx, "joycon2.disconnect", map[string]any{"id": 1001}, new(any)); err == nil || rpcCodeOf(t, err) != -32004 {
		t.Fatalf("second disconnect: %v", err)
	}
	// Forget removes it; an unknown serial is -32004.
	if err := client.CallResult(ctx, "joycon2.forget", map[string]any{"serial": "FAKEJC2L0001"}, &st); err != nil || len(st.Known) != 1 || st.Known[0].Serial != "FAKEJC2R0001" {
		t.Fatalf("forget: %+v, %v", st, err)
	}
	if err := client.CallResult(ctx, "joycon2.forget", map[string]any{"serial": "FAKEJC2L0001"}, new(any)); err == nil || rpcCodeOf(t, err) != -32004 {
		t.Fatalf("second forget: %v", err)
	}

	// The window runs out by itself.
	if err := client.CallResult(ctx, "joycon2.pair", map[string]any{"seconds": 1}, new(any)); err != nil {
		t.Fatal(err)
	}
	opened := false
	for !opened {
		opened = next("short window").WindowOpen
	}
	if st := next("window ran out"); st.WindowOpen {
		t.Fatalf("window did not run out: %+v", st)
	}

	// The fake gamepad is still the only device, and the fake keeps no state.json.
	var devices []map[string]any
	if err := client.CallResult(ctx, "devices.list", nil, &devices); err != nil || len(devices) != 1 {
		t.Fatalf("devices.list = %v, %v", devices, err)
	}
	// Bad requests.
	if err := client.CallResult(ctx, "fake.joycon2", map[string]any{"side": "X"}, new(any)); err == nil || rpcCodeOf(t, err) != jrpc2.InvalidParams {
		t.Fatalf("bad side: %v", err)
	}
}

// TestFakeJoyCon2NeedsFakeInput: fake.joycon2 is a fake.* method, -32010
// without --fake-input.
func TestFakeJoyCon2NeedsFakeInput(t *testing.T) {
	tl, _ := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "fake.joycon2", nil, new(any)); err == nil || rpcCodeOf(t, err) != -32010 {
		t.Fatalf("fake.joycon2 without the fake input: %v", err)
	}
}
