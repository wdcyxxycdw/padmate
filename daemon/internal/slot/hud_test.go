package slot_test

import (
	"reflect"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// hudScriptBridge is a runtime whose script keeps hud panels (SCR-22).
type hudScriptBridge struct{ *scriptSlotBridge }

func (b hudScriptBridge) HUDPanels() []script.HUDPanel { return b.host.HUDPanels() }

// slot.View carries the panels of the slot's running script, read on the
// slot's goroutine; a runtime without HUDPanels has none, and the panels go
// with the runtime's release.
func TestViewCarriesTheScriptPanels(t *testing.T) {
	sim := simtest.New(t)
	h := script.NewHostWithOptions(script.HostOptions{Clock: sim.Clock, Output: hostSlotOutput{sim.Arb}, Owner: output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript, Cycle: 1}})
	p, err := goja.Compile("hud.js", `const b = globalThis.__padmate;
	 b.on("south", "press", () => b.hud.panel("brush", { title: "画笔", rows: [["大小", "12"]] }));
	 b.on("east", "press", () => b.hud.panel("brush", null));`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: sim.Clock, Arbiter: sim.Arb, Program: compile(t), Script: hudScriptBridge{&scriptSlotBridge{h}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Dispose() })
	if v := s.View(); v.Panels != nil {
		t.Fatalf("panels before any: %+v", v.Panels)
	}
	s.Handle(button("south", true))
	s.Handle(button("south", false))
	want := []script.HUDPanel{{ID: "brush", Title: "画笔", Rows: [][2]string{{"大小", "12"}}}}
	if got := s.View().Panels; !reflect.DeepEqual(got, want) {
		t.Fatalf("panels = %+v, want %+v", got, want)
	}
	s.Handle(button("east", true))
	s.Handle(button("east", false))
	if got := s.View().Panels; got != nil {
		t.Fatalf("panels after removing it: %+v", got)
	}
	s.Handle(button("south", true))
	s.Handle(button("south", false))
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	h.ReleaseAll() // what the real runtime's ReleaseAll does to its host
	if got := s.View().Panels; got != nil {
		t.Fatalf("panels after ReleaseAll: %+v", got)
	}

	// A runtime that keeps no panels.
	plain, _ := newScriptSlot(t, simtest.New(t), compile(t), `0;`)
	if got := plain.View().Panels; got != nil {
		t.Fatalf("panels of a runtime without HUDPanels: %+v", got)
	}
}
