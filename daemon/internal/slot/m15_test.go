package slot_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// TestRingKnobAndSecondStageCapabilities checks which pads have the controls
// of the rings, the knob and the second trigger stage (ANA-10, ANA-11,
// ANA-14): the rings and the ticks of a stick need both its axes, the second
// stage the trigger axis.
func TestRingKnobAndSecondStageCapabilities(t *testing.T) {
	// Axis masks are by axis number - 1: leftx, lefty, rightx, righty,
	// lefttrigger, righttrigger.
	const leftX, leftY, rightX, rightY, leftTrigger = 1 << 0, 1 << 1, 1 << 2, 1 << 3, 1 << 4
	for _, tc := range []struct {
		name string
		mask uint8
		has  []control.Control
		not  []control.Control
	}{
		{
			"left stick and left trigger", leftX | leftY | leftTrigger,
			[]control.Control{control.LSInner, control.LSOuter, control.LSCW, control.LSCCW, control.LTFull},
			[]control.Control{control.RSInner, control.RSOuter, control.RSCW, control.RSCCW, control.RTFull},
		},
		{
			"one axis of each stick", leftX | rightY,
			nil,
			[]control.Control{control.LSInner, control.LSOuter, control.LSCW, control.LSCCW, control.RSInner, control.RSOuter, control.RSCW, control.RSCCW, control.LTFull, control.RTFull},
		},
		{
			"right stick", rightX | rightY,
			[]control.Control{control.RSInner, control.RSOuter, control.RSCW, control.RSCCW},
			[]control.Control{control.LSInner, control.LSCW, control.LTFull, control.RTFull},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simtest.New(t)
			s, err := slot.New(slot.Config{Gen: 1, Program: compile(t), Arbiter: sim.Arb, Clock: sim.Clock})
			if err != nil {
				t.Fatal(err)
			}
			s.Handle(input.Event{Kind: input.DeviceAdded, Device: 3, Info: &input.DeviceInfo{ID: 3, Name: "pad", IsGamepad: true, ButtonMask: 1, AxisMask: tc.mask}})
			v := s.View()
			for _, c := range tc.has {
				if !v.HasControl[c] {
					t.Errorf("HasControl[%s] is false", c)
				}
			}
			for _, c := range tc.not {
				if v.HasControl[c] {
					t.Errorf("HasControl[%s] is true", c)
				}
			}
		})
	}
}

// TestScriptSeesTicksRingsAndSecondStage checks that a script can handle the
// new controls with on(), and read them with pad.isDown and pad.has.
func TestScriptSeesTicksRingsAndSecondStage(t *testing.T) {
	sim := simtest.New(t)
	program := scriptProgram(t, `{"schemaVersion":1,"name":"m15","analog":{"rs":{"mode":"knob"}},"layers":{"base":{}}}`)
	s, h := newScriptSlot(t, sim, program, `var seen=[]; const b=globalThis.__padmate;
	 for (const c of ["rs.cw","rs.ccw","rs.inner","rs.outer","lt.full"]) for (const g of ["press","release"])
	   b.on(c,g,e=>seen.push([c,g,b.pad.isDown(c),b.pad.has(c)]));`)
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 8, Info: &input.DeviceInfo{ID: 8, Name: "virtual", IsGamepad: true, ButtonMask: ^uint32(0), AxisMask: 63}})
	// The slot loop calls Advance when a deadline falls due; the knob reads
	// the angle KnobSettle after the input that turned it.
	at := func(ms int) {
		sim.Clock.Set(clock.Time(time.Duration(ms) * time.Millisecond))
		s.Advance()
	}
	stick := func(deg, norm float64) {
		mag := 0.15 + 0.8*norm
		rad := deg * math.Pi / 180
		s.Handle(input.Event{Kind: input.AxisMotion, Control: "rightx", Value: int16(math.Round(mag * math.Sin(rad) * 32767))})
		s.Handle(input.Event{Kind: input.AxisMotion, Control: "righty", Value: int16(-math.Round(mag * math.Cos(rad) * 32767))})
	}
	at(1)
	stick(0, 0.5)
	at(100)
	stick(40, 0.5)
	at(200)
	stick(40, 0.95)
	at(300)
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 32767})
	at(400)
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 0})
	stick(40, 0.5)
	stick(40, 0)
	at(500)
	got := fmt.Sprint(h.VM.Get("seen").Export())
	// The tick goes down and up at the same time; the ring changes follow the
	// depth; the second stage comes with the trigger.
	want := "[[rs.inner press true true] [rs.cw press true true] [rs.cw release false true] [rs.inner release false true] [rs.outer press true true] [lt.full press true true] [lt.full release false true] [rs.outer release false true] [rs.inner press true true] [rs.inner release false true]]"
	if got != want {
		t.Fatalf("script saw\n%s\nwant\n%s", got, want)
	}
}
