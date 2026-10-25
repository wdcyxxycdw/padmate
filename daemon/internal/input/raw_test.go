package input

import (
	"testing"
	"unsafe"

	"github.com/Zyko0/go-sdl3/sdl"
)

// rawSource returns a Source with one device, id 1, that is asked for raw
// events with two axes. Its clock is now, in ns.
func rawSource(now *uint64) *Source {
	s := &Source{
		rawEvents: make(chan RawEvent, 64),
		devices:   map[sdl.JoystickID]*device{},
		ticks:     func() uint64 { return *now },
	}
	s.devices[1] = &device{id: 1, pub: 1, kind: kindRaw, raw: &rawWatch{axes: make([]rawAxis, 2)}}
	s.rawOn = 1
	return s
}

func drainRaw(s *Source) []RawEvent {
	var out []RawEvent
	for {
		select {
		case e := <-s.rawEvents:
			out = append(out, e)
		default:
			return out
		}
	}
}

// TestRawAxisRateLimit: an axis sends at most once in rawAxisInterval; the
// newest value waits and goes out when the time is up, so that the last
// position always arrives; a value equal to the one sent is not sent, and one
// that comes back to it takes the waiting value back.
func TestRawAxisRateLimit(t *testing.T) {
	var now uint64
	s := rawSource(&now)
	ms := func(n uint64) uint64 { return n * 1_000_000 }
	axis := func(at uint64, v int16) {
		now = at
		s.rawAxis(1, 0, v, at)
	}
	want := func(what string, vals ...int16) {
		t.Helper()
		got := drainRaw(s)
		if len(got) != len(vals) {
			t.Fatalf("%s: %+v, want values %v", what, got, vals)
		}
		for i, e := range got {
			if e.Kind != RawAxis || e.Device != 1 || e.Value != vals[i] {
				t.Errorf("%s: event %d = %+v, want axis value %d", what, i, e, vals[i])
			}
		}
	}
	axis(ms(0), 100)
	want("the first value goes out at once", 100)
	axis(ms(3), 200)
	axis(ms(5), 300)
	want("values inside the interval wait")
	if s.rawPending != 1 {
		t.Fatalf("rawPending = %d, want 1", s.rawPending)
	}
	now = ms(7)
	s.flushRaw()
	want("not yet")
	now = ms(8)
	s.flushRaw()
	got := drainRaw(s)
	if len(got) != 1 || got[0].Value != 300 || got[0].Time != ms(5) || s.rawPending != 0 {
		t.Fatalf("flush sent %+v, rawPending %d; want the newest value, 300, with its own time", got, s.rawPending)
	}
	axis(ms(9), 300)
	want("the value that was sent last is not sent again")
	// A value that comes back to the one sent takes the waiting one back.
	axis(ms(10), 400)
	axis(ms(11), 300)
	if s.rawPending != 0 {
		t.Errorf("rawPending = %d after the axis came back", s.rawPending)
	}
	now = ms(40)
	s.flushRaw()
	want("nothing is left to send after the axis came back")
	// After the interval the next value goes out at once.
	axis(ms(60), 500)
	want("a value after the interval", 500)
	// Another axis has its own interval.
	now = ms(60)
	s.rawAxis(1, 1, -7, ms(60))
	if got := drainRaw(s); len(got) != 1 || got[0].Index != 1 {
		t.Errorf("axis 1: %+v", got)
	}
	// An index the joystick does not have, and a device nobody asked for.
	s.rawAxis(1, 9, 1, ms(70))
	s.rawAxis(2, 0, 1, ms(70))
	want("an unknown axis and an unknown device")
}

// TestRawButtonAndHat: buttons and hats go out as they come.
func TestRawButtonAndHat(t *testing.T) {
	var now uint64
	s := rawSource(&now)
	s.rawButton(1, 4, true, 10)
	s.rawButton(1, 4, false, 20)
	s.rawHat(1, 0, 6, 30)
	s.rawButton(7, 1, true, 40) // nobody asked for device 7
	got := drainRaw(s)
	want := []RawEvent{
		{Time: 10, Device: 1, Kind: RawButton, Index: 4, Value: 1},
		{Time: 20, Device: 1, Kind: RawButton, Index: 4, Value: 0},
		{Time: 30, Device: 1, Kind: RawHat, Index: 0, Value: 6},
	}
	if len(got) != len(want) {
		t.Fatalf("events %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRawFullChannelDrops: T-input never waits for the consumer of the raw
// events.
func TestRawFullChannelDrops(t *testing.T) {
	var now uint64
	s := rawSource(&now)
	s.rawEvents = make(chan RawEvent, 2)
	for i := range 5 {
		s.rawButton(1, uint8(i), true, uint64(i))
	}
	if n := s.RawDropped(); n != 3 || len(s.rawEvents) != 2 {
		t.Errorf("dropped %d, queued %d; want 3 and 2", n, len(s.rawEvents))
	}
}

// TestHandleJoystickEventsWithoutRawCostNothing: while no device is asked
// for, the joystick events that SDL sends for every open gamepad too are not
// decoded: no allocation on T-input's hot path, and nothing sent.
func TestHandleJoystickEventsWithoutRawCostNothing(t *testing.T) {
	var now uint64
	s := rawSource(&now)
	s.rawOn = 0
	var axis, button, hat sdl.Event
	*(*sdl.JoyAxisEvent)(unsafe.Pointer(&axis)) = sdl.JoyAxisEvent{Type: sdl.EVENT_JOYSTICK_AXIS_MOTION, Which: 1, Axis: 0, Value: 1000}
	*(*sdl.JoyButtonEvent)(unsafe.Pointer(&button)) = sdl.JoyButtonEvent{Type: sdl.EVENT_JOYSTICK_BUTTON_DOWN, Which: 1, Button: 2, Down: true}
	*(*sdl.JoyHatEvent)(unsafe.Pointer(&hat)) = sdl.JoyHatEvent{Type: sdl.EVENT_JOYSTICK_HAT_MOTION, Which: 1, Hat: 0, Value: 4}
	for name, ev := range map[string]*sdl.Event{"axis": &axis, "button": &button, "hat": &hat} {
		if n := testing.AllocsPerRun(1000, func() { s.handle(ev) }); n != 0 {
			t.Errorf("%s: %v allocations a call", name, n)
		}
	}
	if len(drainRaw(s)) != 0 {
		t.Error("raw events sent while nobody asked")
	}
	// Asked for, the same events go out.
	s.rawOn = 1
	s.handle(&button)
	s.handle(&hat)
	s.handle(&axis)
	if got := drainRaw(s); len(got) != 3 {
		t.Errorf("events %+v, want a button, a hat and an axis", got)
	}
}
