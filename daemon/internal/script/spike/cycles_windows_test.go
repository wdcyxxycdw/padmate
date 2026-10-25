package spike

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// Plausible cycles per µs: QueryThreadCycleTime counts at the TSC rate,
// which is between 1 and 6 GHz on current x64 CPUs.
const (
	minRate = 100
	maxRate = 20000
)

func TestStartupCalibrationIsQuick(t *testing.T) {
	start := time.Now()
	r, err := CycleCalibration(CycleOptions{Calibrations: 1})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Calibrations) != 1 {
		t.Fatalf("got %d calibrations, want 1", len(r.Calibrations))
	}
	if c := r.Calibrations[0]; c < minRate || c > maxRate {
		t.Errorf("calibration = %.1f cycles per µs, want %d to %d", c, minRate, maxRate)
	}
	if elapsed > time.Second {
		t.Errorf("CycleCalibration took %s, want under 1s", elapsed)
	}
	t.Logf("%.1f cycles per µs in %s", r.Calibrations[0], elapsed)
}

func TestCycleCalibration(t *testing.T) {
	opts := CycleOptions{
		Spins:            []time.Duration{time.Millisecond, 10 * time.Millisecond},
		Runs:             3,
		Calibrations:     2,
		SleepSpin:        5 * time.Millisecond,
		Sleep:            20 * time.Millisecond,
		PerCPU:           2 * time.Millisecond,
		CrossThread:      30 * time.Millisecond,
		PollEvery:        2 * time.Millisecond,
		ThreadTimes:      2,
		ThreadTimesLimit: 200 * time.Millisecond,
	}
	start := time.Now()
	r, err := CycleCalibration(opts)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > time.Second {
		t.Errorf("CycleCalibration took %s, want under 1s", elapsed)
	}

	if r.QPCFrequency <= 0 || r.LogicalCPUs <= 0 || r.CPU == "" {
		t.Errorf("QPCFrequency = %d, LogicalCPUs = %d, CPU = %q", r.QPCFrequency, r.LogicalCPUs, r.CPU)
	}
	if r.QueryCost <= 0 || r.QueryCost > 100*time.Microsecond {
		t.Errorf("QueryCost = %s", r.QueryCost)
	}
	rate := func(what string, x float64) {
		t.Helper()
		if x < minRate || x > maxRate {
			t.Errorf("%s = %.1f cycles per µs, want %d to %d", what, x, minRate, maxRate)
		}
	}

	if len(r.Spins) != len(opts.Spins) {
		t.Fatalf("got %d spin lengths, want %d", len(r.Spins), len(opts.Spins))
	}
	for _, s := range r.Spins {
		if len(s.Samples) != opts.Runs {
			t.Errorf("%s: %d samples, want %d", s.Target, len(s.Samples), opts.Runs)
		}
		for _, x := range s.Samples {
			if x.Wall < s.Target*99/100 || x.Iterations == 0 {
				t.Errorf("%s: sample %+v", s.Target, x)
			}
		}
		// Interruptions only lower the rate, so the fastest run is the
		// one to check.
		rate(s.Target.String()+" spin", s.CyclesPerMicrosecond().Max)
	}
	if len(r.Calibrations) != opts.Calibrations {
		t.Errorf("got %d calibrations, want %d", len(r.Calibrations), opts.Calibrations)
	}
	best := 0.0
	for _, c := range r.Calibrations {
		rate("calibration", c)
		best = max(best, c)
	}

	if s := r.Sleep; s == nil {
		t.Error("no sleep result")
	} else {
		if s.Slept < opts.Sleep/2 {
			t.Errorf("SleepEx(%s) took %s", opts.Sleep, s.Slept)
		}
		// The thread runs for tens of µs of the sleep, not for all of it.
		if got := float64(s.SleptCycles) / micros(s.Slept); got > best/20 {
			t.Errorf("sleeping was charged %.1f cycles per µs; the thread's rate is %.1f", got, best)
		}
		rate("spin before the sleep", s.Before.CyclesPerMicrosecond())
		rate("spin after the sleep", s.After.CyclesPerMicrosecond())
	}

	if len(r.CPUs) == 0 {
		t.Error("no per-CPU results")
	}
	for _, c := range r.CPUs {
		if c.Sample.Migrations != 0 || c.Sample.FirstCPU != c.CPU {
			t.Errorf("pinned to CPU %d, the spin ran on %d with %d migrations", c.CPU, c.Sample.FirstCPU, c.Sample.Migrations)
		}
	}
	if len(r.CPUs) >= 2 {
		switch g := r.Migration; {
		case g == nil:
			t.Error("no migration result")
		case g.From.CPU == g.To.CPU || g.Total.FirstCPU != g.From.CPU || g.Total.LastCPU != g.To.CPU:
			t.Errorf("migration from %d to %d ran from %d to %d", g.From.CPU, g.To.CPU, g.Total.FirstCPU, g.Total.LastCPU)
		}
	}

	if c := r.CrossThread; c == nil {
		t.Error("no cross-thread result")
	} else {
		if c.Alone.Reads != 0 || c.Tight.Reads == 0 || c.Periodic.Reads == 0 {
			t.Errorf("reads: alone %d, tight %d, periodic %d", c.Alone.Reads, c.Tight.Reads, c.Periodic.Reads)
		}
		rate("cross-thread spin", c.Alone.Spin.CyclesPerMicrosecond())
		if r.LogicalCPUs >= 2 && len(c.Intervals) == 0 && !tightReaderSeesChanges(t, qpc{r.QPCFrequency}, opts.CrossThread) {
			t.Error("the tight reader never saw the spinner's cycle time change")
		}
	}

	if tt := r.ThreadTimes; tt == nil || len(tt.Increments) == 0 {
		t.Errorf("GetThreadTimes: %+v", tt)
	} else {
		for _, d := range tt.Increments {
			if d <= 0 {
				t.Errorf("GetThreadTimes increment %s", d)
			}
		}
	}

	var b strings.Builder
	if err := r.WriteMarkdown(&b); err != nil {
		t.Fatal(err)
	}
	t.Logf("took %s\n%s", elapsed, b.String())
}

