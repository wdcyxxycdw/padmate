package output_test

import (
	"cmp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// paced is a Sim option: an Arbiter with an event gap (OUT-17).
func paced(gap, minPress, minGap time.Duration) simtest.Option {
	return simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: gap, MinPress: minPress, MinGap: minGap})
}

// expectPaced checks that every Send call but the forced ones (by call
// index) has one event and comes at least gap after the call before it.
func expectPaced(t *testing.T, r *simtest.Recorder, gap time.Duration, forced ...int) {
	t.Helper()
	type call struct {
		n  int
		at clock.Time
	}
	var calls []call
	for _, rec := range r.Records() {
		if rec.Batch == len(calls) {
			calls = append(calls, call{at: rec.T})
		}
		calls[rec.Batch].n++
	}
	for i, c := range calls {
		skip := false
		for _, f := range forced {
			skip = skip || f == i
		}
		if skip {
			continue
		}
		if c.n != 1 {
			t.Errorf("Send call %d has %d events, want 1", i, c.n)
		}
		if i > 0 && c.at.Sub(calls[i-1].at) < gap {
			t.Errorf("Send call %d at %v, %v after the one before, want at least %v", i, c.at, c.at.Sub(calls[i-1].at), gap)
		}
	}
}

func TestEventGap(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		gap, minPress, minGap time.Duration // gap 0 means 10 ms
		run                   func(s tsim)
		want                  string
	}{{
		name: "tap",
		run:  func(s tsim) { s.tap(ownerA, "Ctrl+Shift+F13", 0) },
		want: "+LCtrl@0 +LShift@10 +F13@20 -F13@30 -LShift@40 -LCtrl@50",
	}, {
		// MinPress counts from each key's own Send; the chord goes up
		// together once the last key pressed has had it.
		name: "tap with MinPress", minPress: 20 * ms,
		run:  func(s tsim) { s.tap(ownerA, "Ctrl+Shift+F13", 0) },
		want: "+LCtrl@0 +LShift@10 +F13@20 -F13@40 -LShift@50 -LCtrl@60",
	}, {
		name: "hold",
		run: func(s tsim) {
			s.down(ownerA, "Ctrl+Z")
			s.Advance(5 * ms)
			s.up(ownerA, "Ctrl+Z")
		},
		want: "+LCtrl@0 +Z@10 -Z@20 -LCtrl@30",
	}, {
		name: "hold with MinPress", minPress: 20 * ms,
		run: func(s tsim) {
			s.down(ownerA, "Ctrl+Z")
			s.Advance(5 * ms)
			s.up(ownerA, "Ctrl+Z")
		},
		want: "+LCtrl@0 +Z@10 -Z@30 -LCtrl@40",
	}, {
		// One stream for every owner.
		name: "two owners",
		run: func(s tsim) {
			s.tap(ownerA, "Ctrl+Z", 0)
			s.tap(ownerB, "F13", 0)
		},
		want: "+LCtrl@0 +Z@10 -Z@20 -LCtrl@30 +F13@40 -F13@50",
	}, {
		// MinGap counts from the release that went out.
		name: "MinGap", minGap: 20 * ms,
		run: func(s tsim) {
			s.tap(ownerA, "F13", 0)
			s.tap(ownerA, "F13", 0)
		},
		want: "+F13@0 -F13@10 +F13@30 -F13@40",
	}, {
		name: "repeat",
		run: func(s tsim) {
			s.down(ownerA, "F13")
			s.repeat(ownerA, "F13")
			s.Advance(15 * ms)
			s.up(ownerA, "F13")
		},
		want: "+F13@0 +F13@10 -F13@20",
	}, {
		// Up drops the repeat still queued: no press after the button.
		name: "repeat dropped by Up",
		run: func(s tsim) {
			s.down(ownerA, "F13")
			s.repeat(ownerA, "F13")
			s.up(ownerA, "F13")
		},
		want: "+F13@0 -F13@10",
	}, {
		// A Down's modifier takes over a release still queued, as one
		// MinPress delays.
		name: "take over",
		run: func(s tsim) {
			s.tap(ownerA, "Ctrl+Z", 0)
			s.Advance(ms)
			s.ok("DownFor", ownerB, "Ctrl+X", s.Arb.DownFor(ownerB, keys.MustParse("Ctrl+X"), 0))
			s.Advance(30 * ms)
			s.up(ownerB, "Ctrl+X")
		},
		want: "+LCtrl@0 +Z@10 -Z@20 +X@30 -X@40 -LCtrl@50",
	}, {
		// The main key of a Down presses again behind its queued release,
		// also a modifier: two presses stay two.
		name: "press again",
		run: func(s tsim) {
			s.tap(ownerA, "Ctrl+Z", 0)
			s.Advance(ms)
			s.down(ownerB, "LCtrl")
			s.Advance(30 * ms)
			s.up(ownerB, "LCtrl")
		},
		want: "+LCtrl@0 +Z@10 -Z@20 -LCtrl@30 +LCtrl@40 -LCtrl@50",
	}, {
		// A tap's hold counts from the Send of its main key, as a binding's
		// tapPressMs or a script's tap asks.
		name: "tap hold", gap: 50 * ms, minPress: 20 * ms,
		run:  func(s tsim) { s.tap(ownerA, "Ctrl+Shift+Z", 100*ms) },
		want: "+LCtrl@0 +LShift@50 +Z@100 -Z@200 -LShift@250 -LCtrl@300",
	}, {
		// A modifier another reference held goes up after the key a tap
		// pressed with it, whose MinPress counts from a later Send.
		name: "modifier after the key", minPress: 30 * ms,
		run: func(s tsim) {
			s.down(ownerA, "Ctrl")
			s.tap(ownerB, "Ctrl+S", 0)
			s.up(ownerA, "Ctrl")
		},
		want: "+LCtrl@0 +S@10 -S@40 -LCtrl@50",
	}, {
		// The same when a tap takes the modifier over from an earlier tap
		// whose key still waits.
		name: "modifier taken over", minPress: 30 * ms,
		run: func(s tsim) {
			s.down(ownerB, "Shift")
			s.tap(ownerA, "Ctrl+Z", 0)
			s.tap(ownerA, "Ctrl+Shift", 0)
			s.Settle()
			s.up(ownerB, "Shift")
		},
		want: "+LShift@0 +LCtrl@10 +Z@20 +LShift@30 -Z@50 -LCtrl@60 -LShift@70",
	}, {
		// With a gap above MinPress the chord still goes up key first.
		name: "gap above MinPress", gap: 25 * ms, minPress: 20 * ms,
		run:  func(s tsim) { s.tap(ownerA, "Ctrl+Shift+F13", 0) },
		want: "+LCtrl@0 +LShift@25 +F13@50 -F13@75 -LShift@100 -LCtrl@125",
	}, {
		// The release deadline passes while the press waits behind other
		// events; the minimum counts from the press that went out.
		name: "deadline before the press",
		run: func(s tsim) {
			s.tap(ownerB, "F14", 0)
			s.tap(ownerB, "F15", 0)
			s.ok("DownFor", ownerA, "F13", s.Arb.DownFor(ownerA, keys.MustParse("F13"), 20*ms))
			s.Advance(ms)
			s.up(ownerA, "F13")
		},
		want: "+F14@0 -F14@10 +F15@20 -F15@30 +F13@40 -F13@60",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			gap := cmp.Or(tc.gap, 10*ms)
			s := newSim(t, paced(gap, tc.minPress, tc.minGap))
			tc.run(s)
			s.Settle()
			if got := s.Output(); got != tc.want {
				t.Errorf("output:\n got %s\nwant %s", got, tc.want)
			}
			expectPaced(t, s.Out, gap)
		})
	}
}

// TestEventGapZero: without a gap a tap is one Send, as before OUT-17.
func TestEventGapZero(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "Ctrl+Shift+F13", 0)
	s.expectBatches(0, "[+LCtrl +LShift +F13 -F13 -LShift -LCtrl]")
	if got := s.Arb.EventGap(); got != 0 {
		t.Errorf("EventGap() = %v, want 0", got)
	}
}

