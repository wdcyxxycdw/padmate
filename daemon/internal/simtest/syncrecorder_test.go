package simtest_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// TestSyncRecorder: another goroutine sends while the test waits for the
// trace, then reads the Recorder under the lock.
func TestSyncRecorder(t *testing.T) {
	clk := clock.NewFake()
	s := simtest.NewSyncRecorder(clk)
	z, zUp := event(t, "Z", false), event(t, "Z", true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, e := range []output.KeyEvent{z, zUp, z} {
			if _, err := s.Send([]output.KeyEvent{e}); err != nil {
				t.Error(err)
			}
		}
	}()
	s.WaitFor(t, "+Z@0 -Z@0 +Z@0")
	<-done

	s.With(func(r *simtest.Recorder) { r.FailNext(0, nil) })
	if n, err := s.Send([]output.KeyEvent{zUp}); n != 0 || err == nil {
		t.Errorf("Send after FailNext = %d, %v; want 0 and an error", n, err)
	}
	clk.Set(clock.Time(5 * milli))
	must(t, sendOne(s, zUp))
	if got, want := s.Trace(), "+Z@0 -Z@0 +Z@0 -Z@5"; got != want {
		t.Errorf("Trace() = %s, want %s", got, want)
	}
	s.With(func(r *simtest.Recorder) {
		if r.Calls() != 5 {
			t.Errorf("Calls() = %d, want 5", r.Calls())
		}
		simtest.AssertNoneHeld(t, nil, r)
	})
}

func sendOne(s *simtest.SyncRecorder, e output.KeyEvent) error {
	_, err := s.Send([]output.KeyEvent{e})
	return err
}
