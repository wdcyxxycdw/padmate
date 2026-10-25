package supervisor

import (
	"encoding/json"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// powerEvent is the DevicePower of device id.
func powerEvent(id input.DeviceID, state input.PowerState, percent int) input.Event {
	p := input.Power{State: state, Percent: percent}
	return input.Event{Device: id, Kind: input.DevicePower, Power: &p}
}

func addedPad(id input.DeviceID) input.Event {
	return input.Event{Time: uint64(id), Device: id, Kind: input.DeviceAdded,
		Info: &input.DeviceInfo{ID: id, Name: "Pad", GUID: "0300000000000000000000000000000" + string(rune('0'+id)), IsGamepad: true}}
}

// batteryOf returns the Battery of the device id in the router's views.
func batteryOf(t *testing.T, r *router, id input.DeviceID) *input.Power {
	t.Helper()
	for _, v := range r.deviceViews() {
		if v.Info.ID == id {
			return v.Battery
		}
	}
	t.Fatalf("no view of device %d", id)
	return nil
}

// TestRouterStoresPower (IN-12): the router keeps the latest power of each
// gamepad it holds, mapped, waiting or ignored; the view omits it until
// one was read. A removed gamepad keeps nothing, and the same gamepad
// back with a new id starts without a value until it reads again. No slot
// gets the event.
func TestRouterStoresPower(t *testing.T) {
	r := newTestRouter(1)
	r.event(addedPad(1)) // gets the slot
	r.event(addedPad(2)) // waits: every slot is taken
	if b := batteryOf(t, r, 1); b != nil {
		t.Fatalf("battery before a read: %+v", b)
	}
	r.event(powerEvent(1, input.PowerDischarging, 80))
	r.event(powerEvent(2, input.PowerWired, -1))
	if b := batteryOf(t, r, 1); b == nil || *b != (input.Power{State: input.PowerDischarging, Percent: 80}) {
		t.Fatalf("battery of 1 = %+v", b)
	}
	if b := batteryOf(t, r, 2); b == nil || *b != (input.Power{State: input.PowerWired, Percent: -1}) {
		t.Fatalf("battery of the waiting pad 2 = %+v", b)
	}
	// The latest value wins, and unknown is a value.
	r.event(powerEvent(1, input.PowerCharging, 85))
	r.event(powerEvent(1, input.PowerUnknown, -1))
	if b := batteryOf(t, r, 1); b == nil || b.State != input.PowerUnknown {
		t.Fatalf("battery of 1 = %+v, want unknown", b)
	}
	// The slot learned of its gamepad and nothing of its power.
	for _, e := range taken(r.routes[0]) {
		if e.Event.Kind == input.DevicePower {
			t.Errorf("a slot got %+v", e)
		}
	}
	// Events of a device the router does not hold, or without a value, do nothing.
	r.event(powerEvent(9, input.PowerCharging, 5))
	r.event(input.Event{Device: 1, Kind: input.DevicePower})
	if b := batteryOf(t, r, 1); b == nil || b.State != input.PowerUnknown {
		t.Fatalf("an empty DevicePower changed the value: %+v", b)
	}

	// The view is a copy: the stored value is not the event's, nor the view's.
	p := input.Power{State: input.PowerDischarging, Percent: 50}
	r.event(input.Event{Device: 1, Kind: input.DevicePower, Power: &p})
	p.Percent = 1
	views := r.deviceViews()
	views[0].Battery.Percent = 2
	if b := batteryOf(t, r, 1); b == nil || b.Percent != 50 {
		t.Fatalf("battery %+v aliases an event or a view", b)
	}

	// A removed gamepad is gone with its power; the pad that comes back with
	// a new id (the receiver watchdog's revival) has none until it reads.
	r.event(input.Event{Device: 1, Kind: input.DeviceRemoved})
	for _, v := range r.deviceViews() {
		if v.Info.ID == 1 {
			t.Fatalf("the removed gamepad is in the views: %+v", v)
		}
	}
	const alias = input.DeviceID(1<<32 - 1)
	back := addedPad(1)
	back.Device, back.Info.ID = alias, alias
	r.event(back)
	if b := batteryOf(t, r, alias); b != nil {
		t.Fatalf("the revived gamepad starts with %+v", b)
	}
	r.event(powerEvent(alias, input.PowerDischarging, 80))
	if b := batteryOf(t, r, alias); b == nil || b.Percent != 80 {
		t.Fatalf("the revived gamepad's battery = %+v", b)
	}
	r.event(powerEvent(1, input.PowerCharging, 5)) // the old id: ignored
	if b := batteryOf(t, r, alias); b == nil || b.Percent != 80 {
		t.Fatalf("a stale DevicePower changed the revived gamepad: %+v", b)
	}
}

// TestRouterPowerIsNotInput: OnInput, which padmated turns into the IPC input
// topic, does not see DevicePower: it is routing metadata, as it is for traces.
func TestRouterPowerIsNotInput(t *testing.T) {
	r := newTestRouter(1)
	var seen []input.EventKind
	r.s.cfg.OnInput = func(e input.Event) { seen = append(seen, e.Kind) }
	r.take(addedPad(1))
	r.take(powerEvent(1, input.PowerDischarging, 10))
	r.take(input.Event{Device: 1, Kind: input.ButtonDown, Control: "south"})
	if len(seen) != 2 || seen[0] != input.DeviceAdded || seen[1] != input.ButtonDown {
		t.Fatalf("OnInput saw %v, want the DeviceAdded and the ButtonDown", seen)
	}
	if b := batteryOf(t, r, 1); b == nil || b.Percent != 10 {
		t.Fatalf("battery %+v after take", b)
	}
	// The slot's edges hold no power either: the press, after its DeviceAdded.
	for _, e := range taken(r.routes[0]) {
		if e.Event.Kind == input.DevicePower {
			t.Errorf("slot edge %+v", e)
		}
	}
}

// TestRouterPowerDoesNotMoveSlots: power does not confirm an assignment,
// take over a slot or reach a slot that is behind.
func TestRouterPowerDoesNotMoveSlots(t *testing.T) {
	r := newTestRouter(1)
	r.event(addedPad(1))
	r.event(addedPad(2))
	before := len(r.queue)
	rt := r.routes[0]
	prov := rt.provisional
	r.event(powerEvent(2, input.PowerCharging, 10)) // the waiting pad
	r.event(powerEvent(1, input.PowerCharging, 10))
	if len(r.queue) != before || rt.dev == nil || rt.dev.id != 1 || rt.provisional != prov {
		t.Fatalf("a DevicePower moved a slot: queue %d, dev %+v, provisional %v", len(r.queue), rt.dev, rt.provisional)
	}
	if edges := taken(rt); len(edges) != 1 || edges[0].Event.Kind != input.DeviceAdded {
		t.Fatalf("slot edges %+v, want the DeviceAdded only", edges)
	}
}

// TestDeviceViewBatteryJSON: the view carries "battery" as devices.list and
// status.get show it, and leaves the member out while nothing was read.
func TestDeviceViewBatteryJSON(t *testing.T) {
	v := DeviceView{Info: input.DeviceInfo{ID: 3}, Key: "k", Slot: -1, Status: "waiting"}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["battery"]; ok {
		t.Errorf("a view without a battery has one: %s", b)
	}
	v.Battery = &input.Power{State: input.PowerDischarging, Percent: 42}
	b, _ = json.Marshal(v)
	_ = json.Unmarshal(b, &m)
	if got := string(m["battery"]); got != `{"state":"discharging","percent":42}` {
		t.Errorf("battery = %s", got)
	}
	var back DeviceView
	if err := json.Unmarshal(b, &back); err != nil || back.Battery == nil || *back.Battery != *v.Battery {
		t.Errorf("round trip = %+v, %v", back.Battery, err)
	}
}

// programWithFeedback compiles an empty profile whose feedback member is
// feedback.
func programWithFeedback(t *testing.T, feedback string) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(`{"schemaVersion": 1, "name": "t", "feedback": ` + feedback + `, "layers": {"base": {}}}`))
	if p == nil {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// TestRouterOnPower (IN-12): OnPower gets each DevicePower of a gamepad the
// router holds, after the router stored it, with the identity of the gamepad
// (its key, which a revival keeps), whether a slot holds it and the feedback of
// the profile it is mapped with. A gamepad that waits has the default
// feedback, whatever the profile says; one the filter or the ignore setting
// leaves out is not reported; a device the router does not hold and an empty
// event are not either.
func TestRouterOnPower(t *testing.T) {
	r := newTestRouter(1)
	r.program = programWithFeedback(t, `{"strength": 1234, "shortMs": 55}`)
	var got []PowerUpdate
	r.s.cfg.OnPower = func(u PowerUpdate) {
		// The value is stored before the hook runs.
		if b := batteryOf(t, r, u.Device); b == nil || *b != u.Power {
			t.Errorf("OnPower before the router stored %+v: view has %+v", u.Power, b)
		}
		got = append(got, u)
	}
	r.event(addedPad(1)) // gets the slot
	r.event(addedPad(2)) // waits: every slot is taken
	r.event(powerEvent(1, input.PowerDischarging, 15))
	r.event(powerEvent(2, input.PowerDischarging, 12))
	if len(got) != 2 {
		t.Fatalf("OnPower calls = %+v, want one for each of the two gamepads", got)
	}
	mapped, waiting := got[0], got[1]
	if mapped.Device != 1 || !mapped.Mapped || mapped.Name != "Pad" || mapped.Key.String() == "" ||
		mapped.Power != (input.Power{State: input.PowerDischarging, Percent: 15}) {
		t.Errorf("update of the mapped gamepad = %+v", mapped)
	}
	if mapped.Key != r.devices[1].key {
		t.Errorf("key = %v, want the key of the device record %v", mapped.Key, r.devices[1].key)
	}
	if mapped.Feedback.Strength != 1234 || mapped.Feedback.ShortMs != 55 {
		t.Errorf("a mapped gamepad's feedback = %+v, want the profile's (strength 1234, shortMs 55)", mapped.Feedback)
	}
	if waiting.Device != 2 || waiting.Mapped || waiting.Feedback != profile.DefaultFeedback() {
		t.Errorf("update of the waiting gamepad = %+v, want unmapped and the defaults", waiting)
	}

	// A device the router does not hold, and an event without a value.
	got = nil
	r.event(powerEvent(9, input.PowerCharging, 5))
	r.event(input.Event{Device: 1, Kind: input.DevicePower})
	if len(got) != 0 {
		t.Errorf("OnPower for an unknown device or an empty event: %+v", got)
	}

	// The ignore setting: stored for Devices, not reported.
	r.s.cfg.IgnoreDevice = func(input.DeviceKey) bool { return true }
	r.event(powerEvent(1, input.PowerDischarging, 10))
	if len(got) != 0 {
		t.Errorf("OnPower for an ignored gamepad: %+v", got)
	}
	if b := batteryOf(t, r, 1); b == nil || b.Percent != 10 {
		t.Errorf("the ignored gamepad's battery = %+v, want it stored", b)
	}
}

// TestRouterOnPowerFollowsTheReload: the feedback in the update is that of the
// router's program, which a reload replaces in order with the events.
func TestRouterOnPowerFollowsTheReload(t *testing.T) {
	r := newTestRouter(1)
	var got []PowerUpdate
	r.s.cfg.OnPower = func(u PowerUpdate) { got = append(got, u) }
	r.event(addedPad(1))
	r.event(powerEvent(1, input.PowerDischarging, 50)) // no program yet: defaults
	r.program = programWithFeedback(t, `{"strength": 500}`)
	r.event(powerEvent(1, input.PowerDischarging, 40))
	r.program = programWithFeedback(t, `{"strength": 0}`)
	r.event(powerEvent(1, input.PowerDischarging, 30))
	if len(got) != 3 {
		t.Fatalf("OnPower calls = %+v", got)
	}
	if got[0].Feedback != profile.DefaultFeedback() || got[1].Feedback.Strength != 500 || got[2].Feedback.Strength != 0 {
		t.Errorf("feedback of the three updates = %+v, %+v, %+v", got[0].Feedback, got[1].Feedback, got[2].Feedback)
	}
}
