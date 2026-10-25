package engine_test

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the stick rings (ANA-10), the virtual knob (ANA-11) and the second
// trigger stage (ANA-14).

// holdKey is a binding of a press that holds the key.
func holdKey(name, key string) string {
	return fmt.Sprintf(`%q:{"press":{"type":"keys","keys":%q,"mode":"hold"}}`, name, key)
}

// stickAt puts a stick at angle deg (0 up, clockwise) and normalized depth
// norm (0 is the deadzone, 1 the saturation radius, with the default 0.15 and
// 0.95) at ms, and returns once both axes are set. The X axis goes first.
func stickAt(e *engine.Engine, r *recorder, right bool, deg, norm float64, ms int) {
	// Whatever fell due before this input has run, as in a slot.
	advance(e, r, ms)
	mag := 0.15 + 0.8*norm
	rad := deg * math.Pi / 180
	x := int16(math.Round(mag * math.Sin(rad) * 32767))
	y := int16(-math.Round(mag * math.Cos(rad) * 32767))
	ax, ay := control.LeftX, control.LeftY
	if right {
		ax, ay = control.RightX, control.RightY
	}
	axisStep(e, r, ax, x, ms)
	axisStep(e, r, ay, y, ms)
}

// stickCenter lets go of a stick at ms.
func stickCenter(e *engine.Engine, r *recorder, right bool, ms int) {
	advance(e, r, ms)
	ax, ay := control.LeftX, control.LeftY
	if right {
		ax, ay = control.RightX, control.RightY
	}
	axisStep(e, r, ax, 0, ms)
	axisStep(e, r, ay, 0, ms)
}

// advance runs Apply at every deadline of e up to and including ms, the way
// a slot does, and leaves the recorder's clock at ms.
func advance(e *engine.Engine, r *recorder, ms int) {
	t := clock.Time(time.Duration(ms) * time.Millisecond)
	for {
		d, ok := e.NextDeadline()
		if !ok || d > t {
			break
		}
		r.now = max(d, r.now)
		e.Apply(r.now)
		if d2, ok := e.NextDeadline(); ok && d2 <= r.now {
			panic(fmt.Sprintf("deadline %v is still due after Apply at %v", d2, r.now))
		}
	}
	r.now = t
}

// balanced fails the test when r has a key down without its up: the ticks
// and the rings leave nothing held (invariant 1).
func balanced(t *testing.T, r *recorder) {
	t.Helper()
	held := map[string]int{}
	for _, s := range r.out {
		_, rest, _ := strings.Cut(s, " ")
		verb, rest, _ := strings.Cut(rest, " ")
		owner, key, _ := strings.Cut(rest, " ")
		switch verb {
		case "down":
			held[owner+" "+key]++
		case "up":
			held[owner+" "+key]--
		}
	}
	for k, n := range held {
		if n != 0 {
			t.Errorf("%s is held %d times at the end of %s", k, n, r)
		}
	}
}

// downs returns the times of the commands whose text starts with prefix.
func downs(r *recorder, prefix string) []int {
	var out []int
	for _, s := range r.out {
		at, rest, _ := strings.Cut(s, " ")
		if !strings.HasPrefix(rest, prefix) {
			continue
		}
		ms, err := strconv.Atoi(strings.TrimPrefix(at, "@"))
		if err != nil {
			panic("recorder line " + s)
		}
		out = append(out, ms)
	}
	return out
}

// buttons records the button edges a program recognizes, as the script
// resolver sees them.
type buttons struct{ events []string }

func (b *buttons) options() engine.Options {
	return engine.Options{Button: func(ev engine.ButtonEvent) bool {
		// The half axes are recognized too; these tests are about the rest.
		if ev.Control >= control.LeftXPositive && ev.Control <= control.RightTriggerNegative {
			return false
		}
		edge := "up"
		if ev.Down {
			edge = "down"
		}
		b.events = append(b.events, fmt.Sprintf("@%d %s %s", time.Duration(ev.Time)/time.Millisecond, ev.Control, edge))
		return false
	}}
}

func (b *buttons) String() string { return strings.Join(b.events, ", ") }

func TestRingTransitions(t *testing.T) {
	bind := strings.Join([]string{holdKey("ls.active", "F1"), holdKey("ls.inner", "F2"), holdKey("ls.outer", "F3"), holdKey("ls.up", "F4")}, ",")
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, bind), r, engine.Options{})
	check := func(ms int, want string) {
		t.Helper()
		if got := r.String(); got != want {
			t.Fatalf("after %d ms: got %s\nwant %s", ms, got, want)
		}
	}
	stickAt(e, r, false, 0, 0.5, 0)
	check(0, "@0 down decl/1 F1, @0 down decl/2 F2, @0 down decl/3 F4")
	// Into the outer ring: inner leaves before outer comes.
	stickAt(e, r, false, 0, 0.95, 10)
	check(10, "@0 down decl/1 F1, @0 down decl/2 F2, @0 down decl/3 F4, @10 up decl/2 F2, @10 down decl/4 F3")
	// 0.87 is under the threshold but inside the hysteresis: still outer.
	stickAt(e, r, false, 0, 0.87, 20)
	if got := len(r.out); got != 5 {
		t.Fatalf("hysteresis: %d commands after 20 ms, want 5: %s", got, r)
	}
	// Below threshold - 0.05: back to the inner ring, outer leaves first.
	stickAt(e, r, false, 0, 0.84, 30)
	if got, want := strings.Join(r.out[5:], ", "), "@30 up decl/4 F3, @30 down decl/5 F2"; got != want {
		t.Fatalf("outer to inner: got %s; want %s", got, want)
	}
	// 0.89 does not enter the outer ring again: that takes the threshold.
	stickAt(e, r, false, 0, 0.89, 40)
	if got := len(r.out); got != 7 {
		t.Fatalf("0.89: %d commands, want 7: %s", got, r)
	}
	stickAt(e, r, false, 0, 0.92, 50)
	if got, want := strings.Join(r.out[7:], ", "), "@50 up decl/5 F2, @50 down decl/6 F3"; got != want {
		t.Fatalf("inner to outer: got %s; want %s", got, want)
	}
	// Letting go: active first, then the ring, then the direction.
	stickCenter(e, r, false, 60)
	if got, want := strings.Join(r.out[9:], ", "), "@60 up decl/1 F1, @60 up decl/6 F3, @60 up decl/3 F4"; got != want {
		t.Fatalf("release: got %s; want %s", got, want)
	}
	balanced(t, r)
}

