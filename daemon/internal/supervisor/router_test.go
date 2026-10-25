package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// newTestRouter returns a router of n slots without runner goroutines:
// each route's edges stay in its channel, which holds 8.
func newTestRouter(n int) *router {
	r := &router{
		s:   &Supervisor{status: make([]SlotStatus, n)},
		gen: 1, log: slog.New(slog.DiscardHandler), reg: input.NewRegistry(),
		devices: map[input.DeviceID]*device{}, ignored: map[input.DeviceKey]bool{},
	}
	for i := range n {
		run := &runner{index: uint8(i), gen: 1, edges: make(chan slot.Edge, 8), wake: make(chan struct{}, 1)}
		r.routes = append(r.routes, &route{index: run.index, run: run})
	}
	return r
}

// taken returns the edges in the channel of rt.
func taken(rt *route) []slot.Edge {
	var out []slot.Edge
	for len(rt.run.edges) > 0 {
		out = append(out, <-rt.run.edges)
	}
	return out
}

// handoff is a slot's expected hand-off in TestHandoffOrder.
type handoff struct {
	id   input.DeviceID // 0: none
	time uint64
	st   slot.State
}

// TestHandoffOrder drives the router over two slots: the matching gamepads
// without a slot wait in the order they connected; a freed slot goes at
// once to the one that waited longest, with a copy of its DeviceAdded at
// the time of the removal and then a Resync to the router's state of it,
// provisionally, the slot's home unchanged; a gamepad that leaves while it
// waits just leaves the line; and a free slot, which has no home, goes to
// the next gamepad that connects, which becomes its home, provisionally
// too, until it presses a button.
func TestHandoffOrder(t *testing.T) {
	r := newTestRouter(2)
	infos := map[input.DeviceID]*input.DeviceInfo{}
	for id := range input.DeviceID(7) {
		infos[id] = &input.DeviceInfo{ID: id, Name: fmt.Sprintf("Pad %d", id), GUID: fmt.Sprintf("%032x", id), IsGamepad: true}
	}
	add := func(id input.DeviceID) input.Event {
		return input.Event{Time: uint64(id), Device: id, Kind: input.DeviceAdded, Info: infos[id]}
	}
	gone := func(id input.DeviceID, t uint64) input.Event {
		return input.Event{Time: t, Device: id, Kind: input.DeviceRemoved}
	}
	press := func(id input.DeviceID, down bool) input.Event {
		k := input.ButtonUp
		if down {
			k = input.ButtonDown
		}
		return input.Event{Device: id, Kind: k, Control: "north"}
	}
	for id := input.DeviceID(1); id <= 5; id++ {
		r.event(add(id))
	}
	// Pads 1 and 2 press a button, so that their slots are their own and
	// Pad 3's press takes neither over.
	for _, e := range []input.Event{press(1, true), press(1, false), press(2, true), press(2, false)} {
		r.event(e)
	}
	r.event(input.Event{Device: 3, Kind: input.ButtonDown, Control: "east"})
	r.event(input.Event{Device: 3, Kind: input.AxisMotion, Control: "leftx", Value: 900})
	var east slot.State
	east.Buttons[control.East] = true
	east.Axes[control.LeftX] = 900

	// step checks the edges each slot got, the gamepads that wait and the
	// status after an event: each slot's gamepad, the gamepad whose key is
	// its home, and whether it is provisional.
	step := func(name string, want [2][]slot.Edge, handed [2]handoff, waiting []input.DeviceID, slots [2]slotWant) {
		t.Helper()
		for i, rt := range r.routes {
			got := taken(rt)
			n := len(want[i])
			if handed[i].id != 0 {
				n += 2
			}
			if len(got) != n {
				t.Fatalf("%s: slot %d got %d edges %+v, want %d", name, i, len(got), got, n)
			}
			for j, e := range want[i] {
				if !reflect.DeepEqual(got[j], e) {
					t.Errorf("%s: slot %d edge %d: %+v, want %+v", name, i, j, got[j], e)
				}
			}
			if h := handed[i]; h.id != 0 {
				a, rs := got[len(want[i])], got[len(want[i])+1]
				e := a.Event
				if a.Reset || a.Resync != nil || e.Kind != input.DeviceAdded || e.Device != h.id || e.Time != h.time ||
					e.Info == nil || e.Info == infos[h.id] || *e.Info != *infos[h.id] {
					t.Errorf("%s: slot %d: %+v, want a copy of the DeviceAdded of %d at %d", name, i, a, h.id, h.time)
				}
				if rs.Resync == nil || *rs.Resync != h.st {
					t.Errorf("%s: slot %d: %+v, want a Resync to the state of %d", name, i, rs, h.id)
				}
			}
		}
		var ids []input.DeviceID
		for _, d := range r.queue {
			ids = append(ids, d.id)
		}
		if !slices.Equal(ids, waiting) {
			t.Errorf("%s: waiting %v, want %v", name, ids, waiting)
		}
		for i, st := range r.s.Status().Slots {
			var want SlotStatus
			if w := slots[i]; w.id != 0 {
				want = SlotStatus{
					Key: input.DeviceKey{GUID: infos[w.id].GUID}, ID: w.id, Name: infos[w.id].Name,
					Home: input.DeviceKey{GUID: infos[w.home].GUID}, Provisional: w.prov,
				}
			}
			if st != want {
				t.Errorf("%s: slot %d status %+v, want %+v", name, i, st, want)
			}
		}
	}
	none := [2]handoff{}
	pressed := func(id input.DeviceID) []slot.Edge {
		first := add(id)
		first.AssignedKey = infos[id].GUID
		return []slot.Edge{{Event: first}, {Event: press(id, true)}, {Event: press(id, false)}}
	}
	step("connected", [2][]slot.Edge{pressed(1), pressed(2)}, none, []input.DeviceID{3, 4, 5}, [2]slotWant{{1, 1, false}, {2, 2, false}})
	r.event(gone(4, 40))
	step("waiting leaves", [2][]slot.Edge{}, none, []input.DeviceID{3, 5}, [2]slotWant{{1, 1, false}, {2, 2, false}})
	r.event(gone(2, 50))
	step("slot 1 freed", [2][]slot.Edge{nil, {{Event: gone(2, 50)}}}, [2]handoff{1: {3, 50, east}}, []input.DeviceID{5}, [2]slotWant{{1, 1, false}, {3, 2, true}})
	r.event(gone(1, 60))
	step("slot 0 freed", [2][]slot.Edge{{{Event: gone(1, 60)}}}, [2]handoff{0: {5, 60, slot.State{}}}, nil, [2]slotWant{{5, 1, true}, {3, 2, true}})
	r.event(gone(3, 70))
	step("nobody waits", [2][]slot.Edge{nil, {{Event: gone(3, 70)}}}, none, nil, [2]slotWant{{5, 1, true}, {}})
	r.event(add(6))
	last := add(6)
	last.AssignedKey = infos[6].GUID
	step("free slot taken", [2][]slot.Edge{nil, {{Event: last}}}, none, nil, [2]slotWant{{5, 1, true}, {6, 6, true}})
}

// slotWant is the status of a slot after a step of TestHandoffOrder: its
// gamepad (0 for none), the gamepad whose key is its home, and whether it
// is provisional.
type slotWant struct {
	id, home input.DeviceID
	prov     bool
}

// policyWant is the status of the slot after a step of TestPolicy.
type policyWant struct {
	id   input.DeviceID // its gamepad; 0 for none
	home string         // the DeviceKey string of its home
	prov bool           // provisional
}

