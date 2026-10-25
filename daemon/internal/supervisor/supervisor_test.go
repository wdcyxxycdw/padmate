package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

var (
	padA = pad(1, "Pad A", "03ab")
	padB = pad(2, "Pad B", "057e")
	padC = pad(3, "Pad C", "0c0c")
	padD = pad(4, "Pad D", "0d0d")
)

const (
	msgWaiting       = "gamepad waiting: every slot is taken"
	msgIgnoredFilter = "gamepad ignored: it does not match the device filter"
)

// checkAttrs checks the attributes of a log line.
func checkAttrs(t *testing.T, r logRecord, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if r.Attrs[k] != v {
			t.Errorf("%q: %s = %q, want %q (all: %v)", r.Msg, k, r.Attrs[k], v, r.Attrs)
		}
	}
}

func TestRunMapsAssignedPad(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.set(20)
	h.rec.WaitFor(t, "+LCtrl@0 +Z@0 -Z@20 -LCtrl@20")
	keyA := input.DeviceKey{GUID: "03ab"}
	want := supervisor.SlotStatus{Index: 0, Key: keyA, ID: 1, Name: "Pad A", Gen: 1, Home: keyA}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	checkAttrs(t, h.waitLog("device connected", 1)[0], map[string]string{"id": "1", "name": "Pad A", "device": "03ab", "reconnected": "false"})
	checkAttrs(t, h.waitLog("slot assigned", 1)[0], map[string]string{"slot": "0", "gen": "1", "id": "1", "device": "03ab", "provisional": "true"})
	// Its first press made the slot its own.
	checkAttrs(t, h.waitLog("slot confirmed", 1)[0], map[string]string{"slot": "0", "id": "1", "name": "Pad A", "device": "03ab"})
	if st := h.states(); len(st) != 1 || !st[0].Ran || !st[0].Assigned || st[0].Got != st[0].Want {
		t.Errorf("Sync states %+v, want the slot at the router's state", st)
	}
}

// TestHoldReleasedOnCancel: Run releases through the slot loop, before it
// returns and before output.Loop.Stop.
func TestHoldReleasedOnCancel(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(50)
	h.expect("+Space@0")
	h.cancel()
	if err := h.wait(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	h.expect("+Space@0 -Space@50")
	if err := h.sup.Sync(context.Background()); !errors.Is(err, supervisor.ErrStopped) {
		t.Errorf("Sync after Run = %v, want ErrStopped", err)
	}
}

func TestHoldReleasedOnEventsClosed(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(50)
	close(h.events)
	if err := h.wait(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	h.expect("+Space@0 -Space@50")
}

func TestHoldReleasedOnRemove(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(50)
	h.send(removed(1))
	h.sync()
	h.expect("+Space@0 -Space@50")
	checkAttrs(t, h.waitLog("slot freed", 1)[0], map[string]string{"slot": "0", "device": "03ab"})
	if got := h.slot0(); got != (supervisor.SlotStatus{Gen: 1}) {
		t.Errorf("Status %+v after the removal, want no gamepad", got)
	}
	// The next gamepad takes the slot; the first comes back to its key and
	// waits.
	h.send(added(padB))
	h.send(button(2, "east", true))
	h.set(60)
	h.send(button(2, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@50 +Space@50 -Space@60")
	if got := h.slot0(); got.ID != 2 || got.Key.GUID != "057e" {
		t.Errorf("Status %+v, want Pad B", got)
	}
	h.send(added(pad(3, "Pad A", "03ab")))
	checkAttrs(t, h.waitLog("device connected", 3)[2], map[string]string{"id": "3", "device": "03ab", "reconnected": "true"})
	h.send(button(2, "east", true)) // released by the cleanup
	h.sync()
	if got := h.slot0(); got.ID != 2 {
		t.Errorf("Status %+v, want Pad B still", got)
	}
}

// TestReconnect: a gamepad that comes back with its key to a free slot
// takes it, and is mapped again.
func TestReconnect(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.send(removed(1))
	h.set(20)
	h.send(added(pad(3, "Pad A", "03ab")))
	h.send(button(3, "south", true))
	h.send(button(3, "south", false))
	h.set(40)
	h.rec.WaitFor(t, "+Space@0 -Space@10 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40")
	lines := h.waitLog("slot assigned", 2)
	checkAttrs(t, lines[0], map[string]string{"id": "1", "reconnected": "false", "handoff": "false"})
	checkAttrs(t, lines[1], map[string]string{"id": "3", "device": "03ab", "reconnected": "true", "handoff": "false"})
	keyA := input.DeviceKey{GUID: "03ab"}
	want := supervisor.SlotStatus{Index: 0, Key: keyA, ID: 3, Name: "Pad A", Gen: 1, Home: keyA}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v, want %+v", got, want)
	}
}

// TestHandoff: when the slot's gamepad leaves, the gamepad that waits
// takes the slot at once, provisionally: the slot sees a copy of its
// DeviceAdded at the time of the removal, then the Resync presses what it
// holds already.
func TestHandoff(t *testing.T) {
	var mu sync.Mutex
	var seen []input.Event
	h := start(t, opts{onEvent: func(_ uint8, e input.Event) {
		mu.Lock()
		seen = append(seen, e)
		mu.Unlock()
	}})
	h.send(added(padA))
	// Pad A presses a button, so Pad B's press takes nothing over.
	h.send(button(1, "west", true))
	h.send(button(1, "west", false))
	h.send(added(padB))
	h.send(button(2, "east", true))
	h.send(axis(2, "leftx", 1000))
	h.set(50)
	h.expect("")
	h.send(input.Event{Time: uint64(50 * ms), Device: 1, Kind: input.DeviceRemoved})
	h.sync()
	h.expect("+Space@50")
	if st := h.states(); !st[0].Assigned || st[0].Got != st[0].Want || !st[0].Got.Buttons[control.East] || st[0].Got.Axes[control.LeftX] != 1000 {
		t.Errorf("Sync states %+v, want the slot at Pad B's state", st)
	}
	want := supervisor.SlotStatus{
		Index: 0, Key: input.DeviceKey{GUID: "057e"}, ID: 2, Name: "Pad B", Gen: 1,
		Home: input.DeviceKey{GUID: "03ab"}, Provisional: true,
	}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	lines := h.waitLog("slot assigned", 2)
	checkAttrs(t, lines[0], map[string]string{"id": "1", "handoff": "false", "provisional": "true"})
	checkAttrs(t, lines[1], map[string]string{
		"slot": "0", "id": "2", "name": "Pad B", "device": "057e", "reconnected": "false", "handoff": "true", "provisional": "true",
	})
	if got := h.logs.with(msgWaiting); len(got) != 1 {
		t.Errorf("%d lines %q, want 1", len(got), msgWaiting)
	}
	mu.Lock()
	got := slices.Clone(seen)
	mu.Unlock()
	if len(got) != 7 {
		t.Fatalf("OnEvent saw %+v, want 7 events", got)
	}
	if e := got[4]; e.Kind != input.DeviceAdded || e.Device != 2 || e.Time != uint64(50*ms) || e.Info == nil || e.Info == padB || *e.Info != *padB {
		t.Errorf("OnEvent saw %+v fifth, want a copy of Pad B's DeviceAdded at 50 ms", e)
	}
	for i, w := range []input.Event{
		{Device: 1, Kind: input.DeviceAdded},
		{Device: 1, Kind: input.ButtonDown, Control: "west"}, {Device: 1, Kind: input.ButtonUp, Control: "west"},
		{Device: 1, Kind: input.DeviceRemoved}, {},
		{Kind: input.ButtonDown, Control: "east"}, {Kind: input.AxisMotion, Control: "leftx", Value: 1000},
	} {
		if e := got[i]; i != 4 && (e.Kind != w.Kind || e.Device != w.Device || e.Control != w.Control || e.Value != w.Value) {
			t.Errorf("OnEvent event %d: %+v, want %+v", i, e, w)
		}
	}
	h.set(60)
	h.send(button(2, "east", false))
	h.sync()
	h.expect("+Space@50 -Space@60")
}

// deviceEvents records the DeviceAdded and DeviceRemoved events OnEvent
// sees, as "<kind><device>@<ms>".
type deviceEvents struct {
	mu   sync.Mutex
	seen []string
}

func (d *deviceEvents) onEvent(_ uint8, e input.Event) {
	if e.Kind != input.DeviceAdded && e.Kind != input.DeviceRemoved {
		return
	}
	d.mu.Lock()
	d.seen = append(d.seen, fmt.Sprintf("%v%d@%d", e.Kind, e.Device, e.Time/uint64(ms)))
	d.mu.Unlock()
}

func (d *deviceEvents) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.seen, " ")
}

// at returns e at msec ms.
func at(msec int, e input.Event) input.Event {
	e.Time = uint64(time.Duration(msec) * ms)
	return e
}

// TestReclaim: the gamepad a hand-off came from takes the slot back when it
// reconnects before the one it was handed to pressed a button (IN-05). The
// slot forgets the stand-in, releasing what it holds, before it learns of
// the gamepad that came back; the stand-in waits again, first in line. The
// gamepad that came back is provisional until its first press.
func TestReclaim(t *testing.T) {
	dev := &deviceEvents{}
	h := start(t, opts{onEvent: dev.onEvent})
	keyA, keyB := input.DeviceKey{GUID: "03ab"}, input.DeviceKey{GUID: "057e"}
	h.send(added(padA))
	// Pad A presses a button, so Pad B's press takes nothing over.
	h.send(button(1, "west", true))
	h.send(button(1, "west", false))
	h.send(added(padB))
	h.send(added(pad(3, "Pad C", "0c0c")))
	h.send(button(2, "east", true))
	h.set(10)
	h.send(at(10, removed(1)))
	h.sync()
	h.expect("+Space@10")
	want := supervisor.SlotStatus{Key: keyB, ID: 2, Name: "Pad B", Gen: 1, Home: keyA, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the hand-off, want %+v", got, want)
	}
	// Axes do not confirm the hand-off, the triggers neither.
	h.send(axis(2, "leftx", 1000))
	h.send(axis(2, "righttrigger", 1000))
	h.set(20)
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after axis motion, want %+v", got, want)
	}
	h.send(at(20, added(pad(4, "Pad A", "03ab"))))
	h.sync()
	h.expect("+Space@10 -Space@20")
	checkAttrs(t, h.waitLog("slot reclaimed", 1)[0], map[string]string{
		"slot": "0", "id": "4", "name": "Pad A", "device": "03ab", "holder": "2", "holderDevice": "057e",
	})
	checkAttrs(t, h.waitLog("slot assigned", 3)[2], map[string]string{
		"id": "4", "reconnected": "true", "handoff": "false", "provisional": "true",
	})
	want = supervisor.SlotStatus{Key: keyA, ID: 4, Name: "Pad A", Gen: 1, Home: keyA, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the reclaim, want %+v", got, want)
	}
	// Pad A presses, which makes the slot its own; then Pad B's presses
	// reach no slot and take nothing over.
	h.send(button(4, "south", true))
	h.send(button(4, "south", false))
	h.send(button(2, "east", false))
	h.send(button(2, "east", true))
	h.set(40)
	h.rec.WaitFor(t, "+Space@10 -Space@20 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40")
	// Pad A leaves again: Pad B, first in line, stands in again, holding
	// east.
	h.set(50)
	h.send(at(50, removed(4)))
	h.sync()
	h.expect("+Space@10 -Space@20 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40 +Space@50")
	want = supervisor.SlotStatus{Key: keyB, ID: 2, Name: "Pad B", Gen: 1, Home: keyA, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the second hand-off, want %+v", got, want)
	}
	h.send(button(2, "east", false))
	h.sync()
	h.expect("+Space@10 -Space@20 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40 +Space@50 -Space@50")
	if got, want := dev.String(), "added1@0 removed1@10 added2@10 removed2@20 added4@20 removed4@50 added2@50"; got != want {
		t.Errorf("OnEvent saw %s, want %s", got, want)
	}
	if got := h.logs.with("slot confirmed"); len(got) != 2 || got[0].Attrs["id"] != "1" || got[1].Attrs["id"] != "4" {
		t.Errorf("logged %v; want the presses of Pad A, as 1 and 4, to confirm the slot, no others", got)
	}
}

