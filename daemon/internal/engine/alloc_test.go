package engine_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// counter is an engine.Output that does not allocate.
type counter struct {
	downs, ups, taps, repeats int
	moves                     int
	dx, dy                    int64
	jumps                     int
	sequences, texts          int
}

func (c *counter) Down(engine.OwnerID, keys.Chord, time.Duration) { c.downs++ }
func (c *counter) Up(engine.OwnerID, keys.Chord)                  { c.ups++ }
func (c *counter) Tap(engine.OwnerID, keys.Chord, time.Duration)  { c.taps++ }
func (c *counter) Repeat(engine.OwnerID, keys.Chord)              { c.repeats++ }
func (c *counter) TapSequence(_ engine.OwnerID, items []keys.Chord, _, _ time.Duration) {
	c.taps += len(items)
}
func (c *counter) Sequence(_ engine.OwnerID, steps []keys.Step, _, _ time.Duration) {
	c.sequences += len(steps)
}
func (c *counter) TypeText(engine.OwnerID, string, time.Duration)      { c.texts++ }
func (c *counter) RepeatTap(engine.OwnerID, keys.Chord, time.Duration) { c.repeats++ }
func (*counter) StopRepeat(engine.OwnerID, keys.Chord)                 {}
func (c *counter) Move(_ engine.OwnerID, dx, dy int32) {
	c.moves++
	c.dx += int64(dx)
	c.dy += int64(dy)
}
func (c *counter) Jump(engine.OwnerID, int32, int32) { c.jumps++ }

// TestNoAllocs checks the allocation contract of Options.Trace: Step never
// allocates, paused or not, and Apply, CancelAll, Reset, Pause and Resume
// do not without a trace.
func TestNoAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	btn := func(c control.Control, down bool) engine.Input {
		return engine.Input{Kind: engine.InputButton, Control: c, Down: down}
	}
	trig := func(v int16) engine.Input {
		return engine.Input{Kind: engine.InputAxis, Axis: control.LeftTrigger, Value: v}
	}
	steps := map[string][]engine.Input{
		"hold":             {btn(control.East, true), btn(control.East, false)},
		"tap":              {btn(control.South, true), btn(control.South, false)},
		"unbound":          {btn(control.North, true), btn(control.North, false)},
		"spurious":         {btn(control.East, false), btn(control.LT, true), {Kind: 0}},
		"trigger crossing": {trig(32767), trig(8000), trig(0), trig(-5), trig(9830), trig(6553)},
		"stick":            {{Kind: engine.InputAxis, Axis: control.LeftX, Value: 100}},
		// The function key (MAP-27, start by default): its edges, and a press
		// under it, with an ordinary center key (back) pressed in between.
		"function key":      {btn(control.Start, true), btn(control.Start, false), btn(control.Back, true), btn(control.Back, false)},
		"function key held": {btn(control.Start, true), btn(control.South, true), btn(control.South, false), btn(control.Back, true), btn(control.Back, false), btn(control.East, true), btn(control.East, false), btn(control.Start, false)},
	}
	prog := compile(t, eastHold, southTap, ltAlt)
	var decisions int
	for _, trace := range []func(engine.Decision){nil, func(engine.Decision) { decisions++ }} {
		name := map[bool]string{true: "no trace", false: "trace"}[trace == nil]
		out := &counter{}
		e := engine.New(prog, out, engine.Options{Trace: trace})
		var now clock.Time
		for _, paused := range []bool{false, true} {
			if paused {
				e.Pause(now)
			}
			for s, ins := range steps {
				if n := testing.AllocsPerRun(100, func() {
					for _, in := range ins {
						now = now.Add(time.Millisecond)
						in.T = now
						e.Step(in)
					}
				}); n != 0 {
					t.Errorf("%s: Step %s (paused %t): %v allocations", name, s, paused, n)
				}
			}
		}
		e.Resume()
		if trace != nil {
			continue
		}
		for s, fn := range map[string]func(){
			"Apply":        func() { e.Apply(now) },
			"NextDeadline": func() { e.NextDeadline() },
			"CancelAll": func() {
				e.Step(btn(control.East, true))
				e.Step(trig(32767))
				e.CancelAll(now)
				e.Step(btn(control.East, false))
				e.Step(trig(0))
			},
			"Reset": func() {
				e.Step(btn(control.East, true))
				e.Reset(now)
			},
			"Pause": func() {
				e.Step(btn(control.East, true))
				e.Pause(now)
				e.Step(btn(control.South, true))
				e.Resume()
				e.Step(btn(control.East, false))
				e.Step(btn(control.South, false))
			},
		} {
			if n := testing.AllocsPerRun(100, fn); n != 0 {
				t.Errorf("%s: %s: %v allocations", name, s, n)
			}
		}
		if out.downs == 0 || out.ups == 0 || out.taps == 0 {
			t.Errorf("%s: output %+v: the steps did not run", name, out)
		}
	}
	if decisions == 0 {
		t.Error("the trace saw no decisions")
	}
}

