package input

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"
)

// These tests need no SDL: they cover the pure logic of the receiver
// watchdog (IN-30) on made-up SDL tick times.

// testTiming is receiverTiming for a 1 s timeout: prove 500 ms, gap 125 ms.
func testTiming(t *testing.T) receiverTiming {
	t.Helper()
	tm, err := newReceiverTiming(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// noAxes is the rest position of every axis.
var noAxes [sdl.GAMEPAD_AXIS_COUNT]int16

func TestReceiverTiming(t *testing.T) {
	for _, tc := range []struct {
		in                         time.Duration
		timeout, prove, gap, stall time.Duration
		err                        string // a part of the error, or "" for none
	}{
		{in: 0},
		{in: 2 * time.Second, timeout: 2 * time.Second, prove: time.Second, gap: 250 * time.Millisecond, stall: 500 * time.Millisecond},
		{in: time.Second, timeout: time.Second, prove: 500 * time.Millisecond, gap: 125 * time.Millisecond, stall: 250 * time.Millisecond},
		{in: 100 * time.Millisecond, timeout: 100 * time.Millisecond, prove: 50 * time.Millisecond, gap: 12500 * time.Microsecond, stall: 25 * time.Millisecond},
		{in: 99 * time.Millisecond, err: "at least 100ms"},
		{in: time.Nanosecond, err: "at least 100ms"},
		{in: -time.Second, err: "at least 100ms"},
	} {
		got, err := newReceiverTiming(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("newReceiverTiming(%v) = %+v, %v; want an error with %q", tc.in, got, err, tc.err)
			}
			continue
		}
		want := receiverTiming{timeout: uint64(tc.timeout), prove: uint64(tc.prove), gap: uint64(tc.gap), stall: uint64(tc.stall)}
		if err != nil || got != want {
			t.Errorf("newReceiverTiming(%v) = %+v, %v; want %+v", tc.in, got, err, want)
		}
		if got.on() != (tc.in != 0) {
			t.Errorf("newReceiverTiming(%v).on() = %v", tc.in, got.on())
		}
	}
	if DefaultReceiverTimeout != 2*time.Second {
		t.Errorf("DefaultReceiverTimeout = %v, want 2s (owner decision 1 of IN-30)", DefaultReceiverTimeout)
	}
	if tm, _ := newReceiverTiming(minReceiverTimeout); tm.gap <= uint64(receiverWatchEvery) {
		t.Errorf("the gap %v of the shortest timeout is not above the watch %v", time.Duration(tm.gap), receiverWatchEvery)
	}
}

// dogStep is a step of a watchdog script: a heartbeat, idle or not, a
// button event or an axis event at a time.
type dogStep struct {
	at     int // ms
	kind   byte
	leftx  int16
	expect bool // for 'b' and 'c': beat reports that it armed the watchdog now
}

// runDog runs steps on a watchdog started at 0 and returns it. A 'b' step
// is an idle heartbeat, 'c' a heartbeat whose content changed, 'p' a
// button event, 'x' an axis event that moves leftx, 's' a simple report
// (0x3F), and 'w' a watch, which checks that switched reports expect.
func runDog(t *testing.T, steps []dogStep) *watchdog {
	t.Helper()
	w := &watchdog{tm: testTiming(t)}
	w.start(0)
	return stepDog(t, w, steps)
}

// stepDog runs steps on w, as runDog does, and returns it.
func stepDog(t *testing.T, w *watchdog, steps []dogStep) *watchdog {
	t.Helper()
	axes := noAxes
	for i, s := range steps {
		at := ms(uint64(s.at))
		switch s.kind {
		case 'b', 'c':
			if got := w.beat(at, s.kind == 'b', axes); got != s.expect {
				t.Fatalf("step %d: beat at %d ms = %v, want %v", i, s.at, got, s.expect)
			}
		case 'p':
			w.control(at, true, axes)
		case 'x':
			axes[sdl.GAMEPAD_AXIS_LEFTX] = s.leftx
			w.control(at, false, axes)
		case 's':
			w.otherMode()
		case 'w':
			if got := w.switched(at); got != s.expect {
				t.Fatalf("step %d: switched at %d ms = %v, want %v", i, s.at, got, s.expect)
			}
		}
	}
	return w
}

// beats returns an idle heartbeat every 10 ms from from to to, inclusive,
// with expect set on the one at arm.
func beats(from, to, arm int) []dogStep { return beatsEvery(10, 'b', from, to, arm) }

// beatsEvery returns a heartbeat of kind, 'b' or 'c', every step ms from
// from to to, inclusive, with expect set on the one at arm.
func beatsEvery(step int, kind byte, from, to, arm int) []dogStep {
	var s []dogStep
	for at := from; at <= to; at += step {
		s = append(s, dogStep{at: at, kind: kind, expect: at == arm})
	}
	return s
}

// changing returns, every 10 ms from from to to, inclusive, a heartbeat
// whose content changed, and an axis event inside the band: a device that
// reports only when its input changes, with a thumb that rests on a stick.
func changing(from, to int) []dogStep {
	var s []dogStep
	for at := from; at <= to; at += 10 {
		s = append(s, dogStep{at: at, kind: 'x', leftx: int16(100 * (at / 10 % 2))}, dogStep{at: at, kind: 'c'})
	}
	return s
}

func cat(parts ...[]dogStep) []dogStep {
	var s []dogStep
	for _, p := range parts {
		s = append(s, p...)
	}
	return s
}

// merge returns the steps of parts in time order, those at the same time
// in the order of parts.
func merge(parts ...[]dogStep) []dogStep {
	s := cat(parts...)
	slices.SortStableFunc(s, func(a, b dogStep) int { return cmp.Compare(a.at, b.at) })
	return s
}

// TestWatchdogArmsAtRest: idle heartbeats at rest arm the watchdog
// exactly prove after the run began, not before. A gap between idle
// heartbeats longer than gap, a button event, or an axis that leaves the
// band around the run's rest restarts the run. Jitter inside the band does
// not. Heartbeats whose content changed prove nothing: a device that sends
// only those never arms, even when every change is inside the band. Once
// armed, control events do not disarm it.
func TestWatchdogArmsAtRest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []dogStep
		armed bool
	}{
		{"arms at prove", beats(0, 500, 500), true},
		{"not yet", beats(0, 490, -1), false},
		{"run starts at the first beat", beats(100, 600, 600), true},
		{"gap restarts", cat(beats(0, 200, -1), beats(460, 960, 960)), true},
		{"gap of exactly gap keeps the run", cat(beats(0, 200, -1), beats(325, 505, 505)), true},
		{"gap beyond gap restarts", cat(beats(0, 200, -1), beats(326, 826, 826)), true},
		{"button restarts", cat(beats(0, 300, -1), []dogStep{{at: 305, kind: 'p'}}, beats(310, 810, 810)), true},
		{"axis beyond the band restarts", cat(beats(0, 300, -1), []dogStep{{at: 305, kind: 'x', leftx: receiverBand + 1}}, beats(310, 810, 810)), true},
		{"jitter inside the band", cat(beats(0, 300, -1), []dogStep{{at: 305, kind: 'x', leftx: receiverBand}, {at: 306, kind: 'x', leftx: -receiverBand}}, beats(310, 500, 500)), true},
		{"moving never arms", func() []dogStep {
			var s []dogStep
			for at := 0; at < 3000; at += 10 {
				s = append(s, dogStep{at: at, kind: 'b'}, dogStep{at: at + 5, kind: 'x', leftx: int16(20000 * (at/10%2*2 - 1))})
			}
			return s
		}(), false},
		{"control after arming keeps it armed", cat(beats(0, 500, 500), []dogStep{{at: 505, kind: 'p'}, {at: 506, kind: 'x', leftx: 30000}}, beats(510, 2000, -1)), true},
		{"changes only never arm", changing(0, 5000), false},
		{"changing heartbeats only never arm", beatsEvery(5, 'c', 0, 5000, -1), false},
		{"idle heartbeats a gap apart among changes", merge(changing(0, 1000), beatsEvery(125, 'b', 0, 500, 500)), true},
		{"idle heartbeats too far apart among changes", merge(changing(0, 1000), beatsEvery(130, 'b', 0, 1000, -1)), false},
		{"changes do not end the run", cat(beats(0, 200, -1), changing(205, 300), beats(310, 500, 500)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := runDog(t, tc.steps)
			if w.armed != tc.armed {
				t.Errorf("armed = %v, want %v", w.armed, tc.armed)
			}
		})
	}
}

