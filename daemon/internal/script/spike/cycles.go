package spike

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CalibrationSpins short spins of CalibrationSpin make one startup
// calibration, the method this spike proposes for the watchdog: the
// fastest of them is the cycles-per-µs rate. A spin the scheduler or an
// interrupt cuts into only reads low, so the maximum is the rate of an
// uninterrupted spin, and a spin this short is often not interrupted at
// all. The error has no bound: it is the share of the best spin that was
// preempted, so it grows with load, and nothing detects a bad
// calibration. Its sign is safe: a low rate overstates CPU time, so the
// watchdog fires early. Observed on a 13600KF under load: 10 spins missed
// the rate by more than 0.1% in 5 of 100 calibrations (worst -1.9%), 5
// spins in 24 of 100 (worst -3.1%), and a full spk03 run saw -2.45% in 20;
// when lightly loaded, 100 calibrations stayed within -0.17%.
const (
	CalibrationSpins = 10
	CalibrationSpin  = time.Millisecond
)

// CycleOptions chooses what CycleCalibration measures. A zero field skips
// that part of the run.
type CycleOptions struct {
	// Spins are spin lengths in QPC time. Each is spun Runs times on the
	// calibration thread, which is locked but not pinned to a processor.
	Spins []time.Duration
	Runs  int
	// Calibrations is how many startup calibrations to run.
	Calibrations int
	// SleepSpin is spun before and after a SleepEx(Sleep) call.
	SleepSpin, Sleep time.Duration
	// PerCPU is spun once on each logical processor of processor group 0,
	// with the calibration thread pinned to it, and once across a forced
	// migration from the fastest to the slowest efficiency class.
	PerCPU time.Duration
	// CrossThread is spun three times on a second locked thread: with no
	// reader, while the calibration thread reads its cycle time in a
	// tight loop, and while it reads it every PollEvery (time.Sleep), as
	// the watchdog would poll a slot thread.
	CrossThread, PollEvery time.Duration
	// ThreadTimes is how many GetThreadTimes increments to observe while
	// spinning, for at most ThreadTimesLimit.
	ThreadTimes      int
	ThreadTimesLimit time.Duration
}

// DefaultCycleOptions returns the full SPK-03 run. It keeps one core busy
// for about 3 s, and a second one during CrossThread.
func DefaultCycleOptions() CycleOptions {
	return CycleOptions{
		Spins:            []time.Duration{time.Millisecond, 10 * time.Millisecond, 100 * time.Millisecond},
		Runs:             7,
		Calibrations:     20,
		SleepSpin:        10 * time.Millisecond,
		Sleep:            50 * time.Millisecond,
		PerCPU:           20 * time.Millisecond,
		CrossThread:      200 * time.Millisecond,
		PollEvery:        2 * time.Millisecond,
		ThreadTimes:      8,
		ThreadTimesLimit: 2 * time.Second,
	}
}

// CycleReport is what CycleCalibration measured. The parts CycleOptions
// skipped are nil or empty.
type CycleReport struct {
	// CPU is the ProcessorNameString of CPU 0 and NominalMHz its ~MHz,
	// both from HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0.
	CPU        string
	NominalMHz uint32
	// LogicalCPUs counts the logical processors of all groups.
	LogicalCPUs int
	// QPCFrequency is QueryPerformanceFrequency, in counts per second.
	QPCFrequency int64
	// TimerResolution is the global timer resolution
	// (NtQueryTimerResolution), the finest any process has asked for.
	// Since Windows 10 2004 it does not apply to the waits of a process
	// that has not asked for it itself.
	TimerResolution time.Duration
	// QueryCost is the mean cost of one QueryThreadCycleTime call on the
	// calling thread.
	QueryCost time.Duration

	Spins []SpinResult
	// Calibrations are startup calibrations, in cycles per µs.
	Calibrations []float64
	Sleep        *SleepResult
	CPUs         []CPUResult
	Migration    *MigrationResult
	CrossThread  *CrossThreadResult
	ThreadTimes  *ThreadTimesResult
}

// Sample is one spin: QueryThreadCycleTime charged Cycles to the thread
// over Wall of QPC time.
type Sample struct {
	Wall   time.Duration
	Cycles uint64
	// Iterations counts the units of fixed integer work (64 dependent
	// multiply-adds) the spin did, to tell a fast core from a slow one.
	Iterations uint64
	// Migrations counts the processor changes the spin saw;
	// FirstCPU and LastCPU are where it started and ended.
	Migrations        int
	FirstCPU, LastCPU uint32
}