// gestureAllocProfile has a binding for each M2 path of the engine
// (ENG-13): tap, double tap and hold with the balanced policy on west,
// holdPreferred on north, timeOnly on dpad.up, typematic repeat on east, a
// tap-mode sequence on leftshoulder, a latch on rightshoulder, and hold
// and toggle of the overlay fn, which binds south, on dpad.down and
// dpad.left, a sequence action on leftstick and a text action, and a sequence
// on hold, on rightstick (MAP-22, OUT-21), and the function set (MAP-27, start
// is the function key): it rebinds south and west. misc1 has only a
// double tap, so a single tap ends its window with no action (ADR-0071).
const gestureAllocProfile = `{"schemaVersion": 0, "name": "alloc", "layers": {
  "base": {
    "west": {"tap": {"type": "keys", "keys": "F13"}, "doubleTap": {"type": "keys", "keys": "F14"}, "hold": {"type": "keys", "keys": "E"}},
    "north": {"policy": "holdPreferred", "tap": {"type": "keys", "keys": "B"}, "hold": {"type": "keys", "keys": "Shift"}},
    "dpad.up": {"policy": "timeOnly", "tap": {"type": "keys", "keys": "F18"}, "hold": {"type": "keys", "keys": "F19"}},
    "east": {"press": {"type": "keys", "keys": "A", "repeat": {"delayMs": 30, "rateHz": 50}}},
    "south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}},
    "leftshoulder": {"press": {"type": "keys", "keys": ["Ctrl+K", "S"], "mode": "tap"}},
    "rightshoulder": {"latch": true, "press": {"type": "keys", "keys": "Ctrl"}},
    "dpad.down": {"press": {"type": "layer", "layer": "fn", "op": "hold"}},
    "dpad.left": {"press": {"type": "layer", "layer": "fn", "op": "toggle"}},
    "dpad.right": {"press": {"type": "keys", "keys": "F21", "repeat": {"style": "tap", "delayMs": 0, "rateHz": 1000}}},
    "leftstick": {"press": {"type": "sequence", "steps": [{"keys": "Ctrl+A"}, {"wait": 100}, {"text": "hi"}]}},
    "rightstick": {"tap": {"type": "text", "text": "你好"}, "hold": {"type": "sequence", "steps": [{"wait": 5}]}},
    "misc1": {"doubleTap": {"type": "keys", "keys": "F16"}}
  },
  "fn": {"south": {"press": {"type": "keys", "keys": "F20"}}}
}, "functionSet": {
  "south": {"press": {"type": "keys", "keys": "F22"}}, "west": {"press": {"type": "keys", "keys": "F18", "mode": "tap"}}
}}`

// gestureDriver drives an Engine in virtual milliseconds, running every
// deadline at its time, as slot.Replay does.
type gestureDriver struct {
	e   *engine.Engine
	now clock.Time
}

