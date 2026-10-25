package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// identifyCalls returns the identify calls the fake source received.
func identifyCalls(src *fakeSource) []effectCall {
	var got []effectCall
	for _, c := range src.effects.all() {
		if c.Kind == "identify" {
			got = append(got, c)
		}
	}
	return got
}

// TestIPCIdentify (IN-13): devices.identify asks the source for the cues of
// the one controller the selector names, a connection ID or a DeviceKey, and
// answers which it can give. It works for every status a controller has,
// mapped, waiting, ignored and unmapped, and while the mapping is paused,
// since it does not go through the engine. A controller that can do nothing
// answers all false and is no error; an unknown controller is -32004
// ("device is not connected", as devices.list), a missing selector and any
// other parameter is -32602. A fake source stands in for T-input, so it
// needs no SDL and no controller is reached.
func TestIPCIdentify(t *testing.T) {
	tl, src := sessionStart(t, func(cfg *liveConfig) { cfg.slots = 1 }, nil, "--dry-run", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// One pad of each status: the filter of the config keeps "other" out,
	// and with one slot the second pad waits.
	pads := []struct {
		id      input.DeviceID
		name    string
		gamepad bool
		status  string
		cues    haptic.Cues
	}{
		{1, liveTestPad, true, "mapped", haptic.Cues{Rumble: true, LED: true}},
		{2, liveTestPad + " two", true, "waiting", haptic.Cues{Rumble: true}},
		{3, "other controller", true, "ignored", haptic.Cues{Player: true}},
		{4, "a joystick", false, "unmapped", haptic.Cues{}},
	}
	for _, p := range pads {
		src.setCues(p.id, p.cues)
		info := &input.DeviceInfo{ID: p.id, Name: p.name, Path: fmt.Sprintf("fake%d", p.id), IsGamepad: p.gamepad}
		src.events <- input.Event{Device: p.id, Kind: input.DeviceAdded, Info: info}
	}
	list := func() []supervisor.DeviceView {
		t.Helper()
		var rows []supervisor.DeviceView
		if err := client.CallResult(ctx, "devices.list", nil, &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	tl.waitUntil(t, "the four devices", func() bool { return len(list()) == len(pads) })
	byID := map[input.DeviceID]supervisor.DeviceView{}
	for _, v := range list() {
		byID[v.Info.ID] = v
	}
	for _, p := range pads {
		if got := byID[p.id].Status; got != p.status {
			t.Fatalf("pad %d is %q, want %q", p.id, got, p.status)
		}
	}

	identify := func(device string) (haptic.Cues, error) {
		t.Helper()
		var cues haptic.Cues
		err := client.CallResult(ctx, "devices.identify", map[string]string{"device": device}, &cues)
		return cues, err
	}
	rpcCode := func(err error) jrpc2.Code {
		t.Helper()
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("error %v is not a JSON-RPC error", err)
		}
		return rpcErr.Code
	}

	// Each status, by connection ID and by DeviceKey: the answer is what the
	// source said for that pad, and the source got exactly that pad.
	for _, p := range pads {
		for _, selector := range []string{fmt.Sprint(p.id), byID[p.id].Key} {
			before := len(identifyCalls(src))
			got, err := identify(selector)
			if err != nil || got != p.cues {
				t.Errorf("identify %q (%s) = %+v, %v; want %+v", selector, p.status, got, err, p.cues)
			}
			calls := identifyCalls(src)
			if len(calls) != before+1 || calls[before].Device != p.id {
				t.Errorf("identify %q reached the source as %+v, want one call for pad %d", selector, calls[before:], p.id)
			}
		}
	}

	// Paused or not, the mapping has no say.
	if err := client.CallResult(ctx, "engine.pause", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	before := len(identifyCalls(src))
	if got, err := identify("1"); err != nil || got != pads[0].cues {
		t.Errorf("identify while paused = %+v, %v", got, err)
	}
	if n := len(identifyCalls(src)); n != before+1 {
		t.Errorf("identify while paused made %d source calls, want 1", n-before)
	}
	if err := client.CallResult(ctx, "engine.resume", nil, new(any)); err != nil {
		t.Fatal(err)
	}

	// A controller that can do nothing is no error: the answer is all false.
	src.setCues(1, haptic.Cues{})
	if got, err := identify("1"); err != nil || got != (haptic.Cues{}) {
		t.Errorf("identify of a controller without cues = %+v, %v; want all false and no error", got, err)
	}
	raw := map[string]bool{}
	if err := client.CallResult(ctx, "devices.identify", map[string]string{"device": "1"}, &raw); err != nil ||
		len(raw) != 3 || raw["rumble"] || raw["led"] || raw["player"] {
		t.Errorf("the answer is %v, %v; want the three members rumble, led and player, all false", raw, err)
	}

	// Not connected: no such ID or key, and a source that lost the pad
	// between the lookup and the call.
	before = len(identifyCalls(src))
	for _, selector := range []string{"99", "0", byID[1].Key + "x", "no such key"} {
		if _, err := identify(selector); err == nil || rpcCode(err) != -32004 {
			t.Errorf("identify %q: %v, want -32004", selector, err)
		}
	}
	if n := len(identifyCalls(src)); n != before {
		t.Errorf("identify of an unknown controller reached the source %d times", n-before)
	}
	src.failIdentify(input.ErrNoDevice)
	if _, err := identify("2"); err == nil || rpcCode(err) != -32004 {
		t.Errorf("identify of a pad the source lost: %v, want -32004", err)
	}
	// Any other failure of the controller is an error too, not an answer.
	src.failIdentify(errors.New("write failed"))
	if _, err := identify("2"); err == nil || rpcCode(err) == -32004 || rpcCode(err) == jrpc2.InvalidParams {
		t.Errorf("identify with a failing controller: %v, want an error that is neither -32004 nor -32602", err)
	}
	src.failIdentify(nil)

	// The selector is required, a string, and the only parameter.
	before = len(identifyCalls(src))
	for name, params := range map[string]any{
		"none":        nil,
		"empty":       map[string]any{},
		"empty text":  map[string]any{"device": ""},
		"number":      map[string]any{"device": 1},
		"null":        map[string]any{"device": nil},
		"extra":       map[string]any{"device": "1", "all": true},
		"unknown key": map[string]any{"id": "1"},
	} {
		err := client.CallResult(ctx, "devices.identify", params, new(any))
		if err == nil || rpcCode(err) != jrpc2.InvalidParams {
			t.Errorf("devices.identify with %s parameters: %v, want -32602", name, err)
		}
	}
	if n := len(identifyCalls(src)); n != before {
		t.Errorf("a bad request reached the source %d times", n-before)
	}
}

// TestIPCIdentifyIsNoFeedback: identify neither uses the feedback of the
// layers nor disturbs it: the fake source sees no SetFeedback or Play call
// caused by it, so the mixer below the source is the only place the cues
// meet the layer colors (TestIdentifyDoesNotDisturbAmbient, package input).
func TestIPCIdentifyIsNoFeedback(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
	src.setCues(1, haptic.Cues{Rumble: true, LED: true})
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A slot that gets its gamepad publishes its ambient state once.
	tl.waitUntil(t, "the first feedback", func() bool { return len(feedbackCalls(src, 1)) >= 1 })
	other := len(src.effects.all())
	if err := client.CallResult(ctx, "devices.identify", map[string]string{"device": "1"}, new(any)); err != nil {
		t.Fatal(err)
	}
	calls := src.effects.all()
	if len(calls) != other+1 || calls[other].Kind != "identify" || calls[other].Device != 1 {
		t.Errorf("effect calls %+v, want the earlier ones and one identify of pad 1", calls)
	}
}