// policyStep is an event of TestPolicy and what the router makes of it:
// the edges the slot gets, as fmtEdges writes them, the gamepads that wait,
// in order, and the slot's status.
type policyStep struct {
	ev    input.Event
	edges string
	queue []input.DeviceID
	slot  policyWant
}

// fmtEdges writes edges as "<kind><device>[:<control>][@<time>]", the time
// for DeviceAdded and DeviceRemoved only, "resync" and "reset".
func fmtEdges(edges []slot.Edge) string {
	var fs []string
	for _, e := range edges {
		switch {
		case e.Reset:
			fs = append(fs, "reset")
		case e.Resync != nil:
			fs = append(fs, "resync")
		default:
			ev := e.Event
			f := fmt.Sprintf("%v%d", ev.Kind, ev.Device)
			if ev.Control != "" {
				f += ":" + ev.Control
			}
			if ev.Kind == input.DeviceAdded || ev.Kind == input.DeviceRemoved {
				f += fmt.Sprintf("@%d", ev.Time)
			}
			fs = append(fs, f)
		}
	}
	return strings.Join(fs, " ")
}

// TestPolicy drives the router over one slot through the assignment
// policy (IN-05): every assignment is provisional until the gamepad it
// went to presses a button, and until then the slot's home takes it back
// when it reconnects, if another gamepad has it, and a gamepad that waits
// takes it over when it presses a button, the holder waiting again at the
// front of the line; a take-over is final at once.
func TestPolicy(t *testing.T) {
	const a, b, c, d, o, twin = "03ab", "057e", "0c0c", "0d0d", "0f0f", "0e0e"
	names := map[string]string{
		a: "Pad A", b: "Pad B", c: "Pad C", d: "Pad D", o: "XInput Controller #1", twin: "Pad", "x" + a: "Xbox",
	}
	add := func(id input.DeviceID, guid string, at uint64) input.Event {
		info := &input.DeviceInfo{ID: id, Name: names[guid], GUID: strings.TrimPrefix(guid, "x"), IsGamepad: true}
		return input.Event{Time: at, Device: id, Kind: input.DeviceAdded, Info: info}
	}
	gone := func(id input.DeviceID, at uint64) input.Event {
		return input.Event{Time: at, Device: id, Kind: input.DeviceRemoved}
	}
	down := func(id input.DeviceID, c string) input.Event {
		return input.Event{Device: id, Kind: input.ButtonDown, Control: c}
	}
	up := func(id input.DeviceID, c string) input.Event {
		return input.Event{Device: id, Kind: input.ButtonUp, Control: c}
	}
	axis := func(id input.DeviceID, c string, v int16) input.Event {
		return input.Event{Device: id, Kind: input.AxisMotion, Control: c, Value: v}
	}
	for _, tc := range []struct {
		name   string
		device string // Config.Device
		steps  []policyStep
	}{
		{"startup", "", []policyStep{
			// An idle gamepad that connected first, such as a virtual one,
			// takes the free slot provisionally; its sticks jitter.
			{add(1, o, 0), "added1@0", nil, policyWant{1, o, true}},
			{axis(1, "leftx", 300), "axis1:leftx", nil, policyWant{1, o, true}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, o, true}},
			// The first press of the gamepad in use takes the slot over and
			// reaches it; the idle one waits first.
			{down(2, "south"), "removed1@0 added2@0 resync down2:south", []input.DeviceID{1}, policyWant{2, b, false}},
			{up(2, "south"), "up2:south", []input.DeviceID{1}, policyWant{2, b, false}},
			// It sleeps: the idle one stands in, until it comes back.
			{gone(2, 30), "removed2@30 added1@30 resync", nil, policyWant{1, b, true}},
			{add(3, b, 40), "removed1@40 added3@40", []input.DeviceID{1}, policyWant{3, b, true}},
		}},
		{"reclaim", "", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{down(1, "south"), "down1:south", nil, policyWant{1, a, false}},
			{up(1, "south"), "up1:south", nil, policyWant{1, a, false}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, false}},
			// Pad A pressed a button: Pad B's press takes nothing.
			{down(2, "east"), "", []input.DeviceID{2}, policyWant{1, a, false}},
			{gone(1, 20), "removed1@20 added2@20 resync", nil, policyWant{2, a, true}},
			// Neither an axis nor a release confirms the hand-off.
			{axis(2, "leftx", 900), "axis2:leftx", nil, policyWant{2, a, true}},
			{axis(2, "righttrigger", 900), "axis2:righttrigger", nil, policyWant{2, a, true}},
			{up(2, "east"), "up2:east", nil, policyWant{2, a, true}},
			// Pad A comes back: Pad B's slot forgets it, and it waits first.
			{add(3, a, 30), "removed2@30 added3@30", []input.DeviceID{2}, policyWant{3, a, true}},
			// Pad A pressed nothing since: Pad B's press takes the slot over
			// again.
			{down(2, "east"), "removed3@0 added2@0 resync down2:east", []input.DeviceID{3}, policyWant{2, b, false}},
			{gone(3, 40), "", nil, policyWant{2, b, false}},
		}},
		{"confirmed", "", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, true}},
			{gone(1, 20), "removed1@20 added2@20 resync", nil, policyWant{2, a, true}},
			// Any button confirms, bound or not.
			{down(2, "leftshoulder"), "down2:leftshoulder", nil, policyWant{2, b, false}},
			{add(3, a, 30), "", []input.DeviceID{3}, policyWant{2, b, false}},
			// The slot now belongs to Pad B, which takes it back.
			{gone(2, 40), "removed2@40 added3@40 resync", nil, policyWant{3, b, true}},
			{add(4, b, 50), "removed3@50 added4@50", []input.DeviceID{3}, policyWant{4, b, true}},
			// Its press confirms it again; Pad A's then takes nothing.
			{down(4, "south"), "down4:south", []input.DeviceID{3}, policyWant{4, b, false}},
			{down(3, "south"), "", []input.DeviceID{3}, policyWant{4, b, false}},
		}},
		{"chain", "", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, true}},
			{add(3, c, 20), "", []input.DeviceID{2, 3}, policyWant{1, a, true}},
			{gone(1, 30), "removed1@30 added2@30 resync", []input.DeviceID{3}, policyWant{2, a, true}},
			{gone(2, 40), "removed2@40 added3@40 resync", nil, policyWant{3, a, true}},
			// Pad B comes back, but the slot's home is Pad A.
			{add(4, b, 50), "", []input.DeviceID{4}, policyWant{3, a, true}},
			// Pad A takes it back; Pad C, which loses the slot, waits first.
			{add(5, a, 60), "removed3@60 added5@60", []input.DeviceID{3, 4}, policyWant{5, a, true}},
		}},
		{"front of the line", "", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{down(1, "south"), "down1:south", nil, policyWant{1, a, false}},
			{up(1, "south"), "up1:south", nil, policyWant{1, a, false}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, false}},
			{add(3, c, 20), "", []input.DeviceID{2, 3}, policyWant{1, a, false}},
			{gone(1, 30), "removed1@30 added2@30 resync", []input.DeviceID{3}, policyWant{2, a, true}},
			{add(4, a, 40), "removed2@40 added4@40", []input.DeviceID{2, 3}, policyWant{4, a, true}},
			{down(3, "east"), "removed4@0 added3@0 resync down3:east", []input.DeviceID{4, 2}, policyWant{3, c, false}},
			// A gamepad that lost the slot waits before one that connected
			// earlier: the line is not in the order they connected.
			{gone(3, 50), "removed3@50 added4@50 resync", []input.DeviceID{2}, policyWant{4, c, true}},
		}},
		{"free slot", "", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, true}},
			{gone(1, 20), "removed1@20 added2@20 resync", nil, policyWant{2, a, true}},
			// Nobody waits: the slot is free, with no home.
			{gone(2, 30), "removed2@30", nil, policyWant{}},
			// Pad D takes it and becomes its home; Pad A waits.
			{add(3, d, 40), "added3@40", nil, policyWant{3, d, true}},
			{add(4, a, 50), "", []input.DeviceID{4}, policyWant{3, d, true}},
			// Pad D presses first: the slot is its own, and Pad A's press
			// takes nothing.
			{down(3, "south"), "down3:south", []input.DeviceID{4}, policyWant{3, d, false}},
			{down(4, "east"), "", []input.DeviceID{4}, policyWant{3, d, false}},
		}},
		{"twins", "", []policyStep{
			{add(1, twin, 0), "added1@0", nil, policyWant{1, twin, true}},
			{add(2, twin, 10), "", []input.DeviceID{2}, policyWant{1, twin, true}},
			{add(3, twin, 20), "", []input.DeviceID{2, 3}, policyWant{1, twin, true}},
			{gone(1, 30), "removed1@30 added2@30 resync", []input.DeviceID{3}, policyWant{2, twin, true}},
			{gone(2, 40), "removed2@40 added3@40 resync", nil, policyWant{3, twin, true}},
			// A twin that connects gets the key of the first twin that left,
			// the home: it takes the slot back.
			{add(4, twin, 50), "removed3@50 added4@50", []input.DeviceID{3}, policyWant{4, twin, true}},
			// The next gets #1: not the home.
			{add(5, twin, 60), "", []input.DeviceID{3, 5}, policyWant{4, twin, true}},
			{gone(4, 70), "removed4@70 added3@70 resync", []input.DeviceID{5}, policyWant{3, twin, true}},
			{down(3, "east"), "down3:east", []input.DeviceID{5}, policyWant{3, twin + "#2", false}},
			// Its key is #0 again, and the home is #2.
			{add(6, twin, 80), "", []input.DeviceID{5, 6}, policyWant{3, twin + "#2", false}},
		}},
		{"twins take over", "", []policyStep{
			{add(1, twin, 0), "added1@0", nil, policyWant{1, twin, true}},
			// The twin that presses takes the slot over from the idle one.
			{add(2, twin, 10), "", []input.DeviceID{2}, policyWant{1, twin, true}},
			{down(2, "south"), "removed1@0 added2@0 resync down2:south", []input.DeviceID{1}, policyWant{2, twin + "#1", false}},
			{gone(2, 20), "removed2@20 added1@20 resync", nil, policyWant{1, twin + "#1", true}},
			// It comes back as #1, the home, and takes the slot back.
			{add(3, twin, 30), "removed1@30 added3@30", []input.DeviceID{1}, policyWant{3, twin + "#1", true}},
			// It pressed nothing since: the other twin's press takes the slot.
			{down(1, "east"), "removed3@0 added1@0 resync down1:east", []input.DeviceID{3}, policyWant{1, twin, false}},
		}},
		{"filter", "pad", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, true}},
			{gone(1, 20), "removed1@20 added2@20 resync", nil, policyWant{2, a, true}},
			// Pad A's key, but not a match: ignored, and its press takes
			// nothing over.
			{add(3, "x"+a, 30), "", nil, policyWant{2, a, true}},
			{down(3, "east"), "", nil, policyWant{2, a, true}},
		}},
		{"take over", "", []policyStep{
			{add(1, a, 0), "added1@0", nil, policyWant{1, a, true}},
			{down(1, "west"), "down1:west", nil, policyWant{1, a, false}},
			{add(2, b, 10), "", []input.DeviceID{2}, policyWant{1, a, false}},
			{add(3, c, 20), "", []input.DeviceID{2, 3}, policyWant{1, a, false}},
			// A press of a gamepad that waits takes nothing from a slot
			// whose gamepad pressed a button.
			{down(3, "east"), "", []input.DeviceID{2, 3}, policyWant{1, a, false}},
			{gone(1, 30), "removed1@30 added2@30 resync", []input.DeviceID{3}, policyWant{2, a, true}},
			// Neither an axis, a trigger pulled all the way included, nor a
			// release takes the slot over.
			{axis(3, "leftx", 900), "", []input.DeviceID{3}, policyWant{2, a, true}},
			{axis(3, "lefttrigger", 32767), "", []input.DeviceID{3}, policyWant{2, a, true}},
			{axis(3, "lefttrigger", 0), "", []input.DeviceID{3}, policyWant{2, a, true}},
			{up(3, "east"), "", []input.DeviceID{3}, policyWant{2, a, true}},
			// A press does: the slot forgets Pad B, which waits first, and
			// becomes Pad C's own; the press follows the Resync.
			{down(3, "south"), "removed2@0 added3@0 resync down3:south", []input.DeviceID{2}, policyWant{3, c, false}},
			// Pad A comes back and waits.
			{add(4, a, 40), "", []input.DeviceID{2, 4}, policyWant{3, c, false}},
			{gone(3, 50), "removed3@50 added2@50 resync", []input.DeviceID{4}, policyWant{2, c, true}},
			{down(4, "east"), "removed2@0 added4@0 resync down4:east", []input.DeviceID{2}, policyWant{4, a, false}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(1)
			r.s.cfg.Device = tc.device
			rt := r.routes[0]
			for i, s := range tc.steps {
				r.event(s.ev)
				if got := fmtEdges(taken(rt)); got != s.edges {
					t.Errorf("step %d (%v %d): edges %q, want %q", i+1, s.ev.Kind, s.ev.Device, got, s.edges)
				}
				var ids []input.DeviceID
				for _, d := range r.queue {
					ids = append(ids, d.id)
				}
				if !slices.Equal(ids, s.queue) {
					t.Errorf("step %d (%v %d): waiting %v, want %v", i+1, s.ev.Kind, s.ev.Device, ids, s.queue)
				}
				st := r.s.Status().Slots[0]
				want := SlotStatus{ID: s.slot.id, Provisional: s.slot.prov}
				if dev := r.devices[s.slot.id]; dev != nil {
					want.Key, want.Name = dev.key, dev.info.Name
					if want.Home = st.Home; st.Home.String() != s.slot.home {
						t.Errorf("step %d (%v %d): home %v, want %s", i+1, s.ev.Kind, s.ev.Device, st.Home, s.slot.home)
					}
				}
				if st != want {
					t.Errorf("step %d (%v %d): status %+v, want %+v", i+1, s.ev.Kind, s.ev.Device, st, want)
				}
			}
		})
	}
}