func (d *gestureDriver) edge(c control.Control, down bool) {
	d.e.Step(engine.Input{T: d.now, Kind: engine.InputButton, Control: c, Down: down})
}

func (d *gestureDriver) press(c control.Control)   { d.edge(c, true) }
func (d *gestureDriver) release(c control.Control) { d.edge(c, false) }

// wait moves ms forward.
func (d *gestureDriver) wait(ms int) {
	target := d.now.Add(time.Duration(ms) * time.Millisecond)
	for {
		t, ok := d.e.NextDeadline()
		if !ok || t > target {
			break
		}
		d.now = max(d.now, t)
		d.e.Apply(d.now)
	}
	d.now = target
}

// settle runs every deadline left, then moves 1 ms forward.
func (d *gestureDriver) settle() {
	for {
		t, ok := d.e.NextDeadline()
		if !ok {
			break
		}
		d.now = max(d.now, t)
		d.e.Apply(d.now)
	}
	d.wait(1)
}

// gestureScenario is one M2 path: run starts and ends with every control
// up and no deadline, and sends want each time.
type gestureScenario struct {
	name string
	run  func(d *gestureDriver)
	want counter
}

var gestureScenarios = []gestureScenario{
	{"tap, committed by the double-tap window", func(d *gestureDriver) {
		d.press(control.West)
		d.wait(10)
		d.release(control.West)
		d.settle()
	}, counter{taps: 1}},
	{"double tap", func(d *gestureDriver) {
		d.press(control.West)
		d.wait(10)
		d.release(control.West)
		d.wait(50)
		d.press(control.West)
		d.wait(10)
		d.release(control.West)
		d.settle()
	}, counter{taps: 1}},
	{"hold", func(d *gestureDriver) {
		d.press(control.West)
		d.wait(260)
		d.release(control.West)
		d.settle()
	}, counter{downs: 1, ups: 1}},
	{"a window that ends with no action for its taps", func(d *gestureDriver) {
		d.press(control.Misc1)
		d.wait(10)
		d.release(control.Misc1)
		d.settle()
	}, counter{}},
	{"a window flushed by another press, with no action for its taps", func(d *gestureDriver) {
		d.press(control.Misc1)
		d.wait(10)
		d.release(control.Misc1)
		d.wait(10)
		d.press(control.South)
		d.release(control.South)
		d.settle()
	}, counter{taps: 1}},
	{"balanced nested interrupt, replayed", func(d *gestureDriver) {
		d.press(control.West)
		d.wait(20)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.wait(10)
		d.release(control.West)
		d.settle()
	}, counter{downs: 1, ups: 1, taps: 1}},
	{"balanced rolling interrupt commits the tap", func(d *gestureDriver) {
		d.press(control.West)
		d.wait(20)
		d.press(control.South)
		d.wait(10)
		d.release(control.West)
		d.wait(10)
		d.release(control.South)
		d.settle()
	}, counter{taps: 2}},
	{"holdPreferred", func(d *gestureDriver) {
		d.press(control.North)
		d.wait(20)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.wait(10)
		d.release(control.North)
		d.settle()
	}, counter{downs: 1, ups: 1, taps: 1}},
	{"timeOnly", func(d *gestureDriver) {
		d.press(control.DpadUp)
		d.wait(20)
		d.press(control.South)
		d.release(control.South)
		d.wait(10)
		d.release(control.DpadUp)
		d.settle()
	}, counter{taps: 2}},
	{"latch", func(d *gestureDriver) {
		d.press(control.RightShoulder)
		d.wait(10)
		d.release(control.RightShoulder)
		d.wait(10)
		d.press(control.RightShoulder)
		d.wait(10)
		d.release(control.RightShoulder)
		d.settle()
	}, counter{downs: 1, ups: 1}},
	{"layer hold and toggle", func(d *gestureDriver) {
		d.press(control.DpadDown)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.release(control.DpadDown)
		d.press(control.DpadLeft)
		d.release(control.DpadLeft)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.press(control.DpadLeft)
		d.release(control.DpadLeft)
		d.settle()
	}, counter{downs: 2, ups: 2}},
	{"typematic", func(d *gestureDriver) {
		d.press(control.East)
		d.wait(100)
		d.release(control.East)
		d.settle()
	}, counter{downs: 1, ups: 1, repeats: 2}},
	{"sequence", func(d *gestureDriver) {
		d.press(control.LeftShoulder)
		d.wait(10)
		d.release(control.LeftShoulder)
		d.settle()
	}, counter{taps: 2}},
	{"sequence action", func(d *gestureDriver) {
		d.press(control.LeftStick)
		d.wait(10)
		d.release(control.LeftStick)
		d.settle()
	}, counter{sequences: 3}},
	{"text action", func(d *gestureDriver) {
		d.press(control.RightStick)
		d.wait(10)
		d.release(control.RightStick)
		d.settle()
	}, counter{texts: 1}},
	{"sequence action on hold", func(d *gestureDriver) {
		d.press(control.RightStick)
		d.wait(300)
		d.release(control.RightStick)
		d.settle()
	}, counter{sequences: 1}},
	{"tap repeat", func(d *gestureDriver) {
		d.press(control.DpadRight)
		d.wait(100)
		d.release(control.DpadRight)
		d.settle()
	}, counter{taps: 1, repeats: 2}},
	{"function key set", func(d *gestureDriver) {
		d.press(control.Start)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.release(control.Start)
		d.settle()
	}, counter{downs: 1, ups: 1}},
	{"function key let go before the release", func(d *gestureDriver) {
		d.press(control.Start)
		d.press(control.South)
		d.release(control.Start)
		d.wait(10)
		d.release(control.South)
		d.settle()
	}, counter{downs: 1, ups: 1}},
	{"function key under a pending gesture", func(d *gestureDriver) {
		d.press(control.West)
		d.wait(10)
		d.press(control.Start)
		d.press(control.South)
		d.release(control.South)
		d.release(control.Start)
		d.wait(10)
		d.release(control.West)
		d.settle()
	}, counter{downs: 2, ups: 2}},
	{"an ordinary center key under the function key", func(d *gestureDriver) {
		d.press(control.Start)
		d.press(control.Back)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.release(control.Back)
		d.release(control.Start)
		d.press(control.South)
		d.wait(10)
		d.release(control.South)
		d.settle()
	}, counter{downs: 1, ups: 1, taps: 1}},
}

