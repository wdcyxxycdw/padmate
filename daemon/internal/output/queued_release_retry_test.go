package output_test

import (
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

type queuedTapCompletion struct {
	owner  output.Owner
	at     clock.Time
	result output.TapCompletion
}

type queuedReleaseHarness struct {
	clk  *clock.Fake
	rec  *simtest.Recorder
	arb  *output.Arbiter
	done []queuedTapCompletion
}

func startFailedQueuedRelease(t *testing.T) *queuedReleaseHarness {
	t.Helper()
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: 20 * time.Millisecond, MinGap: 20 * time.Millisecond})
	h := &queuedReleaseHarness{clk: clk, rec: rec, arb: arb}
	t.Cleanup(func() {
		if err := arb.ReleaseAll(); err != nil {
			t.Error(err)
		}
		simtest.AssertNoneHeld(t, arb, rec)
	})
	chord := keys.MustParse("A")
	for _, owner := range []output.Owner{ownerA, ownerB} {
		if err := arb.TapWithCompletion(owner, chord, 0, func(result output.TapCompletion) {
			h.done = append(h.done, queuedTapCompletion{owner: owner, at: clk.Now(), result: result})
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec.FailNext(0, nil)
	rec.FailNext(0, nil)
	clk.Advance(20 * time.Millisecond)
	if err := arb.Advance(); err == nil {
		t.Fatal("missing failed-release error")
	}
	if got := simtest.FormatTrace(rec.Edges()); arb.Queued() != 1 || got != "+A@0" {
		t.Fatalf("tap started while previous physical release failed: queue=%d output=%q", arb.Queued(), got)
	}
	if len(h.done) != 0 {
		t.Fatalf("completion fired before physical release: %+v", h.done)
	}
	h.deadline(t, 120*time.Millisecond)
	return h
}

func (h *queuedReleaseHarness) deadline(t *testing.T, want time.Duration) {
	t.Helper()
	at, ok := h.arb.NextDeadline()
	if !ok || at != clock.Time(want) || at <= h.clk.Now() {
		t.Fatalf("deadline=%v/%t at now=%v, want future %v", at, ok, h.clk.Now(), want)
	}
}

func TestQueuedTapWaitsForTwiceFailedRelease(t *testing.T) {
	h := startFailedQueuedRelease(t)
	h.clk.Advance(100 * time.Millisecond)
	if err := h.arb.Advance(); err != nil {
		t.Fatal(err)
	}
	if got := simtest.FormatTrace(h.rec.Edges()); h.arb.Queued() != 1 || got != "+A@0 -A@120" {
		t.Fatalf("queue ignored gap after successful retry: queue=%d output=%q", h.arb.Queued(), got)
	}
	want := []queuedTapCompletion{{owner: ownerA, at: clock.Time(120 * time.Millisecond), result: output.TapCompletion{Dropped: true}}}
	if !slices.Equal(h.done, want) {
		t.Fatalf("first completion=%+v, want %+v", h.done, want)
	}
	h.deadline(t, 140*time.Millisecond)
	h.clk.Advance(20 * time.Millisecond)
	if err := h.arb.Advance(); err != nil {
		t.Fatal(err)
	}
	if got := simtest.FormatTrace(h.rec.Edges()); h.arb.Queued() != 0 || got != "+A@0 -A@120 +A@140" {
		t.Fatalf("queued tap did not start after the gap: queue=%d output=%q", h.arb.Queued(), got)
	}
	h.deadline(t, 160*time.Millisecond)
	h.clk.Advance(20 * time.Millisecond)
	if err := h.arb.Advance(); err != nil {
		t.Fatal(err)
	}
	want = append(want, queuedTapCompletion{owner: ownerB, at: clock.Time(160 * time.Millisecond)})
	if !slices.Equal(h.done, want) {
		t.Fatalf("terminal completions=%+v, want %+v", h.done, want)
	}
	if got := simtest.FormatTrace(h.rec.Edges()); got != "+A@0 -A@120 +A@140 -A@160" {
		t.Fatalf("physical tap pairs=%q", got)
	}
	if at, ok := h.arb.NextDeadline(); ok {
		t.Fatalf("completed queue left a deadline: %v", at)
	}
}

func TestQueuedTapFailedReleaseOwnerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		match      func(output.Owner) bool
		wantQueued int
	}{
		{"queued owner", func(o output.Owner) bool { return o == ownerB }, 0},
		{"active owner", func(o output.Owner) bool { return o == ownerA }, 1},
		{"all owners", func(output.Owner) bool { return true }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startFailedQueuedRelease(t)
			h.rec.FailNext(0, nil)
			h.rec.FailNext(0, nil)
			if err := h.arb.ReleaseOwners(tc.match); err == nil {
				t.Fatal("missing owner-release error")
			}
			if got := simtest.FormatTrace(h.rec.Edges()); h.arb.Queued() != tc.wantQueued || got != "+A@0" {
				t.Fatalf("owner cancellation started queued tap: queue=%d output=%q", h.arb.Queued(), got)
			}
			for _, done := range h.done {
				if !tc.match(done.owner) || done.result != (output.TapCompletion{Dropped: true, Canceled: true}) {
					t.Fatalf("cancellation notification=%+v", done)
				}
			}
			h.deadline(t, 120*time.Millisecond)
			h.clk.Advance(100 * time.Millisecond)
			if err := h.arb.Advance(); err != nil {
				t.Fatal(err)
			}
			if tc.wantQueued != 0 {
				h.deadline(t, 140*time.Millisecond)
				h.clk.Advance(20 * time.Millisecond)
				if err := h.arb.Advance(); err != nil {
					t.Fatal(err)
				}
				h.deadline(t, 160*time.Millisecond)
				h.clk.Advance(20 * time.Millisecond)
				if err := h.arb.Advance(); err != nil {
					t.Fatal(err)
				}
			}
			if len(h.done) != 2 {
				t.Fatalf("terminal notifications=%+v, want one per tap", h.done)
			}
			for _, owner := range []output.Owner{ownerA, ownerB} {
				index := slices.IndexFunc(h.done, func(done queuedTapCompletion) bool { return done.owner == owner })
				want := output.TapCompletion{Dropped: tc.match(owner) || owner == ownerA, Canceled: tc.match(owner)}
				if index < 0 || h.done[index].result != want {
					t.Fatalf("owner %v completion=%+v", owner, h.done)
				}
			}
			wantTrace := "+A@0 -A@120"
			if tc.wantQueued != 0 {
				wantTrace += " +A@140 -A@160"
			}
			if got := simtest.FormatTrace(h.rec.Edges()); got != wantTrace {
				t.Fatalf("owner cancellation output=%q, want %q", got, wantTrace)
			}
			if at, ok := h.arb.NextDeadline(); ok {
				t.Fatalf("canceled queue left a deadline: %v", at)
			}
		})
	}
}
