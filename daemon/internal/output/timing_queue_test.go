package output_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

func TestTapQueuePerPhysicalKeyAndGap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms, MinGap: 20 * ms}))
	s.tap(ownerA, "F13", 0)
	s.Clock.Advance(5 * ms)
	var merged []output.TapCompletion
	s.tap(ownerB, "Ctrl+F13", 0)
	if err := s.Arb.TapWithCompletion(ownerS, keys.MustParse("Shift+F13"), 0, func(r output.TapCompletion) { merged = append(merged, r) }); err != nil {
		t.Fatal(err)
	}
	if s.Arb.Queued() != 1 || len(merged) != 1 || !merged[0].Dropped || merged[0].Canceled {
		t.Fatalf("queue=%d completion=%+v", s.Arb.Queued(), merged)
	}
	if got := s.Arb.Stats(); got.QueuedTaps != 1 || got.MergedTaps != 1 || got.TapsOnHeld != 0 {
		t.Fatalf("stats=%+v", got)
	}
	s.Clock.Set(clock.Time(20 * ms))
	if err := s.Arb.Advance(); err != nil {
		t.Fatal(err)
	}
	s.expectDeadline(40 * ms)
	s.Clock.Set(clock.Time(40 * ms))
	if err := s.Arb.Advance(); err != nil {
		t.Fatal(err)
	}
	if got := s.Output(); got != "+F13@0 -F13@20 +LCtrl@40 +F13@40" {
		t.Fatalf("output=%s", got)
	}
	s.Settle()
}

func TestDownForRetainsLargestBindingMinimum(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	c := keys.MustParse("F13")
	if err := s.Arb.DownFor(ownerA, c, 50*ms); err != nil {
		t.Fatal(err)
	}
	s.Clock.Advance(5 * ms)
	if err := s.Arb.DownFor(ownerB, c, 10*ms); err != nil {
		t.Fatal(err)
	}
	s.up(ownerA, "F13")
	s.up(ownerB, "F13")
	s.expectDeadline(50 * ms)
	if err := s.Arb.SetTiming(0, 0); err != nil {
		t.Fatal(err)
	}
	s.expectDeadline(50 * ms)
	s.Settle()
	if got := s.Output(); got != "+F13@0 -F13@50" {
		t.Fatalf("output=%s", got)
	}
}

func TestTapSequenceWaitsForCompletionThenGap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	if err := s.Arb.TapSequence(ownerA, []keys.Chord{keys.MustParse("A"), keys.MustParse("B")}, 5*ms, 10*ms); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@20 +B@30 -B@50" {
		t.Fatalf("output=%s", got)
	}
}

func TestTapQueueCancellationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(*output.Arbiter) error
	}{
		{"owner", func(a *output.Arbiter) error { return a.ReleaseOwner(ownerB) }},
		{"slot", func(a *output.Arbiter) error {
			return a.ReleaseOwners(func(o output.Owner) bool { return o.Slot == ownerB.Slot })
		}},
		{"generation", func(a *output.Arbiter) error {
			return a.ReleaseOwners(func(o output.Owner) bool { return o.Gen == ownerB.Gen })
		}},
		{"all", (*output.Arbiter).ReleaseAll},
		{"gate", func(a *output.Arbiter) error { a.SetBlocked(true); return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms, MinGap: 20 * ms}))
			s.tap(ownerA, "A", 0)
			var done []output.TapCompletion
			if err := s.Arb.TapWithCompletion(ownerB, keys.MustParse("A"), 0, func(r output.TapCompletion) { done = append(done, r) }); err != nil {
				t.Fatal(err)
			}
			if err := s.Arb.TapSequence(ownerB, []keys.Chord{keys.MustParse("A"), keys.MustParse("B")}, 0, 10*ms); err != nil {
				t.Fatal(err)
			}
			if err := tc.cancel(s.Arb); err != nil {
				t.Fatal(err)
			}
			if len(done) != 1 || !done[0].Canceled {
				t.Fatalf("completion=%+v", done)
			}
			s.Settle()
			if s.Arb.Queued() != 0 {
				t.Fatalf("remaining queue=%d", s.Arb.Queued())
			}
			for _, e := range s.Out.Records() {
				if e.Edge().Name == "B" {
					t.Fatal("canceled sequence sent B")
				}
			}
		})
	}
}

func TestStopRepeatOnlyDropsQueuedRepeat(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms, MinGap: 20 * ms}))
	c := keys.MustParse("A")
	s.tap(ownerA, "A", 0)
	if err := s.Arb.RepeatTap(ownerB, c, 0); err != nil {
		t.Fatal(err)
	}
	s.Arb.SetBlocked(true)
	if err := s.Arb.StopRepeat(ownerB, c); err != nil {
		t.Fatal(err)
	}
	s.Arb.SetBlocked(false)
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@20" {
		t.Fatalf("output=%s", got)
	}
}

