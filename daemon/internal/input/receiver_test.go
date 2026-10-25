package input

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
)

// These tests need no SDL: they build a Source by hand, with fake
// heartbeat sources, and call T-input's receiver watchdog (IN-30)
// directly. receiver_sdl_test.go drives it through SDL.

// logBuffer collects the JSON records of a slog.Logger at debug level.
type logBuffer struct{ bytes.Buffer }

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// records returns the records with message msg, decoded.
func (b *logBuffer) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(b.String()) {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

// testSource returns a Source for these tests, with the watchdog on at a
// 1 s timeout and its log in lb.
func testSource(t *testing.T, lb *logBuffer) *Source {
	t.Helper()
	return &Source{
		events:   make(chan Event, 64),
		devices:  make(map[sdl.JoystickID]*device),
		virtuals: make(map[sdl.JoystickID]*Virtual),
		receiver: testTiming(t),
		log:      lb.logger(),
	}
}

// watchedDevice adds device id to s, watched with the heartbeat source
// b and started at start.
func watchedDevice(s *Source, id sdl.JoystickID, b *hidBeat, start uint64) *device {
	d := &device{id: id, pub: DeviceID(id), beat: b, dog: watchdog{tm: s.receiver}}
	d.dog.start(start)
	s.devices[id] = d
	s.watched++
	return d
}

// armDog arms the watchdog of d with heartbeats every 10 ms from from ms,
// and returns the time of the last one.
func armDog(t *testing.T, d *device, from int) uint64 {
	t.Helper()
	var at uint64
	for n := from; ; n += 10 {
		at = ms(uint64(n))
		if d.dog.beat(at, true, d.axes) {
			return at
		}
		if n > from+5000 {
			t.Fatal("the watchdog did not arm")
		}
	}
}

// silent is a heartbeat source that reads nothing.
func silent() *hidBeat { return &hidBeat{read: func([]byte) int { return 0 }} }

// drainAll returns the events on s.events.
func drainAll(s *Source) []Event {
	var evs []Event
	for {
		select {
		case e := <-s.events:
			evs = append(evs, e)
		default:
			return evs
		}
	}
}

// TestWatchReceiversNoEarlyCombo: the DeviceRemoved that the watchdog
// makes for device A takes the time safe, which fireDue has already been
// given, not now. So device B's combo, due after safe, does not fire
// early: B's release of a member, queued in SDL before now, still
// disarms it.
func TestWatchReceiversNoEarlyCombo(t *testing.T) {
	var lb logBuffer
	s := testSource(t, &lb)
	combos := 0
	s.onCombo = func() { combos++ }
	s.watch = NewComboWatch(func(info DeviceInfo) Combo {
		c := DefaultCombo(info, false)
		c.Hold = 100 * time.Millisecond
		return c
	})
	a := watchedDevice(s, 1, silent(), 0)
	last := armDog(t, a, 0)
	const b = 2
	s.emit(Event{Time: ms(10_000), Device: b, Kind: DeviceAdded, Info: &DeviceInfo{ID: b, Type: "standard", IsGamepad: true}})
	s.emit(Event{Time: ms(10_000), Device: b, Kind: ButtonDown, Control: "back"})
	s.emit(Event{Time: ms(10_000), Device: b, Kind: ButtonDown, Control: "start"})
	due, ok := s.watch.Due()
	if !ok || due != ms(10_100) {
		t.Fatalf("Due = %d, %v; want %d", due, ok, ms(10_100))
	}
	drainAll(s)

	safe := due - ms(1)
	if safe-last < s.receiver.timeout {
		t.Fatalf("the test's safe %d is not a timeout after the last beat %d", safe, last)
	}
	s.lastWatch = due // not a stall
	s.watchReceivers(due+ms(10), safe)
	evs := drainAll(s)
	if len(evs) != 1 || evs[0].Kind != DeviceRemoved || evs[0].Device != 1 || evs[0].Time != safe {
		t.Fatalf("events %+v, want A's DeviceRemoved at %d only", evs, safe)
	}
	if combos != 0 {
		t.Fatalf("OnCombo ran %d times", combos)
	}
	s.emit(Event{Time: due - ms(1)/2, Device: b, Kind: ButtonUp, Control: "back"})
	s.fireDue(due + ms(20))
	for _, e := range drainAll(s) {
		if e.Kind == ComboHeld {
			t.Fatalf("combo fired: %+v", e)
		}
	}
	if combos != 0 {
		t.Fatalf("OnCombo ran %d times", combos)
	}
}

// TestWatchReceiversStallAndDrainOrder: after T-input has not watched for
// longer than stall, a silent armed device gets a timeout of grace, once a
// silence, and the log says so; drained heartbeats count before the
// watchdog decides; watches closer than receiverWatchEvery do nothing;
// and with no device watched nothing is drained.
func TestWatchReceiversStallAndDrainOrder(t *testing.T) {
	timeout := testTiming(t).timeout
	every := uint64(receiverWatchEvery)

	t.Run("grace after a stall", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		d := watchedDevice(s, 1, silent(), 0)
		last := armDog(t, d, 0)
		s.lastWatch = last
		now := last + 3*timeout
		s.watchReceivers(now, now)
		if evs := drainAll(s); len(evs) != 0 || d.dormant {
			t.Fatalf("events %+v after the stall, want none", evs)
		}
		for at := now + every; at < now+timeout; at += every {
			s.watchReceivers(at, at)
			if evs := drainAll(s); len(evs) != 0 {
				t.Fatalf("events %+v at %v after the stall, want none", evs, time.Duration(at-now))
			}
		}
		s.watchReceivers(now+timeout, now+timeout)
		evs := drainAll(s)
		if len(evs) != 1 || evs[0].Kind != DeviceRemoved || evs[0].Time != now+timeout || !d.dormant {
			t.Fatalf("events %+v a timeout after the stall, want the DeviceRemoved at %d", evs, now+timeout)
		}
		r := lb.records(t, "receiver heartbeat grace")
		if len(r) != 1 || r[0]["level"] != "INFO" || r[0]["id"] != float64(1) || r[0]["granted"] != true || r[0]["stalled"] != "3s" {
			t.Errorf("grace records %v, want one info of id 1, granted, stalled 3s", r)
		}
	})

	// SDL may block T-input for seconds on every pump while a USB Switch
	// device is silent (ForceUSB, SDL_hidapi_switch.c): stalls that come
	// again and again get one grace, and the device is still lost.
	t.Run("one grace per silence", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		d := watchedDevice(s, 1, silent(), 0)
		last := armDog(t, d, 0)
		s.lastWatch = last
		step := s.receiver.stall + ms(1)
		first := last + step
		var removed []Event
		at := first
		for ; at < first+3*timeout && len(removed) == 0; at += step {
			s.watchReceivers(at, at)
			removed = drainAll(s)
		}
		at -= step
		if len(removed) != 1 || removed[0].Kind != DeviceRemoved || !d.dormant {
			t.Fatalf("events %+v after stalls until %v, want the DeviceRemoved", removed, time.Duration(at-first))
		}
		if at < first+timeout || at >= first+timeout+step {
			t.Errorf("removed %v after the first grace, want at the first watch from a timeout", time.Duration(at-first))
		}
		r := lb.records(t, "receiver heartbeat grace")
		if len(r) < 2 || r[0]["granted"] != true || r[0]["level"] != "INFO" || r[1]["granted"] != false || r[1]["level"] != "DEBUG" {
			t.Errorf("grace records %v, want the first granted at info, then refused at debug", r)
		}
	})

	// A device whose heartbeat goes on through the stalls is never lost,
	// and needs no grace.
	t.Run("stalls with heartbeats", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		timer, gave := byte(0), false
		d := watchedDevice(s, 1, &hidBeat{read: func(p []byte) int {
			if gave { // one report a drain
				gave = false
				return 0
			}
			gave, timer = true, timer+1
			p[0], p[1] = 0x30, timer
			return 64
		}}, 0)
		last := armDog(t, d, 0)
		s.lastWatch = last
		step := 3 * timeout / 2
		for at := last + step; at < last+20*timeout; at += step {
			s.watchReceivers(at, at)
			if evs := drainAll(s); len(evs) != 0 || d.dormant {
				t.Fatalf("events %+v at %v, want none", evs, time.Duration(at-last))
			}
		}
		if r := lb.records(t, "receiver heartbeat grace"); len(r) != 0 {
			t.Errorf("grace records %v, want none", r)
		}
	})

	// A grace goes to armed devices only: a new device's first watch may
	// come long after the last one, when no device was watched.
	t.Run("no grace unarmed", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		watchedDevice(s, 1, silent(), ms(60_000))
		s.lastWatch = ms(1000)
		s.watchReceivers(ms(60_010), ms(60_010))
		if r := lb.records(t, "receiver heartbeat grace"); len(r) != 0 {
			t.Errorf("grace records %v, want none", r)
		}
	})

	t.Run("heartbeats drained first", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		b := &hidBeat{}
		d := watchedDevice(s, 1, b, 0)
		last := armDog(t, d, 0)
		s.lastWatch = last
		timer := byte(0)
		b.read = func(p []byte) int {
			if timer == 3 {
				return 0
			}
			timer++
			p[0], p[1] = 0x30, timer
			return 64
		}
		now := last + 3*timeout
		s.watchReceivers(now, now)
		if evs := drainAll(s); len(evs) != 0 || d.dormant || d.dog.lastBeat != now {
			t.Fatalf("events %+v, lastBeat %d; want none and %d", evs, d.dog.lastBeat, now)
		}
	})

	t.Run("too soon", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		reads := 0
		d := watchedDevice(s, 1, &hidBeat{read: func([]byte) int { reads++; return 0 }}, 0)
		last := armDog(t, d, 0)
		s.lastWatch = last + timeout
		s.watchReceivers(last+timeout+every-1, last+timeout+every-1)
		if reads != 0 || len(drainAll(s)) != 0 {
			t.Fatalf("%d reads within receiverWatchEvery, want none and no event", reads)
		}
		s.watchReceivers(last+timeout+every, last+timeout+every)
		if reads != 1 || len(drainAll(s)) != 1 {
			t.Fatalf("%d reads at receiverWatchEvery, want 1 and the removal", reads)
		}
	})

	t.Run("none watched", func(t *testing.T) {
		var lb logBuffer
		s := testSource(t, &lb)
		reads := 0
		d := &device{id: 1, pub: 1, beat: &hidBeat{read: func([]byte) int { reads++; return 0 }}}
		s.devices[1] = d // watched stays 0
		s.watchReceivers(ms(60_000), ms(60_000))
		if reads != 0 || s.lastWatch != 0 {
			t.Fatalf("%d reads, lastWatch %d with no device watched; want none", reads, s.lastWatch)
		}
	})
}