func TestRingFlickGoesStraightToOuter(t *testing.T) {
	var b buttons
	e := engine.New(compileAnalog(t, `{}`, ``), &recorder{}, b.options())
	r := &recorder{}
	stickAt(e, r, true, 90, 1, 5)
	stickCenter(e, r, true, 6)
	want := "@5 rs.active down, @5 rs.outer down, @5 rs.right down, @6 rs.active up, @6 rs.outer up, @6 rs.right up"
	if got := b.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestRingEdgeOrderWithCallbacks(t *testing.T) {
	var b buttons
	e := engine.New(compileAnalog(t, `{"rs":{"directionDelayMs":0}}`, ``), &recorder{}, b.options())
	r := &recorder{}
	stickAt(e, r, true, 180, 0.5, 0)
	stickAt(e, r, true, 180, 0.95, 1)
	stickAt(e, r, true, 180, 0.5, 2)
	stickCenter(e, r, true, 3)
	want := "@0 rs.active down, @0 rs.inner down, @0 rs.down down, " +
		"@1 rs.inner up, @1 rs.outer down, " +
		"@2 rs.outer up, @2 rs.inner down, " +
		"@3 rs.active up, @3 rs.inner up, @3 rs.down up"
	if got := b.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestRingsAreNotDelayedByDirectionDelay(t *testing.T) {
	bind := strings.Join([]string{holdKey("ls.inner", "F2"), holdKey("ls.up", "F4")}, ",")
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"directionDelayMs":10}}`, bind), r, engine.Options{})
	stickAt(e, r, false, 0, 0.5, 0)
	if got, want := r.String(), "@0 down decl/1 F2"; got != want {
		t.Fatalf("before the delay: got %s; want %s", got, want)
	}
	advance(e, r, 10)
	if got, want := r.String(), "@0 down decl/1 F2, @10 down decl/2 F4"; got != want {
		t.Fatalf("after the delay: got %s; want %s", got, want)
	}
}

func TestRingInnerNeverWhenOuterIsNotAboveDirection(t *testing.T) {
	bind := strings.Join([]string{holdKey("ls.inner", "F2"), holdKey("ls.outer", "F3")}, ",")
	for _, outer := range []string{"0.3", "0.2", "0"} {
		t.Run(outer, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{"ls":{"outerThreshold":`+outer+`}}`, bind), r, engine.Options{})
			stickAt(e, r, false, 45, 0.31, 0)
			stickAt(e, r, false, 45, 0.6, 1)
			stickCenter(e, r, false, 2)
			if got, want := r.String(), "@0 down decl/1 F3, @2 up decl/1 F3"; got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
}