// TestNoAllocsGestures checks the allocation contract on the M2 paths
// (ENG-13): without a trace or a registry, Step and Apply do not allocate
// for taps, holds, multi-taps committed by their window or at their count,
// interrupts replayed from the buffer, holdPreferred, timeOnly, latches,
// layers, typematic repeat and sequences. The counts check that each
// scenario takes its path: south taps in the base layer, but holds F20 in
// fn.
func TestNoAllocsGestures(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	out := &counter{}
	d := &gestureDriver{e: engine.New(compileProfile(t, gestureAllocProfile), out, engine.Options{})}
	for _, sc := range gestureScenarios {
		*out = counter{}
		sc.run(d)
		if *out != sc.want {
			t.Errorf("%s: output %+v, want %+v", sc.name, *out, sc.want)
		}
		if dl, ok := d.e.NextDeadline(); ok {
			t.Fatalf("%s: deadline %v left", sc.name, dl)
		}
		if n := testing.AllocsPerRun(100, func() { sc.run(d) }); n != 0 {
			t.Errorf("%s: %v allocations", sc.name, n)
		}
	}
}

// TestNoAllocsUnmatchedMultiTap checks that the decision of a multi-tap window
// that ends with no action for its taps (ADR-0071) allocates nothing with a
// trace, whether the window runs out or another press ends it. Each run of
// the single tap of misc1, which has only a double tap, makes that one
// decision; the press that starts the wait is another.
func TestNoAllocsUnmatchedMultiTap(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	var unmatched, presses int
	e := engine.New(compileProfile(t, gestureAllocProfile), &counter{}, engine.Options{Trace: func(d engine.Decision) {
		switch {
		case d.Control == control.Misc1 && d.Gesture == engine.GestureTap && d.Reason == engine.ReasonUnbound:
			unmatched++
		case d.Control == control.Misc1 && d.Gesture == engine.GesturePress && d.Reason == engine.ReasonUnbound:
			presses++
		}
	}})
	d := &gestureDriver{e: e}
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"the window runs out", func() {
			d.press(control.Misc1)
			d.wait(10)
			d.release(control.Misc1)
			d.settle()
		}},
		{"another press ends the window", func() {
			d.press(control.Misc1)
			d.wait(10)
			d.release(control.Misc1)
			d.wait(10)
			d.press(control.South)
			d.release(control.South)
			d.settle()
		}},
	} {
		unmatched, presses = 0, 0
		tc.run()
		if unmatched != 1 || presses != 1 {
			t.Fatalf("%s: %d decisions for the unmatched taps and %d for the press; want one each", tc.name, unmatched, presses)
		}
		if n := testing.AllocsPerRun(100, tc.run); n != 0 {
			t.Errorf("%s: %v allocations", tc.name, n)
		}
	}
}

