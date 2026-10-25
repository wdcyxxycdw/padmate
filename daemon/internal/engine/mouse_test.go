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

// Tests of the stick that moves the pointer (MOU-01, MOU-04, MOU-05, MOU-06,
// MOU-08; ADR-0062, section 5).

// moveEvent is a Move or Jump the engine sent.
type moveEvent struct {
	t      clock.Time
	dx, dy int32
	owner  engine.OwnerID
}

// mouseOut is an engine.Output that records the moves and jumps with their
// times, and the rest as text like the recorder.
type mouseOut struct {
	*recorder
	moves, jumps []moveEvent
}

func (m *mouseOut) Move(o engine.OwnerID, dx, dy int32) {
	m.moves = append(m.moves, moveEvent{m.now, dx, dy, o})
}

func (m *mouseOut) Jump(o engine.OwnerID, x, y int32) {
	m.jumps = append(m.jumps, moveEvent{m.now, x, y, o})
}

// msim is an Engine driven in virtual time the way a slot does: Apply runs at
// every deadline before an input.
type msim struct {
	t   *testing.T
	e   *engine.Engine
	out *mouseOut
	now clock.Time
}

// The analog settings of most tests: no deadzone and the whole travel, so a
// raw 32767 is depth 1 and a raw x is depth x/32767. The direction threshold is
// the default, 0.3: the pointer does not look at it.
const mouseAnalog = `"mode":"mouse","mouseSpeed":1000,"deadzone":0,"saturation":1`

func newMsim(t *testing.T, ls string, bindings ...string) *msim {
	t.Helper()
	return newMsimAnalog(t, `{"ls":{`+ls+`}}`, bindings...)
}

func newMsimAnalog(t *testing.T, analog string, bindings ...string) *msim {
	t.Helper()
	prog := compileAnalog(t, analog, strings.Join(bindings, ","))
	out := &mouseOut{recorder: &recorder{}}
	return &msim{t: t, e: engine.New(prog, out, engine.Options{}), out: out}
}

// at moves virtual time to d, running Apply at every deadline up to and
// including d.
func (m *msim) at(d time.Duration) {
	m.t.Helper()
	target := clock.Time(d)
	if target < m.now {
		m.t.Fatalf("at(%v) is before %v", d, time.Duration(m.now))
	}
	for {
		dl, ok := m.e.NextDeadline()
		if !ok || dl > target {
			break
		}
		m.now = max(dl, m.now)
		m.out.now = m.now
		m.e.Apply(m.now)
		if d2, ok := m.e.NextDeadline(); ok && d2 <= m.now {
			m.t.Fatalf("deadline %v is still due after Apply at %v", time.Duration(d2), time.Duration(m.now))
		}
	}
	m.now = target
	m.out.now = target
}

// stick puts the left stick at raw (x, y) at d: X first, then Y at the same
// time, as one report reaches a slot.
func (m *msim) stick(d time.Duration, x, y int16) {
	m.t.Helper()
	m.at(d)
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputAxis, Axis: control.LeftX, Value: x})
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputAxis, Axis: control.LeftY, Value: y})
}

// button presses or releases a button at d.
func (m *msim) button(d time.Duration, name string, down bool) {
	m.t.Helper()
	m.at(d)
	c, ok := control.Parse(name)
	if !ok {
		m.t.Fatalf("unknown control %q", name)
	}
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputButton, Control: c, Down: down})
}

// total returns the sum of the moves so far.
func (m *msim) total() (dx, dy int) {
	for _, mv := range m.out.moves {
		dx += int(mv.dx)
		dy += int(mv.dy)
	}
	return dx, dy
}

// totalUpTo returns the sum of the moves sent up to and including d.
func (m *msim) totalUpTo(d time.Duration) (dx, dy int) {
	for _, mv := range m.out.moves {
		if time.Duration(mv.t) <= d {
			dx += int(mv.dx)
			dy += int(mv.dy)
		}
	}
	return dx, dy
}

func near(t *testing.T, what string, got int, want float64, tol float64) {
	t.Helper()
	if math.Abs(float64(got)-want) > tol {
		t.Errorf("%s = %d, want %v (+-%v)", what, got, want, tol)
	}
}

const full = 32767

// MouseTickForTest is engine.MouseTick.
const MouseTickForTest = engine.MouseTick

// Full deflection right for a second at 1000 per second goes 1000 pixels, and
// the pointer stops after the stick does.
func TestMouseFullDeflection(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	m.stick(1000*ms, 0, 0)
	m.at(2000 * ms)
	dx, dy := m.total()
	near(t, "dx", dx, 1000, 1)
	near(t, "dy", dy, 0, 0)
	if _, ok := m.e.NextDeadline(); ok {
		t.Error("the engine still has a deadline after the pointer stopped")
	}
	// Eight pixels every tick, the first at the first tick.
	if mv := m.out.moves[0]; mv.t != clock.Time(8*ms) || mv.dx != 8 || mv.dy != 0 {
		t.Errorf("first move %+v, want 8,0 at 8ms", mv)
	}
	for _, mv := range m.out.moves {
		if mv.owner != (engine.OwnerID{Source: engine.SourceDecl}) {
			t.Errorf("move %+v owned by %v, want decl/0", mv, mv.owner)
		}
		if mv.dx == 0 && mv.dy == 0 {
			t.Errorf("a move of nothing was sent: %+v", mv)
		}
	}
	// It stopped with the stick: the last move is at the first tick after the
	// release, at the latest.
	if last := m.out.moves[len(m.out.moves)-1]; last.t > clock.Time(1008*ms) {
		t.Errorf("the last move is at %v, after the first tick behind the release", time.Duration(last.t))
	}
}

