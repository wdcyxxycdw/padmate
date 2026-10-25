package engine_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the rate-modulated repeat and the response curves (ANA-12, ANA-13,
// ANA-15). A repeat with minRateHz goes at minRateHz when its control has just
// reached its activation threshold, and at rateHz at full travel; the stick's
// or trigger's curve shapes what lies between. The trigger RT is the main
// subject: its raw value is exact (stage fractions of 32767), so the depth of a
// pull is known to 1e-4.

// rateBind is a binding of control name to a hold of A that repeats by repeat.
func rateBind(name, repeat string) string {
	return fmt.Sprintf(`%q:{"press":{"type":"keys","keys":"A","mode":"hold","repeat":%s}}`, name, repeat)
}

// repeats returns the times, in ms, of the repeats r has recorded.
func repeats(r *recorder) []int { return downs(r, "repeat") }

// within fails the test when got is not want, give or take tol ms.
func within(t *testing.T, what string, got, want []int, tol int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %v; want %v", what, got, want)
		return
	}
	for i := range got {
		if d := got[i] - want[i]; d < -tol || d > tol {
			t.Errorf("%s: %v; want %v (give or take %d ms)", what, got, want, tol)
			return
		}
	}
}

// gapsOf returns the differences between successive times.
func gapsOf(times []int) []int {
	var out []int
	for i := 1; i < len(times); i++ {
		out = append(out, times[i]-times[i-1])
	}
	return out
}

// rate4to20 is a repeat after 100 ms that goes from 4 to 20 Hz.
const rate4to20 = `{"delayMs":100,"rateHz":20,"minRateHz":4}`

