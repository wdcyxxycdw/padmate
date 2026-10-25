package slot_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type scriptSlotBridge struct{ host *script.Host }

type configurableScriptBridge struct {
	*scriptSlotBridge
	configured bool
}

func (b *configurableScriptBridge) Configured() bool { return b.configured }

func (b *scriptSlotBridge) Dispatch(e slot.ScriptEvent) (bool, error) {
	r, err := b.host.DispatchEvent(script.Event{Control: e.Control, Gesture: e.Gesture, Time: e.Time, Duration: e.Duration, Count: e.Count, Cycle: e.Cycle, Scope: e.Scope, Held: e.Held})
	return r.Consumed, err
}
func (b *scriptSlotBridge) UpdatePad(p slot.PadSnapshot) {
	b.host.UpdatePad(script.PadState{Connected: p.Connected, Info: p.Info, Key: p.Key, Down: p.Down, Since: p.Since, Axes: p.Axes, HasControl: p.HasControl, HasAxis: p.HasAxis})
}
func (b *scriptSlotBridge) RunDue() error                    { return b.host.RunDue() }
func (b *scriptSlotBridge) NextDeadline() (clock.Time, bool) { return b.host.Loop.NextDeadline() }
func (b *scriptSlotBridge) ReleaseAll() error                { b.host.ReleaseKeys(); return nil }
func (b *scriptSlotBridge) Reload(uint32) error              { return nil }
func (b *scriptSlotBridge) Dispose() error                   { return b.host.Dispose() }

type hostSlotOutput struct{ arb *output.Arbiter }

func (o hostSlotOutput) Down(owner output.Owner, c keys.Chord) error { return o.arb.Down(owner, c) }
func (o hostSlotOutput) Up(owner output.Owner, c keys.Chord) error   { return o.arb.Up(owner, c) }
func (o hostSlotOutput) Tap(owner output.Owner, c keys.Chord, d time.Duration) error {
	return o.arb.Tap(owner, c, d)
}
func (o hostSlotOutput) ReleaseOwner(owner output.Owner) error {
	return o.arb.ReleaseOwners(func(candidate output.Owner) bool { return candidate == owner })
}

