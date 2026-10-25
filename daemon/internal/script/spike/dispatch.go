package spike

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Latency is a distribution of durations, by nearest rank.
type Latency struct {
	N                        int
	P50, P90, P99, P999, Max time.Duration
}

// latencyOf summarizes stamp differences.
func latencyOf(ticks []int64) Latency {
	if len(ticks) == 0 {
		return Latency{}
	}
	s := slices.Clone(ticks)
	slices.Sort(s)
	rank := func(p float64) time.Duration {
		i := max(int(math.Ceil(p*float64(len(s))))-1, 0)
		return stampDuration(s[i])
	}
	return Latency{
		N: len(s), P50: rank(0.50), P90: rank(0.90), P99: rank(0.99), P999: rank(0.999),
		Max: stampDuration(s[len(s)-1]),
	}
}

// DispatchOptions say how many dispatches MeasureDispatch makes per
// scenario.
type DispatchOptions struct {
	// Warmup dispatches run first and are not measured.
	Warmup int
	// N dispatches are timed one by one.
	N int
	// AllocN dispatches are counted for allocations, in a pass of their
	// own, since reading runtime.MemStats around each one stops the world.
	AllocN int
	// IdleN dispatches are timed in each ExtraRuns entry with a gap, after
	// the same warm-up; 0 skips those runs.
	IdleN int
}

// DefaultDispatch is the SPK-03 run: 100k timed dispatches, and 100
// after each idle gap.
func DefaultDispatch() DispatchOptions {
	return DispatchOptions{Warmup: 10000, N: 100000, AllocN: 2000, IdleN: 100}
}

// CPU is a logical processor a measurement thread was pinned to.
type CPU struct {
	Index           uint32
	EfficiencyClass uint8
}

// DispatchReport is what MeasureDispatch measured.
type DispatchReport struct {
	// Fast is the processor of the fastest efficiency class that every run
	// but the E-core ones was pinned to; Slow, of the slowest class, is the
	// one for those. Fast is nil when the thread was not pinned (off
	// Windows), Slow when all processors are in one class.
	Fast, Slow *CPU
	// Results are the Scenarios in order, then the ExtraRuns that ran.
	Results []DispatchResult
}

// DispatchResult is one run's numbers.
type DispatchResult struct {
	Scenario *Scenario
	// Label says how the run differs from the main one of its scenario;
	// "" for the main runs.
	Label   string
	Latency Latency
	// AllocsPerOp and BytesPerOp count heap allocations in the timed step;
	// negative when not counted.
	AllocsPerOp, BytesPerOp float64
	// GCs is the number of GC cycles during the timed pass.
	GCs uint32
}

// ExtraRun is a scenario timed again after the main runs: after an idle
// gap before each timed dispatch, on the slowest efficiency class, or
// both.
type ExtraRun struct {
	Scenario string
	Gap      time.Duration
	Slow     bool
}

// ExtraRuns show what the hot loop of the main runs hides. The slot loop
// parks between events, so a dispatch meets a woken core and cold caches:
// stick samples come every few ms, button presses hundreds of ms apart;
// noop after the gap is the host's share of that. A background daemon may
// also run on an E-core.
var ExtraRuns = []ExtraRun{
	{Scenario: "tap", Slow: true},
	{Scenario: "stick", Gap: 8 * time.Millisecond},
	{Scenario: "tap", Gap: 8 * time.Millisecond},
	{Scenario: "noop", Gap: 100 * time.Millisecond},
	{Scenario: "tap", Gap: 100 * time.Millisecond},
	{Scenario: "tap", Gap: 100 * time.Millisecond, Slow: true},
}

// label is the DispatchResult label of x, run on cpu.
func (x ExtraRun) label(cpu *CPU) string {
	var parts []string
	if x.Gap > 0 {
		parts = append(parts, "after "+x.Gap.String()+" idle")
	}
	if x.Slow {
		parts = append(parts, fmt.Sprintf("on E-core CPU %d", cpu.Index))
	}
	return strings.Join(parts, " ")
}

// A Scenario is one kind of dispatch: a handler of the benchmark script
// and the event it gets.
type Scenario struct {
	Name, Desc string
	setup      func(h *Host) ([]step, func() error, error)
}

// step is one operation of a scenario iteration. Only the timed step is
// measured; the others keep the script in a steady state.
type step struct {
	timed bool
	run   func(i int) error
}

// Scenarios are the dispatches the benchmark times, in report order.
var Scenarios = []*Scenario{
	{Name: "noop", Desc: "empty handler (misc1 press): host overhead only", setup: gesture("misc1", "press", "", Event{})},
	{Name: "tap", Desc: `south press → key.tap("Ctrl+Z")`, setup: gesture("south", "press", "", Event{})},
	{Name: "holdKeys", Desc: `west hold → e.holdKeys("F14")`, setup: gesture("west", "hold", "", Event{Duration: 300})},
	{Name: "layer", Desc: `alt layer: south press → key.tap("F15")`, setup: gesture("south", "press", "alt", Event{Scope: "layer:alt"})},
	{Name: "stick", Desc: "rs stick sample → direction, remap, RateAccumulator, sometimes key.tap (§8.1)", setup: stick},
	{Name: "async-call", Desc: "north tap → await key.tap, then pending at await sleep", setup: async(true)},
	{Name: "async-resume", Desc: "host resolves the sleep → continuation runs key.tap", setup: async(false)},
	{Name: "tap+watchdog", Desc: "tap inside Watchdog Begin/End, watchdog polling every 2 ms", setup: watched},
}

