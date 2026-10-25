package latency

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestRecorderPercentiles (DMN-15): the Recorder summarizes with the same
// nearest-rank rule as the bench (Summarize), over virtual data: 1..1000 µs
// has p50 500 µs, p90 900 µs, p99 990 µs and a maximum of 1000 µs.
func TestRecorderPercentiles(t *testing.T) {
	r := NewRecorder()
	for i := 1; i <= 1000; i++ {
		r.Record("pad", time.Duration(i)*time.Microsecond)
	}
	got := r.Summary("pad")
	want := Summary{N: 1000, P50: 500 * time.Microsecond, P90: 900 * time.Microsecond, P99: 990 * time.Microsecond, P999: 999 * time.Microsecond, Max: 1000 * time.Microsecond}
	if got != want {
		t.Errorf("Summary = %+v, want %+v", got, want)
	}
	if s := r.Summary("other"); s != (Summary{}) {
		t.Errorf("Summary of a device without samples = %+v, want the zero Summary", s)
	}
}

// TestRecorderRing: only the latest RingSize samples count, the oldest are
// overwritten, and a ring that has not wrapped yet counts what it has.
func TestRecorderRing(t *testing.T) {
	r := NewRecorder()
	for i := 1; i <= 7; i++ {
		r.Record("pad", time.Duration(i)*time.Millisecond)
	}
	if s := r.Summary("pad"); s.N != 7 || s.Max != 7*time.Millisecond {
		t.Errorf("before the ring is full: N %d, max %v, want 7 and 7ms", s.N, s.Max)
	}
	for i := 1; i <= 2*RingSize+300; i++ {
		r.Record("pad", time.Duration(i)*time.Microsecond)
	}
	s := r.Summary("pad")
	if s.N != RingSize {
		t.Fatalf("N = %d, want %d", s.N, RingSize)
	}
	// The latest 1000 of 2300 samples are 1301..2300 µs: the 7 ms and the
	// older samples are gone.
	if s.Max != 2300*time.Microsecond || s.P50 != 1800*time.Microsecond {
		t.Errorf("max %v, p50 %v, want 2.3ms and 1.8ms", s.Max, s.P50)
	}
}

// TestRecorderDevices: each device has its own ring; Reset forgets one,
// ResetAll all; and with more than MaxDevices devices the ring written least
// recently is given up, never one in use more recently.
func TestRecorderDevices(t *testing.T) {
	r := NewRecorder()
	r.Record("a", time.Millisecond)
	r.Record("a", 3*time.Millisecond)
	r.Record("b", 2*time.Millisecond)
	if a, b := r.Summary("a"), r.Summary("b"); a.N != 2 || a.Max != 3*time.Millisecond || b.N != 1 || b.Max != 2*time.Millisecond {
		t.Errorf("a = %+v, b = %+v", a, b)
	}
	r.Reset("a")
	if a, b := r.Summary("a"), r.Summary("b"); a.N != 0 || b.N != 1 {
		t.Errorf("after Reset(a): a.N %d, b.N %d, want 0 and 1", a.N, b.N)
	}
	r.Reset("nobody") // no ring, nothing to do
	r.Record("a", time.Millisecond)
	r.ResetAll()
	if a, b := r.Summary("a"), r.Summary("b"); a.N != 0 || b.N != 0 {
		t.Errorf("after ResetAll: a.N %d, b.N %d, want 0", a.N, b.N)
	}

	// MaxDevices+1 devices: the first is the least recently written, unless it
	// is written again just before.
	for i := range MaxDevices {
		r.Record(fmt.Sprint("pad", i), time.Duration(i+1)*time.Millisecond)
	}
	r.Record("pad0", 9*time.Millisecond) // pad1 is now the oldest
	r.Record("newcomer", time.Millisecond)
	if s := r.Summary("pad1"); s.N != 0 {
		t.Errorf("the least recently written ring was kept: %+v", s)
	}
	for _, key := range []string{"pad0", "pad2", "newcomer"} {
		if s := r.Summary(key); s.N == 0 {
			t.Errorf("ring %s was given up", key)
		}
	}
	if s := r.Summary("pad0"); s.N != 2 {
		t.Errorf("pad0 has %d samples, want its own 2", s.N)
	}
}

