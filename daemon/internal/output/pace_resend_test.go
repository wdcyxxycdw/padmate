package output_test

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestEventGapDownForMinimumBehindFailedRelease: a DownFor of a key that is
// down and has two releases queued (its tap's, and the tap's behind it)
// presses it again behind both (OUT-17), and its minimum belongs to that
// press alone. The first release fails and its one resend fails too, two
// Send calls for one release: the second queued release is still ahead of
// the DownFor's press, so the press before it is let go of after its own
// short hold, not after the DownFor's 100 ms (the fuzz seed bec4cdec0e03dbf4
// of FuzzArbiterTiming found the model counting the resend as a second
// release, and giving the minimum to the wrong press).
func TestEventGapDownForMinimumBehindFailedRelease(t *testing.T) {
	s := newSim(t, paced(20*ms, 0, 0))
	s.tap(ownerA, "Ctrl", 10*ms)
	// The tap ends at 10 ms, but its release waits for the gap: the Send at
	// 20 ms fails, and so does the resend that follows at once.
	s.Out.FailNext(0, errBlocked)
	s.Out.FailNext(0, errBlocked)
	s.AdvanceTo(clock.Time(10 * ms))
	s.tap(ownerB, "Z", 0) // a press after the release queued: Ctrl is pressed again behind it
	s.tap(ownerA, "Ctrl+X", 5*ms)
	s.AdvanceTo(clock.Time(15 * ms))
	if got := queuedKeys(s.Arb); got != "-LCtrl +Z -Z +LCtrl +X -X -LCtrl" {
		t.Fatalf("queue = %q", got)
	}
	s.ok("DownFor", ownerA, "Ctrl", s.Arb.DownFor(ownerA, keys.MustParse("Ctrl"), 100*ms))
	if got := queuedKeys(s.Arb); got != "-LCtrl +Z -Z +LCtrl +X -X -LCtrl +LCtrl" {
		t.Fatalf("queue = %q", got)
	}
	s.up(ownerA, "Ctrl")
	s.Settle()
	// The release at 20 ms was offered twice and did not go out.
	var offered int
	for _, r := range s.Out.Records() {
		if r.Event.Up && !r.Injected {
			offered++
			if r.T != clock.Time(20*ms) {
				t.Errorf("failed release at %v, want 20ms", r.T)
			}
		}
	}
	if offered != 2 {
		t.Errorf("%d failed releases offered, want 2: the release and its one resend", offered)
	}
	// The press at 80 ms is let go of at 140 ms, 60 ms later: it had its own
	// hold of 5 ms, not the 100 ms of the press behind it, which goes up
	// 100 ms after its Send at 160 ms.
	s.ExpectOutput("+LCtrl@0 +Z@40 -Z@60 +LCtrl@80 +X@100 -X@120 -LCtrl@140 +LCtrl@160 -LCtrl@260")
	if errs := s.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "failed twice") {
		t.Errorf("errors = %v, want one error: the release failed twice", errs)
	}
}

// queuedKeys lists the events waiting for the event gap as "+LCtrl -LCtrl".
func queuedKeys(a *output.Arbiter) string {
	var out string
	for i, e := range output.PacedEvents(a) {
		if i > 0 {
			out += " "
		}
		if e.Up {
			out += "-"
		} else {
			out += "+"
		}
		out += e.Key.String()
	}
	return out
}