// ScenarioByName returns the scenario called name, or nil.
func ScenarioByName(name string) *Scenario {
	for _, s := range Scenarios {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// eventFor fills in the fields every gesture event has.
func eventFor(control, gesture string, base Event, i int) Event {
	e := base
	e.Control, e.Gesture = control, gesture
	if e.Scope == "" {
		e.Scope = "base"
	}
	e.Count = 1
	e.Cycle = int64(i)
	e.Time = float64(i) * 8
	e.Held = gesture == "press" || gesture == "hold"
	return e
}

func gesture(control, gesture, layer string, base Event) func(h *Host) ([]step, func() error, error) {
	return func(h *Host) ([]step, func() error, error) {
		fn, err := h.Handler(control, gesture, layer)
		if err != nil {
			return nil, nil, err
		}
		run := func(i int) error {
			return h.Dispatch(fn, h.NewEvent(eventFor(control, gesture, base, i)))
		}
		return []step{{timed: true, run: run}}, nil, nil
	}
}

// stickPath is a sweep of stick positions: all directions, from inside the
// deadzone to full tilt.
var stickPath = func() [][2]float64 {
	var p [][2]float64
	for k := range 64 {
		a := float64(k) * 2 * math.Pi / 64
		m := []float64{0.1, 0.4, 0.7, 1.0}[k%4]
		p = append(p, [2]float64{m * math.Sin(a), m * math.Cos(a)})
	}
	return p
}()

func stick(h *Host) ([]step, func() error, error) {
	fn, err := h.Handler("rs", "stick", "")
	if err != nil {
		return nil, nil, err
	}
	run := func(i int) error {
		p := stickPath[i%len(stickPath)]
		return h.Dispatch(fn, h.NewStickEvent("rs", p[0], p[1], 8))
	}
	return []step{{timed: true, run: run}}, nil, nil
}

// async times either the call of the async handler, which returns at
// await sleep, or the host resolving that sleep.
func async(timeCall bool) func(h *Host) ([]step, func() error, error) {
	return func(h *Host) ([]step, func() error, error) {
		fn, err := h.Handler("north", "tap", "")
		if err != nil {
			return nil, nil, err
		}
		call := func(i int) error {
			if err := h.Dispatch(fn, h.NewEvent(eventFor("north", "tap", Event{}, i))); err != nil {
				return err
			}
			if h.PendingSleeps() != 1 {
				return fmt.Errorf("async handler: %d pending sleeps after the call, want 1", h.PendingSleeps())
			}
			return nil
		}
		resume := func(int) error { return h.ResumeSleeps() }
		return []step{{timed: timeCall, run: call}, {timed: !timeCall, run: resume}}, nil, nil
	}
}

func watched(h *Host) ([]step, func() error, error) {
	fn, err := h.Handler("south", "press", "")
	if err != nil {
		return nil, nil, err
	}
	w, err := StartWatchdog(h.VM, HandlerWatchdog)
	if err != nil {
		return nil, nil, err
	}
	run := func(i int) error {
		if err := w.Begin(); err != nil {
			return err
		}
		err := h.Dispatch(fn, h.NewEvent(eventFor("south", "press", Event{}, i)))
		if f := w.End(); f != nil {
			return fmt.Errorf("watchdog fired: %w", f.Reason)
		}
		return err
	}
	return []step{{timed: true, run: run}}, w.Stop, nil
}

// prepare activates p and sets s up. Call cleanup when done, on the same
// goroutine, which must be locked to its thread for tap+watchdog.
func (s *Scenario) prepare(p *goja.Program) (h *Host, steps []step, cleanup func() error, err error) {
	h, err = Activate(p)
	if err != nil {
		return nil, nil, nil, err
	}
	steps, cleanup, err = s.setup(h)
	if err != nil {
		return nil, nil, nil, err
	}
	if cleanup == nil {
		cleanup = func() error { return nil }
	}
	return h, steps, cleanup, nil
}

// MeasureDispatch times every scenario, then the ExtraRuns, on a new
// locked OS thread, as the slot loop would run. On Windows the thread is
// pinned to one processor of the fastest efficiency class, so that runs
// are comparable on a hybrid CPU.
func MeasureDispatch(p *goja.Program, opts DispatchOptions) (*DispatchReport, error) {
	type result struct {
		r   *DispatchReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		// Never unlocked: the Go runtime destroys the thread when the
		// goroutine exits, so its pinned affinity reaches no other
		// goroutine.
		runtime.LockOSThread()
		r, err := measureDispatch(p, opts)
		done <- result{r, err}
	}()
	res := <-done
	return res.r, res.err
}

func measureDispatch(p *goja.Program, opts DispatchOptions) (*DispatchReport, error) {
	fast, slow, err := dispatchCPUs()
	if err != nil {
		return nil, err
	}
	rep := &DispatchReport{}
	if rep.Fast, err = pinFirst(fast); err != nil {
		return nil, err
	}
	for _, s := range Scenarios {
		r, err := measureScenario(p, s, opts, 0)
		if err != nil {
			return nil, fmt.Errorf("spike: dispatch %s: %w", s.Name, err)
		}
		rep.Results = append(rep.Results, r)
	}
	if rep.Fast != nil {
		if rep.Slow, err = pinFirst(slow); err != nil {
			return nil, err
		}
	}
	for _, x := range ExtraRuns {
		cpu := rep.Fast
		if x.Slow {
			cpu = rep.Slow
		}
		if x.Slow && cpu == nil || x.Gap > 0 && opts.IdleN == 0 {
			continue
		}
		s := ScenarioByName(x.Scenario)
		if s == nil {
			return nil, fmt.Errorf("spike: extra dispatch run of unknown scenario %q", x.Scenario)
		}
		if cpu != nil {
			if err := pinThread(*cpu); err != nil {
				return nil, err
			}
		}
		o := opts
		if x.Gap > 0 {
			o = DispatchOptions{Warmup: opts.Warmup, N: opts.IdleN}
		}
		r, err := measureScenario(p, s, o, x.Gap)
		r.Label = x.label(cpu)
		if err != nil {
			return nil, fmt.Errorf("spike: dispatch %s %s: %w", s.Name, r.Label, err)
		}
		rep.Results = append(rep.Results, r)
	}
	return rep, nil
}

// pinFirst pins the calling thread to the first of cpus the process may
// run on and returns it; nil, without pinning, when there is none.
func pinFirst(cpus []CPU) (*CPU, error) {
	for _, c := range cpus {
		err := pinThread(c)
		if err == nil {
			return &c, nil
		}
		if !outsideAffinity(err) {
			return nil, err
		}
	}
	return nil, nil
}

// iterations runs a scenario's steps. What it does with the timed step
// depends on the pass: nothing extra (warm-up), count its allocations or
// time it, after sleeping gap when gap is not 0.
type iterations struct {
	h     *Host
	steps []step
	next  int

	samples                 []int64
	gap                     time.Duration
	countAllocs             bool
	allocs, bytes           uint64
	beforeStats, afterStats runtime.MemStats
}

func (it *iterations) run(n int) error {
	for k := range n {
		for _, st := range it.steps {
			var err error
			switch {
			case !st.timed:
				err = st.run(it.next)
			case it.samples != nil:
				if it.gap > 0 {
					// The thread parks, as the slot loop does between
					// events; the stopwatch starts once it is awake.
					time.Sleep(it.gap)
				}
				t0 := stamp()
				err = st.run(it.next)
				it.samples[k] = stamp() - t0
			case it.countAllocs:
				runtime.ReadMemStats(&it.beforeStats)
				err = st.run(it.next)
				runtime.ReadMemStats(&it.afterStats)
				it.allocs += it.afterStats.Mallocs - it.beforeStats.Mallocs
				it.bytes += it.afterStats.TotalAlloc - it.beforeStats.TotalAlloc
			default:
				err = st.run(it.next)
			}
			if err != nil {
				return err
			}
		}
		it.h.Reset()
		it.next++
	}
	return nil
}

// measureScenario runs s: opts.Warmup dispatches, opts.AllocN counted for
// allocations and opts.N timed, each of those after sleeping gap.
func measureScenario(p *goja.Program, s *Scenario, opts DispatchOptions, gap time.Duration) (r DispatchResult, err error) {
	r.Scenario = s
	r.AllocsPerOp, r.BytesPerOp = -1, -1
	h, steps, cleanup, err := s.prepare(p)
	if err != nil {
		return r, err
	}
	defer func() {
		if cerr := cleanup(); err == nil {
			err = cerr
		}
	}()
	it := &iterations{h: h, steps: steps}
	if err := it.run(opts.Warmup); err != nil {
		return r, err
	}

	it.countAllocs = true
	if err := it.run(opts.AllocN); err != nil {
		return r, err
	}
	it.countAllocs = false
	if opts.AllocN > 0 {
		r.AllocsPerOp = float64(it.allocs) / float64(opts.AllocN)
		r.BytesPerOp = float64(it.bytes) / float64(opts.AllocN)
	}

	it.samples, it.gap = make([]int64, opts.N), gap
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	err = it.run(opts.N)
	runtime.ReadMemStats(&after)
	if err != nil {
		return r, err
	}
	r.Latency = latencyOf(it.samples)
	r.GCs = after.NumGC - before.NumGC
	return r, nil
}
