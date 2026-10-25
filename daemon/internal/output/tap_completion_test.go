package output_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

func TestTapCompletionWaitsForMinPressAndFailedReleaseRetry(t *testing.T) {
	for _, tc := range []struct {
		name        string
		minPress    time.Duration
		failRelease bool
		wantDropped bool
	}{
		{"min press", 20 * time.Millisecond, false, false},
		{"failed release", 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewFake()
			rec := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: tc.minPress})
			var results []output.TapCompletion
			if tc.failRelease {
				rec.FailNext(1, nil) // initial press, missing release
				rec.FailNext(0, nil) // immediate release retry also fails
			}
			err := arb.TapWithCompletion(ownerA, keys.MustParse("Z"), 0, func(r output.TapCompletion) {
				results = append(results, r)
			})
			if tc.failRelease && err == nil {
				t.Fatal("failed Send was not reported")
			}
			if !tc.failRelease && err != nil {
				t.Fatal(err)
			}
			if len(results) != 0 {
				t.Fatalf("completed with key pending: %+v", results)
			}
			if d, ok := arb.NextDeadline(); !ok || d <= clk.Now() {
				t.Fatalf("next deadline = %v %v", d, ok)
			}
			clk.Advance(max(tc.minPress, output.DefaultRetryInterval))
			if err := arb.Advance(); err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Dropped != tc.wantDropped {
				t.Fatalf("completion = %+v", results)
			}
			simtest.AssertNoneHeld(t, arb, rec)
		})
	}
}

func TestTapCompletionCanceledByOwnerAndGlobalBlock(t *testing.T) {
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{})
	var result []output.TapCompletion
	if err := arb.TapWithCompletion(ownerA, keys.MustParse("Z"), time.Second, func(r output.TapCompletion) { result = append(result, r) }); err != nil {
		t.Fatal(err)
	}
	if err := arb.ReleaseOwners(func(o output.Owner) bool { return o == ownerA }); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || !result[0].Dropped {
		t.Fatalf("owner release = %+v", result)
	}
	arb.SetBlocked(true)
	if err := arb.TapWithCompletion(ownerA, keys.MustParse("X"), 0, func(r output.TapCompletion) { result = append(result, r) }); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || !result[1].Dropped {
		t.Fatalf("blocked tap = %+v", result)
	}
	simtest.AssertNoneHeld(t, arb, rec)
}

func TestTapCompletionCanceledBySlotGenerationAndGlobalRelease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release func(*output.Arbiter) error
	}{
		{"slot", func(a *output.Arbiter) error {
			return a.ReleaseOwners(func(o output.Owner) bool { return o.Slot == ownerA.Slot })
		}},
		{"generation", func(a *output.Arbiter) error {
			return a.ReleaseOwners(func(o output.Owner) bool { return o.Gen == ownerA.Gen })
		}},
		{"global", (*output.Arbiter).ReleaseAll},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewFake()
			rec := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, rec, output.ArbiterOptions{})
			var results []output.TapCompletion
			if err := arb.TapWithCompletion(ownerA, keys.MustParse("Z"), time.Second, func(r output.TapCompletion) { results = append(results, r) }); err != nil {
				t.Fatal(err)
			}
			if err := tc.release(arb); err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || !results[0].Canceled || !results[0].Dropped {
				t.Fatalf("completion = %+v", results)
			}
			simtest.AssertNoneHeld(t, arb, rec)
		})
	}
}

func TestTapCompletionWhenMinPressReleaseIsReusedByAnotherOwner(t *testing.T) {
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: 20 * time.Millisecond})
	var results []output.TapCompletion
	if err := arb.TapWithCompletion(ownerA, keys.MustParse("Z"), 0, func(r output.TapCompletion) { results = append(results, r) }); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("tap completed during MinPress: %+v", results)
	}
	if err := arb.Down(ownerB, keys.MustParse("Z")); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Dropped {
		t.Fatalf("completion after another owner took hold = %+v", results)
	}
	if _, ok := arb.NextDeadline(); ok {
		t.Fatal("reused key left a tap completion deadline")
	}
	if err := arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	simtest.AssertNoneHeld(t, arb, rec)
}

func TestTapCompletionReportsMinPressReleaseFailure(t *testing.T) {
	for _, retryFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate_retry_succeeds", true: "later_retry_succeeds"}[retryFails], func(t *testing.T) {
			clk := clock.NewFake()
			rec := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: 20 * time.Millisecond})
			var results []output.TapCompletion
			if err := arb.TapWithCompletion(ownerA, keys.MustParse("Z"), 0, func(r output.TapCompletion) { results = append(results, r) }); err != nil {
				t.Fatal(err)
			}
			rec.FailNext(0, nil)
			if retryFails {
				rec.FailNext(0, nil)
			}
			clk.Advance(20 * time.Millisecond)
			if err := arb.Advance(); err == nil {
				t.Fatal("MinPress release failure was not reported")
			}
			if retryFails {
				if len(results) != 0 {
					t.Fatalf("completed while release still pending: %+v", results)
				}
				clk.Advance(output.DefaultRetryInterval)
				if err := arb.Advance(); err != nil {
					t.Fatal(err)
				}
			}
			if len(results) != 1 || !results[0].Dropped {
				t.Fatalf("completion after failed release = %+v", results)
			}
			simtest.AssertNoneHeld(t, arb, rec)
		})
	}
}