// reporter is a heartbeat source that gives one full report a drain, with
// a new timer each time, until silent is set; change gives the first byte
// of the left stick of the n-th report.
type reporter struct {
	n            int
	gave, silent bool
	change       func(n int) byte
}

func (r *reporter) source() *hidBeat {
	return &hidBeat{read: func(p []byte) int {
		if r.silent || r.gave {
			r.gave = false
			return 0
		}
		r.gave = true
		r.n++
		clear(p[:64])
		p[0], p[1], p[2], p[6] = 0x30, byte(r.n), 0x90, r.change(r.n)
		return 64
	}}
}

// TestWatchReceiversChangesNeverArm: a device that reports only when its
// input changes never arms, even when every change is so small that the
// gamepad is at rest, and so is never removed when it falls silent, a
// control held (ADR-0029 decision 5). The same device whose reports repeat
// their input arms after prove.
func TestWatchReceiversChangesNeverArm(t *testing.T) {
	every := uint64(receiverWatchEvery)
	for _, tc := range []struct {
		name   string
		change func(n int) byte
		armed  bool
	}{
		{"every report changes", func(n int) byte { return byte(n % 2) }, false},
		{"reports repeat", func(int) byte { return 0x80 }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lb logBuffer
			s := testSource(t, &lb)
			r := &reporter{change: tc.change}
			d := watchedDevice(s, 1, r.source(), 0)
			at := uint64(0)
			for ; at < ms(10_000); at += every {
				if at%ms(10) == 0 {
					// A thumb that rests on the stick, inside the band.
					d.axes[sdl.GAMEPAD_AXIS_LEFTX] = int16(at / ms(10) % 2 * 300)
					d.dog.control(at, false, d.axes)
				}
				s.watchReceivers(at, at)
			}
			if d.dog.armed != tc.armed {
				t.Fatalf("armed = %v after 10 s, want %v", d.dog.armed, tc.armed)
			}
			d.dog.control(at, true, d.axes) // a button held from here
			r.silent = true
			var evs []Event
			for end := at + 5*s.receiver.timeout; at < end; at += every {
				s.watchReceivers(at, at)
				evs = append(evs, drainAll(s)...)
			}
			if removed := len(evs) == 1 && evs[0].Kind == DeviceRemoved; removed != tc.armed || len(evs) > 1 {
				t.Errorf("events %+v after the silence, want the removal %v", evs, tc.armed)
			}
		})
	}
}

