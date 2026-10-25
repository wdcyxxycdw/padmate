package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

func powerEvent(id input.DeviceID, state input.PowerState, percent int) input.Event {
	p := input.Power{State: state, Percent: percent}
	return input.Event{Device: id, Kind: input.DevicePower, Power: &p}
}

// TestDevicesListCarriesBattery (IN-12): devices.list and status.get show
// the latest power of each gamepad as "battery":{"state","percent"}; the
// member is absent until T-input read one, the percent is left out when the
// gamepad does not report it, and a gamepad that is removed takes it along.
// A fake source stands in for T-input, so it needs no SDL.
func TestDevicesListCarriesBattery(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list := func() []map[string]json.RawMessage {
		t.Helper()
		var rows []map[string]json.RawMessage
		if err := client.CallResult(ctx, "devices.list", nil, &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := list()
	if len(rows) != 1 {
		t.Fatalf("devices.list = %v, want the one gamepad", rows)
	}
	if b, ok := rows[0]["battery"]; ok {
		t.Fatalf("battery %s before a read", b)
	}

	src.events <- powerEvent(1, input.PowerDischarging, 80)
	tl.waitUntil(t, "the battery in devices.list", func() bool { return list()[0]["battery"] != nil })
	if got := string(list()[0]["battery"]); got != `{"state":"discharging","percent":80}` {
		t.Errorf("battery = %s", got)
	}
	var devices []supervisor.DeviceView
	if err := client.CallResult(ctx, "devices.list", nil, &devices); err != nil || len(devices) != 1 ||
		devices[0].Battery == nil || *devices[0].Battery != (input.Power{State: input.PowerDischarging, Percent: 80}) {
		t.Fatalf("devices.list = %+v, %v", devices, err)
	}

	// status.get shows the same devices, and the selection by key too.
	var st rpcStatus
	if err := client.CallResult(ctx, "status.get", nil, &st); err != nil || len(st.Devices) != 1 ||
		st.Devices[0].Battery == nil || st.Devices[0].Battery.Percent != 80 {
		t.Fatalf("status.get devices = %+v, %v", st.Devices, err)
	}
	if err := client.CallResult(ctx, "devices.list", map[string]string{"device": devices[0].Key}, &devices); err != nil ||
		len(devices) != 1 || devices[0].Battery == nil {
		t.Fatalf("devices.list by key = %+v, %v", devices, err)
	}

	// No percent: the member is left out, not 0 or -1.
	src.events <- powerEvent(1, input.PowerWired, -1)
	tl.waitUntil(t, "the wired battery", func() bool { return string(list()[0]["battery"]) == `{"state":"wired"}` })

	// The gamepad goes; the one that comes back under a new id has no
	// battery until T-input reads it again.
	src.events <- input.Event{Device: 1, Kind: input.DeviceRemoved}
	tl.waitUntil(t, "the removal", func() bool { return len(list()) == 0 })
	const alias = input.DeviceID(1<<32 - 1)
	info := &input.DeviceInfo{ID: alias, Name: liveTestPad, Path: "fake1", IsGamepad: true}
	src.events <- input.Event{Device: alias, Kind: input.DeviceAdded, Info: info}
	tl.waitUntil(t, "the new device", func() bool { return len(list()) == 1 })
	if b, ok := list()[0]["battery"]; ok {
		t.Fatalf("the revived gamepad has battery %s before it reads", b)
	}
	src.events <- powerEvent(alias, input.PowerCharging, 55)
	tl.waitUntil(t, "the revived battery", func() bool { return string(list()[0]["battery"]) == `{"state":"charging","percent":55}` })
}

// TestInputTopicNeverCarriesPower: DevicePower is routing metadata, not an
// input trace record, so the IPC input topic (padmatectl monitor and
// record read it as traces) does not carry it, whoever listens.
func TestInputTopicNeverCarriesPower(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
	inputs := make(chan string, 16)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		if r.Method() == "input" {
			inputs <- r.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"input"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	src.events <- powerEvent(1, input.PowerDischarging, 80)
	button(src, "south", true)
	select {
	case got := <-inputs:
		if strings.Contains(got, `"power"`) || !strings.Contains(got, `"kind":"down"`) {
			t.Fatalf("the first input event is %s, want the button", got)
		}
	case <-ctx.Done():
		t.Fatal("no input event")
	}
	select {
	case got := <-inputs:
		t.Fatalf("an extra input event %s", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestLiveAsksForPowerEvents: padmated opens the Source with PowerEvents, the
// switch that makes T-input read the batteries (IN-12).
func TestLiveAsksForPowerEvents(t *testing.T) {
	var got input.Options
	tl, _ := sessionStart(t, func(cfg *liveConfig) {
		open := cfg.open
		cfg.open = func(o input.Options) (eventSource, error) {
			got = o
			return open(o)
		}
	}, nil, "--dry-run", "--watch=false")
	_ = tl
	if !got.PowerEvents {
		t.Errorf("padmated opened the Source with %+v, want PowerEvents", got)
	}
}