func TestRingsUnboundAndPerStick(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"outerThreshold":0.5}}`, holdKey("rs.outer", "F3")), r, engine.Options{})
	// The left stick has no bindings; its rings do nothing. The right stick's
	// outer ring starts at 0.5 and its inner ring has no binding.
	stickAt(e, r, false, 0, 1, 0)
	stickAt(e, r, true, 0, 0.4, 1)
	stickAt(e, r, true, 0, 0.6, 2)
	stickCenter(e, r, true, 3)
	if got, want := r.String(), "@2 down decl/1 F3, @3 up decl/1 F3"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestRingsAfterCancelAll(t *testing.T) {
	var b buttons
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, ``), r, b.options())
	stickAt(e, r, false, 0, 0.95, 0)
	e.CancelAll(clock.Time(5 * ms))
	// The ring stays down in the engine's view until the stick falls out of
	// it; nothing presses again.
	if !e.ControlDown(control.LSOuter) {
		t.Fatal("ls.outer was released by CancelAll")
	}
	stickAt(e, r, false, 0, 0.97, 10)
	stickCenter(e, r, false, 20)
	want := "@0 ls.active down, @0 ls.outer down, @0 ls.up down, @20 ls.active up, @20 ls.outer up, @20 ls.up up"
	if got := b.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestRingsAfterReload(t *testing.T) {
	var b buttons
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, ``), r, b.options())
	stickAt(e, r, false, 0, 0.92, 0)
	// A new program with a higher outer threshold: the stick is held inner
	// now. The reload releases ls.outer if the stick is not in it any more,
	// and never presses ls.inner.
	e.Reload(clock.Time(5*ms), compileAnalog(t, `{"ls":{"outerThreshold":0.99}}`, ``))
	want := "@0 ls.active down, @0 ls.outer down, @0 ls.up down, @5 ls.outer up"
	if got := b.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	stickAt(e, r, false, 0, 0.995, 6)
	stickCenter(e, r, false, 10)
	// 0.995 enters the new outer ring, which presses ls.outer again; ls.inner
	// was never pressed.
	want += ", @6 ls.outer down, @10 ls.active up, @10 ls.outer up, @10 ls.up up"
	if got := b.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestRingsReset(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, holdKey("ls.outer", "F3")), r, engine.Options{})
	stickAt(e, r, false, 0, 0.95, 0)
	e.Reset(clock.Time(5 * ms))
	stickAt(e, r, false, 0, 0.95, 10)
	if got, want := r.String(), "@0 down decl/1 F3, @10 down decl/2 F3"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

const knobBind = `"rs.cw":{"press":{"type":"keys","keys":"]","mode":"hold"}},"rs.ccw":{"press":{"type":"keys","keys":"[","mode":"hold"}}`

func TestKnobTicks(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)    // engaged: no tick
	stickAt(e, r, true, 25, 0.5, 10)  // under a step
	stickAt(e, r, true, 35, 0.5, 100) // a step clockwise
	stickAt(e, r, true, 5, 0.5, 200)  // reference 30: 25 degrees back, no tick
	stickAt(e, r, true, -5, 0.5, 300) // 35 degrees back from 30: counter-clockwise
	// Over the top: the reference is 0, 25 degrees clockwise is none, 35 is one.
	stickAt(e, r, true, 25, 0.5, 400)
	stickAt(e, r, true, 35, 0.5, 500)
	advance(e, r, 600)
	// The angle is read KnobSettle (4 ms) after the input.
	want := "@104 down decl/1 ], @104 up decl/1 ], @304 down decl/2 [, @304 up decl/2 [, @504 down decl/3 ], @504 up decl/3 ]"
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if _, ok := e.NextDeadline(); ok {
		t.Error("NextDeadline has a deadline with nothing queued")
	}
	balanced(t, r)
}

func TestKnobWrapsAroundNorth(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 350, 0.5, 0)
	stickAt(e, r, true, 5, 0.5, 100) // 15 degrees clockwise
	advance(e, r, 150)
	if r.String() != "" {
		t.Fatalf("a tick under a step: %s", r)
	}
	stickAt(e, r, true, 25, 0.5, 200) // 35 clockwise from 350
	stickAt(e, r, true, 340, 0.5, 300)
	advance(e, r, 400)
	// 340 from the reference at 20: 40 counter-clockwise, one tick (reference 350).
	want := "@204 down decl/1 ], @204 up decl/1 ], @304 down decl/2 [, @304 up decl/2 ["
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestKnobStepSize(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob","knobStepDeg":90}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 80, 0.5, 100)
	stickAt(e, r, true, 100, 0.5, 200)
	advance(e, r, 300)
	if got, want := r.String(), "@204 down decl/1 ], @204 up decl/1 ]"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestKnobSeveralStepsInOneInput(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	advance(e, r, 9)
	// 100 degrees in one go: the X axis moves first, and 100 is three steps.
	axisStep(e, r, control.RightX, int16(math.Round(0.55*math.Sin(100*math.Pi/180)*32767)), 10)
	axisStep(e, r, control.RightY, int16(-math.Round(0.55*math.Cos(100*math.Pi/180)*32767)), 10)
	if r.String() != "" {
		t.Fatalf("the knob read the angle before KnobSettle: %s", r)
	}
	if d, ok := e.NextDeadline(); !ok || d != clock.Time(10*ms+engine.KnobSettle) {
		t.Fatalf("NextDeadline = %v, %t; want 14ms", d, ok)
	}
	// One tick at once, the other two wait for the minimum period, 40 ms.
	advance(e, r, 14)
	if got, want := r.String(), "@14 down decl/1 ], @14 up decl/1 ]"; got != want {
		t.Fatalf("at 14 ms: got %s\nwant %s", got, want)
	}
	if d, ok := e.NextDeadline(); !ok || d != clock.Time(54*ms) {
		t.Fatalf("NextDeadline = %v, %t; want 54ms", d, ok)
	}
	advance(e, r, 200)
	want := "@14 down decl/1 ], @14 up decl/1 ], @54 down decl/2 ], @54 up decl/2 ], @94 down decl/3 ], @94 up decl/3 ]"
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if _, ok := e.NextDeadline(); ok {
		t.Error("a deadline is left")
	}
}

func TestKnobReversalCancelsQueuedTicks(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 35, 0.5, 10) // read at 14: a tick at once; the next waits until 54
	stickAt(e, r, true, 65, 0.5, 20) // queued
	stickAt(e, r, true, 95, 0.5, 30) // queued
	stickAt(e, r, true, 55, 0.5, 40) // a step back from the reference, 90: cancels one
	stickAt(e, r, true, 25, 0.5, 50) // cancels the other
	stickAt(e, r, true, -5, 0.5, 60) // nothing queued to cancel: a tick counter-clockwise
	advance(e, r, 300)
	// ] at 14, and the counter-clockwise tick of the last move at 64.
	want := "@14 down decl/1 ], @14 up decl/1 ], @64 down decl/2 [, @64 up decl/2 ["
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	balanced(t, r)
}

// TestKnobRateLimit is the automatic acceptance of ANA-11: a knob spun three
// revolutions in 100 ms never emits ticks closer than the key can follow, the
// tap press plus the tap gap, 40 ms with the defaults.
func TestKnobRateLimit(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	for ms := 1; ms <= 100; ms++ {
		advance(e, r, ms)
		stickAt(e, r, true, 10.8*float64(ms), 0.5, ms)
	}
	advance(e, r, 2000)
	times := downs(r, "down decl/")
	t.Logf("ticks at %v ms", times)
	if len(times) < 10 {
		t.Fatalf("only %d ticks: %s", len(times), r)
	}
	for i := 1; i < len(times); i++ {
		if gap := times[i] - times[i-1]; gap < 40 {
			t.Errorf("ticks %d and %d are %d ms apart; want at least 40", i-1, i, gap)
		}
	}
	// The first tick comes with the first reading after the knob is turned,
	// then at most a revolution (12 steps of 30 degrees) is queued: three
	// ticks go out while the knob spins, at 8, 48 and 88 ms, and the 12
	// queued ones follow them.
	if want := 15; len(times) != want {
		t.Errorf("%d ticks, want %d: %v", len(times), want, times)
	}
	if _, ok := e.NextDeadline(); ok {
		t.Error("a deadline is left")
	}
	balanced(t, r)
}

func TestKnobQueueIsOneRevolution(t *testing.T) {
	for _, tc := range []struct {
		step, want int
	}{{30, 12}, {45, 8}, {100, 4}, {180, 2}, {10, 36}} {
		t.Run(fmt.Sprint(tc.step), func(t *testing.T) {
			// A tap of 2 s and a gap of 2 s: nothing drains while the knob spins.
			src := fmt.Sprintf(`{"schemaVersion":0,"name":"t","timing":{"tapPressMs":2000,"tapGapMs":2000},"analog":{"rs":{"mode":"knob","knobStepDeg":%d}},"layers":{"base":{%s}}}`, tc.step, knobBind)
			p, ds := profile.Parse([]byte(src))
			if p == nil || ds.HasErrors() {
				t.Fatal(ds)
			}
			prog, err := engine.Compile(p)
			if err != nil {
				t.Fatal(err)
			}
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			stickAt(e, r, true, 0, 0.5, 0)
			// Five revolutions, 5 degrees every 5 ms.
			for i := 1; i <= 5*360/5; i++ {
				stickAt(e, r, true, float64(5*i), 0.5, 5*i)
			}
			advance(e, r, 1000000)
			// One tick at the first step, the queue of one revolution after it.
			if got := len(downs(r, "down decl/")); got != 1+tc.want {
				t.Errorf("%d ticks, want %d", got, 1+tc.want)
			}
		})
	}
}

func TestKnobTimingFollowsTheBinding(t *testing.T) {
	for _, tc := range []struct {
		name, analog, binding string
		period                int
	}{
		{"profile timing", `{"rs":{"mode":"knob"}}`, knobBind, 40},
		{"binding timing", `{"rs":{"mode":"knob"}}`, `"rs.cw":{"timing":{"tapPressMs":50,"tapGapMs":60},"press":{"type":"keys","keys":"]","mode":"hold"}}`, 110},
		{"binding with one value", `{"rs":{"mode":"knob"}}`, `"rs.cw":{"timing":{"tapGapMs":70},"press":{"type":"keys","keys":"]","mode":"hold"}}`, 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, tc.analog, tc.binding), r, engine.Options{})
			stickAt(e, r, true, 0, 0.5, 0)
			stickAt(e, r, true, 100, 0.5, 5)
			advance(e, r, 1000)
			times := downs(r, "down decl/")
			if len(times) != 3 || times[1]-times[0] != tc.period || times[2]-times[1] != tc.period {
				t.Errorf("ticks at %v; want three, %d ms apart", times, tc.period)
			}
		})
	}
	// A profile that makes the tap longer slows every tick.
	src := `{"schemaVersion":0,"name":"t","timing":{"tapPressMs":30,"tapGapMs":45},"analog":{"rs":{"mode":"knob"}},"layers":{"base":{` + knobBind + `}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 70, 0.5, 5)
	advance(e, r, 1000)
	if times := downs(r, "down decl/"); !slices.Equal(times, []int{9, 84}) {
		t.Errorf("ticks at %v, want 9 and 84", times)
	}
}

