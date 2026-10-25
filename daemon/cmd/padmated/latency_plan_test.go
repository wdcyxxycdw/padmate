package main

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// benchRow is one row of TestLatencyLive (ENG-11): n edges, each a press
// or a release, with gap of idle before each.
type benchRow struct {
	name  string
	gap   time.Duration
	n     int  // edges, a multiple of 4; of 8 for row H, so that each pad ends up
	noop  bool // the no-op sender: the pipeline without SendInput
	gated bool // p99 < benchTarget, with at least minN samples
	minN  int  // n at scale 1 for gated rows, else 0
	pads  int  // gamepads: 1; 4 for row H, which presses one control on each in turn
	// macros measures with benchMacroProfile, a profile that also binds a
	// macro (SCR-50), on an unpressed control: the hold bindings beside a
	// runtime that only runs macros.
	macros bool
}

// The plan of TestLatencyLive: the gate, the warm-up and the time model of
// the estimate.
const (
	benchTarget = latency.WarnP99 // the p99 of each gated row (ARCHITECTURE §13), also the product threshold of DMN-15
	// benchWarmup edges at benchWarmupGap come before row A, and
	// benchWarmupSteps steps of row H's four pads before row H. They are
	// not scaled, and their samples are dropped.
	benchWarmup      = 52
	benchWarmupSteps = 8
	benchWarmupGap   = 5 * time.Millisecond
	// benchSetup is the estimated time of the start (SDL, the hook, the
	// first pad and the calibrations), benchPadsSetup that of attaching
	// row H's other pads, and benchEdgeCost that of one edge beyond its
	// gap: T-input's pickup of the command (0–2 ms), the pipeline and the
	// fence.
	benchSetup     = 5 * time.Second
	benchPadsSetup = 700 * time.Millisecond
	benchEdgeCost  = 1500 * time.Microsecond
	// benchProfileSwitch is the estimated time of row M's switches to the
	// macro profile, whose runtimes build, and back.
	benchProfileSwitch = time.Second
)

// benchRows0 is the plan at scale 1 (ARCHITECTURE §14, TestLatencyLive):
// each n is N₀.
var benchRows0 = []benchRow{
	{name: "A", gap: 5 * time.Millisecond, n: 2000, gated: true, pads: 1},  // busy use: chords, fast switching
	{name: "B", gap: 50 * time.Millisecond, n: 1000, gated: true, pads: 1}, // normal use
	{name: "C", gap: 250 * time.Millisecond, n: 200, gated: true, pads: 1}, // sparse use, threads parked
	{name: "D", gap: time.Second, n: 32, pads: 1},                          // long idle and C-states; info
	{name: "E", gap: 50 * time.Millisecond, n: 300, noop: true, pads: 1},   // without SendInput; info
	{name: "M", gap: 50 * time.Millisecond, n: 300, macros: true, pads: 1}, // beside a macro runtime, as B; info
	{name: "H", gap: 50 * time.Millisecond, n: 400, pads: benchPadsMax},    // four pads at once; info
}

// benchPadsMax is the gamepads of row H: the supervisor's slots (IN-08).
const benchPadsMax = 4

// benchPlan returns the rows of TestLatencyLive for scale, which must be
// in latency.ParseScale's range (0, latency.MaxScale], and the estimated
// wall time of the whole test. Each n is latency.Scaled(N₀, scale), and
// row H's is rounded up to a multiple of 8, so that each of its four
// pads presses and releases in turn an even number of times and ends up.
// A gated row's minN stays its scale-1 count. multi adds row H, which
// needs several slots (IN-08).
func benchPlan(scale float64, multi bool) (rows []benchRow, estimate time.Duration, err error) {
	if !(scale > 0 && scale <= latency.MaxScale) { // also rejects NaN
		return nil, 0, fmt.Errorf("scale %v is not in (0, %d]", scale, latency.MaxScale)
	}
	for _, r := range benchRows0 {
		if r.pads > 1 && !multi {
			continue
		}
		n0 := r.n
		r.n = latency.Scaled(n0, scale)
		if r.pads > 1 {
			step := 2 * r.pads
			r.n = (r.n + step - 1) / step * step
		}
		if r.gated {
			r.minN = n0
		}
		rows = append(rows, r)
	}
	return rows, benchSetup + benchWarmup*(benchWarmupGap+benchEdgeCost) + benchEstimate(rows), nil
}

// benchEstimate returns the estimated time of rows: their gaps and
// benchEdgeCost per edge. A step of row H has one gap for its four
// edges, and a row H also attaches its pads and warms them up. Row M also
// switches the profile twice and warms up after the first switch.
func benchEstimate(rows []benchRow) time.Duration {
	var d time.Duration
	for _, r := range rows {
		if r.macros {
			d += benchProfileSwitch + benchWarmupSteps*(benchWarmupGap+benchEdgeCost)
		}
		if r.pads > 1 {
			perStep := time.Duration(r.pads) * benchEdgeCost
			d += benchPadsSetup + benchWarmupSteps*(benchWarmupGap+perStep) + time.Duration(r.n/r.pads)*(r.gap+perStep)
			continue
		}
		d += time.Duration(r.n) * (r.gap + benchEdgeCost)
	}
	return d
}