// queued is a heartbeat source that reads the reports in q, in order.
type queued struct{ q [][]byte }

func (qs *queued) source() *hidBeat {
	return &hidBeat{read: func(p []byte) int {
		if len(qs.q) == 0 {
			return 0
		}
		n := copy(p, qs.q[0])
		qs.q = qs.q[1:]
		return n
	}}
}

// TestWatchReceiversModeChange: an armed device whose heartbeat gives way
// to simple reports (0x3F), one with each control event, has been put into
// another input mode. The watchdog disarms, logs it once, and does not
// report the device removed when it then holds a control still; it arms
// again once the heartbeat is back. Replies to commands (0x21, 0x81) in
// place of the heartbeat do not disarm it: a receiver may send them by
// itself once its gamepad has gone.
func TestWatchReceiversModeChange(t *testing.T) {
	every := uint64(receiverWatchEvery)
	for _, tc := range []struct {
		name    string
		report  []byte // what the device sends with each control event
		removed bool
	}{
		{"simple reports", []byte{0x3f, 0x00, 0x00, 0x08}, false},
		{"subcommand replies", []byte{0x21, 0x05, 0x8e, 0x80, 0x02}, true},
		{"USB replies", []byte{0x81, 0x01}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lb logBuffer
			s := testSource(t, &lb)
			qs := &queued{}
			d := watchedDevice(s, 1, qs.source(), 0)
			timer := byte(0)
			at := uint64(0)
			beatUntil := func(done func() bool) {
				t.Helper()
				for end := at + ms(5000); !done(); at += every {
					if at > end {
						t.Fatal("not armed after 5 s")
					}
					timer++
					qs.q = append(qs.q, full(0x30, timer, 0x90, 0))
					s.watchReceivers(at, at)
				}
			}
			beatUntil(func() bool { return d.dog.armed })
			// The heartbeat stops. The gamepad is used for 300 ms, a report
			// with each control event, then holds a button still.
			for end := at + ms(300); at < end; at += every {
				if at%ms(20) == 0 {
					d.dog.control(at, true, d.axes)
					qs.q = append(qs.q, tc.report)
				}
				s.watchReceivers(at, at)
			}
			var evs []Event
			for end := at + 5*s.receiver.timeout; at < end; at += every {
				s.watchReceivers(at, at)
				evs = append(evs, drainAll(s)...)
			}
			if removed := len(evs) == 1 && evs[0].Kind == DeviceRemoved; removed != tc.removed || len(evs) > 1 {
				t.Fatalf("events %+v after the silence, want the removal %v", evs, tc.removed)
			}
			r := lb.records(t, "receiver heartbeat mode changed")
			if tc.removed {
				if len(r) != 0 {
					t.Errorf("mode changed records %v, want none", r)
				}
				return
			}
			if len(r) != 1 {
				t.Fatalf("mode changed records %v, want one", r)
			}
			if ids, _ := r[0]["ids"].(string); r[0]["level"] != "INFO" || r[0]["id"] != float64(1) ||
				!strings.HasPrefix(ids, "30:"+strconv.Itoa(int(timer))+",21:0,3f:") || strings.Contains(ids, "3f:0") {
				t.Errorf("mode changed record %v, want an info of id 1, after the simple reports", r[0])
			}
			if d.dog.armed || d.dormant {
				t.Fatalf("armed %v, dormant %v; want neither", d.dog.armed, d.dormant)
			}
			beatUntil(func() bool { return d.dog.armed })
			if r := lb.records(t, "receiver heartbeat armed"); len(r) != 2 {
				t.Errorf("armed records %v, want two", r)
			}
		})
	}
}