func newScriptSlot(t *testing.T, sim *simtest.Sim, program *engine.Program, source string) (*slot.Slot, *script.Host) {
	t.Helper()
	h := script.NewHostWithOptions(script.HostOptions{Clock: sim.Clock, Output: hostSlotOutput{sim.Arb}, Owner: output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript, Cycle: 1}})
	p, err := goja.Compile("script-integration.js", source, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: sim.Clock, Arbiter: sim.Arb, Program: program, Script: &scriptSlotBridge{h}})
	if err != nil {
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

func scriptProgram(t *testing.T, source string) *engine.Program {
	t.Helper()
	p, diagnostics := profile.Parse([]byte(source))
	if p == nil {
		t.Fatalf("parse profile: %v", diagnostics)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func TestScriptSlotScopesAndAsymmetricRelease(t *testing.T) {
	sim := simtest.New(t)
	program := scriptProgram(t, `{"schemaVersion":0,"name":"scope","layers":{"base":{"leftshoulder":{"press":{"type":"layer","op":"hold","layer":"ink"}},"south":{"press":{"type":"keys","keys":"Space","mode":"hold"},"release":{"type":"keys","keys":"X","mode":"tap"}}},"ink":{"south":{"press":{"type":"keys","keys":"Z","mode":"hold"}}}}}`)
	s, h := newScriptSlot(t, sim, program, `var seen=[]; const b=globalThis.__padmate;
	 b.global().on("south","press", e=>{seen.push("global:"+e.scope);e.pass()});
	 b.on("south","press", e=>{seen.push("base:"+e.scope);e.pass()});
	 b.on("south","release", e=>{seen.push("release:"+e.scope)});`)
	s.Handle(button("leftshoulder", true))
	s.Handle(button("south", true))
	s.Handle(button("south", false))
	s.Handle(button("leftshoulder", false))
	s.Handle(button("south", true))
	s.Handle(button("south", false))
	got := h.VM.Get("seen").Export().([]any)
	want := []string{"global:global", "global:global", "base:base", "release:base"}
	if len(got) != len(want) {
		t.Fatalf("handlers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("handlers = %v, want %v", got, want)
		}
	}
	if trace := simtest.FormatTrace(sim.Out.Edges()); trace != "+Z@0 -Z@0 +Space@0 -Space@0" {
		t.Fatalf("output = %q", trace)
	}
}

func TestScriptSlotPadSnapshotAcrossInputAndTimers(t *testing.T) {
	sim := simtest.New(t)
	s, h := newScriptSlot(t, sim, compile(t), `var seen=[]; const b=globalThis.__padmate;
	 b.observe("south",e=>seen.push([e.gesture,b.pad.isDown("south"),b.pad.heldFor("south"),b.pad.axis("lefty"),b.pad.has("south"),b.pad.has("misc6"),b.pad.info.name,b.pad.info.type,b.pad.info.key]));
	 b.every(20,()=>seen.push(["timer",b.pad.isDown("south"),b.pad.heldFor("south"),b.pad.axis("lefty"),b.pad.info.name]));`)
	info := &input.DeviceInfo{ID: 5, Name: "Xbox pad", Type: "xboxone", IsGamepad: true, ButtonMask: 1, AxisMask: 1 << 1}
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 5, Info: info, AssignedKey: "03ab#1"})
	sim.Clock.Set(clock.Time(10 * time.Millisecond))
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty", Value: -16384})
	sim.Clock.Set(clock.Time(20 * time.Millisecond))
	s.Advance()
	sim.Clock.Set(clock.Time(35 * time.Millisecond))
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 5})
	sim.Clock.Set(clock.Time(40 * time.Millisecond))
	s.Advance()
	got := h.VM.Get("seen").Export().([]any)
	if len(got) != 4 {
		t.Fatalf("observations = %v", got)
	}
	if row := got[0].([]any); row[0] != "press" || row[1] != true || row[4] != true || row[5] != false || row[6] != "Xbox pad" || row[7] != "xbox" || row[8] != "03ab#1" {
		t.Errorf("press = %v", row)
	}
	if row := got[1].([]any); row[0] != "timer" || row[1] != true || fmt.Sprint(row[2]) != "10" || math.Abs(row[3].(float64)-0.5) > 0.001 {
		t.Errorf("timer = %v", row)
	}
	if row := got[2].([]any); row[0] != "release" || row[1] != false || fmt.Sprint(row[2]) != "0" {
		t.Errorf("release = %v", row)
	}
	if row := got[3].([]any); row[0] != "timer" || row[1] != false || row[4] != "" {
		t.Errorf("removed = %v", row)
	}
}

func TestScriptSlotVirtualButtonsConsumeDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, bindings string }{
		{"declarative bindings", `"lt":{"press":{"type":"keys","keys":"L","mode":"hold"}},"ls.up":{"press":{"type":"keys","keys":"U","mode":"hold"}},"dpad.upleft":{"press":{"type":"keys","keys":"D","mode":"hold"}}`},
		{"script only", ""},
	} {
		t.Run(tc.name, func(t *testing.T) { testScriptVirtualButtons(t, tc.bindings) })
	}
}