// TestEventGapZeroAllocs: without a gap a tap and the Advance that ends
// it allocate no more than before OUT-17 (17 times, on 28f0078).
func TestEventGapZeroAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, nopSender{}, output.ArbiterOptions{})
	c := keys.MustParse("Ctrl+F13")
	if n := testing.AllocsPerRun(100, func() {
		_ = arb.Tap(ownerA, c, 5*ms)
		clk.Advance(5 * ms)
		_ = arb.Advance()
	}); n > 17 {
		t.Errorf("%v allocations per tap and Advance, want at most 17", n)
	}
}

// TestEventGapReleaseAll: the forced release goes out at once in one
// batch; presses never sent are dropped, not sent and released. The mask
// key keeps the modifiers left alone from making a hotkey (Ctrl+Shift
// switches the layout).
func TestEventGapReleaseAll(t *testing.T) {
	t.Run("mid chord", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 20*ms, 0))
		s.down(ownerA, "Ctrl+Shift+F13")
		s.Advance(3 * ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+LCtrl] [+vk:0xE8 -vk:0xE8 -LCtrl]")
		s.ExpectOutput("+LCtrl@0 +vk:0xE8@3 -vk:0xE8@3 -LCtrl@3")
		s.expectHeld("")
		s.expectDeadline(-1)
		s.Settle()
		s.ExpectOutput("")
	})
	t.Run("Ctrl+Shift", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0), simtest.WithMenuMask())
		s.down(ownerA, "Ctrl+Shift+F13")
		s.Advance(15 * ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+LCtrl] [+LShift] [+vk:0xE8 -vk:0xE8 -LShift -LCtrl]")
		s.expectHeld("")
		s.expectDeadline(-1)
	})
	// Alt is the last modifier: the MenuMask masks its release, and the
	// Arbiter's mask key counts for it.
	t.Run("Alt+Shift", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0), simtest.WithMenuMask())
		s.down(ownerA, "Alt+Shift+F13")
		s.Advance(15 * ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.ExpectOutput("+LShift@0 +LAlt@10 +vk:0xE8@15 -vk:0xE8@15 -LAlt@15 -LShift@15")
		s.expectHeld("")
	})
	// No mask when nothing was dropped, or when another key went down
	// after the modifiers.
	t.Run("no mask", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		s.down(ownerA, "Ctrl+Shift+F13")
		s.Advance(25 * ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.ExpectOutput("+LCtrl@0 +LShift@10 +F13@20 -F13@25 -LShift@25 -LCtrl@25")
	})
	// The modifier release left from the queue counts too: the mask goes
	// before it.
	t.Run("queued modifier release", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		s.tap(ownerA, "Ctrl+Shift+Z", 0)
		s.Advance(3 * ms)
		if err := s.Arb.ReleaseOwner(ownerA); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+LCtrl] [+vk:0xE8 -vk:0xE8 -LCtrl]")
		s.expectHeld("")
		s.expectDeadline(-1)
	})
	// A key another owner presses after the modifiers' releases comes too
	// late: the mask still goes before the first of them (a tap whose
	// releases were queued, and releases queued early for a press again).
	t.Run("tap then other", func(t *testing.T) {
		for _, gap := range []time.Duration{10 * ms, 50 * ms} {
			s := newSim(t, paced(gap, 0, 0), simtest.WithMenuMask())
			s.tap(ownerA, "Ctrl+Shift+F13", 0)
			s.tap(ownerB, "W", 0)
			s.Advance(gap + gap/2) // +LCtrl +LShift
			if err := s.Arb.ReleaseOwner(ownerA); err != nil {
				t.Fatal(err)
			}
			s.expectBatches(0, "[+LCtrl] [+LShift] [+vk:0xE8 -vk:0xE8 -LShift -LCtrl +W -W]")
			s.expectHeld("")
		}
	})
	t.Run("pressed again", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 20*ms, 20*ms), simtest.WithMenuMask())
		s.down(ownerA, "Ctrl+Shift+F13")
		s.AdvanceTo(clock.Time(5 * ms))
		s.up(ownerA, "Ctrl+Shift+F13")
		s.AdvanceTo(clock.Time(25 * ms))
		s.down(ownerB, "F13")
		s.AdvanceTo(clock.Time(60 * ms)) // +LShift went out at 50
		if err := s.Arb.ReleaseOwner(ownerA); err != nil {
			t.Fatal(err)
		}
		s.AdvanceTo(clock.Time(100 * ms))
		s.up(ownerB, "F13")
		s.Settle()
		s.expectBatches(0, "[+LCtrl] [+LShift] [+vk:0xE8 -vk:0xE8 -LShift -LCtrl +F13] [-F13]")
	})
	// A mask release that fails is resent like any other.
	t.Run("mask release fails", func(t *testing.T) {
		s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 10 * ms, RetryInterval: 7 * ms}))
		s.down(ownerA, "Ctrl+F13")
		s.Advance(3 * ms)
		s.Out.FailNext(1, errBlocked) // +vk:0xE8 only
		s.Out.FailNext(0, errBlocked) // the immediate resend
		if err := s.Arb.ReleaseAll(); err == nil {
			t.Fatal("ReleaseAll succeeded with a failing Send")
		}
		s.expectHeld("LCtrl(pending) vk:0xE8(pending)")
		s.Settle()
		_ = s.Errors()
		s.expectBatches(0, "[+LCtrl] [+vk:0xE8 -vk:0xE8! -LCtrl!] [-vk:0xE8! -LCtrl!] [-vk:0xE8 -LCtrl]")
		s.expectHeld("")
	})
	t.Run("queued releases", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		var got []output.TapCompletion
		s.ok("Tap", ownerA, "Ctrl+Z", s.Arb.TapWithCompletion(ownerA, keys.MustParse("Ctrl+Z"), 0, func(r output.TapCompletion) {
			got = append(got, r)
		}))
		s.Advance(15 * ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+LCtrl] [+Z] [-Z -LCtrl]")
		s.ExpectOutput("+LCtrl@0 +Z@10 -Z@15 -LCtrl@15")
		if len(got) != 1 || !got[0].Canceled {
			t.Errorf("completion = %+v, want one canceled", got)
		}
		s.expectDeadline(-1)
	})
}

// TestEventGapReleaseOwner: releasing one owner drops its presses never
// sent and sends at once, ahead of the releases, the presses still queued
// for the others, so their chords keep their order.
func TestEventGapReleaseOwner(t *testing.T) {
	s := newSim(t, paced(10*ms, 0, 0))
	s.down(ownerA, "Ctrl+Z")
	var got []output.TapCompletion
	s.ok("Tap", ownerB, "F13", s.Arb.TapWithCompletion(ownerB, keys.MustParse("F13"), 0, func(r output.TapCompletion) {
		got = append(got, r)
	}))
	s.Advance(3 * ms)
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.expectBatches(0, "[+LCtrl] [+F13 -F13 -LCtrl]")
	s.ExpectOutput("+LCtrl@0 +F13@3 -F13@3 -LCtrl@3")
	if len(got) != 1 || got[0].Dropped {
		t.Errorf("B's completion = %+v, want one normal", got)
	}
	s.expectHeld("")
	s.expectDeadline(-1)
}

// TestEventGapReleaseOrder: a forced release sends the queued events in
// their order, releases included, so no press goes out while the
// modifier of an earlier chord is still down.
func TestEventGapReleaseOrder(t *testing.T) {
	t.Run("other owner", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		s.down(ownerS, "F14")
		s.tap(ownerA, "Ctrl+S", 0)
		s.Advance(11 * ms)
		s.tap(ownerA, "W", 0)
		if err := s.Arb.ReleaseOwner(ownerS); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+F14] [+LCtrl] [+S -S -LCtrl +W -W -F14]")
		s.expectHeld("")
		s.expectDeadline(-1)
	})
	// The dropped S leaves Ctrl alone: the mask goes before its release,
	// though W goes down after it in the same batch.
	t.Run("own queued release", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		s.tap(ownerA, "Ctrl+S", 0)
		s.tap(ownerB, "W", 0)
		if err := s.Arb.ReleaseOwner(ownerA); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+LCtrl] [+vk:0xE8 -vk:0xE8 -LCtrl +W -W]")
		s.expectHeld("")
		s.expectDeadline(-1)
	})
}

