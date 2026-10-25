package output_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// TestCanceledFailedReleaseMinimum confirms that a forced cancellation
// ends the old binding's minimum even if its release cannot be injected.
// Taking over the failed release starts a new logical press, whose own
// binding/profile minimum still applies, including after a failed down.
func TestCanceledFailedReleaseMinimum(t *testing.T) {
	for _, tc := range []struct {
		name        string
		oldInjected bool
		tap         bool
	}{
		{"failed down then tap", false, true},
		{"injected down then tap", true, true},
		{"failed down then binding", false, false},
		{"injected down then binding", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewFake()
			rec := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: 20 * time.Millisecond})
			t.Cleanup(func() {
				if err := arb.ReleaseAll(); err != nil {
					t.Error(err)
				}
				simtest.AssertNoneHeld(t, arb, rec)
			})
			ctrl := keys.MustParse("Ctrl")
			if !tc.oldInjected {
				rec.FailNext(0, nil)
			}
			if err := arb.DownFor(ownerA, ctrl, 48*time.Millisecond); (err == nil) != tc.oldInjected {
				t.Fatalf("initial down error=%v, injected=%t", err, tc.oldInjected)
			}
			rec.FailNext(0, nil)
			rec.FailNext(0, nil)
			if err := arb.ReleaseOwner(ownerA); err == nil {
				t.Fatal("forced release unexpectedly succeeded")
			}
			// The next command also fails to retry the canceled press's up,
			// then takes that key over for a different owner's new press.
			rec.FailNext(0, nil)
			completions := []output.TapCompletion{}
			var err error
			if tc.tap {
				err = arb.TapWithCompletion(ownerB, keys.MustParse("Ctrl+Z"), 24*time.Millisecond, func(result output.TapCompletion) {
					completions = append(completions, result)
				})
			} else {
				err = arb.DownFor(ownerB, ctrl, 37*time.Millisecond)
			}
			if err == nil {
				t.Fatal("missing failed retry error")
			}
			clk.Advance(27 * time.Millisecond)
			if err := arb.Advance(); err != nil {
				t.Fatal(err)
			}
			wantTrace := "+LCtrl@0"
			if tc.oldInjected {
				wantTrace += " +LCtrl@0"
			}
			if tc.tap {
				wantTrace += " +Z@0 -Z@27 -LCtrl@27"
				if len(completions) != 1 || completions[0] != (output.TapCompletion{}) {
					t.Fatalf("new tap completion=%+v", completions)
				}
			} else {
				if err := arb.Up(ownerB, ctrl); err != nil {
					t.Fatal(err)
				}
				at, ok := arb.NextDeadline()
				if !ok || at != clock.Time(37*time.Millisecond) {
					t.Fatalf("new binding deadline=%v/%t; want 37ms", at, ok)
				}
				clk.Advance(10 * time.Millisecond)
				if err := arb.Advance(); err != nil {
					t.Fatal(err)
				}
				wantTrace += " -LCtrl@37"
			}
			if got := simtest.FormatTrace(rec.Edges()); got != wantTrace {
				t.Fatalf("trace=%q, want %q", got, wantTrace)
			}
			if at, ok := arb.NextDeadline(); ok {
				t.Fatalf("new press left a deadline: %v", at)
			}
		})
	}
}