// testAdd and testGone are the DeviceAdded of gamepad id, named after its
// guid, and its DeviceRemoved, at time at.
func testAdd(id input.DeviceID, guid string, at uint64) input.Event {
	info := &input.DeviceInfo{ID: id, Name: "Pad " + guid, GUID: guid, IsGamepad: true}
	return input.Event{Time: at, Device: id, Kind: input.DeviceAdded, Info: info}
}

func testGone(id input.DeviceID, at uint64) input.Event {
	return input.Event{Time: at, Device: id, Kind: input.DeviceRemoved}
}

// TestPressDisabled: a press of the stand-in while its slot is disabled
// does not confirm the hand-off, since the slot maps nothing; so the home
// that reconnects still takes the slot back, provisionally, and a gamepad
// that waits still takes it over with a press, either of which enables it
// again.
func TestPressDisabled(t *testing.T) {
	const a, b, c = "0a0a", "0b0b", "0c0c"
	for _, tc := range []struct {
		name  string
		ev    input.Event
		edges string // after the enable; the fresh slot gets no removal of Pad B
		id    input.DeviceID
		home  string
		prov  bool
	}{
		{"reclaim", testAdd(4, a, 40), "added4@40", 4, a, true},
		{"take over", input.Event{Time: 40, Device: 3, Kind: input.ButtonDown, Control: "east"}, "added3@40 resync down3:east", 3, c, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(1)
			rt := r.routes[0]
			for _, e := range []input.Event{testAdd(1, a, 0), testAdd(2, b, 10), testAdd(3, c, 20), testGone(1, 30)} {
				r.event(e)
			}
			if got, want := fmtEdges(taken(rt)), "added1@0 removed1@30 added2@30 resync"; got != want {
				t.Fatalf("edges %q, want %q", got, want)
			}
			r.disable(rt) // the dead slot left no edge: it stays disabled
			if !rt.disabled {
				t.Fatal("the slot is not disabled")
			}
			r.event(input.Event{Time: 35, Device: 2, Kind: input.ButtonDown, Control: "south"})
			r.event(input.Event{Time: 36, Device: 2, Kind: input.ButtonUp, Control: "south"})
			if st := r.s.Status().Slots[0]; st.ID != 2 || !st.Provisional || st.Home.String() != a {
				t.Fatalf("status %+v after Pad B's press on the disabled slot, want it provisional, home %s", st, a)
			}
			if got := taken(rt); len(got) != 0 {
				t.Fatalf("the disabled slot got %+v", got)
			}
			r.event(tc.ev)
			if msgs := rt.run.take(); rt.disabled || len(msgs) != 1 || !msgs[0].enable {
				t.Errorf("disabled %v, posted %+v; want the slot enabled", rt.disabled, msgs)
			}
			if got := fmtEdges(taken(rt)); got != tc.edges {
				t.Errorf("edges %q, want %q", got, tc.edges)
			}
			if st := r.s.Status().Slots[0]; st.ID != tc.id || st.Provisional != tc.prov || st.Home.String() != tc.home {
				t.Errorf("status %+v, want gamepad %d, home %s, provisional %v", st, tc.id, tc.home, tc.prov)
			}
			if len(r.queue) == 0 || r.queue[0].id != 2 {
				t.Errorf("waiting %+v, want Pad B first", r.queue)
			}
		})
	}
}