func TestRepeatRateAtThresholdAndFullTravel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frac  float64
		until int
		want  []int
	}{
		// 4 Hz is 250 ms; the first repeat waits the unmodulated delay.
		{"at the press threshold", 0.30, 1110, []int{100, 350, 600, 850, 1100}},
		// 20 Hz is 50 ms.
		{"at full travel", 1.0, 320, []int{100, 150, 200, 250, 300}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{}`, rateBind("rt", rate4to20)), r, engine.Options{})
			trig(e, r, control.RightTrigger, tc.frac, 0)
			advance(e, r, tc.until)
			within(t, "repeats", repeats(r), tc.want, 1)
			trig(e, r, control.RightTrigger, 0, tc.until)
			balanced(t, r)
		})
	}
}

// TestRepeatRateCurves checks the rate between the ends for each curve: at
// half depth linear gives the middle, quadratic a quarter of the way up and
// cubic an eighth.
func TestRepeatRateCurves(t *testing.T) {
	// The pull is 0.65 of the travel: (0.65 - 0.30) / (1 - 0.30) = 0.5.
	for _, tc := range []struct {
		curve string
		hz    float64
	}{
		{"linear", 4 + 16.0/2},
		{"quadratic", 4 + 16.0/4},
		{"cubic", 4 + 16.0/8},
		{"", 4 + 16.0/2}, // the default is linear
	} {
		t.Run(tc.curve, func(t *testing.T) {
			analog := `{}`
			if tc.curve != "" {
				analog = `{"rt":{"curve":"` + tc.curve + `"}}`
			}
			r := &recorder{}
			e := engine.New(compileAnalog(t, analog, rateBind("rt", `{"delayMs":0,"rateHz":20,"minRateHz":4}`)), r, engine.Options{})
			trig(e, r, control.RightTrigger, 0.65, 0)
			advance(e, r, 1000)
			var want []int
			for i := 1; float64(i)*1000/tc.hz <= 1000; i++ {
				want = append(want, int(float64(i)*1000/tc.hz))
			}
			within(t, "repeats", repeats(r), want, 2)
		})
	}
}

// TestRepeatRateMonotonic checks that, for each curve, a deeper pull never
// repeats slower, and that the period is the one the formula gives.
func TestRepeatRateMonotonic(t *testing.T) {
	for _, curve := range []string{"linear", "quadratic", "cubic"} {
		t.Run(curve, func(t *testing.T) {
			prev := math.Inf(1)
			for step := 0; step <= 10; step++ {
				depth := float64(step) / 10
				frac := 0.30 + 0.70*depth
				r := &recorder{}
				e := engine.New(compileAnalog(t, `{"rt":{"curve":"`+curve+`"}}`, rateBind("rt", `{"delayMs":0,"rateHz":20,"minRateHz":3}`)), r, engine.Options{})
				trig(e, r, control.RightTrigger, frac, 0)
				advance(e, r, 2000)
				times := repeats(r)
				if len(times) < 2 {
					t.Fatalf("depth %.1f: repeats %v", depth, times)
				}
				period := float64(times[1] - times[0]) // the first interval after the start
				shaped := depth
				switch curve {
				case "quadratic":
					shaped = depth * depth
				case "cubic":
					shaped = depth * depth * depth
				}
				want := 1000 / (3 + 17*shaped)
				want = max(want, 40) // the same-key maximum, 25 Hz
				if math.Abs(period-want) > 2 {
					t.Errorf("depth %.1f: period %v ms, want about %.1f", depth, period, want)
				}
				if period > prev+1 {
					t.Errorf("depth %.1f: period %v ms is longer than %v ms at the depth before", depth, period, prev)
				}
				prev = period
			}
		})
	}
}

// TestRepeatRateDepthChangesMidRepeat checks that a change of depth after the
// first repeat moves the next one, earlier or later, measured from the last
// repeat, and that a repeat already due goes once, not once for each period it
// missed.
func TestRepeatRateDepthChangesMidRepeat(t *testing.T) {
	t.Run("deeper and already due", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{}`, rateBind("rt", rate4to20)), r, engine.Options{})
		trig(e, r, control.RightTrigger, 0.30, 0)
		advance(e, r, 100) // the first repeat; the next would be at 350
		trig(e, r, control.RightTrigger, 1.0, 200)
		// 100 + 50 ms is past already: one repeat at once, then 50 ms apart.
		advance(e, r, 400)
		within(t, "repeats", repeats(r), []int{100, 200, 250, 300, 350, 400}, 1)
		// Back to the threshold: 250 ms after the last repeat, at 400.
		trig(e, r, control.RightTrigger, 0.30, 420)
		advance(e, r, 700)
		within(t, "repeats", repeats(r), []int{100, 200, 250, 300, 350, 400, 650}, 1)
	})
	t.Run("deeper and not yet due", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{}`, rateBind("rt", rate4to20)), r, engine.Options{})
		trig(e, r, control.RightTrigger, 0.30, 0)
		advance(e, r, 100)
		// 10 Hz is 0.375 of the way from 4 to 20: 0.30 + 0.70 * 0.375.
		trig(e, r, control.RightTrigger, 0.5625, 120)
		advance(e, r, 330)
		within(t, "repeats", repeats(r), []int{100, 200, 300}, 1)
	})
	t.Run("shallower", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{}`, rateBind("rt", rate4to20)), r, engine.Options{})
		trig(e, r, control.RightTrigger, 1.0, 0)
		advance(e, r, 200)
		trig(e, r, control.RightTrigger, 0.30, 210)
		advance(e, r, 690)
		// The repeat that would have gone at 250 now goes 250 ms after 200.
		within(t, "repeats", repeats(r), []int{100, 150, 200, 450}, 1)
	})
	t.Run("a deadline is always ahead", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{}`, rateBind("rt", rate4to20)), r, engine.Options{})
		trig(e, r, control.RightTrigger, 0.30, 0)
		advance(e, r, 100)
		trig(e, r, control.RightTrigger, 1.0, 5000)
		// Hours behind: the deadline is due, and Apply sends one repeat and
		// moves it ahead of now.
		d, ok := e.NextDeadline()
		if !ok || d > clock.Time(5000*time.Millisecond) {
			t.Fatalf("NextDeadline = %v, %t; want a due deadline", d, ok)
		}
		e.Apply(r.now)
		if d, ok := e.NextDeadline(); !ok || d <= r.now {
			t.Fatalf("after Apply NextDeadline = %v, %t; want one ahead of %v", d, ok, r.now)
		}
		if got := repeats(r); len(got) != 2 {
			t.Errorf("repeats %v; want the first and one catch-up", got)
		}
	})
}

// TestRepeatRateInitialDelay checks that the delay before the first repeat is
// not modulated, whatever the depth is at the start or while it runs.
func TestRepeatRateInitialDelay(t *testing.T) {
	const repeat = `{"delayMs":300,"rateHz":20,"minRateHz":4}`
	for _, tc := range []struct {
		name string
		frac float64
		then float64 // the pull at 100 ms
		want []int
	}{
		{"deep from the start", 1.0, 1.0, []int{300, 350, 400}},
		{"shallow from the start", 0.30, 0.30, []int{300, 550}},
		{"deeper during the delay", 0.30, 1.0, []int{300, 350, 400}},
		{"shallower during the delay", 1.0, 0.30, []int{300, 550}},
	} {
		until := 410
		if tc.then < 0.5 {
			until = 560
		}
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{}`, rateBind("rt", repeat)), r, engine.Options{})
			trig(e, r, control.RightTrigger, tc.frac, 0)
			advance(e, r, 100)
			trig(e, r, control.RightTrigger, tc.then, 100)
			advance(e, r, until)
			within(t, "repeats", repeats(r), tc.want, 1)
		})
	}
}