func TestKnobSpringBackMakesNoTicks(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind+","+holdKey("rs.active", "F1")), r, engine.Options{})
	stickAt(e, r, true, 0, 0.8, 0)
	stickAt(e, r, true, 100, 0.8, 100) // three ticks, the first at once
	advance(e, r, 300)
	before := len(r.out)
	// Letting go: the stick goes back to the center in a few readings at a
	// different angle each, never leaving the active zone's reference.
	stickAt(e, r, true, 100, 0.6, 400)
	stickAt(e, r, true, 100, 0.35, 410)
	stickCenter(e, r, true, 420)
	advance(e, r, 1000)
	released := r.out[before:]
	for _, s := range released {
		if strings.Contains(s, "]") || strings.Contains(s, "[") {
			t.Errorf("the spring back ticked: %v", released)
		}
	}
	// A new push takes its own reference: no tick for the angle it comes at.
	n := len(r.out)
	stickAt(e, r, true, 250, 0.8, 1100)
	stickAt(e, r, true, 262, 0.8, 1110)
	for _, s := range r.out[n:] {
		if strings.Contains(s, "]") || strings.Contains(s, "[") {
			t.Errorf("engaging ticked: %v", r.out[n:])
		}
	}
	stickCenter(e, r, true, 1120)
	balanced(t, r)
}

// TestKnobFastReleaseFromDiagonal: SDL sends the two axes of one report as two
// events. A stick let go from a diagonal in one report shows an intermediate
// reading in between, one axis at rest and the other not, whose angle is 45
// degrees away; the knob must not tick for it. It reads the angle KnobSettle
// after the first input, when both axes have arrived, and finds the stick at
// rest and disengaged.
func TestKnobFastReleaseFromDiagonal(t *testing.T) {
	const diagonal = 22937 // 0.7 of the travel, on both axes: 45 degrees, fully pushed
	for _, tc := range []struct {
		name   string
		xFirst bool
		gap    time.Duration
	}{
		{"x then y at the same time", true, 0},
		{"y then x at the same time", false, 0},
		{"x then y 100 us apart", true, 100 * time.Microsecond},
		{"y then x 100 us apart", false, 100 * time.Microsecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
			step := func(at clock.Time, axis control.Axis, v int16) {
				r.now = at
				e.Step(engine.Input{T: at, Kind: engine.InputAxis, Axis: axis, Value: v})
			}
			step(0, control.RightX, diagonal)
			step(0, control.RightY, -diagonal)
			advance(e, r, 10) // engaged at 45 degrees
			first, second := control.RightX, control.RightY
			if !tc.xFirst {
				first, second = second, first
			}
			at := clock.Time(100 * ms)
			step(at, first, 0)
			step(at.Add(tc.gap), second, 0)
			if r.String() != "" {
				t.Fatalf("the knob read the angle of an intermediate reading: %s", r)
			}
			if d, ok := e.NextDeadline(); !ok || d != at.Add(engine.KnobSettle) {
				t.Fatalf("NextDeadline = %v, %t; want the reading %v after the first input", d, ok, engine.KnobSettle)
			}
			advance(e, r, 120)
			if r.String() != "" {
				t.Fatalf("the release from a diagonal ticked: %s", r)
			}
			if d, ok := e.NextDeadline(); ok {
				t.Fatalf("NextDeadline = %v after the release", d)
			}
			// Disengaged: a push at another angle takes that angle as the
			// reference, where a knob still engaged at 45 degrees would tick.
			stickAt(e, r, true, 200, 0.5, 200)
			advance(e, r, 300)
			if r.String() != "" {
				t.Fatalf("the knob was still engaged after the release: %s", r)
			}
		})
	}
}