// TestHandoffConfirmed: the first press of the gamepad a hand-off went to,
// of any button, makes the slot its own: the one it came from waits when
// it comes back, and the slot goes back to the confirmed one.
func TestHandoffConfirmed(t *testing.T) {
	h := start(t, opts{})
	keyA, keyB := input.DeviceKey{GUID: "03ab"}, input.DeviceKey{GUID: "057e"}
	h.send(added(padA))
	h.send(added(padB))
	h.set(10)
	h.send(at(10, removed(1)))
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.set(30)
	h.rec.WaitFor(t, "+LCtrl@10 +Z@10 -Z@30 -LCtrl@30")
	checkAttrs(t, h.waitLog("slot confirmed", 1)[0], map[string]string{"slot": "0", "id": "2", "name": "Pad B", "device": "057e"})
	want := supervisor.SlotStatus{Key: keyB, ID: 2, Name: "Pad B", Gen: 1, Home: keyB}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	// Pad A comes back and waits; holding east, it is handed the slot when
	// Pad B leaves, provisionally.
	h.send(added(pad(3, "Pad A", "03ab")))
	h.send(button(3, "east", true))
	h.sync()
	checkAttrs(t, h.waitLog(msgWaiting, 2)[1], map[string]string{"id": "3", "device": "03ab"})
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v with Pad A back, want %+v", got, want)
	}
	h.set(40)
	h.send(at(40, removed(2)))
	h.sync()
	h.expect("+LCtrl@10 +Z@10 -Z@30 -LCtrl@30 +Space@40")
	want = supervisor.SlotStatus{Key: keyA, ID: 3, Name: "Pad A", Gen: 1, Home: keyB, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the hand-off, want %+v", got, want)
	}
	// Pad B takes it back, provisionally, and Pad A lets go of east.
	h.send(at(40, added(pad(4, "Pad B", "057e"))))
	h.sync()
	h.expect("+LCtrl@10 +Z@10 -Z@30 -LCtrl@30 +Space@40 -Space@40")
	checkAttrs(t, h.waitLog("slot reclaimed", 1)[0], map[string]string{"id": "4", "device": "057e", "holder": "3", "holderDevice": "03ab"})
	want = supervisor.SlotStatus{Key: keyB, ID: 4, Name: "Pad B", Gen: 1, Home: keyB, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the reclaim, want %+v", got, want)
	}
}

// TestTakeOver: a gamepad that waits behind a stand-in that pressed no
// button, such as an idle gamepad that stays connected, takes the slot
// over with a press (IN-05). The slot forgets the stand-in, releasing what
// it holds, learns of the gamepad that pressed, with the buttons it held
// before, and then maps the press; the slot is its own, and the stand-in
// waits again, first.
func TestTakeOver(t *testing.T) {
	dev := &deviceEvents{}
	h := start(t, opts{onEvent: dev.onEvent})
	keyB, keyC := input.DeviceKey{GUID: "057e"}, input.DeviceKey{GUID: "0c0c"}
	h.send(added(padA))
	// Pad A presses a button, so the presses of Pads B and C take nothing
	// over from it.
	h.send(button(1, "north", true))
	h.send(button(1, "north", false))
	h.send(added(padB))
	h.send(added(pad(3, "Pad C", "0c0c")))
	h.send(button(2, "east", true))
	h.send(button(3, "west", true)) // unbound
	h.set(10)
	h.send(at(10, removed(1)))
	h.sync()
	h.expect("+Space@10")
	h.set(20)
	h.send(at(20, button(3, "south", true)))
	h.send(button(3, "south", false))
	h.set(40)
	h.rec.WaitFor(t, "+Space@10 -Space@20 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40")
	checkAttrs(t, h.waitLog("slot taken over", 1)[0], map[string]string{
		"slot": "0", "id": "3", "name": "Pad C", "device": "0c0c", "holder": "2", "holderDevice": "057e",
	})
	checkAttrs(t, h.waitLog("slot assigned", 3)[2], map[string]string{
		"id": "3", "reconnected": "false", "handoff": "true", "provisional": "false",
	})
	want := supervisor.SlotStatus{Key: keyC, ID: 3, Name: "Pad C", Gen: 1, Home: keyC}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the take-over, want %+v", got, want)
	}
	if st := h.states(); !st[0].Got.Buttons[control.West] || st[0].Got != st[0].Want {
		t.Errorf("Sync states %+v, want the slot at Pad C's state, west held", st)
	}
	// Pad B waits, Pad A too when it comes back: the slot is Pad C's
	// own, so their presses reach no slot. Pad B's release lets go of
	// nothing.
	h.send(at(40, added(pad(4, "Pad A", "03ab"))))
	h.send(button(2, "east", false))
	h.send(button(4, "south", true))
	h.send(button(4, "south", false))
	h.sync()
	h.expect("+Space@10 -Space@20 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40")
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v with Pad A back, want %+v", got, want)
	}
	if got, want := dev.String(), "added1@0 removed1@10 added2@10 removed2@20 added3@20"; got != want {
		t.Errorf("OnEvent saw %s, want %s", got, want)
	}
	// Pad C leaves: Pad B, first in line, stands in, and the slot's home
	// is Pad C.
	h.send(at(50, removed(3)))
	h.sync()
	h.expect("+Space@10 -Space@20 +LCtrl@20 +Z@20 -Z@40 -LCtrl@40")
	want = supervisor.SlotStatus{Key: keyB, ID: 2, Name: "Pad B", Gen: 1, Home: keyC, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after Pad C left, want %+v", got, want)
	}
}