// TestNoAllocsWaitingPressAnswers checks that the decisions that answer a press
// which only waits allocate nothing with a trace: the cancel of a multi-tap
// window that CancelAll drops, and the script's decision for a release its
// button handler consumed. Each run makes one.
func TestNoAllocsWaitingPressAnswers(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	var canceled, scripted int
	trace := func(d engine.Decision) {
		switch {
		case d.Control == control.Misc1 && d.Reason == engine.ReasonCancel:
			canceled++
		case d.Control == control.Misc1 && d.Gesture == engine.GestureRelease && d.Reason == engine.ReasonScript:
			scripted++
		}
	}
	prog := compileProfile(t, gestureAllocProfile)
	cancel := &gestureDriver{e: engine.New(prog, &counter{}, engine.Options{Trace: trace})}
	consume := &gestureDriver{e: engine.New(prog, &counter{}, engine.Options{Trace: trace, Button: func(ev engine.ButtonEvent) bool { return !ev.Down }})}
	for _, tc := range []struct {
		name string
		run  func()
		n    *int
	}{
		{"CancelAll drops the window", func() {
			cancel.press(control.Misc1)
			cancel.wait(10)
			cancel.release(control.Misc1)
			cancel.wait(10)
			cancel.e.CancelAll(cancel.now)
			cancel.settle()
		}, &canceled},
		{"the script consumes the release", func() {
			consume.press(control.Misc1)
			consume.wait(10)
			consume.release(control.Misc1)
			consume.settle()
		}, &scripted},
	} {
		*tc.n = 0
		tc.run()
		if *tc.n != 1 {
			t.Fatalf("%s: %d decisions; want one", tc.name, *tc.n)
		}
		if n := testing.AllocsPerRun(100, tc.run); n != 0 {
			t.Errorf("%s: %v allocations", tc.name, n)
		}
	}
}

// BenchmarkGestures is the ENG-13 report used with -benchmem (task bench):
// one op runs every scenario of TestNoAllocsGestures once, in virtual
// milliseconds, without a trace or a registry.
func BenchmarkGestures(b *testing.B) {
	d := &gestureDriver{e: engine.New(compileProfile(b, gestureAllocProfile), &counter{}, engine.Options{})}
	for _, sc := range gestureScenarios {
		sc.run(d)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, sc := range gestureScenarios {
			sc.run(d)
		}
	}
}