// The pointer moves for as long as the clock says the stick was pushed, also
// when the release falls between two ticks.
func TestMouseDistanceIsSpeedTimesTime(t *testing.T) {
	for _, held := range []time.Duration{100 * ms, 333 * ms, 1003 * ms, 2500 * ms} {
		t.Run(held.String(), func(t *testing.T) {
			m := newMsim(t, mouseAnalog)
			m.stick(5*ms, full, 0)
			m.stick(5*ms+held, 0, 0)
			m.at(5*ms + held + time.Second)
			dx, _ := m.total()
			near(t, "dx", dx, held.Seconds()*1000, 1)
		})
	}
}

func TestMouseDirections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x, y   int16
		wx, wy float64
	}{
		{"right", full, 0, 1000, 0},
		{"left", -full, 0, -1000, 0},
		{"up", 0, -full, 0, -1000}, // the stick up is a negative Y; the pointer goes up
		{"down", 0, full, 0, 1000},
		{"down right", 23170, 23170, 707.1, 707.1},
		{"up left", -23170, -23170, -707.1, -707.1},
		{"up right", 23170, -23170, 707.1, -707.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMsim(t, mouseAnalog)
			m.stick(0, tc.x, tc.y)
			m.stick(time.Second, 0, 0)
			m.at(2 * time.Second)
			dx, dy := m.total()
			near(t, "dx", dx, tc.wx, 1.5)
			near(t, "dy", dy, tc.wy, 1.5)
		})
	}
}

// A diagonal is not faster than a straight push: the speed is that of the
// length of the push, not of each axis.
func TestMouseDiagonalSpeed(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, 20000, 20000) // length 0.863 of travel
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	dx, dy := m.total()
	got := math.Hypot(float64(dx), float64(dy))
	// depth = 0.863, the length of the push
	want := 1000 * math.Hypot(20000, 20000) / 32767
	if math.Abs(got-want) > 2 {
		t.Errorf("distance %v, want %v", got, want)
	}
	if math.Abs(float64(dx-dy)) > 1 {
		t.Errorf("dx %d and dy %d differ on a 45 degree push", dx, dy)
	}
}

// The curve shapes the speed: quadratic squares the depth, cubic cubes it.
func TestMouseCurves(t *testing.T) {
	// Half of the travel is depth 0.5.
	const raw = 16384
	for _, tc := range []struct {
		curve string
		want  float64
	}{{"linear", 500}, {"quadratic", 250}, {"cubic", 125}} {
		t.Run(tc.curve, func(t *testing.T) {
			m := newMsim(t, mouseAnalog+`,"curve":"`+tc.curve+`"`)
			m.stick(0, raw, 0)
			m.stick(time.Second, 0, 0)
			m.at(2 * time.Second)
			dx, _ := m.total()
			near(t, "dx", dx, tc.want, 1.5)
		})
	}
}

// The pointer starts the moment the stick leaves the deadzone, whatever the
// directionThreshold is: the speed is 0 at the edge of the deadzone and
// mouseSpeed at saturation, and nothing moves inside the deadzone.
func TestMouseDeadzoneAndSpeed(t *testing.T) {
	const ls = `"mode":"mouse","mouseSpeed":400,"deadzone":0.2,"saturation":0.9,"directionThreshold":0.5`
	push := func(t *testing.T, raw int16) int {
		t.Helper()
		m := newMsim(t, ls)
		m.stick(0, raw, 0)
		m.stick(time.Second, 0, 0)
		m.at(2 * time.Second)
		dx, dy := m.total()
		if dy != 0 {
			t.Errorf("dy = %d", dy)
		}
		return dx
	}
	// Inside the deadzone (a magnitude of 0.15 against 0.2) nothing happens.
	if dx := push(t, 4915); dx != 0 {
		t.Errorf("a push inside the deadzone moved the pointer by %d", dx)
	}
	// Just outside it (0.25), far under the directionThreshold (it needs depth
	// 0.5): depth (0.25-0.2)/0.7, at 400 px/s.
	mag := func(raw float64) float64 { return raw / 32767 }
	depth := func(raw float64) float64 { return (mag(raw) - 0.2) / 0.7 }
	near(t, "just outside the deadzone", push(t, 8192), 400*depth(8192), 1.5)
	// Half the travel (0.55): depth 0.5.
	near(t, "half the travel", push(t, 18022), 200, 1.5)
	// The saturation radius is full travel: speed 400.
	near(t, "full", push(t, full), 400, 1)
	// The first tick already moves the pointer: the push just outside the
	// deadzone is not held back by anything.
	m := newMsim(t, ls)
	m.stick(0, 8192, 0)
	m.at(100 * ms)
	if len(m.out.moves) == 0 || time.Duration(m.out.moves[0].t) > 40*ms {
		t.Errorf("moves %+v: the pointer did not start with the push", m.out.moves)
	}
}

// The directionThreshold is for the controls of the stick (ls.active), which
// follow it as before, and the pointer does not wait for it.
func TestMouseDirectionThresholdIsForTheControls(t *testing.T) {
	m := newMsim(t, `"mode":"mouse","mouseSpeed":1000,"deadzone":0,"saturation":1,"directionThreshold":0.5`,
		holdKey("ls.active", "A"))
	m.stick(0, 9830, 0) // 0.3 of the travel: moving, not active
	m.stick(500*ms, 22938, 0)
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 300*0.5+700*0.5, 1) // 0.3 for half a second, 0.7 for half a second
	// ls.active is pressed once the stick is past 0.5, at the second report, and
	// let go with the stick.
	if got, want := strings.Join(m.out.out, ", "), "@500 down decl/1 A, @1000 up decl/1 A"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}
	balanced(t, m.out.recorder)
}