// TestReclaimBehind: a reclaim while the slot is behind reaches it in
// order: the removal of the stand-in, which releases what it holds, then
// the DeviceAdded of the gamepad that came back, then the Resync to its
// state.
func TestReclaimBehind(t *testing.T) {
	var st *stall
	h := start(t, opts{buffer: 3, onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
	st = newStall(t, "leftx")
	st.armed.Store(false)
	h.send(added(padA))
	st.next(t)
	// Pad A presses a button, so Pad B's press takes nothing over.
	h.send(button(1, "west", true))
	h.send(button(1, "west", false))
	h.sync()
	h.send(added(padB))
	h.send(button(2, "east", true))
	h.send(removed(1)) // the 3 edges fit
	h.sync()
	h.expect("+Space@0")
	// The slot stalls on Pad B's stick; the next motions fill the channel.
	st.armed.Store(true)
	h.send(axis(2, "leftx", 1000))
	st.wait(t, st.stalled)
	for _, v := range []int16{2000, 3000, 4000} {
		h.send(axis(2, "leftx", v))
	}
	h.send(added(pad(3, "Pad A", "03ab")))
	h.send(button(3, "east", true)) // skipped: the Resync covers it
	st.release()
	states := h.statesTicking()
	if !states[0].Assigned || states[0].Got != states[0].Want || !states[0].Got.Buttons[control.East] {
		t.Errorf("Sync states %+v, want the slot at Pad A's state", states)
	}
	// The removal released Pad B's east, the Resync pressed Pad A's.
	if got := untimed(h.rec.Trace()); got != "+Space -Space +Space" {
		t.Errorf("output %s, want +Space -Space +Space", got)
	}
	var kinds []string
	for len(st.seen) > 0 {
		if e := <-st.seen; e.Kind == input.DeviceAdded || e.Kind == input.DeviceRemoved {
			kinds = append(kinds, fmt.Sprintf("%v%d", e.Kind, e.Device))
		}
	}
	if got := strings.Join(kinds, " "); got != "removed1 added2 removed2 added3" {
		t.Errorf("OnEvent saw %s, want removed1 added2 removed2 added3", got)
	}
	checkAttrs(t, h.waitLog("slot behind; resyncing", 1)[0], map[string]string{"skipped": "1"})
	keyA := input.DeviceKey{GUID: "03ab"}
	want := supervisor.SlotStatus{Key: keyA, ID: 3, Name: "Pad A", Gen: 1, Home: keyA}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	h.send(button(3, "east", false))
	h.sync()
	if got := untimed(h.rec.Trace()); got != "+Space -Space +Space -Space" {
		t.Errorf("output %s, want +Space -Space +Space -Space", got)
	}
}

// TestReclaimDisabled: the Resync of a hand-off presses a button whose
// action panics a third time, which disables the slot; the gamepad the
// slot was handed from takes it back, and the slot is enabled again for
// it. A press of the stand-in while the slot is disabled maps nothing, so
// it does not make the slot the stand-in's own.
func TestReclaimDisabled(t *testing.T) {
	for _, press := range []bool{false, true} {
		t.Run(fmt.Sprintf("press=%v", press), func(t *testing.T) {
			p := &panicky{}
			p.armed.Store(true)
			h := start(t, opts{trace: p.trace})
			keyA := input.DeviceKey{GUID: "03ab"}
			h.send(added(padA))
			h.send(added(padB))
			for i, backoff := range []time.Duration{100 * ms, 200 * ms} {
				h.send(button(1, "south", true))
				h.waitLog("slot panicked", i+1)
				h.send(button(1, "south", false))
				h.clk.Advance(backoff)
				h.sync()
			}
			// Pad A pressed, so Pad B's press takes nothing over.
			h.send(button(2, "south", true))
			h.send(removed(1))
			h.waitLog("slot disabled", 1)
			h.sync()
			if got := h.slot0(); !got.Disabled || got.ID != 2 || !got.Provisional {
				t.Errorf("Status %+v, want disabled, Pad B provisional", got)
			}
			p.armed.Store(false)
			if press {
				h.routerDisabled()
				h.send(button(2, "east", true))
				h.send(button(2, "east", false))
				h.sync()
				if got := h.slot0(); !got.Disabled || !got.Provisional || got.Home != keyA {
					t.Errorf("Status %+v after Pad B pressed, want disabled, Pad B provisional, home Pad A", got)
				}
			}
			h.send(added(pad(3, "Pad A", "03ab")))
			h.waitLog("slot enabled", 1)
			h.sync()
			want := supervisor.SlotStatus{Key: keyA, ID: 3, Name: "Pad A", Gen: 1, Home: keyA, Provisional: true}
			if got := h.slot0(); got != want {
				t.Errorf("Status %+v, want %+v", got, want)
			}
			h.send(button(3, "east", true))
			h.send(button(3, "east", false))
			h.sync()
			h.expect("+Space@300 -Space@300")
		})
	}
}

// TestSecondPadWaits: a matching gamepad that finds the slot taken waits,
// logged once per gamepad; its events only update the router's state of
// it, one that leaves while it waits just leaves the line, and the slot
// goes to the one that waits when its gamepad leaves.
func TestSecondPadWaits(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	// Pad A presses a button, so Pad B's presses take nothing over.
	h.send(button(1, "west", true))
	h.send(button(1, "west", false))
	h.send(added(padB))
	h.send(button(2, "east", true))
	h.send(button(2, "south", true))
	h.sync()
	h.expect("")
	h.send(removed(2))
	h.send(added(pad(3, "Pad B", "057e"))) // the same key again
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	if got := h.logs.with(msgWaiting); len(got) != 1 {
		t.Errorf("%d lines %q, want 1: once per gamepad", len(got), msgWaiting)
	} else {
		checkAttrs(t, got[0], map[string]string{"id": "2", "name": "Pad B", "device": "057e"})
	}
	if got := h.slot0(); got.ID != 1 {
		t.Errorf("Status %+v, want Pad A", got)
	}
	// A non-gamepad is not even connected.
	h.send(input.Event{Device: 9, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 9, Name: "Joystick"}})
	h.send(input.Event{Device: 10, Kind: input.DeviceAdded})
	h.send(button(9, "east", false))
	h.sync()
	if got := h.logs.with("device ignored: not a gamepad"); len(got) != 2 || got[0].Level != slog.LevelDebug {
		t.Errorf("non-gamepad lines %v, want 2 at debug", got)
	}
	if got := len(h.logs.with("device connected")); got != 3 {
		t.Errorf("%d devices connected, want 3", got)
	}
	// Pad A leaves: Pad B, back as 3 and holding nothing, takes the slot.
	h.send(removed(1))
	h.sync()
	h.expect("+Space@0 -Space@0")
	if got := h.slot0(); got.ID != 3 || got.Key.GUID != "057e" {
		t.Errorf("Status %+v, want Pad B as 3", got)
	}
	checkAttrs(t, h.waitLog("slot assigned", 2)[1], map[string]string{"id": "3", "reconnected": "true", "handoff": "true"})
	h.send(button(3, "south", true))
	h.send(button(3, "south", false))
	h.set(20)
	h.rec.WaitFor(t, "+Space@0 -Space@0 +LCtrl@0 +Z@0 -Z@20 -LCtrl@20")
}

func TestDeviceFilter(t *testing.T) {
	xbox, pro := pad(1, "Xbox Controller", "03ab"), pad(2, "Pro Controller", "057e")
	for _, tc := range []struct {
		name, device string
		want         input.DeviceID // 0: none
	}{
		{"any", "", 1},
		{"name substring", "pro con", 2},
		{"key substring", "057E", 2},
		{"no match", "dualsense", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := start(t, opts{device: tc.device})
			h.send(added(xbox))
			h.send(added(pro))
			h.send(button(1, "east", true))
			h.send(button(2, "south", true))
			h.sync()
			want := map[input.DeviceID]string{0: "", 1: "+Space@0", 2: "+LCtrl@0 +Z@0"}[tc.want]
			h.expect(want)
			if got := h.slot0().ID; got != tc.want {
				t.Errorf("slot 0 has %d, want %d", got, tc.want)
			}
			filtered := 2
			if tc.want != 0 {
				filtered = 0
				if tc.device != "" {
					filtered = 1
				}
			}
			if got := len(h.logs.with(msgIgnoredFilter)); got != filtered {
				t.Errorf("%d lines %q, want %d", got, msgIgnoredFilter, filtered)
			}
			h.set(20)
			h.rec.WaitFor(t, strings.ReplaceAll(want, "+Z@0", "+Z@0 -Z@20 -LCtrl@20"))
		})
	}
}

// stall makes OnEvent block on the first press or motion of its control
// while armed until release; stalled is closed once it blocks. seen gets
// the other events OnEvent sees.
type stall struct {
	control string
	armed   atomic.Bool
	stalled chan struct{}
	gate    chan struct{}
	once    sync.Once
	seen    chan input.Event
}

// newStall returns an armed stall on c that the test's end releases,
// before the harness stops Run: call it after start.
func newStall(t *testing.T, c string) *stall {
	s := &stall{control: c, stalled: make(chan struct{}), gate: make(chan struct{}), seen: make(chan input.Event, 64)}
	s.armed.Store(true)
	t.Cleanup(s.release)
	return s
}

func (s *stall) release() { s.once.Do(func() { close(s.gate) }) }

