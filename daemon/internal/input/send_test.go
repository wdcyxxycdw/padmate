package input

import (
	"slices"
	"testing"
)

// These tests need no SDL: they drive the parts of a Source that do not
// call it.

// drain returns the events on s.events, which it closes.
func drain(s *Source) []Event {
	close(s.events)
	var got []Event
	for e := range s.events {
		got = append(got, e)
	}
	return got
}

// TestSendDrops fills the Events channel: send drops the next button and
// axis events without blocking and counts them.
func TestSendDrops(t *testing.T) {
	s := &Source{events: make(chan Event, 2)}
	for _, k := range []EventKind{ButtonDown, ButtonUp, AxisMotion, ButtonDown} {
		s.send(Event{Kind: k})
	}
	if n := s.Dropped(); n != 2 {
		t.Errorf("Dropped() = %d, want 2", n)
	}
	got := drain(s)
	if len(got) != 2 || got[0].Kind != ButtonDown || got[1].Kind != ButtonUp {
		t.Errorf("delivered %v, want [down up]", got)
	}
}

// TestSendBacklog fills the Events channel: the device events that do not
// fit wait in line and follow in order as the consumer reads; a button or
// axis event is dropped while a device event still waits, even after the
// flush that send does first, and is sent once that flush emptied the
// line.
func TestSendBacklog(t *testing.T) {
	s := &Source{events: make(chan Event, 2)}
	down1 := Event{Device: 1, Kind: ButtonDown, Control: "south"}
	add2 := Event{Device: 2, Kind: DeviceAdded, Info: &DeviceInfo{ID: 2}}
	add3 := Event{Device: 3, Kind: DeviceAdded, Info: &DeviceInfo{ID: 3}}
	gone2 := Event{Device: 2, Kind: DeviceRemoved}
	down3 := Event{Device: 3, Kind: ButtonDown, Control: "east"}
	var got []Event
	read := func() { got = append(got, <-s.events) }
	steps := []struct {
		name    string
		send    *Event
		read    int // events the consumer reads before the send
		backlog int
		dropped uint64
	}{
		{"fits", &down1, 0, 0, 0},
		{"fits", &add2, 0, 0, 0},
		{"full: dropped", &Event{Device: 1, Kind: ButtonUp, Control: "south"}, 0, 0, 1},
		{"full: waits", &add3, 0, 1, 1},
		{"waits behind", &gone2, 0, 2, 1},
		{"dropped behind", &Event{Device: 3, Kind: AxisMotion, Control: "leftx", Value: 5}, 0, 2, 2},
		{"flushed one, dropped", &down3, 1, 1, 3},
		{"flushed all, sent", &down3, 2, 0, 3},
	}
	for i, st := range steps {
		for range st.read {
			read()
		}
		s.send(*st.send)
		if len(s.backlog) != st.backlog || s.Dropped() != st.dropped {
			t.Fatalf("step %d (%s): backlog %d, dropped %d; want %d, %d", i, st.name, len(s.backlog), s.Dropped(), st.backlog, st.dropped)
		}
	}
	got = append(got, drain(s)...)
	want := []Event{down1, add2, add3, gone2, down3}
	if !slices.Equal(got, want) {
		t.Errorf("delivered %+v,\nwant %+v", got, want)
	}
}

// TestSendCancels checks that a removal whose DeviceAdded still waits in
// line takes it out, and is not sent either: nothing of that device
// reached the consumer. A removal of a device whose DeviceAdded was sent
// waits in line.
func TestSendCancels(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	add4 := Event{Device: 4, Kind: DeviceAdded, Info: &DeviceInfo{ID: 4}}
	gone4 := Event{Device: 4, Kind: DeviceRemoved}
	add5 := Event{Device: 5, Kind: DeviceAdded, Info: &DeviceInfo{ID: 5}}
	s.send(add4) // fills the channel
	s.send(Event{Device: 2, Kind: DeviceAdded, Info: &DeviceInfo{ID: 2}})
	s.send(Event{Device: 2, Kind: ButtonDown, Control: "south"})
	s.send(gone4)
	s.send(add5)
	s.send(Event{Device: 2, Kind: DeviceRemoved})
	if want := []Event{gone4, add5}; !slices.Equal(s.backlog, want) {
		t.Fatalf("backlog %+v, want %+v", s.backlog, want)
	}
	if n := s.Dropped(); n != 1 {
		t.Errorf("Dropped() = %d, want 1: the button event", n)
	}
	got := []Event{<-s.events}
	s.flush()
	got = append(got, <-s.events)
	s.flush()
	got = append(got, drain(s)...)
	if want := []Event{add4, gone4, add5}; !slices.Equal(got, want) {
		t.Errorf("delivered %+v, want %+v", got, want)
	}
}

