package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type coldFixture struct {
	s        *slot.Slot
	runtime  *scriptRuntime
	clock    *clock.Fake
	recorder *simtest.Recorder
	open     func()
}

func coldScriptFixture(t *testing.T, bindings string) coldFixture {
	t.Helper()
	source := `import {defineScript,on,key} from "padmate";
export default defineScript({actions:{paint:{run(){key.tap("F21");}}},setup(){
on("south","press",()=>{key.down("F22");return true;});
on("south","release",()=>{key.up("F22");return true;});
on("lt","press",()=>{key.down("F23");return true;});
on("lt","release",()=>{key.up("F23");return true;});
}});`
	return coldScriptFixtureSource(t, bindings, source)
}

func coldScriptFixtureSource(t *testing.T, bindings, source string) coldFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: coldScriptProgram(t, bindings), Arbiter: arb, Clock: clk, Script: r})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{Name: "cold fixture", IsGamepad: true}})
	gate := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }
	r.buildOverride = func(ctx context.Context, root string, cfg *profile.ScriptConfig) (*goja.Program, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		built, err := compile.BuildCached(compile.BuildOptions{Root: root, Entry: cfg.Entry, API: cfg.API})
		if err != nil {
			return nil, err
		}
		return goja.Compile(cfg.Entry, string(built.JS), false)
	}
	t.Cleanup(func() { open(); _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, rec) })
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	return coldFixture{s, r, clk, rec, open}
}

func TestColdScriptHeldInputKeepsFreshCycle(t *testing.T) {
	for _, tc := range []struct {
		name, control, axis string
		value               int16
	}{
		{name: "button", control: "south"},
		{name: "trigger", control: "lt", axis: "lefttrigger", value: 30000},
		{name: "stick", control: "ls.up", axis: "lefty", value: -30000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `import {defineScript,on,onTick,key,pad} from "padmate";
export default defineScript({setup(){
globalThis.setupDown=pad.isDown("` + tc.control + `");
on("` + tc.control + `","press",()=>{key.down("F22");return true;});
on("` + tc.control + `","release",()=>{key.up("F22");return true;});
onTick(()=>{globalThis.tickDown=pad.isDown("` + tc.control + `");});
},onActivate(){globalThis.activation=[pad.isDown("` + tc.control + `"),pad.heldFor("` + tc.control + `")];}});`
			f := coldScriptFixtureSource(t, `{"`+tc.control+`":{"press":{"type":"keys","keys":"F13","mode":"hold"}}}`, source)
			down := input.Event{Kind: input.ButtonDown, Control: tc.control}
			up := input.Event{Kind: input.ButtonUp, Control: tc.control}
			if tc.axis != "" {
				down = input.Event{Kind: input.AxisMotion, Control: tc.axis, Value: tc.value}
				up = input.Event{Kind: input.AxisMotion, Control: tc.axis}
			}
			f.clock.Advance(10 * time.Millisecond)
			f.s.Handle(down)
			// No Advance between this physical edge and the completed worker:
			// heldFor must include the entire wait, not start at the poll.
			f.clock.Advance(80 * time.Millisecond)
			f.ready(t)
			h := f.runtime.lifecycle.Current()
			activation := h.VM.Get("activation").Export().([]any)
			if activation[0] != true || h.VM.Get("setupDown").ToBoolean() != true {
				t.Errorf("startup snapshots: setup=%v activation=%v", h.VM.Get("setupDown"), activation)
			}
			if held := h.VM.Get("activation").ToObject(h.VM).Get("1").ToFloat(); held < 80 {
				t.Errorf("activation heldFor=%v, want >=80 ms", held)
			}
			found := false
			for _, e := range f.recorder.Edges() {
				if e.Name == "F13" {
					t.Error("held fresh press fell through to declaration")
				}
				if e.Name == "F22" && e.Kind == simtest.Down {
					found = true
				}
			}
			if !found {
				t.Error("held fresh press was not dispatched", f.recorder.Edges())
			}
			f.clock.Advance(20 * time.Millisecond)
			f.s.Advance()
			if value := h.VM.Get("tickDown"); value == nil || !value.ToBoolean() {
				t.Error("continuous callback did not see held input", value)
			}
			f.s.Handle(up)
			f.clock.Advance(100 * time.Millisecond)
			f.s.Advance()
			if len(f.recorder.Down()) != 0 {
				t.Fatal("fresh held cycle was not released", f.recorder.Down())
			}
		})
	}
}

