package supervisor_test

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// unroutedLog keeps what Config.OnUnrouted saw.
type unroutedLog struct {
	mu  sync.Mutex
	got []string
}

func (u *unroutedLog) add(e input.Event, why string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	edge := "up"
	if e.Kind == input.ButtonDown {
		edge = "down"
	}
	u.got = append(u.got, e.Control+" "+edge+" "+why+" device="+string('0'+rune(e.Device)))
}

func (u *unroutedLog) list() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.got)
}

// TestUnrouted (ADR-0071): the button edges of a gamepad that no slot maps
// are reported with why, for the diagnostics page: it waits for a slot
// ("queued"), the device filter or the ignore setting leaves it out
// ("ignored"), it has no gamepad mapping ("notGamepad"), or its slot is
// disabled ("disabled"). Mapped gamepads, axes and everything that is no
// button edge are not reported.
func TestUnrouted(t *testing.T) {
	t.Run("queued", func(t *testing.T) {
		var u unroutedLog
		h := start(t, opts{slots: 1, onUnrouted: u.add})
		h.send(added(padA))
		h.send(button(1, "east", true)) // confirms the slot: the press is mapped
		h.send(button(1, "east", false))
		h.send(added(padB))
		h.send(button(2, "south", true))
		h.send(input.Event{Device: 2, Kind: input.AxisMotion, Control: "leftx", Value: 9000})
		h.send(button(2, "south", false))
		h.sync()
		want := []string{"south down queued device=2", "south up queued device=2"}
		if got := u.list(); !slices.Equal(got, want) {
			t.Errorf("unrouted %v, want %v", got, want)
		}
	})
	t.Run("a press that takes the slot over is routed", func(t *testing.T) {
		var u unroutedLog
		h := start(t, opts{slots: 1, onUnrouted: u.add})
		h.send(added(padA))
		h.send(added(padB))
		h.send(button(2, "south", true)) // padA pressed nothing yet: padB takes the slot (IN-05)
		h.send(button(2, "south", false))
		h.sync()
		if got := u.list(); len(got) != 0 {
			t.Errorf("unrouted %v, want none", got)
		}
	})
	t.Run("ignored by the device filter", func(t *testing.T) {
		var u unroutedLog
		h := start(t, opts{slots: 1, device: "Pad A", onUnrouted: u.add})
		h.send(added(padA))
		h.send(added(padB))
		h.send(button(2, "east", true))
		h.send(button(1, "east", true))
		h.sync()
		if got, want := u.list(), []string{"east down ignored device=2"}; !slices.Equal(got, want) {
			t.Errorf("unrouted %v, want %v", got, want)
		}
	})
	t.Run("not a gamepad", func(t *testing.T) {
		var u unroutedLog
		h := start(t, opts{slots: 1, onUnrouted: u.add})
		h.send(input.Event{Device: 4, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 4, Name: "wheel"}})
		h.send(button(4, "south", true))
		// A device nobody announced is not worth an event.
		h.send(button(9, "south", true))
		h.sync()
		if got, want := u.list(), []string{"south down notGamepad device=4"}; !slices.Equal(got, want) {
			t.Errorf("unrouted %v, want %v", got, want)
		}
	})
	t.Run("disabled slot", func(t *testing.T) {
		var u unroutedLog
		p := &panicky{}
		p.armed.Store(true)
		h := start(t, opts{trace: p.trace, onUnrouted: u.add})
		h.send(added(padA))
		for i, backoff := range []int{100, 200, 0} {
			h.send(button(1, "south", true))
			h.waitLog("slot panicked", i+1)
			h.send(button(1, "south", false))
			h.clk.Advance(time.Duration(backoff) * ms)
			h.sync()
		}
		h.waitLog("slot disabled", 1)
		h.routerDisabled()
		p.armed.Store(false)
		u.mu.Lock()
		u.got = nil
		u.mu.Unlock()
		h.send(button(1, "east", true))
		h.sync()
		if got, want := u.list(), []string{"east down disabled device=1"}; !slices.Equal(got, want) {
			t.Errorf("unrouted %v, want %v", got, want)
		}
	})
}

// The reasons are the words padmated sends and the UI reads.
func TestUnroutedWords(t *testing.T) {
	for got, want := range map[string]string{
		supervisor.UnroutedQueued:     "queued",
		supervisor.UnroutedIgnored:    "ignored",
		supervisor.UnroutedNotGamepad: "notGamepad",
		supervisor.UnroutedDisabled:   "disabled",
	} {
		if got != want {
			t.Errorf("reason %q, want %q", got, want)
		}
	}
}

// The reason a gamepad is unrouted costs a device filter match and the ignore
// setting's lookup (strings are built for it), so it is worked out only for
// the button edges that are reported: a queued or ignored gamepad's sticks
// jitter hundreds of times a second on the goroutine that carries the mapped
// gamepads' events too.
func TestUnroutedReasonOnlyForReportedEdges(t *testing.T) {
	var asked atomic.Int64
	ignore := func(input.DeviceKey) bool { asked.Add(1); return false }
	// A second gamepad that waits for the only slot.
	setup := func(t *testing.T, o opts) (*harness, int64) {
		o.slots, o.ignore = 1, ignore
		h := start(t, o)
		h.send(added(padA))
		h.send(button(1, "east", true)) // confirms the slot
		h.send(button(1, "east", false))
		h.send(added(padB))
		h.sync()
		return h, asked.Load()
	}
	t.Run("axes", func(t *testing.T) {
		var u unroutedLog
		h, before := setup(t, opts{onUnrouted: u.add})
		for i := range 50 {
			h.send(axis(2, "leftx", int16(1000+i)))
		}
		h.sync()
		if n := asked.Load() - before; n != 0 {
			t.Errorf("the ignore setting was asked %d times for 50 axis events", n)
		}
		if got := u.list(); len(got) != 0 {
			t.Errorf("unrouted %v, want none", got)
		}
	})
	t.Run("nobody listens", func(t *testing.T) {
		h, before := setup(t, opts{})
		h.send(axis(2, "leftx", 9000))
		h.send(button(2, "south", false)) // an Up takes no slot over
		h.sync()
		if n := asked.Load() - before; n != 0 {
			t.Errorf("the ignore setting was asked %d times with no OnUnrouted", n)
		}
	})
	t.Run("a button edge still has its reason", func(t *testing.T) {
		var u unroutedLog
		h, _ := setup(t, opts{onUnrouted: u.add})
		h.send(button(2, "south", false))
		h.sync()
		if got, want := u.list(), []string{"south up queued device=2"}; !slices.Equal(got, want) {
			t.Errorf("unrouted %v, want %v", got, want)
		}
	})
}
