package slot_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// eventSlot is a slot on a Sim of its own that records the events Handle
// gets.
type eventSlot struct {
	*simtest.Sim
	s   *slot.Slot
	evs []input.Event
}

func newEventSlot(t *testing.T, prog *engine.Program) *eventSlot {
	t.Helper()
	es := &eventSlot{Sim: simtest.New(t)}
	s, err := slot.New(slot.Config{
		Gen: 1, Program: prog, Arbiter: es.Arb, Clock: es.Clock,
		OnEvent: func(ev input.Event) { es.evs = append(es.evs, ev) },
	})
	if err != nil {
		t.Fatal(err)
	}
	es.s = s
	return es
}

// finish releases everything and runs the deadlines left, so that the
// Sim finds nothing held.
func (es *eventSlot) finish(t *testing.T) {
	t.Helper()
	if err := es.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if err := es.s.Replay(es.Clock, nil); err != nil {
		t.Fatal(err)
	}
}

// handle handles events at the clock's Now and takes the events it
// recorded.
func (es *eventSlot) handle(evs ...input.Event) {
	for _, ev := range evs {
		es.s.Handle(ev)
	}
	es.evs = nil
}

// eventString formats ev as "+east", "-south" or "leftx=-100".
func eventString(ev input.Event) string {
	switch ev.Kind {
	case input.ButtonDown:
		return "+" + ev.Control
	case input.ButtonUp:
		return "-" + ev.Control
	case input.AxisMotion:
		return ev.Control + "=" + strconv.Itoa(int(ev.Value))
	default:
		return ev.Kind.String() + " " + ev.Control
	}
}

func eventsString(evs []input.Event) string {
	s := make([]string, len(evs))
	for i, ev := range evs {
		s[i] = eventString(ev)
	}
	return strings.Join(s, " ")
}

func axis(name string, v int16) input.Event {
	return input.Event{Kind: input.AxisMotion, Control: name, Value: v}
}

// stateWith returns the State with the buttons down and the axis values
// in set, "east" or "leftx=-100" each.
func stateWith(t *testing.T, set ...string) slot.State {
	t.Helper()
	var st slot.State
	for _, s := range set {
		if name, v, ok := strings.Cut(s, "="); ok {
			a, aok := control.ParseAxis(name)
			n, err := strconv.ParseInt(v, 10, 16)
			if !aok || err != nil {
				t.Fatalf("stateWith: bad axis %q", s)
			}
			st.Axes[a] = int16(n)
			continue
		}
		c, ok := control.Parse(s)
		if !ok {
			t.Fatalf("stateWith: bad button %q", s)
		}
		st.Buttons[c] = true
	}
	return st
}

func TestStateTracksEvents(t *testing.T) {
	prog := compile(t, eastHold, southTap, lbCtrl, ltAlt)
	for _, tc := range []struct {
		name    string
		evs     []input.Event
		want    []string // stateWith
		unknown int      // ErrUnknownInput errors
	}{
		{"downs and ups", []input.Event{button("south", true), button("east", true), button("south", false)}, []string{"east"}, 0},
		{"aliases", []input.Event{button("a", true), button("lb", true), button("lb", false)}, []string{"south"}, 0},
		{"axes", []input.Event{axis("leftx", -100), axis("lefttrigger", 20000), axis("righty", 5), axis("leftx", 7)},
			[]string{"leftx=7", "lefttrigger=20000", "righty=5"}, 0},
		{"unknown names", []input.Event{button("nosuch", true), axis("nosuch", 5), axis("east", 5)}, nil, 3},
		{"trigger as a button", []input.Event{button("lt", true), button("rt", true)}, nil, 2},
		{"unknown kind", []input.Event{{Kind: 99, Control: "east"}}, nil, 1},
		{"device removed", []input.Event{button("east", true), axis("leftx", 100), {Kind: input.DeviceRemoved}}, nil, 0},
		{"device added", []input.Event{button("east", true), {Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 2}}}, []string{"east"}, 0},
		{"removed, then more", []input.Event{button("east", true), {Kind: input.DeviceRemoved}, button("b", true), axis("leftx", 3)},
			[]string{"east", "leftx=3"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			es := newEventSlot(t, prog)
			es.handle(tc.evs...)
			want := stateWith(t, tc.want...)
			if got := es.s.State(); got != want {
				t.Errorf("State() = %+v\nwant %+v", got, want)
			}
			var applied slot.State
			for _, ev := range tc.evs {
				applied.Apply(ev)
			}
			if applied != want {
				t.Errorf("State.Apply over the events = %+v\nwant %+v", applied, want)
			}
			errs := es.s.Errors()
			unknown := 0
			for _, err := range errs {
				if errors.Is(err, slot.ErrUnknownInput) {
					unknown++
				}
			}
			if unknown != tc.unknown || len(errs) != unknown {
				t.Errorf("errors %v, want %d unknown inputs", errs, tc.unknown)
			}
			es.finish(t)
		})
	}
}

