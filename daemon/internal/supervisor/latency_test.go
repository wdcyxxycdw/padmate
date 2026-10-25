package supervisor_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// latencyEast is the binding of the latency benchmarks (ENG-11): east
// holds F13, so that each edge makes one Send.
const latencyEast = `"east": {"press": {"type": "keys", "keys": "F13", "mode": "hold"}}`

// sigSender injects every event and counts the presses and releases. After
// each Send it stamps ret and signals sent without blocking. It runs on
// T-output: the benchmark reads ret, which is stored before the signal,
// after receiving the signal, and the counts only after the Loop's Stop,
// which returns once T-output has ended.
type sigSender struct {
	downs, ups int
	ret        atomic.Int64 // latency.Stamp at the end of the last Send
	sent       chan struct{}
}

func (s *sigSender) Send(events []output.KeyEvent) (int, error) {
	for _, e := range events {
		if e.Up {
			s.ups++
		} else {
			s.downs++
		}
	}
	s.ret.Store(int64(latency.Now()))
	select {
	case s.sent <- struct{}{}:
	default:
	}
	return len(events), nil
}

// BenchmarkLatencyRoute measures one east edge from the Events channel
// through the router, the slot loop and the Do hop to T-output's Send
// (ENG-11), with no SDL, on a Supervisor configured as padmated's:
// MaxSlots slots, the gamepad on the first, real clock, slot loops and
// T-output locked to their threads. One op pushes an edge, down and up in turn, and waits for its
// Send. The per-op time ends at the stamp the Sender takes on T-output,
// so the benchmark goroutine's own wake-up is not in it. The gapped
// variants idle between ops, so that the threads park; their sleep counts
// toward -benchtime but not toward the per-op times, and their ns/op is
// suppressed.
func BenchmarkLatencyRoute(b *testing.B) {
	for _, gap := range []time.Duration{0, time.Millisecond, 10 * time.Millisecond} {
		name := "gap=0"
		if gap > 0 {
			name = "gap=" + gap.String()
		}
		b.Run(name, func(b *testing.B) { benchRoute(b, gap) })
	}
}

func benchRoute(b *testing.B, gap time.Duration) {
	prog, err := compileBase(latencyEast)
	if err != nil {
		b.Fatal(err)
	}
	sig := &sigSender{sent: make(chan struct{}, 1)}
	loop := output.StartLoop(clock.Real(), sig, output.LoopOptions{Lock: true})
	events := make(chan input.Event, 16) // buffered, as input's is
	sup, err := supervisor.New(supervisor.Config{
		Clock: clock.Real(), Events: events, Output: loop, Program: prog, Lock: true, Slots: supervisor.MaxSlots,
	})
	if err != nil {
		_ = loop.Stop()
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- sup.Run(ctx) }()
	// stop ends Run, then the Loop; the cleanup calls it after a Fatal.
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-result:
			if err != nil {
				b.Errorf("Run: %v", err)
			}
		case <-time.After(waitLimit):
			b.Errorf("Run did not return within %v", waitLimit)
		}
		if err := loop.Stop(); err != nil {
			b.Errorf("output.Loop.Stop: %v", err)
		}
	}
	b.Cleanup(stop)

	timer := time.NewTimer(waitLimit)
	defer timer.Stop()
	// push hands e to Run; the channel has room, so it neither waits nor
	// allocates unless Run has stopped taking events.
	push := func(e input.Event) {
		select {
		case events <- e:
			return
		default:
		}
		select {
		case events <- e:
		case <-time.After(waitLimit):
			b.Fatalf("Run takes no events (%v)", e.Kind)
		}
	}
	// await waits for the next Send, with the one timer: it allocates
	// nothing.
	await := func(what string) {
		timer.Reset(waitLimit)
		select {
		case <-sig.sent:
			timer.Stop()
		case <-timer.C:
			b.Fatalf("no Send within %v after %s", waitLimit, what)
		}
	}
	edges := [2]input.Event{button(1, "east", true), button(1, "east", false)}
	// The gamepad takes the slot provisionally, and its first press
	// confirms it (ADR-0019).
	push(added(pad(1, "Latency pad", "03ab")))
	push(edges[0])
	await("the first press")
	push(edges[1])
	await("the first release")

	lat := make([]time.Duration, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if gap > 0 {
			time.Sleep(gap)
		}
		t0 := latency.Now()
		push(edges[i%2])
		await("an edge")
		lat[i] = latency.Stamp(sig.ret.Load()).Sub(t0)
	}
	b.StopTimer()
	if b.N%2 == 1 {
		push(edges[1])
		await("the last release")
	}
	stop()
	if want := 1 + (b.N+1)/2; sig.downs != want || sig.ups != want {
		b.Fatalf("the Sender saw %d downs and %d ups, want %d of each (CLAUDE.md invariant 1)", sig.downs, sig.ups, want)
	}
	s := latency.Summarize(lat)
	b.ReportMetric(float64(s.P50), "p50-ns")
	b.ReportMetric(float64(s.P99), "p99-ns")
	b.ReportMetric(float64(s.Max), "max-ns")
	if gap > 0 {
		b.ReportMetric(0, "ns/op")
	}
}