// The default speed is 1200 per second.
func TestMouseDefaultSpeed(t *testing.T) {
	m := newMsimAnalog(t, `{"ls":{"mode":"mouse","deadzone":0,"saturation":1}}`)
	m.stick(0, full, 0)
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 1200, 1)
}

// The right stick moves the pointer too, with its own settings.
func TestMouseRightStick(t *testing.T) {
	m := newMsimAnalog(t, `{"rs":{"mode":"mouse","mouseSpeed":500,"deadzone":0,"saturation":1}}`)
	m.at(0)
	m.e.Step(engine.Input{T: 0, Kind: engine.InputAxis, Axis: control.RightX, Value: -full})
	m.e.Step(engine.Input{T: 0, Kind: engine.InputAxis, Axis: control.RightY, Value: 0})
	m.at(time.Second)
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputAxis, Axis: control.RightX, Value: 0})
	m.at(2 * time.Second)
	dx, dy := m.total()
	near(t, "dx", dx, -500, 1)
	near(t, "dy", dy, 0, 0)
}

// A mouse-mode stick sends no directions and no knob ticks; its ring and
// active controls work.
func TestMouseSendsNoDirections(t *testing.T) {
	m := newMsim(t, mouseAnalog,
		holdKey("ls.up", "U"), holdKey("ls.right", "R"), holdKey("ls.cw", "C"),
		holdKey("ls.active", "A"), holdKey("ls.outer", "O"))
	m.stick(0, full, 0)
	m.stick(500*ms, 0, -full)
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	for _, line := range m.out.out {
		if strings.HasSuffix(line, " U") || strings.HasSuffix(line, " R") || strings.HasSuffix(line, " C") {
			t.Errorf("a stick in mode mouse sent a direction or a knob tick: %s", line)
		}
	}
	got := strings.Join(m.out.out, ", ")
	if !strings.Contains(got, "down decl/1 A") || !strings.Contains(got, "up decl/1 A") || !strings.Contains(got, " O") {
		t.Errorf("ls.active and ls.outer did not work: %s", got)
	}
	balanced(t, m.out.recorder)
}

// The smoothing is an exponential moving average of the velocity: the pointer
// eases in with the time constant, and glides on after the stick is let go,
// for the same total distance as without it.
func TestMouseSmoothingStepResponse(t *testing.T) {
	const tau = 100 * ms
	m := newMsim(t, mouseAnalog+`,"mouseSmoothingMs":100`)
	m.stick(0, full, 0)
	m.at(time.Second)
	// distance(t) = u*(t - tau*(1 - e^(-t/tau)))
	dist := func(t time.Duration) float64 {
		x, ta := t.Seconds(), tau.Seconds()
		return 1000 * (x - ta*(1-math.Exp(-x/ta)))
	}
	for _, at := range []time.Duration{50 * ms, 100 * ms, 200 * ms, 500 * ms, time.Second} {
		dx, _ := m.totalUpTo(at)
		// What has been sent is the distance at the last tick (every 8 ms from
		// the first at 8 ms), less a pixel not whole yet.
		lastTick := at - at%MouseTickForTest
		near(t, fmt.Sprintf("distance at %v", at), dx, dist(lastTick), 1.01)
	}
	m.stick(time.Second, 0, 0)
	m.at(3 * time.Second)
	dx, _ := m.total()
	// The average keeps the area: 1000 px/s for a second.
	near(t, "total", dx, 1000, 1)
	// It glided on: a lot of that distance came after the release.
	after := dx - mustUpTo(m, time.Second)
	if after < 90 || after > 101 {
		t.Errorf("%d px after the release, want about 100 (1000 px/s times 0.1 s)", after)
	}
	if _, ok := m.e.NextDeadline(); ok {
		t.Error("the pointer never stopped gliding")
	}
	// It stopped when the speed fell under half a pixel a second: about
	// ln(2000) time constants after the release.
	last := time.Duration(m.out.moves[len(m.out.moves)-1].t)
	if last > time.Second+800*ms {
		t.Errorf("the last move is at %v, after the glide should be over", last)
	}
}

func mustUpTo(m *msim, d time.Duration) int {
	dx, _ := m.totalUpTo(d)
	return dx
}

// The smoothing is exact whatever the ticks do: a coarse tick pattern (a stall
// between inputs of a held stick) gives the distance of the continuous system.
func TestMouseSmoothingFollowsTheClock(t *testing.T) {
	m := newMsim(t, mouseAnalog+`,"mouseSmoothingMs":200`)
	// Pushed half way for 30 ms, then full for 40 ms, then nothing for 30 ms:
	// each step shorter than the 50 ms cap.
	m.stick(0, 16384, 0)
	m.stick(30*ms, full, 0)
	m.stick(70*ms, 0, 0)
	m.at(10 * time.Second)
	dx, _ := m.total()
	// Replay the continuous system.
	norm := func(raw float64) float64 { return raw / 32767 }
	depth := norm
	tau := 0.2
	v, d := 0.0, 0.0
	step := func(u float64, dt float64) {
		k := math.Exp(-dt / tau)
		d += u*dt + (v-u)*tau*(1-k)
		v = u + (v-u)*k
	}
	step(1000*depth(16384), 0.030)
	step(1000*depth(full), 0.040)
	step(0, 10) // the glide ends: the whole area is in
	// The fraction of a pixel left at the stop is dropped.
	near(t, "total", dx, d, 1.5)
}

