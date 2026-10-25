package slot_test

import (
	"errors"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// latencyEast is the binding of the latency benchmarks (ENG-11): east
// holds F13, so that each edge is one Arbiter command and one Send.
const latencyEast = `"east": {"press": {"type": "keys", "keys": "F13", "mode": "hold"}}`

// countSender injects every event and counts the presses and releases.
type countSender struct{ downs, ups int }

func (c *countSender) Send(events []output.KeyEvent) (int, error) {
	for _, e := range events {
		if e.Up {
			c.ups++
		} else {
			c.downs++
		}
	}
	return len(events), nil
}

// BenchmarkLatencyHandle measures Slot.Handle of one east edge, down and up
// in turn (ENG-11): the engine and the Arbiter on the caller's goroutine,
// which is slot→send without the Do hop to T-output (BenchmarkLatencyDo
// measures the hop). It reports the p50, p99 and max of the per-op times.
func BenchmarkLatencyHandle(b *testing.B) {
	cs := &countSender{}
	arb := output.NewArbiter(clock.Real(), cs, output.ArbiterOptions{})
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(b, latencyEast), Arbiter: arb, Clock: clock.Real()})
	if err != nil {
		b.Fatal(err)
	}
	edges := [2]input.Event{button("east", true), button("east", false)}
	// Untimed: the first Now finds QueryPerformanceCounter, which
	// allocates, and the first press and release set up the slot's state,
	// so that a run of -benchtime 1x does not report them.
	_ = latency.Now()
	s.Handle(edges[0])
	s.Handle(edges[1])
	lat := make([]time.Duration, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		t0 := latency.Now()
		s.Handle(edges[i%2])
		lat[i] = latency.Now().Sub(t0)
	}
	b.StopTimer()
	if b.N%2 == 1 {
		s.Handle(edges[1])
	}
	if errs := s.Errors(); len(errs) > 0 {
		b.Fatalf("slot errors: %v", errors.Join(errs...))
	}
	if want := 1 + (b.N+1)/2; cs.downs != want || cs.ups != want {
		b.Fatalf("the Sender saw %d downs and %d ups, want %d of each (CLAUDE.md invariant 1)", cs.downs, cs.ups, want)
	}
	if held := arb.Held(); len(held) > 0 {
		b.Fatalf("the Arbiter still holds %v (CLAUDE.md invariant 1)", held)
	}
	st := latency.Summarize(lat)
	b.ReportMetric(float64(st.P50), "p50-ns")
	b.ReportMetric(float64(st.P99), "p99-ns")
	b.ReportMetric(float64(st.Max), "max-ns")
}