// TestSendFlapping connects and disconnects a device again and again while
// the consumer reads nothing: each removal cancels the DeviceAdded before
// it, so the line never grows past one event.
func TestSendFlapping(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Device: 1, Kind: ButtonDown, Control: "south"}) // fills the channel
	for i := range 100 {
		id := DeviceID(10 + i)
		for _, e := range []Event{
			{Device: id, Kind: DeviceAdded, Info: &DeviceInfo{ID: id}},
			{Device: id, Kind: ButtonDown, Control: "east"},
			{Device: id, Kind: DeviceRemoved},
		} {
			s.send(e)
			if len(s.backlog) > 1 {
				t.Fatalf("cycle %d: backlog %d after %v", i, len(s.backlog), e.Kind)
			}
		}
	}
	if len(s.backlog) != 0 || s.Dropped() != 100 {
		t.Errorf("backlog %d, dropped %d; want 0, 100", len(s.backlog), s.Dropped())
	}
}

// comboSource returns a Source with combo detection on, whose combo is
// back+start held for 2 s, and a counter of its OnCombo calls.
func comboSource(capacity int) (*Source, *int) {
	calls := new(int)
	s := &Source{events: make(chan Event, capacity), onCombo: func() { *calls++ }}
	s.watch = NewComboWatch(func(info DeviceInfo) Combo { return DefaultCombo(info, false) })
	return s, calls
}

func ms(n uint64) uint64 { return n * 1e6 }

// TestEmitCombo checks that emit fires a combo due at an event's time
// before the event, after one OnCombo call.
func TestEmitCombo(t *testing.T) {
	s, calls := comboSource(10)
	add := Event{Time: 0, Device: 1, Kind: DeviceAdded, Info: &DeviceInfo{ID: 1, Type: "ps4"}}
	back := Event{Time: 0, Device: 1, Kind: ButtonDown, Control: "back"}
	start := Event{Time: ms(10), Device: 1, Kind: ButtonDown, Control: "start"}
	up := Event{Time: ms(2010), Device: 1, Kind: ButtonUp, Control: "back"}
	for _, e := range []Event{add, back, start} {
		s.emit(e)
	}
	s.fireDue(ms(2009))
	if *calls != 0 {
		t.Fatalf("OnCombo called %d times before the due time", *calls)
	}
	s.emit(up)
	if *calls != 1 {
		t.Errorf("OnCombo called %d times, want 1", *calls)
	}
	combo := Event{Time: ms(2010), Device: 1, Kind: ComboHeld}
	if got, want := drain(s), []Event{add, back, start, combo, up}; !slices.Equal(got, want) {
		t.Errorf("delivered %+v,\nwant %+v", got, want)
	}
}

// TestEmitComboQueueFull holds a combo while Events is full: the watch
// sees the button events that send drops, so OnCombo still runs when the
// hold is reached, and the ComboHeld waits in line and follows once there
// is room. It stays when a removal takes out its device's DeviceAdded.
// Dropped counts only the button and axis events.
func TestEmitComboQueueFull(t *testing.T) {
	s, calls := comboSource(1)
	fill := Event{Device: 9, Kind: ButtonDown, Control: "south"}
	s.emit(fill)
	s.emit(Event{Device: 2, Kind: DeviceAdded, Info: &DeviceInfo{ID: 2}})
	s.emit(Event{Time: ms(10), Device: 2, Kind: ButtonDown, Control: "back"})
	s.emit(Event{Time: ms(20), Device: 2, Kind: ButtonDown, Control: "start"})
	s.fireDue(ms(2019))
	if *calls != 0 || s.Dropped() != 2 {
		t.Fatalf("before the due time: %d OnCombo calls, %d dropped; want 0, 2", *calls, s.Dropped())
	}
	s.fireDue(ms(2500)) // as the loop does when no event comes
	if *calls != 1 {
		t.Errorf("OnCombo called %d times, want 1", *calls)
	}
	s.emit(Event{Time: ms(2600), Device: 2, Kind: AxisMotion, Control: "leftx", Value: 5})
	s.emit(Event{Time: ms(2700), Device: 2, Kind: DeviceRemoved})
	combo := Event{Time: ms(2020), Device: 2, Kind: ComboHeld}
	if want := []Event{combo}; !slices.Equal(s.backlog, want) {
		t.Fatalf("backlog %+v, want %+v", s.backlog, want)
	}
	got := []Event{<-s.events}
	s.flush()
	got = append(got, drain(s)...)
	if want := []Event{fill, combo}; !slices.Equal(got, want) {
		t.Errorf("delivered %+v, want %+v", got, want)
	}
	if n := s.Dropped(); n != 3 {
		t.Errorf("Dropped() = %d, want 3: the buttons and the axis", n)
	}
	if *calls != 1 {
		t.Errorf("OnCombo called %d times, want 1", *calls)
	}
}