// queuedAt is a slog.Handler that notes, for the message of each record,
// how many events wait on the Source's Events as it is logged.
type queuedAt struct {
	s    *Source
	seen map[string]int
}

func (h *queuedAt) Enabled(context.Context, slog.Level) bool { return true }

func (h *queuedAt) Handle(_ context.Context, r slog.Record) error {
	h.seen[r.Message] = len(h.s.events)
	return nil
}

func (h *queuedAt) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *queuedAt) WithGroup(string) slog.Handler      { return h }

// TestLoseEmitsFirst: lose sends the DeviceRemoved before it logs, so
// that its slot releases the keys however long the log takes (ADR-0029
// decision 11).
func TestLoseEmitsFirst(t *testing.T) {
	s := testSource(t, &logBuffer{})
	h := &queuedAt{s: s, seen: map[string]int{}}
	s.log = slog.New(h)
	d := watchedDevice(s, 1, silent(), 0)
	s.lose(d, ms(5000))
	if n, ok := h.seen["receiver silent: reported removed"]; !ok || n != 1 {
		t.Errorf("logged with %d events queued (logged %v), want the DeviceRemoved queued first", n, ok)
	}
	if evs := drainAll(s); len(evs) != 1 || evs[0].Kind != DeviceRemoved || !d.dormant {
		t.Errorf("events %+v, dormant %v; want one DeviceRemoved and dormant", evs, d.dormant)
	}
}

