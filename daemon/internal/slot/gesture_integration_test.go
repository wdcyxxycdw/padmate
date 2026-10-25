package slot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type gestureBridge struct{ *scriptSlotBridge }

func (b *gestureBridge) GestureEntries() []engine.GestureEntry      { return b.host.GestureEntries() }
func (b *gestureBridge) ObserveGesture(e engine.GestureEvent) error { return b.host.ObserveGesture(e) }
func (b *gestureBridge) DispatchGesture(id uint64, e engine.GestureEvent) (bool, error) {
	return b.host.DispatchGesture(id, e)
}
func (b *gestureBridge) EndGesture(e engine.GestureEvent) error { return b.host.EndGesture(e) }
func (b *gestureBridge) BindLayers(a engine.LayerAccess)        { b.host.BindLayers(a) }

func newGestureSlot(t *testing.T, sim *simtest.Sim, profileSource, source string) (*slot.Slot, *script.Host) {
	t.Helper()
	h := script.NewHostWithOptions(script.HostOptions{Clock: sim.Clock, Output: sim.Arb})
	p, err := goja.Compile("gestures.js", source, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Gen: 1, Clock: sim.Clock, Arbiter: sim.Arb, Program: scriptProgram(t, profileSource), Script: &gestureBridge{&scriptSlotBridge{h}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.ReleaseAll(); err != nil {
			t.Error(err)
		}
		if err := s.Dispose(); err != nil {
			t.Error(err)
		}
	})
	return s, h
}

