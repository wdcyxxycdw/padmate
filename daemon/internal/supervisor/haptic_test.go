package supervisor_test

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// TestOnHapticDevice: the haptics of each slot (MAP-25) reach
// Config.OnHapticDevice with the slot's index and its controller's id, from
// the slot's own goroutine, and a controller that is not assigned plays none.
func TestOnHapticDevice(t *testing.T) {
	prog, err := compileBase(
		`"east": {"haptic": "tick", "press": {"type": "keys", "keys": "Space", "mode": "tap"}}`,
		`"west": {"haptic": "error", "press": {"type": "keys", "keys": "Shift", "mode": "tap"}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	h := start(t, opts{slots: 2, program: prog, onHaptic: func(slot uint8, id input.DeviceID, x engine.Haptic) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, fmt.Sprintf("slot %d device %d %s", slot, id, x.Preset))
	}})
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.send(button(1, "east", false))
	h.send(button(2, "west", true))
	h.send(button(2, "west", false))
	h.set(100)
	h.sync()
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(got)
	if want := []string{"slot 0 device 1 tick", "slot 1 device 2 error"}; !slices.Equal(got, want) {
		t.Errorf("haptics %q; want %q", got, want)
	}
}
