package spike

import (
	"bytes"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// benchProgram is the compiled benchmark script, built once per test
// binary.
var benchProgram = sync.OnceValues(func() (*goja.Program, error) {
	s, err := NewScript()
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	res, err := s.Build()
	if err != nil {
		return nil, err
	}
	return CompileBundle(res)
})

func program(tb testing.TB) *goja.Program {
	tb.Helper()
	p, err := benchProgram()
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

func activate(t *testing.T) *Host {
	t.Helper()
	h, err := Activate(program(t))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func handler(t *testing.T, h *Host, control, gesture, layer string) goja.Callable {
	t.Helper()
	fn, err := h.Handler(control, gesture, layer)
	if err != nil {
		t.Fatal(err)
	}
	return fn
}

func TestRegistrations(t *testing.T) {
	h := activate(t)
	type reg struct{ control, gesture, label, layer string }
	var got []reg
	for _, r := range h.Registrations {
		got = append(got, reg{r.Control, r.Gesture, r.Label, r.Layer})
	}
	// Module order: async, gestures, stick, then main's own on().
	want := []reg{
		{"north", "tap", "", ""},
		{"south", "press", "Undo", ""},
		{"back", "tap", "Layers panel", ""},
		{"back", "doubleTap", "Alt layer", ""},
		{"west", "tap", "Pen", ""},
		{"west", "hold", "Eraser while held", ""},
		{"rightshoulder", "press", "", ""},
		{"south", "press", "Redo", "alt"},
		{"rs", "stick", "", ""},
		{"misc1", "press", "", ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("registrations:\n got %v\nwant %v", got, want)
	}
}

func TestHandlersRecord(t *testing.T) {
	h := activate(t)
	for _, tc := range []struct {
		control, gesture, layer string
		taps, holds, layers     []string
	}{
		{control: "south", gesture: "press", taps: []string{"Ctrl+Z"}},
		{control: "south", gesture: "press", layer: "alt", taps: []string{"F15"}},
		{control: "back", gesture: "tap", taps: []string{"F19"}},
		{control: "back", gesture: "doubleTap"},
		{control: "west", gesture: "tap", taps: []string{"F13"}},
		{control: "west", gesture: "hold", holds: []string{"F14"}},
		{control: "rightshoulder", gesture: "press", layers: []string{"alt"}},
		{control: "misc1", gesture: "press"},
	} {
		h.Reset()
		fn := handler(t, h, tc.control, tc.gesture, tc.layer)
		ev := h.NewEvent(eventFor(tc.control, tc.gesture, Event{}, 1))
		if err := h.Dispatch(fn, ev); err != nil {
			t.Fatalf("%s %s: %v", tc.control, tc.gesture, err)
		}
		if !slices.Equal(h.Taps, tc.taps) || !slices.Equal(h.Holds, tc.holds) || !slices.Equal(h.HeldLayers, tc.layers) {
			t.Errorf("%s %s layer %q: taps %q holds %q layers %q, want %q %q %q", tc.control, tc.gesture, tc.layer,
				h.Taps, h.Holds, h.HeldLayers, tc.taps, tc.holds, tc.layers)
		}
	}
}

func TestEventFields(t *testing.T) {
	h := activate(t)
	fn, err := h.VM.RunString(`(e) => [e.control, e.gesture, e.time, e.duration, e.count, e.scope, e.cycle, e.held,
		typeof e.pass, Object.keys(e).join()].join("|")`)
	if err != nil {
		t.Fatal(err)
	}
	call, _ := goja.AssertFunction(fn)
	v, err := call(goja.Undefined(), h.NewEvent(Event{
		Control: "west", Gesture: "hold", Scope: "layer:alt", Time: 12.5, Duration: 300, Count: 2, Cycle: 7, Held: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "west|hold|12.5|300|2|layer:alt|7|true|function|control,gesture,time,duration,count,scope,cycle,held"
	if v.String() != want {
		t.Errorf("event = %s, want %s", v, want)
	}
}

func TestStickHandler(t *testing.T) {
	h := activate(t)
	fn := handler(t, h, "rs", "stick", "")
	stickN := func(x, y float64, n int) []string {
		t.Helper()
		h.Reset()
		for range n {
			if err := h.Dispatch(fn, h.NewStickEvent("rs", x, y, 8)); err != nil {
				t.Fatal(err)
			}
		}
		return slices.Clone(h.Taps)
	}
	// Full tilt is 20 Hz: 0.16 taps per 8 ms sample, so 100 samples make
	// 16 taps.
	if got := stickN(0, 1, 100); len(got) != 16 || slices.ContainsFunc(got, func(s string) bool { return s != "F17" }) {
		t.Errorf("up: taps %q, want 16 × F17", got)
	}
	if got := stickN(0, -1, 100); len(got) != 16 || slices.ContainsFunc(got, func(s string) bool { return s != "F18" }) {
		t.Errorf("down: taps %q, want 16 × F18", got)
	}
	if got := stickN(1, 0, 100); len(got) != 0 {
		t.Errorf("right: taps %q, want none", got)
	}
	if got := stickN(0, 0.1, 100); len(got) != 0 {
		t.Errorf("deadzone: taps %q, want none", got)
	}
	// dt is cut to two ticks (16 ms): 3 Hz at the deadzone edge needs 21
	// samples however long they are.
	if got := stickN(0, 0.15, 20); len(got) != 0 {
		t.Errorf("slow: %d taps after 20 samples, want 0", len(got))
	}
}

func TestAsyncContinuation(t *testing.T) {
	h := activate(t)
	fn := handler(t, h, "north", "tap", "")
	for i := range 3 {
		h.Reset()
		if err := h.Dispatch(fn, h.NewEvent(eventFor("north", "tap", Event{}, i))); err != nil {
			t.Fatal(err)
		}
		// The continuation after await key.tap is a microtask; it ran inside
		// Dispatch and stopped at the sleep.
		if !slices.Equal(h.Taps, []string{"F20"}) || h.PendingSleeps() != 1 {
			t.Fatalf("after the call: taps %q, %d pending sleeps; want [F20], 1", h.Taps, h.PendingSleeps())
		}
		if err := h.ResumeSleeps(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(h.Taps, []string{"F20", "F21"}) || h.PendingSleeps() != 0 {
			t.Fatalf("after the resume: taps %q, %d pending sleeps; want [F20 F21], 0", h.Taps, h.PendingSleeps())
		}
	}
}

func TestFixedSources(t *testing.T) {
	const js = `Date.now() + "|" + new Date().toISOString() + "|" + Math.random() + "|" + Math.random()`
	var got []string
	for range 2 {
		v, err := NewHost().VM.RunString(js)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v.String())
	}
	if got[0] != got[1] {
		t.Errorf("two hosts differ: %s and %s", got[0], got[1])
	}
	if want := "1767225600000|2026-01-01T00:00:00.000Z|"; !strings.HasPrefix(got[0], want) {
		t.Errorf("got %s, want prefix %s", got[0], want)
	}
}

func TestOnRejectsNonFunction(t *testing.T) {
	h := NewHost()
	_, err := h.VM.RunString(`__padmate.on("south", "press", 42)`)
	if err == nil || !strings.Contains(err.Error(), "handler is not a function") {
		t.Errorf("err = %v, want a TypeError about the handler", err)
	}
}

// quickWatchdog keeps the watchdog checks short.
var quickWatchdog = WatchdogCheckOptions{
	Watchdog:        WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 10 * time.Millisecond, Wall: 2 * time.Second},
	Loops:           2,
	NativeSleep:     40 * time.Millisecond,
	NativeInterrupt: 5 * time.Millisecond,
	Blocked:         WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 5 * time.Millisecond, Wall: 20 * time.Millisecond},
	BlockedSleep:    60 * time.Millisecond,
}

func TestWatchdog(t *testing.T) {
	rep, err := CheckWatchdog(quickWatchdog)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Loops) != quickWatchdog.Loops || !rep.NextCallOK {
		t.Errorf("while(true){}: %d interrupted runs, next call OK %v; want %d, true", len(rep.Loops), rep.NextCallOK, quickWatchdog.Loops)
	}
	for _, l := range rep.Loops {
		if l.Reason.Clock != "cpu" || l.Reason.Used < l.Reason.Budget {
			t.Errorf("while(true){} interrupted by %+v, want the cpu budget, used past it", l.Reason)
		}
	}
	if !rep.StaleFails || !rep.StaleClears || !rep.ClearWorks {
		t.Errorf("idle interrupt: fails %v, cleared by goja %v, ClearInterrupt works %v; want all true",
			rep.StaleFails, rep.StaleClears, rep.ClearWorks)
	}
	// Interrupt does not reach into a Go native: the call runs the whole
	// sleep and ends at the next JavaScript instruction.
	n := rep.Native
	if !n.Interrupted || n.Continued || n.Call < quickWatchdog.NativeSleep-time.Millisecond {
		t.Errorf("native sleep: %+v; want interrupted, not continued, the whole %s", n, quickWatchdog.NativeSleep)
	}
	b := rep.Blocked
	if b.Reason == nil || b.Call < quickWatchdog.BlockedSleep-time.Millisecond {
		t.Errorf("blocked: %+v; want a firing and the whole %s", b, quickWatchdog.BlockedSleep)
	} else if runtime.GOOS == "windows" && b.Reason.Clock != "wall" {
		// Elsewhere the thread clock is the wall time, so the CPU budget
		// fires first.
		t.Errorf("blocked: fired on the %s clock, want wall: a sleep uses no CPU time", b.Reason.Clock)
	}
	// goja's StackOverflowError is uncatchable: the script's catch and
	// finally do not run, and the Go caller gets the error.
	s := rep.Stack
	if !s.Overflow || s.Caught || s.Finally || !s.Reusable || s.Depth < MaxCallStackSize-10 || s.Depth > MaxCallStackSize+1 {
		t.Errorf("stack: %+v; want an uncaught overflow near depth %d and a reusable runtime", s, MaxCallStackSize)
	}
}

func TestLatencyOf(t *testing.T) {
	ticks := make([]int64, 1000)
	for i := range ticks {
		ticks[i] = int64(1000 - i) // unsorted
	}
	got := latencyOf(ticks)
	want := Latency{N: 1000, P50: stampDuration(500), P90: stampDuration(900), P99: stampDuration(990),
		P999: stampDuration(999), Max: stampDuration(1000)}
	if got != want {
		t.Errorf("latencyOf = %+v, want %+v", got, want)
	}
	if got := latencyOf(nil); got != (Latency{}) {
		t.Errorf("latencyOf(nil) = %+v", got)
	}
}

func TestRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the script several times")
	}
	rep, err := Run(Options{Dispatch: DispatchOptions{Warmup: 10, N: 200, AllocN: 10, IdleN: 3}, Runs: 3, Watchdog: quickWatchdog})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Dispatch
	if (d.Fast != nil) != (runtime.GOOS == "windows") || d.Fast != nil && d.Slow != nil && d.Slow.EfficiencyClass >= d.Fast.EfficiencyClass {
		t.Errorf("pinned to %+v, E-core %+v", d.Fast, d.Slow)
	}
	var want []string
	for _, s := range Scenarios {
		want = append(want, s.Name)
	}
	for _, x := range ExtraRuns {
		if !x.Slow || d.Slow != nil {
			want = append(want, x.Scenario+" "+x.label(d.Slow))
		}
	}
	var got []string
	for _, r := range d.Results {
		name := strings.TrimSpace(r.Scenario.Name + " " + r.Label)
		got = append(got, name)
		n, allocs := 200, true
		if strings.Contains(r.Label, "idle") {
			n, allocs = 3, false
		}
		if r.Latency.N != n || r.Latency.P50 <= 0 || r.Latency.Max < r.Latency.P999 || (r.AllocsPerOp >= 0) != allocs {
			t.Errorf("%s: latency %+v, %.1f allocs/op", name, r.Latency, r.AllocsPerOp)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("dispatch runs %q, want %q", got, want)
	}
	if a := rep.Activation; a.Registrations != 10 || a.Size <= a.SourceMap || a.SourceMap == 0 || a.Warm.N != 3 {
		t.Errorf("activation: %+v", a)
	}
	if !strings.Contains(rep.TLA, "top-level await is not allowed") {
		t.Errorf("TLA diagnostic = %q", rep.TLA)
	}
	var buf bytes.Buffer
	if err := rep.WriteMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"| dispatch tap |", "| dispatch async-resume |", "| activation warm: cached Program → Runtime + RunProgram | 3 |",
		"| esbuild build, cold: first in process | 1 |", "### Watchdog", "### Top-level await", "Skipped.", "- goja: github.com/dop251/goja v",
		"| dispatch tap after 100ms idle | 3 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, out)
		}
	}
	// With 3 samples p99.9 is not shown, nor GCs on a row taken from a
	// part of the cold activation pass.
	for _, metric := range []string{"dispatch tap after 100ms idle", "esbuild build, warm: every build after the first"} {
		cols := tableRow(out, metric)
		if len(cols) < 10 || cols[5] != "–" || (cols[9] == "–") != strings.HasPrefix(metric, "esbuild") {
			t.Errorf("row %q: %q", metric, cols)
		}
	}
}

// tableRow returns the cells of the results table row for metric.
func tableRow(md, metric string) []string {
	for line := range strings.SplitSeq(md, "\n") {
		if cells := strings.Split(strings.Trim(line, "| "), " | "); len(cells) > 1 && cells[0] == metric {
			return cells
		}
	}
	return nil
}

func TestRunMarkdownFails(t *testing.T) {
	rep := &Report{Options: DefaultOptions()}
	errWrite := errors.New("write failed")
	if err := rep.WriteMarkdown(failWriter{errWrite}); !errors.Is(err, errWrite) {
		t.Errorf("WriteMarkdown to a failing writer = %v, want %v", err, errWrite)
	}
}
