package slot_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Bindings of the mouse-button tests (ADR-0062): a drag, a drag under Shift
// and a click.
const (
	eastDrag   = `"east": {"label": "拖动", "press": {"type": "keys", "keys": "MouseLeft", "mode": "hold"}}`
	ltShiftRMB = `"lt": {"label": "右键", "press": {"type": "keys", "keys": "Shift+RButton", "mode": "hold"}}`
	southClick = `"south": {"label": "点击", "press": {"type": "keys", "keys": "MouseMiddle", "mode": "tap"}}`
)

// A mouse button held by a hold binding goes up on every path that ends the
// slot's hold (CLAUDE.md invariant 1), as a key does: the control's release, a
// ReleaseAll (the exit and the emergency release), a reload (a profile switch),
// the pause (also by the emergency combo), a removed device, Reset's relatives.
// Afterwards nothing is held, and the late physical release does nothing.
func TestMouseButtonReleasedOnEveryPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(t *testing.T, h *harness)
		out  string
	}{
		{"control release", func(t *testing.T, h *harness) { h.replay(t, "-east@100 lefttrigger=0@100") },
			"-MouseLeft@100 -MouseRight@100 -LShift@100"},
		{"ReleaseAll", func(t *testing.T, h *harness) {
			h.at(t, 100)
			if err := h.s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		}, "-MouseRight@100 -MouseLeft@100 -LShift@100"},
		{"ReleaseAllForExit", func(t *testing.T, h *harness) {
			h.at(t, 100)
			if err := h.s.ReleaseAllForExit(); err != nil {
				t.Fatal(err)
			}
		}, "-MouseRight@100 -MouseLeft@100 -LShift@100"},
		{"pause", func(t *testing.T, h *harness) {
			h.at(t, 100)
			if err := h.s.SetPaused(true); err != nil {
				t.Fatal(err)
			}
		}, "-MouseRight@100 -MouseLeft@100 -LShift@100"},
		{"pause combo", func(t *testing.T, h *harness) {
			if err := h.s.Replay(h.Clock, comboTrace(t, "combo@100")); err != nil {
				t.Fatal(err)
			}
		}, "-MouseRight@100 -MouseLeft@100 -LShift@100"},
		{"reload", func(t *testing.T, h *harness) {
			h.at(t, 100)
			if err := h.s.Reload(compile(t, southClick), 2); err != nil {
				t.Fatal(err)
			}
		}, "-MouseRight@100 -MouseLeft@100 -LShift@100"},
		{"device removed", func(t *testing.T, h *harness) {
			h.at(t, 100)
			h.s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 7})
		}, "-MouseRight@100 -MouseLeft@100 -LShift@100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, 0, 0, eastDrag, ltShiftRMB)
			h.replay(t, "+east@0 lefttrigger=0.5@10")
			h.ExpectOutput("+MouseLeft@0 +LShift@10 +MouseRight@10")
			tc.path(t, h)
			h.ExpectOutput(tc.out)
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if d, ok := h.s.NextDeadline(); ok {
				t.Errorf("NextDeadline = %v after the release, want none", d)
			}
			// The late physical release of the controls sends nothing more.
			h.replay(t, "-east@200 lefttrigger=0@210")
			h.ExpectOutput("")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
		})
	}
}

// A click is a tap of the button: it goes up at the end of its hold, and a
// release path in the middle of it lets go at once.
func TestMouseButtonClick(t *testing.T) {
	h := newHarness(t, 0, 0, southClick, eastDrag)
	h.replay(t, "+south@0 -south@5")
	h.at(t, 20)
	h.s.Advance()
	h.ExpectOutput("+MouseMiddle@0 -MouseMiddle@20")

	h.at(t, 100)
	h.s.Handle(button("south", true))
	h.at(t, 110)
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("+MouseMiddle@100 -MouseMiddle@110")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if d, ok := h.s.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after the release, want none: the click ended", d)
	}
}

// Two slots hold the same button: it goes up with the last release, and a slot
// that goes away lets go of its own hold only.
func TestMouseButtonSharedBetweenSlots(t *testing.T) {
	h := newHarness(t, 0, 0, eastDrag)
	other := h.newSlot(t, 1, 1, compile(t, `"west": {"press": {"type": "keys", "keys": "LButton", "mode": "hold"}}`))
	h.replay(t, "+east@0")
	other.Handle(button("west", true))
	h.ExpectOutput("+MouseLeft@0")
	h.at(t, 50)
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("") // the other slot still holds the button
	if got, want := owners(h.Arb), []string{"1/1/decl/1"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q, want %q", got, want)
	}
	h.at(t, 60)
	if err := other.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("-MouseLeft@60")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
}

