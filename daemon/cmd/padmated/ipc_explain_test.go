package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIPCActivateAndExplainLayers(t *testing.T) {
	dir := t.TempDir()
	const source = `{"schemaVersion":0,"name":"layers","common":{"south":{"press":{"type":"keys","keys":"F13"}}},"layers":{"base":{},"alt":{"south":{"label":"alt key","press":{"type":"keys","keys":"F14"}}},"blocked":{"south":{"block":true}},"label":{"south":{"label":"label only"}}}}`
	writeLibraryProfile(t, dir, "first", source)
	writeLibraryProfile(t, dir, "second", strings.ReplaceAll(source, "F13", "F15"))
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, tc := range []struct{ layer, want string }{{"base", "common"}, {"alt", "alt"}, {"blocked", "blocked"}, {"label", "label"}} {
		if err := client.CallResult(ctx, "engine.setLayer", map[string]string{"device": "1", "layer": tc.layer}, new(any)); err != nil {
			t.Fatal(err)
		}
		var result rpcExplanation
		if err := client.CallResult(ctx, "profiles.explain", map[string]string{"control": "a", "device": "1"}, &result); err != nil {
			t.Fatal(err)
		}
		if result.Control != "south" || len(result.Slots) != 1 || len(result.Slots[0].Layers) != 5 {
			t.Fatalf("explain=%+v", result)
		}
		chosen := ""
		for _, entry := range result.Slots[0].Layers {
			if entry.Selected {
				chosen += entry.Layer
			}
		}
		if chosen != tc.want {
			t.Fatalf("base=%s selected=%s want=%s", tc.layer, chosen, tc.want)
		}
	}
	if err := client.CallResult(ctx, "engine.setLayer", map[string]string{"device": "1", "layer": "base"}, new(any)); err != nil {
		t.Fatal(err)
	}
	button(src, "south", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "second"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.stdout, "send -F13\n")
	button(src, "south", false)
	button(src, "south", true)
	button(src, "south", false)
	tl.waitFor(t, tl.stdout, "send -F15\n")
	if err := client.CallResult(ctx, "profiles.activate", map[string]any{"id": nil}, new(any)); err != nil {
		t.Fatal(err)
	}
	if tl.profileTarget.Load().id != "" {
		t.Fatal("null did not disable mapping")
	}
	for _, raw := range []string{`{}`, `{"id":12}`, `{"id":"../escape"}`, `{"id":"missing"}`, `{"id":"first","device":"1"}`} {
		if err := client.CallResult(ctx, "profiles.activate", json.RawMessage(raw), new(any)); err == nil {
			t.Fatalf("invalid activation accepted: %s", raw)
		}
	}
	var result rpcExplanation
	if err := client.CallResult(ctx, "profiles.explain", map[string]string{"id": "second", "control": "south"}, &result); err != nil || len(result.Slots) != 1 || result.Slots[0].Index != -1 {
		t.Fatal(result, err)
	}
	if err := client.CallResult(ctx, "profiles.explain", map[string]string{"control": "not-a-button"}, new(any)); err == nil {
		t.Fatal("unknown control accepted")
	}
	checkBalanced(t, tl.stdout.String())
}