// checkInvariants asserts the router's invariants (IN-08) on its status,
// its line and its connected gamepads.
func checkInvariants(t *testing.T, r *router) {
	t.Helper()
	slots := r.s.Status().Slots
	for i := range slots {
		slots[i].Index = uint8(i) // newTestRouter leaves every Index 0
	}
	var matching []input.DeviceKey
	for _, d := range r.devices {
		if r.matches(d) {
			matching = append(matching, d.key)
		}
	}
	if err := CheckInvariants(slots, matching, len(r.queue)); err != nil {
		t.Error(err)
	}
}

// checkSlots checks, after the step name of a router test, the edges each
// slot got, as fmtEdges writes them, the status of each slot, the gamepads
// that wait, in order, and the invariants. A slot past edges or slots gets
// no edge or is free. resync, when set, is the state of every Resync.
func checkSlots(t *testing.T, r *router, name string, edges []string, slots []policyWant, waiting []input.DeviceID, resync *slot.State) {
	t.Helper()
	for i, rt := range r.routes {
		var want string
		if i < len(edges) {
			want = edges[i]
		}
		got := taken(rt)
		if f := fmtEdges(got); f != want {
			t.Errorf("%s: slot %d edges %q, want %q", name, i, f, want)
		}
		for _, e := range got {
			if resync != nil && e.Resync != nil && *e.Resync != *resync {
				t.Errorf("%s: slot %d: Resync %+v, want %+v", name, i, *e.Resync, *resync)
			}
		}
	}
	for i, st := range r.s.Status().Slots {
		var w policyWant
		if i < len(slots) {
			w = slots[i]
		}
		want := SlotStatus{ID: w.id, Provisional: w.prov}
		if dev := r.devices[w.id]; dev != nil {
			want.Key, want.Name = dev.key, dev.info.Name
		}
		if want.Home = st.Home; st.Home.String() != w.home {
			t.Errorf("%s: slot %d home %v, want %s", name, i, st.Home, w.home)
		}
		if st != want {
			t.Errorf("%s: slot %d status %+v, want %+v", name, i, st, want)
		}
	}
	var ids []input.DeviceID
	for _, d := range r.queue {
		ids = append(ids, d.id)
	}
	if !slices.Equal(ids, waiting) {
		t.Errorf("%s: waiting %v, want %v", name, ids, waiting)
	}
	checkInvariants(t, r)
}

// logLine is a line the router logged, its attributes fmt.Sprint'ed.
type logLine struct {
	msg   string
	attrs map[string]string
}

// lineHandler keeps the lines logged through it. The router tests run no
// goroutines, so it takes no lock.
type lineHandler struct{ lines *[]logLine }

func (lineHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h lineHandler) Handle(_ context.Context, rec slog.Record) error {
	l := logLine{msg: rec.Message, attrs: map[string]string{}}
	rec.Attrs(func(a slog.Attr) bool {
		l.attrs[a.Key] = fmt.Sprint(a.Value.Resolve().Any())
		return true
	})
	*h.lines = append(*h.lines, l)
	return nil
}

func (lineHandler) WithAttrs([]slog.Attr) slog.Handler { panic("lineHandler: WithAttrs is not used") }
func (lineHandler) WithGroup(string) slog.Handler      { panic("lineHandler: WithGroup is not used") }

// linesWith returns the lines of lines with message msg.
func linesWith(lines []logLine, msg string) []logLine {
	var out []logLine
	for _, l := range lines {
		if l.msg == msg {
			out = append(out, l)
		}
	}
	return out
}

// TestRehomeFree drives the router over two slots: a gamepad that
// reconnects while a slot is free takes that slot, and the stand-in on the
// slot whose home is its key keeps its slot, which gets no edge, so that
// what the stand-in holds stays held, and becomes its home, still
// provisionally (IN-08). No slot is free then, so the next gamepad to
// connect waits.
func TestRehomeFree(t *testing.T) {
	const a, b, c, d = "0a0a", "0b0b", "0c0c", "0d0d"
	r := newTestRouter(2)
	var lines []logLine
	r.log = slog.New(lineHandler{&lines})
	for _, e := range []input.Event{
		testAdd(1, a, 0), testAdd(2, b, 10), testAdd(3, c, 20),
		// Pads A and B press a button, so that Pad C's press takes neither
		// slot over.
		{Time: 21, Device: 1, Kind: input.ButtonDown, Control: "north"},
		{Time: 22, Device: 2, Kind: input.ButtonDown, Control: "north"},
		{Time: 25, Device: 3, Kind: input.ButtonDown, Control: "east"},
		testGone(1, 30), testGone(2, 40),
	} {
		r.event(e)
	}
	var east slot.State
	east.Buttons[control.East] = true
	// Pad C stands in for Pad A on slot 0, holding east; slot 1 is free.
	checkSlots(t, r, "before", []string{"added1@0 down1:north removed1@30 added3@30 resync", "added2@10 down2:north removed2@40"},
		[]policyWant{{3, a, true}, {}}, nil, &east)
	r.event(testAdd(4, a, 50))
	checkSlots(t, r, "rehome", []string{"", "added4@50"}, []policyWant{{3, c, true}, {4, a, true}}, nil, nil)
	if got := linesWith(lines, "slot rehomed"); len(got) != 1 ||
		got[0].attrs["slot"] != "0" || got[0].attrs["id"] != "3" || got[0].attrs["device"] != c || got[0].attrs["home"] != a {
		t.Errorf("slot rehomed lines %+v, want one of slot 0, Pad C, home %s", got, a)
	}
	r.event(testAdd(5, d, 60))
	checkSlots(t, r, "next", nil, []policyWant{{3, c, true}, {4, a, true}}, []input.DeviceID{5}, nil)
}

