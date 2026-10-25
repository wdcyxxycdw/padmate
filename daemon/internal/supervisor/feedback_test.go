package supervisor_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// switchesHolder is a Config.Switches source whose value a test changes while
// the slots run: padmated reads the committed config.json the same way.
type switchesHolder struct {
	v atomic.Pointer[profile.FeedbackSwitches]
}

func newSwitches(sw profile.FeedbackSwitches) *switchesHolder {
	h := &switchesHolder{}
	h.v.Store(&sw)
	return h
}

func (h *switchesHolder) get() profile.FeedbackSwitches   { return *h.v.Load() }
func (h *switchesHolder) set(sw profile.FeedbackSwitches) { h.v.Store(&sw) }

// TestSwitchesReachEverySlot: Config.Switches is the global feedback switches
// of every slot's engine (UI-27, ADR-0067). A haptic follows the rumble switch
// at once; RefreshFeedback has every slot publish its state again, with the
// current switches, behind the edges delivered so far; and a slot without a
// controller publishes with device 0 (padmated drops that).
func TestSwitchesReachEverySlot(t *testing.T) {
	prog, err := compileBase(
		`"east": {"haptic": "tick", "press": {"type": "keys", "keys": "Space", "mode": "tap"}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	sw := newSwitches(profile.DefaultFeedbackSwitches())
	var mu sync.Mutex
	var haptics, states []string
	h := start(t, opts{
		slots: 2, program: prog, switches: sw.get,
		onHaptic: func(slot uint8, id input.DeviceID, x engine.Haptic) {
			mu.Lock()
			defer mu.Unlock()
			haptics = append(haptics, fmt.Sprintf("slot %d device %d %s", slot, id, x.Preset))
		},
		onFeedback: func(slot uint8, id input.DeviceID, f engine.Feedback) {
			mu.Lock()
			defer mu.Unlock()
			states = append(states, fmt.Sprintf("slot %d device %d led=%t pulse=%t", slot, id, f.LED, f.Pulse))
		},
	})
	h.send(added(padA))
	h.send(added(padB))
	tap := func(id input.DeviceID) {
		h.send(button(id, "east", true))
		h.send(button(id, "east", false))
	}
	tap(1)
	h.set(100)
	h.sync()
	sw.set(profile.FeedbackSwitches{LED: true, Pause: true, LowBattery: true}) // rumble off
	tap(2)
	h.set(200)
	h.sync()
	mu.Lock()
	if want := []string{"slot 0 device 1 tick"}; !slices.Equal(haptics, want) {
		t.Errorf("haptics %q; want %q (the second tap came with rumble off)", haptics, want)
	}
	states = nil // what the controllers joining published
	mu.Unlock()

	sw.set(profile.FeedbackSwitches{Rumble: true, Pause: true, LowBattery: true}) // led off
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.sup.RefreshFeedback(ctx); err != nil {
		t.Fatalf("RefreshFeedback: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(states)
	want := []string{"slot 0 device 1 led=false pulse=false", "slot 1 device 2 led=false pulse=false"}
	if !slices.Equal(states, want) {
		t.Errorf("states after RefreshFeedback %q; want %q", states, want)
	}
}

// TestRefreshFeedbackAfterRun: like Sync, it reports ErrStopped once Run has
// returned, and without Config.Switches it still refreshes (the engine has
// every switch on then).
func TestRefreshFeedbackAfterRun(t *testing.T) {
	h := start(t, opts{})
	if err := h.sup.RefreshFeedback(context.Background()); err != nil {
		t.Fatalf("RefreshFeedback while running: %v", err)
	}
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatal(err)
	}
	if err := h.sup.RefreshFeedback(context.Background()); err == nil {
		t.Fatal("RefreshFeedback after Run succeeded")
	}
}