// TestEventGapReleaseShared: a press never sent still goes out when
// another owner's command used it, so that command keeps its chord; only
// a release of every owner that used it drops it.
func TestEventGapReleaseShared(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     func(s tsim) error
		batches string
	}{
		{"release", func(s tsim) error { return s.Arb.ReleaseOwner(ownerS) },
			"[+F14] [-F14 +LCtrl +X -X -LCtrl]"},
		{"up then release", func(s tsim) error {
			s.up(ownerS, "Ctrl")
			return s.Arb.ReleaseOwner(ownerS)
		}, "[+F14] [-F14 +LCtrl +X -X -LCtrl]"},
		{"release all", func(s tsim) error { return s.Arb.ReleaseAll() }, "[+F14] [-F14]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(10*ms, 0, 0))
			s.tap(ownerB, "F14", 0)
			s.down(ownerS, "Ctrl")
			s.tap(ownerA, "Ctrl+X", 0)
			if err := tc.run(s); err != nil {
				t.Fatal(err)
			}
			s.expectBatches(0, tc.batches)
			s.expectHeld("")
			s.expectDeadline(-1)
		})
	}
	// A tap on keys another owner holds relies on them too.
	t.Run("tap on held", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		s.tap(ownerB, "F14", 0)
		s.down(ownerS, "Ctrl+Z")
		s.tap(ownerA, "Ctrl+Z", 0)
		if err := s.Arb.ReleaseOwner(ownerS); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+F14] [-F14 +LCtrl +Z +Z -Z -LCtrl]")
		s.expectHeld("")
	})
	// The extra press of a released owner is dropped with it.
	t.Run("released repeat", func(t *testing.T) {
		s := newSim(t, paced(10*ms, 0, 0))
		s.down(ownerA, "F13")
		s.repeat(ownerA, "F13")
		if err := s.Arb.ReleaseOwner(ownerA); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+F13] [-F13]")
	})
}

// TestEventGapTapOnHeldCompletion: a tap whose keys are all held by Down
// completes once its extra press went out, and is canceled when its owner
// is released before. The release drops the extra press, which counts as
// a dropped press: the mask key goes before LCtrl's release (one too many
// here, as Z went down after LCtrl; that is harmless).
func TestEventGapTapOnHeldCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release bool
		want    output.TapCompletion
		out     string
	}{
		{"sent", false, output.TapCompletion{}, "+LCtrl@0 +Z@10 +Z@20"},
		{"released", true, output.TapCompletion{Dropped: true, Canceled: true}, "+LCtrl@0 +Z@10 -Z@15 +vk:0xE8@15 -vk:0xE8@15 -LCtrl@15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(10*ms, 0, 0))
			s.down(ownerA, "Ctrl+Z")
			var got []output.TapCompletion
			s.ok("Tap", ownerA, "Ctrl+Z", s.Arb.TapWithCompletion(ownerA, keys.MustParse("Ctrl+Z"), 0, func(r output.TapCompletion) {
				got = append(got, r)
			}))
			if len(got) != 0 {
				t.Fatalf("completed before its press went out: %+v", got)
			}
			s.Advance(15 * ms)
			if tc.release {
				if err := s.Arb.ReleaseOwner(ownerA); err != nil {
					t.Fatal(err)
				}
			}
			s.Advance(10 * ms)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("completion = %+v, want one %+v", got, tc.want)
			}
			s.ExpectOutput(tc.out)
			if !tc.release {
				s.up(ownerA, "Ctrl+Z")
				s.Settle()
			}
		})
	}
}

// TestEventGapMenuMask: the mask key goes out in the same Send as the
// lone Alt release it protects.
func TestEventGapMenuMask(t *testing.T) {
	s := newSim(t, paced(10*ms, 0, 0), simtest.WithMenuMask())
	s.down(ownerA, "Alt")
	s.up(ownerA, "Alt")
	s.Settle()
	s.expectBatches(0, "[+LAlt] [+vk:0xE8 -vk:0xE8 -LAlt]")
	s.ExpectOutput("+LAlt@0 +vk:0xE8@10 -vk:0xE8@10 -LAlt@10")
}

// TestEventGapChange: a new gap applies from the last Send; 0 sends what
// is queued at once, in one batch.
func TestEventGapChange(t *testing.T) {
	s := newSim(t, paced(10*ms, 0, 0))
	s.tap(ownerA, "Ctrl+Shift+F13", 0)
	s.Advance(5 * ms)
	if err := s.Arb.SetEventGap(20 * ms); err != nil {
		t.Fatal(err)
	}
	s.expectDeadline(20 * ms)
	s.Advance(20 * ms)
	if err := s.Arb.SetEventGap(0); err != nil {
		t.Fatal(err)
	}
	s.expectBatches(0, "[+LCtrl] [+LShift] [+F13 -F13 -LShift -LCtrl]")
	s.ExpectOutput("+LCtrl@0 +LShift@20 +F13@25 -F13@25 -LShift@25 -LCtrl@25")
	s.expectDeadline(-1)
	s.tap(ownerA, "Ctrl+Z", 0)
	s.expectBatches(3, "[+LCtrl +Z -Z -LCtrl]")
	if err := s.Arb.SetEventGap(-time.Second); err != nil || s.Arb.EventGap() != 0 {
		t.Errorf("SetEventGap(-1s) = %v, EventGap() = %v; want nil, 0", err, s.Arb.EventGap())
	}
}

// TestEventGapTapCompletion: a tap completes once its last release went
// out; a queued press that fails fails the tap, and its key still goes up.
func TestEventGapTapCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fail    bool
		dropped bool
		out     string
	}{
		{"sent", false, false, "+LCtrl@0 +Z@10 -Z@20 -LCtrl@30"},
		{"press fails", true, true, "+LCtrl@0 -Z@20 -LCtrl@30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(10*ms, 0, 0))
			var got []output.TapCompletion
			if err := s.Arb.TapWithCompletion(ownerA, keys.MustParse("Ctrl+Z"), 0, func(r output.TapCompletion) {
				got = append(got, r)
			}); err != nil {
				t.Fatal(err)
			}
			if tc.fail {
				s.Out.FailNext(0, nil) // +Z, after +LCtrl went out
			}
			s.Advance(29 * ms)
			_ = s.Errors()
			if len(got) != 0 {
				t.Fatalf("completed before the last release: %+v", got)
			}
			s.Advance(ms)
			if len(got) != 1 || got[0].Dropped != tc.dropped || got[0].Canceled {
				t.Errorf("completion = %+v, want Dropped %v", got, tc.dropped)
			}
			s.ExpectOutput(tc.out)
		})
	}
}

// TestEventGapRepeatBacklog: Repeat faster than the gap merges into the
// press still queued, so the queue stays bounded, and Up drops it: no
// press goes out after Up, and the release follows within a few gaps, as
// long as the button was held (the engine's 25 Hz repeat, gap 50 ms).
func TestEventGapRepeatBacklog(t *testing.T) {
	for _, tc := range []struct {
		name   string
		gap    time.Duration
		period time.Duration
		chords []string
	}{
		{"one key at 25 Hz", 50 * ms, 40 * ms, []string{"Ctrl+Z"}},
		{"two keys at 12 Hz", 50 * ms, time.Second / 12, []string{"F13", "F14"}},
		{"gap 0", 0, 40 * ms, []string{"Ctrl+Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(tc.gap, 20*ms, 20*ms))
			owners := []output.Owner{ownerA, ownerB}
			for i, c := range tc.chords {
				s.down(owners[i], c)
			}
			s.Advance(350 * ms)
			for range int(10 * time.Second / tc.period) {
				for i, c := range tc.chords {
					s.repeat(owners[i], c)
				}
				if n := len(output.PacedEvents(s.Arb)); n > 2*len(tc.chords) {
					t.Fatalf("%d events queued: %v", n, output.PacedEvents(s.Arb))
				}
				s.Advance(tc.period)
			}
			upAt := s.Clock.Now()
			for i, c := range tc.chords {
				s.up(owners[i], c)
			}
			s.Settle()
			for _, rec := range s.Out.Records() {
				if rec.T <= upAt {
					continue
				}
				if !rec.Event.Up {
					t.Errorf("%s pressed at %v, after Up at %v", keyName(rec.Event), rec.T, upAt)
				}
				if d := rec.T.Sub(upAt); d > 3*tc.gap {
					t.Errorf("%s released %v after Up", keyName(rec.Event), d)
				}
			}
		})
	}
}