// TestRepeatRateCappedBySameKeyMaximum checks that the rate never goes past
// what the key can follow: tapPressMs + tapGapMs, 40 ms by default.
func TestRepeatRateCappedBySameKeyMaximum(t *testing.T) {
	for _, frac := range []float64{0.30, 0.5, 1.0} {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{}`, rateBind("rt", `{"delayMs":0,"rateHz":60,"minRateHz":50}`)), r, engine.Options{})
		trig(e, r, control.RightTrigger, frac, 0)
		advance(e, r, 400)
		within(t, fmt.Sprintf("repeats at %.2f", frac), repeats(r), []int{40, 80, 120, 160, 200, 240, 280, 320, 360, 400}, 1)
	}
	// A binding that gives the key more time makes the cap longer.
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, `"rt":{"press":{"type":"keys","keys":"A","repeat":{"delayMs":0,"rateHz":60,"minRateHz":30}},"timing":{"tapPressMs":30,"tapGapMs":30}}`), r, engine.Options{})
	trig(e, r, control.RightTrigger, 1.0, 0)
	advance(e, r, 300)
	within(t, "repeats with a longer cap", repeats(r), []int{60, 120, 180, 240, 300}, 1)
}

// TestRepeatRateTapStyle checks the tap style: complete taps, spaced by the
// modulated period.
func TestRepeatRateTapStyle(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, `"rt":{"press":{"type":"keys","keys":"A","repeat":{"style":"tap","delayMs":100,"rateHz":20,"minRateHz":4}}}`), r, engine.Options{})
	trig(e, r, control.RightTrigger, 1.0, 0)
	advance(e, r, 210)
	within(t, "repeat-taps", downs(r, "repeat-tap"), []int{100, 150, 200}, 1)
	trig(e, r, control.RightTrigger, 0.30, 210)
	trig(e, r, control.RightTrigger, 0, 220)
	if d, ok := e.NextDeadline(); ok {
		t.Errorf("a deadline %v is left after the release", d)
	}
}

// TestRepeatRateSources checks the controls that have a depth, and the depth
// each one follows: the stick controls from the direction threshold, the half
// axes from the axis threshold, the triggers from their press thresholds, the
// second stage from fullPress. The pull or push is at half depth, which with
// min 4 and max 20 under the quadratic curve is 8 Hz: 125 ms.
func TestRepeatRateSources(t *testing.T) {
	const repeat = `{"delayMs":0,"rateHz":20,"minRateHz":4}`
	const curves = `{"ls":{"curve":"quadratic"},"rs":{"curve":"quadratic"},"lt":{"curve":"quadratic"},"rt":{"curve":"quadratic"}}`
	stick := func(right bool, deg, norm float64) func(e *engine.Engine, r *recorder) {
		return func(e *engine.Engine, r *recorder) { stickAt(e, r, right, deg, norm, 0) }
	}
	axis := func(a control.Axis, v float64) func(e *engine.Engine, r *recorder) {
		return func(e *engine.Engine, r *recorder) { axisStep(e, r, a, int16(math.Round(v*32767)), 0) }
	}
	for _, tc := range []struct {
		name, control string
		push          func(e *engine.Engine, r *recorder)
		analog        string
	}{
		{"ls.up", "ls.up", stick(false, 0, 0.65), curves},
		{"ls.down", "ls.down", stick(false, 180, 0.65), curves},
		{"ls.left", "ls.left", stick(false, 270, 0.65), curves},
		{"rs.right", "rs.right", stick(true, 90, 0.65), curves},
		{"ls.active", "ls.active", stick(false, 0, 0.65), curves},
		{"rs.active", "rs.active", stick(true, 40, 0.65), curves},
		{"ls.inner", "ls.inner", stick(false, 0, 0.65), curves},
		// The depth is measured from the direction threshold, not from the
		// threshold of the ring: (0.65 - 0.30) / 0.70 here too.
		{"ls.outer", "ls.outer", stick(false, 0, 0.65), `{"ls":{"curve":"quadratic","outerThreshold":0.5}}`},
		{"ls.upright (eight-way)", "ls.upright", stick(false, 45, 0.65), `{"ls":{"mode":"eightWay","curve":"quadratic"}}`},
		{"rs.downleft (eight-way)", "rs.downleft", stick(true, 225, 0.65), `{"rs":{"mode":"eightWay","curve":"quadratic"}}`},
		{"rt", "rt", axis(control.RightTrigger, 0.65), curves},
		{"lt", "lt", axis(control.LeftTrigger, 0.65), curves},
		// The second stage: fullPress 0.9, so 0.95 is half way to 1.
		{"lt.full", "lt.full", axis(control.LeftTrigger, 0.95), curves},
		{"rt.full", "rt.full", axis(control.RightTrigger, 0.95), curves},
		// A half axis is measured from the axis threshold, 0.30.
		{"rightx+", "rightx+", axis(control.RightX, 0.65), curves},
		{"leftx-", "leftx-", axis(control.LeftX, -0.65), curves},
		{"lefty+", "lefty+", axis(control.LeftY, 0.65), curves},
		{"righty-", "righty-", axis(control.RightY, -0.65), curves},
		{"lefttrigger+", "lefttrigger+", axis(control.LeftTrigger, 0.65), curves},
		{"righttrigger+", "righttrigger+", axis(control.RightTrigger, 0.65), curves},
		// The axis threshold is its own setting.
		{"rightx+ with another axisThreshold", "rightx+", axis(control.RightX, 0.75), `{"axisThreshold":0.5,"rs":{"curve":"quadratic"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, tc.analog, rateBind(tc.control, repeat)), r, engine.Options{})
			tc.push(e, r)
			advance(e, r, 1000)
			got := repeats(r)
			if len(got) < 7 {
				t.Fatalf("repeats %v in %s", got, r)
			}
			for i, ms := range got[:7] {
				if want := 125 * (i + 1); ms < want-3 || ms > want+3 {
					t.Fatalf("repeats %v; want 125 ms apart", got)
				}
			}
		})
	}
}