// cpuSetEntry encodes one 32-byte SYSTEM_CPU_SET_INFORMATION.
func cpuSetEntry(typ uint32, group uint16, cpu, core, class byte) []byte {
	e := make([]byte, 32)
	binary.LittleEndian.PutUint32(e[0:], 32)
	binary.LittleEndian.PutUint32(e[4:], typ)
	binary.LittleEndian.PutUint16(e[12:], group)
	e[14], e[15], e[18] = cpu, core, class
	return e
}

func TestParseCPUSets(t *testing.T) {
	var buf []byte
	buf = append(buf, cpuSetEntry(cpuSetInformation, 0, 0, 0, 1)...)
	buf = append(buf, cpuSetEntry(cpuSetInformation, 1, 1, 1, 1)...) // group 1
	buf = append(buf, cpuSetEntry(7, 0, 2, 2, 1)...)                 // unknown type
	buf = append(buf, cpuSetEntry(cpuSetInformation, 0, 13, 9, 0)...)
	got, err := parseCPUSets(buf)
	if err != nil {
		t.Fatal(err)
	}
	want := []cpuSet{{cpu: 0, core: 0, class: 1}, {cpu: 13, core: 9, class: 0}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("parseCPUSets = %+v, want %+v", got, want)
	}

	for _, size := range []uint32{4, 33} {
		e := cpuSetEntry(cpuSetInformation, 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(e, size)
		if _, err := parseCPUSets(e); err == nil {
			t.Errorf("entry size %d of 32 bytes: no error", size)
		}
	}
}

func TestQPCConversion(t *testing.T) {
	for _, freq := range []int64{10_000_000, 3_579_545, 2_441_406} {
		q := qpc{freq}
		for _, d := range []time.Duration{time.Millisecond, time.Second, 90 * 24 * time.Hour} {
			back := q.duration(q.ticks(d))
			// Each conversion truncates: at most one tick, then 1ns.
			if tick := time.Second/time.Duration(freq) + 2; back > d || d-back > tick {
				t.Errorf("freq %d: %s round-trips to %s", freq, d, back)
			}
		}
	}
}

// tightReaderSeesChanges spins with a tight reader a few more times. The
// reader sees the spinner's cycle time change only while both threads
// run at once, and on a runner with 2 logical CPUs the other test
// binaries can keep one of them off the CPU for a whole spin.
func tightReaderSeesChanges(t *testing.T, q qpc, d time.Duration) bool {
	for i := range 5 {
		var intervals []time.Duration
		if _, err := polledSpin(q, d, 0, &intervals); err != nil {
			t.Fatal(err)
		}
		if len(intervals) > 0 {
			t.Logf("the tight reader saw changes on retry %d", i+1)
			return true
		}
	}
	return false
}