// TestEventGapRepeatTapOnHeld: RepeatTap on a key another owner holds with
// Down presses it once more; one such press waits for the gap at a time,
// the next tap queues behind it, and StopRepeat cancels that one.
func TestEventGapRepeatTapOnHeld(t *testing.T) {
	s := newSim(t, paced(50*ms, 20*ms, 20*ms))
	s.down(ownerA, "Z")
	for range 50 {
		s.ok("RepeatTap", ownerB, "Z", s.Arb.RepeatTap(ownerB, keys.MustParse("Z"), 0))
		if n := len(output.PacedEvents(s.Arb)); n > 1 {
			t.Fatalf("%d events queued: %v", n, output.PacedEvents(s.Arb))
		}
		s.Advance(20 * ms)
	}
	s.ok("StopRepeat", ownerB, "Z", s.Arb.StopRepeat(ownerB, keys.MustParse("Z")))
	if n := s.Arb.Queued(); n != 0 {
		t.Errorf("Queued() = %d after StopRepeat, want 0", n)
	}
	s.up(ownerA, "Z")
	s.Settle()
	if st := s.Arb.Stats(); st.MergedTaps == 0 || st.QueuedTaps == 0 {
		t.Errorf("Stats() = %+v, want queued and merged taps", st)
	}
}

// TestEventGapDoubleDown: a hold binding pressed twice quickly presses its
// main key twice, as without a gap, wherever the first press is: its
// release queued, waiting for MinPress from a press that went out late, or
// waiting for its press still queued. The modifiers the second Down holds
// stay down; the ones it does not go up before its key goes down. Within
// the minimum from the first Down, the second takes the key over, as
// without a gap; a third Down while a press of the key already waits
// behind another one does too.
func TestEventGapDoubleDown(t *testing.T) {
	for _, tc := range []struct {
		name          string
		gap, minPress time.Duration
		first, second string
		at            []time.Duration // Down, Up, Down, Up
		want          string
	}{{
		name: "release queued", gap: 50 * ms, minPress: 20 * ms,
		first: "Ctrl+Z", second: "Ctrl+Z", at: []time.Duration{0, 30 * ms, 90 * ms, 120 * ms},
		want: "+LCtrl@0 +Z@50 -Z@100 +Z@150 -Z@200 -LCtrl@250",
	}, {
		name: "release waits for MinPress", gap: 10 * ms, minPress: 20 * ms,
		first: "Ctrl+Z", second: "Ctrl+Z", at: []time.Duration{0, 12 * ms, 25 * ms, 55 * ms},
		want: "+LCtrl@0 +Z@10 -Z@30 +Z@40 -Z@60 -LCtrl@70",
	}, {
		name: "press still queued", gap: 50 * ms, minPress: 20 * ms,
		first: "Ctrl+Shift+Z", second: "Ctrl+Shift+Z", at: []time.Duration{0, 30 * ms, 60 * ms, 90 * ms},
		want: "+LCtrl@0 +LShift@50 +Z@100 -Z@150 +Z@200 -Z@250 -LShift@300 -LCtrl@350",
	}, {
		name: "other modifier", gap: 10 * ms, minPress: 20 * ms,
		first: "Ctrl+Z", second: "Shift+Z", at: []time.Duration{0, 12 * ms, 25 * ms, 55 * ms},
		want: "+LCtrl@0 +Z@10 -Z@30 -LCtrl@40 +LShift@50 +Z@60 -Z@80 -LShift@90",
	}, {
		name: "within MinPress", gap: 50 * ms, minPress: 20 * ms,
		first: "Ctrl+Z", second: "Ctrl+Z", at: []time.Duration{0, 5 * ms, 10 * ms, 40 * ms},
		want: "+LCtrl@0 +Z@50 -Z@100 -LCtrl@150",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(tc.gap, tc.minPress, 20*ms))
			s.down(ownerA, tc.first)
			s.AdvanceTo(clock.Time(tc.at[1]))
			s.up(ownerA, tc.first)
			s.AdvanceTo(clock.Time(tc.at[2]))
			s.down(ownerA, tc.second)
			s.AdvanceTo(clock.Time(tc.at[3]))
			s.up(ownerA, tc.second)
			s.Settle()
			s.ExpectOutput(tc.want)
			expectPaced(t, s.Out, tc.gap)
		})
	}
	t.Run("third", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 0, 0))
		s.tap(ownerS, "F14", 0)
		s.tap(ownerS, "F15", 0)
		for range 3 {
			s.down(ownerA, "Z")
			s.up(ownerA, "Z")
		}
		s.Settle()
		s.ExpectOutput("+F14@0 -F14@50 +F15@100 -F15@150 +Z@200 -Z@250 +Z@300 -Z@350")
	})
}

// TestEventGapModifierAgain: a modifier whose release is queued, with
// another key's press behind it, goes up and down again for a new chord,
// as without a gap: taking it over would hold it down for that key.
func TestEventGapModifierAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		gap  time.Duration
		want string
	}{
		{"gap", 10 * ms, "+LCtrl@0 +X@10 -X@20 -LCtrl@30 +W@40 -W@50 +LCtrl@60 +Y@70 -Y@100 -LCtrl@110"},
		{"no gap", 0, "+LCtrl@0 +X@0 -X@0 -LCtrl@0 +W@0 -W@0 +LCtrl@0 +Y@0 -Y@100 -LCtrl@100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(tc.gap, 0, 0))
			s.tap(ownerA, "Ctrl+X", 0)
			s.tap(ownerB, "W", 0)
			s.down(ownerS, "Ctrl+Y")
			s.Advance(100 * ms)
			s.up(ownerS, "Ctrl+Y")
			s.Settle()
			s.ExpectOutput(tc.want)
		})
	}
}

// TestEventGapReleaseAgain: a forced release drops a key's press never
// sent, and the press it was pressed again behind when only the released
// owners used that one too; another owner's press and its release go out.
func TestEventGapReleaseAgain(t *testing.T) {
	t.Run("both dropped", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 20*ms, 0))
		s.down(ownerA, "Ctrl+Shift+Z")
		s.Advance(30 * ms)
		s.up(ownerA, "Ctrl+Shift+Z")
		s.Advance(30 * ms)
		s.down(ownerA, "Ctrl+Shift+Z")
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.ExpectOutput("+LCtrl@0 +LShift@50 +vk:0xE8@60 -vk:0xE8@60 -LShift@60 -LCtrl@60")
		s.expectHeld("")
		s.expectDeadline(-1)
	})
	t.Run("other owner's kept", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 0, 0))
		s.tap(ownerS, "F14", 0)
		s.tap(ownerB, "Z", 0)
		s.down(ownerA, "Z")
		if err := s.Arb.ReleaseOwner(ownerA); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+F14] [-F14 +Z -Z]")
		s.expectHeld("")
		s.expectDeadline(-1)
	})
	// The release of the first press fails: the key stays in the ledger and
	// its release is resent (CLAUDE.md invariant 1).
	t.Run("kept release fails", func(t *testing.T) {
		s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 50 * ms, RetryInterval: 7 * ms}))
		s.tap(ownerS, "F14", 0)
		s.tap(ownerB, "Z", 0)
		s.down(ownerA, "Z")
		s.Out.FailNext(2, errBlocked) // -F14 +Z
		s.Out.FailNext(0, errBlocked) // the resend of -Z
		if err := s.Arb.ReleaseOwner(ownerA); err == nil {
			t.Fatal("ReleaseOwner succeeded with a failing Send")
		}
		s.expectHeld("Z(pending)")
		s.Settle()
		_ = s.Errors()
		s.expectBatches(1, "[-F14 +Z -Z!] [-Z!] [-Z]")
		s.expectHeld("")
	})
}