func TestColdScriptQueuePreservesOldHeldCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, control, axis string
		value               int16
	}{
		{name: "button", control: "south"},
		{name: "trigger", control: "lt", axis: "lefttrigger", value: 30000},
		{name: "stick", control: "ls.up", axis: "lefty", value: -30000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `import {defineScript,on,key,pad} from "padmate";
export default defineScript({setup(){
on("` + tc.control + `","press",()=>{key.down("F22");return true;});
on("` + tc.control + `","release",()=>{key.up("F22");return true;});
on("north","press",()=>{key.down("F23");return true;});
on("north","release",()=>{key.up("F23");return true;});
},onActivate(){globalThis.oldHeld=pad.isDown("` + tc.control + `");}});`
			f := coldScriptFixtureSource(t, `{}`, source)
			down := input.Event{Kind: input.ButtonDown, Control: tc.control}
			up := input.Event{Kind: input.ButtonUp, Control: tc.control}
			if tc.axis != "" {
				down = input.Event{Kind: input.AxisMotion, Control: tc.axis, Value: tc.value}
				up = input.Event{Kind: input.AxisMotion, Control: tc.axis}
			}
			f.s.Handle(down)
			f.ready(t)
			if len(f.recorder.Down()) == 0 {
				t.Fatal("fixture old cycle did not press")
			}
			if err := f.s.SetPaused(true); err != nil {
				t.Fatal(err)
			}
			first := len(f.recorder.Edges())
			gate := make(chan struct{})
			var once sync.Once
			f.open = func() { once.Do(func() { close(gate) }) }
			t.Cleanup(f.open)
			build := f.runtime.buildOverride
			f.runtime.buildOverride = func(ctx context.Context, root string, cfg *profile.ScriptConfig) (*goja.Program, error) {
				select {
				case <-gate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return build(ctx, root, cfg)
			}
			if err := f.s.SetPaused(false); err != nil {
				t.Fatal(err)
			}
			f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "north"})
			f.ready(t)
			if !f.runtime.lifecycle.Current().VM.Get("oldHeld").ToBoolean() {
				t.Error("activation did not see old held physical state")
			}
			f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "north"})
			f.s.Handle(up)
			found := false
			for _, e := range f.recorder.Edges()[first:] {
				if e.Name == "F22" {
					t.Error("old held cycle was replayed across pause", e)
				}
				if e.Name == "F23" && e.Kind == simtest.Down {
					found = true
				}
			}
			if !found {
				t.Error("fresh cycle alongside old held was lost")
			}
			first = len(f.recorder.Edges())
			f.s.Handle(down)
			f.s.Handle(up)
			if len(f.recorder.Edges()) == first {
				t.Error("released old control could not start a fresh cycle")
			}
			if len(f.recorder.Down()) != 0 {
				t.Error("recovery left held output", f.recorder.Down())
			}
		})
	}
}