// TestRepeatRateUsesTheCurveOfItsOwnControl checks that a control follows the
// curve of the stick or trigger it comes from, and no other.
func TestRepeatRateUsesTheCurveOfItsOwnControl(t *testing.T) {
	const analog = `{"ls":{"curve":"cubic"},"rs":{"curve":"linear"},"lt":{"curve":"quadratic"},"rt":{"curve":"cubic"}}`
	bind := strings.Join([]string{
		rateBind("rs.up", `{"delayMs":0,"rateHz":20,"minRateHz":4}`),
		rateBind("leftx+", `{"delayMs":0,"rateHz":20,"minRateHz":4}`),
	}, ",")
	r := &recorder{}
	e := engine.New(compileAnalog(t, analog, bind), r, engine.Options{})
	// The right stick (linear): 12 Hz at half depth. The left half axis
	// (cubic, the left stick's curve): 6 Hz at half depth.
	stickAt(e, r, true, 0, 0.65, 0)
	axisStep(e, r, control.LeftX, int16(math.Round(0.65*32767)), 0)
	advance(e, r, 1000)
	rs := downs(r, "repeat decl/1 ")
	lx := downs(r, "repeat decl/2 ")
	if len(rs) < 10 || len(lx) < 5 {
		t.Fatalf("repeats %v and %v in %s", rs, lx, r)
	}
	if g := gapsOf(rs)[0]; g < 82 || g > 85 {
		t.Errorf("rs.up repeats every %d ms; want 83 (linear)", g)
	}
	if g := gapsOf(lx)[0]; g < 165 || g > 168 {
		t.Errorf("leftx+ repeats every %d ms; want 167 (cubic)", g)
	}
}

