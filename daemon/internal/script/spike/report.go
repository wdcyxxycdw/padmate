package spike

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The performance targets of SCRIPTING §7.
const (
	TargetDispatchP50 = 20 * time.Microsecond
	TargetDispatchP99 = 200 * time.Microsecond
	TargetActivation  = 10 * time.Millisecond
	TargetBuild       = 50 * time.Millisecond
)

// Options say what Run measures.
type Options struct {
	Dispatch DispatchOptions
	// Runs is the number of cold and of warm activations.
	Runs     int
	Watchdog WatchdogCheckOptions
	// Cycles is the QueryThreadCycleTime calibration; nil skips it.
	Cycles *CycleOptions
}

// DefaultOptions are the full SPK-03 run.
func DefaultOptions() Options {
	c := DefaultCycleOptions()
	return Options{Dispatch: DefaultDispatch(), Runs: 200, Watchdog: DefaultWatchdogCheck(), Cycles: &c}
}

// Report is everything Run measured.
type Report struct {
	Options    Options
	Machine    Machine
	Activation *ActivationReport
	Dispatch   *DispatchReport
	Watchdog   *WatchdogReport
	// TLA is the build error of a script with a top-level await.
	TLA string
	// Cycles is the calibration, nil when skipped or unsupported;
	// CyclesErr is the errors.ErrUnsupported error when unsupported.
	Cycles    *CycleReport
	CyclesErr error
	Elapsed   time.Duration
}

// Run makes the SPK-03 measurements. The first esbuild build and goja
// activation of the process are the "first" rows, so call it before
// anything else in the process uses esbuild or goja.
func Run(opts Options) (*Report, error) {
	t0 := stamp()
	rep := &Report{Options: opts, Machine: MachineInfo()}
	s, err := NewScript()
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	if rep.Activation, err = MeasureActivation(s, opts.Runs); err != nil {
		return nil, err
	}
	if rep.Dispatch, err = MeasureDispatch(rep.Activation.Program, opts.Dispatch); err != nil {
		return nil, err
	}
	if rep.Watchdog, err = CheckWatchdog(opts.Watchdog); err != nil {
		return nil, err
	}
	if rep.TLA, err = TLADiagnostic(s); err != nil {
		return nil, err
	}
	if opts.Cycles != nil {
		rep.Cycles, rep.CyclesErr = CycleCalibration(*opts.Cycles)
		if rep.CyclesErr != nil && !errors.Is(rep.CyclesErr, errors.ErrUnsupported) {
			return nil, rep.CyclesErr
		}
	}
	rep.Elapsed = stampDuration(stamp() - t0)
	return rep, nil
}

// Row is one line of the results table.
type Row struct {
	Metric  string
	Latency Latency
	// Single is a one-off measurement (N = 1), shown as p50.
	Single *time.Duration
	// Allocs and Bytes are per operation; negative when not measured.
	Allocs, Bytes float64
	// GCs is the GC count of the row's own timed pass; negative when the
	// row has none: a one-off, or a part of the cold activation pass,
	// whose count is on the "activation cold" row.
	GCs int
	// Target is the SCRIPTING §7 target, "" for none.
	Target string
	// Pass is the verdict against Target.
	Pass bool
}

// minP999 is the smallest N whose p99.9 the table shows: with fewer
// samples the rank is one of the last few, the maximum for N = 200.
const minP999 = 10000

// Rows are the results table.
func (r *Report) Rows() []Row {
	var rows []Row
	if r.Dispatch != nil {
		for _, d := range r.Dispatch.Results {
			metric := "dispatch " + d.Scenario.Name
			if d.Label != "" {
				metric += " " + d.Label
			}
			rows = append(rows, Row{
				Metric: metric, Latency: d.Latency, Allocs: d.AllocsPerOp, Bytes: d.BytesPerOp, GCs: int(d.GCs),
				Target: "p50 < 20µs, p99 < 200µs",
				Pass:   d.Latency.P50 < TargetDispatchP50 && d.Latency.P99 < TargetDispatchP99,
			})
		}
	}
	if a := r.Activation; a != nil {
		p99 := func(metric string, l Latency, gcs int, target time.Duration) Row {
			return Row{Metric: metric, Latency: l, Allocs: -1, Bytes: -1, GCs: gcs, Target: "p99 < " + target.String(), Pass: l.P99 < target}
		}
		warm := p99("activation warm: cached Program → Runtime + RunProgram", a.Warm, int(a.WarmGCs), TargetActivation)
		warm.Allocs, warm.Bytes = a.WarmAllocs, a.WarmBytes
		firstBuild := stampDuration(a.First.Build)
		firstAll := stampDuration(a.First.Build + a.First.Compile + a.First.Run)
		rows = append(rows,
			warm,
			p99("activation from cached bundle: goja.Compile + Runtime + RunProgram", a.CompileRun, -1, TargetActivation),
			p99("activation cold: esbuild + goja.Compile + Runtime + RunProgram", a.Cold, int(a.ColdGCs), TargetActivation),
			Row{Metric: "activation cold, first in process", Single: &firstAll, Allocs: -1, Bytes: -1, GCs: -1,
				Target: "< " + TargetActivation.String(), Pass: firstAll < TargetActivation},
			p99("esbuild build, warm: every build after the first", a.Build, -1, TargetBuild),
			Row{Metric: "esbuild build, cold: first in process", Single: &firstBuild, Allocs: -1, Bytes: -1, GCs: -1,
				Target: "< " + TargetBuild.String(), Pass: firstBuild < TargetBuild},
			Row{Metric: "goja.Compile of the bundle (part of cold)", Latency: a.Compile, Allocs: -1, Bytes: -1, GCs: -1},
		)
	}
	return rows
}

