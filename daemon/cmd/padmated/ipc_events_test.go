package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func TestIPCProductionTopicsIncludeLabelLatchAndProfile(t *testing.T) {
	dir := t.TempDir()
	const source = `{"schemaVersion":0,"name":"paint","layers":{"base":{"south":{"label":"Draw","press":{"type":"keys","keys":"F13"}},"east":{"latch":true,"press":{"type":"keys","keys":"Shift"}}}}}`
	writeLibraryProfile(t, dir, "paint", source)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}]}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	observed := make(chan struct{})
	go func() { defer close(observed); tl.observeIPC(ctx) }()
	t.Cleanup(func() { cancel(); <-observed })
	type event struct{ topic, data string }
	events := make(chan event, 128)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) { events <- event{r.Method(), r.ParamString()} }})
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": ipc.Topics}, new(any)); err != nil {
		t.Fatal(err)
	}
	// A foreground change publishes app.foreground (PRO-16).
	tl.onForeground(focusApp("paint.exe"))
	button(src, "south", true)
	button(src, "south", false)
	button(src, "east", true)
	button(src, "east", false)
	tl.liveRun.log.Info("ipc production topic marker")
	found := map[string]bool{}
	label, latch := false, false
	for len(found) < 4 || !label || !latch {
		select {
		case e := <-events:
			found[e.topic] = true
			label = label || e.topic == "engine.activity" && strings.Contains(e.data, `"label":"Draw"`)
			latch = latch || e.topic == "engine.activity" && strings.Contains(e.data, `"latches":["east"]`)
		case <-ctx.Done():
			t.Fatalf("missing topics/label/latch: %v %v %v", found, label, latch)
		}
	}
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "paint", "revision": profilelib.Revision([]byte(source)), "text": source + "\n"}, new(any)); err != nil {
		t.Fatal(err)
	}
	// What the tray's exit publishes (DMN-22), without exiting.
	tl.announceExit("tray")
	// What a script's hud.toast sends (SCR-22; TestIPCScriptHUD runs a script).
	hudEvents{hub: tl.liveRun.events}.Toast(0, "ipc production topic marker", 1500)
	for _, topic := range ipc.Topics {
		if topic == "input.raw" {
			continue // only a controller asked for with devices.rawInfo sends it (ipc_mapping_test.go)
		}
		for !found[topic] {
			select {
			case e := <-events:
				found[e.topic] = true
			case <-ctx.Done():
				t.Fatal("missing", topic)
			}
		}
	}
	if err := client.CallResult(ctx, "engine.releaseAll", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.stdout, "send -LShift\n")
	checkBalanced(t, tl.stdout.String())
}