// TestRepeatRateOtherAxesDoNotMoveIt checks that the input of an axis that the
// control does not follow leaves its repeat alone.
func TestRepeatRateOtherAxesDoNotMoveIt(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, rateBind("ls.up", rate4to20)), r, engine.Options{})
	stickAt(e, r, false, 0, 0.31, 0)
	advance(e, r, 100)
	before, ok := e.NextDeadline()
	if !ok {
		t.Fatal("no deadline")
	}
	for i, in := range []struct {
		axis control.Axis
		v    int16
	}{{control.RightX, 32767}, {control.RightY, -32767}, {control.LeftTrigger, 32767}, {control.RightTrigger, 32767}} {
		axisStep(e, r, in.axis, in.v, 110+i)
		if d, _ := e.NextDeadline(); d != before {
			t.Errorf("input %d moved the deadline from %v to %v", i, before, d)
		}
	}
}

// TestRepeatWithoutMinRateIsUnchanged checks that a repeat that sets no
// minRateHz keeps its rate whatever the control does.
func TestRepeatWithoutMinRateIsUnchanged(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, rateBind("rt", `{"delayMs":100,"rateHz":20}`)), r, engine.Options{})
	trig(e, r, control.RightTrigger, 0.30, 0)
	advance(e, r, 120)
	trig(e, r, control.RightTrigger, 1.0, 120)
	advance(e, r, 200)
	trig(e, r, control.RightTrigger, 0.35, 200)
	advance(e, r, 320)
	within(t, "repeats", repeats(r), []int{100, 150, 200, 250, 300}, 1)
}

// TestRepeatRateDefaultRate checks that without rateHz the top of the range is
// the default rate, 12 Hz.
func TestRepeatRateDefaultRate(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, rateBind("rt", `{"delayMs":0,"minRateHz":4}`)), r, engine.Options{})
	trig(e, r, control.RightTrigger, 1.0, 0)
	advance(e, r, 400)
	within(t, "repeats", repeats(r), []int{83, 166, 250, 333}, 1)
}

// TestRepeatRateEnds checks that every way a repeat ends leaves nothing to
// move: the release of the control, CancelAll, Pause, Reload and Reset all
// stop it, and a later axis input neither revives it nor leaves a deadline.
func TestRepeatRateEnds(t *testing.T) {
	prog := compileAnalog(t, `{}`, rateBind("rt", rate4to20))
	for _, tc := range []struct {
		name string
		end  func(e *engine.Engine, r *recorder)
		// pull is true when the trigger may keep moving: the engine still sees
		// it down, so it does not press again. After a release or a Reset it
		// would, and begin a new cycle.
		pull bool
	}{
		{"release", func(e *engine.Engine, r *recorder) { trig(e, r, control.RightTrigger, 0, 210) }, false},
		{"CancelAll", func(e *engine.Engine, r *recorder) { e.CancelAll(r.now) }, true},
		{"Pause", func(e *engine.Engine, r *recorder) { e.Pause(r.now) }, true},
		{"Reload", func(e *engine.Engine, r *recorder) { e.Reload(r.now, prog) }, true},
		{"Reset", func(e *engine.Engine, r *recorder) { e.Reset(r.now) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			trig(e, r, control.RightTrigger, 1.0, 0)
			advance(e, r, 200)
			tc.end(e, r)
			n := len(r.out)
			for i, v := range []float64{0.5, 1.0, 0.35, 0.95} {
				if tc.pull {
					trig(e, r, control.RightTrigger, v, 220+10*i)
				} else {
					axisStep(e, r, control.RightX, int16(v*1000), 220+10*i)
				}
				advance(e, r, 220+10*i)
			}
			advance(e, r, 3000)
			for _, s := range r.out[n:] {
				if strings.Contains(s, "repeat") {
					t.Errorf("a repeat after the end: %v", r.out[n:])
				}
			}
			if d, ok := e.NextDeadline(); ok {
				t.Errorf("a deadline %v is left", d)
			}
		})
	}
}