// The pointer is smoothed on its way up and down, but it does not glide on
// for ever: it stops.
func TestMouseSmoothingStops(t *testing.T) {
	m := newMsim(t, mouseAnalog+`,"mouseSmoothingMs":500`)
	m.stick(0, full, 0)
	m.stick(100*ms, 0, 0)
	m.at(time.Minute)
	if _, ok := m.e.NextDeadline(); ok {
		t.Error("the pointer is still ticking a minute after the stick was let go")
	}
	dx, _ := m.total()
	near(t, "total", dx, 100, 1)
}

// After a stall the pointer takes at most MouseMaxDT of it: it does not jump
// by what it would have moved while nothing ran.
func TestMouseDTCap(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	m.at(104 * ms)
	before, _ := m.total()
	near(t, "before the stall", before, 104, 0)
	// Nothing runs until 1100 ms. Applying at 1100 ms moves it by 50 ms of
	// speed, not by 996 ms.
	m.now = clock.Time(1100 * ms)
	m.out.now = m.now
	m.e.Apply(m.now)
	after, _ := m.total()
	if got := after - before; got < 50 || got > 51 {
		t.Errorf("moved %d px after a 996 ms stall, want %d (MouseMaxDT of speed)", got, 50)
	}
	// And the ticks go on from there at their pace.
	m.at(1200 * ms)
	later, _ := m.total()
	near(t, "after the stall", later-after, 100, 8)
}

// A stick held steady sends no input, and the pointer keeps going: only the
// clock moves it.
func TestMouseKeepsGoingWithoutInput(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	m.at(3 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 3000, 8)
	m.stick(3*time.Second, 0, 0)
	m.at(4 * time.Second)
	dx, _ = m.total()
	near(t, "dx", dx, 3000, 8)
}

// The two axes of one report: X arrives first with the old Y. The half-updated
// target is in force for no time at all.
func TestMouseHalfReportIsNotUsed(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	// Release a push to the right and move to the left in one report at 500
	// ms: X first, with a tick due at exactly that moment.
	m.at(500 * ms)
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputAxis, Axis: control.LeftX, Value: 0})
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputAxis, Axis: control.LeftY, Value: -full})
	m.at(time.Second)
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	dx, dy := m.total()
	near(t, "dx", dx, 500, 1)
	near(t, "dy", dy, -500, 1)
}

// A move is only sent when there is a whole pixel to send, and the fractions
// are not lost.
func TestMouseKeepsTheRemainder(t *testing.T) {
	m := newMsim(t, `"mode":"mouse","mouseSpeed":50,"deadzone":0,"saturation":1`)
	m.stick(0, full, 0)
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	// 50 px/s is 0.4 px per tick: a pixel every 2.5 ticks, never 0.4.
	for _, mv := range m.out.moves {
		if mv.dx != 1 || mv.dy != 0 {
			t.Errorf("move %+v, want one pixel", mv)
		}
	}
	dx, _ := m.total()
	near(t, "dx", dx, 50, 1)
}

// A backward step of time counts as none.
func TestMouseTimeGoingBack(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(100*ms, full, 0)
	m.at(200 * ms)
	before, _ := m.total()
	m.e.Step(engine.Input{T: clock.Time(150 * ms), Kind: engine.InputAxis, Axis: control.LeftX, Value: full})
	m.e.Apply(clock.Time(150 * ms))
	m.at(300 * ms)
	after, _ := m.total()
	if after < before {
		t.Errorf("the pointer went back: %d then %d", before, after)
	}
	near(t, "distance", after, 200, 8)
}

// Pause stops the pointer and drops what it had not sent; it does not move
// while paused, nor after the resume until the stick has been let go.
func TestMousePause(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	m.at(500 * ms)
	m.e.Pause(m.now)
	n := len(m.out.moves)
	if _, ok := m.e.NextDeadline(); ok {
		t.Error("a deadline is left after Pause")
	}
	m.at(time.Second)
	m.stick(time.Second, full, 0) // a report while paused
	m.at(1500 * ms)
	m.e.Resume()
	m.stick(1500*ms, full, -full) // the stick moves after the resume, still pushed
	m.at(2 * time.Second)
	if len(m.out.moves) != n {
		t.Errorf("the pointer moved %d times while paused or still locked after the resume", len(m.out.moves)-n)
	}
	// Let go, and push again: it moves.
	m.stick(2*time.Second, 0, 0)
	m.stick(2100*ms, full, 0)
	m.stick(3100*ms, 0, 0)
	m.at(4 * time.Second)
	dx, _ := m.total()
	// The pointer had moved up to the last tick before the pause: the 4 ms
	// since are dropped with the pause.
	near(t, "dx", dx, 500+1000, 8)
}

// A stick that is not pushed when the engine is paused moves the pointer when
// it is pushed after the resume.
func TestMousePausedWhileCentered(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.e.Pause(0)
	m.stick(0, full, 0)
	m.at(500 * ms)
	if len(m.out.moves) != 0 {
		t.Fatalf("the pointer moved while paused: %+v", m.out.moves)
	}
	m.stick(500*ms, 0, 0)
	m.e.Resume()
	m.stick(600*ms, full, 0)
	m.stick(1600*ms, 0, 0)
	m.at(3 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 1000, 1)
}