// RunLoop lets go of a held button when its edges end, its context is done
// and a job or the trace panics, through a plain Arbiter and through the
// T-output loop: the same exit paths as a key's (invariant 1).
func TestRunLoopReleasesMouseButtons(t *testing.T) {
	bindings := []string{eastDrag, ltShiftRMB, southClick}
	for _, remote := range []bool{false, true} {
		name := "plain"
		if remote {
			name = "remote"
		}
		t.Run(name+" edges closed", func(t *testing.T) {
			h := startSlotLoop(t, loopOpts{bindings: bindings, remote: remote})
			h.button("east", true)
			h.set(50)
			close(h.edges)
			if err := h.wait(); err != nil {
				t.Errorf("RunLoop = %v, want nil", err)
			}
			expect(t, h.rec, "+MouseLeft@0 -MouseLeft@50")
		})
		t.Run(name+" canceled", func(t *testing.T) {
			h := startSlotLoop(t, loopOpts{bindings: bindings, remote: remote})
			h.button("east", true)
			h.set(50)
			h.send(slot.Edge{Event: button("lt", true)})
			h.cancel()
			if err := h.wait(); !errors.Is(err, context.Canceled) {
				t.Errorf("RunLoop = %v, want context.Canceled", err)
			}
			if got := h.rec.Trace(); !strings.HasPrefix(got, "+MouseLeft@0") || strings.Count(got, "-MouseLeft") != 1 {
				t.Errorf("output %s, want the button released once", got)
			}
		})
		t.Run(name+" job panics", func(t *testing.T) {
			h := startSlotLoop(t, loopOpts{bindings: bindings, remote: remote})
			h.button("east", true)
			h.jobJust(func(*slot.Slot) { panic("boom") })
			var pe *slot.PanicError
			if err := h.wait(); !errors.As(err, &pe) {
				t.Fatalf("RunLoop = %v, want a *PanicError", err)
			}
			if pe.ReleaseErr != nil {
				t.Errorf("ReleaseErr = %v, want nil", pe.ReleaseErr)
			}
			expect(t, h.rec, "+MouseLeft@0 -MouseLeft@0")
		})
	}

	t.Run("pause edge", func(t *testing.T) {
		h := startSlotLoop(t, loopOpts{bindings: bindings})
		h.button("east", true)
		h.set(30)
		h.edge(slot.Edge{Pause: slot.PauseOn})
		expect(t, h.rec, "+MouseLeft@0 -MouseLeft@30")
		h.expectSlot(true, stateWith(t, "east"))
		h.set(40)
		h.button("east", false)
		expect(t, h.rec, "+MouseLeft@0 -MouseLeft@30")
	})

	t.Run("trace panics", func(t *testing.T) {
		boom := func(d engine.Decision) {
			if d.Control.String() == "south" {
				panic("boom")
			}
		}
		h := startSlotLoop(t, loopOpts{bindings: bindings, trace: boom})
		h.button("east", true)
		h.send(slot.Edge{Event: button("south", true)})
		var pe *slot.PanicError
		if err := h.wait(); !errors.As(err, &pe) {
			t.Fatalf("RunLoop = %v, want a *PanicError", err)
		}
		expect(t, h.rec, "+MouseLeft@0 -MouseLeft@0")
	})
}

// A tap repeat is repeated clicks: each is a press and a release of the button,
// so it is never down twice. (A hold binding with a repeat is rejected by
// Compile, so the engine has no button to repeat typematically.)
func TestMouseButtonTapRepeatClicks(t *testing.T) {
	h := newHarness(t, 0, 0,
		`"east": {"press": {"type": "keys", "keys": "MouseLeft", "mode": "tap", "repeat": {"delayMs": 100, "rateHz": 10}}}`)
	h.replay(t, "+east@0 -east@305")
	// A click at the press, then one every 100 ms while it is held; each click
	// is down and up, so the button is never down twice.
	h.ExpectOutput("+MouseLeft@0 -MouseLeft@20 +MouseLeft@100 -MouseLeft@120 +MouseLeft@200 -MouseLeft@220 +MouseLeft@300 -MouseLeft@320")

}