// TestReclaimFull drives the router over two slots, both taken: a gamepad
// that reconnects takes its home slot back from the stand-in there, which
// waits again, first; the other slot gets nothing (ADR-0019 decision 4).
func TestReclaimFull(t *testing.T) {
	const a, b, c = "0a0a", "0b0b", "0c0c"
	r := newTestRouter(2)
	for _, e := range []input.Event{
		testAdd(1, a, 0), testAdd(2, b, 10),
		{Time: 11, Device: 1, Kind: input.ButtonDown, Control: "north"},
		{Time: 12, Device: 2, Kind: input.ButtonDown, Control: "north"},
		testAdd(3, c, 20),
		// No slot is provisional: Pad C's press takes nothing over.
		{Time: 25, Device: 3, Kind: input.ButtonDown, Control: "east"},
		testGone(1, 30),
	} {
		r.event(e)
	}
	var east slot.State
	east.Buttons[control.East] = true
	checkSlots(t, r, "before", []string{"added1@0 down1:north removed1@30 added3@30 resync", "added2@10 down2:north"},
		[]policyWant{{3, a, true}, {2, b, false}}, nil, &east)
	r.event(testAdd(4, a, 50))
	checkSlots(t, r, "reclaim", []string{"removed3@50 added4@50"}, []policyWant{{4, a, true}, {2, b, false}}, []input.DeviceID{3}, nil)
}

// TestTakeOverOldest drives the router over two slots: a press of a
// gamepad that waits takes over the provisional slot assigned longest ago,
// slot 1 here, not the one of the lowest index (IN-08); the next press
// takes the other.
func TestTakeOverOldest(t *testing.T) {
	const a, b, c, d = "0a0a", "0b0b", "0c0c", "0d0d"
	r := newTestRouter(2)
	for _, e := range []input.Event{testAdd(1, a, 0), testAdd(2, b, 10), testAdd(3, c, 20), testGone(1, 30), testAdd(4, d, 40)} {
		r.event(e)
	}
	// Pad C was handed slot 0 after Pad B got slot 1.
	checkSlots(t, r, "before", []string{"added1@0 removed1@30 added3@30 resync", "added2@10"},
		[]policyWant{{3, a, true}, {2, b, true}}, []input.DeviceID{4}, &slot.State{})
	if s0, s1 := r.routes[0].seq, r.routes[1].seq; s0 != 3 || s1 != 2 {
		t.Errorf("seqs %d, %d; want 3, 2", s0, s1)
	}
	r.event(input.Event{Time: 45, Device: 4, Kind: input.ButtonDown, Control: "south"})
	checkSlots(t, r, "Pad D presses", []string{"", "removed2@45 added4@45 resync down4:south"},
		[]policyWant{{3, a, true}, {4, d, false}}, []input.DeviceID{2}, &slot.State{})
	r.event(input.Event{Time: 50, Device: 2, Kind: input.ButtonDown, Control: "south"})
	checkSlots(t, r, "Pad B presses", []string{"removed3@50 added2@50 resync down2:south"},
		[]policyWant{{2, b, false}, {4, d, false}}, []input.DeviceID{3}, &slot.State{})
}

// slotsStep is an event of TestPolicySlots and what the router makes of
// it, as checkSlots checks it; before, when set, runs first.
type slotsStep struct {
	before func(*router)
	ev     input.Event
	edges  []string
	queue  []input.DeviceID
	slots  []policyWant
}