// TestKnobReadingWaitsOnce: the reading is taken KnobSettle after the first
// input that nothing is waiting on, and later inputs do not move it, so a
// continuous turn is read every KnobSettle; a stick at rest that is not
// engaged schedules nothing.
func TestKnobReadingWaitsOnce(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	axisStep(e, r, control.RightX, 100, 0) // in the dead zone, not engaged
	axisStep(e, r, control.RightY, 100, 0)
	if d, ok := e.NextDeadline(); ok {
		t.Fatalf("a stick at rest scheduled a reading at %v", d)
	}
	stickAt(e, r, true, 0, 0.5, 1)
	for ms := 2; ms <= 3; ms++ {
		stickAt(e, r, true, float64(ms), 0.5, ms)
	}
	if d, ok := e.NextDeadline(); !ok || d != clock.Time(1*ms+engine.KnobSettle) {
		t.Fatalf("NextDeadline = %v, %t; want 5ms: %v after the first input, not pushed out", d, ok, engine.KnobSettle)
	}
	advance(e, r, 5)
	// Engaged now. The next input starts the next wait.
	stickAt(e, r, true, 10, 0.5, 6)
	if d, ok := e.NextDeadline(); !ok || d != clock.Time(6*ms+engine.KnobSettle) {
		t.Fatalf("NextDeadline = %v, %t; want 10ms", d, ok)
	}
	// A preview, which never sends, does not schedule it.
	p := e.PreviewInput(nil)
	p.Step(engine.Input{T: clock.Time(20 * ms), Kind: engine.InputAxis, Axis: control.RightX, Value: 5000})
	if d, ok := e.NextDeadline(); !ok || d != clock.Time(10*ms) {
		t.Fatalf("NextDeadline = %v, %t after a preview; want 10ms", d, ok)
	}
}

func TestKnobHasNoDirections(t *testing.T) {
	bind := strings.Join([]string{holdKey("rs.active", "F1"), holdKey("rs.up", "F2"), holdKey("rs.right", "F3"), holdKey("rs.upright", "F4"), holdKey("rs.inner", "F5")}, ",")
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, bind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.6, 0)
	stickAt(e, r, true, 45, 0.6, 1)
	stickAt(e, r, true, 90, 0.6, 2)
	stickCenter(e, r, true, 3)
	if got, want := r.String(), "@0 down decl/1 F1, @0 down decl/2 F5, @3 up decl/1 F1, @3 up decl/2 F5"; got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestKnobOnlyInKnobMode(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	for deg := 10; deg <= 360; deg += 10 {
		stickAt(e, r, true, float64(deg), 0.5, deg)
	}
	advance(e, r, 1000)
	if r.String() != "" {
		t.Fatalf("a standard stick ticked: %s", r)
	}
}

// TestKnobZeroPeriod: with no tap press and no gap there is no rate limit to
// wait for, but the next tick still comes after this one, never at the same
// time, so that NextDeadline is never already due after an Apply.
func TestKnobZeroPeriod(t *testing.T) {
	src := `{"schemaVersion":0,"name":"t","timing":{"tapPressMs":0,"tapGapMs":0},"analog":{"rs":{"mode":"knob"}},"layers":{"base":{` + knobBind + `}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 100, 0.5, 5)
	advance(e, r, 20) // panics when a deadline is still due after an Apply
	if got := len(downs(r, "down decl/")); got != 3 {
		t.Fatalf("%d ticks, want 3: %s", got, r)
	}
}

func TestKnobUnboundTicksAreDropped(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, holdKey("rs.ccw", "[")), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 100, 0.5, 5) // clockwise: rs.cw has no binding
	advance(e, r, 20)
	if r.String() != "" {
		t.Fatalf("an unbound tick sent %s", r)
	}
	if d, ok := e.NextDeadline(); ok {
		t.Fatalf("NextDeadline = %v for dropped ticks", d)
	}
	// Counter-clockwise ticks are bound and still come.
	stickAt(e, r, true, 50, 0.5, 30)
	advance(e, r, 40)
	if got, want := r.String(), "@34 down decl/1 [, @34 up decl/1 ["; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestKnobTickDecisions(t *testing.T) {
	var got []string
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, `"rs.cw":{"label":"brush+","press":{"type":"keys","keys":"]","mode":"tap"}}`), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, brief(d)) }})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 40, 0.5, 7)
	advance(e, r, 20)
	want := []string{"@11 rs.cw down dispatch ] brush+", "@11 rs.cw up end brush+"}
	if !slices.Equal(got, want) {
		t.Fatalf("decisions %v\nwant %v", got, want)
	}
	if want := "@11 tap decl/1 ] 20ms"; r.String() != want {
		t.Fatalf("got %s; want %s", r, want)
	}
}

func TestKnobTicksReachScripts(t *testing.T) {
	var b buttons
	r := &recorder{}
	// Nothing binds the ticks; a script resolver is enough to recognize them.
	e := engine.New(compileAnalog(t, `{"ls":{"mode":"knob"}}`, ``), r, b.options())
	stickAt(e, r, false, 0, 0.5, 0)
	stickAt(e, r, false, 40, 0.5, 5)
	stickAt(e, r, false, -5, 0.5, 100)
	advance(e, r, 200)
	want := "@0 ls.active down, @0 ls.inner down, @9 ls.cw down, @9 ls.cw up, @104 ls.ccw down, @104 ls.ccw up"
	if got := b.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// queueTicks leaves two ticks queued behind the rate limit: one went out at 14
// ms, and the next is due at 54 ms.
func queueTicks(e *engine.Engine, r *recorder) {
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 35, 0.5, 10)
	stickAt(e, r, true, 65, 0.5, 20)
	stickAt(e, r, true, 95, 0.5, 30)
	advance(e, r, 40)
}

func TestKnobQueueIsClearedBy(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(e *engine.Engine, prog *engine.Program)
	}{
		{"CancelAll", func(e *engine.Engine, _ *engine.Program) { e.CancelAll(clock.Time(45 * ms)) }},
		{"Pause", func(e *engine.Engine, _ *engine.Program) { e.Pause(clock.Time(45 * ms)); e.Resume() }},
		{"Reload", func(e *engine.Engine, prog *engine.Program) { e.Reload(clock.Time(45*ms), prog) }},
		{"Reset", func(e *engine.Engine, _ *engine.Program) { e.Reset(clock.Time(45 * ms)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind)
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			queueTicks(e, r)
			if d, ok := e.NextDeadline(); !ok || d != clock.Time(54*ms) {
				t.Fatalf("queued ticks: NextDeadline = %v, %t; want 54ms", d, ok)
			}
			// The stick turns on, and the reading of that turn has not been
			// taken yet when the queue is cleared: it is dropped too.
			stickAt(e, r, true, 125, 0.5, 42)
			if d, ok := e.NextDeadline(); !ok || d != clock.Time(46*ms) {
				t.Fatalf("a turn waiting to be read: NextDeadline = %v, %t; want 46ms", d, ok)
			}
			r.now = clock.Time(45 * ms)
			tc.do(e, prog)
			if d, ok := e.NextDeadline(); ok {
				t.Fatalf("NextDeadline = %v after %s", d, tc.name)
			}
			advance(e, r, 500)
			if got, want := r.String(), "@14 down decl/1 ], @14 up decl/1 ]"; got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
}

func TestKnobAfterCancelAllTicksAtOnce(t *testing.T) {
	prog := compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	queueTicks(e, r)
	r.now = clock.Time(45 * ms)
	e.CancelAll(r.now)
	// The stick is still held and still turns: the reference stayed, and the
	// rate limit starts over.
	stickAt(e, r, true, 130, 0.5, 50)
	advance(e, r, 60)
	if got, want := r.String(), "@14 down decl/1 ], @14 up decl/1 ], @54 down decl/2 ], @54 up decl/2 ]"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestKnobReloadTakesTheCurrentAngle(t *testing.T) {
	prog := compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	stickAt(e, r, true, 20, 0.5, 5) // under a step: the reference is still 0
	e.Reload(clock.Time(10*ms), compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind))
	// 45 is 25 from where the knob was at the reload, and 45 from 0.
	stickAt(e, r, true, 45, 0.5, 20)
	advance(e, r, 28)
	if r.String() != "" {
		t.Fatalf("a reload kept the old reference: %s", r)
	}
	stickAt(e, r, true, 60, 0.5, 30)
	advance(e, r, 40)
	if got, want := r.String(), "@34 down decl/1 ], @34 up decl/1 ]"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestKnobDoesNotTickWhilePaused(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob"}}`, knobBind), r, engine.Options{})
	stickAt(e, r, true, 0, 0.5, 0)
	e.Pause(clock.Time(ms))
	stickAt(e, r, true, 100, 0.5, 5)
	advance(e, r, 200)
	e.Resume()
	advance(e, r, 300)
	if r.String() != "" {
		t.Fatalf("paused knob sent %s", r)
	}
	if _, ok := e.NextDeadline(); ok {
		t.Error("a paused knob queued ticks")
	}
	// After the resume the reference is where the knob was turned to.
	stickAt(e, r, true, 125, 0.5, 400)
	advance(e, r, 409)
	if r.String() != "" {
		t.Fatalf("the knob ticked for the turn made while paused: %s", r)
	}
	stickAt(e, r, true, 140, 0.5, 410)
	advance(e, r, 500)
	if got, want := r.String(), "@414 down decl/1 ], @414 up decl/1 ]"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

