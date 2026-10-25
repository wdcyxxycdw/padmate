package output_test

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// scriptSender records a copy of every batch it is given. Call i (from 0)
// answers script[i] when there is one, and otherwise injects the whole
// batch.
type scriptSender struct {
	batches [][]output.KeyEvent
	script  map[int]scriptReply
}

// scriptReply is the answer of one scripted Send call.
type scriptReply struct {
	n   int // events injected, at most the batch's length
	err error
}

func (s *scriptSender) Send(events []output.KeyEvent) (int, error) {
	i := len(s.batches)
	s.batches = append(s.batches, slices.Clone(events))
	if r, ok := s.script[i]; ok {
		return min(r.n, len(events)), r.err
	}
	return len(events), nil
}

// sends formats the batches from index from on as "[+F13] [-F13]".
func (s *scriptSender) sends(from int) string {
	parts := make([]string, 0, len(s.batches)-from)
	for _, b := range s.batches[from:] {
		parts = append(parts, "["+eventsString(b)+"]")
	}
	return strings.Join(parts, " ")
}

// TestArbiterBenchSends pins the Send accounting of the latency benchmark
// (ENG-11) for its key set: a hold edge makes one Arbiter command, which
// makes exactly one Send with 1 (F13) or 3 (Ctrl+Shift+F14) events unless
// SendInput injects fewer events or a release is pending. A latency
// measurement that pairs each edge with its Sends has to tell these cases
// apart; the benchmarks here only count presses and releases.
func TestArbiterBenchSends(t *testing.T) {
	type step struct {
		up      bool // Up, else Down
		spec    string
		sends   string // the batches the step makes
		err     bool   // the step returns an error
		pending int    // Stats().PendingReleases after the step
	}
	for _, tt := range []struct {
		name   string
		script map[int]scriptReply
		steps  []step
		stats  output.ArbiterStats // at the end
	}{
		{
			name: "hold F13",
			steps: []step{
				{spec: "F13", sends: "[+F13]"},
				{up: true, spec: "F13", sends: "[-F13]"},
			},
		},
		{
			name: "hold Ctrl+Shift+F14",
			steps: []step{
				{spec: "Ctrl+Shift+F14", sends: "[+LCtrl +LShift +F14]"},
				{up: true, spec: "Ctrl+Shift+F14", sends: "[-F14 -LShift -LCtrl]"},
			},
		},
		{
			// The resend of the rest follows at once, in the same command.
			name:   "partial up",
			script: map[int]scriptReply{1: {n: 1}},
			steps: []step{
				{spec: "Ctrl+Shift+F14", sends: "[+LCtrl +LShift +F14]"},
				{up: true, spec: "Ctrl+Shift+F14", sends: "[-F14 -LShift -LCtrl] [-LShift -LCtrl]", err: true},
			},
			stats: output.ArbiterStats{SendErrors: 1, Retries: 1},
		},
		{
			// The release stays pending, and the next command retries it
			// first: that edge makes two Sends.
			name:   "failed up and resend",
			script: map[int]scriptReply{1: {err: errBlocked}, 2: {err: errBlocked}},
			steps: []step{
				{spec: "F13", sends: "[+F13]"},
				{up: true, spec: "F13", sends: "[-F13] [-F13]", err: true, pending: 1},
				{spec: "F13", sends: "[-F13] [+F13]"},
				{up: true, spec: "F13", sends: "[-F13]"},
			},
			stats: output.ArbiterStats{SendErrors: 2, Retries: 2},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &scriptSender{script: tt.script}
			a := output.NewArbiter(clock.NewFake(), s, output.ArbiterOptions{})
			for i, st := range tt.steps {
				from := len(s.batches)
				op, cmd := "Down", a.Down
				if st.up {
					op, cmd = "Up", a.Up
				}
				err := cmd(ownerA, keys.MustParse(st.spec))
				if (err != nil) != st.err {
					t.Errorf("step %d: %s(%s) = %v, want an error: %v", i, op, st.spec, err, st.err)
				}
				if got := s.sends(from); got != st.sends {
					t.Errorf("step %d: %s(%s) sent %s, want %s", i, op, st.spec, got, st.sends)
				}
				if got := a.Stats().PendingReleases; got != st.pending {
					t.Errorf("step %d: %s(%s): PendingReleases = %d, want %d", i, op, st.spec, got, st.pending)
				}
			}
			if st := a.Stats(); st != tt.stats {
				t.Errorf("Stats() = %+v, want %+v", st, tt.stats)
			}
			if held := a.Held(); len(held) > 0 {
				t.Errorf("Held() = %s, want nothing (CLAUDE.md invariant 1)", heldString(held))
			}
		})
	}
}

