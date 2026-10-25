package input

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Zyko0/go-sdl3/sdl"
)

// These tests need no SDL library: they drive the parts of a Source that
// read power through a function, and the mapping of SDL's values.

// TestPowerStateMapping maps every SDL power state, with the percents that
// go with it, to the four states of the script API (IN-12). SDL_POWERSTATE_ERROR
// is 4294967295 in go-sdl3 (ADR-0010) and is unknown; so is a state that a
// newer SDL adds. A percent below 0 is not reported, and one above 100 is
// 100.
func TestPowerStateMapping(t *testing.T) {
	tests := []struct {
		name    string
		state   sdl.PowerState
		percent int32
		want    Power
	}{
		{"on battery", sdl.POWERSTATE_ON_BATTERY, 80, Power{PowerDischarging, 80}},
		{"on battery, empty", sdl.POWERSTATE_ON_BATTERY, 0, Power{PowerDischarging, 0}},
		{"on battery, percent unknown", sdl.POWERSTATE_ON_BATTERY, -1, Power{PowerDischarging, -1}},
		{"charging", sdl.POWERSTATE_CHARGING, 40, Power{PowerCharging, 40}},
		{"charging, percent unknown", sdl.POWERSTATE_CHARGING, -1, Power{PowerCharging, -1}},
		{"charged is wired", sdl.POWERSTATE_CHARGED, 100, Power{PowerWired, 100}},
		{"no battery is wired", sdl.POWERSTATE_NO_BATTERY, -1, Power{PowerWired, -1}},
		{"unknown", sdl.POWERSTATE_UNKNOWN, -1, Power{PowerUnknown, -1}},
		{"unknown drops a percent", sdl.POWERSTATE_UNKNOWN, 50, Power{PowerUnknown, -1}},
		{"error", sdl.POWERSTATE_ERROR, -1, Power{PowerUnknown, -1}},
		{"error is 4294967295", sdl.PowerState(math.MaxUint32), 70, Power{PowerUnknown, -1}},
		{"a state of a newer SDL", sdl.PowerState(99), 70, Power{PowerUnknown, -1}},
		{"percent below -1", sdl.POWERSTATE_ON_BATTERY, -50, Power{PowerDischarging, -1}},
		{"percent above 100", sdl.POWERSTATE_ON_BATTERY, 250, Power{PowerDischarging, 100}},
	}
	for _, tt := range tests {
		// The driver "hidapi" has no say in these: the table is the same for
		// it, for the drivers that report charging for real (wgi) and for an
		// unknown one.
		for _, driver := range []string{"hidapi", "wgi", "dinput", ""} {
			if got := powerFromSDL(tt.state, tt.percent, driver); got != tt.want {
				t.Errorf("%s (%s): powerFromSDL(%d, %d) = %+v, want %+v", tt.name, driver, tt.state, tt.percent, got, tt.want)
			}
		}
	}
	if sdl.POWERSTATE_ERROR != 4294967295 {
		t.Errorf("SDL_POWERSTATE_ERROR = %d, want 4294967295 (ADR-0010)", uint32(sdl.POWERSTATE_ERROR))
	}
}

