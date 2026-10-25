package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
)

// The key hints (HUD-03, ADR-0063) reach the UI in the slot object of the
// engine.activity state event, and of status.get: an array of the control and
// its label while a hints action is held, and no field at all when it is not.
func TestIPCStateEventCarriesTheKeyHints(t *testing.T) {
	dir := t.TempDir()
	const source = `{"schemaVersion":1,"name":"paint","layers":{"base":{` +
		`"rightshoulder":{"press":{"type":"hud","op":"hints"}},` +
		`"south":{"label":"撤销","press":{"type":"keys","keys":"Ctrl+Z","mode":"tap"}},` +
		`"east":{"press":{"type":"keys","keys":"Space"}},` +
		`"west":{"block":true}}}}`
	writeLibraryProfile(t, dir, "paint", source)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}]}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := make(chan struct{})
	go func() { defer close(observed); tl.observeIPC(ctx) }()
	t.Cleanup(func() { cancel(); <-observed })
	events := make(chan string, 256)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		if r.Method() == "engine.activity" && strings.Contains(r.ParamString(), `"kind":"state"`) {
			events <- r.ParamString()
		}
	}})
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"engine.activity"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	type slotHints struct {
		Slots []struct {
			Hints *[]struct {
				Control string `json:"control"`
				Label   string `json:"label"`
			} `json:"hints"`
		} `json:"slots"`
	}
	next := func(what string, want func(data string, s slotHints) bool) string {
		t.Helper()
		for {
			select {
			case data := <-events:
				var s slotHints
				if err := json.Unmarshal([]byte(data), &s); err != nil {
					t.Fatalf("%s: %v in %s", what, err, data)
				}
				if want(data, s) {
					return data
				}
			case <-ctx.Done():
				t.Fatalf("%s: no such state event", what)
			}
		}
	}
	off := func(data string, s slotHints) bool {
		return len(s.Slots) > 0 && s.Slots[0].Hints == nil && !strings.Contains(data, `"hints"`)
	}
	on := func(data string, s slotHints) bool { return len(s.Slots) > 0 && s.Slots[0].Hints != nil }

	next("before the press", off)
	button(src, "rightshoulder", true)
	data := next("held", on)
	const want = `"hints":[{"control":"south","label":"撤销"},{"control":"east","label":"Space"}]`
	if !strings.Contains(data, want) {
		t.Errorf("the state event has %s\nwant %s in it", data, want)
	}
	// status.get has the same slot object while the hints are held.
	var status struct {
		Slots []struct {
			Hints []struct{ Control, Label string } `json:"hints"`
		} `json:"slots"`
	}
	if err := client.CallResult(ctx, "status.get", nil, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Slots) == 0 || len(status.Slots[0].Hints) != 2 || status.Slots[0].Hints[0].Control != "south" {
		t.Errorf("status.get slots: %+v", status.Slots)
	}
	button(src, "rightshoulder", false)
	data = next("released", off)
	if strings.Contains(data, "hints") {
		t.Errorf("the state event after the release still has hints: %s", data)
	}
	// Nothing in the event gets the hints back by itself.
	for range 3 {
		next("later", func(data string, s slotHints) bool { return true })
	}
	select {
	case data := <-events:
		if strings.Contains(data, `"hints"`) {
			t.Errorf("hints came back: %s", data)
		}
	default:
	}
	// What padmated does at its exit.
	if err := client.CallResult(ctx, "engine.releaseAll", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	checkBalanced(t, tl.stdout.String())
}