// TestEventGapAgainBehindPress: a key pressed again whose new press is
// still queued, with its release waiting for it, is taken over by the next
// chord's modifier: no press of the key goes out without the release of
// the one before it (FuzzArbiterTiming f33f039710b05ec5).
func TestEventGapAgainBehindPress(t *testing.T) {
	s := newSim(t, paced(10*ms, 20*ms, 0))
	s.down(ownerA, "Ctrl+X")
	s.up(ownerA, "Ctrl+X")
	s.Advance(25 * ms)
	s.down(ownerB, "LCtrl") // presses LCtrl again behind its release
	s.up(ownerB, "LCtrl")
	s.tap(ownerS, "F14", 0)
	s.down(ownerA, "Ctrl+Y")
	s.Advance(100 * ms)
	s.up(ownerA, "Ctrl+Y")
	s.Settle()
	s.ExpectOutput("+LCtrl@0 +X@10 -X@30 -LCtrl@40 +LCtrl@50 +F14@60 +Y@70 -F14@80 -Y@125 -LCtrl@135")
	expectPaced(t, s.Out, 10*ms)
}

// TestEventGapAgainReleaseFails: the release of the press a key was pressed
// again behind fails twice; a forced release that drops the new press
// still releases the key (CLAUDE.md invariant 1).
func TestEventGapAgainReleaseFails(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 10 * ms, RetryInterval: 7 * ms}))
	s.tap(ownerS, "F14", 0)
	s.tap(ownerB, "Z", 0)
	s.down(ownerA, "Z") // pressed again behind B's tap, still queued
	s.Advance(25 * ms)  // -F14 +Z
	s.Out.FailNext(0, errBlocked)
	s.Out.FailNext(0, errBlocked)
	s.Advance(5 * ms) // -Z fails twice
	_ = s.Errors()
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	s.expectBatches(0, "[+F14] [-F14] [+Z] [-Z!] [-Z!] [-Z]")
	s.expectHeld("")
	simtest.AssertNoneHeld(t, s.Arb, s.Out)
}

// TestEventGapAgainSetEventGapZero: SetEventGap(0) keeps the minimum of a
// release queued before it passed, and the press behind it waits.
func TestEventGapAgainSetEventGapZero(t *testing.T) {
	s := newSim(t, paced(10*ms, 20*ms, 0))
	s.down(ownerA, "Ctrl+Z")
	s.Advance(12 * ms)
	s.up(ownerA, "Ctrl+Z")
	s.Advance(13 * ms)
	s.down(ownerA, "Ctrl+Z")
	if err := s.Arb.SetEventGap(0); err != nil {
		t.Fatal(err)
	}
	s.expectDeadline(30 * ms)
	s.Advance(30 * ms)
	s.up(ownerA, "Ctrl+Z")
	s.Settle()
	s.expectBatches(0, "[+LCtrl] [+Z] [-Z +Z] [-Z -LCtrl]")
	s.ExpectOutput("+LCtrl@0 +Z@10 -Z@30 +Z@30 -Z@55 -LCtrl@55")
}

// TestEventGapTapCompletesOwnEvents: a tap completes once its own events
// went out, though another owner pressed its key again since.
func TestEventGapTapCompletesOwnEvents(t *testing.T) {
	s := newSim(t, paced(10*ms, 0, 0))
	var got []output.TapCompletion
	s.ok("Tap", ownerA, "Ctrl+Z", s.Arb.TapWithCompletion(ownerA, keys.MustParse("Ctrl+Z"), 0, func(r output.TapCompletion) {
		got = append(got, r)
	}))
	s.Advance(25 * ms) // +LCtrl +Z -Z
	s.down(ownerB, "Z")
	s.up(ownerB, "Z")
	s.Advance(5 * ms) // -LCtrl
	if len(got) != 1 || got[0] != (output.TapCompletion{}) {
		t.Errorf("completion after -LCtrl = %+v, want one normal", got)
	}
	s.Settle()
	s.ExpectOutput("+LCtrl@0 +Z@10 -Z@20 -LCtrl@30 +Z@40 -Z@50")
}

// TestEventGapForcedReleaseCompletes: a tap waits for no key of another
// owner. When a forced release drops that owner's press never sent, the
// tap completes all the same and a sequence goes on.
func TestEventGapForcedReleaseCompletes(t *testing.T) {
	s := newSim(t, paced(10*ms, 0, 0))
	var got []output.TapCompletion
	s.ok("Tap", ownerA, "Ctrl+Z", s.Arb.TapWithCompletion(ownerA, keys.MustParse("Ctrl+Z"), 0, func(r output.TapCompletion) {
		got = append(got, r)
	}))
	s.ok("TapSequence", ownerS, "X", s.Arb.TapSequence(ownerS, []keys.Chord{keys.MustParse("Ctrl+Z"), keys.MustParse("X")}, 0, 0))
	s.Advance(45 * ms) // A's tap, and S's Ctrl+Z up to -Z
	s.down(ownerB, "Z")
	s.up(ownerB, "Z")
	s.Advance(5 * ms) // -LCtrl: the queued Z is B's
	if err := s.Arb.ReleaseOwner(ownerB); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	if len(got) != 1 || got[0] != (output.TapCompletion{}) {
		t.Errorf("completion = %+v, want one normal", got)
	}
	if n := s.Arb.Queued(); n != 0 {
		t.Errorf("Queued() = %d, want 0: the sequence stalled", n)
	}
	if out := s.Output(); !strings.Contains(out, "+X@") {
		t.Errorf("output %s, want the sequence's X", out)
	}
}

// downWith returns, for each press of key that went out, the other keys
// physically down at the time, joined with "+" in press order.
func downWith(s tsim, key string) []string {
	var down []string
	var with []string
	for _, rec := range s.Out.Records() {
		if !rec.Injected {
			continue
		}
		k := keyName(rec.Event)
		if rec.Event.Up {
			if i := slices.Index(down, k); i >= 0 {
				down = slices.Delete(down, i, i+1)
			}
			continue
		}
		if k == key {
			with = append(with, strings.Join(down, "+"))
		}
		if !slices.Contains(down, k) {
			down = append(down, k)
		}
	}
	return with
}

// TestEventGapReleaseBeforeNextChord: a release that waits for its minimum
// from a press the gap delayed goes into the queue when it would go out
// without a gap, ahead of the next chord the user asked for after that:
// the chord's key does not go down with the earlier chord's keys (Ctrl+Z
// is not Ctrl+Shift+Z, Ctrl+Z after Alt+X is not Ctrl+Alt+Z).
func TestEventGapReleaseBeforeNextChord(t *testing.T) {
	for _, tc := range []struct {
		name string
		gap  time.Duration
		run  func(s tsim)
		want string
	}{{
		name: "hold", gap: 10 * ms,
		run: func(s tsim) {
			s.down(ownerA, "Ctrl+Shift+S")
			s.AdvanceTo(clock.Time(5 * ms))
			s.up(ownerA, "Ctrl+Shift+S")
			s.AdvanceTo(clock.Time(25 * ms))
			s.down(ownerA, "Ctrl+Z")
			s.AdvanceTo(clock.Time(60 * ms))
			s.up(ownerA, "Ctrl+Z")
		},
		want: "+LCtrl@0 +LShift@10 +S@20 -S@40 -LShift@50 +Z@60 -Z@80 -LCtrl@90",
	}, {
		name: "hold gap 50", gap: 50 * ms,
		run: func(s tsim) {
			s.down(ownerA, "Ctrl+Shift+S")
			s.AdvanceTo(clock.Time(25 * ms))
			s.up(ownerA, "Ctrl+Shift+S")
			s.AdvanceTo(clock.Time(60 * ms))
			s.down(ownerA, "Ctrl+Z")
			s.AdvanceTo(clock.Time(100 * ms))
			s.up(ownerA, "Ctrl+Z")
		},
		want: "+LCtrl@0 +LShift@50 +S@100 -S@150 -LShift@200 +Z@250 -Z@300 -LCtrl@350",
	}, {
		name: "taps", gap: 50 * ms,
		run: func(s tsim) {
			s.tap(ownerA, "Alt+X", 0)
			s.AdvanceTo(clock.Time(30 * ms))
			s.tap(ownerB, "Ctrl+Z", 0)
		},
		want: "+LAlt@0 +X@50 -X@100 -LAlt@150 +LCtrl@200 +Z@250 -Z@300 -LCtrl@350",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, paced(tc.gap, 20*ms, 20*ms))
			tc.run(s)
			s.Settle()
			s.ExpectOutput(tc.want)
			if got := downWith(s, "Z"); !slices.Equal(got, []string{"LCtrl"}) {
				t.Errorf("down with Z: %q, want LCtrl only", got)
			}
			expectPaced(t, s.Out, tc.gap)
		})
	}
}

