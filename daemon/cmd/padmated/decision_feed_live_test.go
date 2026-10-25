package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// activityClient subscribes to engine.activity on tl and returns the
// events as they come (the "state" snapshots left out).
func activityClient(t *testing.T, tl *testLive) <-chan map[string]any {
	t.Helper()
	events := make(chan map[string]any, 256)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		if r.Method() != "engine.activity" {
			return
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(r.ParamString()), &ev); err != nil {
			return
		}
		if ev["kind"] != "state" {
			events <- ev
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"engine.activity"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	return events
}

// nextActivity waits for the next event of kind, skipping the others.
func nextActivity(t *testing.T, events <-chan map[string]any, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(liveWait)
	for {
		select {
		case ev := <-events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("waited for an engine.activity event")
		}
	}
}

func decisionOf(ev map[string]any) map[string]any {
	d, _ := ev["decision"].(map[string]any)
	return d
}

// A decision event that the IPC client gets carries the wall time, what an
// unbound press waits for, and how long the tap waited (ADR-0071), whole
// through the production wiring: the slot, the feed's publisher, the Hub.
func TestLiveDecisionEventsCarryTimeAndWaiting(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{"south":{"label":"撤销","tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"F14"}},"east":{"press":{"type":"keys","keys":"F15"}}}}}`)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	events := activityClient(t, tl)
	before := time.Now().UnixMilli()
	button(src, "south", true)
	press := nextActivity(t, events, func(ev map[string]any) bool { return ev["kind"] == "decision" && decisionOf(ev)["control"] == "south" })
	if d := decisionOf(press); d["reason"] != "unbound" || d["gesture"] != "press" {
		t.Fatalf("press decision %v", press)
	}
	if w, _ := press["waiting"].([]any); len(w) != 2 || w[0] != "tap" || w[1] != "hold" {
		t.Errorf("waiting %v, want tap and hold", press["waiting"])
	}
	if at, _ := press["at"].(float64); int64(at) < before || int64(at) > time.Now().UnixMilli() {
		t.Errorf("at %v is not a wall time between %d and now", press["at"], before)
	}
	if _, ok := press["waitedMs"]; ok {
		t.Errorf("a press has waitedMs: %v", press)
	}
	time.Sleep(60 * time.Millisecond)
	button(src, "south", false)
	tap := nextActivity(t, events, func(ev map[string]any) bool {
		return ev["kind"] == "decision" && decisionOf(ev)["gesture"] == "tap"
	})
	if d := decisionOf(tap); d["reason"] != "dispatch" || d["action"] == nil {
		t.Errorf("tap decision %v", tap)
	}
	if w, _ := tap["waitedMs"].(float64); w < 50 || w > 2000 {
		t.Errorf("waitedMs %v, want about 60", tap["waitedMs"])
	}
	if _, ok := tap["waiting"]; ok {
		t.Errorf("a tap waits for nothing: %v", tap)
	}
	tl.waitFor(t, tl.stdout, "send +F13")
	// A press with an action of its own is no wait.
	button(src, "east", true)
	east := nextActivity(t, events, func(ev map[string]any) bool { return ev["kind"] == "decision" && decisionOf(ev)["control"] == "east" })
	if _, ok := east["waiting"]; ok || decisionOf(east)["reason"] != "dispatch" {
		t.Errorf("east %v", east)
	}
	button(src, "east", false)
	if n := tl.decisions.Dropped(); n != 0 {
		t.Errorf("%d events dropped", n)
	}
}

// The button edges of a gamepad that waits for a slot reach the IPC clients as
// unrouted events (ADR-0071), and a mapped gamepad's do not.
func TestLiveUnroutedEvents(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{"east":{"press":{"type":"keys","keys":"F15"}}}}}`)
	writeLastProfile(t, dir, "paint")
	tl, src := startLibraryLive(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true}, func(c *liveConfig) { c.slots = 1 })
	events := activityClient(t, tl)
	// The first gamepad confirms its slot with a press.
	button(src, "east", true)
	button(src, "east", false)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 2, Info: &input.DeviceInfo{ID: 2, GUID: strings.Repeat("e3", 16), Name: "second pad", IsGamepad: true}}
	tl.sync()
	src.events <- input.Event{Kind: input.ButtonDown, Device: 2, Control: "south"}
	ev := nextActivity(t, events, func(ev map[string]any) bool { return ev["kind"] == "unrouted" })
	if ev["device"] != float64(2) || ev["control"] != "south" || ev["down"] != true || ev["why"] != "queued" {
		t.Errorf("unrouted event %v", ev)
	}
	if at, _ := ev["at"].(float64); at < 1e12 {
		t.Errorf("at %v is not a Unix time in ms", ev["at"])
	}
}
