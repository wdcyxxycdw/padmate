package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

type gestureRegistry struct {
	entries []engine.GestureEntry
	events  []engine.GestureEvent
	ends    []engine.GestureEvent
	pass    bool
}

func (r *gestureRegistry) GestureEntries() []engine.GestureEntry { return r.entries }
func (r *gestureRegistry) ObserveGesture(engine.GestureEvent)    {}
func (r *gestureRegistry) DispatchGesture(_ uint64, e engine.GestureEvent) bool {
	r.events = append(r.events, e)
	return !r.pass
}
func (r *gestureRegistry) EndGesture(e engine.GestureEvent) { r.ends = append(r.ends, e) }

func TestScriptGestureUsesEnginePendingQueue(t *testing.T) {
	r := &recorder{}
	g := &gestureRegistry{entries: []engine.GestureEntry{{ID: 1, Control: control.West, Gesture: engine.GestureHold, Scope: "base"}}}
	p := compile(t, `"west":{"tap":{"type":"keys","keys":"A"}}`, southTap)
	e := engine.New(p, r, engine.Options{Gestures: g})
	run(t, e, r, "+west@0 +south@10 -south@20 -west@30 apply@300")
	if len(g.events) != 1 || g.events[0].Gesture != engine.GestureHold || int64(g.events[0].Time) != int64(20*ms) {
		t.Fatalf("script hold did not use balanced interruption: %+v", g.events)
	}
	if len(g.ends) != 2 || !strings.Contains(r.String(), "@20 tap decl/2 Ctrl+Z") {
		t.Fatalf("queue/end: %s %+v", r.String(), g.ends)
	}
}

func TestScriptClaimRemovesUnreachableWaiting(t *testing.T) {
	r := &recorder{}
	g := &gestureRegistry{entries: []engine.GestureEntry{
		{ID: 1, Control: control.West, Gesture: engine.GesturePress, Scope: "global"},
		{ID: 2, Control: control.West, Scope: "global", Claim: true},
	}}
	p := compile(t, `"west":{"tap":{"type":"keys","keys":"A"},"hold":{"type":"keys","keys":"B"}}`)
	e := engine.New(p, r, engine.Options{Gestures: g})
	run(t, e, r, "+west@0 -west@10 apply@300")
	if len(g.events) != 1 || g.events[0].Time != 0 || r.String() != "" {
		t.Fatalf("unreachable gesture delayed/ran: %+v %s", g.events, r.String())
	}
}

func TestAnalogCancelDoesNotReenterOnJitter(t *testing.T) {
	p := compileAnalog(t, `{"ls":{"mode":"eightWay","directionDelayMs":20}}`, `"ls.right":{"press":{"type":"keys","keys":"R"}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	run(t, e, r, "leftx=28000@0 cancel@5 leftx=28001@6 apply@30 leftx=0@40 apply@60 leftx=28000@70 apply@90 leftx=0@100 apply@120")
	if got := r.String(); strings.Contains(got, "@30 down") || !strings.Contains(got, "@90 down") {
		t.Fatalf("canceled deflection reentered: %s", got)
	}
}

func TestAnalogReloadSettlesChangedVirtualControls(t *testing.T) {
	bindings := `"ls.up":{"press":{"type":"keys","keys":"U"}},"ls.right":{"press":{"type":"keys","keys":"R"}},"ls.upright":{"press":{"type":"keys","keys":"D"}}`
	r := new(recorder)
	e := engine.New(compileAnalog(t, `{"ls":{"mode":"standard","directionDelayMs":1}}`, bindings), r, engine.Options{})
	axisStep(e, r, control.LeftX, 24000, 0)
	axisStep(e, r, control.LeftY, -24000, 0)
	r.now = timeAt(1)
	e.Apply(r.now)
	r.now = timeAt(2)
	e.Reload(r.now, compileAnalog(t, `{"ls":{"mode":"eightWay","directionDelayMs":1}}`, bindings))
	if e.ControlDown(control.LSUp) || e.ControlDown(control.LSRight) {
		t.Fatal("reload retained obsolete cardinal controls")
	}
	before := r.String()
	axisStep(e, r, control.LeftX, 24001, 3)
	r.now = timeAt(4)
	e.Apply(r.now)
	if r.String() != before {
		t.Fatalf("reload jitter started new direction: %s", r.String())
	}
	axisStep(e, r, control.LeftX, 0, 5)
	axisStep(e, r, control.LeftY, 0, 5)
	r.now = timeAt(6)
	e.Apply(r.now)
	axisStep(e, r, control.LeftX, 24000, 7)
	axisStep(e, r, control.LeftY, -24000, 7)
	r.now = timeAt(8)
	e.Apply(r.now)
	if !strings.Contains(r.String(), "@8 down") {
		t.Fatalf("neutral did not rearm new virtual direction: %s", r.String())
	}
}

func timeAt(ms int) clock.Time { return clock.Time(time.Duration(ms) * time.Millisecond) }