// TestWatchReceiversReadError: a heartbeat source that fails is closed
// and no longer watched; its device stays and is never removed by the
// watchdog.
func TestWatchReceiversReadError(t *testing.T) {
	var lb logBuffer
	s := testSource(t, &lb)
	closed := 0
	d := watchedDevice(s, 1, &hidBeat{read: func([]byte) int { return -1 }, closeFn: func() { closed++ }}, 0)
	armDog(t, d, 0)
	s.watchReceivers(ms(60_000), ms(60_000))
	if d.beat != nil || s.watched != 0 || closed != 1 || len(drainAll(s)) != 0 {
		t.Fatalf("beat %v, watched %d, closed %d; want the source closed and dropped, no event", d.beat, s.watched, closed)
	}
	if r := lb.records(t, "receiver heartbeat failed"); len(r) != 1 || r[0]["level"] != "WARN" {
		t.Errorf("receiver heartbeat failed records %v, want one warn", r)
	}
}

// TestOpenBeatUnavailable: a device whose heartbeat source does not open
// is not watched, and the log says why once; one that opens is watched.
// lose reports the public id removed, keeps the device, dormant, and
// stops its feedback; a revived device gets ids down from 2³²−1.
func TestOpenBeatUnavailable(t *testing.T) {
	info := func() *DeviceInfo {
		return &DeviceInfo{ID: 1, Name: "Nintendo Switch Pro Controller", Vendor: 0x057e, Product: 0x2009, Driver: "hidapi", Path: `\\?\HID#VID_057E&PID_2009#1`, IsGamepad: true}
	}
	for _, openErr := range []error{errNoHIDBinding, errors.New("access denied")} {
		var lb logBuffer
		s := testSource(t, &lb)
		s.openHID = func(string) (*hidBeat, error) { return nil, openErr }
		d := &device{id: 1, pub: 1}
		s.devices[1] = d
		s.openBeat(d, info(), sdl.JOYSTICK_CONNECTION_WIRED, ms(10))
		if d.beat != nil || s.watched != 0 {
			t.Errorf("%v: beat %v, watched %d; want not watched", openErr, d.beat, s.watched)
		}
		r := lb.records(t, "receiver heartbeat unavailable")
		if len(r) != 1 || r[0]["level"] != "INFO" || r[0]["err"] != openErr.Error() || r[0]["id"] != float64(1) {
			t.Errorf("%v: unavailable records %v, want one info with id and err", openErr, r)
		}
	}

	// Without the bindings the default opener fails with errNoHIDBinding.
	open, read, closeDev := hidOpenPath, hidReadTimeout, hidClose
	hidOpenPath, hidReadTimeout, hidClose = nil, nil, nil
	_, err := openHIDPath(`\\?\HID#VID_057E&PID_2009#1`)
	hidOpenPath, hidReadTimeout, hidClose = open, read, closeDev
	if !errors.Is(err, errNoHIDBinding) {
		t.Errorf("openHIDPath without bindings: %v, want errNoHIDBinding", err)
	}

	var lb logBuffer
	s := testSource(t, &lb)
	var opened string
	s.openHID = func(path string) (*hidBeat, error) { opened = path; return silent(), nil }
	d := &device{id: 7, pub: 7}
	s.devices[7] = d
	// A pulse train runs on it through the Mixer.
	var clk int
	rec := &recEffector{now: &clk, player: -1}
	s.effects().Add(7, rec, haptic.Caps{Rumble: true})
	if _, err := s.effects().Play(7, haptic.Ambient, haptic.Train(2, 70, 100, 0x7000), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	i := info()
	s.openBeat(d, i, sdl.JOYSTICK_CONNECTION_WIRED, ms(10))
	if d.beat == nil || s.watched != 1 || opened != i.Path || d.dog.lastControl != ms(10) {
		t.Fatalf("beat %v, watched %d, opened %q, lastControl %d; want watched from %d", d.beat, s.watched, opened, d.dog.lastControl, ms(10))
	}
	if r := lb.records(t, "receiver heartbeat watching"); len(r) != 1 || r[0]["id"] != float64(7) || r[0]["path"] != i.Path || r[0]["name"] != i.Name {
		t.Errorf("watching records %v, want one with id, name and path", r)
	}
	// Not watched: another protocol, or the watchdog off.
	for _, tc := range []struct {
		name string
		s    func() *Source
		info *DeviceInfo
	}{
		{"xinput", func() *Source { return testSource(t, &lb) }, func() *DeviceInfo { i := info(); i.Driver = "xinput"; return i }()},
		{"off", func() *Source { s := testSource(t, &lb); s.receiver = receiverTiming{}; return s }, info()},
	} {
		s := tc.s()
		s.openHID = func(string) (*hidBeat, error) { t.Fatalf("%s: opened", tc.name); return nil, nil }
		d := &device{id: 1, pub: 1}
		s.openBeat(d, tc.info, sdl.JOYSTICK_CONNECTION_WIRED, 0)
		if d.beat != nil || s.watched != 0 {
			t.Errorf("%s: watched", tc.name)
		}
	}

	s.lose(d, ms(5000))
	evs := drainAll(s)
	if len(evs) != 1 || evs[0].Kind != DeviceRemoved || evs[0].Device != 7 || evs[0].Time != ms(5000) {
		t.Fatalf("lose sent %+v, want device 7 removed at %d", evs, ms(5000))
	}
	if s.devices[7] != d || !d.dormant || s.mix.Has(7) || d.beat == nil {
		t.Errorf("after lose: in map %v, dormant %v, effects %v, beat %v; want kept, dormant, no effects, still open", s.devices[7] == d, d.dormant, s.mix.Has(7), d.beat)
	}
	if want := []string{"@0 rumble 28672/28672 70ms", "@0 rumble stop"}; !slices.Equal(rec.calls, want) {
		t.Errorf("effector calls %v, want the pulse and its stop %v", rec.calls, want)
	}
	if r := lb.records(t, "receiver silent: reported removed"); len(r) != 1 || r[0]["level"] != "WARN" || r[0]["silent"] != "4.99s" {
		t.Errorf("silent records %v, want one warn with silent 4.99s", r)
	}
	if a, b := s.alias(), s.alias(); a != math.MaxUint32 || b != math.MaxUint32-1 {
		t.Errorf("aliases %d, %d; want %d, %d", a, b, uint32(math.MaxUint32), uint32(math.MaxUint32-1))
	}
}

// TestOpenReceiverTimeoutError: a ReceiverTimeout below 100ms fails Open
// before it loads SDL or takes the process's Source.
func TestOpenReceiverTimeoutError(t *testing.T) {
	for _, d := range []time.Duration{-time.Second, time.Nanosecond, 99 * time.Millisecond} {
		s, err := Open(Options{Library: "does-not-exist", ReceiverTimeout: d})
		if s != nil || err == nil || !strings.Contains(err.Error(), "ReceiverTimeout") {
			t.Errorf("Open with ReceiverTimeout %v = %v, %v; want an error naming it", d, s, err)
		}
		if active.Load() {
			t.Fatalf("Open with ReceiverTimeout %v left the Source taken", d)
		}
	}
}