// TestBenchPlan checks the row arithmetic of TestLatencyLive (ARCHITECTURE §14)
// on every OS.
func TestBenchPlan(t *testing.T) {
	names := func(rows []benchRow) string {
		s := ""
		for _, r := range rows {
			s += r.name
		}
		return s
	}
	ns := func(rows []benchRow) []int {
		n := make([]int, len(rows))
		for i, r := range rows {
			n[i] = r.n
		}
		return n
	}
	// The gaps set what each row measures (C: threads parked, D: C-states),
	// and no scale changes them.
	gaps := map[string]time.Duration{
		"A": 5 * time.Millisecond, "B": 50 * time.Millisecond, "C": 250 * time.Millisecond,
		"D": time.Second, "E": 50 * time.Millisecond, "M": 50 * time.Millisecond, "H": 50 * time.Millisecond,
	}
	for _, tt := range []struct {
		scale float64
		multi bool
		names string
		ns    []int
	}{
		{scale: 1, names: "ABCDEM", ns: []int{2000, 1000, 200, 32, 300, 300}},
		{scale: 1, multi: true, names: "ABCDEMH", ns: []int{2000, 1000, 200, 32, 300, 300, 400}},
		// The CI smoke: each at least 4, and row H 8, two steps.
		{scale: 0.02, multi: true, names: "ABCDEMH", ns: []int{40, 20, 4, 4, 8, 8, 8}},
		{scale: 0.001, multi: true, names: "ABCDEMH", ns: []int{4, 4, 4, 4, 4, 4, 8}},
		// Row H: 20 edges would be 5 steps, so the pads would end down.
		{scale: 0.05, multi: true, names: "ABCDEMH", ns: []int{100, 52, 12, 4, 16, 16, 24}},
		// 5·32 = 160; 5·300 = 1500.
		{scale: 5, multi: true, names: "ABCDEMH", ns: []int{10000, 5000, 1000, 160, 1500, 1500, 2000}},
		{scale: 1.1, multi: true, names: "ABCDEMH", ns: []int{2200, 1100, 220, 36, 332, 332, 440}},
		{scale: latency.MaxScale, multi: true, names: "ABCDEMH", ns: []int{20000, 10000, 2000, 320, 3000, 3000, 4000}},
	} {
		t.Run(fmt.Sprintf("%g/%v", tt.scale, tt.multi), func(t *testing.T) {
			rows, est, err := benchPlan(tt.scale, tt.multi)
			if err != nil {
				t.Fatalf("benchPlan(%g, %v): %v", tt.scale, tt.multi, err)
			}
			if got := names(rows); got != tt.names {
				t.Fatalf("rows %s, want %s", got, tt.names)
			}
			if got := ns(rows); fmt.Sprint(got) != fmt.Sprint(tt.ns) {
				t.Errorf("n = %v, want %v", got, tt.ns)
			}
			for _, r := range rows {
				if r.n%4 != 0 || r.n < 4 {
					t.Errorf("row %s: n = %d, want a multiple of 4, at least 4", r.name, r.n)
				}
				if r.gap != gaps[r.name] {
					t.Errorf("row %s: gap %v, want %v", r.name, r.gap, gaps[r.name])
				}
				wantGated := r.name == "A" || r.name == "B" || r.name == "C"
				if r.gated != wantGated {
					t.Errorf("row %s: gated %v, want %v", r.name, r.gated, wantGated)
				}
				if want := map[string]int{"A": 2000, "B": 1000, "C": 200}[r.name]; r.minN != want {
					t.Errorf("row %s: minN = %d, want %d", r.name, r.minN, want)
				}
				if r.macros != (r.name == "M") {
					t.Errorf("row %s: macros %v", r.name, r.macros)
				}
				if r.noop != (r.name == "E") {
					t.Errorf("row %s: noop %v", r.name, r.noop)
				}
				if wantPads := map[bool]int{false: 1, true: benchPadsMax}[r.name == "H"]; r.pads != wantPads {
					t.Errorf("row %s: %d pads, want %d", r.name, r.pads, wantPads)
				}
				if r.pads > 1 && r.n%(2*r.pads) != 0 {
					t.Errorf("row %s: n = %d is not a multiple of %d: a pad would end down", r.name, r.n, 2*r.pads)
				}
			}
			if est <= benchSetup || est < benchEstimate(rows) {
				t.Errorf("estimate %v, want more than the setup %v and the rows' %v", est, benchSetup, benchEstimate(rows))
			}
			if tt.scale == 1 && (est < 150*time.Second || est > 210*time.Second) {
				t.Errorf("estimate at scale 1 = %v, want 2.5–3.5 min", est)
			}
			if tt.scale == latency.MaxScale && est >= 40*time.Minute {
				t.Errorf("estimate at scale %d = %v, want below 40 min (task bench's -timeout 45m)", latency.MaxScale, est)
			}
		})
	}
	for _, scale := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), latency.MaxScale + 0.5} {
		if rows, _, err := benchPlan(scale, true); err == nil {
			t.Errorf("benchPlan(%v) = %d rows, want an error", scale, len(rows))
		}
	}
	// The estimate of the rest shrinks row by row, down to nothing.
	rows, _, _ := benchPlan(1, true)
	for i := range rows {
		if a, b := benchEstimate(rows[i:]), benchEstimate(rows[i+1:]); b >= a {
			t.Errorf("benchEstimate from row %s = %v, from the next %v: want less", rows[i].name, a, b)
		}
	}
	if d := benchEstimate(nil); d != 0 {
		t.Errorf("benchEstimate(nil) = %v, want 0", d)
	}
}