func (s *stall) onEvent(_ uint8, e input.Event) {
	if (e.Kind == input.ButtonDown || e.Kind == input.AxisMotion) && e.Control == s.control && s.armed.CompareAndSwap(true, false) {
		close(s.stalled)
		<-s.gate
		return
	}
	select {
	case s.seen <- e:
	default:
	}
}

func (s *stall) wait(t *testing.T, what chan struct{}) {
	t.Helper()
	select {
	case <-what:
	case <-time.After(waitLimit):
		t.Fatal("the slot did not stall")
	}
}

// next waits for the next event OnEvent sees.
func (s *stall) next(t *testing.T) input.Event {
	t.Helper()
	select {
	case e := <-s.seen:
		return e
	case <-time.After(waitLimit):
		t.Fatal("OnEvent saw nothing")
		return input.Event{}
	}
}

func TestSlotBehindResyncs(t *testing.T) {
	var st *stall
	h := start(t, opts{buffer: 1, onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
	st = newStall(t, "west")
	h.send(added(padA))
	if e := st.next(t); e.Kind != input.DeviceAdded {
		t.Fatalf("OnEvent saw %v first", e.Kind)
	}
	h.send(button(1, "west", true))
	st.wait(t, st.stalled)
	h.send(button(1, "west", false)) // queued: the channel holds one
	// The slot is behind: skipped, the router only keeps the state.
	h.send(button(1, "east", true))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(axis(1, "leftx", 1000))
	st.release()
	if e := st.next(t); e.Kind != input.ButtonUp || e.Control != "west" {
		t.Fatalf("OnEvent saw %v %s, want the queued release of west", e.Kind, e.Control)
	}
	states := h.states()
	if len(states) != 1 || !states[0].Ran || !states[0].Assigned || states[0].Got != states[0].Want {
		t.Fatalf("Sync states %+v, want the slot at the router's state", states)
	}
	if !states[0].Got.Buttons[control.East] {
		t.Errorf("slot state %+v, want east down", states[0].Got)
	}
	// The resync pressed east; the south tap was lost.
	h.expect("+Space@0")
	checkAttrs(t, h.waitLog("slot behind; resyncing", 1)[0], map[string]string{"slot": "0", "skipped": "4"})
	// Edges flow again.
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@0")
}

// TestSlotBehindRemoval: a removal while behind reaches the slot before
// anything of the next gamepad, whether that one connects after it or
// waited and is handed the slot; the next gamepad's DeviceAdded follows,
// then the Resync, and the retries on the clock catch the slot up.
func TestSlotBehindRemoval(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "connects after", true: "waiting"}[waiting], func(t *testing.T) {
			var st *stall
			h := start(t, opts{buffer: 1, onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
			st = newStall(t, "west")
			h.send(added(padA))
			st.next(t)
			h.send(button(1, "east", true))
			st.next(t)
			if waiting {
				// Pad A pressed, so Pad B's press takes nothing over.
				h.send(added(padB))
				h.send(button(2, "east", true))
			}
			h.send(button(1, "west", true))
			st.wait(t, st.stalled)
			h.send(button(1, "west", false)) // queued
			// Pending, and so is Pad B's DeviceAdded; a hand-off's Resync
			// is skipped.
			h.send(removed(1))
			if waiting {
				// Of no device: the router takes it once it has handled
				// the removal and the hand-off.
				h.send(button(9, "south", true))
			} else {
				h.send(added(padB))
				h.send(button(2, "east", true)) // skipped: the Resync covers it
			}
			st.release()
			states := h.statesTicking()
			for _, want := range []input.Event{
				button(1, "west", false), removed(1), {Device: 2, Kind: input.DeviceAdded}, {Kind: input.ButtonDown, Control: "east"},
			} {
				if e := st.next(t); e.Kind != want.Kind || e.Device != want.Device || e.Control != want.Control {
					t.Fatalf("OnEvent saw %+v, want %+v", e, want)
				}
			}
			if !states[0].Assigned || states[0].Got != states[0].Want || !states[0].Got.Buttons[control.East] {
				t.Errorf("Sync states %+v, want the slot at Pad B's state", states)
			}
			// The removal released Pad A's east, the Resync pressed Pad B's;
			// the times depend on the retries.
			if got := untimed(h.rec.Trace()); got != "+Space -Space +Space" {
				t.Errorf("output %s, want +Space -Space +Space", got)
			}
			checkAttrs(t, h.waitLog("slot behind; resyncing", 1)[0], map[string]string{"skipped": "1"})
			if got := h.slot0(); got.ID != 2 {
				t.Errorf("Status %+v, want Pad B", got)
			}
		})
	}
}

func TestDroppedResets(t *testing.T) {
	var dropped atomic.Uint64
	h := start(t, opts{dropped: dropped.Load})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	dropped.Add(3)
	h.send(axis(1, "leftx", 5)) // the check after it resets
	h.sync()
	h.expect("+Space@0 -Space@10")
	checkAttrs(t, h.waitLog("input dropped events", 1)[0], map[string]string{"dropped": "3"})
	if st := h.states(); st[0].Got != st[0].Want || st[0].Got.Buttons[control.East] {
		t.Errorf("Sync states %+v, want both reset", st)
	}
	// East is still down on the pad: its release does nothing, the next
	// press works.
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.set(20)
	h.expect("+Space@0 -Space@10 +Space@10")
	// A drop seen by a Sync alone.
	dropped.Add(1)
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@10 -Space@20")
	if got := len(h.logs.with("input dropped events")); got != 2 {
		t.Errorf("%d drop lines, want 2", got)
	}
}

// TestDroppedResetAfterQueued: the reset for a drop comes after the events
// that were queued in Events when the router saw it.
func TestDroppedResetAfterQueued(t *testing.T) {
	var dropped atomic.Uint64
	dropped.Store(1) // before Run, whose baseline is 0
	h := start(t, opts{dropped: dropped.Load, queued: []input.Event{
		added(padA), button(1, "east", true), button(1, "south", true),
	}})
	h.sync()
	h.expect("+Space@0 +LCtrl@0 +Z@0 -Z@0 -Space@0 -LCtrl@0")
	if st := h.states(); st[0].Got != st[0].Want || st[0].Got.Buttons[control.East] {
		t.Errorf("Sync states %+v, want both reset", st)
	}
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 +LCtrl@0 +Z@0 -Z@0 -Space@0 -LCtrl@0 +Space@0")
}

// panicky makes Trace panic on each south press while armed.
type panicky struct{ armed atomic.Bool }

func (p *panicky) trace(_ uint8, d engine.Decision) {
	if p.armed.Load() && d.Control == control.South && d.Edge == engine.EdgeDown {
		panic("test: south")
	}
}

func TestSlotPanicRestarts(t *testing.T) {
	p := &panicky{}
	p.armed.Store(true)
	h := start(t, opts{trace: p.trace})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.send(button(1, "south", true))
	got := h.waitLog("slot panicked", 1)[0]
	checkAttrs(t, got, map[string]string{"slot": "0", "backoff": "100ms"})
	if !strings.Contains(got.Attrs["err"], "test: south") || !strings.Contains(got.Attrs["stack"], "goroutine") {
		t.Errorf("slot panicked: %v, want the panic and its stack", got.Attrs)
	}
	h.rec.WaitFor(t, "+Space@0 -Space@0") // released by the panic
	if got := h.slot0().Restarts; got != 0 {
		t.Errorf("Restarts %d before the backoff", got)
	}
	// Edges sent during the backoff go to the new slot.
	h.send(button(1, "south", false))
	h.clk.Advance(100 * ms)
	h.sync()
	if got := h.slot0(); got.Restarts != 1 || got.Disabled {
		t.Errorf("Status %+v, want 1 restart", got)
	}
	p.armed.Store(false)
	h.send(button(1, "south", true))
	h.set(120)
	h.rec.WaitFor(t, "+Space@0 -Space@0 +LCtrl@100 +Z@100 -Z@120 -LCtrl@120")
	// No resync at the restart: east stays up on the slot until pressed
	// again.
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@0 +LCtrl@100 +Z@100 -Z@120 -LCtrl@120 +Space@120")
}

// TestSlotPanicDisables: 3 panics in 10 s disable the slot until a gamepad
// is next assigned after its device left, one that connects then or one
// that waited and is handed the slot.
func TestSlotPanicDisables(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "connects after", true: "waiting"}[waiting], func(t *testing.T) {
			p := &panicky{}
			p.armed.Store(true)
			h := start(t, opts{trace: p.trace})
			h.send(added(padA))
			for i, backoff := range []time.Duration{100 * ms, 200 * ms} {
				h.send(button(1, "south", true))
				h.waitLog("slot panicked", i+1)
				h.send(button(1, "south", false))
				h.clk.Advance(backoff)
				h.sync()
			}
			h.send(button(1, "east", true))
			h.send(button(1, "south", true))
			got := h.waitLog("slot disabled", 1)[0]
			checkAttrs(t, got, map[string]string{"slot": "0", "panics": "3"})
			h.sync()
			if got := h.slot0(); !got.Disabled || got.Restarts != 2 || got.ID != 1 {
				t.Errorf("Status %+v, want disabled after 2 restarts, the gamepad still assigned", got)
			}
			h.expect("+Space@300 -Space@300")
			// Its edges are discarded; Sync does not wait for it.
			h.send(button(1, "east", false))
			h.send(button(1, "east", true))
			h.sync()
			h.expect("+Space@300 -Space@300")
			// Enabled again when a gamepad is next assigned after its device
			// left, however late the router learned that the slot was
			// disabled.
			p.armed.Store(false)
			if waiting {
				// Pad B waits, holding east; the removal hands it the slot.
				h.send(added(padB))
				h.send(button(2, "east", true))
				h.send(removed(1))
			} else {
				h.send(removed(1))
				h.send(added(padB))
				h.send(button(2, "east", true))
			}
			h.waitLog("slot enabled", 1)
			h.sync()
			if got := h.slot0(); got.Disabled || got.Restarts != 0 || got.ID != 2 {
				t.Errorf("Status %+v, want enabled, 0 restarts, Pad B", got)
			}
			h.expect("+Space@300 -Space@300 +Space@300")
			h.send(button(2, "east", false))
			h.sync()
			h.expect("+Space@300 -Space@300 +Space@300 -Space@300")
		})
	}
}

