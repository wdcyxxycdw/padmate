package script

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// The escape interrupt prevents a broken watchdog from hanging the test binary.
func escapeStuckRuntime(t *testing.T, h *Host) {
	t.Helper()
	timer := time.AfterFunc(3*time.Second, func() { h.VM.Interrupt(errors.New("test escape interrupt")) })
	t.Cleanup(func() { timer.Stop(); h.VM.ClearInterrupt() })
}

func requireHandlerCPUBudget(t *testing.T, err error) {
	t.Helper()
	if !isBudgetFailure(err) {
		t.Fatalf("error = %v, want budget failure", err)
	}
	var budget *BudgetError
	if !errors.As(err, &budget) {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			budget, _ = interrupted.Value().(*BudgetError)
		}
	}
	if budget == nil || budget.Budget != HandlerWatchdog.Budget {
		t.Fatalf("budget = %+v, want %s handler budget", budget, HandlerWatchdog.Budget)
	}
	if runtime.GOOS == "windows" && budget.Clock != "cpu" {
		t.Fatalf("budget clock = %q, want cpu", budget.Clock)
	}
}

func TestHostTimerAndSleepContinuationUseWatchdog(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		start        func(*Host) error
	}{
		{"every", `__padmate.every(1, () => { __padmate.key.down("Z"); while (true) {} });`, nil},
		{"sleep continuation", `__padmate.on("south", "press", async () => { __padmate.key.down("Z"); await __padmate.sleep(1); while (true) {} });`, func(h *Host) error {
			return h.Dispatch(Event{Control: "south", Gesture: "press"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clock.NewFake()
			h := NewHost(c)
			h.EnableWatchdog()
			p, err := goja.Compile("timer.js", tc.source, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Activate(p); err != nil {
				t.Fatal(err)
			}
			if tc.start != nil {
				if err := tc.start(h); err != nil {
					t.Fatal(err)
				}
			}
			escapeStuckRuntime(t, h)
			c.Advance(time.Millisecond)
			err = h.RunDue()
			requireHandlerCPUBudget(t, err)
			if err := h.Dispose(); err != nil {
				t.Fatal(err)
			}
			out := h.Output()
			if len(out) != 2 || out[0].Up || !out[1].Up {
				t.Fatalf("output = %+v, want down then release", out)
			}
		})
	}
}

func TestLifecycleTimerInterruptionReleasesAndRestarts(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	builds := 0
	build := func(context.Context) (*goja.Program, error) {
		builds++
		return goja.Compile("timer.js", `__padmate.every(1, () => { __padmate.key.down("Z"); while (true) {} });
__padmate.on("south", "press", () => { __padmate.key.down("X"); });`, false)
	}
	loaded := <-l.Load(context.Background(), build)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	h := loaded.Host
	escapeStuckRuntime(t, h)
	c.Advance(time.Millisecond)
	requireHandlerCPUBudget(t, l.RunDue())
	if l.Current() != nil || l.State() != StateDisposed {
		t.Fatalf("state/current = %v/%v", l.State(), l.Current())
	}
	out := h.Output()
	if len(out) != 2 || !out[1].Up {
		t.Fatalf("output = %+v, want released Z", out)
	}
	// A retired runtime is polled before the later restart deadline.
	if d, ok := l.NextDeadline(); !ok || d != c.Now()+clock.Time(workerPollInterval) {
		t.Fatalf("restart deadline = %v, %v", d, ok)
	}
	c.Advance(time.Second)
	deadline := time.Now().Add(time.Second)
	for l.Current() == nil && time.Now().Before(deadline) {
		_ = l.RunDue()
		time.Sleep(time.Millisecond)
	}
	if l.Current() == nil || l.Current() == h || l.State() != StateRunning || builds != 2 {
		t.Fatalf("restart current/builds = %v/%d", l.Current(), builds)
	}
	next := l.Current()
	if _, err := l.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	if out := next.Output(); len(out) != 1 || out[0].Spec != "X" || out[0].Up {
		t.Fatalf("restarted runtime output = %+v, want X down", out)
	}
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	if out := next.Output(); len(out) != 2 || !out[1].Up || out[1].Spec != "X" {
		t.Fatalf("restarted runtime after Dispose = %+v, want X release", out)
	}
	c.Advance(time.Second)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if len(h.Output()) != 2 {
		t.Fatalf("disposed timer ran: %+v", h.Output())
	}
}

func TestLifecycleHandlerInterruptionReleasesAndRestarts(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	builds := 0
	build := func(context.Context) (*goja.Program, error) {
		builds++
		return goja.Compile("handler.js", `__padmate.on("south", "press", () => { __padmate.key.down("Z"); while (true) {} });
__padmate.on("east", "press", () => { __padmate.key.down("X"); });`, false)
	}
	loaded := <-l.Load(context.Background(), build)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	h := loaded.Host
	escapeStuckRuntime(t, h)
	_, err := l.Dispatch(Event{Control: "south", Gesture: "press"})
	requireHandlerCPUBudget(t, err)
	if l.Current() != nil || l.State() != StateDisposed {
		t.Fatalf("state/current = %v/%v", l.State(), l.Current())
	}
	out := h.Output()
	if len(out) != 2 || !out[1].Up {
		t.Fatalf("output = %+v, want released Z", out)
	}
	if d, ok := l.NextDeadline(); !ok || d != c.Now()+clock.Time(workerPollInterval) {
		t.Fatalf("restart deadline = %v, %v", d, ok)
	}
	c.Advance(time.Second)
	deadline := time.Now().Add(time.Second)
	for l.Current() == nil && time.Now().Before(deadline) {
		if err := l.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if l.Current() == nil || l.Current() == h || l.State() != StateRunning || builds != 2 {
		t.Fatalf("restart current/state/builds = %v/%v/%d", l.Current(), l.State(), builds)
	}
	next := l.Current()
	if _, err := l.Dispatch(Event{Control: "east", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	if out := next.Output(); len(out) != 1 || out[0].Spec != "X" || out[0].Up {
		t.Fatalf("restarted runtime output = %+v, want X down", out)
	}
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	if out := next.Output(); len(out) != 2 || out[1].Spec != "X" || !out[1].Up {
		t.Fatalf("restarted runtime after Dispose = %+v, want X release", out)
	}
}

func TestPromiseRejectionReportsSourceAndIgnoresHandled(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	p, err := goja.Compile("promise.js", `
__padmate.on("south", "press", async () => {
  await __padmate.sleep(1);
  throw new Error("async boom");
});
Promise.reject(new Error("caught")).catch(() => {});
`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if err := h.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	c.Advance(time.Millisecond)
	err = h.RunDue()
	if err == nil || !strings.Contains(err.Error(), "async boom") || strings.Contains(err.Error(), "caught") {
		t.Fatalf("RunDue error = %v", err)
	}
	ds := DiagnosticsOf(err)
	if len(ds) != 1 || ds[0].File != "promise.js" || ds[0].Line != 4 {
		t.Fatalf("diagnostics = %+v", ds)
	}
}

func TestPromiseRejectionDiagnosticStaysUnderWatchdog(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{"toString", `{ toString() { while (true) {} } }`},
		{"stack getter", `{ toString() { return "custom"; }, get stack() { while (true) {} } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(clock.NewFake())
			h.EnableWatchdog()
			source := `__padmate.on("south", "press", () => {
  __padmate.key.down("Z");
  Promise.reject(` + tc.reason + `);
});`
			p, err := goja.Compile("reason.js", source, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Activate(p); err != nil {
				t.Fatal(err)
			}
			escapeStuckRuntime(t, h)
			requireHandlerCPUBudget(t, h.Dispatch(Event{Control: "south", Gesture: "press"}))
			if err := h.Dispose(); err != nil {
				t.Fatal(err)
			}
			if out := h.Output(); len(out) != 2 || out[0].Up || !out[1].Up {
				t.Fatalf("output = %+v, want down then release", out)
			}
		})
	}
}

func TestPromiseRejectionDiagnosticDoesNotReenter(t *testing.T) {
	h := NewHost(clock.NewFake())
	p, err := goja.Compile("nested.js", `__padmate.on("south", "press", () => {
  Promise.reject({ toString() { Promise.reject("nested"); return "outer"; } });
});`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	err = h.Dispatch(Event{Control: "south", Gesture: "press"})
	if err == nil || !strings.Contains(err.Error(), "outer") || !strings.Contains(err.Error(), "while formatting") {
		t.Fatalf("Dispatch error = %v, want outer and bounded nested rejection", err)
	}
}

func TestPromiseRejectionKeepsTypeScriptSourceMap(t *testing.T) {
	root := t.TempDir()
	source := "import { defineScript, on, sleep } from \"padmate\";\n" +
		"export default defineScript({ setup() {\n" +
		"  on(\"south\", \"press\", async () => {\n" +
		"    await sleep(1);\n" +
		"    throw new Error(\"mapped rejection\");\n" +
		"  });\n" +
		"} });\n"
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goja.Compile("bundle.js", string(bundle.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	c := clock.NewFake()
	h := NewHost(c)
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if err := h.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Millisecond)
	err = h.RunDue()
	ds := DiagnosticsOf(err)
	if len(ds) != 1 || ds[0].File != "main.ts" || ds[0].Line != 5 {
		t.Fatalf("diagnostics = %+v; want main.ts:5", ds)
	}
}

func TestDisposeClearsTimersRegisteredByTeardown(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	p, err := goja.Compile("teardown.js", `globalThis.fired = 0;
globalThis.__padmate_bundle = { default: { teardown() {
  __padmate.every(1, () => { globalThis.fired++; });
} } };`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if err := h.Dispose(); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.Loop.NextDeadline(); ok {
		t.Fatalf("disposed host has timer deadline %v", d)
	}
	c.Advance(time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := h.VM.Get("fired").ToInteger(); got != 0 {
		t.Fatalf("disposed timer fired %d times", got)
	}
}

func TestImmediatePromiseRejectionKeepsLifecyclePhase(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		phase        Phase
		onActivate   bool
	}{
		{"setup", `globalThis.__padmate_bundle = { default: { setup() { Promise.reject(new Error("setup rejection")); } } };`, PhaseSetup, false},
		{"onActivate", `globalThis.__padmate_bundle = { default: { onActivate() { Promise.reject(new Error("activate rejection")); } } };`, PhaseActivate, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(clock.NewFake())
			p, err := goja.Compile(tc.name+".js", tc.source, false)
			if err != nil {
				t.Fatal(err)
			}
			err = h.Activate(p)
			if tc.onActivate && err == nil {
				err = h.OnActivate()
			}
			ds := DiagnosticsOf(err)
			if len(ds) != 1 || ds[0].Phase != tc.phase || ds[0].Line != 1 {
				t.Fatalf("diagnostics = %+v, want %s at source line 1", ds, tc.phase)
			}
		})
	}
}

func TestRunDueReturnsTimerErrorBeforeNextTimer(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	p, err := goja.Compile("timers.js", `globalThis.secondRan = false;
__padmate.every(1, () => { throw new Error("timer failed"); });
__padmate.every(1, () => { globalThis.secondRan = true; });`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Millisecond)
	err = h.RunDue()
	if err == nil || !strings.Contains(err.Error(), "timer failed") {
		t.Fatalf("RunDue error = %v", err)
	}
	if h.VM.Get("secondRan").ToBoolean() {
		t.Fatal("second timer ran after first timer failed")
	}
}
