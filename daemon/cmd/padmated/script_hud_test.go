package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The panels a slot reports are those of its running script (SCR-22):
// none while the runtime is paused or stopped, a resumed runtime starts
// with none, and the old profile's runtime that a failed switch leaves
// installed shows none under the new profile.
func TestScriptRuntimeHUDPanels(t *testing.T) {
	if got := new(scriptRuntime).HUDPanels(); got != nil {
		t.Fatalf("a runtime with no lifecycle: %v", got)
	}
	dir := t.TempDir()
	const source = `import { hud, on } from "padmate";
on("south", "press", () => hud.panel("brush", { title: "画笔", rows: [["大小", "12"]] }));`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	t.Cleanup(func() {
		_ = s.ReleaseAll()
		_ = s.Dispose()
		simtest.AssertNoneHeld(t, arb, recorder)
	})
	press := func() {
		s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
		s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	}
	want := []script.HUDPanel{{ID: "brush", Title: "画笔", Rows: [][2]string{{"大小", "12"}}}}
	if got := s.View().Panels; got != nil {
		t.Fatalf("panels before the press: %v", got)
	}
	press()
	if got := s.View().Panels; !reflect.DeepEqual(got, want) {
		t.Fatalf("panels = %+v, want %+v", got, want)
	}

	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if got := s.View().Panels; got != nil {
		t.Fatalf("panels while paused: %v", got)
	}
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for (r.pending != nil || r.lifecycle == nil || r.lifecycle.Current() == nil) && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		s.Advance()
		time.Sleep(time.Millisecond)
	}
	if r.pending != nil || r.lifecycle == nil || r.lifecycle.Current() == nil {
		t.Fatal("resume did not adopt a fresh runtime")
	}
	if got := s.View().Panels; got != nil {
		t.Fatalf("the runtime built at the resume has the old panels: %v", got)
	}
	press()
	if got := s.View().Panels; !reflect.DeepEqual(got, want) {
		t.Fatalf("panels of the rebuilt runtime = %+v, want %+v", got, want)
	}

	// A runtime stopped by its wall budget (stopForWall) shows nothing.
	r.wallStopped = true
	if got := s.View().Panels; got != nil {
		t.Fatalf("panels of a stopped runtime: %v", got)
	}
	r.wallStopped = false
	// What a failed switch leaves: the old folder's runtime still
	// installed while the definition names the new profile's folder.
	r.activeRoot = filepath.Join(dir, "elsewhere")
	if got := s.View().Panels; got != nil {
		t.Fatalf("panels of the old profile's runtime after a failed switch: %v", got)
	}
}