// TestKnobTicksAlwaysRelease is a seeded random walk of both sticks with the
// calls that must let everything go: every tick and ring key that goes down
// comes up again (invariant 1).
func TestKnobTicksAlwaysRelease(t *testing.T) {
	bind := strings.Join([]string{knobBind, holdKey("ls.cw", "F5"), holdKey("ls.ccw", "F6"), holdKey("ls.inner", "F7"), holdKey("ls.outer", "F8"), holdKey("rs.outer", "F9"), holdKey("rs.active", "F10")}, ",")
	prog := compileAnalog(t, `{"ls":{"mode":"knob","knobStepDeg":20},"rs":{"mode":"knob"}}`, bind)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	seed := uint32(12345)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	angle := [2]float64{}
	ms := 0
	for i := 0; i < 3000; i++ {
		ms += next(30)
		advance(e, r, ms)
		side := next(2)
		switch next(10) {
		case 0:
			stickCenter(e, r, side == 1, ms)
		case 1:
			e.CancelAll(clock.Time(time.Duration(ms) * time.Millisecond))
		default:
			angle[side] += float64(next(80) - 35)
			stickAt(e, r, side == 1, angle[side], float64(next(100))/100, ms)
		}
	}
	e.CancelAll(clock.Time(time.Duration(ms) * time.Millisecond))
	advance(e, r, ms+5000)
	if len(r.out) < 100 {
		t.Fatalf("the walk sent only %d commands", len(r.out))
	}
	// CancelAll sends no Up for the keys of a cycle the Arbiter releases, so
	// count the controls the engine still holds, not the recorder's keys:
	// after a CancelAll every ring and tick control is released by the stick.
	stickCenter(e, r, false, ms+6000)
	stickCenter(e, r, true, ms+6000)
	for _, c := range []control.Control{control.LSInner, control.LSOuter, control.LSCW, control.LSCCW, control.RSInner, control.RSOuter, control.RSCW, control.RSCCW} {
		if e.ControlDown(c) {
			t.Errorf("%s is still down", c)
		}
	}
}