// TestPowerFromXInput (IN-12, ADR-0067): SDL reports XInput's
// BATTERY_TYPE_WIRED as SDL_POWERSTATE_CHARGING at 100 % (SDL_xinputjoystick.c,
// and the same code for an XInput-correlated device in SDL_rawinputjoystick.c),
// and XInput cannot tell charging from wired. A 2.4G receiver for a wireless
// pad reports it for ever, in Xbox mode. For the two drivers that take their
// battery from XInput, charging is wired; the percent stays, and a pad that
// discharges is not touched. The drivers that read the battery for real (WGI,
// HIDAPI) keep their charging.
func TestPowerFromXInput(t *testing.T) {
	tests := []struct {
		name    string
		driver  string
		state   sdl.PowerState
		percent int32
		want    Power
	}{
		{"xinput wired type", "xinput", sdl.POWERSTATE_CHARGING, 100, Power{PowerWired, 100}},
		{"xinput wired type, level unknown", "xinput", sdl.POWERSTATE_CHARGING, -1, Power{PowerWired, -1}},
		{"rawinput through xinput", "rawinput", sdl.POWERSTATE_CHARGING, 100, Power{PowerWired, 100}},
		{"xinput discharging", "xinput", sdl.POWERSTATE_ON_BATTERY, 40, Power{PowerDischarging, 40}},
		{"xinput empty", "xinput", sdl.POWERSTATE_ON_BATTERY, 10, Power{PowerDischarging, 10}},
		{"rawinput discharging", "rawinput", sdl.POWERSTATE_ON_BATTERY, 70, Power{PowerDischarging, 70}},
		{"xinput unknown", "xinput", sdl.POWERSTATE_UNKNOWN, -1, Power{PowerUnknown, -1}},
		{"xinput wired already", "xinput", sdl.POWERSTATE_NO_BATTERY, -1, Power{PowerWired, -1}},
		{"wgi charges for real", "wgi", sdl.POWERSTATE_CHARGING, 60, Power{PowerCharging, 60}},
		{"hidapi charges for real", "hidapi", sdl.POWERSTATE_CHARGING, 55, Power{PowerCharging, 55}},
		{"gameinput is not xinput", "gameinput", sdl.POWERSTATE_CHARGING, 30, Power{PowerCharging, 30}},
		{"no driver", "", sdl.POWERSTATE_CHARGING, 30, Power{PowerCharging, 30}},
	}
	for _, tt := range tests {
		if got := powerFromSDL(tt.state, tt.percent, tt.driver); got != tt.want {
			t.Errorf("%s: powerFromSDL(%d, %d, %q) = %+v, want %+v", tt.name, tt.state, tt.percent, tt.driver, got, tt.want)
		}
	}
}

// The reads of a device use its driver: the first read, the poll and SDL's
// battery event all give a receiver's permanent "charging" as wired.
func TestPowerReadsUseTheDriver(t *testing.T) {
	reading := Power{PowerCharging, 100}
	s, d := padSource(16, 7, &reading)
	d.driver = "xinput"
	s.readPower(d, 10)
	s.handlePower(&sdl.JoyBatteryEvent{Which: 7, State: sdl.POWERSTATE_CHARGING, Percent: 100, Timestamp: 11})
	reading = Power{PowerDischarging, 40}
	s.readPower(d, 12)
	got := powers(s)
	want := []Power{{PowerWired, 100}, {PowerDischarging, 40}}
	if len(got) != len(want) {
		t.Fatalf("sent %d DevicePower events, want %d: %+v", len(got), len(want), got)
	}
	for i, e := range got {
		if e.Power == nil || *e.Power != want[i] {
			t.Errorf("event %d = %v, want %+v", i, e.Power, want[i])
		}
	}
	// The same read from a pad of another driver is charging.
	other, od := padSource(16, 8, &Power{PowerCharging, 100})
	od.driver = "hidapi"
	other.readPower(od, 1)
	if got := powers(other); len(got) != 1 || got[0].Power == nil || *got[0].Power != (Power{PowerCharging, 100}) {
		t.Errorf("a hidapi pad = %+v, want charging at 100", got)
	}
}

func TestPowerStateNames(t *testing.T) {
	for st, name := range map[PowerState]string{
		PowerUnknown: "unknown", PowerDischarging: "discharging", PowerCharging: "charging", PowerWired: "wired",
		PowerState(200): "unknown",
	} {
		if got := st.String(); got != name {
			t.Errorf("PowerState(%d).String() = %q, want %q", st, got, name)
		}
	}
	for _, name := range []string{"unknown", "discharging", "charging", "wired"} {
		st, ok := ParsePowerState(name)
		if !ok || st.String() != name {
			t.Errorf("ParsePowerState(%q) = %v, %t", name, st, ok)
		}
	}
	for _, name := range []string{"", "Wired", "on battery", "charged", "error"} {
		if _, ok := ParsePowerState(name); ok {
			t.Errorf("ParsePowerState(%q) accepted", name)
		}
	}
}