// TestEventGapModifierAfterLaterKey: a modifier goes up after a key pressed
// after it whose release comes due no later than its own without a gap,
// also when that key's tap ends after the modifier was let go of; the
// releases go out in the order they would without a gap.
func TestEventGapModifierAfterLaterKey(t *testing.T) {
	for _, tc := range []struct {
		gap  time.Duration
		want string
	}{
		{0, "+LCtrl@0 +S@0 -S@20 -LCtrl@20"},
		{10 * ms, "+LCtrl@0 +S@10 -S@30 -LCtrl@40"},
		{50 * ms, "+LCtrl@0 +S@50 -S@100 -LCtrl@150"},
	} {
		t.Run(tc.gap.String(), func(t *testing.T) {
			s := newSim(t, paced(tc.gap, 20*ms, 20*ms))
			s.down(ownerA, "Ctrl")
			s.tap(ownerB, "Ctrl+S", 10*ms) // a script's tap, or tapPressMs 10
			s.up(ownerA, "Ctrl")
			s.Settle()
			s.ExpectOutput(tc.want)
		})
	}
	ups := func(s tsim) string {
		var order []string
		for _, rec := range s.Out.Records() {
			if rec.Event.Up {
				order = append(order, keyName(rec.Event))
			}
		}
		return strings.Join(order, " ")
	}
	for _, minPress := range []time.Duration{30 * ms, 80 * ms} {
		run := func(t *testing.T, gap time.Duration) string {
			s := newSim(t, paced(gap, minPress, 20*ms))
			s.down(ownerA, "Ctrl")
			s.tap(ownerB, "Ctrl+S", 40*ms)
			s.down(ownerA, "Shift")
			s.up(ownerA, "Ctrl")
			s.tap(ownerB, "Shift+X", 0)
			s.up(ownerA, "Shift")
			s.Settle()
			return ups(s)
		}
		want := run(t, 0)
		for _, gap := range []time.Duration{ms, 5 * ms, 10 * ms, 25 * ms, 50 * ms} {
			if got := run(t, gap); got != want {
				t.Errorf("MinPress %v, gap %v: releases %s, want %s as without a gap", minPress, gap, got, want)
			}
		}
	}
}

// TestEventGapAlternatingDowns: hold bindings pressed one after another
// faster than the gap lets their events out, with MinPress 0 too
// (timing.tapPressMs 0): a Down whose main key already has a press queued
// behind another one of its presses merges into it, so the queue stays
// bounded and the keys go up within a few gaps of the last Up. Every press
// that goes out has its own chord's modifiers, and no modifier goes down
// and up again with no other key pressed between.
func TestEventGapAlternatingDowns(t *testing.T) {
	for _, tc := range []struct {
		gap, minPress, hold, pause time.Duration
		chords                     []string
	}{
		{50 * ms, 0, 40 * ms, 40 * ms, []string{"Ctrl+Z", "X"}},
		{50 * ms, 20 * ms, 40 * ms, 40 * ms, []string{"Ctrl+Z", "X"}},
		{20 * ms, 0, 20 * ms, 20 * ms, []string{"Ctrl+Z", "X"}},
		{50 * ms, 0, 40 * ms, 40 * ms, []string{"Ctrl+Z", "Shift+Z"}},
		{50 * ms, 20 * ms, 100 * ms, 20 * ms, []string{"Ctrl+Z", "Shift+Z"}},
		{50 * ms, 0, 20 * ms, 5 * ms, []string{"Z", "X", "Y"}},
		{50 * ms, 20 * ms, 60 * ms, 40 * ms, []string{"Ctrl+Shift+Z", "Alt+X"}},
	} {
		name := strings.Join(tc.chords, ",") + " " + tc.gap.String() + " " + tc.minPress.String()
		t.Run(name, func(t *testing.T) {
			s := newSim(t, paced(tc.gap, tc.minPress, 20*ms))
			owners := []output.Owner{ownerA, ownerB, ownerS}
			maxQueued := 0
			for range 100 {
				for i, c := range tc.chords {
					s.down(owners[i], c)
					s.Advance(tc.hold)
					s.up(owners[i], c)
					s.Advance(tc.pause)
					maxQueued = max(maxQueued, len(output.PacedEvents(s.Arb)))
				}
			}
			upAt := s.Clock.Now()
			s.Settle()
			if maxQueued > 24 {
				t.Errorf("%d events queued at most", maxQueued)
			}
			if out := s.Out.Records(); len(out) > 0 {
				if d := out[len(out)-1].T.Sub(upAt); d > 20*tc.gap {
					t.Errorf("last event %v after the last Up", d)
				}
			}
			// The modifiers of each chord, by main key.
			mods := map[string][]string{}
			for _, c := range tc.chords {
				names := strings.Split(strings.NewReplacer("Ctrl", "LCtrl", "Shift", "LShift", "Alt", "LAlt").Replace(c), "+")
				main := names[len(names)-1]
				mods[main] = append(mods[main], strings.Join(names[:len(names)-1], "+"))
			}
			for main, want := range mods {
				for _, got := range downWith(s, main) {
					// A key that is not a modifier may stay down across another
					// one's press: a merged Down holds it.
					got = strings.Join(slices.DeleteFunc(strings.Split(got, "+"), func(k string) bool {
						return !strings.HasPrefix(k, "L")
					}), "+")
					if !slices.Contains(want, got) {
						t.Errorf("%s went down with %q, want one of %q", main, got, want)
					}
				}
			}
			lone := map[string]bool{}
			for _, rec := range s.Out.Records() {
				k := keyName(rec.Event)
				switch isMod := strings.HasPrefix(k, "L"); {
				case !rec.Event.Up && isMod:
					lone[k] = true
				case !rec.Event.Up:
					clear(lone)
				case isMod && lone[k]:
					t.Fatalf("%s went down and up at %v with no key pressed between", k, rec.T)
				}
			}
		})
	}
}

// TestEventGapMergedDown: a Down merged into a press of its main key still
// queued holds the modifiers it can take over, so a triple press of one
// chord keeps them down with the key, and does no Repeat when it left one
// out: the repeat would press the key without it.
func TestEventGapMergedDown(t *testing.T) {
	t.Run("same chord", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 0, 0))
		s.tap(ownerS, "F14", 0)
		for range 3 {
			s.down(ownerA, "Ctrl+Z")
			s.up(ownerA, "Ctrl+Z")
		}
		s.down(ownerA, "Ctrl+Z")
		s.repeat(ownerA, "Ctrl+Z")
		s.Advance(time.Second)
		s.up(ownerA, "Ctrl+Z")
		s.Settle()
		s.ExpectOutput("+F14@0 -F14@50 +LCtrl@100 +Z@150 -Z@200 +Z@250 +Z@300 -Z@1000 -LCtrl@1050")
	})
	t.Run("modifier left out", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 0, 0))
		for range 2 {
			s.down(ownerA, "Ctrl+Z")
			s.up(ownerA, "Ctrl+Z")
			s.down(ownerB, "X")
			s.up(ownerB, "X")
		}
		s.down(ownerA, "Ctrl+Z")
		s.repeat(ownerA, "Ctrl+Z")
		s.expectHeld("LCtrl(pending) Z(A) X(pending)")
		s.Advance(time.Second)
		s.up(ownerA, "Ctrl+Z")
		s.Settle()
		s.ExpectOutput("+LCtrl@0 +Z@50 -Z@100 -LCtrl@150 +X@200 -X@250 +LCtrl@300 +Z@350 -LCtrl@400 +X@450 -X@500 -Z@1000")
	})
}