// WriteMarkdown writes the report: machine, results table, notes,
// watchdog, top-level await and the QueryThreadCycleTime calibration.
func (r *Report) WriteMarkdown(w io.Writer) error {
	m := &mdWriter{w: w}
	mc := r.Machine
	m.printf("## SPK-03 results\n\n")
	m.printf("- CPU: %s; %d logical CPUs; GOMAXPROCS %d\n", mc.CPU, mc.LogicalCPUs, mc.GOMAXPROCS)
	m.printf("- OS: %s\n", mc.OS)
	m.printf("- Go: %s; GOGC: %s\n", mc.Go, orDefault(mc.GOGC, "unset (100)"))
	m.printf("- goja: %s\n", mc.Goja)
	m.printf("- esbuild: %s\n", mc.Esbuild)
	m.printf("- Stopwatch: %s, %s per reading (included in the timings, not subtracted)\n", mc.Stopwatch, fmtDuration(mc.StampCost))
	d := r.Options.Dispatch
	thread := "a locked OS thread, not pinned"
	if r.Dispatch != nil && r.Dispatch.Fast != nil {
		c := r.Dispatch.Fast
		thread = fmt.Sprintf("a locked OS thread pinned to CPU %d (efficiency class %d, the fastest)", c.Index, c.EfficiencyClass)
	}
	m.printf("- Dispatch: per scenario a fresh runtime, %d warm-up dispatches, %d timed one by one on %s; "+
		"allocations from runtime.MemStats around each of %d more\n", d.Warmup, d.N, thread, d.AllocN)
	if a := r.Activation; a != nil {
		m.printf("- Activation: %d cold and %d warm runs; the script makes %d registrations\n", a.Runs, a.Runs, a.Registrations)
		m.printf("- Bundle: %d bytes, of which %d are the inline source map (%d bytes of code)\n", a.Size, a.SourceMap, a.Size-a.SourceMap)
	}
	m.printf("\n| Metric | N | p50 | p90 | p99 | p99.9 | max | allocs/op | B/op | GCs | Target | Result |\n")
	m.printf("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|---|\n")
	for _, row := range r.Rows() {
		m.printf("| %s |", row.Metric)
		if row.Single != nil {
			m.printf(" 1 | %s | – | – | – | – |", fmtDuration(*row.Single))
		} else {
			l := row.Latency
			p999 := "–"
			if l.N >= minP999 {
				p999 = fmtDuration(l.P999)
			}
			m.printf(" %d | %s | %s | %s | %s | %s |", l.N, fmtDuration(l.P50), fmtDuration(l.P90), fmtDuration(l.P99),
				p999, fmtDuration(l.Max))
		}
		if row.Allocs >= 0 {
			m.printf(" %.1f | %.0f |", row.Allocs, row.Bytes)
		} else {
			m.printf(" – | – |")
		}
		if row.GCs >= 0 {
			m.printf(" %d |", row.GCs)
		} else {
			m.printf(" – |")
		}
		switch {
		case row.Target == "":
			m.printf(" – | – |\n")
		case row.Pass:
			m.printf(" %s | pass |\n", row.Target)
		default:
			m.printf(" %s | **fail** |\n", row.Target)
		}
	}
	m.printf("\nScenarios:\n\n")
	for _, s := range Scenarios {
		m.printf("- %s: %s\n", s.Name, s.Desc)
	}
	m.printf("\nNotes:\n\n")
	if d.IdleN > 0 {
		m.printf("- \"after G idle\": each timed dispatch follows time.Sleep(G) on the locked thread, as the slot loop parks "+
			"between events; the stopwatch starts once the thread is awake, so the row is the dispatch on a woken core with "+
			"cold caches. %d per row, after the same warm-up; allocations not counted.\n", d.IdleN)
	}
	if r.Dispatch != nil && r.Dispatch.Slow != nil {
		c := r.Dispatch.Slow
		m.printf("- \"on E-core CPU %d\": the thread pinned to CPU %d, of the slowest efficiency class (%d), instead.\n",
			c.Index, c.Index, c.EfficiencyClass)
	}
	m.printf("- tap and layer do the same work in the timed step (the layer lookup is in setup): the gap between their " +
		"rows is run-to-run noise.\n")
	m.printf("- p99.9 is shown for N ≥ %d only; with fewer samples it is one of the last few.\n", minP999)
	m.printf("- GCs are counted over the row's own timed pass. Rows taken from parts of the cold activation pass have " +
		"none of their own: the pass's count is on the \"activation cold\" row.\n")
	if r.Watchdog != nil {
		r.Watchdog.writeMarkdown(m)
	}
	if r.TLA != "" {
		m.printf("\n### Top-level await\n\n```\n%s\n```\n", r.TLA)
	}
	m.printf("\n### QueryThreadCycleTime calibration\n\n")
	switch {
	case r.Cycles != nil:
		if m.err == nil {
			m.err = r.Cycles.WriteMarkdown(w)
		}
	case r.CyclesErr != nil:
		m.printf("Not measured: %v\n", r.CyclesErr)
	default:
		m.printf("Skipped.\n")
	}
	m.printf("\nTotal run time: %s.\n", fmtDuration(r.Elapsed))
	return m.err
}