// TestPowerJSON: the JSON form is devices.list's "battery". The percent is
// left out when it is not reported, and 0 is a percent.
func TestPowerJSON(t *testing.T) {
	tests := []struct {
		p    Power
		json string
	}{
		{Power{PowerDischarging, 80}, `{"state":"discharging","percent":80}`},
		{Power{PowerDischarging, 0}, `{"state":"discharging","percent":0}`},
		{Power{PowerCharging, 100}, `{"state":"charging","percent":100}`},
		{Power{PowerWired, -1}, `{"state":"wired"}`},
		{Power{PowerUnknown, -1}, `{"state":"unknown"}`},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.p)
		if err != nil || string(b) != tt.json {
			t.Errorf("Marshal(%+v) = %s, %v; want %s", tt.p, b, err, tt.json)
		}
		var back Power
		if err := json.Unmarshal([]byte(tt.json), &back); err != nil || back != tt.p {
			t.Errorf("Unmarshal(%s) = %+v, %v; want %+v", tt.json, back, err, tt.p)
		}
	}
	// A pointer, as DeviceView has it, is left out when nil.
	b, err := json.Marshal(struct {
		Battery *Power `json:"battery,omitempty"`
	}{})
	if err != nil || string(b) != `{}` {
		t.Errorf("Marshal(nil battery) = %s, %v", b, err)
	}
	for _, bad := range []string{
		`{}`, `{"state":"on battery"}`, `{"state":1}`, `{"state":"wired","percent":101}`,
		`{"state":"wired","percent":-1}`, `{"state":"wired","percent":"5"}`, `{"state":"wired","extra":1}`, `[]`, `null5`,
	} {
		var p Power
		if err := json.Unmarshal([]byte(bad), &p); err == nil {
			t.Errorf("Unmarshal(%s) = %+v, want an error", bad, p)
		}
	}
}

// padSource returns a Source for the tests of its power reads, with the
// events channel of capacity n and power events on, and a gamepad id whose
// power reads from *reading. nothing of SDL is called.
func padSource(n int, id sdl.JoystickID, reading *Power) (*Source, *device) {
	s := &Source{events: make(chan Event, n), devices: map[sdl.JoystickID]*device{}, power: true}
	d := &device{id: id, pub: DeviceID(id)}
	d.powerFn = func() (sdl.PowerState, int32) { return reading.sdl() }
	s.devices[id] = d
	return s, d
}

// sdl is the inverse of powerFromSDL, for the tests.
func (p Power) sdl() (sdl.PowerState, int32) {
	state := map[PowerState]sdl.PowerState{
		PowerUnknown: sdl.POWERSTATE_UNKNOWN, PowerDischarging: sdl.POWERSTATE_ON_BATTERY,
		PowerCharging: sdl.POWERSTATE_CHARGING, PowerWired: sdl.POWERSTATE_NO_BATTERY,
	}[p.State]
	return state, int32(p.Percent)
}

// powers returns the DevicePower events on s.events and its backlog, in
// order, without consuming the channel.
func powers(s *Source) []Event {
	var out []Event
	n := len(s.events)
	for range n {
		e := <-s.events
		if e.Kind == DevicePower {
			out = append(out, e)
		}
		s.events <- e
	}
	for _, e := range s.backlog {
		if e.Kind == DevicePower {
			out = append(out, e)
		}
	}
	return out
}

