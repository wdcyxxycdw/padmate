package slot_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// TestRepeatStopsAfterRelease checks M2's acceptance that repeat stops as
// soon as its control is released (OUT-11): through a slot and its
// Arbiter, no edge comes later than tapPressMs + 10 ms after the release,
// whatever the phase of the release in the repeat period, and nothing is
// held before ReleaseAll.
func TestRepeatStopsAfterRelease(t *testing.T) {
	// The first repeat is at 30 ms, then one every
	// max(1 s / 50, tapPressMs + tapGapMs) = 40 ms.
	const bind = `"east": {"press": {"type": "keys", "keys": "F13", "mode": "hold", "repeat": {"delayMs": 30, "rateHz": 50}}}`
	t.Run("typematic", func(t *testing.T) {
		for _, release := range []int{10, 29, 30, 31, 50, 69, 70, 71, 109, 110, 111, 250} {
			t.Run(strconv.Itoa(release), func(t *testing.T) {
				h := newHarness(t, 0, 0, bind)
				h.replay(t, fmt.Sprintf("+east@0 -east@%d", release))
				simtest.AssertNoneHeld(t, h.Arb, h.Out)
				if d, ok := h.s.NextDeadline(); ok {
					t.Errorf("slot deadline %v after the replay", d)
				}
				limit := clock.Time(time.Duration(release)*ms + engine.DefaultTapPress + 10*ms)
				downs := 0
				for _, e := range h.Out.Edges() {
					if e.T > limit {
						t.Errorf("edge %s after the release at %d ms", e, release)
					}
					if e.Kind == simtest.Down {
						downs++
					}
				}
				// The press, then each repeat due by the release: a
				// deadline runs before an event at the same time.
				want := 1
				for r := 30; r <= release; r += 40 {
					want++
				}
				if downs != want {
					t.Errorf("%d presses of F13 (%s), want %d", downs, h.Output(), want)
				}
				edges := len(h.Out.Edges())
				if err := h.s.ReleaseAll(); err != nil {
					t.Fatal(err)
				}
				h.Advance(time.Second)
				if n := len(h.Out.Edges()); n != edges {
					t.Errorf("ReleaseAll and a second later sent %d more edges: %s", n-edges, h.Output())
				}
			})
		}
	})
	t.Run("tap", func(t *testing.T) {
		const tapBind = `"east":{"press":{"type":"keys","keys":"F13","repeat":{"style":"tap","delayMs":0,"rateHz":1000}}}`
		for _, release := range []int{10, 39, 40, 41, 59, 60, 61, 79, 80, 81, 250} {
			t.Run(strconv.Itoa(release), func(t *testing.T) {
				h := newHarness(t, 0, 0, tapBind)
				if err := h.Arb.SetTiming(20*ms, 20*ms); err != nil {
					t.Fatal(err)
				}
				h.replay(t, fmt.Sprintf("+east@0 -east@%d", release))
				simtest.AssertNoneHeld(t, h.Arb, h.Out)
				downs := 0
				limit := clock.Time(time.Duration(release)*ms + engine.DefaultTapPress + 10*ms)
				for _, edge := range h.Out.Edges() {
					if edge.T > limit {
						t.Errorf("late output %s after release at %d", edge, release)
					}
					if edge.Kind == simtest.Down {
						downs++
					}
				}
				if want := 1 + release/40; downs != want {
					t.Fatalf("%d taps, want %d: %s", downs, want, h.Output())
				}
				count := len(h.Out.Edges())
				if err := h.s.ReleaseAll(); err != nil {
					t.Fatal(err)
				}
				h.Advance(time.Second)
				if len(h.Out.Edges()) != count {
					t.Fatal("output after release settled")
				}
			})
		}
	})
	// east's release comes after south's press, which waits in the buffer
	// for west's gesture until 350 ms, so the release waits too and F13
	// comes up at 350 ms, after Ctrl+Z (ROADMAP §5.2). The repeat stops at
	// the release all the same.
	t.Run("buffered", func(t *testing.T) {
		const others = `"west": {"tap": {"type": "keys", "keys": "F14"}, "hold": {"type": "keys", "keys": "E"}},
			"south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`
		for _, release := range []int{106, 109, 110, 111, 149, 150, 151, 300} {
			t.Run(strconv.Itoa(release), func(t *testing.T) {
				h := newHarness(t, 0, 0, bind, others)
				h.replay(t, fmt.Sprintf("+east@0 +west@100 +south@105 -east@%d -south@400 -west@410", release))
				simtest.AssertNoneHeld(t, h.Arb, h.Out)
				limit := clock.Time(time.Duration(release)*ms + engine.DefaultTapPress + 10*ms)
				downs := 0
				var up clock.Time
				for _, e := range h.Out.Edges() {
					if e.Name != "F13" {
						continue
					}
					if e.Kind == simtest.Down {
						downs++
						if e.T > limit {
							t.Errorf("F13 pressed at %v, after the release at %d ms", e.T, release)
						}
					} else {
						up = e.T
					}
				}
				want := 1
				for r := 30; r <= release; r += 40 {
					want++
				}
				if downs != want {
					t.Errorf("%d presses of F13 (%s), want %d", downs, h.Output(), want)
				}
				if want := clock.Time(350 * ms); up != want {
					t.Errorf("F13 released at %v (%s), want %v, after west's hold", up, h.Output(), want)
				}
			})
		}
	})
}

func TestTapRepeatReleaseDropsOnlyItsQueuedRepeat(t *testing.T) {
	h := newHarness(t, 0, 0, `"east":{"press":{"type":"keys","keys":"F13","repeat":{"style":"tap","delayMs":0,"rateHz":1000}}}`)
	if err := h.Arb.SetTiming(20*ms, 20*ms); err != nil {
		t.Fatal(err)
	}
	h.s.Handle(button("east", true))
	h.Clock.Set(clock.Time(20 * ms))
	h.s.Advance()
	h.Clock.Set(clock.Time(30 * ms))
	other := output.Owner{Slot: 1, Gen: 1, Source: output.SourceScript}
	if err := h.Arb.Tap(other, keys.MustParse("F13"), 100*ms); err != nil {
		t.Fatal(err)
	}
	h.Clock.Set(clock.Time(40 * ms))
	h.s.Advance()
	if h.Arb.Queued() != 1 {
		t.Fatalf("repeat queue=%d", h.Arb.Queued())
	}
	h.Clock.Set(clock.Time(50 * ms))
	h.s.Handle(button("east", false))
	if h.Arb.Queued() != 0 {
		t.Fatal("physical release kept its queued repeat")
	}
	h.Settle()
	if got := h.Output(); got != "+F13@0 -F13@20 +F13@40 -F13@140" {
		t.Fatalf("output=%s", got)
	}
}
