package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Real slot and output loops receive only the initial axis event. Subsequent
// taps must come from the runtime deadline, without tests calling RunDue.
func TestContinuousIdleSlot20HzAndDeadzoneStop(t *testing.T) {
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 128)}
	out := output.StartLoop(clk, sender, output.LoopOptions{})
	dir := t.TempDir()
	source := `import {onStick,key} from "padmate";import {RateAccumulator} from "padmate/math";
const acc=new RateAccumulator();onStick("rs",(s,dt)=>{if(s.direction(4)===-1){acc.reset();return;}if(acc.step(20,dt))key.tap("F13");});`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI, TickHz: 125}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, out.Remote())
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, out.Remote(), r, `{}`)
	edges := make(chan slot.Edge)
	jobs := make(chan func(*slot.Slot))
	done := make(chan error, 1)
	go func() { done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges, Jobs: jobs}) }()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := out.Stop(); err != nil {
			t.Error(err)
		}
	})
	send := func(e input.Event) { edges <- slot.Edge{Event: e} }
	send(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, IsGamepad: true}})
	send(input.Event{Kind: input.AxisMotion, Device: 1, Control: "rightx", Value: 32767})
	start := time.Now()
	count := 0
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for running := true; running; {
		select {
		case e := <-sender.events:
			if !e.up && e.key == "F13" {
				count++
			}
		case <-timer.C:
			running = false
		}
	}
	if count < 18 || count > 21 {
		t.Fatalf("%d taps over %v, want approximately 20 Hz", count, time.Since(start))
	}
	send(input.Event{Kind: input.AxisMotion, Device: 1, Control: "rightx", Value: 0})
	barrier := make(chan struct{})
	jobs <- func(*slot.Slot) { close(barrier) }
	<-barrier
	returned := time.Now()
	settle := time.NewTimer(80 * time.Millisecond)
	defer settle.Stop()
	for {
		select {
		case e := <-sender.events:
			if !e.up && time.Since(returned) > 8*time.Millisecond {
				t.Fatalf("down after deadzone tick: %+v", e)
			}
		case <-settle.C:
			return
		}
	}
}