// TestPowerSentOnlyWhenChanged: the first read is always sent, even when
// it is unknown, so that a consumer can tell "not read" from "read"; the
// next ones only when they differ from the last one sent.
func TestPowerSentOnlyWhenChanged(t *testing.T) {
	reading := Power{PowerUnknown, -1}
	s, d := padSource(16, 7, &reading)
	s.readPower(d, 10)
	s.readPower(d, 11) // the same
	reading = Power{PowerDischarging, 80}
	s.readPower(d, 12)
	s.readPower(d, 13) // the same
	reading = Power{PowerDischarging, 70}
	s.readPower(d, 14) // the percent changed
	reading = Power{PowerCharging, 70}
	s.readPower(d, 15) // the state changed
	reading = Power{PowerUnknown, -1}
	s.readPower(d, 16) // back to unknown: a change as well
	got := powers(s)
	want := []Power{{PowerUnknown, -1}, {PowerDischarging, 80}, {PowerDischarging, 70}, {PowerCharging, 70}, {PowerUnknown, -1}}
	if len(got) != len(want) {
		t.Fatalf("sent %d DevicePower events %+v, want %d", len(got), got, len(want))
	}
	for i, e := range got {
		if e.Power == nil || *e.Power != want[i] || e.Device != 7 {
			t.Errorf("event %d = %+v (power %v), want %+v of device 7", i, e, e.Power, want[i])
		}
	}
	if got[1].Time != 12 {
		t.Errorf("Time = %d, want the time of the read, 12", got[1].Time)
	}
	// A read of a Source without power events sends nothing and does not
	// call SDL.
	off, od := padSource(4, 8, &reading)
	off.power = false
	od.powerFn = func() (sdl.PowerState, int32) { t.Error("power read with power events off"); return 0, -1 }
	off.readPower(od, 1)
	off.pollPower(time.Now().Add(time.Hour))
	if n := len(off.events) + len(off.backlog); n != 0 {
		t.Errorf("a Source without power events sent %d", n)
	}
}

// TestPowerEvent: SDL's battery event carries the new value, which is sent
// when it changed. The event of a device that T-input does not hold, of a
// joystick-only device, and of a dormant one is ignored.
func TestPowerEvent(t *testing.T) {
	reading := Power{PowerUnknown, -1}
	s, d := padSource(16, 7, &reading)
	s.devices[9] = &device{id: 9, pub: 9} // no gamepad
	battery := func(which sdl.JoystickID, state sdl.PowerState, percent int32) *sdl.Event {
		var ev sdl.Event
		*(*sdl.JoyBatteryEvent)(unsafe.Pointer(&ev)) = sdl.JoyBatteryEvent{
			Type: sdl.EVENT_JOYSTICK_BATTERY_UPDATED, Timestamp: 55, Which: which, State: state, Percent: percent,
		}
		return &ev
	}
	s.handle(battery(7, sdl.POWERSTATE_ON_BATTERY, 40))
	s.handle(battery(7, sdl.POWERSTATE_ON_BATTERY, 40)) // no change
	s.handle(battery(7, sdl.POWERSTATE_CHARGING, 40))
	s.handle(battery(7, sdl.PowerState(math.MaxUint32), -1)) // SDL_POWERSTATE_ERROR
	s.handle(battery(8, sdl.POWERSTATE_ON_BATTERY, 10))      // not a device of T-input
	s.handle(battery(9, sdl.POWERSTATE_ON_BATTERY, 10))      // no gamepad
	d.dormant = true
	s.handle(battery(7, sdl.POWERSTATE_ON_BATTERY, 90)) // dormant: reported removed
	got := powers(s)
	want := []Power{{PowerDischarging, 40}, {PowerCharging, 40}, {PowerUnknown, -1}}
	if len(got) != len(want) {
		t.Fatalf("sent %+v, want %v", got, want)
	}
	for i, e := range got {
		if *e.Power != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, *e.Power, want[i])
		}
	}
	if got[0].Time != 55 {
		t.Errorf("Time = %d, want the event's, 55", got[0].Time)
	}
}

// TestPowerPoll: every non-dormant gamepad is read every 30 s, as a safety
// net for a driver that sends no battery event; the first poll is 30 s
// after the first look, and a poll that finds nothing new sends nothing.
func TestPowerPoll(t *testing.T) {
	reading := Power{PowerDischarging, 80}
	s, d := padSource(16, 7, &reading)
	other := Power{PowerCharging, 20}
	s.devices[8] = &device{id: 8, pub: 8, powerFn: other.sdl}
	dormant := &device{id: 6, pub: 6, dormant: true, powerFn: func() (sdl.PowerState, int32) { t.Error("a dormant device was polled"); return 0, -1 }}
	s.devices[6] = dormant

	t0 := time.Unix(1000, 0)
	s.pollPower(t0) // arms the timer
	if n := len(powers(s)); n != 0 {
		t.Fatalf("the first look sent %d events", n)
	}
	s.pollPower(t0.Add(powerPollEvery - time.Nanosecond))
	if n := len(powers(s)); n != 0 {
		t.Fatalf("a poll before the time sent %d events", n)
	}
	s.pollPower(t0.Add(powerPollEvery))
	if got := powers(s); len(got) != 2 {
		t.Fatalf("the poll sent %+v, want one event per gamepad", got)
	}
	s.pollPower(t0.Add(powerPollEvery + time.Second)) // not due again
	reading = Power{PowerDischarging, 75}
	s.pollPower(t0.Add(2 * powerPollEvery))
	got := powers(s)
	if len(got) != 3 || *got[2].Power != (Power{PowerDischarging, 75}) || got[2].Device != 7 {
		t.Fatalf("the second poll sent %+v, want only the change of device 7", got)
	}
	if d.dormant {
		t.Error("the poll changed the device")
	}
}