// TestPolicySlots drives the router over several slots through the
// assignment policy (IN-08): a gamepad that connects takes the free slot
// of the lowest index, and a stand-in for its key is rehomed; it takes its
// home slot back only when every slot is taken; a press of a gamepad that
// waits takes over the provisional slot assigned longest ago; a hand-off
// keeps the slot's home; a gamepad on a disabled slot stays there; and one
// that does not match the filter takes no slot. The invariants hold after
// every step.
func TestPolicySlots(t *testing.T) {
	const a, b, c, d, e, f, g = "0a0a", "0b0b", "0c0c", "0d0d", "0e0e", "0f0f", "1a1a"
	// add is testAdd; a guid prefixed with x names the gamepad "Xbox".
	add := func(id input.DeviceID, guid string, at uint64) input.Event {
		ev := testAdd(id, strings.TrimPrefix(guid, "x"), at)
		if strings.HasPrefix(guid, "x") {
			ev.Info.Name = "Xbox"
		}
		return ev
	}
	down := func(id input.DeviceID, c string, at uint64) input.Event {
		return input.Event{Time: at, Device: id, Kind: input.ButtonDown, Control: c}
	}
	disable := func(i int) func(*router) {
		// The dead slot left no edge: it stays disabled.
		return func(r *router) { r.disable(r.routes[i]) }
	}
	for _, tc := range []struct {
		name   string
		slots  int
		device string // Config.Device
		steps  []slotsStep
	}{
		{"startup", 3, "", []slotsStep{
			// The gamepads take the slots in the order they connect.
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: add(3, c, 20), edges: []string{"", "", "added3@20"}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: add(4, d, 30), queue: []input.DeviceID{4}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: down(2, "south", 35), edges: []string{"", "down2:south"}, queue: []input.DeviceID{4},
				slots: []policyWant{{1, a, true}, {2, b, false}, {3, c, true}}},
			// Pad D's press takes slot 0, the oldest provisional one; Pad A's
			// then takes slot 2.
			{ev: down(4, "east", 40), edges: []string{"removed1@40 added4@40 resync down4:east"}, queue: []input.DeviceID{1},
				slots: []policyWant{{4, d, false}, {2, b, false}, {3, c, true}}},
			{ev: down(1, "south", 45), edges: []string{"", "", "removed3@45 added1@45 resync down1:south"}, queue: []input.DeviceID{3},
				slots: []policyWant{{4, d, false}, {2, b, false}, {1, a, false}}},
			// None is provisional now.
			{ev: down(3, "south", 50), queue: []input.DeviceID{3}, slots: []policyWant{{4, d, false}, {2, b, false}, {1, a, false}}},
		}},
		{"lowest free", 3, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: add(3, c, 20), edges: []string{"", "", "added3@20"}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: testGone(2, 30), edges: []string{"", "removed2@30"}, slots: []policyWant{{1, a, true}, {}, {3, c, true}}},
			{ev: testGone(1, 40), edges: []string{"removed1@40"}, slots: []policyWant{{}, {}, {3, c, true}}},
			// Slot 0, not slot 1, which was freed first.
			{ev: add(4, d, 50), edges: []string{"added4@50"}, slots: []policyWant{{4, d, true}, {}, {3, c, true}}},
			{ev: add(5, e, 60), edges: []string{"", "added5@60"}, slots: []policyWant{{4, d, true}, {5, e, true}, {3, c, true}}},
			{ev: add(6, f, 70), queue: []input.DeviceID{6}, slots: []policyWant{{4, d, true}, {5, e, true}, {3, c, true}}},
		}},
		{"rehome", 2, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: add(3, c, 20), queue: []input.DeviceID{3}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: down(1, "north", 21), edges: []string{"down1:north"}, queue: []input.DeviceID{3}, slots: []policyWant{{1, a, false}, {2, b, true}}},
			{ev: down(2, "north", 22), edges: []string{"", "down2:north"}, queue: []input.DeviceID{3}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: down(3, "east", 25), queue: []input.DeviceID{3}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: testGone(2, 30), edges: []string{"", "removed2@30 added3@30 resync"}, slots: []policyWant{{1, a, false}, {3, b, true}}},
			{ev: testGone(1, 40), edges: []string{"removed1@40"}, slots: []policyWant{{}, {3, b, true}}},
			// Pad B takes slot 0; Pad C keeps slot 1, now its home, with the
			// age of its hand-off.
			{ev: add(4, b, 50), edges: []string{"added4@50"}, slots: []policyWant{{4, b, true}, {3, c, true}}},
			{ev: add(5, d, 60), queue: []input.DeviceID{5}, slots: []policyWant{{4, b, true}, {3, c, true}}},
			// So Pad D's press takes slot 1, not slot 0, assigned later.
			{ev: down(5, "south", 65), edges: []string{"", "removed3@65 added5@65 resync down5:south"}, queue: []input.DeviceID{3},
				slots: []policyWant{{4, b, true}, {5, d, false}}},
		}},
		{"rehome keeps the age", 3, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: add(3, c, 20), edges: []string{"", "", "added3@20"}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: down(1, "north", 21), edges: []string{"down1:north"}, slots: []policyWant{{1, a, false}, {2, b, true}, {3, c, true}}},
			{ev: down(2, "north", 22), edges: []string{"", "down2:north"}, slots: []policyWant{{1, a, false}, {2, b, false}, {3, c, true}}},
			{ev: down(3, "north", 23), edges: []string{"", "", "down3:north"}, slots: []policyWant{{1, a, false}, {2, b, false}, {3, c, false}}},
			{ev: add(4, d, 30), queue: []input.DeviceID{4}, slots: []policyWant{{1, a, false}, {2, b, false}, {3, c, false}}},
			{ev: add(5, e, 40), queue: []input.DeviceID{4, 5}, slots: []policyWant{{1, a, false}, {2, b, false}, {3, c, false}}},
			// Slot 0 is handed on before slot 2.
			{ev: testGone(1, 50), edges: []string{"removed1@50 added4@50 resync"}, queue: []input.DeviceID{5},
				slots: []policyWant{{4, a, true}, {2, b, false}, {3, c, false}}},
			{ev: testGone(3, 60), edges: []string{"", "", "removed3@60 added5@60 resync"}, slots: []policyWant{{4, a, true}, {2, b, false}, {5, c, true}}},
			{ev: testGone(2, 70), edges: []string{"", "removed2@70"}, slots: []policyWant{{4, a, true}, {}, {5, c, true}}},
			// Pad A takes slot 1, assigned after slot 2; slot 0, rehomed,
			// keeps the age of its hand-off, the oldest.
			{ev: add(6, a, 80), edges: []string{"", "added6@80"}, slots: []policyWant{{4, d, true}, {6, a, true}, {5, c, true}}},
			{ev: add(7, g, 90), queue: []input.DeviceID{7}, slots: []policyWant{{4, d, true}, {6, a, true}, {5, c, true}}},
			{ev: down(7, "south", 95), edges: []string{"removed4@95 added7@95 resync down7:south"}, queue: []input.DeviceID{4},
				slots: []policyWant{{7, g, false}, {6, a, true}, {5, c, true}}},
		}},
		{"reclaim renews the age", 2, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: down(1, "north", 11), edges: []string{"down1:north"}, slots: []policyWant{{1, a, false}, {2, b, true}}},
			{ev: down(2, "north", 12), edges: []string{"", "down2:north"}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: add(3, c, 20), queue: []input.DeviceID{3}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: add(4, d, 25), queue: []input.DeviceID{3, 4}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: testGone(1, 30), edges: []string{"removed1@30 added3@30 resync"}, queue: []input.DeviceID{4},
				slots: []policyWant{{3, a, true}, {2, b, false}}},
			{ev: testGone(2, 40), edges: []string{"", "removed2@40 added4@40 resync"}, slots: []policyWant{{3, a, true}, {4, b, true}}},
			// Pad A takes slot 0 back, a new assignment; slot 1's hand-off is
			// now the oldest.
			{ev: add(5, a, 50), edges: []string{"removed3@50 added5@50"}, queue: []input.DeviceID{3},
				slots: []policyWant{{5, a, true}, {4, b, true}}},
			{ev: down(3, "south", 55), edges: []string{"", "removed4@55 added3@55 resync down3:south"}, queue: []input.DeviceID{4},
				slots: []policyWant{{5, a, true}, {3, c, false}}},
		}},
		{"reclaim when full", 2, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: down(1, "north", 11), edges: []string{"down1:north"}, slots: []policyWant{{1, a, false}, {2, b, true}}},
			{ev: down(2, "north", 12), edges: []string{"", "down2:north"}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: add(3, c, 20), queue: []input.DeviceID{3}, slots: []policyWant{{1, a, false}, {2, b, false}}},
			{ev: testGone(1, 30), edges: []string{"removed1@30 added3@30 resync"}, slots: []policyWant{{3, a, true}, {2, b, false}}},
			// Every slot is taken: Pad A takes its home back.
			{ev: add(4, a, 40), edges: []string{"removed3@40 added4@40"}, queue: []input.DeviceID{3}, slots: []policyWant{{4, a, true}, {2, b, false}}},
			// A hand-off keeps the home.
			{ev: testGone(2, 50), edges: []string{"", "removed2@50 added3@50 resync"}, slots: []policyWant{{4, a, true}, {3, b, true}}},
			{ev: add(5, b, 60), edges: []string{"", "removed3@60 added5@60"}, queue: []input.DeviceID{3}, slots: []policyWant{{4, a, true}, {5, b, true}}},
			{ev: testGone(4, 70), edges: []string{"removed4@70 added3@70 resync"}, slots: []policyWant{{3, a, true}, {5, b, true}}},
			{ev: testGone(5, 80), edges: []string{"", "removed5@80"}, slots: []policyWant{{3, a, true}}},
		}},
		{"take over by age", 3, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: add(3, c, 20), edges: []string{"", "", "added3@20"}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: add(4, d, 30), queue: []input.DeviceID{4}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			// Slot 0 is now the newest assignment.
			{ev: testGone(1, 40), edges: []string{"removed1@40 added4@40 resync"}, slots: []policyWant{{4, a, true}, {2, b, true}, {3, c, true}}},
			{ev: add(5, e, 50), queue: []input.DeviceID{5}, slots: []policyWant{{4, a, true}, {2, b, true}, {3, c, true}}},
			{ev: down(5, "south", 55), edges: []string{"", "removed2@55 added5@55 resync down5:south"}, queue: []input.DeviceID{2},
				slots: []policyWant{{4, a, true}, {5, e, false}, {3, c, true}}},
			{ev: down(2, "south", 60), edges: []string{"", "", "removed3@60 added2@60 resync down2:south"}, queue: []input.DeviceID{3},
				slots: []policyWant{{4, a, true}, {5, e, false}, {2, b, false}}},
			{ev: down(3, "south", 65), edges: []string{"removed4@65 added3@65 resync down3:south"}, queue: []input.DeviceID{4},
				slots: []policyWant{{3, c, false}, {5, e, false}, {2, b, false}}},
			{ev: down(4, "south", 70), queue: []input.DeviceID{4}, slots: []policyWant{{3, c, false}, {5, e, false}, {2, b, false}}},
		}},
		{"disabled", 3, "", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			// A press on the disabled slot reaches nothing and confirms
			// nothing.
			{before: disable(1), ev: down(2, "south", 15), slots: []policyWant{{1, a, true}, {2, b, true}}},
			// Pad B stays on slot 1, although slots 0 and 2 are free.
			{ev: testGone(1, 20), edges: []string{"removed1@20"}, slots: []policyWant{{}, {2, b, true}}},
			{ev: add(3, c, 30), edges: []string{"added3@30"}, slots: []policyWant{{3, c, true}, {2, b, true}}},
			{ev: testGone(2, 40), slots: []policyWant{{3, c, true}}},
			// The next assignment enables it: the DeviceAdded reaches it.
			{ev: add(4, d, 50), edges: []string{"", "added4@50"}, slots: []policyWant{{3, c, true}, {4, d, true}}},
		}},
		{"filter", 3, "pad", []slotsStep{
			{ev: add(1, a, 0), edges: []string{"added1@0"}, slots: []policyWant{{1, a, true}}},
			{ev: add(2, b, 10), edges: []string{"", "added2@10"}, slots: []policyWant{{1, a, true}, {2, b, true}}},
			{ev: add(3, c, 20), edges: []string{"", "", "added3@20"}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: add(4, d, 25), queue: []input.DeviceID{4}, slots: []policyWant{{1, a, true}, {2, b, true}, {3, c, true}}},
			{ev: testGone(1, 30), edges: []string{"removed1@30 added4@30 resync"}, slots: []policyWant{{4, a, true}, {2, b, true}, {3, c, true}}},
			{ev: testGone(2, 40), edges: []string{"", "removed2@40"}, slots: []policyWant{{4, a, true}, {}, {3, c, true}}},
			// Pad A's key, but not a match: it takes no free slot, rehomes
			// nothing, and its press takes nothing over.
			{ev: add(5, "x"+a, 50), slots: []policyWant{{4, a, true}, {}, {3, c, true}}},
			{ev: down(5, "east", 55), slots: []policyWant{{4, a, true}, {}, {3, c, true}}},
			{ev: add(6, b, 60), edges: []string{"", "added6@60"}, slots: []policyWant{{4, a, true}, {6, b, true}, {3, c, true}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(tc.slots)
			r.s.cfg.Device = tc.device
			for i, s := range tc.steps {
				if s.before != nil {
					s.before(r)
				}
				r.event(s.ev)
				checkSlots(t, r, fmt.Sprintf("step %d (%v %d)", i+1, s.ev.Kind, s.ev.Device), s.edges, s.slots, s.queue, nil)
			}
		})
	}
}