func TestColdScriptDelayedDirectionSnapshotAndHeldTime(t *testing.T) {
	source := `import {defineScript,on,key,pad} from "padmate";
export default defineScript({setup(){
on("ls.up","press",()=>{globalThis.pressHeld=pad.heldFor("ls.up");key.down("F22");return true;});
on("ls.up","release",()=>{key.up("F22");return true;});
},onActivate(){globalThis.activation=[pad.isDown("ls.up"),pad.heldFor("ls.up")];}});`
	f := coldScriptFixtureSource(t, `{}`, source)
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"delayed","script":{"entry":"main.ts","api":"0.21"},"analog":{"ls":{"directionDelayMs":10}},"layers":{"base":{}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Reload(prog, 2); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(10 * time.Millisecond)
	f.s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty", Value: -30000})
	f.clock.Advance(80 * time.Millisecond)
	f.ready(t)
	h := f.runtime.lifecycle.Current()
	activation := h.VM.Get("activation").ToObject(h.VM)
	if !activation.Get("0").ToBoolean() || activation.Get("1").ToFloat() < 70 {
		t.Error("activation lost delayed virtual state/time", activation.Export())
	}
	if held := h.VM.Get("pressHeld"); held == nil || held.ToFloat() < 70 {
		t.Error("replayed direction lost original held time", held)
	}
	f.s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty"})
	f.clock.Advance(20 * time.Millisecond)
	f.s.Advance()
	if len(f.recorder.Down()) != 0 {
		t.Fatal("delayed direction did not release", f.recorder.Down())
	}
}

func TestColdScriptBufferedGestureTiming(t *testing.T) {
	for _, tc := range []struct {
		name  string
		times []int
		want  string
	}{
		{"tap", []int{10, 40}, "F17"},
		{"hold", []int{10, 160}, "F18"},
		{"double", []int{10, 40, 80, 110}, "F19"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `import {defineScript,on,key} from "padmate";
export default defineScript({setup(){
on("west","tap",()=>{key.tap("F17");},{holdMs:100,multiTapMs:100});
on("west","hold",()=>{key.tap("F18");},{holdMs:100,multiTapMs:100});
on("west","doubleTap",()=>{key.tap("F19");},{holdMs:100,multiTapMs:100});
}});`
			f := coldScriptFixtureSource(t, `{}`, source)
			at := 0
			for i, ms := range tc.times {
				f.clock.Advance(time.Duration(ms-at) * time.Millisecond)
				at = ms
				kind := input.ButtonDown
				if i%2 == 1 {
					kind = input.ButtonUp
				}
				f.s.Handle(input.Event{Kind: kind, Control: "west"})
			}
			f.clock.Advance(time.Second)
			f.ready(t)
			f.clock.Advance(100 * time.Millisecond)
			f.s.Advance()
			var got []string
			for _, e := range f.recorder.Edges() {
				if e.Kind == simtest.Down {
					got = append(got, e.Name)
				}
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("buffered gesture=%v, want [%s]", got, tc.want)
			}
		})
	}
}

func coldScriptProgram(t *testing.T, bindings string) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"cold","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":` + bindings + `}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func (f coldFixture) ready(t *testing.T) {
	t.Helper()
	f.open()
	deadline := time.Now().Add(time.Second)
	for f.runtime.pending != nil && time.Now().Before(deadline) {
		f.clock.Advance(time.Millisecond)
		f.s.Advance()
		time.Sleep(time.Millisecond)
	}
	f.s.Advance()
	if f.runtime.pending != nil {
		t.Fatal("worker did not finish")
	}
}

func TestFirstScriptInputWaitsForColdWorker(t *testing.T) {
	for _, kind := range []string{"action", "registration", "axis"} {
		t.Run(kind, func(t *testing.T) {
			f := coldScriptFixture(t, `{"north":{"press":{"type":"script","name":"paint"}},"south":{"press":{"type":"keys","keys":"F13"}}}`)
			if kind == "action" {
				f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "north"})
				f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "north"})
			}
			if kind == "registration" {
				f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
				f.clock.Advance(20 * time.Millisecond)
				f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
			}
			if kind == "axis" {
				f.s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 30000})
				f.s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 0})
			}
			if len(f.recorder.Records()) != 0 {
				t.Fatal("input dispatched before script registrations ready", f.recorder.Edges())
			}
			f.ready(t)
			want := map[string]string{"action": "F21", "registration": "F22", "axis": "F23"}[kind]
			got := false
			for _, event := range f.recorder.Edges() {
				if event.Name == "F13" {
					t.Fatal("script claim fell through before worker ready")
				}
				if event.Name == want {
					got = true
				}
			}
			if !got {
				t.Fatal("first input was lost", f.recorder.Edges())
			}
			f.clock.Advance(100 * time.Millisecond)
			f.s.Advance()
			if len(f.recorder.Down()) != 0 {
				t.Fatal("buffered release was lost", f.recorder.Down())
			}
		})
	}
}