// TestPowerRevive: a device that comes back as a new device, with a new
// DeviceID, is read again and the value is sent even when it is the one
// it had, since the consumer dropped it with the removal.
func TestPowerRevive(t *testing.T) {
	reading := Power{PowerDischarging, 55}
	s, d := padSource(16, 7, &reading)
	s.readPower(d, 1)
	s.readPower(d, 2)
	if n := len(powers(s)); n != 1 {
		t.Fatalf("%d events before the revival, want 1", n)
	}
	old := d.pub
	d.pub = DeviceID(math.MaxUint32) // what alias gives
	s.rereadPower(d, 3)
	got := powers(s)
	if len(got) != 2 || got[1].Device != d.pub || *got[1].Power != reading || old == d.pub {
		t.Fatalf("events after the revival %+v, want the same value again for the new id", got)
	}
}

// TestSendPowerKeepsTheLatest: a DevicePower that has to wait replaces the
// one of the same device that waits already, and keeps its place in line.
func TestSendPowerKeepsTheLatest(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	add := func(id DeviceID) Event { return Event{Device: id, Kind: DeviceAdded, Info: &DeviceInfo{ID: id}} }
	power := func(id DeviceID, p Power) Event { return Event{Device: id, Kind: DevicePower, Power: &p} }
	s.send(Event{Device: 1, Kind: ButtonDown, Control: "south"}) // fills the channel
	s.send(add(1))
	first := power(1, Power{PowerDischarging, 90})
	s.send(first)
	s.send(add(2))
	other := power(2, Power{PowerCharging, 10})
	s.send(other)
	latest := power(1, Power{PowerDischarging, 80})
	s.send(latest)
	if want := []Event{add(1), latest, add(2), other}; !equalEvents(s.backlog, want) {
		t.Fatalf("backlog %+v, want %+v", s.backlog, want)
	}
	if n := s.Dropped(); n != 0 {
		t.Errorf("Dropped() = %d, want 0", n)
	}
}

// TestSendPowerIsNeverDropped: a DevicePower waits like a device event; a
// button event behind it is dropped, and the power is delivered in order.
func TestSendPowerIsNeverDropped(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Device: 1, Kind: DeviceAdded, Info: &DeviceInfo{ID: 1}}) // fills the channel
	p := Power{PowerCharging, 30}
	pe := Event{Device: 1, Kind: DevicePower, Power: &p}
	s.send(pe)
	s.send(Event{Device: 1, Kind: AxisMotion, Control: "leftx", Value: 1})
	if n := s.Dropped(); n != 1 {
		t.Fatalf("Dropped() = %d, want 1: the axis event behind the power", n)
	}
	if len(s.backlog) != 1 || s.backlog[0].Kind != DevicePower {
		t.Fatalf("backlog %+v, want the power", s.backlog)
	}
	<-s.events
	s.flush()
	if got := <-s.events; got.Kind != DevicePower || *got.Power != p {
		t.Errorf("delivered %+v, want the power", got)
	}
}

