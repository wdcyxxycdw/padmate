package script

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

func TestGestureRegistrationOptionsAndClaims(t *testing.T) {
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), LayerNames: []string{"base", "ink"}})
	if _, err := h.VM.RunString(`__padmate.layer("missing")`); err == nil {
		t.Fatal("unknown layer accepted")
	}
	p, err := goja.Compile("registry.js", `
__padmate.on("a","quadTap",()=>{}, {label:"quad",priority:12,holdMs:45,multiTapMs:80});
var dispose=__padmate.layer("ink").claim("ls");`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	entries := h.GestureEntries()
	if len(entries) < 3 || entries[0].Control != control.South || entries[0].Gesture != engine.GestureQuadTap || entries[0].Label != "quad" || entries[0].Priority != 12 || entries[0].Timing.HoldMs != 45 || entries[0].Timing.MultiTapMs != 80 {
		t.Fatalf("options metadata: %+v", entries)
	}
	for _, e := range entries[1:] {
		if e.Scope != "layer:ink" || !e.Claim {
			t.Fatalf("claim metadata: %+v", e)
		}
	}
	if _, err := h.VM.RunString(`dispose.dispose();dispose.dispose()`); err != nil {
		t.Fatal(err)
	}
	if len(h.GestureEntries()) != 1 {
		t.Fatal("disposed claim remains registered")
	}
	names := h.LayerNames()
	names[0] = "mutated"
	if h.LayerNames()[0] != "base" {
		t.Fatal("LayerNames aliases host snapshot")
	}
}

func TestGestureMomentaryHoldsRejectedAndEndIsMicrotask(t *testing.T) {
	h := NewHost(clock.NewFake())
	p, err := goja.Compile("momentary.js", `
var seen=[];__padmate.on("south","tap",e=>{
seen.push(e.held);
try {e.holdKeys("S")} catch(err) {seen.push(err instanceof TypeError)}
try {e.holdLayer("ink")} catch(err) {seen.push(err instanceof TypeError)}
e.onEnd(()=>seen.push("end"));seen.push("handler");
});`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	r := h.GestureEntries()[0]
	_, err = h.DispatchGesture(r.ID, engine.GestureEvent{Control: control.South, Gesture: engine.GestureTap, Count: 1, Cycle: 1, Runtime: r.Runtime})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.VM.Get("seen").String(); got != "false,true,true,handler,end" {
		t.Fatalf("momentary lifetime: %s", got)
	}
	if len(h.gestureStates) != 0 {
		t.Fatal("momentary event retained a held state")
	}
}

func TestGestureOldRuntimeSnapshotCannotCallSameRegistrationID(t *testing.T) {
	first := NewHost(clock.NewFake())
	second := NewHost(clock.NewFake())
	for _, h := range []*Host{first, second} {
		p, err := goja.Compile("same.js", `var called=false;__padmate.on("south","press",()=>{called=true})`, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Activate(p); err != nil {
			t.Fatal(err)
		}
	}
	r := first.GestureEntries()[0]
	if _, err := second.DispatchGesture(r.ID, engine.GestureEvent{Control: control.South, Gesture: engine.GesturePress, Cycle: 1, Runtime: r.Runtime}); err != nil {
		t.Fatal(err)
	}
	if second.VM.Get("called").ToBoolean() {
		t.Fatal("old snapshot invoked replacement registration")
	}
}

func TestLifecycleCanceledCandidateDoesNotReleaseGestureOwner(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{})
	l := NewLifecycle(clk, HostOptions{Output: arb})
	loaded := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return goja.Compile("hold.js", `__padmate.on("south","press",e=>{e.holdKeys("S");e.holdKeys("S")});`, false)
	})
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	h := l.Current()
	var pad PadState
	pad.Down[control.South] = true
	h.UpdatePad(pad)
	r := h.GestureEntries()[0]
	if _, err := h.DispatchGesture(r.ID, engine.GestureEvent{Control: control.South, Gesture: engine.GesturePress, Cycle: 1, Runtime: r.Runtime, Held: true}); err != nil {
		t.Fatal(err)
	}
	if len(h.Output()) != 1 {
		t.Fatalf("duplicate hold created references: %+v", h.Output())
	}
	candidate := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) { return goja.Compile("candidate.js", ``, false) })
	if candidate.Err != nil {
		t.Fatal(candidate.Err)
	}
	if err := candidate.Host.Discard(); err != nil {
		t.Fatal(err)
	}
	if len(arb.Held()) != 1 {
		t.Fatal("discarded candidate released a gesture owned by active runtime")
	}
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	if len(arb.Held()) != 0 {
		t.Fatalf("keys remain held: %s", rec.trace())
	}
}

func TestGestureEndMicrotaskWakesIdleHost(t *testing.T) {
	clk := clock.NewFake()
	h := NewHost(clk)
	p, err := goja.Compile("idle.js", `var ended=false;__padmate.on("south","press",e=>e.onEnd(()=>{ended=true}));`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	var pad PadState
	pad.Down[control.South] = true
	h.UpdatePad(pad)
	r := h.GestureEntries()[0]
	e := engine.GestureEvent{Control: control.South, Gesture: engine.GesturePress, Runtime: r.Runtime, Held: true, Cycle: 1}
	if _, err := h.DispatchGesture(r.ID, e); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	e.Gesture, e.Held = engine.GestureRelease, false
	if err := h.EndGesture(e); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.NextDeadline(); !ok || d != clk.Now() {
		t.Fatalf("idle onEnd has no immediate deadline: %v %v", d, ok)
	}
	if h.VM.Get("ended").ToBoolean() {
		t.Fatal("onEnd ran synchronously inside engine release")
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if !h.VM.Get("ended").ToBoolean() {
		t.Fatal("onEnd not drained")
	}
}

func TestGestureNormalEndRespectsMinPress(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: 15 * time.Millisecond})
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: arb})
	p, err := goja.Compile("minimum.js", `__padmate.on("south","press",e=>e.holdKeys("S"));`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	var pad PadState
	pad.Down[control.South] = true
	h.UpdatePad(pad)
	r := h.GestureEntries()[0]
	e := engine.GestureEvent{Control: control.South, Gesture: engine.GesturePress, Runtime: r.Runtime, Cycle: 1, Held: true}
	if _, err := h.DispatchGesture(r.ID, e); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	e.Gesture, e.Held = engine.GestureRelease, false
	if err := h.EndGesture(e); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+S]")
	clk.Advance(14 * time.Millisecond)
	if err := arb.Advance(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+S -S]")
	if len(arb.Held()) != 0 {
		t.Fatal("normal gesture release remains held")
	}
}