// TestWatchdogLost: lost holds only for an armed watchdog, from exactly
// timeout after the later of the last heartbeat and the last control event
// or grace, whether or not a control is held. start disarms it.
func TestWatchdogLost(t *testing.T) {
	armed := func(t *testing.T) *watchdog { return runDog(t, beats(0, 500, 500)) }
	for _, tc := range []struct {
		name string
		dog  func(t *testing.T) *watchdog
		at   int
		lost bool
	}{
		{"at the timeout", armed, 1500, true},
		{"just before", armed, 1499, false},
		{"long after", armed, 60000, true},
		{"not armed", func(t *testing.T) *watchdog { return runDog(t, beats(0, 400, -1)) }, 60000, false},
		{"before the last beat", armed, 400, false},
		{"at the last beat", armed, 500, false},
		{"held button", func(t *testing.T) *watchdog {
			return runDog(t, cat(beats(0, 500, 500), []dogStep{{at: 510, kind: 'p'}}))
		}, 1510, true},
		{"control pushes last", func(t *testing.T) *watchdog {
			return runDog(t, cat(beats(0, 500, 500), []dogStep{{at: 900, kind: 'p'}}))
		}, 1500, false},
		{"after the control", func(t *testing.T) *watchdog {
			return runDog(t, cat(beats(0, 500, 500), []dogStep{{at: 900, kind: 'p'}}))
		}, 1900, true},
		{"a changed heartbeat pushes last", func(t *testing.T) *watchdog {
			return runDog(t, cat(beats(0, 500, 500), []dogStep{{at: 900, kind: 'c'}}))
		}, 1899, false},
		{"after the changed heartbeat", func(t *testing.T) *watchdog {
			return runDog(t, cat(beats(0, 500, 500), []dogStep{{at: 900, kind: 'c'}}))
		}, 1900, true},
		{"grace pushes last", func(t *testing.T) *watchdog {
			w := armed(t)
			w.grace(ms(1200))
			return w
		}, 2199, false},
		{"after the grace", func(t *testing.T) *watchdog {
			w := armed(t)
			w.grace(ms(1200))
			return w
		}, 2200, true},
		{"an older grace does nothing", func(t *testing.T) *watchdog {
			w := armed(t)
			w.grace(ms(100))
			return w
		}, 1500, true},
		{"start disarms", func(t *testing.T) *watchdog {
			w := armed(t)
			w.start(ms(600))
			return w
		}, 60000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.dog(t).lost(ms(uint64(tc.at))); got != tc.lost {
				t.Errorf("lost(%d ms) = %v, want %v", tc.at, got, tc.lost)
			}
		})
	}
}

