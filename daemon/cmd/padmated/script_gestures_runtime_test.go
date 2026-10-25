package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Uses the production factory, compiler, worker lifecycle, slot loop, engine
// resolver and output arbiter. All checks and fake-clock changes are jobs on
// the owning loop; no SendInput or real device is involved.
func TestProductionGestureLifecycle(t *testing.T) {
	dir := t.TempDir()
	source := `import {on, layer, layers, key, observe} from "padmate";
export let seen=[];
on("leftshoulder","press",e=>e.holdLayer("ink"));
on("south","tap",e=>seen.push("tap"),{holdMs:20});
layer("ink").on("south","hold",e=>{seen.push("hold:"+e.count);e.holdKeys("Shift");e.onEnd(()=>{seen.push("end");key.tap("F15")})},{holdMs:20,label:"brush"});
observe("south",e=>seen.push("observe:"+e.gesture));
layers.onChange(active=>seen.push(active.join("/")));`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: dtsgen.HostAPI.String()}, false)
	runtime, err := def.Factory(0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	r := runtime.(*scriptRuntime)
	// Add the layer required by the actual compiled layer() registration.
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"production","layers":{"base":{"east":{"press":{"type":"keys","keys":"Shift"}}},"ink":{}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Gen: 1, Clock: clk, Arbiter: arb, Program: prog, Script: runtime})
	if err != nil {
		t.Fatal(err)
	}
	edges := make(chan slot.Edge)
	jobs := make(chan func(*slot.Slot))
	done := make(chan error, 1)
	go func() { done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges, Jobs: jobs}) }()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Error(err)
		}
		simtest.AssertNoneHeld(t, arb, rec)
	})
	call := func(fn func(*slot.Slot)) {
		completed := make(chan struct{})
		jobs <- func(s *slot.Slot) { fn(s); close(completed) }
		<-completed
	}
	call(func(s *slot.Slot) {
		s.Handle(input.Event{Kind: input.DeviceAdded, Info: &input.DeviceInfo{IsGamepad: true}})
		var st slot.State
		st.Buttons[control.South] = true
		s.Resync(st)
	})
	ready := false
	limit := time.Now().Add(2 * time.Second)
	for !ready && time.Now().Before(limit) {
		call(func(s *slot.Slot) {
			clk.Advance(time.Millisecond)
			s.Advance()
			ready = r.lifecycle != nil && r.lifecycle.Current() != nil && r.pending == nil
		})
		if !ready {
			time.Sleep(time.Millisecond)
		}
	}
	if !ready {
		t.Fatal("production script did not activate after complete Resync")
	}
	var errs []error
	var seen string
	var held int
	call(func(s *slot.Slot) {
		s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
		s.Advance()
		seen = r.lifecycle.Current().VM.Get("__padmate_bundle").ToObject(r.lifecycle.Current().VM).Get("seen").String()
		errs = s.Errors()
	})
	if len(errs) > 0 || seen != "" {
		t.Fatalf("initial-held release called new runtime: %q %v", seen, errs)
	}
	call(func(s *slot.Slot) {
		s.Handle(input.Event{Kind: input.ButtonDown, Control: "east"})
		s.Handle(input.Event{Kind: input.ButtonDown, Control: "leftshoulder"})
		s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
		clk.Advance(20 * time.Millisecond)
		s.Advance()
		s.Handle(input.Event{Kind: input.ButtonUp, Control: "east"})
		held = len(arb.Held())
		errs = s.Errors()
	})
	if len(errs) > 0 || held != 1 {
		t.Fatalf("production hold sharing: held=%d errors=%v", held, errs)
	}
	call(func(s *slot.Slot) {
		s.Handle(input.Event{Kind: input.ButtonUp, Control: "leftshoulder"})
		s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	})
	// A barrier job follows the input without calling Advance. The loop must
	// find the queued onEnd's immediate deadline even with no user timers.
	call(func(s *slot.Slot) {
		clk.Advance(35 * time.Millisecond)
		s.Advance()
		seen = r.lifecycle.Current().VM.Get("__padmate_bundle").ToObject(r.lifecycle.Current().VM).Get("seen").String()
		held = len(arb.Held())
		errs = s.Errors()
	})
	if len(errs) > 0 || held != 0 || !strings.Contains(seen, "hold:1") || !strings.Contains(seen, "end") || strings.Contains(seen, "tap") {
		t.Fatalf("production gesture end: held=%d seen=%s errors=%v", held, seen, errs)
	}
}