// TestRunnerPanicEndsRun: a panic or runtime.Goexit of the goroutine that
// supervises a slot, not of the slot loop, ends Run with a *PanicError;
// the slot loops release their keys as at every end of Run.
func TestRunnerPanicEndsRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		die  func()
		want string
	}{
		{"panic", func() { panic("test: runner") }, "test: runner"},
		{"goexit", runtime.Goexit, "runtime.Goexit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &panicky{}
			p.armed.Store(true)
			var armed atomic.Bool
			armed.Store(true)
			// The runner logs "slot panicked" on its own goroutine.
			h := start(t, opts{trace: p.trace, logHook: func(msg string) {
				if msg == "slot panicked" && armed.CompareAndSwap(true, false) {
					tc.die()
				}
			}})
			h.send(added(padA))
			h.send(button(1, "east", true))
			h.send(button(1, "south", true))
			err := h.wait()
			pe, ok := errors.AsType[*supervisor.PanicError](err)
			if !ok || pe.Slot != 0 || !strings.Contains(err.Error(), tc.want) || !strings.Contains(string(pe.Stack), "goroutine") {
				t.Fatalf("Run = %v, want a *supervisor.PanicError of slot 0 with %q and a stack", err, tc.want)
			}
			h.expect("+Space@0 -Space@0")
			if err := h.sup.Sync(context.Background()); !errors.Is(err, supervisor.ErrStopped) {
				t.Errorf("Sync = %v, want ErrStopped", err)
			}
		})
	}
}

// TestSlotPanicDisablesAfterSwap: the gamepad leaves and another takes the
// slot, connecting then or handed it after it waited, before the router
// learns that the third panic disabled it. The new gamepad was assigned
// after the device left, so the slot is enabled again at once, at the new
// gamepad's state.
func TestSlotPanicDisablesAfterSwap(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "connects after", true: "waiting"}[waiting], func(t *testing.T) {
			p := &panicky{}
			p.armed.Store(true)
			var st *stall
			h := start(t, opts{trace: p.trace, onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
			st = newStall(t, "south")
			st.armed.Store(false)
			h.send(added(padA))
			for i, backoff := range []time.Duration{100 * ms, 200 * ms} {
				h.send(button(1, "south", true))
				h.waitLog("slot panicked", i+1)
				h.send(button(1, "south", false))
				h.clk.Advance(backoff)
				h.sync()
			}
			if waiting {
				// Pad A pressed, so Pad B's press takes nothing over.
				h.send(added(padB))
				h.send(button(2, "east", true))
			}
			// The slot stalls on the press that panics the third time. The
			// router routes the swap meanwhile: it has handled the last
			// event sent before the release when it looks at the note of
			// the runner, which comes after.
			st.armed.Store(true)
			h.send(button(1, "south", true))
			st.wait(t, st.stalled)
			h.send(removed(1))
			if !waiting {
				h.send(added(padB))
				h.send(button(2, "east", true))
			}
			st.release()
			h.waitLog("slot disabled", 1)
			h.waitLog("slot enabled", 1)
			states := h.states()
			if !states[0].Ran || !states[0].Assigned || states[0].Got != states[0].Want || !states[0].Got.Buttons[control.East] {
				t.Errorf("Sync states %+v, want the slot at Pad B's state", states)
			}
			// The enabled slot sees Pad B's DeviceAdded, which the dead one
			// left, then the Resync.
			var seen []input.Event
			for len(st.seen) > 0 {
				seen = append(seen, <-st.seen)
			}
			if n := len(seen); n < 2 || seen[n-2].Kind != input.DeviceAdded || seen[n-2].Device != 2 ||
				seen[n-1].Kind != input.ButtonDown || seen[n-1].Control != "east" {
				t.Errorf("OnEvent saw %+v, want Pad B's DeviceAdded and the Resync's east last", seen)
			}
			if got := h.slot0(); got.Disabled || got.Restarts != 0 || got.ID != 2 {
				t.Errorf("Status %+v, want enabled, 0 restarts, Pad B", got)
			}
			h.expect("+Space@300")
			h.send(button(2, "east", false))
			h.sync()
			h.expect("+Space@300 -Space@300")
		})
	}
}

// TestSyncCoversQueued: Sync covers the events still queued in Events when
// it is called, not only those Run took. The router picks between the
// two at random, so the test repeats.
func TestSyncCoversQueued(t *testing.T) {
	for i := range 20 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := start(t, opts{quiet: true, queued: []input.Event{added(padA), button(1, "east", true)}})
			h.sync()
			h.expect("+Space@0")
		})
	}
}

// panicSender panics in Send once armed, then works again: the loop's
// ledger releases through it.
type panicSender struct {
	output.Sender
	armed atomic.Bool
}

func (p *panicSender) Send(events []output.KeyEvent) (int, error) {
	if p.armed.CompareAndSwap(true, false) {
		panic("test: sender")
	}
	return p.Sender.Send(events)
}

func TestOutputLoopDies(t *testing.T) {
	ps := &panicSender{}
	h := start(t, opts{stopErr: true, wrap: func(s output.Sender) output.Sender { ps.Sender = s; return ps }})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.sync()
	ps.armed.Store(true)
	h.send(button(1, "south", true))
	err := h.wait()
	var pe *output.PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("Run = %v, want an error wrapping *output.PanicError", err)
	}
	// The ledger releases what it saw sent, the batch in flight included.
	h.expect("+Space@0 -Z@0 -LCtrl@0 -Space@0")
	h.waitLog("output loop ended", 1)
}

func TestSlotErrorsRateLimited(t *testing.T) {
	errWarn := errors.New("test: warning")
	h := start(t, opts{})
	h.send(added(padA))
	h.rec.With(func(r *simtest.Recorder) {
		for range 3 {
			r.WarnNext(errWarn)
		}
	})
	for _, down := range []bool{true, false, true} {
		h.send(button(1, "east", down))
	}
	h.sync()
	if got := h.logs.with("slot error"); len(got) != 1 || !strings.Contains(got[0].Attrs["err"], "test: warning") {
		t.Fatalf("slot error lines %v, want one", got)
	}
	h.clk.Advance(time.Second)
	h.rec.With(func(r *simtest.Recorder) { r.WarnNext(errWarn) })
	h.send(button(1, "east", false))
	checkAttrs(t, h.waitLog("slot error", 2)[1], map[string]string{"slot": "0", "suppressed": "2"})
}