// TestEmitNoCombo: without OnCombo, a Source has no watch, and back+start
// held for long sends nothing more.
func TestEmitNoCombo(t *testing.T) {
	s := &Source{events: make(chan Event, 10)}
	evs := []Event{
		{Device: 1, Kind: DeviceAdded, Info: &DeviceInfo{ID: 1}},
		{Device: 1, Kind: ButtonDown, Control: "back"},
		{Device: 1, Kind: ButtonDown, Control: "start"},
		{Time: ms(9000), Device: 1, Kind: ButtonUp, Control: "back"},
	}
	for _, e := range evs {
		s.emit(e)
	}
	if s.armed() {
		t.Error("armed() = true without a watch")
	}
	if got := drain(s); !slices.Equal(got, evs) {
		t.Errorf("delivered %+v, want %+v", got, evs)
	}
}

// TestHintTrue checks the reading of a boolean hint.
func TestHintTrue(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"", false}, {"0", false}, {"00", false}, {"0x1", false},
		{"false", false}, {"FALSE", false}, {"False", false},
		{"1", true}, {"true", true}, {"yes", true}, {"2", true},
		{" 0", true}, {"false ", true}, // SDL does not trim
	} {
		if got := hintTrue(tc.v); got != tc.want {
			t.Errorf("hintTrue(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// TestFlush checks that flush, which T-input's loop calls on each turn,
// sends what waits in line without a new event.
func TestFlush(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	down := Event{Device: 1, Kind: ButtonDown, Control: "south"}
	gone := Event{Device: 1, Kind: DeviceRemoved}
	s.send(down)
	s.send(gone)
	s.flush() // no room: nothing changes
	if len(s.backlog) != 1 {
		t.Fatalf("backlog %d, want 1", len(s.backlog))
	}
	got := []Event{<-s.events}
	s.flush()
	if len(s.backlog) != 0 {
		t.Fatalf("backlog %d after the flush, want 0", len(s.backlog))
	}
	got = append(got, drain(s)...)
	if want := []Event{down, gone}; !slices.Equal(got, want) {
		t.Errorf("delivered %+v, want %+v", got, want)
	}
}

// TestSendRemappedBacklog: DeviceRemapped is never dropped; one that waits is
// replaced in its place by a newer one of the device, and a removal takes it
// out, as it does a DevicePower. Reload remaps every open gamepad at once, so
// the line must stay bounded.
func TestSendRemappedBacklog(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Kind: ButtonDown, Device: 1}) // fills Events
	add := Event{Kind: DeviceAdded, Device: 1, Info: &DeviceInfo{ID: 1, IsGamepad: true}}
	first := Event{Kind: DeviceRemapped, Device: 1, Info: &DeviceInfo{ID: 1, ButtonMask: 1}}
	second := Event{Kind: DeviceRemapped, Device: 1, Info: &DeviceInfo{ID: 1, ButtonMask: 3}}
	other := Event{Kind: DeviceRemapped, Device: 2, Info: &DeviceInfo{ID: 2}}
	for _, e := range []Event{add, first, other, second} {
		s.send(e)
	}
	if want := []Event{add, second, other}; !equalEvents(s.backlog, want) {
		t.Fatalf("backlog %+v, want the DeviceAdded, the newer remap in the place of the first, and the other device's", s.backlog)
	}
	s.send(Event{Kind: AxisMotion, Device: 1}) // dropped while events wait
	if s.Dropped() != 1 {
		t.Errorf("Dropped() = %d, want 1", s.Dropped())
	}
	s.send(Event{Kind: DeviceRemoved, Device: 2})
	for _, e := range s.backlog {
		if e.Kind == DeviceRemapped && e.Device == 2 {
			t.Errorf("a DeviceRemapped of a removed device still waits: %+v", s.backlog)
		}
	}
}

// A joystick that gets a mapping is reported twice with one id, the joystick
// and then the gamepad (IN-18), and one that loses it is removed and reported
// as a joystick again. These tests keep the line right when the consumer does
// not read, as the single DeviceAdded per device did before.

func addedAs(id DeviceID, gamepad bool) Event {
	return Event{Kind: DeviceAdded, Device: id, Info: &DeviceInfo{ID: id, IsGamepad: gamepad}}
}

// TestSendUpgradeDowngradeBeforeTheConsumerReads: nothing of a device reached
// the consumer, so its upgrade, its downgrade and a reconnect as a joystick
// leave one DeviceAdded and no DeviceRemoved.
func TestSendUpgradeDowngradeBeforeTheConsumerReads(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Kind: ButtonDown, Device: 9}) // fills Events
	s.send(addedAs(4, false))
	s.send(addedAs(4, true))
	s.send(Event{Kind: DeviceRemoved, Device: 4})
	if len(s.backlog) != 0 {
		t.Fatalf("backlog %+v after the removal of a device the consumer never heard of", s.backlog)
	}
	again := addedAs(4, false)
	s.send(again)
	if want := []Event{again}; !equalEvents(s.backlog, want) || s.backlog[0].Info.IsGamepad {
		t.Fatalf("backlog %+v, want the joystick again", s.backlog)
	}
}