func TestScriptSlotPhysicalAndVirtualCycleMetadata(t *testing.T) {
	sim := simtest.New(t)
	s, h := newScriptSlot(t, sim, scriptProgram(t, `{"schemaVersion":0,"name":"metadata","layers":{"base":{}}}`), `var seen=[];
	 for (const c of ["south", "lefttrigger+"]) for (const g of ["press", "release"])
	 __padmate.on(c,g,e=>seen.push([c,g,e.duration,e.count,e.cycle].join(":")));`)
	sim.Clock.Set(clock.Time(10 * time.Millisecond))
	s.Handle(button("south", true))
	sim.Clock.Set(clock.Time(15 * time.Millisecond))
	s.Handle(button("south", false))
	sim.Clock.Set(clock.Time(30 * time.Millisecond))
	s.Handle(button("south", true))
	sim.Clock.Set(clock.Time(32 * time.Millisecond))
	s.Handle(button("south", false))
	sim.Clock.Set(clock.Time(40 * time.Millisecond))
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	sim.Clock.Set(clock.Time(45 * time.Millisecond))
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 0})
	var got []string
	for _, value := range h.VM.Get("seen").Export().([]any) {
		got = append(got, value.(string))
	}
	want := []string{
		"south:press:0:1:1", "south:release:5:1:1",
		"south:press:0:1:2", "south:release:2:1:2",
		"lefttrigger+:press:0:1:3", "lefttrigger+:release:5:1:3",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("script cycle metadata = %v, want %v", got, want)
	}
}

func TestScriptOnlyVirtualControlsFollowProfileReload(t *testing.T) {
	sim := simtest.New(t)
	program := scriptProgram(t, `{"schemaVersion":0,"name":"virtual","layers":{"base":{}}}`)
	h := script.NewHost(sim.Clock)
	p, err := goja.Compile("virtual.js", `var seen=[]; __padmate.on("lefttrigger+", "press", () => seen.push("press"));`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	bridge := &configurableScriptBridge{scriptSlotBridge: &scriptSlotBridge{host: h}}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: sim.Clock, Arbiter: sim.Arb, Program: program, Script: bridge})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose() })
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	if got := h.VM.Get("seen").Export().([]any); len(got) != 0 {
		t.Fatalf("scriptless profile dispatched virtual control: %v", got)
	}
	bridge.configured = true
	if err := s.Reload(program, 2); err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 0})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	if got := h.VM.Get("seen").Export().([]any); len(got) != 1 {
		t.Fatalf("script profile missed virtual control: %v", got)
	}
	bridge.configured = false
	if err := s.Reload(program, 3); err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 0})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	if got := h.VM.Get("seen").Export().([]any); len(got) != 1 {
		t.Fatalf("removed script still dispatched virtual control: %v", got)
	}
}

func testScriptVirtualButtons(t *testing.T, bindings string) {
	sim := simtest.New(t)
	program := scriptProgram(t, `{"schemaVersion":0,"name":"virtual","analog":{"ls":{"directionDelayMs":1},"dpad":{"mode":"eightWay"}},"layers":{"base":{`+bindings+`}}}`)
	s, h := newScriptSlot(t, sim, program, `var seen=[]; const b=globalThis.__padmate;
	 for (const c of ["lt","ls.up","dpad.upleft"]) for (const g of ["press","release"])
	 b.on(c,g,e=>seen.push([c,g,b.pad.isDown(c),b.pad.has(c)]));`)
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 8, Info: &input.DeviceInfo{ID: 8, Name: "virtual", IsGamepad: true, ButtonMask: ^uint32(0), AxisMask: 63}})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty", Value: -24000})
	sim.Clock.Set(clock.Time(time.Millisecond))
	s.Advance()
	s.Handle(button("dpad.up", true))
	s.Handle(button("dpad.left", true))
	s.Handle(button("dpad.left", false))
	s.Handle(button("dpad.up", false))
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty", Value: 0})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 0})
	sim.Clock.Set(clock.Time(2 * time.Millisecond))
	s.Advance()
	got := h.VM.Get("seen").Export().([]any)
	for _, c := range []string{"lt", "ls.up", "dpad.upleft"} {
		var press, release bool
		for _, value := range got {
			row := value.([]any)
			if row[0] != c {
				continue
			}
			if row[1] == "press" && row[2] == true && row[3] == true {
				press = true
			}
			if row[1] == "release" && row[2] == false && row[3] == true {
				release = true
			}
		}
		if !press || !release {
			t.Errorf("%s press/release missing or pad snapshot wrong: %v", c, got)
		}
	}
	if trace := simtest.FormatTrace(sim.Out.Edges()); trace != "" {
		t.Fatalf("consumed virtual declarations output = %q", trace)
	}
}