func TestSyncAfterStop(t *testing.T) {
	h := start(t, opts{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.sup.Sync(ctx); err == nil {
		// Sync may win the race with the canceled ctx: it had nothing to
		// wait for.
		t.Log("Sync with a canceled ctx returned nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("Sync = %v, want context.Canceled", err)
	}
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatal(err)
	}
	if err := h.sup.Sync(context.Background()); !errors.Is(err, supervisor.ErrStopped) {
		t.Errorf("Sync = %v, want ErrStopped", err)
	}
	if err := h.sup.Run(context.Background()); err == nil {
		t.Error("a second Run returned nil")
	}
}

// TestSyncBeforeRun: Sync waits for Run.
func TestSyncBeforeRun(t *testing.T) {
	clk := clock.NewFake()
	rec := simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := out.Stop(); err != nil {
			t.Errorf("output.Loop.Stop: %v", err)
		}
		rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	}()
	events := make(chan input.Event)
	sup, err := supervisor.New(supervisor.Config{Clock: clk, Events: events, Output: out, Program: compile(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*ms)
	defer cancel()
	if err := sup.Sync(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Sync before Run = %v, want DeadlineExceeded", err)
	}
	close(events)
	if err := sup.Run(context.Background()); err != nil {
		t.Errorf("Run = %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	clk := clock.NewFake()
	out := output.StartLoop(clk, simtest.NewSyncRecorder(clk), output.LoopOptions{})
	defer func() { _ = out.Stop() }()
	ok := supervisor.Config{Clock: clk, Events: make(chan input.Event), Output: out, Program: compile(t), Slots: 1}
	if _, err := supervisor.New(ok); err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*supervisor.Config)
		want string
	}{
		{"no clock", func(c *supervisor.Config) { c.Clock = nil }, "clock"},
		{"no events", func(c *supervisor.Config) { c.Events = nil }, "event"},
		{"no output", func(c *supervisor.Config) { c.Output = nil }, "output"},
		{"no program", func(c *supervisor.Config) { c.Program = nil }, "program"},
		{"negative buffer", func(c *supervisor.Config) { c.EdgeBuffer = -1 }, "negative"},
		{"negative slots", func(c *supervisor.Config) { c.Slots = -1 }, "slots"},
		{"too many slots", func(c *supervisor.Config) { c.Slots = supervisor.MaxSlots + 1 }, "slots"},
	} {
		cfg := ok
		tc.edit(&cfg)
		if s, err := supervisor.New(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: New = %v, %v; want an error with %q", tc.name, s, err, tc.want)
		}
	}
	s, err := supervisor.New(ok)
	if err != nil {
		t.Fatal(err)
	}
	want := supervisor.Status{Slots: []supervisor.SlotStatus{{Index: 0, Gen: 1}}}
	if got := s.Status(); !reflect.DeepEqual(got, want) {
		t.Errorf("Status %+v, want %+v", got, want)
	}
}

// TestSlotsConfig: New makes Config.Slots slots, MaxSlots for 0, and Run
// starts every one: a Sync reaches each, with no gamepad assigned.
func TestSlotsConfig(t *testing.T) {
	clk := clock.NewFake()
	rec := simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := out.Stop(); err != nil {
			t.Errorf("output.Loop.Stop: %v", err)
		}
		rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	}()
	for _, tc := range []struct{ slots, want int }{{0, supervisor.MaxSlots}, {1, 1}, {2, 2}, {4, 4}} {
		t.Run(fmt.Sprint(tc.slots), func(t *testing.T) {
			sup, err := supervisor.New(supervisor.Config{
				Clock: clk, Events: make(chan input.Event), Output: out, Program: compile(t), Slots: tc.slots,
			})
			if err != nil {
				t.Fatal(err)
			}
			var want []supervisor.SlotStatus
			for i := range tc.want {
				want = append(want, supervisor.SlotStatus{Index: uint8(i), Gen: 1})
			}
			if got := sup.Status().Slots; !reflect.DeepEqual(got, want) {
				t.Errorf("Status %+v, want %+v", got, want)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- sup.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("Run = %v", err)
					}
				case <-time.After(waitLimit):
					t.Error("Run did not return")
				}
			}()
			sctx, scancel := context.WithTimeout(context.Background(), waitLimit)
			defer scancel()
			st, err := sup.SyncStates(sctx)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if len(st) != tc.want {
				t.Fatalf("Sync reached %d slots, want %d", len(st), tc.want)
			}
			for i, s := range st {
				if !s.Ran || s.Assigned {
					t.Errorf("slot %d: Sync state %+v, want it running with no gamepad", i, s)
				}
			}
		})
	}
}

// slotsEnv is a harness with slotHooks and a Dropped counter, for the
// tests of several slots.
type slotsEnv struct {
	t       *testing.T
	h       *harness
	hooks   *slotHooks
	dropped *atomic.Uint64
}

func startSlots(t *testing.T, o opts) slotsEnv {
	t.Helper()
	e := slotsEnv{t: t, hooks: &slotHooks{}, dropped: &atomic.Uint64{}}
	o.hooks, o.dropped = e.hooks, e.dropped.Load
	e.h = start(t, o)
	return e
}

// TestSlotsIsolated: each slot holds and lets go of its own keys. There is
// one keyboard, so a modifier one slot holds applies to the keys of
// another: Pad A's Ctrl+Z comes with Pad B's Shift down.
func TestSlotsIsolated(t *testing.T) {
	h := start(t, opts{slots: 2, program: compileMulti(t)})
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.sync()
	h.send(button(2, "west", true))
	h.set(10)
	h.expect("+Space@0 +LShift@0")
	if a, b := h.slot(0), h.slot(1); a.ID != 1 || b.ID != 2 {
		t.Fatalf("Status %+v, %+v; want Pad A on slot 0, Pad B on slot 1", a, b)
	}
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.set(30)
	h.rec.WaitFor(t, "+Space@0 +LShift@0 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30")
	// Pad B's removal lets go of its key alone.
	h.send(removed(2))
	h.sync()
	h.expect("+Space@0 +LShift@0 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30 -LShift@30")
	if got := h.owners(); len(got) != 1 || got[0].Slot != 0 {
		t.Errorf("owners %+v, want slot 0's alone", got)
	}
	if got := h.slot(1); got != (supervisor.SlotStatus{Index: 1, Gen: 1}) {
		t.Errorf("slot 1 %+v after the removal, want it free", got)
	}
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 +LShift@0 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30 -LShift@30 -Space@30")
}

// TestSlotsSharedKey: two slots that hold the same key press it once, and
// it goes up once, when the last lets go, whichever way the other does
// (the Arbiter's references).
func TestSlotsSharedKey(t *testing.T) {
	for _, p := range []struct {
		key  string
		prog func(testing.TB) *engine.Program
	}{{"Space", compile}, {"LShift", compile2}} {
		pressed := "+" + p.key + "@0"
		// releaseA checks that nothing went up yet, then lets Pad A go.
		releaseA := func(e slotsEnv) {
			e.t.Helper()
			e.h.expect(pressed)
			e.h.send(button(1, "east", false))
			e.h.sync()
		}
		for _, path := range []struct {
			name string
			run  func(e slotsEnv, prog *engine.Program)
			at   int // when the key goes up, in ms
		}{
			{"release", func(e slotsEnv, _ *engine.Program) {
				e.h.send(button(2, "east", false))
				e.h.sync()
				releaseA(e)
			}, 10},
			{"remove", func(e slotsEnv, _ *engine.Program) {
				e.h.send(removed(2))
				e.h.sync()
				releaseA(e)
			}, 10},
			{"panic", func(e slotsEnv, _ *engine.Program) {
				e.hooks.panicOn(1, "south")
				e.h.send(button(2, "south", true))
				e.h.waitLog("slot panicked", 1)
				e.h.clk.Advance(100 * ms)
				e.h.sync()
				if got := e.h.slot(1).Restarts; got != 1 {
					e.t.Errorf("slot 1 has %d restarts, want 1", got)
				}
				releaseA(e)
			}, 110},
			{"reload", func(e slotsEnv, prog *engine.Program) {
				e.h.reload(prog, 2)
				if got := e.h.owners(); len(got) != 0 {
					e.t.Errorf("owners %+v after the Reload, want none", got)
				}
			}, 10},
			{"drop", func(e slotsEnv, _ *engine.Program) {
				e.dropped.Add(1)
				e.h.sync()
				e.h.checkReset()
			}, 10},
			{"exit", func(e slotsEnv, _ *engine.Program) {
				e.h.cancel()
				if err := e.h.wait(); err != nil {
					e.t.Errorf("Run = %v, want nil", err)
				}
			}, 10},
		} {
			t.Run(p.key+"/"+path.name, func(t *testing.T) {
				prog := p.prog(t)
				e := startSlots(t, opts{slots: 2, program: prog})
				e.h.send(added(padA))
				e.h.send(added(padB))
				e.h.send(button(1, "east", true))
				e.h.send(button(2, "east", true))
				e.h.set(10)
				e.h.expect(pressed)
				path.run(e, prog)
				e.h.expect(fmt.Sprintf("%s -%s@%d", pressed, p.key, path.at))
			})
		}
	}
}