// profiles.explain lists the sets of the two function keys beside the layers
// and says which function key is held (MAP-27, ADR-0069, ADR-0071). A set
// that binds the control and belongs to the key held now wins over the layers.
func TestIPCExplainFunctionKeys(t *testing.T) {
	dir := t.TempDir()
	const source = `{"schemaVersion":1,"name":"fn","layers":{"base":{"south":{"label":"undo","press":{"type":"keys","keys":"F13"}},"east":{"press":{"type":"keys","keys":"F14"}}}},"functionKeys":{"back":{"south":{"label":"redo","press":{"type":"keys","keys":"F1"}}},"start":{"south":{"block":true}}}}`
	writeLibraryProfile(t, dir, "fn", source)
	writeLibraryProfile(t, dir, "plain", `{"schemaVersion":1,"name":"plain","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`)
	tl, src := startFocusLive(t, dir, "fn", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	explain := func(params map[string]string) rpcSlotExplanation {
		t.Helper()
		var result rpcExplanation
		if err := client.CallResult(ctx, "profiles.explain", params, &result); err != nil || len(result.Slots) != 1 {
			t.Fatalf("explain %v: %+v, %v", params, result, err)
		}
		return result.Slots[0]
	}
	selected := func(s rpcSlotExplanation) string {
		got := ""
		for _, l := range s.Layers {
			if l.Selected {
				got += l.Layer
			}
		}
		for _, f := range s.FunctionSets {
			if f.Selected {
				got += f.Layer
			}
		}
		return got
	}

	t.Run("a library profile lists both sets and holds none", func(t *testing.T) {
		s := explain(map[string]string{"id": "fn", "control": "south"})
		if s.FunctionKey != "" || len(s.FunctionSets) != 2 {
			t.Fatalf("function key %q, sets %+v", s.FunctionKey, s.FunctionSets)
		}
		back, start := s.FunctionSets[0], s.FunctionSets[1]
		if back.Key != "back" || back.Layer != "function:back" || !back.HasSet || back.Active || back.Selected || back.Resolution != "inactive" ||
			back.Binding != "/functionKeys/back/south" || back.Label != "redo" || back.Actions["press"] == "" {
			t.Errorf("back %+v", back)
		}
		if start.Key != "start" || start.Layer != "function:start" || !start.HasSet || start.Active || !start.Block || start.Resolution != "inactive" {
			t.Errorf("start %+v", start)
		}
		if got := selected(s); got != "base" {
			t.Errorf("selected %q, want base", got)
		}
	})
	t.Run("a profile without the sets lists them as empty", func(t *testing.T) {
		s := explain(map[string]string{"id": "plain", "control": "south"})
		if len(s.FunctionSets) != 2 || s.FunctionSets[0].HasSet || s.FunctionSets[1].HasSet || s.FunctionSets[0].Binding != "" || s.FunctionSets[0].Resolution != "inactive" {
			t.Errorf("sets %+v", s.FunctionSets)
		}
	})
	t.Run("a set the held function key owns wins over the layers", func(t *testing.T) {
		button(src, "back", true)
		tl.sync()
		s := explain(map[string]string{"device": "1", "control": "south"})
		if s.FunctionKey != "back" {
			t.Fatalf("function key %q, want back", s.FunctionKey)
		}
		if got := selected(s); got != "function:back" {
			t.Errorf("selected %q, want function:back", got)
		}
		if f := s.FunctionSets[0]; !f.Active || !f.Selected || f.Resolution != "binding" {
			t.Errorf("back %+v", f)
		}
		if f := s.FunctionSets[1]; f.Active || f.Selected {
			t.Errorf("start %+v while back is held", f)
		}
		for _, l := range s.Layers {
			if l.Layer == "base" && (l.Selected || l.Resolution != "shadowed") {
				t.Errorf("base %+v under the set", l)
			}
		}
		// A control the set does not bind falls through to the layers.
		s = explain(map[string]string{"device": "1", "control": "east"})
		if f := s.FunctionSets[0]; !f.Active || f.Selected || f.Resolution != "fallthrough" {
			t.Errorf("back for east %+v", f)
		}
		if got := selected(s); got != "base" {
			t.Errorf("selected %q for east, want base", got)
		}
		button(src, "back", false)
		tl.sync()
		if s := explain(map[string]string{"device": "1", "control": "south"}); s.FunctionKey != "" || selected(s) != "base" || s.FunctionSets[0].Active {
			t.Errorf("after the release: key %q, selected %q", s.FunctionKey, selected(s))
		}
	})
	t.Run("a set can block the control", func(t *testing.T) {
		button(src, "start", true)
		tl.sync()
		s := explain(map[string]string{"device": "1", "control": "south"})
		if f := s.FunctionSets[1]; s.FunctionKey != "start" || !f.Active || !f.Selected || f.Resolution != "blocked" {
			t.Errorf("key %q, start %+v", s.FunctionKey, f)
		}
		button(src, "start", false)
		tl.sync()
	})
	t.Run("a function key is no control to bind", func(t *testing.T) {
		var result rpcExplanation
		if err := client.CallResult(ctx, "profiles.explain", map[string]string{"id": "fn", "control": "back"}, &result); err != nil || !result.IsFunctionKey {
			t.Errorf("explain back: %+v, %v", result, err)
		}
		if err := client.CallResult(ctx, "profiles.explain", map[string]string{"id": "fn", "control": "south"}, &result); err != nil || result.IsFunctionKey {
			t.Errorf("explain south: %+v, %v", result, err)
		}
	})
}