func TestGesturePriorityClaimDisposalAndMetadata(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"priority","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`, `
var seen=[];
const low=__padmate.global().on("south","press",e=>seen.push("low"),{priority:1,label:"low"});
const high=__padmate.global().on("south","press",e=>{seen.push("high");e.pass();},{priority:2,label:"high"});
const claim=__padmate.global().claim("south");
__padmate.on("south","press",e=>{seen.push("base");e.pass();});`)
	entries := h.GestureEntries()
	if len(entries) != 4 || entries[1].Label != "high" || entries[1].Priority != 2 || !entries[2].Claim {
		t.Fatalf("metadata: %+v", entries)
	}
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	if got := h.VM.Get("seen").String(); got != "high" || len(sim.Arb.Held()) != 0 {
		t.Fatalf("claim/pass reached lower source: %s %s", got, sim.Output())
	}
	if _, err := h.VM.RunString(`claim.dispose();high.dispose();low.dispose();`); err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if len(sim.Arb.Held()) != 1 || h.VM.Get("seen").String() != "high,base" {
		t.Fatalf("disposed registrations: %+v %s", sim.Arb.Held(), h.VM.Get("seen"))
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureOptionsAndMultiCountsUseFirstSnapshot(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"multi","layers":{"base":{},"ink":{}}}`, `
var seen=[];
__padmate.on("south","tap",e=>seen.push(e.gesture+":"+e.count));
__padmate.on("south","doubleTap",e=>seen.push(e.gesture+":"+e.count));
__padmate.on("south","tripleTap",e=>seen.push(e.gesture+":"+e.count));
__padmate.on("south","quadTap",e=>seen.push(e.gesture+":"+e.count),{multiTapMs:30});
__padmate.layer("ink").on("south","doubleTap",e=>seen.push("wrong snapshot"));
__padmate.on("west","tap",e=>seen.push("west tap"),{holdMs:25});
__padmate.on("west","hold",e=>{seen.push("west hold:"+e.count);e.holdKeys("W");},{priority:2,holdMs:25});
__padmate.on("west","hold",e=>seen.push("unreachable"),{priority:1,holdMs:60000});`)
	for n := 1; n <= 4; n++ {
		for i := 0; i < n; i++ {
			s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
			sim.Clock.Advance(time.Millisecond)
			s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
			if n == 2 && i == 0 {
				if _, err := h.VM.RunString(`__padmate.layers.enable("ink")`); err != nil {
					t.Fatal(err)
				}
			}
			sim.Clock.Advance(time.Millisecond)
		}
		sim.Clock.Advance(300 * time.Millisecond)
		s.Advance()
		if _, err := h.VM.RunString(`__padmate.layers.disable("ink")`); err != nil {
			t.Fatal(err)
		}
	}
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "west"})
	sim.Clock.Advance(25 * time.Millisecond)
	s.Advance()
	if got := h.VM.Get("seen").String(); got != "tap:1,doubleTap:2,tripleTap:3,quadTap:4,west hold:1" {
		t.Fatalf("gesture count, timing or snapshot: %s", got)
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "west"})
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureObserversRunAcrossLayersWithoutConsuming(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"observe","layers":{"base":{"south":{"tap":{"type":"keys","keys":"A"},"hold":{"type":"keys","keys":"H"}}},"ink":{}}}`, `
var seen=[];
__padmate.observe("south",e=>{seen.push(e.gesture);e.pass();});
__padmate.layers.enable("ink");`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	sim.Clock.Advance(10 * time.Millisecond)
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	sim.Clock.Advance(30 * time.Millisecond)
	s.Advance()
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	sim.Clock.Advance(250 * time.Millisecond)
	s.Advance()
	if len(sim.Arb.Held()) != 1 {
		t.Fatalf("observer consumed declaration: %s", sim.Output())
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	if got := h.VM.Get("seen").String(); got != "press,tap,release,press,hold,release" {
		t.Fatalf("observer events: %s", got)
	}
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureHeldTracksPhysicalReleaseAndRejectsLateHolds(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"late","layers":{"base":{}}}`, `
var seen=[];var saved;
__padmate.on("west","tap",()=>{});
__padmate.on("west","hold",()=>{});
__padmate.on("south","press",e=>{saved=e;seen.push(e.held);try {e.holdKeys("S");} catch(err) {seen.push(err instanceof TypeError);}e.onEnd(()=>seen.push("end"));});`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "west"})
	sim.Clock.Advance(10 * time.Millisecond)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	sim.Clock.Advance(10 * time.Millisecond)
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "west"})
	s.Advance()
	if got := h.VM.Get("seen").String(); got != "false,true,end" {
		t.Fatalf("buffered press held state: %s", got)
	}
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if !h.VM.Get("saved").ToObject(h.VM).Get("held").ToBoolean() {
		t.Fatal("current press not held")
	}
	if _, err := h.VM.RunString(`globalThis.old=saved;globalThis.held=__padmate.key.isHeld("S")`); err != nil {
		t.Fatal(err)
	}
	if !h.VM.Get("held").ToBoolean() {
		t.Fatal("key.isHeld omitted gesture holds")
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Advance()
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if h.VM.Get("old").ToObject(h.VM).Get("held").ToBoolean() {
		t.Fatal("saved event became held in later cycle")
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Advance()
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureLayersReferenceCountingAndChangeNotifications(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"layerapi","layers":{"base":{"leftshoulder":{"press":{"type":"layer","op":"hold","layer":"ink"}}},"ink":{},"other":{}}}`, `
var changes=[];
__padmate.layers.onChange(active=>changes.push(active.join("/")));
__padmate.on("south","press",e=>{e.holdLayer("ink");e.holdLayer("ink");});`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "leftshoulder"})
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "leftshoulder"})
	if _, err := h.VM.RunString(`__padmate.layers.disable("ink");globalThis.on=__padmate.layers.isActive("ink")`); err != nil {
		t.Fatal(err)
	}
	if !h.VM.Get("on").ToBoolean() {
		t.Fatal("disable removed held overlay")
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Advance()
	if _, err := h.VM.RunString(`var a=__padmate.layers.hold("ink");var b=__padmate.layers.hold("ink");a.dispose();a.dispose();__padmate.layers.toggle("ink");b.dispose();globalThis.on=__padmate.layers.isActive("ink");__padmate.layers.disable("ink");__padmate.layers.switch("other");`); err != nil {
		t.Fatal(err)
	}
	s.Advance()
	if !h.VM.Get("on").ToBoolean() {
		t.Fatal("toggle reference was lost with hold")
	}
	if got := h.VM.Get("changes").String(); got != "base,base/ink,base,base/ink,base,other" {
		t.Fatalf("layer notifications: %s", got)
	}
	if _, err := h.VM.RunString(`__padmate.layers.enable("missing")`); err == nil {
		t.Fatal("missing layer accepted")
	}
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureVirtualHeldFlagsClearAfterModeReload(t *testing.T) {
	sim := simtest.New(t)
	profileSource := `{"schemaVersion":0,"name":"mode","analog":{"ls":{"mode":"eightWay","directionDelayMs":20}},"layers":{"base":{}}}`
	s, h := newGestureSlot(t, sim, profileSource, `var seen=[];__padmate.on("ls.right","press",e=>{seen.push("right");e.holdKeys("R")});`)
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 28000})
	sim.Clock.Advance(5 * time.Millisecond)
	if err := s.Reload(scriptProgram(t, profileSource), 2); err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 28001})
	sim.Clock.Advance(30 * time.Millisecond)
	s.Advance()
	if h.VM.Get("seen").String() != "" {
		t.Fatal("canceled direction reentered on jitter")
	}
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 0})
	sim.Clock.Advance(20 * time.Millisecond)
	s.Advance()
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 28000})
	sim.Clock.Advance(20 * time.Millisecond)
	s.Advance()
	if h.VM.Get("seen").String() != "right" {
		t.Fatalf("virtual cancellation stayed stuck: %s", h.VM.Get("seen"))
	}
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 0})
	sim.Clock.Advance(20 * time.Millisecond)
	s.Advance()
	if s.State().Axes[control.LeftX] != 0 {
		t.Fatal("state lost axis neutral")
	}
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureSetupLayersAreStagedInOrder(t *testing.T) {
	sim := simtest.New(t)
	_, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"setup","layers":{"base":{},"ink":{},"other":{}}}`, `
var a=__padmate.layers.hold("ink");
__padmate.layers.enable("other");
var canceled=__padmate.layers.hold("base");canceled.dispose();`)
	v, err := h.VM.RunString(`__padmate.layers.active.join("/")`)
	if err != nil || v.String() != "base/ink/other" {
		t.Fatalf("setup layer order: %v %v", v, err)
	}
	v, err = h.VM.RunString(`a.dispose();__padmate.layers.active.join("/")`)
	if err != nil || v.String() != "base/other" {
		t.Fatalf("staged hold release: %v %v", v, err)
	}
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureHoldKeysEndsWithoutReleasingOtherOwners(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"holds","layers":{"base":{"east":{"press":{"type":"keys","keys":"Shift"}},"west":{"tap":{"type":"keys","keys":"A"}}}}}`, `
__padmate.on("west","hold",e=>{e.holdKeys("Shift");e.onEnd(()=>{globalThis.ended=true});});`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "east"})
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "west"})
	sim.Clock.Advance(250 * time.Millisecond)
	s.Advance()
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatalf("hold dispatch errors: %v; registrations:%+v", errs, h.GestureEntries())
	}
	if len(sim.Arb.Held()) != 1 {
		t.Fatalf("hold not established: %+v", sim.Arb.Held())
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "west"})
	s.Advance()
	if len(sim.Arb.Held()) != 1 {
		t.Fatal("gesture ended another owner's hold")
	}
	if ended := h.VM.Get("ended"); ended == nil || !ended.ToBoolean() {
		t.Fatalf("onEnd did not run; metadata:%+v output:%s", h.GestureEntries(), sim.Output())
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "east"})
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureLayerSnapshotAndPerGestureFallthrough(t *testing.T) {
	sim := simtest.New(t)
	s, h := newGestureSlot(t, sim, `{"schemaVersion":0,"name":"layers","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"},"release":{"type":"keys","keys":"B","mode":"tap"}}},"ink":{"south":{"tap":{"type":"keys","keys":"C"}}}}}`, `
