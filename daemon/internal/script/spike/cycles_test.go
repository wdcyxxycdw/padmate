package spike

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	for _, tc := range []struct {
		xs   []float64
		want Stats
	}{
		{nil, Stats{}},
		{[]float64{2}, Stats{2, 2, 2}},
		{[]float64{3, 1, 2}, Stats{1, 2, 3}},
		{[]float64{4, 1, 3, 2}, Stats{1, 2.5, 4}},
	} {
		in := slices.Clone(tc.xs)
		if got := Summarize(tc.xs); got != tc.want {
			t.Errorf("Summarize(%v) = %+v, want %+v", tc.xs, got, tc.want)
		}
		if !slices.Equal(in, tc.xs) {
			t.Errorf("Summarize changed its input to %v", tc.xs)
		}
	}
	if got := (Stats{Min: 9, Median: 10, Max: 11}).Spread(); got != 0.2 {
		t.Errorf("Spread = %v, want 0.2", got)
	}
}

func TestCPUList(t *testing.T) {
	for _, tc := range []struct {
		cpus []uint32
		want string
	}{
		{nil, ""},
		{[]uint32{5}, "5"},
		{[]uint32{0, 1, 2, 3}, "0-3"},
		{[]uint32{0, 1, 2, 4, 6, 7}, "0-2, 4, 6-7"},
	} {
		if got := cpuList(tc.cpus); got != tc.want {
			t.Errorf("cpuList(%v) = %q, want %q", tc.cpus, got, tc.want)
		}
	}
}

func TestSampleRates(t *testing.T) {
	s := Sample{Wall: 2 * time.Millisecond, Cycles: 7_000_000, Iterations: 16_000}
	if got := s.CyclesPerMicrosecond(); got != 3500 {
		t.Errorf("CyclesPerMicrosecond = %v, want 3500", got)
	}
	if got := s.IterationsPerMicrosecond(); got != 8 {
		t.Errorf("IterationsPerMicrosecond = %v, want 8", got)
	}
}

func sample(wall time.Duration, perMicro float64) Sample {
	return Sample{Wall: wall, Cycles: uint64(perMicro * micros(wall)), Iterations: uint64(8 * micros(wall))}
}

func TestWriteMarkdown(t *testing.T) {
	ms := time.Millisecond
	r := &CycleReport{
		CPU: "Test CPU", NominalMHz: 3500, LogicalCPUs: 4, QPCFrequency: 10_000_000,
		TimerResolution: ms, QueryCost: 150 * time.Nanosecond,
		Spins:        []SpinResult{{Target: ms, Samples: []Sample{sample(ms, 3500), sample(ms, 3430)}}},
		Calibrations: []float64{3500, 3499},
		Sleep: &SleepResult{
			Before: sample(10*ms, 3500), After: sample(10*ms, 3500),
			Requested: 50 * ms, Slept: 50 * ms, SleptCycles: 100_000,
		},
		CPUs: []CPUResult{
			{CPU: 0, EfficiencyClass: 1, Sample: sample(20*ms, 3500)},
			{CPU: 1, EfficiencyClass: 1, Sample: sample(20*ms, 3500)},
			{CPU: 2, EfficiencyClass: 0, Sample: sample(20*ms, 3500)},
		},
		Migration: &MigrationResult{
			From:  CPUResult{CPU: 0, EfficiencyClass: 1, Sample: sample(10*ms, 3500)},
			To:    CPUResult{CPU: 2, EfficiencyClass: 0, Sample: sample(10*ms, 3500)},
			Total: Sample{Wall: 20 * ms, Cycles: 70_000_000, FirstCPU: 0, LastCPU: 2},
		},
		CrossThread: &CrossThreadResult{
			Alone:     PolledSpin{Spin: sample(200*ms, 3500)},
			Tight:     PolledSpin{Spin: sample(200*ms, 2500), Reads: 300_000, PerRead: 666 * time.Nanosecond},
			Periodic:  PolledSpin{Spin: sample(200*ms, 3490), Reads: 91, PerRead: 2200 * time.Microsecond},
			PollEvery: 2 * ms,
			Intervals: []time.Duration{400, 600, 700},
		},
		ThreadTimes: &ThreadTimesResult{
			Increments: []time.Duration{15625 * time.Microsecond, 15625 * time.Microsecond},
			Intervals:  []time.Duration{15 * ms, 16 * ms},
		},
	}
	var b strings.Builder
	if err := r.WriteMarkdown(&b); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"- CPU: Test CPU; ~MHz of CPU 0: 3500; 4 logical CPUs\n",
		"| 1ms | 2 | 3430.0 | 3465.0 | 3500.0 | 2.02% | 0.9900 | 1.0000 | 0 |\n",
		"the fastest of 10 spins of 1ms), 2 times: 3499.0 min, 3499.5 median, 3500.0 max",
		"| SleepEx(50ms) | 50ms | 100000 | 2.00 |\n",
		"| 1 | 0-1 | 3500.0 | 3500.0 | 3500.0 | 8.00 | 8.00 | 8.00 |\n",
		"| 0 | 2 | ",
		"| Whole spin | 0 to 2 | 20ms | 3500.0 |",
		"| Every 2ms (time.Sleep) | 91 | 2.2ms | 3490.0 |",
		"every 400ns min, 600ns median, 700ns max",
		"2 increments of 15.63ms min",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "| 1 | 0-1 |") > strings.Index(got, "| 0 | 2 |") {
		t.Error("efficiency classes are not listed fastest first")
	}

	// Skipped parts are left out, and a missing ~MHz is not a division by
	// zero.
	b.Reset()
	short := &CycleReport{CPU: "x", Spins: r.Spins}
	if err := short.WriteMarkdown(&b); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, "| n/a | n/a |") || strings.Contains(got, "Sleep") {
		t.Errorf("short report:\n%s", got)
	}

	errWrite := errors.New("write failed")
	if err := r.WriteMarkdown(failWriter{errWrite}); !errors.Is(err, errWrite) {
		t.Errorf("WriteMarkdown to a failing writer = %v, want %v", err, errWrite)
	}
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }
