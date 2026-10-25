package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// Session identity goes through the real supervisor reload barrier and
// runtime worker swap. The profiles deliberately have identical names.
func TestSessionSupervisorProfileSwitchReloadAndPause(t *testing.T) {
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 128)}
	out := output.StartLoop(clk, sender, output.LoopOptions{})
	dir := t.TempDir()
	source := `import {defineScript,on,key,session} from "padmate";
on("south","press",()=>{const n=(session.get("seq")||0)+1;session.set("seq",n);key.tap("F"+(12+n));});
export default defineScript({onActivate(){key.tap("F24");}});`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	program := func(name string) *engine.Program {
		p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"` + name + `","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`))
		if len(ds) > 0 {
			t.Fatalf("profile: %v", ds)
		}
		prog, err := engine.Compile(p)
		if err != nil {
			t.Fatal(err)
		}
		return prog
	}
	prog := program("same name")
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), prog.Profile().Script, false)
	events := make(chan input.Event, 16)
	sup, err := supervisor.New(supervisor.Config{Clock: clk, Events: events, Output: out, Program: prog, ProfileKey: "profile-a", ScriptFactory: def.Factory, ScriptConfigured: def.Configured, Slots: 1})
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
	})
	wait := func(key string) {
		t.Helper()
		timeout := time.NewTimer(3 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case e := <-sender.events:
				if e.key == key && !e.up {
					return
				}
			case <-timeout.C:
				t.Fatalf("no %s output", key)
			}
		}
	}
	events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, IsGamepad: true, Name: "Pad"}}
	wait("F24")
	press := func(want string) {
		events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "south"}
		wait(want)
		events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "south"}
		if err := sup.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	press("F13")
	if _, err := sup.Reload(ctx, program("renamed")); err != nil {
		t.Fatal(err)
	}
	wait("F24")
	press("F14")
	if err := sup.Pause(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sup.Pause(ctx, false); err != nil {
		t.Fatal(err)
	}
	wait("F24")
	press("F15")
	if _, err := sup.ReloadProfile(ctx, prog, "profile-b"); err != nil {
		t.Fatal(err)
	}
	wait("F24")
	press("F13")
	if _, err := sup.ReloadProfile(ctx, prog, "profile-a"); err != nil {
		t.Fatal(err)
	}
	wait("F24")
	press("F16")
}