// CancelAll (ReleaseAll) and Reset (a device that goes) stop the pointer the
// same way. A Reload does not (TestMouseReloadKeepsMoving).
func TestMouseStopsAtEverySafetyBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(m *msim)
	}{
		{"cancel", func(m *msim) { m.e.CancelAll(m.now) }},
		{"reset", func(m *msim) { m.e.Reset(m.now) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMsim(t, mouseAnalog)
			m.stick(0, full, 0)
			m.at(500 * ms)
			tc.stop(m)
			n := len(m.out.moves)
			if _, ok := m.e.NextDeadline(); ok {
				t.Error("a deadline is left")
			}
			m.at(2 * time.Second)
			if len(m.out.moves) != n {
				t.Errorf("%d moves after the stop", len(m.out.moves)-n)
			}
		})
	}
}

// After Reset the stick has not been seen: a stick pushed afterwards moves the
// pointer at once.
func TestMouseResetThenPush(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	m.at(100 * ms)
	m.e.Reset(m.now)
	n := len(m.out.moves)
	m.stick(200*ms, full, 0)
	m.stick(1200*ms, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	near(t, "dx after the reset", dx-100, 1000, 8)
	if len(m.out.moves) <= n {
		t.Error("no move after the reset")
	}
}

// After a CancelAll the stick that is still pushed stays locked until it is back
// inside the deadzone: the edge of the deadzone is the release, not the
// directionThreshold, and a stick that stays outside it moves nothing.
func TestMouseLockReleasesInsideTheDeadzone(t *testing.T) {
	m := newMsim(t, `"mode":"mouse","mouseSpeed":1000,"deadzone":0.2,"saturation":1`)
	m.stick(0, full, 0)
	m.at(100 * ms)
	m.e.CancelAll(m.now)
	n := len(m.out.moves)
	before, _ := m.total()
	m.stick(200*ms, 9830, 0) // 0.3: outside the deadzone, under the threshold
	m.stick(300*ms, full, 0)
	m.stick(400*ms, 5000, 0) // 0.15: inside it
	m.at(500 * ms)
	if len(m.out.moves) != n {
		t.Fatalf("%d moves while the stick was locked", len(m.out.moves)-n)
	}
	m.stick(500*ms, 9830, 0) // outside again: the pointer moves
	m.stick(600*ms, 0, 0)
	m.at(time.Second)
	dx, _ := m.total()
	// 100 ms at depth (0.3-0.2)/0.8 = 0.125.
	near(t, "dx after the unlock", dx-before, 12.5, 1)
}

// A reload that changes the mode ends the motion for good.
func TestMouseReloadModes(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, full, 0)
	m.at(500 * ms)
	m.e.Reload(m.now, compileAnalog(t, `{"ls":{"mode":"standard"}}`, holdKey("ls.right", "R")))
	n := len(m.out.moves)
	m.at(time.Second)
	m.stick(time.Second, 0, 0)
	m.stick(1100*ms, full, 0)
	m.at(2 * time.Second)
	if len(m.out.moves) != n {
		t.Errorf("the pointer moved in mode standard")
	}
}

// A reload (an autosave of the profile, a hot reload, an automatic profile
// switch) does not freeze a stick that is still pushed: the pointer goes on with
// the settings of the new program, at its new speed and curve, without the stick
// being let go first.
func TestMouseReloadKeepsMoving(t *testing.T) {
	for _, tc := range []struct {
		name   string
		raw    int16  // the push, from 0 to the reload at 500 ms and to 1000 ms
		reload string // the analog settings of the new program
		want   float64
	}{
		{"faster", full, `"mode":"mouse","mouseSpeed":2000,"deadzone":0,"saturation":1`, 500 + 1000},
		{"slower", full, `"mode":"mouse","mouseSpeed":200,"deadzone":0,"saturation":1`, 500 + 100},
		// Half the travel is 500 px/s; the new curve makes it a quarter of that.
		{"a new curve", 16384, `"mode":"mouse","mouseSpeed":1000,"deadzone":0,"saturation":1,"curve":"quadratic"`, 250 + 125},
		// The speed is 500 px/s all along; the new smoothing glides on for 100 ms
		// of it after the release, from the speed the pointer had.
		{"smoothing", 16384, `"mode":"mouse","mouseSpeed":1000,"deadzone":0,"saturation":1,"mouseSmoothingMs":100`, 250 + 250 + 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMsim(t, mouseAnalog)
			m.stick(0, tc.raw, 0)
			m.at(500 * ms)
			m.e.Reload(m.now, compileAnalog(t, `{"ls":{`+tc.reload+`}}`, ""))
			if _, ok := m.e.NextDeadline(); !ok {
				t.Fatal("the pointer has no deadline after the reload: it stopped")
			}
			n := len(m.out.moves)
			m.at(600 * ms)
			if len(m.out.moves) == n {
				t.Fatal("the pointer did not move after the reload")
			}
			m.stick(time.Second, 0, 0)
			m.at(5 * time.Second)
			dx, _ := m.total()
			near(t, "dx", dx, tc.want, 2)
			if _, ok := m.e.NextDeadline(); ok {
				t.Error("the pointer never stopped")
			}
			balanced(t, m.out.recorder)
		})
	}
}