// CyclesPerMicrosecond is cycles per µs of QPC time.
func (s Sample) CyclesPerMicrosecond() float64 {
	return float64(s.Cycles) / micros(s.Wall)
}

// IterationsPerMicrosecond is the work the spin did per µs.
func (s Sample) IterationsPerMicrosecond() float64 {
	return float64(s.Iterations) / micros(s.Wall)
}

// SpinResult holds the runs of one spin length.
type SpinResult struct {
	Target  time.Duration
	Samples []Sample
}

// SleepResult is a spin, a SleepEx call and another spin, with the cycles
// charged during each.
type SleepResult struct {
	Before, After Sample
	// Requested is the SleepEx argument; Slept is the QPC time the call
	// took and SleptCycles the cycles charged to the thread meanwhile.
	Requested   time.Duration
	Slept       time.Duration
	SleptCycles uint64
}

// CPUResult is a spin pinned to one logical processor.
type CPUResult struct {
	CPU  uint32 // logical processor index in group 0
	Core uint8
	// EfficiencyClass comes from GetSystemCpuSetInformation. Higher is
	// faster: on hybrid Intel CPUs, P-cores are 1 and E-cores 0.
	EfficiencyClass uint8
	Sample          Sample
}

// MigrationResult is a spin whose first half runs pinned to From and
// second half pinned to To.
type MigrationResult struct {
	From, To CPUResult // Sample is the half spun on that processor
	// Total spans both halves and the affinity change between them.
	Total Sample
	// Switch is the QPC time and SwitchCycles the cycles of the
	// SetThreadAffinityMask call that moved the thread.
	Switch       time.Duration
	SwitchCycles uint64
}

// CrossThreadResult is the watchdog's view: the calibration thread reads
// the cycle time of another thread that spins, through a
// THREAD_QUERY_LIMITED_INFORMATION handle.
type CrossThreadResult struct {
	// Alone has no reader, Tight a reader in a tight loop and Periodic
	// one that reads every PollEvery.
	Alone, Tight, Periodic PolledSpin
	PollEvery              time.Duration
	// Intervals is the QPC time between successive changes of the value
	// the tight reader saw, from the first change on.
	Intervals []time.Duration
}

// PolledSpin is a spin on another thread and the reads of its cycle time.
type PolledSpin struct {
	// Spin is the spinning thread's own measurement.
	Spin  Sample
	Reads int
	// PerRead is the QPC time of the reader's loop divided by Reads,
	// sleeps included.
	PerRead time.Duration
}

// ThreadTimesResult is GetThreadTimes (user + kernel time) polled while
// spinning.
type ThreadTimesResult struct {
	// Increments are the successive changes of the reported time and
	// Intervals the QPC time between them, both from the first change on:
	// the time before it is only part of a tick.
	Increments []time.Duration
	Intervals  []time.Duration
}

// Stats summarizes a set of measurements.
type Stats struct{ Min, Median, Max float64 }

// Spread is (Max - Min) / Median.
func (s Stats) Spread() float64 { return (s.Max - s.Min) / s.Median }