// TestWatchdogSwitched: an armed watchdog disarms once a simple report
// (0x3F) has come after its last heartbeat and no heartbeat has come for
// gap, and so is never lost however long it is silent after; it proves
// again when the heartbeat is back. Simple reports among heartbeats, or
// before the last one, do not disarm it, nor do they an unarmed one.
func TestWatchdogSwitched(t *testing.T) {
	armed := beats(0, 500, 500) // the last heartbeat at 500, gap 125 ms
	for _, tc := range []struct {
		name  string
		steps []dogStep
		armed bool
		// lostAt is when lost is checked after the steps: it holds when armed.
		lostAt int
	}{
		{"simple reports disarm after gap", cat(armed, []dogStep{
			{at: 505, kind: 's'}, {at: 510, kind: 'p'}, {at: 520, kind: 's'},
			{at: 624, kind: 'w'}, {at: 625, kind: 'w', expect: true}, {at: 700, kind: 'w'},
		}), false, 60_000},
		{"one simple report is enough", cat(armed, []dogStep{{at: 501, kind: 's'}, {at: 2000, kind: 'w', expect: true}}), false, 60_000},
		{"simple reports among heartbeats", cat(armed, merge(beatsEvery(10, 's', 505, 1000, -1), beats(510, 1000, -1)),
			[]dogStep{{at: 1124, kind: 'w'}}), true, 2000},
		{"a simple report before the last heartbeat", cat(beats(0, 490, -1), []dogStep{{at: 495, kind: 's'}}, beats(500, 500, 500),
			[]dogStep{{at: 1400, kind: 'w'}}), true, 1500},
		{"not armed", cat(beats(0, 400, -1), []dogStep{{at: 405, kind: 's'}, {at: 1000, kind: 'w'}}), false, 60_000},
		{"proves again", cat(armed, []dogStep{{at: 505, kind: 's'}, {at: 700, kind: 'w', expect: true}},
			beats(1000, 1500, 1500), []dogStep{{at: 1600, kind: 'w'}}), true, 2500},
		{"disarms again", cat(armed, []dogStep{{at: 505, kind: 's'}, {at: 700, kind: 'w', expect: true}},
			beats(1000, 1500, 1500), []dogStep{{at: 1505, kind: 's'}, {at: 1625, kind: 'w', expect: true}}), false, 60_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := runDog(t, tc.steps)
			if w.armed != tc.armed {
				t.Errorf("armed = %v, want %v", w.armed, tc.armed)
			}
			if got := w.lost(ms(uint64(tc.lostAt))); got != tc.armed {
				t.Errorf("lost(%d ms) = %v, want %v", tc.lostAt, got, tc.armed)
			}
		})
	}
}