func TestColdScriptInputCanceledAtSafetyBoundaries(t *testing.T) {
	for _, boundary := range []string{"release", "pause", "reset", "disconnect", "reload", "script-reload", "dispose", "overflow"} {
		t.Run(boundary, func(t *testing.T) {
			f := coldScriptFixture(t, `{}`)
			f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
			switch boundary {
			case "release":
				_ = f.s.ReleaseAll()
			case "pause":
				_ = f.s.SetPaused(true)
				_ = f.s.SetPaused(false)
			case "reset":
				f.s.Reset()
			case "disconnect":
				f.s.Handle(input.Event{Kind: input.DeviceRemoved})
			case "reload":
				if err := f.s.Reload(coldScriptProgram(t, `{}`), 2); err != nil {
					t.Fatal(err)
				}
			case "script-reload":
				if err := f.s.ReloadScript(); err != nil {
					t.Fatal(err)
				}
			case "dispose":
				if err := f.s.Dispose(); err != nil {
					t.Fatal(err)
				}
			case "overflow":
				for range 129 {
					f.s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 0})
				}
			}
			f.ready(t)
			f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
			f.clock.Advance(100 * time.Millisecond)
			f.s.Advance()
			if len(f.recorder.Records()) != 0 {
				t.Fatal("canceled startup input was replayed", f.recorder.Edges())
			}
			if boundary == "pause" || boundary == "reload" || boundary == "overflow" {
				f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
				f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
				f.ready(t)
				if len(f.recorder.Records()) == 0 {
					t.Fatal("fresh input failed to recover", boundary)
				}
			}
		})
	}
}

func TestColdScriptInputBuildFailureDoesNotFallThrough(t *testing.T) {
	f := coldScriptFixture(t, `{"south":{"press":{"type":"keys","keys":"F13"}}}`)
	build := f.runtime.buildOverride
	f.runtime.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		return nil, errors.New("broken script")
	}
	f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	f.ready(t)
	f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	if len(f.recorder.Records()) != 0 {
		t.Fatal("failed startup fell through to declaration", f.recorder.Edges())
	}
	if len(f.s.Errors()) == 0 {
		t.Fatal("failed startup was not diagnosed")
	}
	f.runtime.buildOverride = build
	f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	f.ready(t)
	if len(f.recorder.Records()) == 0 {
		t.Fatal("fresh input did not restart after corrected build")
	}
	for _, event := range f.recorder.Edges() {
		if event.Name == "F13" {
			t.Fatal("recovery bypassed script claim")
		}
	}
}

func TestProfileReloadAfterSafetyReleaseStartsIdleWorker(t *testing.T) {
	f := coldScriptFixture(t, `{"north":{"press":{"type":"script","name":"paint"}}}`)
	if err := f.s.Reload(coldScriptProgram(t, `{"north":{"press":{"type":"script","name":"paint"}}}`), 2); err != nil {
		t.Fatal(err)
	}
	if f.runtime.pending == nil || f.runtime.paused {
		t.Fatal("profile reload remained dormant until first input")
	}
	f.ready(t)
	f.s.Handle(input.Event{Kind: input.ButtonDown, Control: "north"})
	f.s.Handle(input.Event{Kind: input.ButtonUp, Control: "north"})
	f.s.Advance()
	if len(f.recorder.Records()) == 0 {
		t.Fatal("first action after profile save lost")
	}
}