// A Reload does not unlock a stick that was locked before it (a pause, a
// ReleaseAll): it has to come back inside the deadzone first. A Reload of a paused
// engine moves nothing either, and after the resume the stick still has to.
func TestMouseReloadKeepsTheLock(t *testing.T) {
	again := func() *engine.Program { return compileAnalog(t, `{"ls":{`+mouseAnalog+`}}`, "") }
	t.Run("locked by a cancel", func(t *testing.T) {
		m := newMsim(t, mouseAnalog)
		m.stick(0, full, 0)
		m.at(100 * ms)
		m.e.CancelAll(m.now)
		n := len(m.out.moves)
		m.e.Reload(m.now, again())
		if _, ok := m.e.NextDeadline(); ok {
			t.Error("a Reload started the pointer of a locked stick")
		}
		m.stick(300*ms, -full, 0)
		m.at(time.Second)
		if len(m.out.moves) != n {
			t.Errorf("%d moves while the stick was locked", len(m.out.moves)-n)
		}
		m.stick(time.Second, 0, 0)
		m.stick(1100*ms, full, 0)
		m.stick(1200*ms, 0, 0)
		m.at(2 * time.Second)
		if len(m.out.moves) == n {
			t.Error("the pointer did not move after the stick was let go")
		}
	})
	t.Run("paused", func(t *testing.T) {
		m := newMsim(t, mouseAnalog)
		m.stick(0, full, 0)
		m.at(100 * ms)
		m.e.Pause(m.now)
		n := len(m.out.moves)
		m.e.Reload(m.now, again())
		if _, ok := m.e.NextDeadline(); ok {
			t.Error("a Reload of a paused engine started the pointer")
		}
		m.at(500 * ms)
		m.e.Resume()
		m.stick(600*ms, full, -full) // still pushed: still locked
		m.at(time.Second)
		if len(m.out.moves) != n {
			t.Errorf("%d moves while paused or locked", len(m.out.moves)-n)
		}
	})
}

// A Reload ends the precision held before it, as it ends any held action, and
// the stick that goes on moves at its full speed.
func TestMouseReloadEndsPrecisionAndKeepsMoving(t *testing.T) {
	prec := `"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}`
	m := newMsim(t, mouseAnalog, prec)
	m.button(0, "lb", true)
	m.stick(0, full, 0)
	m.at(500 * ms)
	m.e.Reload(m.now, compileAnalog(t, `{"ls":{`+mouseAnalog+`}}`, prec))
	m.stick(time.Second, 0, 0)
	m.at(3 * time.Second)
	dx, _ := m.total()
	// 500 ms at half speed, 500 ms at full: the button is still down, but the
	// reload canceled what it held.
	near(t, "dx", dx, 250+500, 2)
}

// A calibration change (IN-14) keeps a pushed stick moving the pointer, with the
// new deadzone and saturation.
func TestMouseCalibrationKeepsMoving(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	m.stick(0, 16384, 0) // half the travel: 500 px/s
	m.at(500 * ms)
	sat := 0.5
	m.e.SetCalibration(m.now, &profile.Calibration{LS: &profile.StickCalibration{Saturation: &sat}})
	if _, ok := m.e.NextDeadline(); !ok {
		t.Fatal("the pointer has no deadline after the calibration: it stopped")
	}
	m.stick(time.Second, 0, 0)
	m.at(3 * time.Second)
	dx, _ := m.total()
	// 500 ms at 500 px/s, then the same push is the whole travel: 1000 px/s.
	near(t, "dx", dx, 250+500, 2)
	// Back to the program's values while pushed.
	m = newMsim(t, mouseAnalog)
	m.e.SetCalibration(0, &profile.Calibration{LS: &profile.StickCalibration{Saturation: &sat}})
	m.stick(0, 16384, 0) // the whole travel with the calibration: 1000 px/s
	m.at(500 * ms)
	m.e.SetCalibration(m.now, nil)
	m.stick(time.Second, 0, 0)
	m.at(3 * time.Second)
	dx, _ = m.total()
	near(t, "dx", dx, 500+250, 2)
}

// With no one reading the output (an input preview) a stick in mode mouse sends
// nothing and has no deadline.
func TestMouseInputPreview(t *testing.T) {
	m := newMsim(t, mouseAnalog)
	p := m.e.PreviewInput(nil)
	p.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftX, Value: full})
	p.Step(engine.Input{T: clock.Time(ms), Kind: engine.InputAxis, Axis: control.LeftY, Value: 0})
	p.Apply(clock.Time(time.Second))
	if len(m.out.moves) != 0 {
		t.Error("the preview moved the pointer")
	}
}

// Two sticks in mode mouse move the pointer by their sum.
func TestMouseTwoSticks(t *testing.T) {
	m := newMsimAnalog(t, `{"ls":{`+mouseAnalog+`},"rs":{"mode":"mouse","mouseSpeed":500,"deadzone":0,"saturation":1}}`)
	m.stick(0, full, 0)
	m.at(0)
	m.e.Step(engine.Input{T: 0, Kind: engine.InputAxis, Axis: control.RightY, Value: full})
	m.stick(time.Second, 0, 0)
	m.e.Step(engine.Input{T: m.now, Kind: engine.InputAxis, Axis: control.RightY, Value: 0})
	m.at(2 * time.Second)
	dx, dy := m.total()
	near(t, "dx", dx, 1000, 1)
	near(t, "dy", dy, 500, 1)
}