// TestWatchdogGraceOnce: a silence gets one grace at most. A second grace
// with neither a heartbeat nor a control event since the first is
// refused, so stalls that come again and again cannot keep a silent
// device; a heartbeat or a control event ends the silence, and start
// begins a new life.
func TestWatchdogGraceOnce(t *testing.T) {
	armed := func(t *testing.T) *watchdog { return runDog(t, beats(0, 500, 500)) }
	for _, tc := range []struct {
		name string
		// between runs after the first grace, at 1200 ms.
		between func(w *watchdog)
		second  bool // whether the grace at 1800 ms is granted
		lostAt  int  // ms: lost from here, not a millisecond before
	}{
		{"refused", func(*watchdog) {}, false, 2200},
		{"after a heartbeat", func(w *watchdog) { w.beat(ms(1300), false, noAxes) }, true, 2800},
		{"after a control event", func(w *watchdog) { w.control(ms(1300), true, noAxes) }, true, 2800},
		{"after start", func(w *watchdog) { w.start(ms(1300)); w.armed = true }, true, 2800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := armed(t)
			if !w.grace(ms(1200)) {
				t.Fatal("the first grace was refused")
			}
			tc.between(w)
			if got := w.grace(ms(1800)); got != tc.second {
				t.Fatalf("the second grace = %v, want %v", got, tc.second)
			}
			if w.lost(ms(uint64(tc.lostAt-1))) || !w.lost(ms(uint64(tc.lostAt))) {
				t.Errorf("lost(%d ms) = %v, lost(%d ms) = %v; want false, true",
					tc.lostAt-1, w.lost(ms(uint64(tc.lostAt-1))), tc.lostAt, w.lost(ms(uint64(tc.lostAt))))
			}
		})
	}
}