func TestResync(t *testing.T) {
	prog := compile(t, eastHold, southTap, lbCtrl, ltAlt)
	for _, tc := range []struct {
		name   string
		before []input.Event
		target func(t *testing.T, cur slot.State) slot.State
		events string // the events Resync handles
		want   []string
	}{{
		name:   "releases, presses, axes",
		before: []input.Event{button("east", true), button("west", true), axis("leftx", 100), axis("righty", -5)},
		target: func(t *testing.T, _ slot.State) slot.State {
			return stateWith(t, "south", "west", "lb", "dpad.up", "leftx=100", "lefty=7", "lefttrigger=20000")
		},
		events: "-east +south +leftshoulder +dpad.up lefty=7 righty=0 lefttrigger=20000",
	}, {
		name:   "releases in control order",
		before: []input.Event{button("lb", true), button("north", true), button("east", true)},
		target: func(t *testing.T, _ slot.State) slot.State { return stateWith(t, "start") },
		events: "-east -north -leftshoulder +start",
	}, {
		name:   "from zero",
		target: func(t *testing.T, _ slot.State) slot.State { return stateWith(t, "east", "lefttrigger=32767") },
		events: "+east lefttrigger=32767",
	}, {
		name:   "the current state",
		before: []input.Event{button("east", true), axis("lefttrigger", 20000)},
		target: func(_ *testing.T, cur slot.State) slot.State { return cur },
	}, {
		name:   "invalid indexes",
		before: []input.Event{button("east", true)},
		target: func(_ *testing.T, cur slot.State) slot.State {
			cur.Buttons[control.Invalid] = true
			cur.Buttons[control.LT] = true
			cur.Buttons[control.RT] = true
			cur.Axes[control.InvalidAxis] = 5
			return cur
		},
		want: []string{"east"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			es := newEventSlot(t, prog)
			ref := newEventSlot(t, prog) // handles the same events itself
			es.handle(tc.before...)
			ref.handle(tc.before...)
			before := simtest.FormatTrace(es.Out.Edges())
			target := tc.target(t, es.s.State())

			n := es.s.Resync(target)
			if got := eventsString(es.evs); got != tc.events {
				t.Errorf("Resync handled %q, want %q", got, tc.events)
			}
			if n != len(es.evs) {
				t.Errorf("Resync = %d, handled %d events", n, len(es.evs))
			}
			want := target
			if tc.want != nil {
				want = stateWith(t, tc.want...)
			}
			if got := es.s.State(); got != want {
				t.Errorf("State() = %+v\nwant %+v", got, want)
			}
			ref.handle(slices.Clone(es.evs)...)
			if got, want := simtest.FormatTrace(es.Out.Edges()), simtest.FormatTrace(ref.Out.Edges()); got != want {
				t.Errorf("output of Resync:\n got %s\nwant %s (handling the events)", got, want)
			}
			if tc.events == "" && simtest.FormatTrace(es.Out.Edges()) != before {
				t.Errorf("Resync to the current state sent %s", simtest.FormatTrace(es.Out.Edges()))
			}
			if n := es.s.Resync(target); n != 0 {
				t.Errorf("second Resync = %d, want 0", n)
			}
			es.finish(t)
			ref.finish(t)
		})
	}
}

func TestReset(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl, ltAlt)
	h.replay(t, "+east@0 +lb@0 lefttrigger=0.5@0")
	h.ExpectOutput("+Space@0 +LCtrl@0 +LAlt@0")
	h.at(t, 10)
	h.s.Reset()
	h.ExpectOutput("-Space@10 -LAlt@10 -LCtrl@10")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if st := h.s.State(); st != (slot.State{}) {
		t.Errorf("State() after Reset = %+v, want zero", st)
	}
	// The engine counts every control as up: the next press works.
	h.replay(t, "+east@20 -east@30")
	h.ExpectOutput("+Space@20 -Space@30")
	h.expectDecisions(t,
		"@0 east down dispatch", "@0 leftshoulder down dispatch", "@0 lt down dispatch",
		"@10 east none cancel", "@10 leftshoulder none cancel", "@10 lt none cancel",
		"@20 east down dispatch", "@30 east up release")
}
