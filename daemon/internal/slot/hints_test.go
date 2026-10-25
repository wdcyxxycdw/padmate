package slot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The key hints (HUD-03, ADR-0063) through a Slot: the slot's View carries the
// list while a hints action is held, and the paths that end what a slot holds
// end it, like the pointer of a stick (mouse_move_test.go).

// hintsBindings has the hints on rightshoulder and two labels.
var hintsBindings = []string{
	`"rightshoulder": {"press": {"type": "hud", "op": "hints"}}`,
	`"south": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`,
	`"east": {"press": {"type": "keys", "keys": "Space", "mode": "hold"}}`,
}

// shownHints is the hints of the slot's View as text, "off" when they are off.
func shownHints(h *harness) string {
	v := h.s.View().Mapping.Hints
	if v == nil {
		return "off"
	}
	parts := make([]string, len(v))
	for i, hint := range v {
		parts[i] = hint.Control.String() + "=" + hint.Label
	}
	return "on:" + strings.Join(parts, " | ")
}

func (h *harness) button(t *testing.T, msec int, control string, down bool) {
	t.Helper()
	h.drive(t, msec)
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	h.s.Handle(input.Event{Kind: kind, Control: control})
}

// The hints are on while the button is held and off at the release; the slot
// sends no key for them.
func TestHintsInTheSlotView(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, 0, 1, compile(t, hintsBindings...))
	if got := shownHints(h); got != "off" {
		t.Fatalf("before: %s", got)
	}
	h.button(t, 10, "rightshoulder", true)
	if got, want := shownHints(h), "on:south=撤销 | east=Space"; got != want {
		t.Errorf("held: %s, want %s", got, want)
	}
	h.drive(t, 500)
	if got, want := shownHints(h), "on:south=撤销 | east=Space"; got != want {
		t.Errorf("held a while: %s, want %s", got, want)
	}
	h.button(t, 600, "rightshoulder", false)
	if got := shownHints(h); got != "off" {
		t.Errorf("released: %s", got)
	}
	if edges := h.Out.Edges(); len(edges) != 0 {
		t.Errorf("the hints sent %v", edges)
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
}

// The hints end on every path that ends what a slot holds: a ReleaseAll (the exit
// and the emergency release), a pause, a reload, a removed device and a Reset;
// the button that is still down starts nothing again.
func TestHintsEndOnEverySlotPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(t *testing.T, h *harness)
	}{
		{"ReleaseAll", func(t *testing.T, h *harness) {
			if err := h.s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		}},
		{"ReleaseAllForExit", func(t *testing.T, h *harness) {
			if err := h.s.ReleaseAllForExit(); err != nil {
				t.Fatal(err)
			}
		}},
		{"pause", func(t *testing.T, h *harness) {
			if err := h.s.SetPaused(true); err != nil {
				t.Fatal(err)
			}
		}},
		{"reload", func(t *testing.T, h *harness) {
			if err := h.s.Reload(compile(t, hintsBindings...), 2); err != nil {
				t.Fatal(err)
			}
		}},
		{"device removed", func(t *testing.T, h *harness) {
			h.s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 7})
		}},
		{"reset", func(t *testing.T, h *harness) { h.s.Reset() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness{Sim: simtest.New(t)}
			h.s = h.newSlot(t, 0, 1, compile(t, hintsBindings...))
			h.button(t, 10, "rightshoulder", true)
			if !strings.HasPrefix(shownHints(h), "on:") {
				t.Fatalf("before the path: %s", shownHints(h))
			}
			tc.path(t, h)
			if got := shownHints(h); got != "off" {
				t.Errorf("after %s: %s", tc.name, got)
			}
			h.drive(t, 1000)
			if got := shownHints(h); got != "off" {
				t.Errorf("a second later: %s", got)
			}
			if d, ok := h.s.NextDeadline(); ok {
				t.Errorf("NextDeadline = %v after the stop, want none", d)
			}
			// The release of the button that is held across does nothing.
			if tc.name != "device removed" && tc.name != "reset" {
				h.button(t, 1100, "rightshoulder", false)
				if got := shownHints(h); got != "off" {
					t.Errorf("after the release: %s", got)
				}
			}
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if errs := h.s.Errors(); len(errs) > 0 {
				t.Errorf("slot errors: %v", errs)
			}
		})
	}
}

// Pausing keeps them off: a press while paused runs nothing; resuming does not
// press again.
func TestHintsWhileTheSlotIsPaused(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, 0, 1, compile(t, hintsBindings...))
	if err := h.s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	h.button(t, 10, "rightshoulder", true)
	if got := shownHints(h); got != "off" {
		t.Errorf("a press while paused: %s", got)
	}
	if err := h.s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if got := shownHints(h); got != "off" {
		t.Errorf("resumed with the button down: %s", got)
	}
	h.button(t, 20, "rightshoulder", false)
	h.button(t, 30, "rightshoulder", true)
	if got := shownHints(h); !strings.HasPrefix(got, "on:") {
		t.Errorf("a press after resuming: %s", got)
	}
	h.button(t, 40, "rightshoulder", false)
}

// The hold gesture of the same binding as a tap: a tap shows nothing; the hold
// shows the hints from the moment it is recognized, deadline included.
func TestHintsHoldGestureInTheSlot(t *testing.T) {
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, 0, 1, compile(t,
		`"rightshoulder": {"tap": {"type": "keys", "keys": "T", "mode": "tap"}, "hold": {"type": "hud", "op": "hints"}}`,
		`"south": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`))
	h.button(t, 10, "rightshoulder", true)
	if got := shownHints(h); got != "off" {
		t.Errorf("pressed: %s", got)
	}
	d, ok := h.s.NextDeadline()
	if !ok {
		t.Fatal("no deadline for the hold")
	}
	if want := 10*ms + engine.DefaultHold; time.Duration(d) != want {
		t.Errorf("the hold is recognized at %v, want %v", time.Duration(d), want)
	}
	h.drive(t, 400)
	if got, want := shownHints(h), "on:south=撤销"; got != want {
		t.Errorf("held: %s, want %s", got, want)
	}
	h.button(t, 500, "rightshoulder", false)
	if got := shownHints(h); got != "off" {
		t.Errorf("released: %s", got)
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
}
