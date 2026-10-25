package engine_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

func TestInputPreviewIsIndependentAndNeverDispatches(t *testing.T) {
	r := &recorder{}
	callbacks := 0
	e := engine.New(compileAnalog(t, `{"ls":{"directionDelayMs":10}}`, `"south":{"press":{"type":"keys","keys":"F13","mode":"hold"}},"ls.up":{"press":{"type":"keys","keys":"F14"}}`), r, engine.Options{
		Button:      func(engine.ButtonEvent) bool { callbacks++; return false },
		LayerChange: func(engine.LayerState) { callbacks++ },
		Script:      func(engine.ScriptActionEvent) { callbacks++ },
		Profile:     func(string) { callbacks++ },
		Feedback:    func(engine.Feedback) { callbacks++ },
	})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South, Down: true})
	e.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftY, Value: -30000})
	e.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftTrigger, Value: 30000})
	before, calls := r.String(), callbacks
	var seen []engine.ButtonEvent
	p := e.PreviewInput(func(ev engine.ButtonEvent) { seen = append(seen, ev) })
	p.Step(engine.Input{T: clock.Time(time.Millisecond), Kind: engine.InputButton, Control: control.South})
	p.Step(engine.Input{T: clock.Time(2 * time.Millisecond), Kind: engine.InputAxis, Axis: control.LeftTrigger, Value: 16000})
	if !p.ControlDown(control.LT) {
		t.Fatal("preview lost trigger hysteresis")
	}
	p.Apply(clock.Time(30 * time.Millisecond))
	if !p.ControlDown(control.LSUp) || e.ControlDown(control.LSUp) || !e.ControlDown(control.South) {
		t.Fatal("preview shared source input state or lost pending stick direction")
	}
	if r.String() != before || callbacks != calls {
		t.Fatalf("preview dispatched source output/callbacks: %q -> %q, %d -> %d", before, r.String(), calls, callbacks)
	}
	// The source trigger is past fullPress, which the preview has copied: the
	// fall to 16000 releases the second stage too (ANA-14).
	want := []engine.ButtonEvent{{Time: clock.Time(time.Millisecond), Control: control.South}, {Time: clock.Time(2 * time.Millisecond), Control: control.LTFull}, {Time: clock.Time(10 * time.Millisecond), Control: control.LSUp, Down: true}}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("preview events=%+v, want %+v", seen, want)
	}
	e.CancelAll(clock.Time(time.Second))
}