func TestKnobTicksReleaseWithoutCancel(t *testing.T) {
	bind := strings.Join([]string{knobBind, holdKey("rs.outer", "F9")}, ",")
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"rs":{"mode":"knob","knobStepDeg":15}}`, bind), r, engine.Options{})
	seed := uint32(99)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	angle, ms := 0.0, 0
	for i := 0; i < 2000; i++ {
		ms += 1 + next(25)
		advance(e, r, ms)
		angle += float64(next(70) - 30)
		stickAt(e, r, true, angle, float64(next(100))/100, ms)
	}
	stickCenter(e, r, true, ms+1)
	advance(e, r, ms+5000)
	if len(downs(r, "down decl/")) < 50 {
		t.Fatalf("the walk sent only %d commands", len(r.out))
	}
	balanced(t, r)
}

const stageBind = `"lt":{"press":{"type":"keys","keys":"F1","mode":"hold"}},"lt.full":{"press":{"type":"keys","keys":"F2","mode":"hold"}}`

func trig(e *engine.Engine, r *recorder, axis control.Axis, frac float64, ms int) {
	axisStep(e, r, axis, int16(math.Round(frac*32767)), ms)
}

func TestTriggerSecondStage(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, stageBind), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 0.35, 0)
	trig(e, r, control.LeftTrigger, 0.85, 10)
	trig(e, r, control.LeftTrigger, 0.92, 20)
	trig(e, r, control.LeftTrigger, 0.85, 30)
	trig(e, r, control.LeftTrigger, 0.79, 40)
	trig(e, r, control.LeftTrigger, 0.5, 50)
	trig(e, r, control.LeftTrigger, 0.15, 60)
	want := "@0 down decl/1 F1, @20 down decl/2 F2, @40 up decl/2 F2, @60 up decl/1 F1"
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestTriggerSecondStageOrderInOneStep(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, stageBind), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 1, 0)
	trig(e, r, control.LeftTrigger, 0, 10)
	want := "@0 down decl/1 F1, @0 down decl/2 F2, @10 up decl/2 F2, @10 up decl/1 F1"
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// The right trigger has its own stage, and its own thresholds.
	r = &recorder{}
	e = engine.New(compileAnalog(t, `{"rt":{"fullPress":0.6,"fullRelease":0.5}}`, `"rt":{"press":{"type":"keys","keys":"F3","mode":"hold"}},"rt.full":{"press":{"type":"keys","keys":"F4","mode":"hold"}}`), r, engine.Options{})
	trig(e, r, control.RightTrigger, 0.65, 0)
	trig(e, r, control.LeftTrigger, 1, 5) // the left trigger has no binding
	trig(e, r, control.RightTrigger, 0.55, 10)
	trig(e, r, control.RightTrigger, 0.45, 20)
	trig(e, r, control.RightTrigger, 0.1, 30)
	want = "@0 down decl/1 F3, @0 down decl/2 F4, @20 up decl/2 F4, @30 up decl/1 F3"
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestTriggerSecondStageUnbound(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, `"lt":{"press":{"type":"keys","keys":"F1","mode":"hold"}}`), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 1, 0)
	trig(e, r, control.LeftTrigger, 0.5, 10)
	trig(e, r, control.LeftTrigger, 0, 20)
	if got, want := r.String(), "@0 down decl/1 F1, @20 up decl/1 F1"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
	// Only the second stage bound: lt is still tracked, and unbound.
	r = &recorder{}
	e = engine.New(compileAnalog(t, `{}`, `"lt.full":{"press":{"type":"keys","keys":"F2","mode":"hold"}}`), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 1, 0)
	trig(e, r, control.LeftTrigger, 0, 10)
	if got, want := r.String(), "@0 down decl/2 F2, @10 up decl/2 F2"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

// TestTriggerFirstStageStaysDown: with a second-stage release below the
// first stage's, LT stays down while the second stage is.
func TestTriggerFirstStageStaysDown(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"lt":{"press":0.3,"release":0.2,"fullPress":0.9,"fullRelease":0.1}}`, stageBind), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 1, 0)
	trig(e, r, control.LeftTrigger, 0.15, 10)
	if got, want := r.String(), "@0 down decl/1 F1, @0 down decl/2 F2"; got != want {
		t.Fatalf("0.15: got %s; want %s", got, want)
	}
	trig(e, r, control.LeftTrigger, 0.05, 20)
	if got, want := r.String(), "@0 down decl/1 F1, @0 down decl/2 F2, @20 up decl/2 F2, @20 up decl/1 F1"; got != want {
		t.Fatalf("0.05: got %s; want %s", got, want)
	}
}

func TestTriggerSecondStageAfterCancelAll(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, stageBind), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 1, 0)
	r.now = clock.Time(10 * ms)
	e.CancelAll(r.now)
	if !e.ControlDown(control.LT) || !e.ControlDown(control.LTFull) {
		t.Fatal("CancelAll let go of the trigger controls in the engine's view")
	}
	trig(e, r, control.LeftTrigger, 0.85, 20) // above fullRelease: nothing
	trig(e, r, control.LeftTrigger, 0.5, 30)  // under it: the second stage ends, without output
	if e.ControlDown(control.LTFull) {
		t.Fatal("lt.full stayed down below fullRelease")
	}
	trig(e, r, control.LeftTrigger, 0.95, 40) // the second stage presses again; lt is still the canceled one
	trig(e, r, control.LeftTrigger, 0.1, 50)
	want := "@0 down decl/1 F1, @0 down decl/2 F2, @40 down decl/3 F2, @50 up decl/3 F2"
	if got := r.String(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestTriggerSecondStageReload(t *testing.T) {
	// A held second stage that the new thresholds no longer want goes up.
	var b buttons
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, ``), r, b.options())
	trig(e, r, control.LeftTrigger, 0.92, 0)
	e.Reload(clock.Time(5*ms), compileAnalog(t, `{"lt":{"fullPress":0.99,"fullRelease":0.95}}`, ``))
	want := "@0 lt down, @0 lt.full down, @5 lt.full up"
	if got := b.String(); got != want {
		t.Fatalf("held second stage: got %s\nwant %s", got, want)
	}
	// One that has become wanted is not pressed by the reload, nor while the
	// trigger stays; it takes a fall below fullRelease and a new pull.
	b = buttons{}
	e = engine.New(compileAnalog(t, `{"lt":{"fullPress":0.99,"fullRelease":0.95}}`, ``), r, b.options())
	trig(e, r, control.LeftTrigger, 0.92, 0)
	e.Reload(clock.Time(5*ms), compileAnalog(t, `{}`, ``))
	trig(e, r, control.LeftTrigger, 0.96, 10)
	trig(e, r, control.LeftTrigger, 0.5, 20)
	trig(e, r, control.LeftTrigger, 0.95, 30)
	want = "@0 lt down, @30 lt.full down"
	if got := b.String(); got != want {
		t.Fatalf("wanted second stage: got %s\nwant %s", got, want)
	}
}

