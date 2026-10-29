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

// profiles.explain lists the function set beside the layers and says whether
// the function key is held (MAP-27, ADR-0069, ADR-0071, ADR-0075). The set,
// when it binds the control and the function key is held now, wins over the
// layers; a binding on the function key itself is ignored.
func TestIPCExplainFunctionKeys(t *testing.T) {
	dir := t.TempDir()
	const source = `{"schemaVersion":1,"name":"fn","layers":{"base":{"south":{"label":"undo","press":{"type":"keys","keys":"F13"}},"east":{"press":{"type":"keys","keys":"F14"}},"dpad.up":{"press":{"type":"keys","keys":"F17"}},"back":{"label":"paste","press":{"type":"keys","keys":"F15"}},"start":{"press":{"type":"keys","keys":"F16"}}}},"functionSet":{"south":{"label":"redo","press":{"type":"keys","keys":"F1"}},"dpad.up":{"block":true},"start":{"press":{"type":"keys","keys":"F2"}}}}`
	writeLibraryProfile(t, dir, "fn", source)
	writeLibraryProfile(t, dir, "plain", `{"schemaVersion":1,"name":"plain","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`)
	tl, src := startFocusLive(t, dir, "fn", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	explainAll := func(params map[string]string) rpcExplanation {
		t.Helper()
		var result rpcExplanation
		if err := client.CallResult(ctx, "profiles.explain", params, &result); err != nil || len(result.Slots) != 1 {
			t.Fatalf("explain %v: %+v, %v", params, result, err)
		}
		return result
	}
	explain := func(params map[string]string) rpcSlotExplanation { t.Helper(); return explainAll(params).Slots[0] }
	selected := func(s rpcSlotExplanation) string {
		got := ""
		for _, l := range s.Layers {
			if l.Selected {
				got += l.Layer
			}
		}
		if s.FunctionSet.Selected {
			got += s.FunctionSet.Layer
		}
		return got
	}
	layer := func(s rpcSlotExplanation, name string) rpcLayerResult {
		for _, l := range s.Layers {
			if l.Layer == name {
				return l
			}
		}
		t.Fatalf("no layer %q in %+v", name, s.Layers)
		return rpcLayerResult{}
	}

	t.Run("a library profile lists the set and holds nothing", func(t *testing.T) {
		result := explainAll(map[string]string{"id": "fn", "control": "south"})
		s := result.Slots[0]
		if result.FunctionKey != "start" || result.IsFunctionKey || s.FunctionHeld {
			t.Fatalf("function key %q, is function key %t, held %t", result.FunctionKey, result.IsFunctionKey, s.FunctionHeld)
		}
		f := s.FunctionSet
		if f.Key != "start" || f.Layer != "function:start" || !f.HasSet || f.Active || f.Selected || f.Resolution != "inactive" ||
			f.Binding != "/functionSet/south" || f.Label != "redo" || f.Actions["press"] == "" {
			t.Errorf("function set %+v", f)
		}
		if got := selected(s); got != "base" {
			t.Errorf("selected %q, want base", got)
		}
	})
	t.Run("a profile without the set lists it as empty", func(t *testing.T) {
		f := explain(map[string]string{"id": "plain", "control": "south"}).FunctionSet
		if f.HasSet || f.Binding != "" || f.Resolution != "inactive" || f.Key != "start" || f.Actions == nil {
			t.Errorf("function set %+v", f)
		}
	})
	t.Run("the set wins over the layers while the function key is held", func(t *testing.T) {
		button(src, "start", true)
		tl.sync()
		s := explain(map[string]string{"device": "1", "control": "south"})
		if !s.FunctionHeld {
			t.Fatal("the function key is not held")
		}
		if got := selected(s); got != "function:start" {
			t.Errorf("selected %q, want function:start", got)
		}
		if f := s.FunctionSet; !f.Active || !f.Selected || f.Resolution != "binding" {
			t.Errorf("function set %+v", f)
		}
		if l := layer(s, "base"); l.Selected || l.Resolution != "shadowed" {
			t.Errorf("base %+v under the set", l)
		}
		// A control the set does not bind falls through to the layers.
		s = explain(map[string]string{"device": "1", "control": "east"})
		if f := s.FunctionSet; !f.Active || f.Selected || f.Resolution != "fallthrough" {
			t.Errorf("set for east %+v", f)
		}
		if got := selected(s); got != "base" {
			t.Errorf("selected %q for east, want base", got)
		}
		// A set can block the control.
		s = explain(map[string]string{"device": "1", "control": "dpad.up"})
		if f := s.FunctionSet; !f.Active || !f.Selected || f.Resolution != "blocked" {
			t.Errorf("set for dpad.up %+v", f)
		}
		button(src, "start", false)
		tl.sync()
		if s := explain(map[string]string{"device": "1", "control": "south"}); s.FunctionHeld || selected(s) != "base" || s.FunctionSet.Active {
			t.Errorf("after the release: held %t, selected %q", s.FunctionHeld, selected(s))
		}
	})
	t.Run("another center key is an ordinary control", func(t *testing.T) {
		result := explainAll(map[string]string{"id": "fn", "control": "back"})
		if result.IsFunctionKey || selected(result.Slots[0]) != "base" || layer(result.Slots[0], "base").Binding != "/layers/base/back" {
			t.Errorf("explain back: %+v", result)
		}
		// pressed while the function key is held, it is not the function key
		button(src, "start", true)
		tl.sync()
		s := explain(map[string]string{"device": "1", "control": "back"})
		if !s.FunctionHeld || selected(s) != "base" || s.FunctionSet.Selected {
			t.Errorf("back under start: %+v", s)
		}
		button(src, "start", false)
		tl.sync()
	})
	t.Run("the function key is no control to bind", func(t *testing.T) {
		result := explainAll(map[string]string{"id": "fn", "control": "start"})
		s := result.Slots[0]
		if !result.IsFunctionKey || result.FunctionKey != "start" {
			t.Errorf("explain start: %+v", result)
		}
		if selected(s) != "" || layer(s, "base").Resolution != "ignored" || s.FunctionSet.Resolution != "ignored" || s.FunctionSet.Binding != "/functionSet/start" {
			t.Errorf("explain start: layers %+v, set %+v", s.Layers, s.FunctionSet)
		}
		if result := explainAll(map[string]string{"id": "fn", "control": "south"}); result.IsFunctionKey {
			t.Errorf("explain south: %+v", result)
		}
	})
}

// With another function key in config.json, the explanation follows it.
func TestIPCExplainFunctionKeyFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "fn", `{"schemaVersion":1,"name":"fn","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}},"start":{"press":{"type":"keys","keys":"F16"}},"guide":{"press":{"type":"keys","keys":"F18"}}}},"functionSet":{"south":{"press":{"type":"keys","keys":"F1"}}}}`)
	tl, src := startFocusLive(t, dir, "fn", `{"schemaVersion":0,"functionKey":"guide"}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var guide, start, south rpcExplanation
	for control, result := range map[string]*rpcExplanation{"guide": &guide, "start": &start, "south": &south} {
		if err := client.CallResult(ctx, "profiles.explain", map[string]string{"device": "1", "control": control}, result); err != nil {
			t.Fatal(err)
		}
	}
	if !guide.IsFunctionKey || guide.FunctionKey != "guide" || start.IsFunctionKey || south.IsFunctionKey {
		t.Fatalf("guide %+v, start %+v, south %+v", guide, start, south)
	}
	if f := guide.Slots[0].FunctionSet; f.Key != "guide" || f.Layer != "function:guide" {
		t.Errorf("function set %+v", f)
	}
	button(src, "guide", true)
	tl.sync()
	if err := client.CallResult(ctx, "profiles.explain", map[string]string{"device": "1", "control": "south"}, &south); err != nil || !south.Slots[0].FunctionHeld || !south.Slots[0].FunctionSet.Selected {
		t.Errorf("south under guide: %+v, %v", south, err)
	}
	button(src, "guide", false)
	tl.sync()
}