// TestEventGapPartialDrain: SetEventGap(0) drains a key's old release and
// its new press in one Send that injects only up to the release. The key
// stays in the ledger for its new press and the extra press a tap queued
// behind it, which go out later: it still goes up (CLAUDE.md invariant 1).
func TestEventGapPartialDrain(t *testing.T) {
	s := newSim(t, paced(50*ms, 30*ms, 0))
	s.down(ownerS, "Shift") // +LShift goes out at 0
	s.down(ownerB, "X")
	s.up(ownerS, "Shift")
	s.up(ownerB, "X")
	s.Advance(30 * ms)
	s.down(ownerS, "Shift") // pressed again behind its release
	s.down(ownerB, "X")
	s.tap(ownerA, "Ctrl+Shift", 0) // an extra press of the held LShift
	s.up(ownerS, "Shift")
	s.Out.FailNext(2, errBlocked) // the drain: +X -LShift, not the new +LShift
	_ = s.Arb.SetEventGap(0)
	s.Settle()
	s.up(ownerB, "X")
	s.Settle()
	_ = s.Errors()
	s.expectHeld("")
}

// TestEventGapZeroTakeOver: with the gap back at 0 and only a release
// waiting for its minimum in the queue, a Down that takes that key over
// empties the queue after it queued a press of another key: its presses
// still go through the queue and leave nothing waiting there, so the keys
// go up after its Up.
func TestEventGapZeroTakeOver(t *testing.T) {
	s := newSim(t, paced(50*ms, 20*ms, 0))
	s.tap(ownerB, "F14", 0)
	s.down(ownerA, "Alt") // +LAlt goes out at 50
	s.AdvanceTo(clock.Time(55 * ms))
	s.up(ownerA, "Alt") // -LAlt waits in the queue for 70
	if err := s.Arb.SetEventGap(0); err != nil {
		t.Fatal(err)
	}
	s.down(ownerS, "Ctrl+Alt+F15") // LAlt taken over: the queue is empty
	s.Advance(30 * ms)
	s.up(ownerS, "Ctrl+Alt+F15")
	s.Settle()
	s.ExpectOutput("+F14@0 +LAlt@50 -F14@55 +LCtrl@55 +F15@55 -F15@85 -LAlt@85 -LCtrl@85")
	s.expectHeld("")
}

var (
	ownerC = output.Owner{Slot: 2, Gen: 3, Source: output.SourceScript}
	ownerD = output.Owner{Slot: 3, Gen: 1, Source: output.SourceDecl, Cycle: 4}
)

// TestEventGapForcedKeepsChord: a forced release sends a queued press
// another owner's command relies on with the modifiers it would go down
// with without a gap, also the dropped presses of the released owner's
// chord (keepChords): no Ctrl+X of Ctrl+Alt+X, no bare Z of Alt+Z.
func TestEventGapForcedKeepsChord(t *testing.T) {
	t.Run("Down on the queued main key", func(t *testing.T) {
		for _, gap := range []time.Duration{ms, 10 * ms, 50 * ms} {
			s := newSim(t, paced(gap, 0, 0))
			s.down(ownerA, "Ctrl+Alt+X")
			s.down(ownerB, "X")
			if err := s.Arb.ReleaseOwner(ownerA); err != nil {
				t.Fatal(err)
			}
			s.expectBatches(0, "[+LCtrl] [+LAlt +X -LAlt -LCtrl]")
			if got := downWith(s, "X"); !slices.Equal(got, []string{"LCtrl+LAlt"}) {
				t.Errorf("gap %v: down with X: %q, want LCtrl+LAlt", gap, got)
			}
			s.expectHeld("X(B)")
			s.up(ownerB, "X")
			s.Settle()
		}
	})
	// The other owner's own modifier is queued after the key: the key does
	// not go down bare.
	t.Run("tap on the queued main key", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 0, 0))
		s.down(ownerB, "A")
		s.tap(ownerS, "Alt+Z", 80*ms)
		s.down(ownerC, "RCtrl+Z")
		if err := s.Arb.ReleaseOwner(ownerS); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+A] [+LAlt +Z +RCtrl -LAlt]")
		s.up(ownerC, "RCtrl+Z")
		s.up(ownerB, "A")
		s.Settle()
	})
	// A modifier of an earlier chord held for its minimum, whose release
	// is queued behind the dropped presses, is down too, as without a gap.
	t.Run("earlier chord", func(t *testing.T) {
		s := newSim(t, paced(50*ms, 20*ms, 0))
		s.ok("DownFor", ownerC, "Alt+X", s.Arb.DownFor(ownerC, keys.MustParse("Alt+X"), 10*ms))
		s.down(ownerA, "Ctrl+Shift+Z")
		s.up(ownerC, "Alt+X")
		s.AdvanceTo(clock.Time(30 * ms))
		s.tap(ownerD, "Z", 40*ms)
		if err := s.Arb.ReleaseOwner(ownerA); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+LAlt] [+X +LCtrl +LShift +Z -X -LAlt +Z -Z -LShift -LCtrl]")
		s.Settle()
		s.expectHeld("")
	})
}

// TestEventGapCanceledTapHold: with a gap a tap's hold becomes the minimum
// of its keys when the tap ends; a tap canceled before leaves none, so a
// key another owner's Down also took goes up after that Down's Up, as
// without a gap, not the canceled hold after its Send.
func TestEventGapCanceledTapHold(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		s := newSim(t, paced(5*ms, 0, 0))
		s.tap(ownerC, "Alt+X", 80*ms)
		s.down(ownerA, "Alt+X")
		s.AdvanceTo(clock.Time(10 * ms))
		if err := s.Arb.ReleaseOwner(ownerC); err != nil {
			t.Fatal(err)
		}
		s.up(ownerA, "Alt+X")
		s.AdvanceTo(clock.Time(20 * ms))
		s.tap(ownerB, "Ctrl+Num1", 0)
		s.Settle()
		s.ExpectOutput("+LAlt@0 +X@5 -X@10 -LAlt@15 +LCtrl@20 +Num1@25 -Num1@30 -LCtrl@35")
		if got := downWith(s, "Num1"); !slices.Equal(got, []string{"LCtrl"}) {
			t.Errorf("down with Num1: %q, want LCtrl", got)
		}
	})
	// A tap that ends keeps its hold from the Send of its keys, though the
	// other reference ended first.
	t.Run("ended", func(t *testing.T) {
		s := newSim(t, paced(5*ms, 0, 0))
		s.tap(ownerC, "Alt+X", 80*ms)
		s.down(ownerA, "Alt+X")
		s.AdvanceTo(clock.Time(10 * ms))
		s.up(ownerA, "Alt+X")
		s.Settle()
		s.ExpectOutput("+LAlt@0 +X@5 -X@85 -LAlt@90")
	})
}