func TestTriggerSecondStageReset(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, stageBind), r, engine.Options{})
	trig(e, r, control.LeftTrigger, 1, 0)
	e.Reset(clock.Time(5 * ms))
	trig(e, r, control.LeftTrigger, 1, 10)
	if got, want := r.String(), "@0 down decl/1 F1, @0 down decl/2 F2, @10 down decl/3 F1, @10 down decl/4 F2"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestTriggerSecondStageDecisions(t *testing.T) {
	var got []string
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, stageBind), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, brief(d)) }})
	trig(e, r, control.LeftTrigger, 1, 0)
	trig(e, r, control.LeftTrigger, 0, 10)
	want := []string{
		"@0 lt down dispatch F1", "@0 lt.full down dispatch F2",
		"@10 lt.full up release F2", "@10 lt up release F1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("decisions %v\nwant %v", got, want)
	}
}

func TestCompileFillsM15Defaults(t *testing.T) {
	p := &profile.Profile{Layers: []*profile.Layer{{Name: "base"}}}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	a := prog.Analog()
	if a.LS.OuterThreshold != 0.90 || a.RS.KnobStepDeg != 30 || a.LT.FullPress != 0.90 || a.LT.FullRelease != 0.80 || a.RT.FullPress != 0.90 {
		t.Errorf("defaults = %+v", a)
	}
	// A model built by hand with resolved analog settings that lack the new
	// ones still gets a knob step and a second stage.
	p = &profile.Profile{AnalogResolved: true, Analog: profile.DefaultAnalog(), Layers: []*profile.Layer{{Name: "base"}}}
	p.Analog.RS.KnobStepDeg, p.Analog.LT.FullPress, p.Analog.LT.FullRelease = 0, 0, 0
	prog, err = engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	if a := prog.Analog(); a.RS.KnobStepDeg != 30 || a.LT.FullPress != 0.90 || a.LT.FullRelease != 0.80 {
		t.Errorf("resolved defaults = %+v", a)
	}
	// Written values are kept.
	prog = compileAnalog(t, `{"ls":{"outerThreshold":0.6,"mode":"knob","knobStepDeg":45},"rt":{"fullPress":0.7,"fullRelease":0.65}}`, ``)
	if a := prog.Analog(); a.LS.OuterThreshold != 0.6 || a.LS.KnobStepDeg != 45 || a.RT.FullPress != 0.7 || a.RT.FullRelease != 0.65 {
		t.Errorf("written values = %+v", a)
	}
}

// TestNoAllocsM15 checks the allocation contract for the paths the rings, the
// knob and the second stage add: Step, Apply, NextDeadline and the calls that
// clear them never allocate.
func TestNoAllocsM15(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	bind := strings.Join([]string{
		knobBind, holdKey("ls.inner", "F5"), holdKey("ls.outer", "F6"), holdKey("ls.active", "F7"), holdKey("ls.up", "F8"),
		holdKey("rs.inner", "F9"), holdKey("rs.outer", "F10"), stageBind, `"rt.full":{"press":{"type":"keys","keys":"F11","mode":"tap"}}`,
	}, ",")
	prog := compileAnalog(t, `{"rs":{"mode":"knob","knobStepDeg":10}}`, bind)
	var decisions int
	for _, trace := range []func(engine.Decision){nil, func(engine.Decision) { decisions++ }} {
		out := &counter{}
		e := engine.New(prog, out, engine.Options{Trace: trace})
		var now clock.Time
		axis := func(a control.Axis, v int16) engine.Input {
			return engine.Input{Kind: engine.InputAxis, Axis: a, Value: v}
		}
		steps := map[string][]engine.Input{
			"rings": {axis(control.LeftY, -20000), axis(control.LeftY, -32767), axis(control.LeftY, -26000), axis(control.LeftY, -20000), axis(control.LeftY, 0)},
			"knob": {
				axis(control.RightX, 0), axis(control.RightY, -20000), axis(control.RightX, 10000), axis(control.RightY, -15000),
				axis(control.RightX, 20000), axis(control.RightY, 0), axis(control.RightX, -20000), axis(control.RightY, 15000), axis(control.RightX, 0),
			},
			"second stage": {axis(control.LeftTrigger, 32767), axis(control.LeftTrigger, 25000), axis(control.LeftTrigger, 0), axis(control.RightTrigger, 32767), axis(control.RightTrigger, 0)},
		}
		for s, ins := range steps {
			if n := testing.AllocsPerRun(100, func() {
				for _, in := range ins {
					now = now.Add(time.Millisecond)
					in.T = now
					e.Step(in)
				}
			}); n != 0 {
				t.Errorf("trace %t: Step %s: %v allocations", trace != nil, s, n)
			}
		}
		spin := func() {
			for deg := 0; deg < 360; deg += 7 {
				now = now.Add(time.Millisecond)
				rad := float64(deg) * math.Pi / 180
				e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.RightX, Value: int16(20000 * math.Sin(rad))})
				e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.RightY, Value: int16(-20000 * math.Cos(rad))})
			}
		}
		if n := testing.AllocsPerRun(20, spin); n != 0 {
			t.Errorf("trace %t: a spin of the knob: %v allocations", trace != nil, n)
		}
		if trace != nil {
			continue
		}
		for s, fn := range map[string]func(){
			"Apply with ticks queued": func() {
				spin()
				for i := 0; i < 5; i++ {
					if d, ok := e.NextDeadline(); ok {
						now = max(now, d)
						e.Apply(now)
					}
				}
			},
			"CancelAll with ticks queued": func() { spin(); e.CancelAll(now) },
			"Reset":                       func() { e.Step(axis(control.LeftTrigger, 32767)); e.Reset(now) },
			"Pause": func() {
				e.Step(axis(control.LeftTrigger, 32767))
				e.Pause(now)
				spin()
				e.Resume()
			},
		} {
			if n := testing.AllocsPerRun(20, fn); n != 0 {
				t.Errorf("%s: %v allocations", s, n)
			}
		}
		if out.downs == 0 || out.ups == 0 {
			t.Errorf("output %+v: the steps did not run", out)
		}
		// Ticks reach the Output: more downs than the rings and the stage give.
		if out.downs < 100 {
			t.Errorf("only %d downs: the knob did not tick", out.downs)
		}
	}
	if decisions == 0 {
		t.Error("the trace saw no decisions")
	}
	_ = keys.Chord{}
}