func TestTapQueueStartsAfterFailedReleaseAndFreshLayoutResolution(t *testing.T) {
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	physical := "A"
	a := output.NewArbiter(clk, rec, output.ArbiterOptions{MinGap: 20 * ms, Resolve: func(keys.Chord) (output.Stroke, error) { return output.ChordStroke(keys.MustParse(physical)) }})
	if err := a.Tap(ownerA, keys.MustParse("A"), 5*ms); err != nil {
		t.Fatal(err)
	}
	if err := a.Tap(ownerB, keys.MustParse("A"), 0); err != nil {
		t.Fatal(err)
	}
	rec.FailNext(0, nil)
	clk.Advance(5 * ms)
	if err := a.Advance(); err == nil {
		t.Fatal("missing release failure")
	}
	if a.Queued() != 1 {
		t.Fatal("queued tap bypassed gap after release retry")
	}
	physical = "B"
	clk.Advance(20 * ms)
	if err := a.Advance(); err != nil {
		t.Fatal(err)
	}
	if a.Queued() != 0 {
		t.Fatal("queued tap did not start")
	}
	simtest.AssertNoneHeld(t, a, rec)
}

func TestZeroTimingSequenceCompletesInOneCommand(t *testing.T) {
	s := newSim(t)
	if err := s.Arb.TapSequence(ownerA, []keys.Chord{keys.MustParse("A"), keys.MustParse("A"), keys.MustParse("B")}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.Output(); got != "+A@0 -A@0 +A@0 -A@0 +B@0 -B@0" {
		t.Fatalf("output=%s", got)
	}
	if s.Arb.Queued() != 0 {
		t.Fatal("sequence remained queued")
	}
	s.expectDeadline(-time.Second)
}

func TestTimingUpdateDoesNotShortenCommittedGap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinGap: 20 * ms}))
	s.tap(ownerA, "A", 0)
	s.tap(ownerB, "A", 0)
	if err := s.Arb.SetTiming(-ms, -ms); err != nil {
		t.Fatal(err)
	}
	s.expectDeadline(20 * ms)
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@0 +A@20 -A@20" {
		t.Fatalf("output=%s", got)
	}
}

func TestDownBypassesTapGapAndStartsQueuedHeldTap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinGap: 20 * ms}))
	s.tap(ownerA, "A", 0)
	s.tap(ownerB, "A", 0)
	s.down(ownerS, "A")
	if got := s.Output(); got != "+A@0 -A@0 +A@0 +A@0" {
		t.Fatalf("output=%s", got)
	}
	if s.Arb.Queued() != 0 || s.Arb.Stats().TapsOnHeld != 1 {
		t.Fatal("held key's queue did not start")
	}
	s.up(ownerS, "A")
}

func TestQueueLayoutChangeKeepsEarliestArrivalAndStatistics(t *testing.T) {
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	remap := false
	a := output.NewArbiter(clk, rec, output.ArbiterOptions{Resolve: func(c keys.Chord) (output.Stroke, error) {
		if remap && c == keys.MustParse("A") {
			c = keys.MustParse("B")
		}
		return output.ChordStroke(c)
	}})
	if err := a.Tap(ownerS, keys.MustParse("A"), 10*ms); err != nil {
		t.Fatal(err)
	}
	if err := a.Tap(ownerS, keys.MustParse("B"), 100*ms); err != nil {
		t.Fatal(err)
	}
	if err := a.Tap(ownerA, keys.MustParse("A"), 0); err != nil {
		t.Fatal(err)
	}
	var merged []output.TapCompletion
	if err := a.TapWithCompletion(ownerB, keys.MustParse("B"), 0, func(r output.TapCompletion) { merged = append(merged, r) }); err != nil {
		t.Fatal(err)
	}
	remap = true
	clk.Advance(10 * ms)
	if err := a.Advance(); err != nil {
		t.Fatal(err)
	}
	q := output.QueuedTaps(a)
	if len(q) != 1 || q[0].Owner != ownerA || len(merged) != 1 || !merged[0].Dropped || merged[0].Canceled {
		t.Fatalf("queue=%+v merged=%+v", q, merged)
	}
	if st := a.Stats(); st.QueuedTaps != 2 || st.MergedTaps != 1 {
		t.Fatalf("stats=%+v", st)
	}
	clk.Advance(90 * ms)
	if err := a.Advance(); err != nil {
		t.Fatal(err)
	}
	simtest.AssertNoneHeld(t, a, rec)
}

func TestMinPressStartsWhenFailedDownFinallyInjects(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	s.Out.FailNext(0, errBlocked)
	wantErr(t, s.Arb.Down(ownerA, keys.MustParse("A")), errBlocked)
	s.Clock.Advance(30 * ms)
	s.repeat(ownerA, "A")
	s.up(ownerA, "A")
	s.expectDeadline(50 * ms)
	s.Settle()
	if got := s.Output(); got != "+A@30 -A@50" {
		t.Fatalf("output=%s", got)
	}
}