// TestSendRemovalOfAnUpgradedDevice: the consumer has the joystick, the
// gamepad report still waits, and the device is unplugged. The removal must
// reach the consumer, which would keep a ghost joystick otherwise.
func TestSendRemovalOfAnUpgradedDevice(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	raw, gp, gone := addedAs(4, false), addedAs(4, true), Event{Kind: DeviceRemoved, Device: 4}
	s.send(raw) // delivered: it fills Events
	s.send(gp)  // waits
	s.send(gone)
	if want := []Event{gone}; !equalEvents(s.backlog, want) {
		t.Fatalf("backlog %+v, want the removal alone: the waiting gamepad report is of no use", s.backlog)
	}
	got := []Event{<-s.events}
	s.flush()
	got = append(got, drain(s)...)
	if want := []Event{raw, gone}; !equalEvents(got, want) {
		t.Errorf("delivered %+v, want %+v", got, want)
	}
}

// TestSendDowngradeOfADeviceTheConsumerKnows: the consumer has the gamepad;
// it loses its mapping (removed, joystick again) while it does not read, then
// the mapping comes back and goes again. The line stays short and the consumer
// ends with the device as a joystick.
func TestSendDowngradeOfADeviceTheConsumerKnows(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	gp, gone := addedAs(4, true), Event{Kind: DeviceRemoved, Device: 4}
	s.send(gp) // delivered
	for range 20 {
		s.send(gone)
		s.send(addedAs(4, false))
		s.send(addedAs(4, true))
		if len(s.backlog) > 3 {
			t.Fatalf("backlog grew to %d: %+v", len(s.backlog), s.backlog)
		}
	}
	s.send(gone)
	s.send(addedAs(4, false))
	// The consumer applies what it reads, as the router does.
	var known = map[DeviceID]bool{} // id -> gamepad
	apply := func(e Event) {
		switch e.Kind {
		case DeviceAdded:
			known[e.Device] = e.Info.IsGamepad
		case DeviceRemoved:
			delete(known, e.Device)
		}
	}
	apply(<-s.events)
	for len(s.backlog) > 0 {
		s.flush()
		apply(<-s.events)
	}
	if gamepad, ok := known[4]; !ok || gamepad {
		t.Errorf("the consumer ends with %v, want the device as a joystick", known)
	}
}

// TestSendTwoRemovalsOfOneDevice: a removal waits for the consumer, so a
// second removal of the device (after it came back and went again) adds
// nothing to the line.
func TestSendTwoRemovalsOfOneDevice(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	gone := Event{Kind: DeviceRemoved, Device: 4}
	s.send(addedAs(4, true))
	s.send(gone)
	s.send(addedAs(4, false))
	s.send(addedAs(4, true))
	s.send(gone)
	if want := []Event{gone}; !equalEvents(s.backlog, want) {
		t.Errorf("backlog %+v, want a single removal", s.backlog)
	}
}