// TestRecorderNoAlloc (DMN-15): Record, which T-output runs once per key sent,
// allocates nothing: not for a ring in use, not for a new device that takes a
// free ring, and not for one that takes the ring of the least recently written.
func TestRecorderNoAlloc(t *testing.T) {
	r := NewRecorder()
	keys := make([]string, 3*MaxDevices)
	for i := range keys {
		keys[i] = fmt.Sprint("DeviceKey-", i)
	}
	if n := testing.AllocsPerRun(100, func() { r.Record(keys[0], time.Millisecond) }); n != 0 {
		t.Errorf("Record of a ring in use allocates %v times", n)
	}
	i := 0
	if n := testing.AllocsPerRun(1000, func() {
		r.Record(keys[i%len(keys)], time.Millisecond)
		i++
	}); n != 0 {
		t.Errorf("Record of new devices allocates %v times", n)
	}
}

// TestRecorderConcurrent: T-output writes while the IPC goroutines read and
// reset. (Run with -race by CI.)
func TestRecorderConcurrent(t *testing.T) {
	r := NewRecorder()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			r.Record(fmt.Sprint("pad", i%3), time.Duration(i)*time.Microsecond)
		}
	}()
	for range 200 {
		_ = r.Summary("pad0")
		r.Reset("pad1")
	}
	r.ResetAll()
	close(stop)
	wg.Wait()
}

// TestWarn (DMN-15, ADR-0071): a p99 above 1.5 ms warns, but only from 50
// samples on; exactly 1.5 ms does not warn, as the bench's gate is p99 < 1.5 ms.
func TestWarn(t *testing.T) {
	if WarnP99 != 1500*time.Microsecond || WarnMinSamples != 50 {
		t.Fatalf("thresholds changed: %v, %d", WarnP99, WarnMinSamples)
	}
	for _, tc := range []struct {
		name string
		s    Summary
		want bool
	}{
		{"none", Summary{}, false},
		{"below", Summary{N: 1000, P99: 1499 * time.Microsecond}, false},
		{"at", Summary{N: 1000, P99: WarnP99}, false},
		{"above", Summary{N: 1000, P99: WarnP99 + time.Microsecond}, true},
		{"above with 50", Summary{N: 50, P99: 5 * time.Millisecond}, true},
		{"above with 49", Summary{N: 49, P99: 5 * time.Millisecond}, false},
	} {
		if got := Warn(tc.s); got != tc.want {
			t.Errorf("%s: Warn = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestRecorderSummaryAllocatesOutsideTheLock (DMN-15): Summary copies a ring
// under the mutex that Record of T-output needs, so it must not allocate
// there: an allocation that stalls (a GC assist) would stall T-output behind
// the reader. The copy goes to the stack, and the one allocation that Summary
// makes is the clone that Summarize sorts, after the mutex is released.
func TestRecorderSummaryAllocatesOutsideTheLock(t *testing.T) {
	r := NewRecorder()
	samples := make([]time.Duration, RingSize)
	for i := range samples {
		samples[i] = time.Duration(i+1) * time.Microsecond
		r.Record("pad", samples[i])
	}
	want := testing.AllocsPerRun(100, func() { _ = Summarize(samples) })
	if got := testing.AllocsPerRun(100, func() { _ = r.Summary("pad") }); got != want {
		t.Errorf("Summary allocates %v times, Summarize of the same samples %v: the copy under the mutex allocates", got, want)
	}
	if got := testing.AllocsPerRun(100, func() { _ = r.Summary("nobody") }); got != 0 {
		t.Errorf("Summary of a device without samples allocates %v times", got)
	}
	if s := r.Summary("pad"); s.N != RingSize || s.Max != RingSize*time.Microsecond {
		t.Errorf("Summary %+v, want %d samples up to %v", s, RingSize, RingSize*time.Microsecond)
	}
}
