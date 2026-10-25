package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// The scripts' hud API (SCR-22) over IPC: host.has("hud") follows a client
// subscribing to hud, a toast is a hud event, and the panels are in the slot
// object of the engine.activity state event until the script removes them or
// its runtime stops (here: a pause).
func TestIPCScriptHUD(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":1,"name":"paint","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.21"}}`)
	const script = `import { host, hud, on } from "padmate";
on("west", "press", () => { hud.panel("has", { rows: [["hud", String(host.has("hud"))]] }); });
on("south", "press", () => {
  hud.toast(" 脚本\n提示 ", { ms: 2000 });
  hud.panel("brush", { title: "画笔", rows: [["大小", "12"]] });
});
on("east", "press", () => { hud.panel("brush", null); });
`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}]}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	observed := make(chan struct{})
	go func() { defer close(observed); tl.observeIPC(ctx) }()
	t.Cleanup(func() { cancel(); <-observed })
	states, toasts := make(chan string, 256), make(chan string, 16)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		switch {
		case r.Method() == "engine.activity" && strings.Contains(r.ParamString(), `"kind":"state"`):
			states <- r.ParamString()
		case r.Method() == "hud":
			toasts <- r.ParamString()
		}
	}})
	subscribe := func(topics ...string) {
		t.Helper()
		if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": topics}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	type panels struct {
		Slots []struct {
			Panels []json.RawMessage `json:"panels"`
		} `json:"slots"`
	}
	// next waits for a state event whose first slot's panels are want ("" for none).
	next := func(what, want string) {
		t.Helper()
		for {
			select {
			case data := <-states:
				var s panels
				if err := json.Unmarshal([]byte(data), &s); err != nil {
					t.Fatalf("%s: %v in %s", what, err, data)
				}
				if len(s.Slots) == 0 {
					continue
				}
				got := ""
				if s.Slots[0].Panels != nil {
					b, _ := json.Marshal(s.Slots[0].Panels)
					got = string(b)
				}
				if got == want {
					return
				}
			case <-ctx.Done():
				t.Fatalf("%s: no state event with panels %s; stdout:\n%s", what, want, tl.stdout.String())
			}
		}
	}
	press := func(control string) {
		button(src, control, true)
		button(src, control, false)
	}

	running := func() {
		t.Helper()
		tl.waitUntil(t, "the script running", func() bool {
			var views []slot.ScriptView
			err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views)
			return err == nil && len(views) == 1 && views[0].Script.State == "Running" && !views[0].Script.Pending && !views[0].Script.Paused
		})
	}
	running()
	subscribe("engine.activity")
	next("at the start", "")
	press("west")
	next("without an overlay", `[{"id":"has","rows":[["hud","false"]]}]`)
	press("south") // no overlay: the toast goes nowhere
	next("brush", `[{"id":"has","rows":[["hud","false"]]},{"id":"brush","title":"画笔","rows":[["大小","12"]]}]`)

	subscribe("engine.activity", "hud")
	press("west")
	next("with an overlay", `[{"id":"has","rows":[["hud","true"]]},{"id":"brush","title":"画笔","rows":[["大小","12"]]}]`)
	press("south")
	select {
	case data := <-toasts:
		if want := `{"kind":"toast","slot":0,"text":"脚本 提示","ms":2000}`; data != want {
			t.Errorf("hud event = %s, want %s", data, want)
		}
	case <-ctx.Done():
		t.Fatal("no hud event for the toast")
	}
	press("east")
	next("brush removed", `[{"id":"has","rows":[["hud","true"]]}]`)

	// A paused runtime shows nothing; the one built at the resume starts
	// empty: after a press that sets only "has", brush is not back.
	press("south")
	next("brush again", `[{"id":"has","rows":[["hud","true"]]},{"id":"brush","title":"画笔","rows":[["大小","12"]]}]`)
	<-toasts
	if err := client.CallResult(ctx, "engine.pause", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	next("paused", "")
	if err := client.CallResult(ctx, "engine.resume", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	running()
	press("west")
	next("resumed", `[{"id":"has","rows":[["hud","true"]]}]`)
	select {
	case data := <-toasts:
		t.Errorf("unexpected hud event %s", data)
	default:
	}
	if err := client.CallResult(ctx, "engine.releaseAll", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	checkBalanced(t, tl.stdout.String())
}