// countSender injects every event and counts the presses and releases. It
// runs on T-output, and the benchmark reads it only after Stop, which
// returns once T-output has ended.
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

// latencyGaps are the idle times of the gapped benchmark variants: none,
// 1 ms (about one T-input poll) and 10 ms (threads parked).
var latencyGaps = []time.Duration{0, time.Millisecond, 10 * time.Millisecond}

// gapName names a sub-benchmark by its gap: "gap=0", "gap=1ms".
func gapName(gap time.Duration) string {
	if gap == 0 {
		return "gap=0"
	}
	return "gap=" + gap.String()
}

// reportLatency reports the p50, p99 and max of the per-op times lat,
// taken as latency.Summarize does. Call it after the last ResetTimer,
// which deletes the reported metrics.
func reportLatency(b *testing.B, lat []time.Duration) {
	s := latency.Summarize(lat)
	b.ReportMetric(float64(s.P50), "p50-ns")
	b.ReportMetric(float64(s.P99), "p99-ns")
	b.ReportMetric(float64(s.Max), "max-ns")
}

// BenchmarkLatencyDo measures one command through the T-output Loop (ENG-11,
// the Do hop of ADR-0016) from a caller locked to its thread, as a slot
// loop is: noop is the bare round trip, and downup alternates
// Remote().Down and Up of F13, which make one Send each. downup-held is
// downup with a held-key log (OUT-14) in a temporary directory: each op
// writes it once, before the press's Send or after the release's. The
// gapped variants idle between ops, so that the loop thread parks; their
// sleep counts toward -benchtime but not toward the per-op times, and
// their ns/op, which would include it, is suppressed.
func BenchmarkLatencyDo(b *testing.B) {
	for _, op := range []string{"noop", "downup", "downup-held"} {
		b.Run(op, func(b *testing.B) {
			for _, gap := range latencyGaps {
				b.Run(gapName(gap), func(b *testing.B) { benchDo(b, op != "noop", op == "downup-held", gap) })
			}
		})
	}
}

func benchDo(b *testing.B, downup, held bool, gap time.Duration) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cs := &countSender{}
	opts := output.LoopOptions{Lock: true}
	if held {
		opts.HeldFile = filepath.Join(b.TempDir(), "held.bin")
		opts.KeyDown = func(output.KeyEvent) bool { return false }
	}
	l := output.StartLoop(clock.Real(), cs, opts)
	b.Cleanup(func() { _ = l.Stop() }) // after a Fatal; Stop is idempotent
	arb, f13 := l.Remote(), keys.MustParse("F13")
	// Untimed, so that a run of -benchtime 1x does not report them: the
	// first Now finds QueryPerformanceCounter, which allocates, the first
	// Do finds the loop goroutine up and its setup done, and for downup
	// the Arbiter holds and releases F13 once.
	_ = latency.Now()
	if err := l.Do(noop); err != nil {
		b.Fatal(err)
	}
	if downup {
		if err := arb.Down(ownerA, f13); err != nil {
			b.Fatal(err)
		}
		if err := arb.Up(ownerA, f13); err != nil {
			b.Fatal(err)
		}
	}
	op := func(int) error { return l.Do(noop) }
	if downup {
		op = func(i int) error {
			if i%2 == 0 {
				return arb.Down(ownerA, f13)
			}
			return arb.Up(ownerA, f13)
		}
	}
	lat := make([]time.Duration, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if gap > 0 {
			time.Sleep(gap)
		}
		t0 := latency.Now()
		err := op(i)
		lat[i] = latency.Now().Sub(t0)
		if err != nil {
			b.Fatalf("op %d: %v", i, err)
		}
	}
	b.StopTimer()
	if downup && b.N%2 == 1 {
		if err := arb.Up(ownerA, f13); err != nil {
			b.Fatal(err)
		}
	}
	if err := l.Stop(); err != nil {
		b.Fatalf("Stop: %v", err)
	}
	want := 0
	if downup {
		want = 1 + (b.N+1)/2
	}
	if d, u := cs.downs, cs.ups; d != want || u != want {
		b.Fatalf("the Sender saw %d downs and %d ups, want %d of each (CLAUDE.md invariant 1)", d, u, want)
	}
	reportLatency(b, lat)
	if gap > 0 {
		b.ReportMetric(0, "ns/op")
	}
}