// TestNoAllocsHaptic checks the allocation contract with Options.Haptic set
// (MAP-25, HUD-10): the haptic of a binding on each gesture path, and the
// tick of a knob, call the function without a single allocation in Step and
// Apply. The counts check that each scenario takes its path.
func TestNoAllocsHaptic(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	prog := compileHaptic(t, `{"strength":30000}`, `{"rs":{"mode":"knob","knobTick":true}}`,
		`"south":{"haptic":"tick","press":{"type":"keys","keys":"Ctrl+Z","mode":"tap"}}`,
		`"west":{"haptic":"confirm","tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`,
		`"east":{"haptic":"error","press":{"type":"keys","keys":"A","repeat":{"delayMs":30,"rateHz":50}}}`,
		`"dpad.down":{"haptic":"pulse2","press":{"type":"layer","layer":"fn","op":"toggle"}}`,
		`"rs.cw":{"press":{"type":"keys","keys":"]","mode":"tap"}}`,
		`"rs.ccw":{"press":{"type":"keys","keys":"[","mode":"tap"}}`,
	)
	rightStick := func(d *gestureDriver, x, y int16) {
		d.e.Step(engine.Input{T: d.now, Kind: engine.InputAxis, Axis: control.RightX, Value: x})
		d.e.Step(engine.Input{T: d.now, Kind: engine.InputAxis, Axis: control.RightY, Value: y})
	}
	for _, sc := range []struct {
		name  string
		run   func(d *gestureDriver)
		calls int // Options.Haptic calls per run
		keys  counter
	}{
		{"press", func(d *gestureDriver) {
			d.press(control.South)
			d.wait(10)
			d.release(control.South)
			d.settle()
		}, 1, counter{taps: 1}},
		{"tap", func(d *gestureDriver) {
			d.press(control.West)
			d.wait(10)
			d.release(control.West)
			d.settle()
		}, 1, counter{taps: 1}},
		{"double tap", func(d *gestureDriver) {
			d.press(control.West)
			d.wait(10)
			d.release(control.West)
			d.wait(50)
			d.press(control.West)
			d.wait(10)
			d.release(control.West)
			d.settle()
		}, 1, counter{taps: 1}},
		{"hold", func(d *gestureDriver) {
			d.press(control.West)
			d.wait(260)
			d.release(control.West)
			d.settle()
		}, 1, counter{downs: 1, ups: 1}},
		{"typematic repeat plays once", func(d *gestureDriver) {
			d.press(control.East)
			d.wait(100)
			d.release(control.East)
			d.settle()
		}, 1, counter{downs: 1, ups: 1, repeats: 2}},
		{"layer action", func(d *gestureDriver) {
			d.press(control.DpadDown)
			d.release(control.DpadDown)
			d.press(control.South)
			d.wait(10)
			d.release(control.South)
			d.press(control.DpadDown)
			d.release(control.DpadDown)
			d.settle()
		}, 2, counter{downs: 1, ups: 1}},
		{"knob step", func(d *gestureDriver) {
			rightStick(d, 0, -20000) // engaged at the top
			d.wait(20)
			rightStick(d, 12856, -15321) // 40 degrees clockwise: one step
			d.wait(60)
			rightStick(d, 0, 0)
			d.settle()
		}, 1, counter{taps: 1}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			var calls int
			out := &counter{}
			d := &gestureDriver{e: engine.New(prog, out, engine.Options{Haptic: func(engine.Haptic) { calls++ }})}
			sc.run(d)
			if calls != sc.calls || *out != sc.keys {
				t.Fatalf("a run played %d haptics and sent %+v; want %d and %+v", calls, *out, sc.calls, sc.keys)
			}
			if dl, ok := d.e.NextDeadline(); ok {
				t.Fatalf("deadline %v left", dl)
			}
			if n := testing.AllocsPerRun(100, func() { sc.run(d) }); n != 0 {
				t.Errorf("%v allocations", n)
			}
		})
	}
}
