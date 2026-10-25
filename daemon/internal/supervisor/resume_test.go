package supervisor

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func TestSuspendInputSelectBeforeWakeCapturesHeld(t *testing.T) {
	r := newTestRouter(1)
	r.event(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, Name: "Pad", IsGamepad: true}})
	r.event(input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"})
	taken(r.routes[0])
	// Suspend has closed the output gate, but the input case won select
	// before the router consumed suspWake and applied its reasons.
	r.s.susp.Store(1<<8 | uint64(winsession.Lock))
	r.event(input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"})
	r.suspended, r.suspCount = winsession.Lock, 1
	r.event(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{}})
	taken(r.routes[0])
	r.s.susp.Store(1 << 8)
	r.suspended = 0
	r.event(input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"})
	if len(taken(r.routes[0])) != 0 {
		t.Fatal("input selected before suspension wake erased quarantine")
	}
}

func TestResumeSnapshotDoesNotConfirmOrTakeOver(t *testing.T) {
	r := newTestRouter(1)
	for _, id := range []input.DeviceID{1, 2} {
		r.event(input.Event{Kind: input.DeviceAdded, Device: id, Info: &input.DeviceInfo{ID: id, Name: "Pad", GUID: "11111111111111111111111111111111", IsGamepad: true}})
	}
	taken(r.routes[0])
	r.event(input.Event{Kind: input.DeviceState, Device: 2, Snapshot: &input.GamepadState{Buttons: []string{"east"}, Axes: map[string]int16{"leftx": 20000}}})
	if r.routes[0].dev.id != 1 || !r.routes[0].provisional || len(taken(r.routes[0])) != 0 {
		t.Fatal("sampled held button took over a slot")
	}
	r.event(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{Buttons: []string{"south"}, Axes: map[string]int16{"lefttrigger": 32767}}})
	edges := taken(r.routes[0])
	if len(edges) != 1 || edges[0].Resync == nil || !edges[0].Resync.Buttons[control.South] || !r.routes[0].provisional {
		t.Fatalf("snapshot not delivered as unconfirmed Resync: %+v", edges)
	}
	r.event(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{}})
	if r.devices[1].state != (slot.State{}) {
		t.Fatal("old buttons or axes survived complete correction")
	}
}

func TestResumeSnapshotSurvivesResetForEarlierDrops(t *testing.T) {
	for _, watermark := range []uint64{2, 3} {
		r := newTestRouter(1)
		r.s.cfg.Dropped = func() uint64 { return 3 }
		r.event(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, Name: "Pad", IsGamepad: true}})
		taken(r.routes[0])
		r.take(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{Buttons: []string{"east"}, Dropped: watermark}})
		if got := r.devices[1].state.Buttons[control.East]; got != (watermark == 3) {
			t.Fatalf("watermark=%d, preserved state=%v", watermark, got)
		}
	}
}

func TestResumeSnapshotDropOrderKeepsAxesSafe(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resetFirst bool
		watermark  uint64
	}{
		{"old_snapshot_before_reset", false, 2},
		{"old_snapshot_after_reset", true, 2},
		{"current_snapshot_before_reset", false, 3},
		{"current_snapshot_after_reset", true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(1)
			r.s.cfg.Dropped = func() uint64 { return 3 }
			r.event(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, Name: "Pad", IsGamepad: true}})
			taken(r.routes[0])
			if tc.resetFirst {
				// Events is empty: losses are noticed and Reset is delivered
				// before the critical snapshot leaves Source's backlog.
				r.checkDropped()
			}
			r.take(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{
				Buttons: []string{"east"}, Axes: map[string]int16{"leftx": 20000}, Dropped: tc.watermark,
			}})
			want := slot.State{}
			if tc.watermark == 3 {
				want.Buttons[control.East], want.Axes[control.LeftX] = true, 20000
			}
			if got := r.devices[1].state; got != want {
				t.Fatalf("router state: %+v, want %+v", got, want)
			}
			var delivered slot.State
			for _, edge := range taken(r.routes[0]) {
				if edge.Reset {
					delivered = slot.State{}
				}
				if edge.Resync != nil {
					delivered = *edge.Resync
				}
			}
			if delivered != want {
				t.Fatalf("delivered state: %+v, want %+v", delivered, want)
			}
		})
	}
}

// The wake scan reads the controls as they are held, and a button or stick
// that was held through the suspension stays quarantined (ADR-0033): the
// router's state of the gamepad and the Resync it delivers lack it, while a
// control that was not quarantined comes through. Without the mask a button
// held through a lock would press its key right after the unlock.
func TestResumeSnapshotIsMaskedByQuarantine(t *testing.T) {
	r := newTestRouter(1)
	r.event(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, Name: "Pad", IsGamepad: true}})
	r.event(input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"})
	r.event(input.Event{Kind: input.AxisMotion, Device: 1, Control: "leftx", Value: 20000})
	taken(r.routes[0])
	// A lock; the next event the router routes captures what is held.
	r.s.susp.Store(1<<8 | uint64(winsession.Lock))
	r.suspended, r.suspCount = winsession.Lock, 1
	r.event(input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"})
	taken(r.routes[0])
	// The unlock; the wake scan still finds east and the stick held, and south,
	// which was not held before, pressed.
	r.s.susp.Store(1 << 8)
	r.suspended = 0
	r.event(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{
		Buttons: []string{"east", "south"}, Axes: map[string]int16{"leftx": 20000, "lefttrigger": 32767},
	}})
	want := slot.State{}
	want.Buttons[control.South], want.Axes[control.LeftTrigger] = true, 32767
	if got := r.devices[1].state; got != want {
		t.Fatalf("router state: %+v, want only south and the trigger: %+v", got, want)
	}
	var delivered slot.State
	for _, edge := range taken(r.routes[0]) {
		if edge.Resync != nil {
			delivered = *edge.Resync
		}
	}
	if delivered != want {
		t.Fatalf("delivered state: %+v, want %+v", delivered, want)
	}
}
