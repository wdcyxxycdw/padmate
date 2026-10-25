package engine_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func TestPersistentLayers(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"layers","common":{"leftstick":{"press":{"type":"layer","op":"cycle","layers":["base","mode2","mode3"]}}},"layers":{"base":{},"mode2":{},"mode3":{},"alt":{},"momentary":{}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	if err := e.ChangeLayer("switch", "mode2"); err != nil {
		t.Fatal(err)
	}
	if err := e.ChangeLayer("enable", "alt"); err != nil {
		t.Fatal(err)
	}
	oldRelease, err := e.HoldLayer("momentary")
	if err != nil {
		t.Fatal(err)
	}
	memory := e.PersistentLayers()
	if memory.Base != "mode2" || !slices.Equal(memory.Toggled, []string{"alt"}) || len(memory.Cycles) != 1 {
		t.Fatalf("snapshot: %+v", memory)
	}
	for _, i := range memory.Cycles {
		if i != 1 {
			t.Fatal(memory.Cycles)
		}
	}
	e.CancelAll(0)
	if err := e.RestoreLayers(memory); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.PersistentLayers(), memory) {
		t.Fatalf("roundtrip: %+v", e.PersistentLayers())
	}
	if len(r.out) != 0 {
		t.Fatal("restore emitted keys", r.out)
	}
	// A hold callback from the disposed runtime cannot release a new hold of
	// the same layer after restoration.
	release, err := e.HoldLayer("momentary")
	if err != nil {
		t.Fatal(err)
	}
	oldRelease()
	if !slices.Contains(e.LayerState().Overlays, "momentary") {
		t.Fatal("stale callback removed new hold")
	}
	release()
	if slices.Contains(e.LayerState().Overlays, "momentary") {
		t.Fatal("new hold did not release")
	}
	runLayerScript(t, e, r, "+leftstick@1 -leftstick@2")
	if e.LayerState().Base != "mode3" {
		t.Fatal("cycle cursor was not restored", e.LayerState())
	}
	e.CancelAll(3)
	if err := e.RestoreLayers(profile.LayerMemory{Base: "removed", Toggled: []string{"alt", "alt", "removed"}}); err != nil {
		t.Fatal(err)
	}
	if e.LayerState().Base != "base" || !slices.Equal(e.LayerState().Overlays, []string{"alt"}) {
		t.Fatal("did not normalize obsolete state", e.LayerState())
	}
}

func TestToggleUnderHeldLayerPublishesPersistentChange(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"layers","common":{"south":{"press":{"type":"layer","op":"toggle","layer":"alt"}}},"layers":{"base":{},"alt":{}}}`)
	r := &recorder{}
	var e *engine.Engine
	var saved profile.LayerMemory
	e = engine.New(prog, r, engine.Options{LayerChange: func(engine.LayerState) { saved = e.PersistentLayers() }})
	release, err := e.HoldLayer("alt")
	if err != nil {
		t.Fatal(err)
	}
	runLayerScript(t, e, r, "+south@1 -south@2")
	if !slices.Equal(saved.Toggled, []string{"alt"}) {
		t.Fatal("toggle under held overlay was not published", saved)
	}
	runLayerScript(t, e, r, "+south@3 -south@4")
	if len(saved.Toggled) != 0 {
		t.Fatal("toggle removal under held overlay was not published", saved)
	}
	release()
	e.CancelAll(5)
}

func TestRestoreLayersDoesNotReplayHeld(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"layers","layers":{"base":{"south":{"press":{"type":"keys","keys":"Space"}}},"alt":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runLayerScript(t, e, r, "+south@0")
	if err := e.RestoreLayers(profile.LayerMemory{Base: "alt"}); err == nil {
		t.Fatal("restored while a cycle owned keys")
	}
	e.CancelAll(1)
	r.out = nil // The caller releases the old Arbiter owners at this boundary.
	if err := e.RestoreLayers(profile.LayerMemory{Base: "alt"}); err != nil {
		t.Fatal(err)
	}
	runLayerScript(t, e, r, "-south@2")
	if len(r.out) != 0 {
		t.Fatal("replayed held control", r.out)
	}
	runLayerScript(t, e, r, "+south@3 -south@30")
	if len(r.out) != 2 {
		t.Fatal("fresh press/release not restored", r.out)
	}
	e.CancelAll(31)
}