// Precision slows the pointer to its factor for as long as the control is
// held.
func TestMousePrecisionPress(t *testing.T) {
	m := newMsim(t, mouseAnalog, `"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}`)
	m.stick(0, full, 0)
	m.button(400*ms, "lb", true)
	m.button(800*ms, "lb", false)
	m.stick(1000*ms, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	// 0.4 s full, 0.4 s half, 0.2 s full.
	near(t, "dx", dx, 400+200+200, 1)
	if _, ok := m.e.NextDeadline(); ok {
		t.Error("a deadline is left")
	}
	// The decision shows the action.
}

// The default factor is 0.3.
func TestMousePrecisionDefaultFactor(t *testing.T) {
	m := newMsim(t, mouseAnalog, `"lb":{"press":{"type":"mouse","op":"precision"}}`)
	m.button(0, "lb", true)
	m.stick(0, full, 0)
	m.stick(time.Second, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 300, 1)
}

// With several held at once the smallest factor wins; letting one go returns
// to the next.
func TestMousePrecisionSmallestWins(t *testing.T) {
	m := newMsim(t, mouseAnalog,
		`"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}`,
		`"rb":{"press":{"type":"mouse","op":"precision","factor":0.2}}`)
	m.stick(0, full, 0)
	m.button(100*ms, "lb", true)
	m.button(200*ms, "rb", true)
	m.button(300*ms, "lb", false) // 0.2 still holds
	m.button(400*ms, "rb", false)
	m.stick(500*ms, 0, 0)
	m.at(time.Second)
	dx, _ := m.total()
	// 100 full, 100 at 0.5, 200 at 0.2 (the two overlap and the smaller wins),
	// 100 full.
	near(t, "dx", dx, 100+50+40+100, 1)
}

// A hold gesture holds the factor once it is recognized, and not before.
func TestMousePrecisionHold(t *testing.T) {
	m := newMsim(t, mouseAnalog, `"lb":{"tap":{"type":"keys","keys":"T","mode":"tap"},"hold":{"type":"mouse","op":"precision","factor":0.5}}`)
	m.stick(0, full, 0)
	m.button(100*ms, "lb", true) // the hold is recognized at 100 + 250 ms
	m.button(850*ms, "lb", false)
	m.stick(1000*ms, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	// 350 full, 500 half (to 850), 150 full.
	near(t, "dx", dx, 350+250+150, 1)
}

// A tap of the same binding does not slow anything.
func TestMousePrecisionHoldNotReachedByATap(t *testing.T) {
	m := newMsim(t, mouseAnalog, `"lb":{"tap":{"type":"keys","keys":"T","mode":"tap"},"hold":{"type":"mouse","op":"precision","factor":0.5}}`)
	m.stick(0, full, 0)
	m.button(100*ms, "lb", true)
	m.button(150*ms, "lb", false)
	m.stick(1000*ms, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 1000, 1)
}

// With smoothing the factor is eased in and out like any change of target:
// the distance is that of the continuous system.
func TestMousePrecisionWithSmoothing(t *testing.T) {
	m := newMsim(t, mouseAnalog+`,"mouseSmoothingMs":100`, `"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}`)
	m.stick(0, full, 0)
	m.button(time.Second, "lb", true)
	m.stick(2*time.Second, 0, 0)
	m.at(10 * time.Second)
	dx, _ := m.total()
	tau := 0.1
	v, d := 0.0, 0.0
	step := func(u, dt float64) {
		k := math.Exp(-dt / tau)
		d += u*dt + (v-u)*tau*(1-k)
		v = u + (v-u)*k
	}
	step(1000, 1)
	step(500, 1)
	step(0, 10)
	near(t, "total", dx, d, 1)
	// The area is 1000 px for the first second and 500 for the second.
	near(t, "total", dx, 1500, 1)
}

// Precision ends with the control's cycle on every path that ends a held
// action: the release, a cancel, a pause, a reload and a reset (a device that
// goes).
func TestMousePrecisionEndsOnEveryExit(t *testing.T) {
	prec := `"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}`
	for _, tc := range []struct {
		name string
		end  func(m *msim)
	}{
		{"release", func(m *msim) { m.button(m.at0(), "lb", false) }},
		{"cancel", func(m *msim) { m.e.CancelAll(m.now) }},
		{"pause", func(m *msim) { m.e.Pause(m.now); m.e.Resume() }},
		{"reload", func(m *msim) { m.e.Reload(m.now, compileAnalog(t, `{"ls":{`+mouseAnalog+`}}`, prec)) }},
		{"reset", func(m *msim) { m.e.Reset(m.now) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMsim(t, mouseAnalog, prec)
			m.button(0, "lb", true)
			m.stick(0, full, 0)
			m.at(500 * ms)
			tc.end(m)
			// Let go, and push for a second: the full speed, not the half.
			m.stick(m.at0(), 0, 0)
			from := m.at0() + 100*ms
			m.at(from)
			before, _ := m.total()
			m.stick(from, full, 0)
			m.stick(from+time.Second, 0, 0)
			m.at(from + 2*time.Second)
			dx, _ := m.total()
			near(t, "dx of the push after "+tc.name, dx-before, 1000, 1)
		})
	}
}

// at0 is the current time as a duration.
func (m *msim) at0() time.Duration { return time.Duration(m.now) }

// A precision held by a control of a layer lasts until that control is
// released, also when the layer is gone by then, like the keys a control holds.
func TestMousePrecisionSurvivesLayerChange(t *testing.T) {
	src := `{"schemaVersion":1,"name":"t","analog":{"ls":{` + mouseAnalog + `}},"layers":{` +
		`"base":{"rb":{"press":{"type":"layer","op":"hold","layer":"slow"}},"lb":{"press":{"type":"keys","keys":"Z"}}},` +
		`"slow":{"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	out := &mouseOut{recorder: &recorder{}}
	m := &msim{t: t, e: engine.New(prog, out, engine.Options{}), out: out}
	m.stick(0, full, 0)
	m.button(100*ms, "rb", true)  // the slow layer is on
	m.button(200*ms, "lb", true)  // the precision of the slow layer
	m.button(300*ms, "rb", false) // the layer is gone, the precision is not
	m.button(600*ms, "lb", false)
	m.stick(1000*ms, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	near(t, "dx", dx, 200+200+400, 1)
}

// The latch keeps a precision until the control is pressed again.
func TestMousePrecisionLatch(t *testing.T) {
	m := newMsim(t, mouseAnalog, `"lb":{"latch":true,"press":{"type":"mouse","op":"precision","factor":0.5}}`)
	m.stick(0, full, 0)
	m.button(200*ms, "lb", true)
	m.button(300*ms, "lb", false) // latched: still half
	m.button(600*ms, "lb", true)  // the press that ends the latch (and starts nothing)
	m.button(700*ms, "lb", false)
	m.stick(1000*ms, 0, 0)
	m.at(2 * time.Second)
	dx, _ := m.total()
	// 200 full; 200..600 half (latched); the second press cancels the latch: it
	// is a press that only ends it (engine behavior of latch), then full to 1000.
	near(t, "dx", dx, 200+200+400, 1)
}

// The trace shows a precision action as "mouse precision 0.5".
func TestMousePrecisionDecision(t *testing.T) {
	var got []string
	prog := compileAnalog(t, `{"ls":{`+mouseAnalog+`}}`, `"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}}`)
	out := &mouseOut{recorder: &recorder{}}
	e := engine.New(prog, out, engine.Options{Trace: func(d engine.Decision) {
		if d.Action != nil {
			got = append(got, d.Action.String())
		}
	}})
	lb, _ := control.Parse("lb")
	e.Step(engine.Input{Kind: engine.InputButton, Control: lb, Down: true})
	if len(got) != 1 || got[0] != "mouse precision 0.5" {
		t.Errorf("decisions %v", got)
	}
}

// A jump action puts the pointer at its position, once, for any gesture.
func TestMouseJump(t *testing.T) {
	m := newMsim(t, mouseAnalog,
		`"south":{"press":{"type":"mouse","op":"jump","x":0.5,"y":0.25}}`,
		`"east":{"tap":{"type":"mouse","op":"jump","x":0,"y":1}}`)
	m.button(10*ms, "south", true)
	m.button(20*ms, "south", false)
	m.button(30*ms, "east", true)
	m.button(40*ms, "east", false)
	m.at(time.Second)
	want := []moveEvent{
		{t: clock.Time(10 * ms), dx: 32768, dy: 16384, owner: engine.OwnerID{Source: engine.SourceDecl, Cycle: 1}},
		{t: clock.Time(40 * ms), dx: 0, dy: 65535, owner: engine.OwnerID{Source: engine.SourceDecl, Cycle: 2}},
	}
	if fmt.Sprint(m.out.jumps) != fmt.Sprint(want) {
		t.Errorf("jumps %+v\nwant %+v", m.out.jumps, want)
	}
	if len(m.out.moves) != 0 {
		t.Errorf("a jump sent moves: %+v", m.out.moves)
	}
}

// While paused a jump does nothing, like any action.
func TestMouseJumpPaused(t *testing.T) {
	m := newMsim(t, mouseAnalog, `"south":{"press":{"type":"mouse","op":"jump","x":0.5,"y":0.5}}`)
	m.e.Pause(0)
	m.button(10*ms, "south", true)
	m.button(20*ms, "south", false)
	if len(m.out.jumps) != 0 {
		t.Errorf("a jump while paused: %+v", m.out.jumps)
	}
}

// The hot path of the pointer allocates nothing: an axis input of a stick in
// mode mouse, its ticks, a precision press and release, and the stop, with and
// without smoothing and a curve.
func TestMouseNoAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	for _, ls := range []string{
		mouseAnalog,
		mouseAnalog + `,"mouseSmoothingMs":100,"curve":"quadratic"`,
	} {
		prog := compileAnalog(t, `{"ls":{`+ls+`}}`, `"lb":{"press":{"type":"mouse","op":"precision","factor":0.5}},"south":{"press":{"type":"mouse","op":"jump","x":0.5,"y":0.5}}`)
		out := &counter{}
		e := engine.New(prog, out, engine.Options{Trace: nil})
		var now clock.Time
		lb, _ := control.Parse("lb")
		axes := []int16{full, 20000, 12000, 0}
		n := testing.AllocsPerRun(200, func() {
			for _, v := range axes {
				now = now.Add(5 * time.Millisecond)
				e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.LeftX, Value: v})
				e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.LeftY, Value: -v / 2})
				for range 3 {
					now = now.Add(3 * time.Millisecond)
					e.Apply(now)
				}
			}
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: lb, Down: true})
			e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.LeftX, Value: full})
			e.Apply(now.Add(10 * time.Millisecond))
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: lb, Down: false})
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: control.South, Down: true})
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: control.South, Down: false})
			e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.LeftX, Value: 0})
			e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: control.LeftY, Value: 0})
			for range 40 {
				now = now.Add(8 * time.Millisecond)
				e.Apply(now)
			}
			e.NextDeadline()
		})
		if n != 0 {
			t.Errorf("%q: %v allocations", ls, n)
		}
		if out.moves == 0 || out.jumps == 0 {
			t.Errorf("%q: output %+v: the steps did not run", ls, out)
		}
	}
}