// TestDropWhileBehind pins a drop counted after a Sync's barriers went out
// (ADR-0023): a slot that is not behind gets the reset after its barrier,
// whose Want is the state before the reset; a slot that is behind gets it
// in pending, before the Resync and its waiting barrier.
func TestDropWhileBehind(t *testing.T) {
	const a, b = "0a0a", "0b0b"
	r := newTestRouter(2)
	var dropped uint64
	r.s.cfg.Dropped = func() uint64 { return dropped } // Events is nil: nothing is queued
	for _, e := range []input.Event{
		testAdd(1, a, 0), testAdd(2, b, 10),
		{Time: 11, Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Time: 12, Device: 2, Kind: input.ButtonDown, Control: "east"},
	} {
		r.event(e)
	}
	var east slot.State
	east.Buttons[control.East] = true
	rt0, rt1 := r.routes[0], r.routes[1]
	taken(rt0)
	taken(rt1)
	// Slot 1's channel fills up; a release of nothing held is skipped.
	for len(rt1.run.edges) < cap(rt1.run.edges) {
		rt1.run.edges <- slot.Edge{Event: input.Event{Device: 2, Kind: input.AxisMotion, Control: "leftx"}}
	}
	r.event(input.Event{Device: 2, Kind: input.ButtonUp, Control: "north"})
	if !rt1.behind || len(rt1.pending) != 0 {
		t.Fatalf("slot 1 behind %v, pending %+v; want behind with nothing pending", rt1.behind, rt1.pending)
	}
	req := syncRequest{reply: make(chan []*barrier, 1)}
	r.sync(req)
	bs := <-req.reply
	if m := rt0.run.take(); len(m) != 1 || m[0].b != bs[0] || !bs[0].assigned || bs[0].want != east {
		t.Fatalf("slot 0 posted %+v, want its barrier at east held", m)
	}
	if m := rt1.run.take(); len(m) != 0 || !slices.Equal(rt1.waiting, []*barrier{bs[1]}) {
		t.Fatalf("slot 1 posted %+v, waiting %v; want its barrier waiting", m, rt1.waiting)
	}

	dropped = 1
	r.checkDropped()
	if got := taken(rt0); len(got) != 1 || !got[0].Reset {
		t.Errorf("slot 0 edges %+v, want a reset", got)
	}
	if bs[0].want != east {
		t.Errorf("slot 0's barrier wants %+v, want east held still", bs[0].want)
	}
	if !slices.Equal(rt1.pending, []slot.Edge{{Reset: true}}) || !slices.Equal(rt1.waiting, []*barrier{bs[1]}) {
		t.Errorf("slot 1 pending %+v, waiting %v; want a reset, and its barrier waiting", rt1.pending, rt1.waiting)
	}
	for _, d := range r.devices {
		if d.state != (slot.State{}) {
			t.Errorf("gamepad %d state %+v, want zero", d.id, d.state)
		}
	}

	// Slot 1 catches up: the reset, the Resync, then its barrier.
	for len(rt1.run.edges) > 0 {
		<-rt1.run.edges
	}
	r.flush(rt1)
	got := taken(rt1)
	if len(got) != 2 || !got[0].Reset || got[1].Resync == nil || *got[1].Resync != (slot.State{}) {
		t.Errorf("slot 1 edges %+v, want a reset, then a Resync to the zero state", got)
	}
	if m := rt1.run.take(); rt1.behind || len(m) != 1 || m[0].b != bs[1] || !bs[1].assigned || bs[1].want != (slot.State{}) {
		t.Errorf("slot 1 behind %v, posted %+v; want caught up, its barrier at the zero state", rt1.behind, m)
	}
}

