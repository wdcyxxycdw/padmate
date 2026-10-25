package script

import (
	"context"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func TestLifecyclePadSnapshotInSetupAndSwap(t *testing.T) {
	clk := clock.NewFake()
	initial := PadState{Connected: true, Info: input.DeviceInfo{Name: "initial", Type: "xboxone", IsGamepad: true}, Key: "key#1"}
	initial.HasControl[control.South] = true
	life := NewLifecycle(clk, HostOptions{Pad: initial})
	p, err := goja.Compile("pad-snapshot.js", `globalThis.__padmate_bundle={default:{setup(){
	 globalThis.setupPad=[__padmate.pad.info.name,__padmate.pad.info.key,__padmate.pad.has("south")];
	 __padmate.on("south","release",()=>{globalThis.released=true});
	},onActivate(){globalThis.activePad=[__padmate.pad.info.name,__padmate.pad.isDown("south")];}}}`, false)
	if err != nil {
		t.Fatal(err)
	}
	result := <-life.Load(context.Background(), func(context.Context) (*goja.Program, error) { return p, nil })
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	updated := initial
	updated.Info.Name = "updated"
	updated.Down[control.South] = true
	life.UpdatePad(updated)
	if err := life.Swap(result.Host); err != nil {
		t.Fatal(err)
	}
	h := life.Current()
	setup := h.VM.Get("setupPad").Export().([]any)
	if setup[0] != "initial" || setup[1] != "key#1" || setup[2] != true {
		t.Fatalf("setup snapshot = %v", setup)
	}
	active := h.VM.Get("activePad").Export().([]any)
	if active[0] != "updated" || active[1] != true {
		t.Fatalf("activation snapshot = %v", active)
	}
	updated.Down[control.South] = false
	life.UpdatePad(updated)
	r, err := life.Dispatch(Event{Control: "south", Gesture: "release"})
	released := h.VM.Get("released")
	if err != nil || r.Consumed || (released != nil && released.ToBoolean()) {
		t.Fatalf("old held cycle release: result=%+v err=%v", r, err)
	}
	if err := life.Dispose(); err != nil {
		t.Fatal(err)
	}
}

func TestPadAxisEndpointsAndUpdateDoesNotCallScriptSetters(t *testing.T) {
	h := NewHost()
	_, err := h.VM.RunString(`globalThis.setterCalls=0;
	 try { Object.defineProperty(__padmate.pad,"info",{set(){setterCalls++}}); } catch (_) {}
	 Object.defineProperty(Object.prototype,"name",{set(){setterCalls++},configurable:true});`)
	if err != nil {
		t.Fatal(err)
	}
	state := PadState{Connected: true, Info: input.DeviceInfo{Name: "first", Type: "ps5"}}
	h.UpdatePad(state)
	if got := h.VM.Get("setterCalls").ToInteger(); got != 0 {
		t.Fatalf("UpdatePad called JS setter %d times", got)
	}
	for _, tc := range []struct {
		raw  int16
		x, y float64
	}{
		{-32768, -1, 1}, {32767, 1, -1}, {-1, -1.0 / 32768, 1.0 / 32768}, {1, 1.0 / 32767, -1.0 / 32767},
	} {
		state.Axes[control.LeftX], state.Axes[control.LeftY] = tc.raw, tc.raw
		h.UpdatePad(state)
		v, err := h.VM.RunString(`[__padmate.pad.axis("leftx"),__padmate.pad.axis("lefty"),__padmate.pad.info.name]`)
		if err != nil {
			t.Fatal(err)
		}
		got := v.ToObject(h.VM)
		x, y, name := got.Get("0").ToFloat(), got.Get("1").ToFloat(), got.Get("2").String()
		if x != tc.x || y != tc.y || name != "first" {
			t.Errorf("raw %d: x=%v y=%v name=%q, want x=%v y=%v name=first", tc.raw, x, y, name, tc.x, tc.y)
		}
	}
	state.Info.Name = "second"
	h.UpdatePad(state)
	if got := h.VM.Get("setterCalls").ToInteger(); got != 0 {
		t.Fatalf("UpdatePad called JS setter %d times", got)
	}
	v, err := h.VM.RunString(`__padmate.pad.info.name`)
	if err != nil || v.String() != "second" {
		t.Fatalf("updated info = %v, %v", v, err)
	}
}
