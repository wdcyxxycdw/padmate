package slot_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The stick that moves the pointer (ADR-0062, section 5) through a Slot and the
// Arbiter. The pointer is not a key, so what these tests check is the ones the
// moves share with keys: they stop on every path that ends a held thing
// (invariant 1's sibling), and nothing is held after.

// mouseAnalog has a left stick that is the pointer at 1000 px/s, with no
// deadzone and the whole travel, so a full push is depth 1.
const mouseAnalog = `{"ls": {"mode": "mouse", "mouseSpeed": 1000, "deadzone": 0, "saturation": 1}}`

// compileMouse compiles a profile with the analog settings and the base-layer
// bindings.
func compileMouse(t testing.TB, analog string, bindings ...string) *engine.Program {
	t.Helper()
	src := `{"schemaVersion": 1, "name": "t", "analog": ` + analog + `, "layers": {"base": {` + strings.Join(bindings, ", ") + `}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return prog
}

// drive moves the clock to msec, running the slot's deadlines on the way, as a
// RunLoop would between inputs.
func (h *harness) drive(t *testing.T, msec int) {
	t.Helper()
	target := clock.Time(time.Duration(msec) * ms)
	for {
		d, ok := h.s.NextDeadline()
		if !ok || d > target {
			break
		}
		h.Clock.Set(max(d, h.Clock.Now()))
		h.s.Advance()
	}
	h.Clock.Set(target)
}

// stick sets the left stick at msec.
func (h *harness) stick(t *testing.T, msec int, x, y int16) {
	t.Helper()
	h.drive(t, msec)
	h.s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: x})
	h.s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty", Value: y})
}

// moved returns the pixels the pointer moved by in the output so far.
func moved(h *harness) (dx, dy int) {
	for _, e := range h.Out.Edges() {
		if e.Kind == simtest.Move {
			dx += int(e.Dx)
			dy += int(e.Dy)
		}
	}
	return dx, dy
}

// The pointer stops on every path that ends what a slot holds: a ReleaseAll
// (the exit and the emergency release), a pause (also by the emergency combo),
// a reload to a profile where the stick is no pointer, and a removed device.
// Afterwards it has no deadline, nothing is held, and the stick that is still
// pushed does nothing until it has been let go. (A reload to a profile where the
// stick still is the pointer does not stop it: TestMousePointerSurvivesReload.)
func TestMousePointerStopsOnEveryPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(t *testing.T, h *harness)
	}{
		{"ReleaseAll", func(t *testing.T, h *harness) {
			if err := h.s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		}},
		{"ReleaseAllForExit", func(t *testing.T, h *harness) {
			if err := h.s.ReleaseAllForExit(); err != nil {
				t.Fatal(err)
			}
		}},
		{"pause", func(t *testing.T, h *harness) {
			if err := h.s.SetPaused(true); err != nil {
				t.Fatal(err)
			}
		}},
		{"reload to a plain stick", func(t *testing.T, h *harness) {
			if err := h.s.Reload(compile(t), 2); err != nil {
				t.Fatal(err)
			}
		}},
		{"device removed", func(t *testing.T, h *harness) {
			h.s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 7})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness{Sim: simtest.New(t)}
			h.s = h.newSlot(t, 0, 1, compileMouse(t, mouseAnalog))
			h.stick(t, 0, 32767, 0)
			h.drive(t, 100)
			before, _ := moved(h)
			if before < 90 || before > 100 {
				t.Fatalf("the pointer moved %d px in 100 ms, want about 100", before)
			}
			tc.path(t, h)
			if d, ok := h.s.NextDeadline(); ok {
				t.Errorf("NextDeadline = %v after the stop, want none", d)
			}
			h.drive(t, 1000)
			if after, _ := moved(h); after != before {
				t.Errorf("the pointer moved %d px after the stop", after-before)
			}
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if errs := h.s.Errors(); len(errs) > 0 {
				t.Errorf("slot errors: %v", errs)
			}
		})
	}
}

// A reload of a stick that is still the pointer (an autosave of the profile, a
// hot reload, an automatic profile switch) does not freeze it: the pointer goes
// on with the new program, at its speed, without the stick being let go, and a
// ReleaseAll ends it as always.
func TestMousePointerSurvivesReload(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, 0, 1, compileMouse(t, mouseAnalog))
	h.stick(t, 0, 32767, 0)
	h.drive(t, 100)
	before, _ := moved(h)
	if err := h.s.Reload(compileMouse(t, `{"ls": {"mode": "mouse", "mouseSpeed": 2000, "deadzone": 0, "saturation": 1}}`), 2); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.s.NextDeadline(); !ok {
		t.Fatal("the pointer has no deadline after the reload")
	}
	h.drive(t, 200)
	// 100 ms at 2000 px/s after, and the 4 ms of 1000 px/s since the last tick.
	if dx, _ := moved(h); dx < before+198 || dx > before+206 {
		t.Errorf("the pointer moved %d px in the 100 ms after the reload, want about 204", dx-before)
	}
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.s.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after ReleaseAll, want none", d)
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if errs := h.s.Errors(); len(errs) > 0 {
		t.Errorf("slot errors: %v", errs)
	}
}

// A trace may end with a stick in mode mouse still pushed, like a key held at the
// end: Replay does not wait for the pointer, which only the caller's ReleaseAll
// ends. A pointer gliding to a stop (smoothing, the stick let go) is waited for.
func TestReplayEndsWithTheStickPushed(t *testing.T) {
	smooth := `{"ls": {"mode": "mouse", "mouseSpeed": 1000, "deadzone": 0, "saturation": 1, "mouseSmoothingMs": 100}}`
	for _, tc := range []struct {
		name    string
		analog  string
		trace   string
		wantDx  float64
		pushing bool // whether the pointer is still going when Replay returns
	}{
		{"pushed at the end", mouseAnalog, "leftx=1@0 lefty=0@0 rightx=0@300", 0, true},
		{"pushed, then more", mouseAnalog, "leftx=1@0 lefty=0@0 leftx=0.5@300 lefty=0@300", 0, true},
		{"released with smoothing", smooth, "leftx=1@0 lefty=0@0 leftx=0@300", 300, false},
		{"pushed with smoothing", smooth, "leftx=1@0 lefty=0@0 rightx=0@300", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness{Sim: simtest.New(t)}
			h.s = h.newSlot(t, 0, 1, compileMouse(t, tc.analog))
			h.replay(t, tc.trace) // fails when Replay does not end cleanly
			dx, _ := moved(h)
			if tc.wantDx != 0 {
				// The glide after the release is waited for: 300 ms of the full
				// push, less the glide that has not been caught up with.
				if math.Abs(float64(dx)-tc.wantDx) > 1.5 {
					t.Errorf("the pointer moved %d px, want %v", dx, tc.wantDx)
				}
			} else if dx == 0 {
				t.Error("the pointer did not move")
			}
			if _, ok := h.s.NextDeadline(); ok != tc.pushing {
				t.Errorf("the slot has a deadline: %v, want %v", ok, tc.pushing)
			}
			if err := h.s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			if d, ok := h.s.NextDeadline(); ok {
				t.Errorf("NextDeadline = %v after ReleaseAll, want none", d)
			}
			// What padmated does at its exit: nothing is left to settle.
			if err := h.s.Replay(h.Clock, nil); err != nil {
				t.Errorf("Replay after ReleaseAll: %v", err)
			}
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
		})
	}
}

// The gate of the Arbiter (a paused or locked desktop) drops the moves a slot
// still sends: nothing is left queued or held.
func TestMousePointerDroppedByTheClosedGate(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, 0, 1, compileMouse(t, mouseAnalog))
	h.stick(t, 0, 32767, 0)
	h.drive(t, 50)
	n, _ := moved(h)
	h.Arb.SetBlocked(true)
	h.drive(t, 150)
	if after, _ := moved(h); after != n {
		t.Errorf("%d px moved with the gate closed", after-n)
	}
	if st := h.Arb.Stats(); st.Blocked == 0 {
		t.Error("no move counted as blocked")
	}
	h.Arb.SetBlocked(false)
	h.drive(t, 250)
	if after, _ := moved(h); after <= n {
		t.Error("the pointer did not move again with the gate open")
	}
	h.stick(t, 250, 0, 0)
	h.drive(t, 400)
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

// A precision binding on a slot slows only that slot's sticks, and its release
// path ends with the cycle: a ReleaseAll while it is held makes the next push
// full speed.
func TestMousePrecisionEndsWithReleaseAll(t *testing.T) {
	prec := `"leftshoulder": {"press": {"type": "mouse", "op": "precision", "factor": 0.5}}`
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, 0, 1, compileMouse(t, mouseAnalog, prec))
	h.s.Handle(button("leftshoulder", true))
	h.stick(t, 0, 32767, 0)
	h.stick(t, 1000, 0, 0)
	h.drive(t, 1100)
	if dx, _ := moved(h); dx < 499 || dx > 501 {
		t.Fatalf("half speed moved %d px in a second, want 500", dx)
	}
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	n, _ := moved(h)
	h.stick(t, 1200, 32767, 0)
	h.stick(t, 2200, 0, 0)
	h.drive(t, 2300)
	if dx, _ := moved(h); dx-n < 999 || dx-n > 1001 {
		t.Errorf("after ReleaseAll the pointer moved %d px in a second, want 1000", dx-n)
	}
}

// Through the Slot and the real Arbiter, on the virtual clock of Replay: a full
// push for a second moves the pointer 1000 px at 1000 px/s, a diagonal the same
// along its length, and the precision button held the whole time halves it.
func TestMousePointerDistanceThroughTheSlot(t *testing.T) {
	prec := `"leftshoulder": {"press": {"type": "mouse", "op": "precision", "factor": 0.5}}`
	for _, tc := range []struct {
		name   string
		trace  string
		wx, wy float64
	}{
		{"right", "leftx=1@0 lefty=0@0 leftx=0@1000", 1000, 0},
		{"down", "leftx=0@0 lefty=1@0 lefty=0@1000", 0, 1000},
		{"diagonal", "leftx=0.7071@0 lefty=-0.7071@0 leftx=0@1000 lefty=0@1000", 707.1, -707.1},
		{"precision held", "+leftshoulder@0 leftx=1@0 lefty=0@0 leftx=0@1000 -leftshoulder@1100", 500, 0},
		{"released halfway", "leftx=1@0 lefty=0@0 +leftshoulder@500 leftx=0@1000", 750, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness{Sim: simtest.New(t)}
			h.s = h.newSlot(t, 0, 1, compileMouse(t, mouseAnalog, prec))
			h.replay(t, tc.trace)
			dx, dy := moved(h)
			if math.Abs(float64(dx)-tc.wx) > 1.5 || math.Abs(float64(dy)-tc.wy) > 1.5 {
				t.Errorf("moved %d,%d, want %v,%v", dx, dy, tc.wx, tc.wy)
			}
			if d, ok := h.s.NextDeadline(); ok {
				t.Errorf("NextDeadline = %v after the pointer stopped", d)
			}
		})
	}
}