// TestSendRemovalDropsThePower: a removal takes the power events of its
// device out of the line, since the consumer has no use for them; with
// the DeviceAdded still waiting, nothing of the device is sent.
func TestSendRemovalDropsThePower(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	pw := func(id DeviceID) Event {
		p := Power{PowerWired, -1}
		return Event{Device: id, Kind: DevicePower, Power: &p}
	}
	s.send(Event{Device: 1, Kind: ButtonDown, Control: "south"}) // fills the channel
	s.send(Event{Device: 2, Kind: DeviceAdded, Info: &DeviceInfo{ID: 2}})
	s.send(pw(2))
	s.send(Event{Device: 3, Kind: DeviceAdded, Info: &DeviceInfo{ID: 3}})
	s.send(pw(3))
	s.send(Event{Device: 2, Kind: DeviceRemoved})
	if len(s.backlog) != 2 || s.backlog[0].Device != 3 || s.backlog[1].Kind != DevicePower || s.backlog[1].Device != 3 {
		t.Fatalf("backlog %+v, want the DeviceAdded and the power of device 3 only", s.backlog)
	}
	// A device whose DeviceAdded was sent keeps its removal in line, and
	// loses its power.
	s2 := &Source{events: make(chan Event, 1)}
	s2.send(Event{Device: 4, Kind: DeviceAdded, Info: &DeviceInfo{ID: 4}}) // fills the channel, sent
	s2.send(pw(4))
	s2.send(Event{Device: 4, Kind: DeviceRemoved})
	if len(s2.backlog) != 1 || s2.backlog[0].Kind != DeviceRemoved {
		t.Fatalf("backlog %+v, want the removal only", s2.backlog)
	}
}

// TestSendPowerFlapping: a device that connects, reports its power and
// disconnects again and again while the consumer reads nothing leaves
// nothing in line.
func TestSendPowerFlapping(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Device: 1, Kind: ButtonDown, Control: "south"}) // fills the channel
	for i := range 100 {
		id := DeviceID(10 + i)
		p := Power{PowerDischarging, i % 101}
		for _, e := range []Event{
			{Device: id, Kind: DeviceAdded, Info: &DeviceInfo{ID: id}},
			{Device: id, Kind: DevicePower, Power: &p},
			{Device: id, Kind: DeviceRemoved},
		} {
			s.send(e)
			if len(s.backlog) > 2 {
				t.Fatalf("cycle %d: backlog %d after %v", i, len(s.backlog), e.Kind)
			}
		}
	}
	if len(s.backlog) != 0 {
		t.Errorf("backlog %+v, want empty", s.backlog)
	}
}

// TestEventKindDevicePowerIsNotATraceRecord: DevicePower is routing
// metadata, as DeviceState is. WriteEvent refuses it and writes nothing;
// ReadEvents rejects its name.
func TestEventKindDevicePowerIsNotATraceRecord(t *testing.T) {
	if got := DevicePower.String(); got != "power" {
		t.Errorf("DevicePower.String() = %q, want power", got)
	}
	var b bytes.Buffer
	p := Power{PowerCharging, 5}
	err := WriteEvent(&b, Event{Time: 5, Device: 1, Kind: DevicePower, Power: &p})
	if err == nil || err.Error() != "input: event kind power is not written to traces" {
		t.Errorf("WriteEvent(DevicePower) = %v", err)
	}
	if b.Len() != 0 {
		t.Errorf("WriteEvent(DevicePower) wrote %q", b.String())
	}
	for _, line := range []string{
		`{"timeNs":0,"kind":"power","device":1}`,
		`{"timeNs":0,"kind":"power","device":1,"power":{"state":"wired"}}`,
	} {
		evs, err := ReadEvents(strings.NewReader(line))
		if err == nil || !strings.Contains(err.Error(), `unknown kind "power"; want added, removed, down, up or axis`) || evs != nil {
			t.Errorf("ReadEvents(%s) = %v, %v; want the unknown kind error", line, evs, err)
		}
	}
	if slices.Contains(traceKinds[:], DevicePower) {
		t.Error("DevicePower is in traceKinds")
	}
}

func equalEvents(a, b []Event) bool {
	return slices.EqualFunc(a, b, func(x, y Event) bool {
		if x.Kind != y.Kind || x.Device != y.Device || x.Time != y.Time || x.Control != y.Control {
			return false
		}
		return (x.Power == nil) == (y.Power == nil) && (x.Power == nil || *x.Power == *y.Power)
	})
}
