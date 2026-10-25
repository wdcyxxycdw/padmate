package slot_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

func TestSlotPersistentLayers(t *testing.T) {
	for _, restore := range []bool{true, false} {
		t.Run(map[bool]string{true: "restore", false: "default"}[restore], func(t *testing.T) {
			sim := simtest.New(t)
			key := strings.Repeat("a0", 16)
			mem := profilelib.NewMemory(profile.State{}, restore)
			p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"paint","layers":{"base":{"north":{"press":{"type":"layer","op":"switch","layer":"mode2"}}},"mode2":{"east":{"press":{"type":"keys","keys":"Space"}}}}}`))
			if ds.HasErrors() {
				t.Fatal(ds)
			}
			prog, err := engine.Compile(p)
			if err != nil {
				t.Fatal(err)
			}
			s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock, ProfileKey: "paint", LayerMemory: mem})
			if err != nil {
				t.Fatal(err)
			}
			add := input.Event{Kind: input.DeviceAdded, Device: 1, AssignedKey: key, Info: &input.DeviceInfo{ID: 1, GUID: key, IsGamepad: true}}
			s.Handle(add)
			s.Handle(input.Event{Kind: input.ButtonDown, Control: "north"})
			s.Handle(input.Event{Kind: input.ButtonUp, Control: "north"})
			s.Handle(input.Event{Kind: input.ButtonDown, Control: "east"})
			sim.ExpectOutput("+Space@0")
			s.SetProfileKey("other")
			if err := s.Reload(compile(t, eastHold), 2); err != nil {
				t.Fatal(err)
			}
			sim.ExpectOutput("-Space@0")
			s.Handle(input.Event{Kind: input.ButtonUp, Control: "east"})
			sim.ExpectOutput("")
			s.SetProfileKey("paint")
			if err := s.Reload(prog, 3); err != nil {
				t.Fatal(err)
			}
			want := "base"
			if restore {
				want = "mode2"
			}
			if got := s.View().Mapping.Layers.Base; got != want {
				t.Fatalf("base %s, want %s", got, want)
			}
			s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 1})
			s.Handle(add)
			if got := s.View().Mapping.Layers.Base; got != want {
				t.Fatalf("reconnect base %s, want %s", got, want)
			}
			// Graceful loop exit saves before ReleaseAll clears transient state.
			edges := make(chan slot.Edge)
			close(edges)
			if err := slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges}); err != nil {
				t.Fatal(err)
			}
			if errs := s.Errors(); len(errs) != 0 {
				t.Fatal(errs)
			}
			if restore {
				got, ok := mem.Load(key, "paint")
				if !ok || got.Base != "mode2" {
					t.Fatal(got, ok)
				}
			}
		})
	}
}
