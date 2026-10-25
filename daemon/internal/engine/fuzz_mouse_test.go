package engine_test

import (
	"math"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// fuzzMouseAnalog has two sticks that move the pointer, at 1000 and 600 px/s,
// the second quadratic; fuzzMouseSmooth is the same smoothed over 120 ms.
const (
	fuzzMouseAnalog = `{"ls":{"mode":"mouse","mouseSpeed":1000,"deadzone":0.1,"saturation":0.95,"directionThreshold":0.1},` +
		`"rs":{"mode":"mouse","mouseSpeed":600,"deadzone":0.1,"saturation":0.95,"directionThreshold":0.1,"curve":"quadratic"}}`
	fuzzMouseSmooth = `{"ls":{"mode":"mouse","mouseSpeed":1000,"deadzone":0.1,"saturation":0.95,"directionThreshold":0.1,"mouseSmoothingMs":120},` +
		`"rs":{"mode":"mouse","mouseSpeed":600,"deadzone":0.1,"saturation":0.95,"directionThreshold":0.1,"curve":"quadratic","mouseSmoothingMs":30}}`
	// Precision on a press, on a hold with nothing else (so it needs no
	// deadline), latched, and a jump.
	fuzzMouseBindings = `"leftshoulder":{"press":{"type":"mouse","op":"precision","factor":0.5}},` +
		`"rightshoulder":{"hold":{"type":"mouse","op":"precision","factor":0.2}},` +
		`"east":{"latch":true,"press":{"type":"mouse","op":"precision","factor":0.4}},` +
		`"south":{"press":{"type":"mouse","op":"jump","x":0.25,"y":0.75}}`
)

// fuzzMouseOut is the Output of FuzzMouseStick. It fails at a move while the
// engine is paused, and at a move longer than fuzzMaxStep.
type fuzzMouseOut struct {
	t     *testing.T
	e     *engine.Engine
	moves int
	sumX  float64
	sumY  float64
	jumps int
}

// fuzzMaxStep is the most one move can have: a capped step (MouseMaxDT) and the
// part of a tick (MouseTick) an input before it had settled already, at the
// fastest speed, plus a pixel of rounding on each.
const fuzzMaxStep = 1000*float64(engine.MouseMaxDT+engine.MouseTick)/float64(time.Second) + 2

func (o *fuzzMouseOut) Down(engine.OwnerID, keys.Chord, time.Duration) {}
func (o *fuzzMouseOut) Up(engine.OwnerID, keys.Chord)                  {}
func (o *fuzzMouseOut) Tap(engine.OwnerID, keys.Chord, time.Duration)  {}
func (o *fuzzMouseOut) Repeat(engine.OwnerID, keys.Chord)              {}
func (o *fuzzMouseOut) TapSequence(engine.OwnerID, []keys.Chord, time.Duration, time.Duration) {
}
func (o *fuzzMouseOut) Sequence(engine.OwnerID, []keys.Step, time.Duration, time.Duration) {
}
func (o *fuzzMouseOut) TypeText(engine.OwnerID, string, time.Duration)      {}
func (o *fuzzMouseOut) RepeatTap(engine.OwnerID, keys.Chord, time.Duration) {}
func (o *fuzzMouseOut) StopRepeat(engine.OwnerID, keys.Chord)               {}

func (o *fuzzMouseOut) Move(_ engine.OwnerID, dx, dy int32) {
	o.t.Helper()
	if o.e.Paused() {
		o.t.Fatalf("a move of %d,%d while paused", dx, dy)
	}
	if dx == 0 && dy == 0 {
		o.t.Fatalf("a move of nothing")
	}
	if math.Abs(float64(dx)) > fuzzMaxStep || math.Abs(float64(dy)) > fuzzMaxStep {
		o.t.Fatalf("a move of %d,%d is more than one capped step (%v px) at the fastest speed", dx, dy, fuzzMaxStep)
	}
	o.moves++
	o.sumX += math.Abs(float64(dx))
	o.sumY += math.Abs(float64(dy))
}

func (o *fuzzMouseOut) Jump(_ engine.OwnerID, x, y int32) {
	o.t.Helper()
	if x < 0 || x > 65535 || y < 0 || y > 65535 {
		o.t.Fatalf("a jump to %d,%d is outside the desktop", x, y)
	}
	if o.e.Paused() {
		o.t.Fatalf("a jump while paused")
	}
	o.jumps++
}

// FuzzMouseStick drives two sticks in mode mouse with random pushes, waits,
// stalls, pauses, cancels, resets and reloads, and checks that the pointer is
// never moved while paused or by more than one capped step of the fastest
// speed, that no deadline is left after a stop (a Reload is a stop only while
// paused: it keeps a pushed stick moving), that the engine never loops on
// a deadline that does not move, and that the total distance is under what the
// time allows. Each byte is one operation: its low 4 bits select it.
//
//	0–3   set the left stick X, Y, the right stick X, Y to (next byte - 128) / 128
//	4–6   toggle leftshoulder, rightshoulder, east (the precision bindings)
//	7     toggle south (a jump)
//	8     wait max(1, next byte) ms, running the deadlines on the way
//	9     wait until the next deadline
//	10    Pause, or Resume when paused
//	11    CancelAll
//	12    Reset
//	13    Reload, switching between the unsmoothed and the smoothed program
//	14    wait (byte>>4)*8+1 ms
//	15    a stall: the clock jumps (next byte) * 10 ms and Apply runs once
func FuzzMouseStick(f *testing.F) {
	f.Add([]byte{0, 255, 1, 128, 8, 200, 8, 200, 0, 128})
	f.Add([]byte{0, 255, 4, 0, 8, 100, 4, 0, 8, 100, 0, 128})
	f.Add([]byte{0, 255, 2, 255, 10, 0, 8, 100, 10, 0, 8, 100, 0, 128, 2, 128})
	f.Add([]byte{3, 0, 13, 0, 8, 50, 11, 0, 12, 0, 8, 50})
	f.Add([]byte{0, 200, 15, 90, 15, 5, 9, 0, 0, 128})
	f.Add([]byte{6, 0, 0, 255, 8, 100, 5, 0, 8, 100, 6, 0, 8, 100, 0, 128, 7, 0, 7, 0})
	progs := [2]*engine.Program{
		compileAnalog(f, fuzzMouseAnalog, fuzzMouseBindings),
		compileAnalog(f, fuzzMouseSmooth, fuzzMouseBindings),
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 600 {
			data = data[:600]
		}
		out := &fuzzMouseOut{t: t}
		e := engine.New(progs[0], out, engine.Options{})
		out.e = e
		var now clock.Time
		which := 0
		var raw [4]int16
		axes := [4]control.Axis{control.LeftX, control.LeftY, control.RightX, control.RightY}
		down := map[control.Control]bool{}
		paused := false
		var elapsed time.Duration

		// run moves time forward by d, running Apply at every deadline.
		run := func(d time.Duration) {
			target := now.Add(d)
			elapsed += d
			for steps := 0; ; steps++ {
				dl, ok := e.NextDeadline()
				if !ok || dl > target {
					break
				}
				if steps > 100000 {
					t.Fatalf("still running deadlines after %d steps", steps)
				}
				now = max(dl, now)
				e.Apply(now)
				if d2, ok := e.NextDeadline(); ok && d2 <= now {
					t.Fatalf("deadline %v is still due after Apply at %v", time.Duration(d2), time.Duration(now))
				}
			}
			now = target
		}
		stopped := func(what string) {
			t.Helper()
			if dl, ok := e.NextDeadline(); ok {
				t.Fatalf("a deadline at %v is left after %s", time.Duration(dl), what)
			}
		}
		arg := func(i int) int {
			if i+1 < len(data) {
				return int(data[i+1])
			}
			return 0
		}
		start := now
		for i := 0; i < len(data); i += 2 {
			op := int(data[i] & 15)
			switch {
			case op <= 3:
				raw[op] = int16(math.Round(float64(arg(i)-128) / 128 * 32767))
				e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: axes[op], Value: raw[op]})
			case op <= 7:
				c := [4]control.Control{control.LeftShoulder, control.RightShoulder, control.East, control.South}[op-4]
				down[c] = !down[c]
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: down[c]})
			case op == 8:
				run(time.Duration(max(1, arg(i))) * time.Millisecond)
			case op == 9:
				if dl, ok := e.NextDeadline(); ok && dl > now {
					run(time.Duration(dl - now))
				}
			case op == 10:
				if paused {
					e.Resume()
				} else {
					e.Pause(now)
					stopped("Pause")
				}
				paused = !paused
			case op == 11:
				e.CancelAll(now)
				stopped("CancelAll")
			case op == 12:
				e.Reset(now)
				clear(down)
				raw = [4]int16{}
				stopped("Reset")
			case op == 13:
				which = 1 - which
				e.Reload(now, progs[which])
				// A Reload does not stop the pointer of a stick still pushed (it
				// takes the new settings), but a paused engine has none.
				if paused {
					stopped("Reload while paused")
				}
			case op == 14:
				run(time.Duration(int(data[i]>>4)*8+1) * time.Millisecond)
			default:
				// A stall: the clock jumps and only then does Apply run once.
				jump := time.Duration(arg(i)) * 10 * time.Millisecond
				now = now.Add(jump)
				elapsed += jump
				e.Apply(now)
			}
		}
		// Everything let go and let settle: the pointer stops.
		e.Reset(now)
		stopped("the final Reset")
		// Two sticks at 1000 and 600 px/s for the time that passed (a stall
		// counts as at most MouseMaxDT, so the elapsed time is an upper bound),
		// plus the pixels of a step that straddles the end.
		limit := 1600*elapsed.Seconds() + 2*fuzzMaxStep
		if out.sumX > limit || out.sumY > limit {
			t.Fatalf("the pointer moved %v,%v px in %v from %v, more than the %v the sticks allow", out.sumX, out.sumY, elapsed, time.Duration(start), limit)
		}
	})
}