__padmate.on("leftshoulder","press",e=>e.holdLayer("ink"));
__padmate.layer("ink").on("south","release",e=>{globalThis.scope=e.scope;});
__padmate.on("south","press",e=>{e.holdKeys("D");});`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "leftshoulder"})
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if len(sim.Arb.Held()) != 1 {
		t.Fatalf("base press not reachable through overlay tap: %+v", sim.Arb.Held())
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "leftshoulder"})
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Advance()
	if h.VM.Get("scope").String() != "layer:ink" {
		t.Fatal("release did not use press layer snapshot")
	}
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}
	sim.Advance(30 * time.Millisecond)
	s.Advance()
	simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
}

func TestGestureHoldsReleaseAtSafetyBoundaries(t *testing.T) {
	for _, boundary := range []string{"release", "pause", "reload", "disconnect", "dispose"} {
		t.Run(boundary, func(t *testing.T) {
			sim := simtest.New(t)
			profile := `{"schemaVersion":0,"name":"safety","layers":{"base":{},"ink":{}}}`
			s, h := newGestureSlot(t, sim, profile, `__padmate.on("south","press",e=>{e.holdKeys("Space");e.holdLayer("ink");e.onEnd(()=>__padmate.key.down("X"));});`)
			s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
			if len(sim.Arb.Held()) != 1 {
				t.Fatal("no gesture hold")
			}
			switch boundary {
			case "release":
				_ = s.ReleaseAll()
			case "pause":
				_ = s.SetPaused(true)
			case "reload":
				_ = s.Reload(scriptProgram(t, profile), 2)
			case "disconnect":
				s.Handle(input.Event{Kind: input.DeviceRemoved})
			case "dispose":
				_ = s.Dispose()
			}
			_ = h.RunDue()
			if got := sim.Output(); strings.Contains(got, "+X") {
				t.Fatalf("old continuation ran at %s: %s", boundary, got)
			}
			simtest.AssertNoneHeld(t, sim.Arb, sim.Out)
		})
	}
}