// TestWatchdogSteadyNeverLost is a property: an armed watchdog whose
// heartbeats come at most gap apart is never lost, checked every
// millisecond over ten minutes, first with no control event at all and
// then with a rare one, far fewer than one a timeout, so that only the
// heartbeats keep it. A healthy gamepad held still for minutes keeps its
// keys.
func TestWatchdogSteadyNeverLost(t *testing.T) {
	tm := testTiming(t)
	for _, tc := range []struct {
		name string
		// control is the chance of a control event in a millisecond, one in
		// control; 0 for none.
		control int
	}{
		{"heartbeats only", 0},
		{"rare control events", 20_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := runDog(t, beats(0, 500, 500))
			rng := rand.New(rand.NewPCG(30, 2026))
			axes := noAxes
			next := ms(500) + 1 + rng.Uint64N(tm.gap)
			controls := 0
			for now := ms(501); now < ms(600_000); now += ms(1) {
				for next <= now {
					// Idle or not, a heartbeat keeps the device.
					w.beat(next, rng.IntN(2) == 0, axes)
					next += 1 + rng.Uint64N(tm.gap)
				}
				if tc.control > 0 && rng.IntN(tc.control) == 0 {
					axes[rng.IntN(len(axes))] = int16(rng.IntN(65536) - 32768)
					w.control(now, rng.IntN(2) == 0, axes)
					controls++
				}
				if w.lost(now) {
					t.Fatalf("lost at %v, last beat %v", time.Duration(now), time.Duration(w.lastBeat))
				}
			}
			if !w.armed {
				t.Fatal("disarmed")
			}
			if tc.control > 0 && (controls == 0 || uint64(controls) > ms(600_000)/tm.timeout/4) {
				t.Fatalf("%d control events in ten minutes, want some, far fewer than one a timeout", controls)
			}
		})
	}
}

func TestWatchable(t *testing.T) {
	hid := func(f func(*DeviceInfo)) *DeviceInfo {
		info := &DeviceInfo{Vendor: 0x057e, Product: 0x2009, Driver: "hidapi", Path: `\\?\HID#VID_057E&PID_2009#8&1&0&0000#{4d1e55b2}`}
		if f != nil {
			f(info)
		}
		return info
	}
	virtual := func(vendor, product uint16) *DeviceInfo {
		return &DeviceInfo{Vendor: vendor, Product: product, Driver: "virtual", Virtual: true}
	}
	wired, wireless := sdl.JOYSTICK_CONNECTION_WIRED, sdl.JOYSTICK_CONNECTION_WIRELESS
	for _, tc := range []struct {
		name      string
		info      *DeviceInfo
		state     sdl.JoystickConnectionState
		heartbeat bool
		want      bool
	}{
		{"USB hidapi Pro protocol", hid(nil), wired, false, true},
		{"wireless", hid(nil), wireless, false, false},
		{"unknown state", hid(nil), sdl.JOYSTICK_CONNECTION_UNKNOWN, false, false},
		{"invalid state", hid(nil), sdl.JOYSTICK_CONNECTION_INVALID, false, false},
		{"no path", hid(func(i *DeviceInfo) { i.Path = "" }), wired, false, false},
		{"xinput", hid(func(i *DeviceInfo) { i.Driver = "xinput" }), wired, false, false},
		{"dinput", hid(func(i *DeviceInfo) { i.Driver = "dinput" }), wired, false, false},
		{"PS4 receiver", hid(func(i *DeviceInfo) { i.Vendor, i.Product = 0x054c, 0x09cc }), wired, false, false},
		{"Joy-Con", hid(func(i *DeviceInfo) { i.Product = 0x2006 }), wired, false, false},
		{"virtual with heartbeat", virtual(0x057e, 0x2009), wired, true, true},
		{"virtual without heartbeat", virtual(0x057e, 0x2009), wired, false, false},
		{"standard virtual with heartbeat", virtual(0, 0), wired, true, false},
	} {
		if got := watchable(tc.info, tc.state, tc.heartbeat); got != tc.want {
			t.Errorf("%s: watchable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