// TestDeliverBehind drives deliver and flush of a route whose channel
// holds one edge: while behind, the edges the slot must get are pending (a
// reset when nothing is, a removal over a reset, the DeviceAdded of the
// next gamepad, which that gamepad's removal cancels) and other edges are
// counted as skipped; flush sends the pending edges, then the Resync, as
// the channel makes room.
func TestDeliverBehind(t *testing.T) {
	up := slot.Edge{Event: input.Event{Device: 1, Kind: input.ButtonUp, Control: "east"}}
	gone := slot.Edge{Event: input.Event{Device: 1, Kind: input.DeviceRemoved}}
	reset := slot.Edge{Reset: true}
	addB := slot.Edge{Event: input.Event{Device: 2, Kind: input.DeviceAdded}}
	upB := slot.Edge{Event: input.Event{Device: 2, Kind: input.ButtonUp, Control: "east"}}
	goneB := slot.Edge{Event: input.Event{Device: 2, Kind: input.DeviceRemoved}}
	addC := slot.Edge{Event: input.Event{Device: 3, Kind: input.DeviceAdded}}
	for _, tc := range []struct {
		name    string
		edges   []slot.Edge
		pending []slot.Edge
		skipped int
	}{
		{"skipped", []slot.Edge{up, up}, nil, 2},
		{"reset", []slot.Edge{up, reset, up}, []slot.Edge{reset}, 2},
		{"removal over reset", []slot.Edge{reset, gone, up}, []slot.Edge{gone}, 1},
		{"reset after removal", []slot.Edge{gone, reset}, []slot.Edge{gone}, 0},
		{"hand-off", []slot.Edge{up, gone, addB, {Resync: &slot.State{}}, upB}, []slot.Edge{gone, addB}, 3},
		{"reset, then added", []slot.Edge{reset, addC, reset}, []slot.Edge{reset, addC}, 0},
		{"removal cancels added", []slot.Edge{gone, addB, upB, goneB, addC}, []slot.Edge{gone, addC}, 1},
		{"added, then removal", []slot.Edge{addB, goneB, reset, addC}, []slot.Edge{reset, addC}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &router{log: slog.New(slog.DiscardHandler)}
			run := &runner{edges: make(chan slot.Edge, 1), wake: make(chan struct{}, 1)}
			d := &device{id: 3}
			d.state.Buttons[control.East] = true
			rt := &route{run: run, dev: d}
			r.deliver(rt, up) // fills the channel
			if rt.behind {
				t.Fatal("behind with room in the channel")
			}
			for _, e := range tc.edges {
				r.deliver(rt, e)
			}
			if !rt.behind || rt.skipped != tc.skipped {
				t.Fatalf("behind %v, skipped %d; want behind, %d", rt.behind, rt.skipped, tc.skipped)
			}
			if !slices.Equal(rt.pending, tc.pending) {
				t.Fatalf("pending %+v, want %+v", rt.pending, tc.pending)
			}
			b := newBarrier()
			rt.waiting = []*barrier{b}
			var got []slot.Edge
			for i := 0; rt.behind; i++ {
				if i == 4 {
					t.Fatal("still behind after 4 flushes")
				}
				r.flush(rt) // no room: nothing changes
				got = append(got, <-run.edges)
				r.flush(rt)
			}
			got = append(got, <-run.edges)
			want := append([]slot.Edge{up}, tc.pending...)
			if len(got) != len(want)+1 || got[len(got)-1].Resync == nil || *got[len(got)-1].Resync != d.state {
				t.Fatalf("flushed %+v, want %+v and a Resync to the device's state", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("edge %d: %+v, want %+v", i, got[i], want[i])
				}
			}
			if len(rt.pending) != 0 || rt.skipped != 0 || len(rt.waiting) != 0 {
				t.Errorf("after the catch-up: pending %+v, skipped %d, waiting %d", rt.pending, rt.skipped, len(rt.waiting))
			}
			if m := run.take(); len(m) != 1 || m[0].b != b || !b.assigned || b.want != d.state {
				t.Errorf("posted %+v, want the waiting barrier at the device's state", m)
			}
		})
	}
}

// TestDisable drives disable, the router's side of a slot its runner
// disabled: it discards the edges the dead slot left, and when the
// DeviceAdded of the gamepad the slot has now is among them, it enables
// the slot at once with that DeviceAdded and a Resync to the gamepad's
// state, the barriers waiting for a catch-up behind them.
func TestDisable(t *testing.T) {
	up := slot.Edge{Event: input.Event{Device: 1, Kind: input.ButtonUp, Control: "east"}}
	gone := slot.Edge{Event: input.Event{Device: 1, Kind: input.DeviceRemoved}}
	addB := slot.Edge{Event: input.Event{Time: 7, Device: 2, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 2, IsGamepad: true}}}
	upB := slot.Edge{Event: input.Event{Device: 2, Kind: input.ButtonUp, Control: "east"}}
	reset := slot.Edge{Reset: true}
	newPadB := func() *device {
		b := &device{id: 2}
		b.state.Buttons[control.East] = true
		return b
	}
	for _, tc := range []struct {
		name    string
		edges   []slot.Edge // in the channel
		pending []slot.Edge // set: the route is behind
		dev     int         // the gamepad the slot has now: 0 none, 1 Pad A, 2 Pad B
		enabled bool
	}{
		{"still assigned", []slot.Edge{up}, nil, 1, false},
		{"reset pending", []slot.Edge{up}, []slot.Edge{reset}, 1, false},
		{"left, no gamepad", []slot.Edge{up, gone}, nil, 0, false},
		{"swapped", []slot.Edge{up, gone, addB, upB}, nil, 2, true},
		{"swapped while behind", []slot.Edge{up}, []slot.Edge{gone, addB}, 2, true},
		{"swapped, reset pending", []slot.Edge{gone, addB}, []slot.Edge{reset}, 2, true},
		{"handed off", []slot.Edge{up, gone, addB, {Resync: &slot.State{}}}, nil, 2, true},
		{"died on the removal", []slot.Edge{addB}, nil, 2, true},
		{"reached by Pad B", []slot.Edge{upB}, nil, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &router{log: slog.New(slog.DiscardHandler)}
			run := &runner{edges: make(chan slot.Edge, 4), wake: make(chan struct{}, 1)}
			b := newPadB()
			rt := &route{run: run, dev: []*device{nil, {id: 1}, b}[tc.dev]}
			for _, e := range tc.edges {
				run.edges <- e
			}
			wait := newBarrier()
			if tc.pending != nil {
				rt.behind, rt.pending, rt.skipped = true, tc.pending, 2
				rt.waiting = []*barrier{wait}
			}
			r.disable(rt)
			if rt.disabled == tc.enabled || rt.behind || len(rt.pending) != 0 || rt.skipped != 0 || len(rt.waiting) != 0 {
				t.Fatalf("route %+v; want disabled %v and caught up", rt, !tc.enabled)
			}
			msgs := run.take()
			if !tc.enabled {
				if len(run.edges) != 0 || len(msgs) != 0 {
					t.Errorf("left %d edges and posted %+v, want none", len(run.edges), msgs)
				}
				if tc.pending != nil && !wait.finished() {
					t.Error("the waiting barrier is not finished")
				}
				return
			}
			if got := taken(rt); len(got) != 2 || got[0] != addB || got[1].Resync == nil || *got[1].Resync != b.state {
				t.Errorf("edges %+v, want Pad B's DeviceAdded and a Resync to its state", got)
			}
			if len(msgs) == 0 || !msgs[0].enable {
				t.Fatalf("posted %+v, want the enable first", msgs)
			}
			if tc.pending != nil && (len(msgs) != 2 || msgs[1].b != wait || !wait.assigned || wait.want != b.state) {
				t.Errorf("posted %+v, want the waiting barrier at Pad B's state after the enable", msgs)
			}
		})
	}
	// With a channel of one edge, the Resync waits for room, and the
	// barriers wait for the Resync.
	t.Run("no room for the Resync", func(t *testing.T) {
		r := &router{log: slog.New(slog.DiscardHandler)}
		run := &runner{edges: make(chan slot.Edge, 1), wake: make(chan struct{}, 1)}
		b := newPadB()
		rt := &route{run: run, dev: b, behind: true, pending: []slot.Edge{addB}}
		run.edges <- gone
		wait := newBarrier()
		rt.waiting = []*barrier{wait}
		r.disable(rt)
		if rt.disabled || !rt.behind || len(rt.waiting) != 1 {
			t.Fatalf("route %+v; want enabled, behind, the barrier waiting", rt)
		}
		if msgs := run.take(); len(msgs) != 1 || !msgs[0].enable {
			t.Fatalf("posted %+v, want the enable only", msgs)
		}
		if e := <-run.edges; e != addB {
			t.Fatalf("edge %+v, want Pad B's DeviceAdded", e)
		}
		r.flush(rt)
		if e := <-run.edges; e.Resync == nil || *e.Resync != b.state {
			t.Errorf("edge %+v, want a Resync to Pad B's state", e)
		}
		if msgs := run.take(); rt.behind || len(msgs) != 1 || msgs[0].b != wait || !wait.assigned || wait.want != b.state {
			t.Errorf("behind %v, posted %+v; want caught up, the waiting barrier at Pad B's state", rt.behind, msgs)
		}
	})
}