// TestRepeatRateStopsAtRelease checks the release of the control directly.
func TestRepeatRateStopsAtRelease(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{}`, rateBind("rt", rate4to20)), r, engine.Options{})
	trig(e, r, control.RightTrigger, 1.0, 0)
	advance(e, r, 200)
	trig(e, r, control.RightTrigger, 0, 210)
	axisStep(e, r, control.RightX, 32767, 220) // an axis input with no repeat left
	advance(e, r, 3000)
	within(t, "repeats", repeats(r), []int{100, 150, 200}, 1)
	if d, ok := e.NextDeadline(); ok {
		t.Errorf("a deadline %v is left", d)
	}
	balanced(t, r)
}

// TestCurveDoesNotMoveThresholds checks that a curve only shapes the repeat
// rate: the controls go down and up at the same inputs under every curve.
func TestCurveDoesNotMoveThresholds(t *testing.T) {
	bind := strings.Join([]string{
		holdKey("ls.up", "F1"), holdKey("ls.active", "F2"), holdKey("ls.inner", "F3"), holdKey("ls.outer", "F4"),
		holdKey("leftx+", "F5"), holdKey("lt", "F6"), holdKey("lt.full", "F7"), holdKey("lefttrigger+", "F8"),
	}, ",")
	play := func(analog string) string {
		r := &recorder{}
		e := engine.New(compileAnalog(t, analog, bind), r, engine.Options{})
		for i, norm := range []float64{0.1, 0.29, 0.31, 0.5, 0.86, 0.91, 0.95, 0.4, 0.2, 0} {
			stickAt(e, r, false, 0, norm, 10*i)
		}
		for i, frac := range []float64{0.1, 0.29, 0.31, 0.5, 0.88, 0.91, 0.95, 0.85, 0.79, 0.4, 0.15, 0} {
			trig(e, r, control.LeftTrigger, frac, 200+10*i)
		}
		for i, v := range []float64{0.1, 0.29, 0.31, 0.5, 0.1, 0} {
			axisStep(e, r, control.LeftX, int16(math.Round(v*32767)), 400+10*i)
		}
		return r.String()
	}
	linear := play(`{}`)
	if !strings.Contains(linear, "F1") || !strings.Contains(linear, "F7") || !strings.Contains(linear, "F5") {
		t.Fatalf("the controls did not go down: %s", linear)
	}
	for _, curve := range []string{"quadratic", "cubic"} {
		analog := `{"ls":{"curve":"` + curve + `"},"lt":{"curve":"` + curve + `"}}`
		if got := play(analog); got != linear {
			t.Errorf("curve %s changed the edges:\n%s\nwant\n%s", curve, got, linear)
		}
	}
}

// TestCompileCurve checks the curve names Compile accepts and what it fills in.
func TestCompileCurve(t *testing.T) {
	p := &profile.Profile{Layers: []*profile.Layer{{Name: "base"}}}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	if a := prog.Analog(); a.LS.Curve != "linear" || a.RS.Curve != "linear" || a.LT.Curve != "linear" || a.RT.Curve != "linear" {
		t.Errorf("curves = %q %q %q %q; want linear", a.LS.Curve, a.RS.Curve, a.LT.Curve, a.RT.Curve)
	}
	// A model with resolved settings that names none is linear too.
	p = &profile.Profile{AnalogResolved: true, Analog: profile.DefaultAnalog(), Layers: []*profile.Layer{{Name: "base"}}}
	p.Analog.RT.Curve = ""
	if prog, err = engine.Compile(p); err != nil {
		t.Fatalf("Compile with no curve: %v", err)
	} else if prog.Analog().RT.Curve != "linear" {
		t.Errorf("RT curve = %q; want linear", prog.Analog().RT.Curve)
	}
	for _, mut := range []func(a *profile.Analog){
		func(a *profile.Analog) { a.LS.Curve = "sine" },
		func(a *profile.Analog) { a.RS.Curve = "Cubic" },
		func(a *profile.Analog) { a.LT.Curve = "x" },
		func(a *profile.Analog) { a.RT.Curve = "quad" },
	} {
		p := &profile.Profile{AnalogResolved: true, Analog: profile.DefaultAnalog(), Layers: []*profile.Layer{{Name: "base"}}}
		mut(&p.Analog)
		if _, err := engine.Compile(p); err == nil || !strings.Contains(err.Error(), "curve") {
			t.Errorf("Compile accepted a curve it does not know: %v", err)
		}
	}
}

// TestNoAllocsRepeatRate checks that the modulated repeat, the retiming at an
// axis input and the end of the repeat never allocate.
func TestNoAllocsRepeatRate(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	bind := strings.Join([]string{
		rateBind("ls.up", `{"delayMs":50,"rateHz":20,"minRateHz":4}`),
		`"rt":{"press":{"type":"keys","keys":"B","repeat":{"style":"tap","delayMs":0,"rateHz":20,"minRateHz":4}}}`,
		rateBind("rightx+", `{"delayMs":50,"rateHz":20,"minRateHz":4}`),
		rateBind("lt.full", `{"delayMs":50,"rateHz":20,"minRateHz":4}`),
	}, ",")
	prog := compileAnalog(t, `{"ls":{"curve":"quadratic"},"rt":{"curve":"cubic"}}`, bind)
	for _, trace := range []func(engine.Decision){nil, func(engine.Decision) {}} {
		out := &counter{}
		e := engine.New(prog, out, engine.Options{Trace: trace})
		var now clock.Time
		axis := func(a control.Axis, v int16) engine.Input {
			return engine.Input{Kind: engine.InputAxis, Axis: a, Value: v}
		}
		ins := []engine.Input{
			axis(control.LeftY, -20000), axis(control.LeftY, -32767), axis(control.LeftY, -15000), axis(control.LeftY, -32767),
			axis(control.RightTrigger, 12000), axis(control.RightTrigger, 32767), axis(control.RightTrigger, 20000),
			axis(control.RightX, 12000), axis(control.RightX, 30000), axis(control.LeftTrigger, 32767), axis(control.LeftTrigger, 29500),
			axis(control.LeftTrigger, 0), axis(control.RightX, 0), axis(control.RightTrigger, 0), axis(control.LeftY, 0),
		}
		run := func() {
			for _, in := range ins {
				now = now.Add(30 * time.Millisecond)
				in.T = now
				e.Step(in)
				if d, ok := e.NextDeadline(); ok && d <= now.Add(30*time.Millisecond) {
					e.Apply(max(now, d))
				}
			}
		}
		if n := testing.AllocsPerRun(50, run); n != 0 {
			t.Errorf("trace %t: Step and Apply with modulated repeats: %v allocations", trace != nil, n)
		}
		for s, fn := range map[string]func(){
			"CancelAll": func() { e.Step(axis(control.RightTrigger, 32767)); e.CancelAll(now) },
			"Reset":     func() { e.Step(axis(control.LeftY, -32767)); e.Reset(now) },
		} {
			if n := testing.AllocsPerRun(20, fn); n != 0 {
				t.Errorf("trace %t: %s: %v allocations", trace != nil, s, n)
			}
		}
		if out.repeats == 0 {
			t.Errorf("trace %t: nothing repeated: %+v", trace != nil, out)
		}
	}
}