// Summarize returns the minimum, median and maximum of xs, or zero Stats
// when xs is empty.
func Summarize(xs []float64) Stats {
	if len(xs) == 0 {
		return Stats{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	med := s[n/2]
	if n%2 == 0 {
		med = (s[n/2-1] + s[n/2]) / 2
	}
	return Stats{Min: s[0], Median: med, Max: s[n-1]}
}

// CyclesPerMicrosecond summarizes the samples of the spin.
func (r SpinResult) CyclesPerMicrosecond() Stats {
	xs := make([]float64, len(r.Samples))
	for i, s := range r.Samples {
		xs[i] = s.CyclesPerMicrosecond()
	}
	return Summarize(xs)
}

// WriteMarkdown writes the report as Markdown for
// docs/bench/scripting.md.
func (r *CycleReport) WriteMarkdown(w io.Writer) error {
	m := &mdWriter{w: w}
	m.printf("- CPU: %s; ~MHz of CPU 0: %d; %d logical CPUs\n", r.CPU, r.NominalMHz, r.LogicalCPUs)
	m.printf("- QueryPerformanceFrequency: %d Hz; global timer resolution: %s\n",
		r.QPCFrequency, fmtDuration(r.TimerResolution))
	m.printf("- QueryThreadCycleTime on the current thread: %s per call\n", fmtDuration(r.QueryCost))
	if len(r.Spins) > 0 {
		m.printf("\nUnpinned spins, cycles per µs of QPC time:\n\n")
		m.printf("| Spin | Runs | Min | Median | Max | Spread | Median / ~MHz | Max / ~MHz | Migrations |\n")
		m.printf("|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, s := range r.Spins {
			st := s.CyclesPerMicrosecond()
			mig := 0
			for _, x := range s.Samples {
				mig += x.Migrations
			}
			m.printf("| %s | %d | %.1f | %.1f | %.1f | %s | %s | %s | %d |\n",
				fmtDuration(s.Target), len(s.Samples), st.Min, st.Median, st.Max, percent(st.Spread()),
				ratio(st.Median, r.NominalMHz), ratio(st.Max, r.NominalMHz), mig)
		}
	}
	if len(r.Calibrations) > 0 {
		st := Summarize(r.Calibrations)
		m.printf("\nStartup calibration (the fastest of %d spins of %s), %d times: %.1f min, %.1f median, %.1f max cycles per µs; "+
			"spread %s; median / ~MHz %s.\n",
			CalibrationSpins, fmtDuration(CalibrationSpin), len(r.Calibrations), st.Min, st.Median, st.Max,
			percent(st.Spread()), ratio(st.Median, r.NominalMHz))
	}
	if s := r.Sleep; s != nil {
		whole := Sample{Wall: s.Before.Wall + s.Slept + s.After.Wall, Cycles: s.Before.Cycles + s.SleptCycles + s.After.Cycles}
		m.printf("\nSleep in the middle of a spin:\n\n")
		m.printf("| Part | QPC time | Cycles | Cycles per µs |\n")
		m.printf("|---|---:|---:|---:|\n")
		m.printf("| Spin before | %s | %d | %.1f |\n", fmtDuration(s.Before.Wall), s.Before.Cycles, s.Before.CyclesPerMicrosecond())
		m.printf("| SleepEx(%s) | %s | %d | %.2f |\n", fmtDuration(s.Requested), fmtDuration(s.Slept), s.SleptCycles,
			float64(s.SleptCycles)/micros(s.Slept))
		m.printf("| Spin after | %s | %d | %.1f |\n", fmtDuration(s.After.Wall), s.After.Cycles, s.After.CyclesPerMicrosecond())
		m.printf("| Whole | %s | %d | %.1f |\n", fmtDuration(whole.Wall), whole.Cycles, whole.CyclesPerMicrosecond())
	}
	if len(r.CPUs) > 0 {
		m.printf("\nPinned to each logical CPU of group 0, by efficiency class (higher is faster; a unit of work is 64 dependent multiply-adds):\n\n")
		m.printf("| Efficiency class | CPUs | Cycles per µs min | median | max | Work per µs min | median | max |\n")
		m.printf("|---:|---|---:|---:|---:|---:|---:|---:|\n")
		for _, class := range classes(r.CPUs) {
			var cpus []uint32
			var cyc, work []float64
			for _, c := range r.CPUs {
				if c.EfficiencyClass == class {
					cpus = append(cpus, c.CPU)
					cyc = append(cyc, c.Sample.CyclesPerMicrosecond())
					work = append(work, c.Sample.IterationsPerMicrosecond())
				}
			}
			cs, ws := Summarize(cyc), Summarize(work)
			m.printf("| %d | %s | %.1f | %.1f | %.1f | %.2f | %.2f | %.2f |\n",
				class, cpuList(cpus), cs.Min, cs.Median, cs.Max, ws.Min, ws.Median, ws.Max)
		}
	}
	if g := r.Migration; g != nil {
		m.printf("\nForced migration in the middle of a spin:\n\n")
		m.printf("| Part | CPU (class) | QPC time | Cycles per µs | Work per µs |\n")
		m.printf("|---|---|---:|---:|---:|\n")
		for _, h := range []struct {
			name string
			c    CPUResult
		}{{"First half", g.From}, {"Second half", g.To}} {
			m.printf("| %s | %d (%d) | %s | %.1f | %.2f |\n", h.name, h.c.CPU, h.c.EfficiencyClass,
				fmtDuration(h.c.Sample.Wall), h.c.Sample.CyclesPerMicrosecond(), h.c.Sample.IterationsPerMicrosecond())
		}
		m.printf("| Whole spin | %d to %d | %s | %.1f | %.2f |\n", g.Total.FirstCPU, g.Total.LastCPU,
			fmtDuration(g.Total.Wall), g.Total.CyclesPerMicrosecond(), g.Total.IterationsPerMicrosecond())
		m.printf("\nThe affinity change itself took %s and %d cycles.\n", fmtDuration(g.Switch), g.SwitchCycles)
	}
	if c := r.CrossThread; c != nil {
		m.printf("\nAnother thread spins for %s while this one reads its cycle time (the watchdog's view):\n\n",
			fmtDuration(c.Alone.Spin.Wall))
		m.printf("| Reader | Reads | Per read | Spinner cycles per µs | Spinner work per µs | Migrations |\n")
		m.printf("|---|---:|---:|---:|---:|---:|\n")
		for _, p := range []struct {
			name string
			s    PolledSpin
		}{{"None", c.Alone}, {"Tight loop", c.Tight}, {"Every " + fmtDuration(c.PollEvery) + " (time.Sleep)", c.Periodic}} {
			per := "-"
			if p.s.Reads > 0 {
				per = fmtDuration(p.s.PerRead)
			}
			m.printf("| %s | %d | %s | %.1f | %.2f | %d |\n", p.name, p.s.Reads, per,
				p.s.Spin.CyclesPerMicrosecond(), p.s.Spin.IterationsPerMicrosecond(), p.s.Spin.Migrations)
		}
		iv := summarizeDurations(c.Intervals)
		m.printf("\nThe tight reader saw the value change %d times after the first change, every %s min, %s median, %s max.\n",
			len(c.Intervals), fmtDuration(iv[0]), fmtDuration(iv[1]), fmtDuration(iv[2]))
	}
	if t := r.ThreadTimes; t != nil {
		inc, iv := summarizeDurations(t.Increments), summarizeDurations(t.Intervals)
		m.printf("\nGetThreadTimes (user + kernel) polled while spinning: %d increments of %s min, %s median, %s max, "+
			"every %s min, %s median, %s max of QPC time.\n",
			len(t.Increments), fmtDuration(inc[0]), fmtDuration(inc[1]), fmtDuration(inc[2]),
			fmtDuration(iv[0]), fmtDuration(iv[1]), fmtDuration(iv[2]))
	}
	return m.err
}

// mdWriter keeps the first write error.
type mdWriter struct {
	w   io.Writer
	err error
}

func (m *mdWriter) printf(format string, args ...any) {
	if m.err == nil {
		_, m.err = fmt.Fprintf(m.w, format, args...)
	}
}

func micros(d time.Duration) float64 { return float64(d) / float64(time.Microsecond) }

func percent(x float64) string { return strconv.FormatFloat(100*x, 'f', 2, 64) + "%" }

// ratio formats cycles per µs divided by the nominal MHz.
func ratio(cyclesPerMicro float64, mhz uint32) string {
	if mhz == 0 {
		return "n/a"
	}
	return strconv.FormatFloat(cyclesPerMicro/float64(mhz), 'f', 4, 64)
}

// fmtDuration rounds d to three or four significant digits.
func fmtDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		d = d.Round(time.Millisecond)
	case d >= time.Millisecond:
		d = d.Round(10 * time.Microsecond)
	case d >= time.Microsecond:
		d = d.Round(10 * time.Nanosecond)
	}
	return d.String()
}

// summarizeDurations returns the minimum, median and maximum of ds.
func summarizeDurations(ds []time.Duration) [3]time.Duration {
	xs := make([]float64, len(ds))
	for i, d := range ds {
		xs[i] = float64(d)
	}
	s := Summarize(xs)
	return [3]time.Duration{time.Duration(s.Min), time.Duration(s.Median), time.Duration(s.Max)}
}

// classes returns the efficiency classes in cs, fastest first.
func classes(cs []CPUResult) []uint8 {
	var out []uint8
	for _, c := range cs {
		if !slices.Contains(out, c.EfficiencyClass) {
			out = append(out, c.EfficiencyClass)
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out
}

// cpuList formats sorted processor indexes with ranges: "0-11, 13".
func cpuList(cpus []uint32) string {
	var parts []string
	for i := 0; i < len(cpus); {
		j := i
		for j+1 < len(cpus) && cpus[j+1] == cpus[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.FormatUint(uint64(cpus[i]), 10))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", cpus[i], cpus[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}