// TestEventGapForcedDroppedExtra: a forced release that drops an extra
// press of a released owner, which may be the only key modifiers went down
// with, masks their release: no lone Shift or Ctrl.
func TestEventGapForcedDroppedExtra(t *testing.T) {
	t.Run("tap on a held key", func(t *testing.T) {
		s := newSim(t, paced(20*ms, 0, 0))
		s.down(ownerA, "Z")
		s.tap(ownerC, "Shift+Z", 10*ms)
		s.AdvanceTo(clock.Time(20 * ms))
		if err := s.Arb.ReleaseOwner(ownerC); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+Z] [+LShift] [+vk:0xE8 -vk:0xE8 -LShift]")
		s.expectHeld("Z(A)")
		s.up(ownerA, "Z")
		s.Settle()
	})
	t.Run("release all", func(t *testing.T) {
		s := newSim(t, paced(2*ms, 0, 0), simtest.WithMenuMask())
		s.ok("DownFor", ownerC, "X", s.Arb.DownFor(ownerC, keys.MustParse("X"), 60*ms))
		s.AdvanceTo(clock.Time(2 * ms))
		s.tap(ownerA, "Ctrl+X", 0)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+X] [+LCtrl] [+vk:0xE8 -vk:0xE8 -LCtrl -X]")
		s.expectHeld("")
	})
	// Another owner's tap on the key, after the modifier of a Down that
	// pressed only it.
	t.Run("other owner's tap", func(t *testing.T) {
		s := newSim(t, paced(20*ms, 0, 0))
		s.down(ownerA, "Z")
		s.down(ownerC, "RCtrl+Z")
		s.AdvanceTo(clock.Time(20 * ms))
		s.tap(ownerS, "Z", 10*ms)
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+Z] [+RCtrl] [-Z +vk:0xE8 -vk:0xE8 -RCtrl]")
		s.expectHeld("")
	})
	// A Repeat of a Down that pressed only its modifier.
	t.Run("repeat", func(t *testing.T) {
		s := newSim(t, paced(5*ms, 0, 0))
		s.down(ownerA, "X")
		s.down(ownerC, "Shift+X")
		s.repeat(ownerC, "Shift+X")
		s.AdvanceTo(clock.Time(5 * ms))
		if err := s.Arb.ReleaseOwner(ownerC); err != nil {
			t.Fatal(err)
		}
		s.expectBatches(0, "[+X] [+LShift] [+vk:0xE8 -vk:0xE8 -LShift]")
		s.up(ownerA, "X")
		s.Settle()
	})
}

// TestEventGapUpKeepsLoneRepeat: Up keeps the queued Repeat of a Down that
// pressed only its modifiers, its main key down already: without it the
// modifiers would go down and up alone (Shift switching the input method).
// As without a gap, the repeat goes out before them.
func TestEventGapUpKeepsLoneRepeat(t *testing.T) {
	s := newSim(t, paced(5*ms, 0, 0))
	s.down(ownerA, "X")
	s.down(ownerC, "Shift+X")
	s.repeat(ownerC, "Shift+X")
	s.up(ownerC, "Shift+X")
	s.Settle()
	s.ExpectOutput("+X@0 +LShift@5 +X@10 -LShift@15")
	s.up(ownerA, "X")
	s.Settle()
	s.expectHeld("")
	// The Up of another hold of the key by the same owner drops nothing of
	// this one's: the repeat goes out with Shift.
	t.Run("other hold", func(t *testing.T) {
		s := newSim(t, paced(5*ms, 0, 0))
		s.down(ownerS, "Alt+Z")
		s.down(ownerS, "Shift+Z")
		s.repeat(ownerS, "Shift+Z")
		s.up(ownerS, "Alt+Z")
		s.up(ownerS, "Shift+Z")
		s.Settle()
		s.ExpectOutput("+LAlt@0 +Z@5 +LShift@10 +Z@15 -LAlt@20 -Z@25 -LShift@30")
	})
}

// TestEventGapZeroLinger: after SetEventGap(0) the keys pressed with the
// gap keep the queue's order and their minimums from their late Sends until
// they went up, and later commands queue behind them: the tap's chord goes
// up key first, and Z does not go down with its Shift. Then a command is
// one Send again.
func TestEventGapZeroLinger(t *testing.T) {
	for _, minPress := range []time.Duration{0, 20 * ms} {
		t.Run("release order "+minPress.String(), func(t *testing.T) {
			s := newSim(t, paced(10*ms, minPress, 0))
			s.tap(ownerA, "Ctrl+X", 20*ms)
			s.AdvanceTo(clock.Time(5 * ms))
			if err := s.Arb.SetEventGap(0); err != nil {
				t.Fatal(err)
			}
			s.Settle()
			s.ExpectOutput("+LCtrl@0 +X@5 -X@25 -LCtrl@25")
		})
	}
	t.Run("later command", func(t *testing.T) {
		s := newSim(t, paced(20*ms, 0, 0))
		s.tap(ownerA, "Ctrl+Shift+X", 40*ms)
		s.AdvanceTo(clock.Time(30 * ms))
		if err := s.Arb.SetEventGap(0); err != nil {
			t.Fatal(err)
		}
		s.AdvanceTo(clock.Time(45 * ms))
		s.tap(ownerB, "Z", 0)
		s.Settle()
		s.ExpectOutput("+LCtrl@0 +LShift@20 +X@30 -X@70 -LShift@70 -LCtrl@70 +Z@70 -Z@70")
		if got := downWith(s, "Z"); !slices.Equal(got, []string{""}) {
			t.Errorf("down with Z: %q, want nothing", got)
		}
		s.tap(ownerA, "Ctrl+Z", 0)
		s.expectBatches(4, "[+LCtrl +Z -Z -LCtrl]")
	})
}

// TestEventGapZeroDrainOwnRelease: SetEventGap(0) drains a press and its
// own release in one Send; the press starts no minimum of a later press's
// release still queued, so the DownFor's 20 ms hold of the second press
// of Z is kept.
func TestEventGapZeroDrainOwnRelease(t *testing.T) {
	s := newSim(t, paced(50*ms, 0, 0))
	s.tap(ownerA, "F14", 0)
	s.tap(ownerA, "Z", 0)
	s.ok("DownFor", ownerB, "X", s.Arb.DownFor(ownerB, keys.MustParse("X"), 20*ms))
	s.AdvanceTo(clock.Time(25 * ms))
	s.up(ownerB, "X")
	s.ok("DownFor", ownerB, "Z", s.Arb.DownFor(ownerB, keys.MustParse("Z"), 20*ms))
	s.AdvanceTo(clock.Time(45 * ms))
	s.up(ownerB, "Z")
	if err := s.Arb.SetEventGap(0); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	s.ExpectOutput("+F14@0 -F14@45 +Z@45 -Z@45 +X@45 -X@65 +Z@65 -Z@85")
}

// TestLoopEventGap: the Loop sends the queued events on its timer, one
// per Send, and writes the held-key log before each: a key is in the log
// before its press goes out and leaves it once its release went out.
func TestLoopEventGap(t *testing.T) {
	path := heldPath(t)
	var cs *coverSender
	lt := startLoopWith(t, output.LoopOptions{
		HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false },
		Arbiter: output.ArbiterOptions{EventGap: 10 * ms},
	}, nil, func(r *simtest.SyncRecorder) output.Sender {
		cs = &coverSender{t: t, path: path, rec: r}
		return cs
	})
	if res := lt.l.Swept(); res.Err != nil || res.Off {
		t.Fatalf("Swept() = %+v", res)
	}
	lt.do(tap(ownerA, "Ctrl+Z", 0))
	wantHeld(t, path, "LCtrl")
	for _, want := range [][]string{{"LCtrl", "Z"}, {"LCtrl"}, {}} {
		lt.clk.Advance(10 * ms)
		lt.sync()
		wantHeld(t, path, want...)
	}
	if got, want := lt.rec.Trace(), "+LCtrl@0 +Z@10 -Z@20 -LCtrl@30"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
	if n := cs.sent.Load(); n != 4 {
		t.Errorf("%d Send calls, want 4", n)
	}
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
}

// TestLoopEventGapRelease: the pause and Stop release at once, mid chord,
// and the presses still queued never go out; the mask key goes before the
// modifier left alone.
func TestLoopEventGapRelease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release func(lt *loopTest)
	}{
		{"block", func(lt *loopTest) { lt.l.Block(true); lt.sync() }},
		// Stop itself, not lt.stop, which first moves the clock to the
		// Loop's timer.
		{"stop", func(lt *loopTest) {
			if err := lt.l.Stop(); err != nil {
				lt.t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lt := startLoop(t, output.LoopOptions{Arbiter: output.ArbiterOptions{EventGap: 10 * ms}})
			lt.do(down(ownerA, "Ctrl+Shift+F13"))
			lt.clk.Advance(3 * ms)
			tc.release(lt)
			if tc.name == "block" {
				lt.clk.Advance(50 * ms)
				lt.sync()
			}
			if got, want := lt.rec.Trace(), "+LCtrl@0 +vk:0xE8@3 -vk:0xE8@3 -LCtrl@3"; got != want {
				t.Errorf("output:\n got %s\nwant %s", got, want)
			}
		})
	}
}
