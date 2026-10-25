package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// The second pad is already held while it waits for the first pad's slot.
// Setup must run after the assignment's Resync, with the allocated key and
// full input state. A release from before activation must not run a script.
func TestSupervisorScriptFirstSetupSeesAssignedPad(t *testing.T) {
	dir := t.TempDir()
	entry := `import { defineScript, on, pad, key } from "padmate";
	export default defineScript({ setup() {
	  if (pad.info.name === "Second" && pad.info.key.endsWith("#1") &&
	      pad.isDown("south") && pad.axis("lefty") > 0.5 && pad.has("south")) {
	    on("east", "press", () => key.down("X"));
	    on("east", "release", () => key.up("X"));
	  }
	  on("south", "release", () => key.down("Z"));
	} });`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	p, diagnostics := profile.Parse([]byte(`{"schemaVersion":0,"name":"startup","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`))
	if p == nil || diagnostics.HasErrors() {
		t.Fatalf("profile: %v", diagnostics)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.Real()
	recorder := simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, recorder, output.LoopOptions{})
	defer func() {
		if err := out.Stop(); err != nil {
			t.Error(err)
		}
		recorder.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	}()
	events := make(chan input.Event, 16)
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), p.Script, false)
	sup, err := supervisor.New(supervisor.Config{Clock: clk, Events: events, Output: out, Program: program, ScriptFactory: def.Factory, Slots: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	step := func() {
		t.Helper()
		c, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if err := sup.Sync(c); err != nil {
			t.Fatal(err)
		}
	}
	c, stop := context.WithTimeout(context.Background(), 2*time.Second)
	if err := sup.Pause(c, true); err != nil {
		t.Fatal(err)
	}
	stop()
	guid := "0123456789abcdef0123456789abcdef"
	first := &input.DeviceInfo{ID: 1, Name: "First", Type: "xboxone", GUID: guid, IsGamepad: true, ButtonMask: 1, AxisMask: 1 << 1}
	second := &input.DeviceInfo{ID: 2, Name: "Second", Type: "xboxone", GUID: guid, IsGamepad: true, ButtonMask: 1, AxisMask: 1 << 1}
	events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: first}
	events <- input.Event{Kind: input.DeviceAdded, Device: 2, Info: second}
	events <- input.Event{Kind: input.ButtonDown, Device: 2, Control: "south"}
	events <- input.Event{Kind: input.AxisMotion, Device: 2, Control: "lefty", Value: -24000}
	step()
	events <- input.Event{Kind: input.DeviceRemoved, Device: 1}
	step()
	if key := sup.Status().Slots[0].Key.String(); key != guid+"#1" {
		t.Fatalf("allocated key = %q", key)
	}
	c, stop = context.WithTimeout(context.Background(), 2*time.Second)
	if err := sup.Pause(c, false); err != nil {
		t.Fatal(err)
	}
	stop()
	// Keep testing a fresh control until the worker candidate is installed.
	// The handler exists only when setup saw the handoff snapshot.
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(recorder.Trace(), "+X@") && time.Now().Before(deadline) {
		events <- input.Event{Kind: input.ButtonDown, Device: 2, Control: "east"}
		events <- input.Event{Kind: input.ButtonUp, Device: 2, Control: "east"}
		step()
		if !strings.Contains(recorder.Trace(), "+X@") {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !strings.Contains(recorder.Trace(), "+X@") {
		t.Fatalf("setup did not see allocated pad snapshot; output=%q", recorder.Trace())
	}
	events <- input.Event{Kind: input.ButtonUp, Device: 2, Control: "south"}
	step()
	if trace := recorder.Trace(); strings.Contains(trace, "+Z@") {
		t.Fatalf("old held cycle called release handler: %q", trace)
	}
}

func TestSupervisorAddsScriptAfterScriptlessAssignment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, pad, key } from "padmate";
export default defineScript({ onActivate() {
  if (pad.info.name === "First" && pad.isDown("south") && pad.axis("lefty") > 0.5) key.down("X");
} });`), 0o600); err != nil {
		t.Fatal(err)
	}
	parse := func(data string) (*profile.Profile, *engine.Program) {
		t.Helper()
		p, diagnostics := profile.Parse([]byte(data))
		if p == nil || diagnostics.HasErrors() {
			t.Fatalf("profile: %v", diagnostics)
		}
		program, err := engine.Compile(p)
		if err != nil {
			t.Fatal(err)
		}
		return p, program
	}
	_, initial := parse(`{"schemaVersion":0,"name":"startup","layers":{"base":{}}}`)
	withScript, updated := parse(`{"schemaVersion":0,"name":"startup","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`)
	clk := clock.Real()
	recorder := simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, recorder, output.LoopOptions{})
	events := make(chan input.Event, 8)
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), nil, false)
	sup, err := supervisor.New(supervisor.Config{
		Clock: clk, Events: events, Output: out, Program: initial, Slots: 1,
		ScriptFactory: def.Factory, ScriptConfigured: def.Configured,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := out.Stop(); err != nil {
			t.Error(err)
		}
		recorder.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	info := &input.DeviceInfo{ID: 1, Name: "First", Type: "xboxone", GUID: "0123456789abcdef0123456789abcdef", IsGamepad: true, ButtonMask: 1, AxisMask: 1 << 1}
	events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: info}
	events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "south"}
	events <- input.Event{Kind: input.AxisMotion, Device: 1, Control: "lefty", Value: -24000}
	deadlineCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	if err := sup.Sync(deadlineCtx); err != nil {
		t.Fatal(err)
	}
	stop()
	def.update(withScript.Script)
	deadlineCtx, stop = context.WithTimeout(context.Background(), 2*time.Second)
	if _, err := sup.Reload(deadlineCtx, updated); err != nil {
		t.Fatal(err)
	}
	stop()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(recorder.Trace(), "+X@") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(recorder.Trace(), "+X@") {
		t.Fatalf("new script did not start with assigned pad snapshot: %s", recorder.Trace())
	}
}