func (rep *WatchdogReport) writeMarkdown(m *mdWriter) {
	o := rep.Options
	m.printf("\n### Watchdog (SCRIPTING §4.1)\n\n")
	m.printf("- Clock: %s. Poll every %s requested, %s achieved on average.\n", rep.Clock, o.Watchdog.Poll, fmtDuration(rep.Poll))
	var over, lat, stop, call []time.Duration
	clocks := map[string]int{}
	within := true
	for _, l := range rep.Loops {
		over = append(over, l.Overshoot)
		lat = append(lat, l.Latency)
		stop = append(stop, l.Overshoot+l.Latency)
		call = append(call, l.Call)
		clocks[l.Reason.Clock]++
		within = within && l.Overshoot+l.Latency <= o.Watchdog.Poll
	}
	if len(rep.Loops) > 0 {
		m.printf("- `while(true){}` under a %s CPU budget, %d runs, all interrupted (budget that fired: %s). Min / median / max: "+
			"seen over budget by %s of CPU time; Interrupt to return %s of wall time; CPU overshoot plus wall latency %s; "+
			"whole call %s. That sum stayed within one %s poll interval in every run: %s. "+
			"The next call worked after each, with only End's ClearInterrupt in between: %s.\n",
			o.Watchdog.Budget, len(rep.Loops), clockList(clocks), mmm(over), mmm(lat), mmm(stop), mmm(call),
			o.Watchdog.Poll, yesNo(within), yesNo(rep.NextCallOK))
	}
	m.printf("- Interrupt while no call runs: the next call fails at once: %s (`%s`). goja clears the flag when that "+
		"interrupt ends the call, so the call after it works: %s. ClearInterrupt before the call prevents the failure: %s.\n",
		yesNo(rep.StaleFails), oneLine(rep.StaleErr), yesNo(rep.StaleClears), yesNo(rep.ClearWorks))
	n := rep.Native
	m.printf("- Interrupt %s into a native Go function that sleeps %s: the call returned after %s, %s after the Interrupt, "+
		"with an InterruptedError: %s; the statement after the native call ran: %s.\n",
		o.NativeInterrupt, o.NativeSleep, fmtDuration(n.Call), fmtDuration(n.Delay), yesNo(n.Interrupted), yesNo(n.Continued))
	b := rep.Blocked
	if b.Reason != nil {
		m.printf("- Watchdog with a %s CPU budget and a %s wall backstop, call sleeping %s in a native function: fired on the %s "+
			"clock at %s (the CPU budget never saw the sleep); the call returned %s after the Interrupt, %s after it began.\n",
			o.Blocked.Budget, o.Blocked.Wall, o.BlockedSleep, b.Reason.Clock, fmtDuration(b.Reason.Used), fmtDuration(b.Latency), fmtDuration(b.Call))
	} else {
		m.printf("- Watchdog with a %s CPU budget and a %s wall backstop, call sleeping %s in a native function: did not fire; "+
			"the call took %s.\n", o.Blocked.Budget, o.Blocked.Wall, o.BlockedSleep, fmtDuration(b.Call))
	}
	s := rep.Stack
	m.printf("- SetMaxCallStackSize(%d), unbounded recursion inside try/catch/finally: depth %d reached; the Go caller got a "+
		"*goja.StackOverflowError: %s, Error() = `%s`; the script's catch ran: %s; its finally ran: %s; the runtime ran the next call: %s.\n",
		MaxCallStackSize, s.Depth, yesNo(s.Overflow), oneLine(s.Err), yesNo(s.Caught), yesNo(s.Finally), yesNo(s.Reusable))
}

// mmm formats the minimum, median and maximum of ds.
func mmm(ds []time.Duration) string {
	s := summarizeDurations(ds)
	return fmtDuration(s[0]) + " / " + fmtDuration(s[1]) + " / " + fmtDuration(s[2])
}

func clockList(clocks map[string]int) string {
	var parts []string
	for _, c := range []string{"cpu", "wall"} {
		if clocks[c] > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", c, clocks[c]))
		}
	}
	return strings.Join(parts, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// oneLine keeps an error message on one Markdown line.
func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ⏎ ")
}