// TestSlotsSharedKeyRetry: the release of a shared key that failed twice
// stays pending with no reference, and the next press of any slot retries
// it first.
func TestSlotsSharedKeyRetry(t *testing.T) {
	fs := newFailSender(t, "-Space", 2)
	h := start(t, opts{slots: 2, wrap: fs.wrap})
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.send(button(2, "east", true))
	h.set(10)
	h.send(button(1, "east", false))
	h.set(20)
	h.send(button(2, "east", false)) // fails, and fails again
	h.sync()
	h.expect("+Space@0")
	checkAttrs(t, h.waitLog("slot error", 1)[0], map[string]string{"slot": "1"})
	var held []output.HeldKey
	if err := h.out.Do(func(a *output.Arbiter) error { held = a.Held(); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || !held[0].Pending || len(held[0].Owners) != 0 {
		t.Errorf("held %+v, want Space pending with no owner", held)
	}
	// Before the retry on the clock (RetryInterval later): Pad A's press
	// retries it.
	h.set(30)
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@30 +Space@30")
	h.set(40)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@30 +Space@30 -Space@40")
}

// TestSlotsMenuMask: the menu mask (OUT-08) goes by the one keyboard that
// every slot shares. Pad A's lone Alt is masked when it goes up, but not
// after Pad B pressed a key while it was down.
func TestSlotsMenuMask(t *testing.T) {
	prog, err := compileBase(
		`"east": {"press": {"type": "keys", "keys": "Alt", "mode": "hold"}}`,
		`"south": {"press": {"type": "keys", "keys": "F13", "mode": "tap"}}`)
	if err != nil {
		t.Fatal(err)
	}
	h := start(t, opts{slots: 2, program: prog})
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.set(10)
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.set(30)
	h.rec.WaitFor(t, "+LAlt@0 +F13@10 -F13@30")
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+LAlt@0 +F13@10 -F13@30 -LAlt@30")
	if a, b := h.slot(0), h.slot(1); a.ID != 1 || b.ID != 2 {
		t.Fatalf("Status %+v, %+v; want Pad A on slot 0, Pad B on slot 1", a, b)
	}
	// Pad B idle: Pad A's Alt alone.
	h.set(40)
	h.send(button(1, "east", true))
	h.set(50)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+LAlt@0 +F13@10 -F13@30 -LAlt@30 +LAlt@40 +vk:0xE8@50 -vk:0xE8@50 -LAlt@50")
}

// startRehomed starts 2 slots where Pad C, standing in for Pad A on slot
// 0, holds east (Space), and Pad A came back while slot 1 was free: it
// took slot 1, and slot 0 became Pad C's home. Nothing went up, and the
// clock is at 10 ms.
func startRehomed(t *testing.T) slotsEnv {
	t.Helper()
	e := startSlots(t, opts{slots: 2})
	h := e.h
	keyA, keyC := input.DeviceKey{GUID: "03ab"}, input.DeviceKey{GUID: "0c0c"}
	h.send(added(padA))
	h.send(added(padB))
	// Both press, so both slots are final and Pad C's press takes nothing
	// over.
	for _, id := range []input.DeviceID{1, 2} {
		h.send(button(id, "north", true))
		h.send(button(id, "north", false))
	}
	h.send(added(padC))
	h.send(button(3, "east", true))
	h.send(removed(1)) // Pad C stands in on slot 0, home Pad A: +Space
	h.send(removed(2)) // slot 1 is free, with no home
	h.send(added(pad(5, "Pad A", "03ab")))
	h.set(10)
	h.expect("+Space@0")
	want := []supervisor.SlotStatus{
		{Index: 0, Key: keyC, ID: 3, Name: "Pad C", Gen: 1, Home: keyC, Provisional: true},
		{Index: 1, Key: keyA, ID: 5, Name: "Pad A", Gen: 1, Home: keyA, Provisional: true},
	}
	if got := h.sup.Status().Slots; !reflect.DeepEqual(got, want) {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	if got := h.logs.with("slot rehomed"); len(got) != 1 {
		t.Errorf("%d lines %q, want 1", len(got), "slot rehomed")
	}
	return e
}

// TestSlotsRehomeReleases: a rehomed stand-in holds its keys across the
// rehome, and every way out lets go of them once. A reclaim cannot reach
// a rehomed slot, whose home is its holder's key; TestSlotsReclaimFull
// reclaims one that holds a key.
func TestSlotsRehomeReleases(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(e slotsEnv)
	}{
		{"removal", func(e slotsEnv) {
			e.h.send(removed(3)) // with east still down
			e.h.sync()
		}},
		{"take-over", func(e slotsEnv) {
			// Pad D waits and presses: slot 0 is the provisional slot
			// assigned longest ago, the hand-off to Pad C before Pad A's
			// return.
			e.h.send(added(padD))
			e.h.send(button(4, "north", true))
			e.h.sync()
			checkAttrs(e.t, e.h.waitLog("slot taken over", 1)[0], map[string]string{"slot": "0", "id": "4", "holder": "3"})
		}},
		{"reload", func(e slotsEnv) { e.h.reload(compile(e.t), 2) }},
		{"panic", func(e slotsEnv) {
			e.hooks.panicOn(0, "south")
			e.h.send(button(3, "south", true))
			e.h.waitLog("slot panicked", 1)
		}},
		{"drop", func(e slotsEnv) {
			e.dropped.Add(1)
			e.h.sync()
		}},
		{"exit", func(e slotsEnv) {
			e.h.cancel()
			if err := e.h.wait(); err != nil {
				e.t.Errorf("Run = %v, want nil", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := startRehomed(t)
			tc.run(e)
			e.h.rec.WaitFor(t, "+Space@0 -Space@10")
		})
	}
}

// TestSlotsReclaimFull: a gamepad that comes back while every slot is
// taken takes its home back from the stand-in there, which lets go of
// what it holds and waits again, first.
func TestSlotsReclaimFull(t *testing.T) {
	h := start(t, opts{slots: 2})
	keyA, keyB := input.DeviceKey{GUID: "03ab"}, input.DeviceKey{GUID: "057e"}
	h.send(added(padA))
	h.send(added(padB))
	for _, id := range []input.DeviceID{1, 2} {
		h.send(button(id, "north", true))
		h.send(button(id, "north", false))
	}
	h.send(added(padC))
	h.send(button(3, "east", true))
	h.set(10)
	h.send(at(10, removed(1)))
	h.set(20)
	h.expect("+Space@10")
	h.send(at(20, added(pad(5, "Pad A", "03ab"))))
	h.sync()
	h.expect("+Space@10 -Space@20")
	checkAttrs(t, h.waitLog("slot reclaimed", 1)[0], map[string]string{"slot": "0", "id": "5", "holder": "3", "holderDevice": "0c0c"})
	want := []supervisor.SlotStatus{
		{Index: 0, Key: keyA, ID: 5, Name: "Pad A", Gen: 1, Home: keyA, Provisional: true},
		{Index: 1, Key: keyB, ID: 2, Name: "Pad B", Gen: 1, Home: keyB},
	}
	if got := h.sup.Status().Slots; !reflect.DeepEqual(got, want) {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	// Pad B leaves: Pad C, first in line, is handed slot 1, holding east.
	h.set(30)
	h.send(at(30, removed(2)))
	h.sync()
	h.expect("+Space@10 -Space@20 +Space@30")
	if got := h.slot(1); got.ID != 3 || got.Home != keyB || !got.Provisional {
		t.Errorf("slot 1 %+v, want Pad C standing in for Pad B", got)
	}
	h.send(button(3, "east", false))
	h.sync()
	h.expect("+Space@10 -Space@20 +Space@30 -Space@30")
}

// TestSlotsTakeOverOldest: a waiting gamepad's press takes over the
// provisional slot assigned longest ago, not the one of lowest index.
func TestSlotsTakeOverOldest(t *testing.T) {
	h := start(t, opts{slots: 2})
	keyA, keyB, keyC, keyD := input.DeviceKey{GUID: "03ab"}, input.DeviceKey{GUID: "057e"}, input.DeviceKey{GUID: "0c0c"}, input.DeviceKey{GUID: "0d0d"}
	h.send(added(padA)) // slot 0
	h.send(added(padB)) // slot 1
	h.send(added(padC)) // waits
	h.send(removed(1))  // slot 0 goes to Pad C, after Pad B took slot 1
	h.send(added(padD)) // waits
	h.set(10)
	h.send(button(4, "south", true))
	h.send(button(4, "south", false))
	h.set(30)
	h.rec.WaitFor(t, "+LCtrl@10 +Z@10 -Z@30 -LCtrl@30")
	checkAttrs(t, h.waitLog("slot taken over", 1)[0], map[string]string{"slot": "1", "id": "4", "holder": "2"})
	want := []supervisor.SlotStatus{
		{Index: 0, Key: keyC, ID: 3, Name: "Pad C", Gen: 1, Home: keyA, Provisional: true},
		{Index: 1, Key: keyD, ID: 4, Name: "Pad D", Gen: 1, Home: keyD},
	}
	if got := h.sup.Status().Slots; !reflect.DeepEqual(got, want) {
		t.Errorf("Status %+v, want %+v", got, want)
	}
	// Pad B waits first; its press takes slot 0, the only provisional one.
	h.set(40)
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.set(60)
	h.rec.WaitFor(t, "+LCtrl@10 +Z@10 -Z@30 -LCtrl@30 +LCtrl@40 +Z@40 -Z@60 -LCtrl@60")
	checkAttrs(t, h.waitLog("slot taken over", 2)[1], map[string]string{"slot": "0", "id": "2", "holder": "3"})
	want[0] = supervisor.SlotStatus{Index: 0, Key: keyB, ID: 2, Name: "Pad B", Gen: 1, Home: keyB}
	if got := h.sup.Status().Slots; !reflect.DeepEqual(got, want) {
		t.Errorf("Status %+v, want %+v", got, want)
	}
}

// TestSlotsPanicIsolated: a slot that panics lets go of its own keys
// alone, and the other slots map on through its backoff.
func TestSlotsPanicIsolated(t *testing.T) {
	e := startSlots(t, opts{slots: 2, program: compileMulti(t)})
	h := e.h
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.sync()
	h.send(button(2, "west", true))
	h.set(10)
	e.hooks.panicOn(1, "south")
	h.send(button(2, "south", true))
	checkAttrs(t, h.waitLog("slot panicked", 1)[0], map[string]string{"slot": "1", "backoff": "100ms"})
	h.rec.WaitFor(t, "+Space@0 +LShift@0 -LShift@10")
	h.send(button(2, "south", false))
	e.hooks.disarm()
	// A Sync would wait for slot 1 to run again.
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.rec.WaitFor(t, "+Space@0 +LShift@0 -LShift@10 +LCtrl@10 +Z@10")
	h.setIdle(30)
	h.rec.WaitFor(t, "+Space@0 +LShift@0 -LShift@10 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30")
	if got := h.slot(1).Restarts; got != 0 {
		t.Errorf("slot 1 has %d restarts during its backoff", got)
	}
	h.setIdle(110)
	h.sync()
	if got := h.slot(1); got.Restarts != 1 || got.Disabled || got.ID != 2 {
		t.Errorf("slot 1 %+v, want Pad B after 1 restart", got)
	}
	// No Resync at the restart: west is up on the slot until pressed again.
	h.send(button(2, "west", false))
	h.send(button(2, "west", true))
	h.sync()
	h.send(button(2, "west", false))
	h.sync()
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 +LShift@0 -LShift@10 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30 +LShift@110 -LShift@110 -Space@110")
}

// TestSlotsDisableIsolated: a disabled slot keeps its gamepad, which maps
// nothing and does not move to a free slot, while the other slots map
// on. Once it leaves, it takes the lowest free slot again, enabled.
func TestSlotsDisableIsolated(t *testing.T) {
	e := startSlots(t, opts{slots: 3})
	h := e.h
	keyB := input.DeviceKey{GUID: "057e"}
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.sync() // before the clock moves
	e.hooks.panicOn(1, "south")
	for i, backoff := range []time.Duration{100 * ms, 200 * ms} {
		h.send(button(2, "south", true))
		h.waitLog("slot panicked", i+1)
		h.send(button(2, "south", false))
		h.clk.Advance(backoff)
		h.sync()
	}
	h.send(button(2, "south", true))
	checkAttrs(t, h.waitLog("slot disabled", 1)[0], map[string]string{"slot": "1", "panics": "3"})
	h.routerDisabledSlot(1)
	if got := h.slot(1); got.Key != keyB || got.ID != 2 || !got.Disabled {
		t.Errorf("slot 1 %+v, want Pad B on it, disabled", got)
	}
	if got := h.slot(2); got != (supervisor.SlotStatus{Index: 2, Gen: 1}) {
		t.Errorf("slot 2 %+v, want it free", got)
	}
	e.hooks.disarm()
	h.send(button(2, "south", false))
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.sync()
	h.expect("+Space@0")
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.set(320)
	h.rec.WaitFor(t, "+Space@0 +LCtrl@300 +Z@300 -Z@320 -LCtrl@320")
	h.send(removed(2))
	h.sync()
	if got := h.slot(1); got.ID != 0 {
		t.Errorf("slot 1 %+v after the removal, want it free", got)
	}
	h.send(added(pad(5, "Pad B", "057e")))
	h.waitLog("slot enabled", 1)
	h.sync()
	if got := h.slot(1); got.ID != 5 || got.Disabled || got.Restarts != 0 {
		t.Errorf("slot 1 %+v, want Pad B back, enabled, 0 restarts", got)
	}
	if got := h.slot(2); got.ID != 0 {
		t.Errorf("slot 2 %+v, want it free", got)
	}
	h.send(button(5, "south", true))
	h.send(button(5, "south", false))
	h.set(340)
	h.rec.WaitFor(t, "+Space@0 +LCtrl@300 +Z@300 -Z@320 -LCtrl@320 +LCtrl@320 +Z@320 -Z@340 -LCtrl@340")
}

// TestSlotsLowestFree: a gamepad takes the free slot of the lowest index.
func TestSlotsLowestFree(t *testing.T) {
	h := start(t, opts{slots: 3})
	keyD := input.DeviceKey{GUID: "0d0d"}
	for _, p := range []*input.DeviceInfo{padA, padB, padC} {
		h.send(added(p))
	}
	h.sync()
	for i, id := range []input.DeviceID{1, 2, 3} {
		if got := h.slot(i); got.ID != id {
			t.Errorf("slot %d %+v, want gamepad %d", i, got, id)
		}
	}
	h.send(removed(2))
	h.sync()
	if got := h.slot(1); got != (supervisor.SlotStatus{Index: 1, Gen: 1}) {
		t.Errorf("slot 1 %+v after the removal, want it free, with no home", got)
	}
	h.send(added(padD))
	h.sync()
	checkAttrs(t, h.waitLog("slot assigned", 4)[3], map[string]string{"slot": "1", "id": "4"})
	want := supervisor.SlotStatus{Index: 1, Key: keyD, ID: 4, Name: "Pad D", Gen: 1, Home: keyD, Provisional: true}
	if got := h.slot(1); got != want {
		t.Errorf("slot 1 %+v, want %+v", got, want)
	}
	if got := h.slot(2); got.ID != 3 {
		t.Errorf("slot 2 %+v, want Pad C still", got)
	}
}

// TestSlotsDropResetsAll: the drop counter is the whole input's, so a drop
// resets every slot.
func TestSlotsDropResetsAll(t *testing.T) {
	e := startSlots(t, opts{slots: 2, program: compileMulti(t)})
	h := e.h
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.sync()
	h.send(button(2, "west", true))
	h.set(10)
	h.expect("+Space@0 +LShift@0")
	e.dropped.Add(1)
	h.sync()
	h.expectInterleaved("+Space@0 -Space@10", "+LShift@0 -LShift@10")
	h.checkReset()
	got := h.waitLog("input dropped events; releasing everything", 1)[0]
	if got.Level != slog.LevelWarn {
		t.Errorf("%q at %v, want a warning", got.Msg, got.Level)
	}
}

// TestSlotsDropAfterQueued: the reset for a drop comes after the events
// that were queued in Events when the router saw it, on every slot.
func TestSlotsDropAfterQueued(t *testing.T) {
	var dropped atomic.Uint64
	dropped.Store(1) // before Run, whose baseline is 0
	h := start(t, opts{slots: 2, dropped: dropped.Load, queued: []input.Event{
		added(padA), added(padB), button(1, "east", true), button(2, "south", true),
	}})
	h.sync()
	h.expectInterleaved("+Space@0 -Space@0", "+LCtrl@0 +Z@0 -Z@0 -LCtrl@0")
	h.checkReset()
	if got := h.owners(); len(got) != 0 {
		t.Errorf("owners %+v, want none", got)
	}
}

// TestSlotsSyncBehind: a slot that stalls falls behind alone; the others
// map on, and Sync catches it up.
func TestSlotsSyncBehind(t *testing.T) {
	e := startSlots(t, opts{slots: 2, buffer: 1})
	h := e.h
	h.send(added(padA))
	h.send(added(padB))
	h.sync()
	e.hooks.stall(1)
	h.send(button(2, "west", true)) // unbound; slot 1 stalls on it
	e.hooks.waitStalled(t, 1)
	h.send(button(2, "west", false)) // queued: the channel holds one
	h.send(axis(2, "leftx", 1000))   // skipped: slot 1 is behind
	// A Sync would wait for slot 1.
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.rec.WaitFor(t, "+LCtrl@0 +Z@0")
	h.setIdle(20)
	h.rec.WaitFor(t, "+LCtrl@0 +Z@0 -Z@20 -LCtrl@20")
	e.hooks.unstall(1)
	states := h.statesTicking()
	if st := states[1]; !st.Assigned || st.Got != st.Want || st.Got.Axes[control.LeftX] != 1000 {
		t.Errorf("slot 1: Sync state %+v, want it at Pad B's state", st)
	}
	lines := h.logs.with("slot behind; resyncing")
	if len(lines) == 0 {
		t.Fatal("no line \"slot behind; resyncing\"")
	}
	for _, r := range h.logs.with("") {
		if r.Level >= slog.LevelWarn && r.Attrs["slot"] != "1" {
			t.Errorf("logged %q at %v with %v, want warnings of slot 1 alone", r.Msg, r.Level, r.Attrs)
		}
	}
	checkAttrs(t, lines[0], map[string]string{"slot": "1", "skipped": "1"})
}

// TestSlotsLogs: the router's lines name the slot.
func TestSlotsLogs(t *testing.T) {
	e := startRehomed(t)
	lines := e.h.waitLog("slot assigned", 4)
	for i, want := range []map[string]string{
		{"slot": "0", "id": "1", "device": "03ab", "handoff": "false"},
		{"slot": "1", "id": "2", "device": "057e", "handoff": "false"},
		{"slot": "0", "id": "3", "device": "0c0c", "handoff": "true"},
		{"slot": "1", "id": "5", "device": "03ab", "reconnected": "true"},
	} {
		checkAttrs(t, lines[i], want)
	}
	checkAttrs(t, e.h.waitLog("slot freed", 2)[1], map[string]string{"slot": "1", "id": "2", "device": "057e"})
	got := e.h.waitLog("slot rehomed", 1)[0]
	checkAttrs(t, got, map[string]string{"slot": "0", "id": "3", "name": "Pad C", "device": "0c0c", "home": "03ab"})
	if got.Attrs["home"] == got.Attrs["device"] {
		t.Errorf("slot rehomed: home %q, want another key than the device's", got.Attrs["home"])
	}
}
